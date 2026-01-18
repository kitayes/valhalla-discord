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
	maxImageWidth = 1000
	jpegQuality   = 75
)

type ImageProcessor struct{}

func NewImageProcessor() *ImageProcessor {
	return &ImageProcessor{}
}

func (p *ImageProcessor) OptimizeForAI(data []byte) ([]byte, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	originalSize := len(data)

	bounds := img.Bounds()
	width := bounds.Dx()

	var optimized image.Image = img

	if width > maxImageWidth {
		optimized = imaging.Resize(img, maxImageWidth, 0, imaging.Lanczos)
	}

	var buf bytes.Buffer
	err = jpeg.Encode(&buf, optimized, &jpeg.Options{Quality: jpegQuality})
	if err != nil {
		return nil, fmt.Errorf("failed to encode jpeg: %w", err)
	}

	compressed := buf.Bytes()
	compressedSize := len(compressed)

	compressionRatio := float64(originalSize) / float64(compressedSize)

	_ = format
	_ = compressionRatio

	return compressed, nil
}
