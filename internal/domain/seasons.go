package domain

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	SeasonResetModeFull = "full" // Reset every player to InitialMMR (1000).
	SeasonResetModeSoft = "soft" // Compress MMR 50% toward InitialMMR (1000).
	SeasonResetModeNone = "none" // Keep MMR unchanged, only archive stats & reset season date.

	SeasonSoftResetFactor = 0.5
	SeasonMinMMR          = 100
	MaxSeasonNameRunes    = 60

	SeasonRewardRank1 = 500
	SeasonRewardRank2 = 300
	SeasonRewardRank3 = 150
)

// IsValidSeasonResetMode reports whether mode is one of full, soft, or none.
func IsValidSeasonResetMode(mode string) bool {
	switch mode {
	case SeasonResetModeFull, SeasonResetModeSoft, SeasonResetModeNone:
		return true
	default:
		return false
	}
}

// ApplySeasonMMRReset computes a player's new MMR for the next season.
func ApplySeasonMMRReset(currentMMR int, mode string) int {
	switch mode {
	case SeasonResetModeFull:
		return BaseMMR
	case SeasonResetModeSoft:
		diff := float64(currentMMR - BaseMMR)
		next := BaseMMR + int(math.Round(diff*SeasonSoftResetFactor))
		if next < SeasonMinMMR {
			return SeasonMinMMR
		}
		return next
	case SeasonResetModeNone:
		return currentMMR
	default:
		return BaseMMR
	}
}

// SeasonPodiumReward returns the ValCoins award for finishing at the given 1-based rank.
func SeasonPodiumReward(rank int) int {
	switch rank {
	case 1:
		return SeasonRewardRank1
	case 2:
		return SeasonRewardRank2
	case 3:
		return SeasonRewardRank3
	default:
		return 0
	}
}

// ValidateSeasonName trims and validates a season display name.
func ValidateSeasonName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: season name cannot be empty", ErrInvalidSeasonName)
	}
	if utf8.RuneCountInString(name) > MaxSeasonNameRunes {
		return "", fmt.Errorf("%w: season name exceeds %d characters", ErrInvalidSeasonName, MaxSeasonNameRunes)
	}
	return name, nil
}

// FormatSeasonResetMode returns a human-readable Russian description of the reset mode.
func FormatSeasonResetMode(mode string) string {
	switch mode {
	case SeasonResetModeFull:
		return fmt.Sprintf("Полный сброс всем на %d MMR", BaseMMR)
	case SeasonResetModeSoft:
		return fmt.Sprintf("Софт-ресет 50%% к %d MMR", BaseMMR)
	case SeasonResetModeNone:
		return "Без сброса MMR"
	default:
		return mode
	}
}
