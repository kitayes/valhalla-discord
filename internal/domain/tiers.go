package domain

import "fmt"

// Tier represents the competitive rank tier of a player.
type Tier string

const (
	TierObsidian Tier = "OBSIDIAN"
	TierOnyx     Tier = "ONYX"
	TierCarbon   Tier = "CARBON"
	TierGraphite Tier = "GRAPHITE"
	TierUnranked Tier = "UNRANKED"
)

var TierRoleNames = map[Tier]string{
	TierObsidian: "OBSIDIAN",
	TierOnyx:     "ONYX",
	TierCarbon:   "CARBON",
	TierGraphite: "GRAPHITE",
}

func DetermineTier(mmr int) Tier {
	switch {
	case mmr >= 2200:
		return TierObsidian
	case mmr >= 1800:
		return TierOnyx
	case mmr >= 1400:
		return TierCarbon
	case mmr >= 1000:
		return TierGraphite
	default:
		return TierUnranked
	}
}

func FormatTierDisplay(t Tier) string {
	switch t {
	case TierObsidian:
		return "⚫ S-Tier: OBSIDIAN"
	case TierOnyx:
		return "⚪ A-Tier: ONYX"
	case TierCarbon:
		return "🔵 B-Tier: CARBON"
	case TierGraphite:
		return "🟢 C-Tier: GRAPHITE"
	default:
		return "⚪ Unranked"
	}
}

func FormatTierWithMMR(mmr int) string {
	tier := DetermineTier(mmr)
	return fmt.Sprintf("%s (%d MMR)", FormatTierDisplay(tier), mmr)
}
