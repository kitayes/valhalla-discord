package domain

import (
	"errors"
	"fmt"
)

var (
	ErrPlayerNotFound        = errors.New("player not found")
	ErrMatchNotFound         = errors.New("match not found")
	ErrDuplicateMatch        = errors.New("duplicate match detected")
	ErrInvalidPlayerName     = errors.New("invalid player name")
	ErrInvalidMatchData      = errors.New("invalid match data")
	ErrPlayerNameTooLong     = errors.New("player name too long")
	ErrPlayerNameEmpty       = errors.New("player name cannot be empty")
	ErrInvalidKDAValues      = errors.New("invalid KDA values")
	ErrImageTooLarge         = errors.New("image file too large")
	ErrImageProcessingFailed = errors.New("image processing failed")
	ErrRateLimitExceeded     = errors.New("rate limit exceeded")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on field '%s': %s", e.Field, e.Message)
}

type DomainError struct {
	Code    string
	Message string
	Err     error
}

func (e *DomainError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s - %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *DomainError) Unwrap() error {
	return e.Err
}

func NewDomainError(code, message string, err error) *DomainError {
	return &DomainError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}
