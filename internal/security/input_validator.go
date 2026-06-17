package security

import (
	"fmt"
	"blackwatch/internal/domain"
)

const (
	MaxImageSize         = 10 * 1024 * 1024 // 10MB
	MaxConcurrentUploads = 3
	MaxPlayersPerMatch   = 10
	MaxHistoryQueryLimit = 100
)

func ValidateImageSize(size int64) error {
	if size > MaxImageSize {
		return fmt.Errorf("%w: size %d exceeds maximum %d", domain.ErrImageTooLarge, size, MaxImageSize)
	}
	if size <= 0 {
		return fmt.Errorf("invalid image size: %d", size)
	}
	return nil
}

func ValidateContentLength(contentLength int64) error {
	if contentLength < 0 {
		return nil
	}
	return ValidateImageSize(contentLength)
}

func SanitizePlayerName(name string) (string, error) {
	return domain.NormalizePlayerName(name)
}

func ValidateQueryLimit(limit int) error {
	if limit <= 0 || limit > MaxHistoryQueryLimit {
		return fmt.Errorf("invalid limit: must be between 1 and %d", MaxHistoryQueryLimit)
	}
	return nil
}

func ValidatePlayerID(id int) error {
	if id <= 0 {
		return domain.ErrPlayerNotFound
	}
	return nil
}

func ValidateMatchID(id int) error {
	if id <= 0 {
		return domain.ErrMatchNotFound
	}
	return nil
}
