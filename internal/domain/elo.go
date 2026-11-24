package domain

import "math"

const (
	BaseMMR     = 1000
	KFactorBase = 32.0
	KFactorHigh = 48.0
	KFactorLow  = 24.0

	MinWinMMRDelta  = 3
	MaxLossMMRDelta = -3

	MinKDAModCap  = 4
	MaxKDAModCap  = 8
	KDAModRatio   = 0.35
	KDAScaleFloor = 0.8
	KDAWeightKP   = 1.5

	MVPBonusMMR      = 3
	SVPMitigationMMR = 4
)

// PlayerKDA holds a player's post-match kill/death/assist counts for MMR weighting.
type PlayerKDA struct {
	PlayerID int
	Kills    int
	Deaths   int
	Assists  int
}

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

// CalculateTeamKDAModifiers computes zero-sum MMR modifiers for one team from
// each player's KDA and kill participation, capped relative to baseDelta.
func CalculateTeamKDAModifiers(team []PlayerKDA, baseDelta int) map[int]int {
	mods := make(map[int]int, len(team))
	if len(team) == 0 || baseDelta == 0 {
		return mods
	}

	teamKills := 0
	clamped := make([]PlayerKDA, len(team))
	for i, p := range team {
		k := max(p.Kills, 0)
		d := max(p.Deaths, 0)
		a := max(p.Assists, 0)
		clamped[i] = PlayerKDA{PlayerID: p.PlayerID, Kills: k, Deaths: d, Assists: a}
		teamKills += k
	}
	denom := float64(max(teamKills, 1))

	perf := make([]float64, len(clamped))
	var sumPerf float64
	for i, p := range clamped {
		effKDA := (float64(p.Kills+p.Assists) + 0.5) / float64(p.Deaths+1)
		kp := math.Min(float64(p.Kills+p.Assists)/denom, 1.0)
		perf[i] = math.Log(1.0+effKDA) + KDAWeightKP*kp
		sumPerf += perf[i]
	}

	mean := sumPerf / float64(len(clamped))
	diff := make([]float64, len(clamped))
	maxAbs := 0.0
	for i, v := range perf {
		diff[i] = v - mean
		if abs := math.Abs(diff[i]); abs > maxAbs {
			maxAbs = abs
		}
	}

	scale := math.Max(maxAbs, KDAScaleFloor)
	maxMod := clampInt(int(math.Round(KDAModRatio*math.Abs(float64(baseDelta)))), MinKDAModCap, MaxKDAModCap)

	raw := make([]float64, len(clamped))
	rounded := make([]int, len(clamped))
	sumMod := 0
	for i, d := range diff {
		raw[i] = float64(maxMod) * (d / scale)
		rounded[i] = clampInt(int(math.Round(raw[i])), -maxMod, maxMod)
		sumMod += rounded[i]
	}

	if len(clamped) >= 2 {
		for sumMod != 0 {
			bestIdx := -1
			bestResidual := math.Inf(-1)
			for i := range rounded {
				if sumMod > 0 {
					if rounded[i] <= -maxMod {
						continue
					}
					// Larger (rounded - raw) means this entry was rounded up most aggressively.
					if res := float64(rounded[i]) - raw[i]; res > bestResidual {
						bestResidual = res
						bestIdx = i
					}
				} else {
					if rounded[i] >= maxMod {
						continue
					}
					// Larger (raw - rounded) means this entry was rounded down most aggressively.
					if res := raw[i] - float64(rounded[i]); res > bestResidual {
						bestResidual = res
						bestIdx = i
					}
				}
			}
			if bestIdx == -1 {
				break
			}
			if sumMod > 0 {
				rounded[bestIdx]--
				sumMod--
			} else {
				rounded[bestIdx]++
				sumMod++
			}
		}
	}

	for i, p := range clamped {
		mods[p.PlayerID] = rounded[i]
	}
	return mods
}

// ApplyMatchDelta combines base Elo shift, KDA modifier, and medal bonus while
// guaranteeing that winners always gain at least MinWinMMRDelta and losers always
// lose at least |MaxLossMMRDelta|.
func ApplyMatchDelta(baseDelta, kdaMod, medalBonus int, won bool) int {
	total := baseDelta + kdaMod + medalBonus
	if won && total < MinWinMMRDelta {
		return MinWinMMRDelta
	}
	if !won && total > MaxLossMMRDelta {
		return MaxLossMMRDelta
	}
	return total
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
