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

// Lobby and betting outcomes callers must be able to branch on. These used to be
// pre-rendered Russian strings returned alongside the result, which forced every
// delivery layer to decode intent out of message text.
var (
	// ErrQueueBanned reports that the player is serving a queue ban.
	ErrQueueBanned = errors.New("player is banned from the queue")
	// ErrLobbyClosed reports that the lobby is not accepting players.
	ErrLobbyClosed = errors.New("lobby is closed")
	// ErrAlreadyQueued reports that the player already holds a place in the
	// queue, so the request was a no-op rather than a failure.
	ErrAlreadyQueued = errors.New("player is already in the queue")

	// ErrMatchNotActive reports that the match exists but has already been
	// closed or cancelled, so a lifecycle action that needs an ACTIVE match
	// cannot be replayed against it. It is deliberately distinct from
	// ErrMatchNotFound: the two need different words in front of the user.
	ErrMatchNotActive = errors.New("match is no longer active")

	// ErrBettingClosed reports that the betting window for a match has shut.
	ErrBettingClosed = errors.New("betting is closed for this match")
	// ErrInsufficientPoints reports that the bettor cannot cover the stake.
	ErrInsufficientPoints = errors.New("insufficient points")
	// ErrAlreadyBet reports that the user already backed this match.
	ErrAlreadyBet = errors.New("a bet was already placed on this match")

	// ErrDiscordNotLinked reports that a player has no Discord account bound.
	ErrDiscordNotLinked = errors.New("player has no Discord account linked")
	// ErrDiscordAlreadyBound reports that the caller's Discord account already
	// owns a different profile.
	ErrDiscordAlreadyBound = errors.New("this Discord account is already bound to another profile")
	// ErrTelegramAlreadyLinked is ErrDiscordAlreadyBound's counterpart for the
	// Telegram side of the same profile.
	ErrTelegramAlreadyLinked = errors.New("this Telegram account is already linked to another profile")
	// ErrDiscordNotBound reports that the caller has not claimed a profile yet,
	// so there is nothing to link a Telegram account to.
	ErrDiscordNotBound = errors.New("your Discord account is not bound to a profile")
	// ErrProfileTaken reports that the requested profile belongs to someone else.
	ErrProfileTaken = errors.New("this profile is already claimed by another Discord account")
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
