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
	// ErrInDraft reports that the player is a captain or a pick of a draft that
	// is still open, so they cannot queue again until it ends.
	ErrInDraft = errors.New("player is already in a draft")
	// ErrInMatch reports that the player is on a roster of a match still being
	// played, so they cannot queue for another game until it ends.
	ErrInMatch = errors.New("player is in an active match")
	// ErrSameCaptain reports that both captains of a draft are one person.
	ErrSameCaptain = errors.New("both captains are the same player")
	// ErrNotInLobby reports that the player is not in the main queue — never
	// joined, already picked, or only on the waitlist.
	ErrNotInLobby = errors.New("player is not in the lobby")
	// ErrDraftNotFound reports that no open draft has this number; drafts live
	// in memory, so a restart is the usual cause.
	ErrDraftNotFound = errors.New("draft not found")
	// ErrDraftComplete reports that every pick of the draft has been made.
	ErrDraftComplete = errors.New("draft is already complete")
	// ErrNotCaptain reports that someone other than the draft's captains tried
	// to pick.
	ErrNotCaptain = errors.New("only the draft's captains can pick")
	// ErrNotYourTurn reports that the captain picked out of turn.
	ErrNotYourTurn = errors.New("it is the other captain's turn")

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
	// ErrBettingOnOwnMatch reports that the bettor is playing in the match they
	// tried to stake on. Backing the other side is then a way to profit from
	// losing on purpose.
	ErrBettingOnOwnMatch = errors.New("cannot bet on a match you are playing in")

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
