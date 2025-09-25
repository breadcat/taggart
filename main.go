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
	db   *sql.DB
	tmpl *template.Template
	config Config
)

type File struct {
	ID       int
	Filename string
	EscapedFilename string
	Path     string
	Description	string
	Tags     map[string]string
}

type Config struct {
	DatabasePath  string `json:"database_path"`
	UploadDir     string `json:"upload_dir"`
	ServerPort    string `json:"server_port"`
}

type TagDisplay struct {
	Value string
	Count int
}

type PageData struct {
	Title string
	Data  interface{}
	Query string
	IP    string
	Port  string
	Files []File
}

// sanitizeFilename removes problematic characters from filenames
func sanitizeFilename(filename string) string {
	if filename == "" {
		return "file"
	}

	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, "..", "_")

	if filename == "" {
		return "file"
	}
	return filename
}

// detectVideoCodec uses ffprobe to detect the video codec
func detectVideoCodec(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "default=nokey=1:noprint_wrappers=1", filePath)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to probe video codec: %v", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// reencodeHEVCToH264 converts HEVC videos to H.264 for browser compatibility
func reencodeHEVCToH264(inputPath, outputPath string) error {
	cmd := exec.Command("ffmpeg", "-i", inputPath,
		"-c:v", "libx264", "-profile:v", "baseline", "-preset", "fast", "-crf", "23",
		"-c:a", "aac", "-movflags", "+faststart", outputPath)

	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	return cmd.Run()
}

// processVideoFile handles codec detection and re-encoding if needed
// Returns final path, warning message (if any), and error
func processVideoFile(tempPath, finalPath string) (string, string, error) {
	codec, err := detectVideoCodec(tempPath)
	if err != nil {
		return "", "", err
	}

	// If HEVC, re-encode to H.264
	if codec == "hevc" || codec == "h265" {
		warningMsg := "The video uses HEVC and has been re-encoded to H.264 for browser compatibility."

		if err := reencodeHEVCToH264(tempPath, finalPath); err != nil {
			return "", "", fmt.Errorf("failed to re-encode HEVC video: %v", err)
		}

		os.Remove(tempPath) // Clean up temp file
		return finalPath, warningMsg, nil
	}

	// If not HEVC, just move temp file to final destination
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", "", fmt.Errorf("failed to move file: %v", err)
	}

	return finalPath, "", nil
}

// saveFileToDatabase adds file record to database and returns the ID
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

// processUploadedFile handles the complete file processing workflow
func processUploadedFile(src io.Reader, originalFilename string) (int64, string, string, error) {
	// Strict duplicate check — error out if file already exists
	finalFilename, finalPath, err := checkFileConflictStrict(originalFilename)
	if err != nil {
		return 0, "", "", err
	}
	// Create temporary file
	tempPath := finalPath + ".tmp"
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return 0, "", "", fmt.Errorf("failed to create temp file: %v", err)
	}

	// Copy data to temp file
	_, err = io.Copy(tempFile, src)
	tempFile.Close()
	if err != nil {
		os.Remove(tempPath)
		return 0, "", "", fmt.Errorf("failed to copy file data: %v", err)
	}

	// Process video (codec detection and potential re-encoding)
	processedPath, warningMsg, err := processVideoFile(tempPath, finalPath)
	if err != nil {
		os.Remove(tempPath)
		return 0, "", "", err
	}

	// Save to database
	id, err := saveFileToDatabase(finalFilename, processedPath)
	if err != nil {
		os.Remove(processedPath)
		return 0, "", "", err
	}

	return id, finalFilename, warningMsg, nil
}

func main() {
	// Load configuration first
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

	// Use configured upload directory for file serving
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
        // Convert wildcards to SQL LIKE pattern
        sqlPattern := strings.ReplaceAll(query, "*", "%")
        sqlPattern = strings.ReplaceAll(sqlPattern, "?", "_")

        rows, err := db.Query(
            "SELECT id, filename, path, COALESCE(description, '') as description FROM files WHERE filename LIKE ? OR description LIKE ? ORDER BY filename",
            sqlPattern, sqlPattern,
        )
        if err != nil {
            http.Error(w, "Search failed", http.StatusInternalServerError)
            return
        }
        defer rows.Close()

        for rows.Next() {
            var f File
            if err := rows.Scan(&f.ID, &f.Filename, &f.Path, &f.Description); err != nil {
                http.Error(w, "Failed to read results", http.StatusInternalServerError)
                return
            }
            f.EscapedFilename = url.PathEscape(f.Filename)
            files = append(files, f)
        }

        searchTitle = fmt.Sprintf("Search Results for: %s", query)
    } else {
        searchTitle = "Search Files"
    }

    pageData := PageData{
        Title: searchTitle,
        Query: query,
        Files: files,
    }

    if err := tmpl.ExecuteTemplate(w, "search.html", pageData); err != nil {
        http.Error(w, "Template rendering failed", http.StatusInternalServerError)
    }
}

// Upload file from URL
func uploadFromURLHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/upload", http.StatusSeeOther)
		return
	}

	fileURL := r.FormValue("fileurl")
	if fileURL == "" {
		http.Error(w, "No URL provided", http.StatusBadRequest)
		return
	}

	customFilename := strings.TrimSpace(r.FormValue("filename"))

	parsedURL, err := url.ParseRequestURI(fileURL)
	if err != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	resp, err := http.Get(fileURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to download file", http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	// Determine filename
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

	// Process the uploaded file
	id, _, warningMsg, err := processUploadedFile(resp.Body, filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Redirect with optional warning
	redirectURL := fmt.Sprintf("/file/%d", id)
	if warningMsg != "" {
		redirectURL += "?warning=" + url.QueryEscape(warningMsg)
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// List all files, plus untagged files
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	// Tagged files
	rows, _ := db.Query(`
		SELECT DISTINCT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
		ORDER BY f.id DESC
	`)
	defer rows.Close()
	var tagged []File
	for rows.Next() {
		var f File
		rows.Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
		f.EscapedFilename = url.PathEscape(f.Filename)
		tagged = append(tagged, f)
	}

	// Untagged files
	untaggedRows, _ := db.Query(`
		SELECT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		LEFT JOIN file_tags ft ON ft.file_id = f.id
		WHERE ft.file_id IS NULL
		ORDER BY f.id DESC
	`)
	defer untaggedRows.Close()
	var untagged []File
	for untaggedRows.Next() {
		var f File
		untaggedRows.Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
		f.EscapedFilename = url.PathEscape(f.Filename)
		untagged = append(untagged, f)
	}

	pageData := PageData{
		Title: "Home",
		Data: struct {
			Tagged   []File
			Untagged []File
		}{tagged, untagged},
	}

	tmpl.ExecuteTemplate(w, "list.html", pageData)
}

// Show untagged files at /untagged
func untaggedFilesHandler(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query(`
		SELECT f.id, f.filename, f.path, COALESCE(f.description, '') as description
		FROM files f
		WHERE NOT EXISTS (
			SELECT 1
			FROM file_tags ft
			WHERE ft.file_id = f.id
		)
		ORDER BY f.id DESC
	`)
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		rows.Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
		f.EscapedFilename = url.PathEscape(f.Filename)
		files = append(files, f)
	}

	pageData := PageData{
		Title: "Untagged Files",
		Data:  files,
	}

	tmpl.ExecuteTemplate(w, "untagged.html", pageData)
}

// Add a file
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		pageData := PageData{
			Title: "Add File",
			Data:  nil,
		}
		tmpl.ExecuteTemplate(w, "add.html", pageData)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to read uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Process the uploaded file
	id, _, warningMsg, err := processUploadedFile(file, header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Redirect with optional warning
	redirectURL := fmt.Sprintf("/file/%d", id)
	if warningMsg != "" {
		redirectURL += "?warning=" + url.QueryEscape(warningMsg)
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// checkFileConflictStrict checks if a file already exists in the upload directory.
// It returns the final filename, full path, or an error if a duplicate exists.
func checkFileConflictStrict(filename string) (string, string, error) {
	finalPath := filepath.Join(config.UploadDir, filename)
	if _, err := os.Stat(finalPath); err == nil {
		return "", "", fmt.Errorf("a file with that name already exists")
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("failed to check for existing file: %v", err)
	}
	return filename, finalPath, nil
}

// raw local IP for raw address
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

// Router for file operations, tag deletion, rename, and delete
func fileRouter(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")

	// Handle delete: /file/{id}/delete
	if len(parts) >= 4 && parts[3] == "delete" {
		fileDeleteHandler(w, r, parts)
		return
	}

	// Handle rename: /file/{id}/rename
	if len(parts) >= 4 && parts[3] == "rename" {
		fileRenameHandler(w, r, parts)
		return
	}

	// Handle tag deletion: /file/{id}/tag/{category}/{value}/delete
	if len(parts) >= 7 && parts[3] == "tag" {
		tagActionHandler(w, r, parts)
		return
	}

	// Default file handler
	fileHandler(w, r)
}

// Handle file deletion
func fileDeleteHandler(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/file/"+parts[2], http.StatusSeeOther)
		return
	}

	fileID := parts[2]

	// Get current file info
	var currentFile File
	err := db.QueryRow("SELECT id, filename, path FROM files WHERE id=?", fileID).Scan(&currentFile.ID, &currentFile.Filename, &currentFile.Path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Start a transaction to ensure data consistency
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback() // Will be ignored if tx.Commit() is called

	// Delete file_tags relationships
	_, err = tx.Exec("DELETE FROM file_tags WHERE file_id=?", fileID)
	if err != nil {
		http.Error(w, "Failed to delete file tags", http.StatusInternalServerError)
		return
	}

	// Delete file record
	_, err = tx.Exec("DELETE FROM files WHERE id=?", fileID)
	if err != nil {
		http.Error(w, "Failed to delete file record", http.StatusInternalServerError)
		return
	}

	// Commit the database transaction
	err = tx.Commit()
	if err != nil {
		http.Error(w, "Failed to commit transaction", http.StatusInternalServerError)
		return
	}

	// Delete the physical file (after successful database deletion)
	err = os.Remove(currentFile.Path)
	if err != nil {
		// Log the error but don't fail the request - database is already clean
		log.Printf("Warning: Failed to delete physical file %s: %v", currentFile.Path, err)
	}

	// Redirect to home page with success
	http.Redirect(w, r, "/?deleted="+currentFile.Filename, http.StatusSeeOther)
}

// Handle file renaming
func fileRenameHandler(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/file/"+parts[2], http.StatusSeeOther)
		return
	}

	fileID := parts[2]
	newFilename := strings.TrimSpace(r.FormValue("newfilename"))

	if newFilename == "" {
		http.Error(w, "New filename cannot be empty", http.StatusBadRequest)
		return
	}

	// Sanitize filename
	newFilename = sanitizeFilename(newFilename)

	// Get current file info
	var currentFile File
	err := db.QueryRow("SELECT id, filename, path FROM files WHERE id=?", fileID).Scan(&currentFile.ID, &currentFile.Filename, &currentFile.Path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Skip if filename hasn't changed
	if currentFile.Filename == newFilename {
		http.Redirect(w, r, "/file/"+fileID, http.StatusSeeOther)
		return
	}

	// Check if new filename already exists
	newPath := filepath.Join(config.UploadDir, newFilename)
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		http.Error(w, "A file with that name already exists", http.StatusConflict)
		return
	}

	// Rename the physical file
	err = os.Rename(currentFile.Path, newPath)
	if err != nil {
		http.Error(w, "Failed to rename physical file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update database
	_, err = db.Exec("UPDATE files SET filename=?, path=? WHERE id=?", newFilename, newPath, fileID)
	if err != nil {
		// Try to rename file back if database update fails
		os.Rename(newPath, currentFile.Path)
		http.Error(w, "Failed to update database", http.StatusInternalServerError)
		return
	}

	// Redirect back to file page
	http.Redirect(w, r, "/file/"+fileID, http.StatusSeeOther)
}

// File detail and add tags
func fileHandler(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/file/")
	if strings.Contains(idStr, "/") {
		idStr = strings.SplitN(idStr, "/", 2)[0]
	}

	var f File
	err := db.QueryRow("SELECT id, filename, path, COALESCE(description, '') as description FROM files WHERE id=?", idStr).Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	f.Tags = make(map[string]string)
	rows, _ := db.Query(`
		SELECT c.name, t.value
		FROM tags t
		JOIN categories c ON c.id = t.category_id
		JOIN file_tags ft ON ft.tag_id = t.id
		WHERE ft.file_id=?`, f.ID)
	for rows.Next() {
		var cat, val string
		rows.Scan(&cat, &val)
		f.Tags[cat] = val
	}
	rows.Close()

	catRows, _ := db.Query("SELECT name FROM categories ORDER BY name")
	var cats []string
	for catRows.Next() {
		var c string
		catRows.Scan(&c)
		cats = append(cats, c)
	}
	catRows.Close()

	if r.Method == http.MethodPost {
		// Handle description update
		if r.FormValue("action") == "update_description" {
			description := r.FormValue("description")
			// Limit description to 2KB (2048 characters)
			if len(description) > 2048 {
				description = description[:2048]
			}

			_, err := db.Exec("UPDATE files SET description = ? WHERE id = ?", description, f.ID)
			if err != nil {
				http.Error(w, "Failed to update description", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/file/"+idStr, http.StatusSeeOther)
			return
		}

		// Handle tag addition (existing functionality)
		cat := r.FormValue("category")
		val := r.FormValue("value")
		if cat != "" && val != "" {
			var catID int
			db.QueryRow("SELECT id FROM categories WHERE name=?", cat).Scan(&catID)
			if catID == 0 {
				res, _ := db.Exec("INSERT INTO categories(name) VALUES(?)", cat)
				cid, _ := res.LastInsertId()
				catID = int(cid)
			}
			var tagID int
			db.QueryRow("SELECT id FROM tags WHERE category_id=? AND value=?", catID, val).Scan(&tagID)
			if tagID == 0 {
				res, _ := db.Exec("INSERT INTO tags(category_id, value) VALUES(?, ?)", catID, val)
				tid, _ := res.LastInsertId()
				tagID = int(tid)
			}
			db.Exec("INSERT OR IGNORE INTO file_tags(file_id, tag_id) VALUES (?, ?)", f.ID, tagID)
		}
		http.Redirect(w, r, "/file/"+idStr, http.StatusSeeOther)
		return
	}

    // IP and port for raw URL
    ip, _ := getLocalIP()
    port := strings.TrimPrefix(config.ServerPort, ":")
    // Escape filename for copy/paste
    escaped := url.PathEscape(f.Filename)

    pageData := PageData{
        Title: f.Filename,
        Data: struct {
            File            File
            Categories      []string
            EscapedFilename string
        }{f, cats, escaped},
        IP:   ip,
        Port: port,
    }

	tmpl.ExecuteTemplate(w, "file.html", pageData)
}

// Delete tag from file
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

// Show all tags
func tagsHandler(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Query(`
		SELECT c.name, t.value, COUNT(ft.file_id)
		FROM tags t
		JOIN categories c ON c.id = t.category_id
		LEFT JOIN file_tags ft ON ft.tag_id = t.id
		GROUP BY t.id
		HAVING COUNT(ft.file_id) > 0
		ORDER BY c.name, t.value`)
	defer rows.Close()

	tagMap := make(map[string][]TagDisplay)
	for rows.Next() {
		var cat, val string
		var count int
		rows.Scan(&cat, &val, &count)
		tagMap[cat] = append(tagMap[cat], TagDisplay{Value: val, Count: count})
	}

	pageData := PageData{
		Title: "All Tags",
		Data:  tagMap,
	}

	tmpl.ExecuteTemplate(w, "tags.html", pageData)
}

// Filter files by tags
func tagFilterHandler(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/tag/"), "/")
	if len(pathParts)%2 != 0 {
		http.Error(w, "Invalid tag filter path", http.StatusBadRequest)
		return
	}

	type filter struct {
		Category string
		Value    string
	}

	var filters []filter
	for i := 0; i < len(pathParts); i += 2 {
		filters = append(filters, filter{pathParts[i], pathParts[i+1]})
	}

	query := `SELECT f.id, f.filename, f.path, COALESCE(f.description, '') as description FROM files f WHERE 1=1`
	args := []interface{}{}
	for _, f := range filters {
		if f.Value == "unassigned" {
			// Files without any tag in this category
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
			// Files with this specific tag value
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

	rows, _ := db.Query(query, args...)
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		rows.Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
		f.EscapedFilename = url.PathEscape(f.Filename)
		files = append(files, f)
	}

	var titleParts []string
	for _, f := range filters {
		titleParts = append(titleParts, fmt.Sprintf("%s: %s", f.Category, f.Value))
	}
	title := "Tagged: " + strings.Join(titleParts, ", ")

	pageData := PageData{
		Title: title,
		Data: struct {
			Tagged   []File
			Untagged []File
		}{files, nil},
	}

	tmpl.ExecuteTemplate(w, "list.html", pageData)
}

func loadConfig() error {
	// Set defaults
	config = Config{
		DatabasePath: "./database.db",
		UploadDir:    "uploads",
		ServerPort:   ":8080",
	}

	// Try to load existing config
	if data, err := ioutil.ReadFile("config.json"); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return err
		}
	}

	// Ensure upload directory exists
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
	// Validate database path is not empty
	if newConfig.DatabasePath == "" {
		return fmt.Errorf("database path cannot be empty")
	}

	// Validate upload directory is not empty
	if newConfig.UploadDir == "" {
		return fmt.Errorf("upload directory cannot be empty")
	}

	// Validate server port format
	if newConfig.ServerPort == "" || !strings.HasPrefix(newConfig.ServerPort, ":") {
		return fmt.Errorf("server port must be in format ':8080'")
	}

	// Try to create upload directory if it doesn't exist
	if err := os.MkdirAll(newConfig.UploadDir, 0755); err != nil {
		return fmt.Errorf("cannot create upload directory: %v", err)
	}

	return nil
}

// Add this settings handler function
func settingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Handle settings update
		newConfig := Config{
			DatabasePath: strings.TrimSpace(r.FormValue("database_path")),
			UploadDir:    strings.TrimSpace(r.FormValue("upload_dir")),
			ServerPort:   strings.TrimSpace(r.FormValue("server_port")),
		}

		// Validate new configuration
		if err := validateConfig(newConfig); err != nil {
			pageData := PageData{
				Title: "Settings",
				Data: struct {
					Config Config
					Error  string
				}{config, err.Error()},
			}
			tmpl.ExecuteTemplate(w, "settings.html", pageData)
			return
		}

		// Check if database path changed and requires restart
		needsRestart := (newConfig.DatabasePath != config.DatabasePath ||
						newConfig.ServerPort != config.ServerPort)

		// Save new configuration
		config = newConfig
		if err := saveConfig(); err != nil {
			pageData := PageData{
				Title: "Settings",
				Data: struct {
					Config Config
					Error  string
				}{config, "Failed to save configuration: " + err.Error()},
			}
			tmpl.ExecuteTemplate(w, "settings.html", pageData)
			return
		}

		// Show success message
		var message string
		if needsRestart {
			message = "Settings saved successfully! Please restart the server for database/port changes to take effect."
		} else {
			message = "Settings saved successfully!"
		}

		pageData := PageData{
			Title: "Settings",
			Data: struct {
				Config  Config
				Error   string
				Success string
			}{config, "", message},
		}
		tmpl.ExecuteTemplate(w, "settings.html", pageData)
		return
	}

	// Show settings form
	pageData := PageData{
		Title: "Settings",
		Data: struct {
			Config  Config
			Error   string
			Success string
		}{config, "", ""},
	}
	tmpl.ExecuteTemplate(w, "settings.html", pageData)
}

// yt-dlp Handler
func ytdlpHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Redirect(w, r, "/upload", http.StatusSeeOther)
        return
    }

    videoURL := r.FormValue("url")
    if videoURL == "" {
        http.Error(w, "No URL provided", http.StatusBadRequest)
        return
    }

    // Step 1: Get the expected filename first
    outTemplate := filepath.Join(config.UploadDir, "%(title)s.%(ext)s")
    filenameCmd := exec.Command("yt-dlp", "--playlist-items", "1", "-f", "mp4", "-o", outTemplate, "--get-filename", videoURL)
    filenameBytes, err := filenameCmd.Output()
    if err != nil {
        http.Error(w, fmt.Sprintf("Failed to get filename: %v", err), http.StatusInternalServerError)
        return
    }
    expectedFullPath := strings.TrimSpace(string(filenameBytes))

    // Extract just the filename from the full path
    expectedFilename := filepath.Base(expectedFullPath)

    // Enforce strict duplicate check
    finalFilename, finalPath, err := checkFileConflictStrict(expectedFilename)
    if err != nil {
        http.Error(w, err.Error(), http.StatusConflict)
        return
    }

    // Step 2: Download with yt-dlp using the full output template
    downloadCmd := exec.Command("yt-dlp", "--playlist-items", "1", "-f", "mp4", "-o", outTemplate, videoURL)
    downloadCmd.Stdout = os.Stdout
    downloadCmd.Stderr = os.Stderr
    if err := downloadCmd.Run(); err != nil {
        http.Error(w, fmt.Sprintf("Failed to download video: %v", err), http.StatusInternalServerError)
        return
    }

    // Step 3: The file should now be at expectedFullPath, move it to finalPath if different
    if expectedFullPath != finalPath {
        if err := os.Rename(expectedFullPath, finalPath); err != nil {
            http.Error(w, fmt.Sprintf("Failed to move downloaded file: %v", err), http.StatusInternalServerError)
            return
        }
    }

    // Step 4: Process video (codec detection and potential re-encoding)
    // Create a temporary path for processing
    tempPath := finalPath + ".tmp"
    if err := os.Rename(finalPath, tempPath); err != nil {
        http.Error(w, fmt.Sprintf("Failed to create temp file for processing: %v", err), http.StatusInternalServerError)
        return
    }

    processedPath, warningMsg, err := processVideoFile(tempPath, finalPath)
    if err != nil {
        os.Remove(tempPath) // Clean up
        http.Error(w, fmt.Sprintf("Failed to process video: %v", err), http.StatusInternalServerError)
        return
    }

    // Step 5: Save to database
    id, err := saveFileToDatabase(finalFilename, processedPath)
    if err != nil {
        os.Remove(processedPath)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Step 6: Redirect with optional warning
    redirectURL := fmt.Sprintf("/file/%d", id)
    if warningMsg != "" {
        redirectURL += "?warning=" + url.QueryEscape(warningMsg)
    }
    http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// Parse file ID ranges like "1-3,6,9" into a slice of integers
func parseFileIDRange(rangeStr string) ([]int, error) {
	var fileIDs []int
	parts := strings.Split(rangeStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, "-") {
			// Handle range like "1-3"
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
			// Handle single ID like "6"
			id, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid file ID: %s", part)
			}
			fileIDs = append(fileIDs, id)
		}
	}

	// Remove duplicates and sort
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

// Validate that all file IDs exist in the database
func validateFileIDs(fileIDs []int) ([]File, error) {
	if len(fileIDs) == 0 {
		return nil, fmt.Errorf("no file IDs provided")
	}

	// Build placeholders for the IN clause
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

	// Check if any IDs were not found
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

// Apply tag operations to multiple files
func applyBulkTagOperations(fileIDs []int, category, value, operation string) error {
	if category == "" {
		return fmt.Errorf("category cannot be empty")
	}

	// For add operations, value is required. For remove operations, value is optional
	if operation == "add" && value == "" {
		return fmt.Errorf("value cannot be empty when adding tags")
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %v", err)
	}
	defer tx.Rollback()

	// Get or create category
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

	// Get or create tag (only needed for specific value operations)
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

	// Apply operation to all files
	for _, fileID := range fileIDs {
		if operation == "add" {
			_, err = tx.Exec("INSERT OR IGNORE INTO file_tags(file_id, tag_id) VALUES (?, ?)", fileID, tagID)
			if err != nil {
				return fmt.Errorf("failed to add tag to file %d: %v", fileID, err)
			}
		} else if operation == "remove" {
			if value != "" {
				// Remove specific tag value
				_, err = tx.Exec("DELETE FROM file_tags WHERE file_id=? AND tag_id=?", fileID, tagID)
				if err != nil {
					return fmt.Errorf("failed to remove tag from file %d: %v", fileID, err)
				}
			} else {
				// Remove all tags in this category
				_, err = tx.Exec(`
					DELETE FROM file_tags
					WHERE file_id=? AND tag_id IN (
						SELECT t.id FROM tags t WHERE t.category_id=?
					)`, fileID, catID)
				if err != nil {
					return fmt.Errorf("failed to remove category tags from file %d: %v", fileID, err)
				}
			}
		} else {
			return fmt.Errorf("invalid operation: %s (must be 'add' or 'remove')", operation)
		}
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %v", err)
	}

	return nil
}

// Bulk tag handler
func bulkTagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Show bulk tag form

		// Get all existing categories for the dropdown
		catRows, _ := db.Query("SELECT name FROM categories ORDER BY name")
		var cats []string
		for catRows.Next() {
			var c string
			catRows.Scan(&c)
			cats = append(cats, c)
		}
		catRows.Close()

		// Get recent files for reference
		recentRows, _ := db.Query("SELECT id, filename FROM files ORDER BY id DESC LIMIT 20")
		var recentFiles []File
		for recentRows.Next() {
			var f File
			recentRows.Scan(&f.ID, &f.Filename)
			recentFiles = append(recentFiles, f)
		}
		recentRows.Close()

		pageData := PageData{
			Title: "Bulk Tag Editor",
			Data: struct {
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
			}{
				Categories:  cats,
				RecentFiles: recentFiles,
				Error:       "",
				Success:     "",
				FormData: struct {
					FileRange string
					Category  string
					Value     string
					Operation string
				}{
					FileRange: "",
					Category:  "",
					Value:     "",
					Operation: "add",
				},
			},
		}

		tmpl.ExecuteTemplate(w, "bulk-tag.html", pageData)
		return
	}

	if r.Method == http.MethodPost {
		// Process bulk tag operation
		rangeStr := strings.TrimSpace(r.FormValue("file_range"))
		category := strings.TrimSpace(r.FormValue("category"))
		value := strings.TrimSpace(r.FormValue("value"))
		operation := r.FormValue("operation") // "add" or "remove"

		// Get categories for form redisplay
		catRows, _ := db.Query("SELECT name FROM categories ORDER BY name")
		var cats []string
		for catRows.Next() {
			var c string
			catRows.Scan(&c)
			cats = append(cats, c)
		}
		catRows.Close()

		// Get recent files for reference
		recentRows, _ := db.Query("SELECT id, filename FROM files ORDER BY id DESC LIMIT 20")
		var recentFiles []File
		for recentRows.Next() {
			var f File
			recentRows.Scan(&f.ID, &f.Filename)
			recentFiles = append(recentFiles, f)
		}
		recentRows.Close()

		// Helper function to create error response
		createErrorResponse := func(errorMsg string) {
			pageData := PageData{
				Title: "Bulk Tag Editor",
				Data: struct {
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
				}{
					Categories:  cats,
					RecentFiles: recentFiles,
					Error:       errorMsg,
					Success:     "",
					FormData: struct {
						FileRange string
						Category  string
						Value     string
						Operation string
					}{
						FileRange: rangeStr,
						Category:  category,
						Value:     value,
						Operation: operation,
					},
				},
			}
			tmpl.ExecuteTemplate(w, "bulk-tag.html", pageData)
		}

		// Validate basic inputs
		if rangeStr == "" {
			createErrorResponse("File range cannot be empty")
			return
		}

		if category == "" {
			createErrorResponse("Category cannot be empty")
			return
		}

		// For add operations, value is required. For remove operations, value is optional
		if operation == "add" && value == "" {
			createErrorResponse("Value cannot be empty when adding tags")
			return
		}

		// Parse file ID range
		fileIDs, err := parseFileIDRange(rangeStr)
		if err != nil {
			createErrorResponse(fmt.Sprintf("Invalid file range: %v", err))
			return
		}

		// Validate file IDs exist
		validFiles, err := validateFileIDs(fileIDs)
		if err != nil {
			createErrorResponse(fmt.Sprintf("File validation error: %v", err))
			return
		}

		// Apply tag operations
		err = applyBulkTagOperations(fileIDs, category, value, operation)
		if err != nil {
			createErrorResponse(fmt.Sprintf("Tag operation failed: %v", err))
			return
		}

		// Success message
		var operationText string
		var successMsg string

		if operation == "add" {
			operationText = "added to"
			successMsg = fmt.Sprintf("Tag '%s: %s' %s %d files",
				category, value, operationText, len(validFiles))
		} else {
			if value != "" {
				operationText = "removed from"
				successMsg = fmt.Sprintf("Tag '%s: %s' %s %d files",
					category, value, operationText, len(validFiles))
			} else {
				operationText = "removed from"
				successMsg = fmt.Sprintf("All '%s' category tags %s %d files",
					category, operationText, len(validFiles))
			}
		}

		var filenames []string
		for _, f := range validFiles {
			filenames = append(filenames, f.Filename)
		}

		if len(filenames) <= 5 {
			successMsg += fmt.Sprintf(": %s", strings.Join(filenames, ", "))
		} else {
			successMsg += fmt.Sprintf(": %s and %d more",
				strings.Join(filenames[:5], ", "), len(filenames)-5)
		}

		pageData := PageData{
			Title: "Bulk Tag Editor",
			Data: struct {
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
			}{
				Categories:  cats,
				RecentFiles: recentFiles,
				Error:       "",
				Success:     successMsg,
				FormData: struct {
					FileRange string
					Category  string
					Value     string
					Operation string
				}{
					FileRange: rangeStr,
					Category:  category,
					Value:     value,
					Operation: operation,
				},
			},
		}

		tmpl.ExecuteTemplate(w, "bulk-tag.html", pageData)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}