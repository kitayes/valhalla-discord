package discord

import "valhalla/internal/application"

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
		return "🥇"
	case 1:
		return "🥈"
	case 2:
		return "🥉"
	default:
		return "▪️"
	}
}

func valueOrDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
