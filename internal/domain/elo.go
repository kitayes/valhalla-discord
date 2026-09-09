package domain

import "math"

const (
	BaseMMR     = 1000
	KFactorBase = 32.0
	KFactorHigh = 48.0
	KFactorLow  = 24.0
)

func CalculateEloShift(avgMMRTeamA, avgMMRTeamB float64, winner string) float64 {
	expectedA := 1.0 / (1.0 + math.Pow(10, (avgMMRTeamB-avgMMRTeamA)/400.0))

	var actualA float64
	switch winner {
	case TeamA:
		actualA = 1.0
	case TeamB:
		actualA = 0.0
	default:
		return 0
	}

	kFactor := selectKFactor(avgMMRTeamA)
	delta := kFactor * (actualA - expectedA)

	return math.Round(delta)
}

func selectKFactor(mmr float64) float64 {
	if mmr >= 2200 {
		return KFactorLow
	}
	if mmr >= 1800 {
		return KFactorBase
	}
	return KFactorHigh
}

func CalculateAverageMMR(mmrs []int) float64 {
	if len(mmrs) == 0 {
		return BaseMMR
	}
	sum := 0
	for _, m := range mmrs {
		sum += m
	}
	return float64(sum) / float64(len(mmrs))
}

func ClampMMR(mmr int) int {
	if mmr < 0 {
		return 0
	}
	if mmr > 5000 {
		return 5000
	}
	return mmr
}
