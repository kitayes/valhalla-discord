package domain

import "math"

const (
	BaseMMR     = 1000
	KFactorBase = 32.0
	KFactorHigh = 48.0
	KFactorLow  = 24.0
)

// CalculateEloShift computes the MMR change for both teams based on the Elo rating system.
// Team A and Team B are average MMR arrays. winner is either "Team A" or "Team B".
// Returns the delta: positive for the winning team (to be added), negative for the losing team.
func CalculateEloShift(avgMMRTeamA, avgMMRTeamB float64, winner string) float64 {
	expectedA := 1.0 / (1.0 + math.Pow(10, (avgMMRTeamB-avgMMRTeamA)/400.0))

	var actualA float64
	switch winner {
	case "Team A":
		actualA = 1.0
	case "Team B":
		actualA = 0.0
	default:
		return 0
	}

	kFactor := selectKFactor(avgMMRTeamA)
	delta := kFactor * (actualA - expectedA)

	// Return the shift for Team A. Team B gets -delta.
	return math.Round(delta)
}

// selectKFactor returns the K-factor based on average MMR.
// Higher K for lower ranks (faster climb), lower K for higher ranks (stability).
func selectKFactor(mmr float64) float64 {
	if mmr >= 2200 {
		return KFactorLow
	}
	if mmr >= 1800 {
		return KFactorBase
	}
	return KFactorHigh
}

// CalculateAverageMMR returns the arithmetic mean of a slice of MMR values.
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

// ClampMMR ensures MMR doesn't fall below a floor or exceed a ceiling.
func ClampMMR(mmr int) int {
	if mmr < 0 {
		return 0
	}
	if mmr > 5000 {
		return 5000
	}
	return mmr
}
