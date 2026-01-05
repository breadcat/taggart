package main

import (
	"archive/zip"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net/http"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// generateCBZThumbnail creates a 2x2 collage thumbnail from a CBZ file
func generateCBZThumbnail(cbzPath, uploadDir, filename string) error {

	thumbDir := filepath.Join(uploadDir, "thumbnails")

	if err := os.MkdirAll(thumbDir, 0755); err != nil {
		return fmt.Errorf("failed to create thumbnails directory: %v", err)
	}

	thumbPath := filepath.Join(thumbDir, filename+".jpg")

	// Open the CBZ (ZIP) file
	r, err := zip.OpenReader(cbzPath)
	if err != nil {
		return fmt.Errorf("failed to open CBZ: %v", err)
	}
	defer r.Close()

	// Get list of image files from the archive
	var imageFiles []*zip.File
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			ext := strings.ToLower(filepath.Ext(f.Name))
			if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
				imageFiles = append(imageFiles, f)
			}
		}
	}

	if len(imageFiles) == 0 {
		return fmt.Errorf("no images found in CBZ")
	}

	// Sort files by name to get consistent ordering
	sort.Slice(imageFiles, func(i, j int) bool {
		return imageFiles[i].Name < imageFiles[j].Name
	})

	// Select up to 4 images evenly distributed
	var selectedFiles []*zip.File
	if len(imageFiles) <= 4 {
		selectedFiles = imageFiles
	} else {
		// Pick 4 images evenly distributed through the comic
		step := len(imageFiles) / 4
		for i := 0; i < 4; i++ {
			selectedFiles = append(selectedFiles, imageFiles[i*step])
		}
	}


	// Load the selected images
	var images []image.Image
	for _, f := range selectedFiles {
		rc, err := f.Open()
		if err != nil {
			log.Printf("CBZ Thumbnail: Failed to open %s: %v", f.Name, err)
			continue
		}

		img, _, err := image.Decode(rc)
		rc.Close()
		if err != nil {
			log.Printf("CBZ Thumbnail: Failed to decode %s: %v", f.Name, err)
			continue
		}
		images = append(images, img)
	}


	if len(images) == 0 {
		return fmt.Errorf("failed to decode any images from CBZ")
	}

	// Create collage
	collage := createCollage(images, 400) // 400px target width

	// Save as JPEG
	outFile, err := os.Create(thumbPath)
	if err != nil {
		return fmt.Errorf("failed to create thumbnail file: %v", err)
	}
	defer outFile.Close()

	if err := jpeg.Encode(outFile, collage, &jpeg.Options{Quality: 85}); err != nil {
		return fmt.Errorf("failed to encode JPEG: %v", err)
	}

	return nil
}

// createCollage creates a 2x2 grid from up to 4 images
func createCollage(images []image.Image, targetWidth int) image.Image {
	// Calculate cell size (half of target width)
	cellSize := targetWidth / 2

	// Create output image
	collageImg := image.NewRGBA(image.Rect(0, 0, targetWidth, targetWidth))

	// Fill with white background
	for y := 0; y < targetWidth; y++ {
		for x := 0; x < targetWidth; x++ {
			collageImg.Set(x, y, image.White)
		}
	}

	// Positions for the 2x2 grid
	positions := []image.Point{
		{0, 0},                    // Top-left
		{cellSize, 0},             // Top-right
		{0, cellSize},             // Bottom-left
		{cellSize, cellSize},      // Bottom-right
	}

	// Draw each image
	for i, img := range images {
		if i >= 4 {
			break
		}

		// Resize image to fit cell
		resized := resizeImage(img, cellSize, cellSize)

		// Draw at position
		pos := positions[i]
		drawImage(collageImg, resized, pos.X, pos.Y)
	}

	return collageImg
}

// resizeImage resizes an image to fit within maxWidth x maxHeight while maintaining aspect ratio
func resizeImage(img image.Image, maxWidth, maxHeight int) image.Image {
	bounds := img.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Calculate scale to fit
	scaleX := float64(maxWidth) / float64(srcWidth)
	scaleY := float64(maxHeight) / float64(srcHeight)
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}

	newWidth := int(float64(srcWidth) * scale)
	newHeight := int(float64(srcHeight) * scale)

	// Create new image
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Simple nearest-neighbor scaling
	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			srcX := int(float64(x) / scale)
			srcY := int(float64(y) / scale)
			dst.Set(x, y, img.At(srcX+bounds.Min.X, srcY+bounds.Min.Y))
		}
	}

	return dst
}

// drawImage draws src onto dst at the given position
func drawImage(dst *image.RGBA, src image.Image, x, y int) {
	bounds := src.Bounds()
	for dy := 0; dy < bounds.Dy(); dy++ {
		for dx := 0; dx < bounds.Dx(); dx++ {
			dst.Set(x+dx, y+dy, src.At(bounds.Min.X+dx, bounds.Min.Y+dy))
		}
	}
}

// CBZImage represents a single image within a CBZ file
type CBZImage struct {
	Filename string
	Index    int
}

// getCBZImages returns a list of images in a CBZ file
func getCBZImages(cbzPath string) ([]CBZImage, error) {
	r, err := zip.OpenReader(cbzPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var images []CBZImage
	for i, f := range r.File {
		if !f.FileInfo().IsDir() {
			ext := strings.ToLower(filepath.Ext(f.Name))
			if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
				images = append(images, CBZImage{
					Filename: f.Name,
					Index:    i,
				})
			}
		}
	}

	// Sort by filename for consistent ordering
	sort.Slice(images, func(i, j int) bool {
		return images[i].Filename < images[j].Filename
	})

	// Re-index after sorting
	for i := range images {
		images[i].Index = i
	}

	return images, nil
}

// serveCBZImage extracts and serves a specific image from a CBZ file
func serveCBZImage(w http.ResponseWriter, cbzPath string, imageIndex int) error {
	r, err := zip.OpenReader(cbzPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Get sorted list of images
	var imageFiles []*zip.File
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			ext := strings.ToLower(filepath.Ext(f.Name))
			if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
				imageFiles = append(imageFiles, f)
			}
		}
	}

	sort.Slice(imageFiles, func(i, j int) bool {
		return imageFiles[i].Name < imageFiles[j].Name
	})

	if imageIndex < 0 || imageIndex >= len(imageFiles) {
		return fmt.Errorf("image index out of range")
	}

	targetFile := imageFiles[imageIndex]

	// Set content type based on extension
	ext := strings.ToLower(filepath.Ext(targetFile.Name))
	switch ext {
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	}

	rc, err := targetFile.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	_, err = io.Copy(w, rc)
	return err
}

// cbzViewerHandler handles the CBZ gallery viewer
func cbzViewerHandler(w http.ResponseWriter, r *http.Request) {
	// Extract file ID from URL
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/cbz/"), "/")
	if len(parts) < 1 {
		renderError(w, "Invalid CBZ viewer path", http.StatusBadRequest)
		return
	}

	fileID := parts[0]

	// Get the file from database
	var f File
	err := db.QueryRow("SELECT id, filename, path, COALESCE(description, '') FROM files WHERE id = ?", fileID).
		Scan(&f.ID, &f.Filename, &f.Path, &f.Description)
	if err != nil {
		renderError(w, "File not found", http.StatusNotFound)
		return
	}

	cbzPath := filepath.Join(config.UploadDir, f.Filename)

	// Check if requesting a specific image
	if len(parts) >= 3 && parts[1] == "image" {
		imageIndex := 0
		fmt.Sscanf(parts[2], "%d", &imageIndex)

		if err := serveCBZImage(w, cbzPath, imageIndex); err != nil {
			renderError(w, "Failed to serve image", http.StatusInternalServerError)
		}
		return
	}

	// Get list of images
	images, err := getCBZImages(cbzPath)
	if err != nil {
		renderError(w, "Failed to read CBZ contents", http.StatusInternalServerError)
		return
	}

	// Determine which image to display
	currentIndex := 0
	if len(parts) >= 2 {
		fmt.Sscanf(parts[1], "%d", &currentIndex)
	}

	if currentIndex < 0 {
		currentIndex = 0
	}
	if currentIndex >= len(images) {
		currentIndex = len(images) - 1
	}

	// Prepare data for template
	type CBZViewData struct {
		File         File
		Images       []CBZImage
		CurrentIndex int
		TotalImages  int
		HasPrev      bool
		HasNext      bool
	}

	viewData := CBZViewData{
		File:         f,
		Images:       images,
		CurrentIndex: currentIndex,
		TotalImages:  len(images),
		HasPrev:      currentIndex > 0,
		HasNext:      currentIndex < len(images)-1,
	}

	pageData := buildPageData(f.Filename, viewData)

	renderTemplate(w, "cbz_viewer.html", pageData)
}