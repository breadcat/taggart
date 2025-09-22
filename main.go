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
	"path/filepath"
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
	Path     string
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
		path TEXT
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
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/upload-url", uploadFromURLHandler)
	http.HandleFunc("/file/", fileRouter)
	http.HandleFunc("/tags", tagsHandler)
	http.HandleFunc("/tag/", tagFilterHandler)
	http.HandleFunc("/untagged", untaggedFilesHandler)
	http.HandleFunc("/search", searchHandler)
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
		// * becomes % and ? becomes _ (standard SQL wildcards)
		sqlPattern := strings.ReplaceAll(query, "*", "%")
		sqlPattern = strings.ReplaceAll(sqlPattern, "?", "_")

		// Search for files matching the pattern
		rows, err := db.Query("SELECT id, filename, path FROM files WHERE filename LIKE ? ORDER BY filename", sqlPattern)
		if err != nil {
			http.Error(w, "Search failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var f File
			rows.Scan(&f.ID, &f.Filename, &f.Path)
			files = append(files, f)
		}

		searchTitle = fmt.Sprintf("Search Results for: %s", query)
	} else {
		searchTitle = "Search Files"
	}

	// Always initialize the data structure properly
	pageData := PageData{
		Title: searchTitle,
		Data: struct {
			Files []File
			Query string
		}{files, query},
	}

	tmpl.ExecuteTemplate(w, "search.html", pageData)
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

	// Validate URL
	parsedURL, err := url.ParseRequestURI(fileURL)
	if err != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	// Download the file
	resp, err := http.Get(fileURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to download file", http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	// Determine filename
	var filename string
	if customFilename != "" {
		filename = customFilename
	} else {
		// Use basename from URL as before
		parts := strings.Split(parsedURL.Path, "/")
		filename = parts[len(parts)-1]
		if filename == "" {
			filename = "file_from_url"
		}
	}

	// Sanitize filename (remove potentially dangerous characters)
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, "..", "_")
	if filename == "" {
		filename = "file_from_url"
	}

	dstPath := filepath.Join(config.UploadDir, filename)

	// Avoid overwriting existing files
	originalFilename := filename
	for i := 1; ; i++ {
		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(originalFilename)
		name := strings.TrimSuffix(originalFilename, ext)
		filename = fmt.Sprintf("%s_%d%s", name, i, ext)
		dstPath = filepath.Join(config.UploadDir, filename)
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, resp.Body)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	// Add to database
	res, err := db.Exec("INSERT INTO files (filename, path) VALUES (?, ?)", filename, dstPath)
	if err != nil {
		http.Error(w, "Failed to record file", http.StatusInternalServerError)
		return
	}

	id, _ := res.LastInsertId()
	http.Redirect(w, r, fmt.Sprintf("/file/%d", id), http.StatusSeeOther)
}

// List all files, plus untagged files
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	// Tagged files
	rows, _ := db.Query(`
		SELECT DISTINCT f.id, f.filename, f.path
		FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
		ORDER BY f.id DESC
	`)
	defer rows.Close()
	var tagged []File
	for rows.Next() {
		var f File
		rows.Scan(&f.ID, &f.Filename, &f.Path)
		tagged = append(tagged, f)
	}

	// Untagged files
	untaggedRows, _ := db.Query(`
		SELECT f.id, f.filename, f.path
		FROM files f
		LEFT JOIN file_tags ft ON ft.file_id = f.id
		WHERE ft.file_id IS NULL
		ORDER BY f.id DESC
	`)
	defer untaggedRows.Close()
	var untagged []File
	for untaggedRows.Next() {
		var f File
		untaggedRows.Scan(&f.ID, &f.Filename, &f.Path)
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
		SELECT f.id, f.filename, f.path
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
		rows.Scan(&f.ID, &f.Filename, &f.Path)
		files = append(files, f)
	}

	pageData := PageData{
		Title: "Untagged Files",
		Data:  files,
	}

	tmpl.ExecuteTemplate(w, "untagged.html", pageData)
}

// Upload a file
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		pageData := PageData{
			Title: "Upload File",
			Data:  nil,
		}
		tmpl.ExecuteTemplate(w, "upload.html", pageData)
		return
	}

	file, header, _ := r.FormFile("file")
	defer file.Close()

	dstPath := filepath.Join(config.UploadDir, header.Filename)
	dst, _ := os.Create(dstPath)
	defer dst.Close()
	_, _ = dst.ReadFrom(file)

	res, _ := db.Exec("INSERT INTO files (filename, path) VALUES (?, ?)", header.Filename, dstPath)
	id, _ := res.LastInsertId()

	http.Redirect(w, r, fmt.Sprintf("/file/%d", id), http.StatusSeeOther)
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
	newFilename = strings.ReplaceAll(newFilename, "/", "_")
	newFilename = strings.ReplaceAll(newFilename, "\\", "_")
	newFilename = strings.ReplaceAll(newFilename, "..", "_")

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
	db.QueryRow("SELECT id, filename, path FROM files WHERE id=?", idStr).Scan(&f.ID, &f.Filename, &f.Path)

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

	pageData := PageData{
		Title: f.Filename,
		Data: struct {
			File       File
			Categories []string
		}{f, cats},
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

	query := `SELECT f.id, f.filename, f.path FROM files f WHERE 1=1`
	args := []interface{}{}
	for _, f := range filters {
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

	rows, _ := db.Query(query, args...)
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		rows.Scan(&f.ID, &f.Filename, &f.Path)
		files = append(files, f)
	}

	// Create title from filters
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