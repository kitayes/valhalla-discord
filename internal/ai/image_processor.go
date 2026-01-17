package ai

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Register PNG decoder

	"github.com/disintegration/imaging"
)

const (
	// Image optimization settings for AI processing
	maxImageWidth = 1000 // px - sufficient for text recognition
	jpegQuality   = 75   // balance between quality and size
)

// ImageProcessor handles image optimization for AI processing
type ImageProcessor struct{}

// NewImageProcessor creates a new image processor instance
func NewImageProcessor() *ImageProcessor {
	return &ImageProcessor{}
}

// OptimizeForAI compresses and resizes image for AI API
// Reduces file size by ~90-95% while preserving text readability
func (p *ImageProcessor) OptimizeForAI(data []byte) ([]byte, error) {
	// Decode image (supports PNG, JPEG, etc.)
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	originalSize := len(data)

	// Check if resizing is needed
	bounds := img.Bounds()
	width := bounds.Dx()

	var optimized image.Image = img

	// Resize if wider than maxImageWidth (preserving aspect ratio)
	if width > maxImageWidth {
		optimized = imaging.Resize(img, maxImageWidth, 0, imaging.Lanczos)
	}

	// Convert to JPEG with compression
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, optimized, &jpeg.Options{Quality: jpegQuality})
	if err != nil {
		return nil, fmt.Errorf("failed to encode jpeg: %w", err)
	}

	compressed := buf.Bytes()
	compressedSize := len(compressed)

	// Calculate compression metrics
	compressionRatio := float64(originalSize) / float64(compressedSize)

	// Log compression results (optional, for monitoring)
	_ = format // Use format if needed for logging
	_ = compressionRatio
	// Example: log.Debug("Image optimized: %s %d KB → JPEG %d KB (%.1fx compression)",
	//     format, originalSize/1024, compressedSize/1024, compressionRatio)

	return compressed, nil
}
