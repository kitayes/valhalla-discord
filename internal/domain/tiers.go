package domain

import "fmt"

// Tier represents the competitive rank tier of a player.
type Tier string

const (
	TierObsidian Tier = "OBSIDIAN" // S-Tier: 2200+
	TierOnyx     Tier = "ONYX"     // A-Tier: 1800-2199
	TierCarbon   Tier = "CARBON"   // B-Tier: 1400-1799
	TierGraphite Tier = "GRAPHITE" // C-Tier: 1000-1399
	TierUnranked Tier = "UNRANKED" // Below 1000
)

// Role names for Discord tier roles.
var TierRoleNames = map[Tier]string{
	TierObsidian: "OBSIDIAN",
	TierOnyx:     "ONYX",
	TierCarbon:   "CARBON",
	TierGraphite: "GRAPHITE",
}

// DetermineTier returns the tier for a given MMR value.
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

// FormatTierDisplay returns a styled string representation of a tier for embeds.
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

// FormatTierWithMMR returns a combined display with MMR.
func FormatTierWithMMR(mmr int) string {
	tier := DetermineTier(mmr)
	return fmt.Sprintf("%s (%d MMR)", FormatTierDisplay(tier), mmr)
}
