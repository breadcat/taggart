package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var (
	db     *sql.DB
	tmpl   *template.Template
	config Config
)

type File struct {
	ID              int
	Filename        string
	EscapedFilename string
	Path            string
	Description     string
	Tags            map[string][]string
}

type Config struct {
	DatabasePath string `json:"database_path"`
	UploadDir    string `json:"upload_dir"`
	ServerPort   string `json:"server_port"`
	InstanceName string `json:"instance_name"`
	GallerySize  string `json:"gallery_size"`
	ItemsPerPage string `json:"items_per_page"`
}

type TagDisplay struct {
	Value string
	Count int
}

type PageData struct {
	Title      string
	Data       interface{}
	Query      string
	IP         string
	Port       string
	Files      []File
	Tags       map[string][]TagDisplay
	Pagination *Pagination
}

type Pagination struct {
	CurrentPage int
	TotalPages  int
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	PerPage     int
}

func getOrCreateCategoryAndTag(category, value string) (int, int, error) {
	category = strings.TrimSpace(category)
	value = strings.TrimSpace(value)
	var catID int
	err := db.QueryRow("SELECT id FROM categories WHERE name=?", category).Scan(&catID)
	if err == sql.ErrNoRows {
		res, err := db.Exec("INSERT INTO categories(name) VALUES(?)", category)
		if err != nil {
			return 0, 0, err
		}
		cid, _ := res.LastInsertId()
		catID = int(cid)
	} else if err != nil {
		return 0, 0, err
	}

	var tagID int
	if value != "" {
		err = db.QueryRow("SELECT id FROM tags WHERE category_id=? AND value=?", catID, value).Scan(&tagID)
		if err == sql.ErrNoRows {
			res, err := db.Exec("INSERT INTO tags(category_id, value) VALUES(?, ?)", catID, value)
			if err != nil {
				return 0, 0, err
			}
			tid, _ := res.LastInsertId()
			tagID = int(tid)
		} else if err != nil {
			return 0, 0, err
		}
	}

	return catID, tagID, nil
}

func queryFilesWithTags(query string, args ...interface{}) ([]File, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.Filename, &f.Path, &f.Description); err != nil {
			return nil, err
		}
		f.EscapedFilename = url.PathEscape(f.Filename)
		files = append(files, f)
	}
	return files, nil
}

func getTaggedFiles() ([]File, error) {
	return queryFilesWithTags(`
		SELECT DISTINCT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
		ORDER BY f.id DESC
	`)
}

func getTaggedFilesPaginated(page, perPage int) ([]File, int, error) {
	// Get total count
	var total int
	err := db.QueryRow(`SELECT COUNT(DISTINCT f.id) FROM files f JOIN file_tags ft ON ft.file_id = f.id`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * perPage
	files, err := queryFilesWithTags(`
		SELECT DISTINCT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
		ORDER BY f.id DESC
		LIMIT ? OFFSET ?
	`, perPage, offset)

	return files, total, err
}

func getUntaggedFiles() ([]File, error) {
	return queryFilesWithTags(`
		SELECT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		LEFT JOIN file_tags ft ON ft.file_id = f.id
		WHERE ft.file_id IS NULL
		ORDER BY f.id DESC
	`)
}

func getUntaggedFilesPaginated(page, perPage int) ([]File, int, error) {
	// Get total count
	var total int
	err := db.QueryRow(`SELECT COUNT(*) FROM files f LEFT JOIN file_tags ft ON ft.file_id = f.id WHERE ft.file_id IS NULL`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * perPage
	files, err := queryFilesWithTags(`
		SELECT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		LEFT JOIN file_tags ft ON ft.file_id = f.id
		WHERE ft.file_id IS NULL
		ORDER BY f.id DESC
		LIMIT ? OFFSET ?
	`, perPage, offset)

	return files, total, err
}

func buildPageData(title string, data interface{}) PageData {
	tagMap, _ := getTagData()
	return PageData{Title: title, Data: data, Tags: tagMap}
}

func buildPageDataWithPagination(title string, data interface{}, page, total, perPage int) PageData {
	pd := buildPageData(title, data)
	pd.Pagination = calculatePagination(page, total, perPage)
	return pd
}

func calculatePagination(page, total, perPage int) *Pagination {
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	return &Pagination{
		CurrentPage: page,
		TotalPages:  totalPages,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
		PerPage:     perPage,
	}
}

func buildPageDataWithIP(title string, data interface{}) PageData {
	pageData := buildPageData(title, data)
	ip, _ := getLocalIP()
	pageData.IP = ip
	pageData.Port = strings.TrimPrefix(config.ServerPort, ":")
	return pageData
}

func renderError(w http.ResponseWriter, message string, statusCode int) {
	http.Error(w, message, statusCode)
}

func renderTemplate(w http.ResponseWriter, tmplName string, data PageData) {
	if err := tmpl.ExecuteTemplate(w, tmplName, data); err != nil {
		renderError(w, "Template rendering failed", http.StatusInternalServerError)
	}
}

func getTagData() (map[string][]TagDisplay, error) {
	rows, err := db.Query(`
		SELECT c.name, t.value, COUNT(ft.file_id)
		FROM tags t
		JOIN categories c ON c.id = t.category_id
		LEFT JOIN file_tags ft ON ft.tag_id = t.id
		GROUP BY t.id
		HAVING COUNT(ft.file_id) > 0
		ORDER BY c.name, t.value`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tagMap := make(map[string][]TagDisplay)
	for rows.Next() {
		var cat, val string
		var count int
		rows.Scan(&cat, &val, &count)
		tagMap[cat] = append(tagMap[cat], TagDisplay{Value: val, Count: count})
	}
	return tagMap, nil
}

func main() {
	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	var err error
	db, err = sql.Open("sqlite3", config.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		filename TEXT,
		path TEXT,
		description TEXT DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE
	);
	CREATE TABLE IF NOT EXISTS tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		category_id INTEGER,
		value TEXT,
		UNIQUE(category_id, value)
	);
	CREATE TABLE IF NOT EXISTS file_tags (
		file_id INTEGER,
		tag_id INTEGER,
		UNIQUE(file_id, tag_id)
	);
	`)
	if err != nil {
		log.Fatal(err)
	}

	os.MkdirAll(config.UploadDir, 0755)
	os.MkdirAll("static", 0755)

	tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"hasAnySuffix": func(s string, suffixes ...string) bool {
			for _, suf := range suffixes {
				if strings.HasSuffix(strings.ToLower(s), suf) {
					return true
				}
			}
			return false
		},
	}).ParseGlob("templates/*.html"))

	http.HandleFunc("/", listFilesHandler)
	http.HandleFunc("/add", uploadHandler)
	http.HandleFunc("/add-yt", ytdlpHandler)
	http.HandleFunc("/upload-url", uploadFromURLHandler)
	http.HandleFunc("/file/", fileRouter)
	http.HandleFunc("/tags", tagsHandler)
	http.HandleFunc("/tag/", tagFilterHandler)
	http.HandleFunc("/untagged", untaggedFilesHandler)
	http.HandleFunc("/search", searchHandler)
	http.HandleFunc("/bulk-tag", bulkTagHandler)
	http.HandleFunc("/settings", settingsHandler)
	http.HandleFunc("/orphans", orphansHandler)

	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(config.UploadDir))))
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	log.Printf("Server started at http://localhost%s", config.ServerPort)
	log.Printf("Database: %s", config.DatabasePath)
	log.Printf("Upload directory: %s", config.UploadDir)
	http.ListenAndServe(config.ServerPort, nil)
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var files []File
	var searchTitle string

	if query != "" {
		sqlPattern := "%" + strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(query), "*", "%"), "?", "_") + "%"

		rows, err := db.Query(`
			SELECT f.id, f.filename, f.path, COALESCE(f.description, '') AS description,
			       c.name AS category, t.value AS tag
			FROM files f
			LEFT JOIN file_tags ft ON ft.file_id = f.id
			LEFT JOIN tags t ON t.id = ft.tag_id
			LEFT JOIN categories c ON c.id = t.category_id
			WHERE LOWER(f.filename) LIKE ? OR LOWER(f.description) LIKE ? OR LOWER(t.value) LIKE ?
			ORDER BY f.filename
		`, sqlPattern, sqlPattern, sqlPattern)
		if err != nil {
			renderError(w, "Search failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		fileMap := make(map[int]*File)
		for rows.Next() {
			var id int
			var filename, path, description, category, tag sql.NullString

			if err := rows.Scan(&id, &filename, &path, &description, &category, &tag); err != nil {
				renderError(w, "Failed to read search results: "+err.Error(), http.StatusInternalServerError)
				return
			}

			f, exists := fileMap[id]
			if !exists {
				f = &File{
					ID:              id,
					Filename:        filename.String,
					Path:            path.String,
					EscapedFilename: url.PathEscape(filename.String),
					Description:     description.String,
					Tags:            make(map[string][]string),
				}
				fileMap[id] = f
			}

			if category.Valid && tag.Valid && tag.String != "" {
				f.Tags[category.String] = append(f.Tags[category.String], tag.String)
			}
		}

		for _, f := range fileMap {
			files = append(files, *f)
		}

		searchTitle = fmt.Sprintf("Search Results for: %s", query)
	} else {
		searchTitle = "Search Files"
	}

	pageData := buildPageData(searchTitle, files)
	pageData.Query = query
	pageData.Files = files
	renderTemplate(w, "search.html", pageData)
}

func processUpload(src io.Reader, filename string) (int64, string, error) {
    finalFilename, finalPath, err := checkFileConflictStrict(filename)
    if err != nil {
        return 0, "", err
    }

    tempPath := finalPath + ".tmp"
    tempFile, err := os.Create(tempPath)
    if err != nil {
        return 0, "", fmt.Errorf("failed to create temp file: %v", err)
    }

    _, err = io.Copy(tempFile, src)
    tempFile.Close()
    if err != nil {
        os.Remove(tempPath)
        return 0, "", fmt.Errorf("failed to copy file data: %v", err)
    }

    ext := strings.ToLower(filepath.Ext(filename))
    videoExts := map[string]bool{
        ".mp4": true, ".mov": true, ".avi": true,
        ".mkv": true, ".webm": true, ".m4v": true,
    }

    var processedPath string
    var warningMsg string

    if videoExts[ext] {
        processedPath, warningMsg, err = processVideoFile(tempPath, finalPath)
        if err != nil {
            os.Remove(tempPath)
            return 0, "", err
        }
    } else {
        // Non-video → just rename temp file to final
        if err := os.Rename(tempPath, finalPath); err != nil {
            return 0, "", fmt.Errorf("failed to move file: %v", err)
        }
        processedPath = finalPath
    }

    id, err := saveFileToDatabase(finalFilename, processedPath)
    if err != nil {
        os.Remove(processedPath)
        return 0, "", err
    }

    return id, warningMsg, nil
}


func uploadFromURLHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/upload", http.StatusSeeOther)
		return
	}

	fileURL := r.FormValue("fileurl")
	if fileURL == "" {
		renderError(w, "No URL provided", http.StatusBadRequest)
		return
	}

	customFilename := strings.TrimSpace(r.FormValue("filename"))

	parsedURL, err := url.ParseRequestURI(fileURL)
	if err != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		renderError(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	resp, err := http.Get(fileURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		renderError(w, "Failed to download file", http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	var filename string
	urlExt := filepath.Ext(parsedURL.Path)
	if customFilename != "" {
		filename = customFilename
		if filepath.Ext(filename) == "" && urlExt != "" {
			filename += urlExt
		}
	} else {
		parts := strings.Split(parsedURL.Path, "/")
		filename = parts[len(parts)-1]
		if filename == "" {
			filename = "file_from_url"
		}
	}

	id, warningMsg, err := processUpload(resp.Body, filename)
	if err != nil {
		renderError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithWarning(w, r, fmt.Sprintf("/file/%d", id), warningMsg)
}

func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	// Get page number from query params
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get per page from config
	perPage := 50
	if config.ItemsPerPage != "" {
		if pp, err := strconv.Atoi(config.ItemsPerPage); err == nil && pp > 0 {
			perPage = pp
		}
	}

	tagged, taggedTotal, _ := getTaggedFilesPaginated(page, perPage)
	untagged, untaggedTotal, _ := getUntaggedFilesPaginated(page, perPage)

	// Use the larger total for pagination
	total := taggedTotal
	if untaggedTotal > total {
		total = untaggedTotal
	}

	pageData := buildPageDataWithPagination("Home", struct {
		Tagged   []File
		Untagged []File
	}{tagged, untagged}, page, total, perPage)

	renderTemplate(w, "list.html", pageData)
}

func untaggedFilesHandler(w http.ResponseWriter, r *http.Request) {
	// Get page number from query params
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get per page from config
	perPage := 50
	if config.ItemsPerPage != "" {
		if pp, err := strconv.Atoi(config.ItemsPerPage); err == nil && pp > 0 {
			perPage = pp
		}
	}

	files, total, _ := getUntaggedFilesPaginated(page, perPage)
	pageData := buildPageDataWithPagination("Untagged Files", files, page, total, perPage)
	renderTemplate(w, "untagged.html", pageData)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		pageData := buildPageData("Add File", nil)
		renderTemplate(w, "add.html", pageData)
		return
	}

	// Parse the multipart form (with max memory limit, e.g., 32MB)
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		renderError(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		renderError(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	var warnings []string

	// Process each file
	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			renderError(w, "Failed to open uploaded file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		_, warningMsg, err := processUpload(file, fileHeader.Filename)
		if err != nil {
			renderError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if warningMsg != "" {
			warnings = append(warnings, warningMsg)
		}
	}

	var warningMsg string
	if len(warnings) > 0 {
		warningMsg = strings.Join(warnings, "; ")
	}

	redirectWithWarning(w, r, "/untagged", warningMsg)
}

func redirectWithWarning(w http.ResponseWriter, r *http.Request, baseURL, warningMsg string) {
	redirectURL := baseURL
	if warningMsg != "" {
		redirectURL += "?warning=" + url.QueryEscape(warningMsg)
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func checkFileConflictStrict(filename string) (string, string, error) {
	finalPath := filepath.Join(config.UploadDir, filename)
	if _, err := os.Stat(finalPath); err == nil {
		return "", "", fmt.Errorf("a file with that name already exists")
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("failed to check for existing file: %v", err)
	}
	return filename, finalPath, nil
}

func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no connected network interface found")
}

func fileRouter(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")

	if len(parts) >= 4 && parts[3] == "delete" {
		fileDeleteHandler(w, r, parts)
		return
	}

	if len(parts) >= 4 && parts[3] == "rename" {
		fileRenameHandler(w, r, parts)
		return
	}

	if len(parts) >= 7 && parts[3] == "tag" {
		tagActionHandler(w, r, parts)
		return
	}

	fileHandler(w, r)
}

func fileDeleteHandler(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/file/"+parts[2], http.StatusSeeOther)
		return
	}

	fileID := parts[2]

	var currentFile File
	err := db.QueryRow("SELECT id, filename, path FROM files WHERE id=?", fileID).Scan(&currentFile.ID, &currentFile.Filename, &currentFile.Path)
	if err != nil {
		renderError(w, "File not found", http.StatusNotFound)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		renderError(w, "Failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if _, err = tx.Exec("DELETE FROM file_tags WHERE file_id=?", fileID); err != nil {
		renderError(w, "Failed to delete file tags", http.StatusInternalServerError)
		return
	}

	if _, err = tx.Exec("DELETE FROM files WHERE id=?", fileID); err != nil {
		renderError(w, "Failed to delete file record", http.StatusInternalServerError)
		return
	}

	if err = tx.Commit(); err != nil {
		renderError(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	if err = os.Remove(currentFile.Path); err != nil {
		log.Printf("Warning: Failed to delete physical file %s: %v", currentFile.Path, err)
	}

	// Delete thumbnail if it exists
	thumbPath := filepath.Join(config.UploadDir, "thumbnails", currentFile.Filename+".jpg")
	if _, err := os.Stat(thumbPath); err == nil {
		if err := os.Remove(thumbPath); err != nil {
			log.Printf("Warning: Failed to delete thumbnail %s: %v", thumbPath, err)
		}
	}

	http.Redirect(w, r, "/?deleted="+currentFile.Filename, http.StatusSeeOther)
}

func fileRenameHandler(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/file/"+parts[2], http.StatusSeeOther)
		return
	}

	fileID := parts[2]
	newFilename := sanitizeFilename(strings.TrimSpace(r.FormValue("newfilename")))

	if newFilename == "" {
		renderError(w, "New filename cannot be empty", http.StatusBadRequest)
		return
	}

	var currentFilename, currentPath string
	err := db.QueryRow("SELECT filename, path FROM files WHERE id=?", fileID).Scan(&currentFilename, &currentPath)
	if err != nil {
		renderError(w, "File not found", http.StatusNotFound)
		return
	}

	if currentFilename == newFilename {
		http.Redirect(w, r, "/file/"+fileID, http.StatusSeeOther)
		return
	}

	newPath := filepath.Join(config.UploadDir, newFilename)
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		renderError(w, "A file with that name already exists", http.StatusConflict)
		return
	}

	if err := os.Rename(currentPath, newPath); err != nil {
		renderError(w, "Failed to rename physical file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	thumbOld := filepath.Join(config.UploadDir, "thumbnails", currentFilename+".jpg")
	thumbNew := filepath.Join(config.UploadDir, "thumbnails", newFilename+".jpg")

	if _, err := os.Stat(thumbOld); err == nil {
		if err := os.Rename(thumbOld, thumbNew); err != nil {
			os.Rename(newPath, currentPath)
			renderError(w, "Failed to rename thumbnail: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	_, err = db.Exec("UPDATE files SET filename=?, path=? WHERE id=?", newFilename, newPath, fileID)
	if err != nil {
		os.Rename(newPath, currentPath)
		if _, err := os.Stat(thumbNew); err == nil {
			os.Rename(thumbNew, thumbOld)
		}
		renderError(w, "Failed to update database", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/file/"+fileID, http.StatusSeeOther)
}

func fileHandler(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/file/")
	if strings.Contains(idStr, "/") {
		idStr = strings.SplitN(idStr, "/", 2)[0]
	}

	var f File
	err := db.QueryRow("SELECT id, filename, path, COALESCE(description, '') as description FROM files WHERE id=?", idStr).Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
	if err != nil {
		renderError(w, "File not found", http.StatusNotFound)
		return
	}

	f.Tags = make(map[string][]string)
	rows, _ := db.Query(`
		SELECT c.name, t.value
		FROM tags t
		JOIN categories c ON c.id = t.category_id
		JOIN file_tags ft ON ft.tag_id = t.id
		WHERE ft.file_id=?`, f.ID)
	for rows.Next() {
		var cat, val string
		rows.Scan(&cat, &val)
		f.Tags[cat] = append(f.Tags[cat], val)
	}
	rows.Close()

	if r.Method == http.MethodPost {
		if r.FormValue("action") == "update_description" {
			description := r.FormValue("description")
			if len(description) > 2048 {
				description = description[:2048]
			}

			if _, err := db.Exec("UPDATE files SET description = ? WHERE id = ?", description, f.ID); err != nil {
				renderError(w, "Failed to update description", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/file/"+idStr, http.StatusSeeOther)
			return
		}
		cat := strings.TrimSpace(r.FormValue("category"))
		val := strings.TrimSpace(r.FormValue("value"))
		if cat != "" && val != "" {
			_, tagID, _ := getOrCreateCategoryAndTag(cat, val)
			db.Exec("INSERT OR IGNORE INTO file_tags(file_id, tag_id) VALUES (?, ?)", f.ID, tagID)
		}
		http.Redirect(w, r, "/file/"+idStr, http.StatusSeeOther)
		return
	}

	catRows, _ := db.Query(`
		SELECT DISTINCT c.name
		FROM categories c
		JOIN tags t ON t.category_id = c.id
		JOIN file_tags ft ON ft.tag_id = t.id
		ORDER BY c.name
	`)
	var cats []string
	for catRows.Next() {
		var c string
		catRows.Scan(&c)
		cats = append(cats, c)
	}
	catRows.Close()

	pageData := buildPageDataWithIP(f.Filename, struct {
		File            File
		Categories      []string
		EscapedFilename string
	}{f, cats, url.PathEscape(f.Filename)})

	renderTemplate(w, "file.html", pageData)
}

func tagActionHandler(w http.ResponseWriter, r *http.Request, parts []string) {
	fileID := parts[2]
	cat := parts[4]
	val := parts[5]
	action := parts[6]

	if action == "delete" && r.Method == http.MethodPost {
		var tagID int
		db.QueryRow(`
			SELECT t.id
			FROM tags t
			JOIN categories c ON c.id=t.category_id
			WHERE c.name=? AND t.value=?`, cat, val).Scan(&tagID)
		if tagID != 0 {
			db.Exec("DELETE FROM file_tags WHERE file_id=? AND tag_id=?", fileID, tagID)
		}
	}
	http.Redirect(w, r, "/file/"+fileID, http.StatusSeeOther)
}

func tagsHandler(w http.ResponseWriter, r *http.Request) {
	pageData := buildPageData("All Tags", nil)
	pageData.Data = pageData.Tags
	renderTemplate(w, "tags.html", pageData)
}

func tagFilterHandler(w http.ResponseWriter, r *http.Request) {
	// Get page number from query params
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get per page from config
	perPage := 50
	if config.ItemsPerPage != "" {
		if pp, err := strconv.Atoi(config.ItemsPerPage); err == nil && pp > 0 {
			perPage = pp
		}
	}

	// Split by /and/tag/ to get individual tag pairs
	fullPath := strings.TrimPrefix(r.URL.Path, "/tag/")
	tagPairs := strings.Split(fullPath, "/and/tag/")

	type filter struct {
		Category string
		Value    string
	}

	var filters []filter
	for _, pair := range tagPairs {
		parts := strings.Split(pair, "/")
		if len(parts) != 2 {
			renderError(w, "Invalid tag filter path", http.StatusBadRequest)
			return
		}
		filters = append(filters, filter{parts[0], parts[1]})
	}

	// Build count query first
	countQuery := `SELECT COUNT(DISTINCT f.id) FROM files f WHERE 1=1`
	countArgs := []interface{}{}
	for _, f := range filters {
		if f.Value == "unassigned" {
			countQuery += `
				AND NOT EXISTS (
					SELECT 1
					FROM file_tags ft
					JOIN tags t ON ft.tag_id = t.id
					JOIN categories c ON c.id = t.category_id
					WHERE ft.file_id = f.id AND c.name = ?
				)`
			countArgs = append(countArgs, f.Category)
		} else {
			countQuery += `
				AND EXISTS (
					SELECT 1
					FROM file_tags ft
					JOIN tags t ON ft.tag_id = t.id
					JOIN categories c ON c.id = t.category_id
					WHERE ft.file_id = f.id AND c.name = ? AND t.value = ?
				)`
			countArgs = append(countArgs, f.Category, f.Value)
		}
	}

	// Get total count
	var total int
	err := db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		renderError(w, "Failed to count files", http.StatusInternalServerError)
		return
	}

	// Build main query with pagination
	query := `SELECT f.id, f.filename, f.path, COALESCE(f.description, '') as description FROM files f WHERE 1=1`
	args := []interface{}{}
	for _, f := range filters {
		if f.Value == "unassigned" {
			query += `
				AND NOT EXISTS (
					SELECT 1
					FROM file_tags ft
					JOIN tags t ON ft.tag_id = t.id
					JOIN categories c ON c.id = t.category_id
					WHERE ft.file_id = f.id AND c.name = ?
				)`
			args = append(args, f.Category)
		} else {
			query += `
				AND EXISTS (
					SELECT 1
					FROM file_tags ft
					JOIN tags t ON ft.tag_id = t.id
					JOIN categories c ON c.id = t.category_id
					WHERE ft.file_id = f.id AND c.name = ? AND t.value = ?
				)`
			args = append(args, f.Category, f.Value)
		}
	}

	// Add pagination
	offset := (page - 1) * perPage
	query += ` ORDER BY f.id DESC LIMIT ? OFFSET ?`
	args = append(args, perPage, offset)

	files, err := queryFilesWithTags(query, args...)
	if err != nil {
		renderError(w, "Failed to fetch files", http.StatusInternalServerError)
		return
	}

	var titleParts []string
	for _, f := range filters {
		titleParts = append(titleParts, fmt.Sprintf("%s: %s", f.Category, f.Value))
	}
	title := "Tagged: " + strings.Join(titleParts, ", ")

	pageData := buildPageDataWithPagination(title, struct {
		Tagged   []File
		Untagged []File
	}{files, nil}, page, total, perPage)

	renderTemplate(w, "list.html", pageData)
}

func loadConfig() error {
	config = Config{
		DatabasePath: "./database.db",
		UploadDir:    "uploads",
		ServerPort:   ":8080",
		InstanceName: "Taggart",
		GallerySize:  "400px",
		ItemsPerPage: "100",
	}

	if data, err := ioutil.ReadFile("config.json"); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return err
		}
	}

	return os.MkdirAll(config.UploadDir, 0755)
}

func saveConfig() error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile("config.json", data, 0644)
}

func validateConfig(newConfig Config) error {
	if newConfig.DatabasePath == "" {
		return fmt.Errorf("database path cannot be empty")
	}

	if newConfig.UploadDir == "" {
		return fmt.Errorf("upload directory cannot be empty")
	}

	if newConfig.ServerPort == "" || !strings.HasPrefix(newConfig.ServerPort, ":") {
		return fmt.Errorf("server port must be in format ':8080'")
	}

	if err := os.MkdirAll(newConfig.UploadDir, 0755); err != nil {
		return fmt.Errorf("cannot create upload directory: %v", err)
	}

	return nil
}

func settingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		newConfig := Config{
			DatabasePath: strings.TrimSpace(r.FormValue("database_path")),
			UploadDir:    strings.TrimSpace(r.FormValue("upload_dir")),
			ServerPort:   strings.TrimSpace(r.FormValue("server_port")),
			InstanceName: strings.TrimSpace(r.FormValue("instance_name")),
			GallerySize:  strings.TrimSpace(r.FormValue("gallery_size")),
			ItemsPerPage: strings.TrimSpace(r.FormValue("items_per_page")),
		}

		if err := validateConfig(newConfig); err != nil {
			pageData := buildPageData("Settings", struct {
				Config Config
				Error  string
			}{config, err.Error()})
			renderTemplate(w, "settings.html", pageData)
			return
		}

		needsRestart := (newConfig.DatabasePath != config.DatabasePath ||
			newConfig.ServerPort != config.ServerPort)

		config = newConfig
		if err := saveConfig(); err != nil {
			pageData := buildPageData("Settings", struct {
				Config Config
				Error  string
			}{config, "Failed to save configuration: " + err.Error()})
			renderTemplate(w, "settings.html", pageData)
			return
		}

		var message string
		if needsRestart {
			message = "Settings saved successfully! Please restart the server for database/port changes to take effect."
		} else {
			message = "Settings saved successfully!"
		}

		pageData := buildPageData("Settings", struct {
			Config  Config
			Error   string
			Success string
		}{config, "", message})
		renderTemplate(w, "settings.html", pageData)
		return
	}

	pageData := buildPageData("Settings", struct {
		Config  Config
		Error   string
		Success string
	}{config, "", ""})
	renderTemplate(w, "settings.html", pageData)
}

func ytdlpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/upload", http.StatusSeeOther)
		return
	}

	videoURL := r.FormValue("url")
	if videoURL == "" {
		renderError(w, "No URL provided", http.StatusBadRequest)
		return
	}

	outTemplate := filepath.Join(config.UploadDir, "%(title)s.%(ext)s")
	filenameCmd := exec.Command("yt-dlp", "--playlist-items", "1", "-f", "mp4", "-o", outTemplate, "--get-filename", videoURL)
	filenameBytes, err := filenameCmd.Output()
	if err != nil {
		renderError(w, fmt.Sprintf("Failed to get filename: %v", err), http.StatusInternalServerError)
		return
	}
	expectedFullPath := strings.TrimSpace(string(filenameBytes))
	expectedFilename := filepath.Base(expectedFullPath)

	finalFilename, finalPath, err := checkFileConflictStrict(expectedFilename)
	if err != nil {
		renderError(w, err.Error(), http.StatusConflict)
		return
	}

	downloadCmd := exec.Command("yt-dlp", "--playlist-items", "1", "-f", "mp4", "-o", outTemplate, videoURL)
	downloadCmd.Stdout = os.Stdout
	downloadCmd.Stderr = os.Stderr
	if err := downloadCmd.Run(); err != nil {
		renderError(w, fmt.Sprintf("Failed to download video: %v", err), http.StatusInternalServerError)
		return
	}

	if expectedFullPath != finalPath {
		if err := os.Rename(expectedFullPath, finalPath); err != nil {
			renderError(w, fmt.Sprintf("Failed to move downloaded file: %v", err), http.StatusInternalServerError)
			return
		}
	}

	tempPath := finalPath + ".tmp"
	if err := os.Rename(finalPath, tempPath); err != nil {
		renderError(w, fmt.Sprintf("Failed to create temp file for processing: %v", err), http.StatusInternalServerError)
		return
	}

	processedPath, warningMsg, err := processVideoFile(tempPath, finalPath)
	if err != nil {
		os.Remove(tempPath)
		renderError(w, fmt.Sprintf("Failed to process video: %v", err), http.StatusInternalServerError)
		return
	}

	id, err := saveFileToDatabase(finalFilename, processedPath)
	if err != nil {
		os.Remove(processedPath)
		renderError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithWarning(w, r, fmt.Sprintf("/file/%d", id), warningMsg)
}

func parseFileIDRange(rangeStr string) ([]int, error) {
	var fileIDs []int
	parts := strings.Split(rangeStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range format: %s", part)
			}

			start, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid start ID in range %s: %v", part, err)
			}

			end, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid end ID in range %s: %v", part, err)
			}

			if start > end {
				return nil, fmt.Errorf("invalid range %s: start must be <= end", part)
			}

			for i := start; i <= end; i++ {
				fileIDs = append(fileIDs, i)
			}
		} else {
			id, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid file ID: %s", part)
			}
			fileIDs = append(fileIDs, id)
		}
	}

	uniqueIDs := make(map[int]bool)
	var result []int
	for _, id := range fileIDs {
		if !uniqueIDs[id] {
			uniqueIDs[id] = true
			result = append(result, id)
		}
	}

	return result, nil
}

func validateFileIDs(fileIDs []int) ([]File, error) {
	if len(fileIDs) == 0 {
		return nil, fmt.Errorf("no file IDs provided")
	}

	placeholders := make([]string, len(fileIDs))
	args := make([]interface{}, len(fileIDs))
	for i, id := range fileIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf("SELECT id, filename, path FROM files WHERE id IN (%s) ORDER BY id",
		strings.Join(placeholders, ","))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("database error: %v", err)
	}
	defer rows.Close()

	var files []File
	foundIDs := make(map[int]bool)

	for rows.Next() {
		var f File
		err := rows.Scan(&f.ID, &f.Filename, &f.Path)
		if err != nil {
			return nil, fmt.Errorf("error scanning file: %v", err)
		}
		files = append(files, f)
		foundIDs[f.ID] = true
	}

	var missingIDs []int
	for _, id := range fileIDs {
		if !foundIDs[id] {
			missingIDs = append(missingIDs, id)
		}
	}

	if len(missingIDs) > 0 {
		return files, fmt.Errorf("file IDs not found: %v", missingIDs)
	}

	return files, nil
}

func applyBulkTagOperations(fileIDs []int, category, value, operation string) error {
	category = strings.TrimSpace(category)
	value = strings.TrimSpace(value)
	if category == "" {
		return fmt.Errorf("category cannot be empty")
	}

	if operation == "add" && value == "" {
		return fmt.Errorf("value cannot be empty when adding tags")
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %v", err)
	}
	defer tx.Rollback()

	var catID int
	err = tx.QueryRow("SELECT id FROM categories WHERE name=?", category).Scan(&catID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to query category: %v", err)
	}

	if catID == 0 {
		if operation == "remove" {
			return fmt.Errorf("cannot remove non-existent category: %s", category)
		}
		res, err := tx.Exec("INSERT INTO categories(name) VALUES(?)", category)
		if err != nil {
			return fmt.Errorf("failed to create category: %v", err)
		}
		cid, _ := res.LastInsertId()
		catID = int(cid)
	}

	var tagID int
	if value != "" {
		err = tx.QueryRow("SELECT id FROM tags WHERE category_id=? AND value=?", catID, value).Scan(&tagID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to query tag: %v", err)
		}

		if tagID == 0 {
			if operation == "remove" {
				return fmt.Errorf("cannot remove non-existent tag: %s=%s", category, value)
			}
			res, err := tx.Exec("INSERT INTO tags(category_id, value) VALUES(?, ?)", catID, value)
			if err != nil {
				return fmt.Errorf("failed to create tag: %v", err)
			}
			tid, _ := res.LastInsertId()
			tagID = int(tid)
		}
	}

	for _, fileID := range fileIDs {
		if operation == "add" {
			_, err = tx.Exec("INSERT OR IGNORE INTO file_tags(file_id, tag_id) VALUES (?, ?)", fileID, tagID)
		} else if operation == "remove" {
			if value != "" {
				_, err = tx.Exec("DELETE FROM file_tags WHERE file_id=? AND tag_id=?", fileID, tagID)
			} else {
				_, err = tx.Exec(`DELETE FROM file_tags WHERE file_id=? AND tag_id IN (SELECT t.id FROM tags t WHERE t.category_id=?)`, fileID, catID)
			}
		} else {
			return fmt.Errorf("invalid operation: %s (must be 'add' or 'remove')", operation)
		}
		if err != nil {
			return fmt.Errorf("failed to %s tag for file %d: %v", operation, fileID, err)
		}
	}

	return tx.Commit()
}

type BulkTagFormData struct {
	Categories  []string
	RecentFiles []File
	Error       string
	Success     string
	FormData    struct {
		FileRange string
		Category  string
		Value     string
		Operation string
	}
}

func getBulkTagFormData() BulkTagFormData {
	catRows, _ := db.Query("SELECT name FROM categories ORDER BY name")
	var cats []string
	for catRows.Next() {
		var c string
		catRows.Scan(&c)
		cats = append(cats, c)
	}
	catRows.Close()

	recentRows, _ := db.Query("SELECT id, filename FROM files ORDER BY id DESC LIMIT 20")
	var recentFiles []File
	for recentRows.Next() {
		var f File
		recentRows.Scan(&f.ID, &f.Filename)
		recentFiles = append(recentFiles, f)
	}
	recentRows.Close()

	return BulkTagFormData{
		Categories:  cats,
		RecentFiles: recentFiles,
		FormData: struct {
			FileRange string
			Category  string
			Value     string
			Operation string
		}{Operation: "add"},
	}
}

func bulkTagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		formData := getBulkTagFormData()
		pageData := buildPageData("Bulk Tag Editor", formData)
		renderTemplate(w, "bulk-tag.html", pageData)
		return
	}

	if r.Method == http.MethodPost {
		rangeStr := strings.TrimSpace(r.FormValue("file_range"))
		category := strings.TrimSpace(r.FormValue("category"))
		value := strings.TrimSpace(r.FormValue("value"))
		operation := r.FormValue("operation")

		formData := getBulkTagFormData()
		formData.FormData.FileRange = rangeStr
		formData.FormData.Category = category
		formData.FormData.Value = value
		formData.FormData.Operation = operation

		createErrorResponse := func(errorMsg string) {
			formData.Error = errorMsg
			pageData := buildPageData("Bulk Tag Editor", formData)
			renderTemplate(w, "bulk-tag.html", pageData)
		}

		if rangeStr == "" {
			createErrorResponse("File range cannot be empty")
			return
		}

		if category == "" {
			createErrorResponse("Category cannot be empty")
			return
		}

		if operation == "add" && value == "" {
			createErrorResponse("Value cannot be empty when adding tags")
			return
		}

		fileIDs, err := parseFileIDRange(rangeStr)
		if err != nil {
			createErrorResponse(fmt.Sprintf("Invalid file range: %v", err))
			return
		}

		validFiles, err := validateFileIDs(fileIDs)
		if err != nil {
			createErrorResponse(fmt.Sprintf("File validation error: %v", err))
			return
		}

		err = applyBulkTagOperations(fileIDs, category, value, operation)
		if err != nil {
			createErrorResponse(fmt.Sprintf("Tag operation failed: %v", err))
			return
		}

		var successMsg string
		if operation == "add" {
			successMsg = fmt.Sprintf("Tag '%s: %s' added to %d files", category, value, len(validFiles))
		} else {
			if value != "" {
				successMsg = fmt.Sprintf("Tag '%s: %s' removed from %d files", category, value, len(validFiles))
			} else {
				successMsg = fmt.Sprintf("All '%s' category tags removed from %d files", category, len(validFiles))
			}
		}

		var filenames []string
		for _, f := range validFiles {
			filenames = append(filenames, f.Filename)
		}

		if len(filenames) <= 5 {
			successMsg += fmt.Sprintf(": %s", strings.Join(filenames, ", "))
		} else {
			successMsg += fmt.Sprintf(": %s and %d more", strings.Join(filenames[:5], ", "), len(filenames)-5)
		}

		formData.Success = successMsg
		pageData := buildPageData("Bulk Tag Editor", formData)
		renderTemplate(w, "bulk-tag.html", pageData)
		return
	}

	renderError(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func sanitizeFilename(filename string) string {
	if filename == "" {
		return "file"
	}
	filename = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(filename, "/", "_"), "\\", "_"), "..", "_")
	if filename == "" {
		return "file"
	}
	return filename
}

func detectVideoCodec(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "default=nokey=1:noprint_wrappers=1", filePath)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to probe video codec: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func reencodeHEVCToH264(inputPath, outputPath string) error {
	cmd := exec.Command("ffmpeg", "-i", inputPath,
		"-c:v", "libx264", "-profile:v", "baseline", "-preset", "fast", "-crf", "23",
		"-c:a", "aac", "-movflags", "+faststart", outputPath)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func processVideoFile(tempPath, finalPath string) (string, string, error) {
	codec, err := detectVideoCodec(tempPath)
	if err != nil {
		return "", "", err
	}

	if codec == "hevc" || codec == "h265" {
		warningMsg := "The video uses HEVC and has been re-encoded to H.264 for browser compatibility."
		if err := reencodeHEVCToH264(tempPath, finalPath); err != nil {
			return "", "", fmt.Errorf("failed to re-encode HEVC video: %v", err)
		}
		os.Remove(tempPath)
		return finalPath, warningMsg, nil
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", "", fmt.Errorf("failed to move file: %v", err)
	}

	ext := strings.ToLower(filepath.Ext(finalPath))
	if ext == ".mp4" || ext == ".mov" || ext == ".avi" || ext == ".mkv" || ext == ".webm" || ext == ".m4v" {
		if err := generateThumbnail(finalPath, config.UploadDir, filepath.Base(finalPath)); err != nil {
			log.Printf("Warning: could not generate thumbnail: %v", err)
		}
	}

	return finalPath, "", nil
}

func saveFileToDatabase(filename, path string) (int64, error) {
	res, err := db.Exec("INSERT INTO files (filename, path, description) VALUES (?, ?, '')", filename, path)
	if err != nil {
		return 0, fmt.Errorf("failed to save file to database: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get inserted ID: %v", err)
	}
	return id, nil
}

func getFilesOnDisk(uploadDir string) ([]string, error) {
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

func getFilesInDB() (map[string]bool, error) {
	rows, err := db.Query(`SELECT filename FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fileMap := make(map[string]bool)
	for rows.Next() {
		var name string
		rows.Scan(&name)
		fileMap[name] = true
	}
	return fileMap, nil
}

func getOrphanedFiles(uploadDir string) ([]string, error) {
	diskFiles, err := getFilesOnDisk(uploadDir)
	if err != nil {
		return nil, err
	}

	dbFiles, err := getFilesInDB()
	if err != nil {
		return nil, err
	}

	var orphans []string
	for _, f := range diskFiles {
		if !dbFiles[f] {
			orphans = append(orphans, f)
		}
	}
	return orphans, nil
}

func orphansHandler(w http.ResponseWriter, r *http.Request) {
	orphans, err := getOrphanedFiles(config.UploadDir)
	if err != nil {
		renderError(w, "Error reading orphaned files", http.StatusInternalServerError)
		return
	}

	pageData := buildPageData("Orphaned Files", orphans)
	renderTemplate(w, "orphans.html", pageData)
}

func generateThumbnail(videoPath, uploadDir, filename string) error {
	thumbDir := filepath.Join(uploadDir, "thumbnails")
	if err := os.MkdirAll(thumbDir, 0755); err != nil {
		return fmt.Errorf("failed to create thumbnails directory: %v", err)
	}

	thumbPath := filepath.Join(thumbDir, filename+".jpg")

	cmd := exec.Command("ffmpeg", "-y", "-ss", "00:00:05", "-i", videoPath, "-vframes", "1", "-vf", "scale=400:-1", thumbPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		cmd := exec.Command("ffmpeg", "-y", "-i", videoPath, "-vframes", "1", "-vf", "scale=400:-1", thumbPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err2 := cmd.Run(); err2 != nil {
			return fmt.Errorf("failed to generate thumbnail: %v", err2)
		}
	}

	return nil
}