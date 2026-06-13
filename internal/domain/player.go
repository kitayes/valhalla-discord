package domain

import (
	"strings"
	"unicode"
)

const (
	MaxPlayerNameLength = 50
	MinPlayerNameLength = 1
)

type Player struct {
	ID   int
	Name string
}

func NewPlayer(id int, name string) (*Player, error) {
	normalized, err := NormalizePlayerName(name)
	if err != nil {
		return nil, err
	}

	return &Player{
		ID:   id,
		Name: normalized,
	}, nil
}

func ValidatePlayerName(name string) error {
	trimmed := strings.TrimSpace(name)

	if len(trimmed) < MinPlayerNameLength {
		return ErrPlayerNameEmpty
	}

	if len(trimmed) > MaxPlayerNameLength {
		return ErrPlayerNameTooLong
	}

	return nil
}

func NormalizePlayerName(name string) (string, error) {
	if err := ValidatePlayerName(name); err != nil {
		return "", err
	}

	name = strings.TrimSpace(name)
	name = strings.ToLower(name)

	var result strings.Builder
	result.Grow(len(name))

	for _, r := range name {
		if (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == ' ' ||
			unicode.IsLetter(r) {
			result.WriteRune(r)
		}
	}

	normalized := strings.TrimSpace(result.String())
	if len(normalized) == 0 {
		return "", ErrInvalidPlayerName
	}

	return normalized, nil
}

type PlayerStats struct {
	Player  Player
	Matches int
	Wins    int
	Losses  int
	Kills   int
	Deaths  int
	Assists int
}

func (ps *PlayerStats) WinRate() float64 {
	if ps.Matches == 0 {
		return 0.0
	}
	return float64(ps.Wins) / float64(ps.Matches) * 100.0
}

func (ps *PlayerStats) KDA() float64 {
	deaths := ps.Deaths
	if deaths == 0 {
		deaths = 1
	}
	return float64(ps.Kills+ps.Assists) / float64(deaths)
}

func (ps *PlayerStats) Validate() error {
	if ps.Kills < 0 || ps.Deaths < 0 || ps.Assists < 0 {
		return ErrInvalidKDAValues
	}
	if ps.Matches < 0 || ps.Wins < 0 || ps.Losses < 0 {
		return ErrInvalidMatchData
	}
	if ps.Wins+ps.Losses > ps.Matches {
		return ErrInvalidMatchData
	}
	return nil
}
