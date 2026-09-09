package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	SignatureSeparator = "|"
	MinPlayersInMatch  = 1
	MaxPlayersInMatch  = 10
)

type MatchResult string

const (
	MatchResultWin  MatchResult = "WIN"
	MatchResultLose MatchResult = "LOSE"
)

type PlayerResult struct {
	PlayerID   int
	PlayerName string
	Result     MatchResult
	Kills      int
	Deaths     int
	Assists    int
}

func (pr *PlayerResult) Validate() error {
	if err := ValidatePlayerName(pr.PlayerName); err != nil {
		return err
	}

	if pr.Kills < 0 || pr.Deaths < 0 || pr.Assists < 0 {
		return ErrInvalidKDAValues
	}

	if pr.Result != MatchResultWin && pr.Result != MatchResultLose {
		return NewDomainError("INVALID_RESULT", "result must be WIN or LOSE", nil)
	}

	return nil
}

type Match struct {
	ID             int
	FileHash       string
	MatchSignature string
	Players        []PlayerResult
	CreatedAt      time.Time
}

func NewMatch(fileHash string, players []PlayerResult) (*Match, error) {
	if len(players) < MinPlayersInMatch || len(players) > MaxPlayersInMatch {
		return nil, NewDomainError(
			"INVALID_PLAYER_COUNT",
			fmt.Sprintf("match must have between %d and %d players", MinPlayersInMatch, MaxPlayersInMatch),
			nil,
		)
	}

	for i, p := range players {
		if err := p.Validate(); err != nil {
			return nil, NewDomainError(
				"INVALID_PLAYER_RESULT",
				fmt.Sprintf("player %d validation failed", i),
				err,
			)
		}
	}

	signature := GenerateMatchSignature(players)

	return &Match{
		FileHash:       fileHash,
		MatchSignature: signature,
		Players:        players,
		CreatedAt:      time.Now(),
	}, nil
}

func GenerateMatchSignature(players []PlayerResult) string {
	var sb strings.Builder
	for _, p := range players {
		sb.WriteString(fmt.Sprintf(
			"%s-%s-%d-%d-%d%s",
			p.PlayerName,
			p.Result,
			p.Kills,
			p.Deaths,
			p.Assists,
			SignatureSeparator,
		))
	}
	return sb.String()
}

func GenerateFileHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// Team names are a cross-service contract: the Discord referee UI writes them,
// the repository stores them on lobby_matches.winner, and the Telegram betting
// flow compares against them. They were duplicated as literals in seven places
// across four packages, so any rename would have silently split the payout logic
// from the match result.
const (
	TeamA = "Team A"
	TeamB = "Team B"
)

// IsValidTeam reports whether s names one of the two sides of a match.
func IsValidTeam(s string) bool {
	return s == TeamA || s == TeamB
}
