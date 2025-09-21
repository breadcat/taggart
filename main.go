package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"io"
	"net/url"

	_ "github.com/mattn/go-sqlite3"
)

var (
	db   *sql.DB
	tmpl *template.Template
)

type File struct {
	ID       int
	Filename string
	Path     string
	Tags     map[string]string
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
	var err error
	db, err = sql.Open("sqlite3", "./database.db")
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

	os.MkdirAll("uploads", 0755)
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

	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	log.Println("Server started at http://localhost:8080")
	http.ListenAndServe(":8080", nil)
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

	dstPath := filepath.Join("uploads", filename)

	// Avoid overwriting existing files
	originalFilename := filename
	for i := 1; ; i++ {
		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(originalFilename)
		name := strings.TrimSuffix(originalFilename, ext)
		filename = fmt.Sprintf("%s_%d%s", name, i, ext)
		dstPath = filepath.Join("uploads", filename)
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

	dstPath := filepath.Join("uploads", header.Filename)
	dst, _ := os.Create(dstPath)
	defer dst.Close()
	_, _ = dst.ReadFrom(file)

	res, _ := db.Exec("INSERT INTO files (filename, path) VALUES (?, ?)", header.Filename, dstPath)
	id, _ := res.LastInsertId()

	http.Redirect(w, r, fmt.Sprintf("/file/%d", id), http.StatusSeeOther)
}

// Router for file operations and tag deletion
func fileRouter(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) >= 7 && parts[3] == "tag" {
		tagActionHandler(w, r, parts)
		return
	}
	fileHandler(w, r)
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