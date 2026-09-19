package discord

import (
	"unicode/utf8"

	"blackwatch/internal/application"
)

// truncateMessage caps a message at Discord's 2000-character limit.
//
// Discord counts characters, not bytes, and rejects invalid UTF-8 outright —
// slicing the string at a byte offset cut Cyrillic player names in half, which
// is most of the roster here.
func truncateMessage(msg string) string {
	if utf8.RuneCountInString(msg) <= maxMessageLength {
		return msg
	}

	const notice = "...\n(список обрезан)"
	limit := maxMessageLength - utf8.RuneCountInString(notice)

	count := 0
	for idx := range msg {
		if count == limit {
			return msg[:idx] + notice
		}
		count++
	}
	return msg + notice
}

func calculateWinRate(stats *application.PlayerStats) float64 {
	if stats.Matches == 0 {
		return 0.0
	}
	return (float64(stats.Wins) / float64(stats.Matches)) * 100
}

func calculateKDA(kills, deaths, assists int) float64 {
	d := deaths
	if d == 0 {
		d = 1
	}
	return float64(kills+assists) / float64(d)
}

func getColorByWinRate(winRate float64) int {
	switch {
	case winRate >= winRateExcellent:
		return colorPurple
	case winRate >= winRateGood:
		return colorGreen
	case winRate < winRatePoor:
		return colorRed
	default:
		return colorGray
	}
}

func getMedalEmoji(position int) string {
	switch position {
	case 0:
		return "[1]"
	case 1:
		return "[2]"
	case 2:
		return "[3]"
	default:
		return "[-]"
	}
}

func valueOrDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
