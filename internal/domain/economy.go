package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Currency display branding.
	CurrencyName   = "ValCoins"
	CurrencySymbol = "🪙"

	// Match coin rewards (idempotent per match).
	MatchWinCoins  = 40
	MatchLossCoins = 15
	MatchMVPCoins  = 25
	MatchSVPCoins  = 15

	// Match XP rewards (awarded once per finished lobby match alongside match coins).
	MatchWinXP  = 60
	MatchLossXP = 40
	MatchMVPXP  = 25
	MatchSVPXP  = 20

	// Daily reward schedule (25..60 ValCoins/day).
	DailyBaseCoins      = 25
	DailyStreakStep     = 5
	DailyMaxStreakBonus = 35
	DailyCooldown       = 20 * time.Hour
	DailyStreakWindow   = 48 * time.Hour

	// Tip rules (anti-inflation burn).
	TipMinAmount        = 5
	TipMaxAmount        = 10000
	TipBurnPercent      = 5
	TipConfirmThreshold = 100

	// Bet insurance refund percentage on losing bet.
	BetInsuranceRefundPercent = 50

	// Chat XP settings (Noctaly-style responsive 15s cooldown + daily XP cap).
	// Message counts always accumulate even after the daily XP cap is reached.
	ChatXPBase      = 5
	ChatXPLongBonus = 3
	ChatXPLongRunes = 40
	ChatXPMinRunes  = 3
	ChatXPCooldown  = 15 * time.Second
	ChatDailyXPCap  = 450

	// Voice XP settings (awarded every minute when >= VoiceMinUsers non-deafened humans are in channel).
	// Voice duration always accumulates even after the daily XP cap is reached.
	VoiceXPPerMinUnmuted = 6
	VoiceXPPerMinMuted   = 3
	VoiceMinUsers        = 2
	VoiceTickInterval    = 1 * time.Minute
	VoiceDailyXPCap      = 720

	// Profile title constraints.
	MaxProfileTitleRunes = 40
)

// Ledger reasons stored in currency_ledger.reason.
const (
	LedgerReasonMatchReward  = "match_reward"
	LedgerReasonLevelUp      = "level_up"
	LedgerReasonDaily        = "daily"
	LedgerReasonBetPlace     = "bet_place"
	LedgerReasonBetCancel    = "bet_cancel"
	LedgerReasonBetWin       = "bet_win"
	LedgerReasonBetRefund    = "bet_refund"
	LedgerReasonBetInsurance = "bet_insurance"
	LedgerReasonMatchRevert  = "match_revert"
	LedgerReasonTipOut       = "tip_out"
	LedgerReasonTipIn        = "tip_in"
	LedgerReasonShopBuy      = "shop_buy"
	LedgerReasonOrderRefund  = "order_refund"
	LedgerReasonAdminGrant   = "admin_grant"
	LedgerReasonQuestReward  = "quest_reward"
	LedgerReasonSeasonReward = "season_reward"
)

// Shop item kinds stored in shop_items.kind.
const (
	ShopKindTitle        = "title"
	ShopKindProfileColor = "profile_color"
	ShopKindDiscordRole  = "discord_role"
	ShopKindBetInsurance = "bet_insurance"
	ShopKindPrize        = "prize"
)

// Shop order statuses stored in shop_orders.status.
const (
	ShopOrderPending   = "pending"
	ShopOrderFulfilled = "fulfilled"
	ShopOrderCancelled = "cancelled"
)

// IsValidShopKind reports whether kind is one of the supported shop item categories.
func IsValidShopKind(kind string) bool {
	switch kind {
	case ShopKindTitle, ShopKindProfileColor, ShopKindDiscordRole, ShopKindBetInsurance, ShopKindPrize:
		return true
	default:
		return false
	}
}

// XPForNextLevel returns the XP required to advance from level to level+1.
// Level 1 -> 2 costs 150 XP, then grows smoothly with a quadratic tail.
func XPForNextLevel(level int) int64 {
	if level < 1 {
		level = 1
	}
	n := int64(level - 1)
	return 150 + 100*n + 15*n*n
}

// TotalXPForLevel returns the cumulative XP needed to reach the given level.
func TotalXPForLevel(level int) int64 {
	if level <= 1 {
		return 0
	}
	var total int64
	for l := 1; l < level; l++ {
		total += XPForNextLevel(l)
	}
	return total
}

// LevelFromXP converts total XP into the current level, XP earned inside this level,
// and XP required to reach the next level.
func LevelFromXP(xp int64) (level int, currentInLevel int64, neededForNext int64) {
	if xp < 0 {
		xp = 0
	}
	level = 1
	rem := xp
	for {
		need := XPForNextLevel(level)
		if rem < need {
			return level, rem, need
		}
		rem -= need
		level++
	}
}

// LevelUpCoinReward returns the ValCoins earned upon reaching targetLevel (>= 2).
// Every 5th level grants an additional +75 milestone bonus.
func LevelUpCoinReward(reachedLevel int) int {
	if reachedLevel < 2 {
		return 0
	}
	base := 40 + (reachedLevel-1)*10
	if base > 200 {
		base = 200
	}
	if reachedLevel%5 == 0 {
		base += 75
	}
	return base
}

// DailyRewardForStreak returns the daily ValCoins reward for the given 1-based streak.
func DailyRewardForStreak(streak int) int {
	if streak < 1 {
		streak = 1
	}
	bonus := (streak - 1) * DailyStreakStep
	if bonus > DailyMaxStreakBonus {
		bonus = DailyMaxStreakBonus
	}
	return DailyBaseCoins + bonus
}

// CalculateTipBurn returns the burned coin fee for a player-to-player tip.
func CalculateTipBurn(amount int) int {
	if amount <= 0 {
		return 0
	}
	return (amount * TipBurnPercent) / 100
}

// BetInsuranceRefund returns the coins returned for a lost insured bet.
func BetInsuranceRefund(stake int) int {
	if stake <= 0 {
		return 0
	}
	refund := (stake * BetInsuranceRefundPercent) / 100
	if refund < 1 {
		return 1
	}
	return refund
}

// ChatXPForMessage returns how much XP a message of the given rune length earns.
func ChatXPForMessage(runes int) int64 {
	if runes < ChatXPMinRunes {
		return 0
	}
	xp := int64(ChatXPBase)
	if runes >= ChatXPLongRunes {
		xp += ChatXPLongBonus
	}
	return xp
}

// FormatCoins renders a coin amount with the currency symbol.
func FormatCoins(amount int) string {
	return fmt.Sprintf("%d %s", amount, CurrencySymbol)
}

// FormatProgressBar renders an ASCII/Unicode progress bar of width blocks.
func FormatProgressBar(current, needed int64, width int) string {
	if width <= 0 {
		width = 10
	}
	if needed <= 0 {
		return strings.Repeat("█", width) + " 100%"
	}
	if current < 0 {
		current = 0
	}
	if current > needed {
		current = needed
	}
	filled := int((current * int64(width)) / needed)
	if filled > width {
		filled = width
	}
	pct := int((current * 100) / needed)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + fmt.Sprintf(" %d%%", pct)
}

// FormatVoiceDuration renders voice seconds as a concise Russian duration ("12ч 35м" or "42м").
func FormatVoiceDuration(seconds int64) string {
	if seconds < 60 {
		return "0м"
	}
	mins := seconds / 60
	hours := mins / 60
	remMins := mins % 60
	if hours == 0 {
		return fmt.Sprintf("%dм", remMins)
	}
	return fmt.Sprintf("%dч %dм", hours, remMins)
}

// ParseHexColor parses "#RRGGBB" or "RRGGBB" into a positive 24-bit Discord embed color.
func ParseHexColor(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "#")
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) != 6 {
		return 0, fmt.Errorf("%w: expected 6 hex digits", ErrInvalidProfileColor)
	}
	v, err := strconv.ParseInt(s, 16, 32)
	if err != nil || v <= 0 || v > 0xFFFFFF {
		return 0, fmt.Errorf("%w: %q", ErrInvalidProfileColor, raw)
	}
	return int(v), nil
}

// SanitizeProfileTitle validates and cleans a player's custom profile title.
func SanitizeProfileTitle(raw string) (string, error) {
	var b strings.Builder
	for _, r := range raw {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
	lower := strings.ToLower(clean)
	if strings.Contains(lower, "@everyone") || strings.Contains(lower, "@here") || strings.Contains(clean, "<@") {
		return "", fmt.Errorf("%w: mentions are not allowed", ErrInvalidProfileTitle)
	}
	runes := utf8.RuneCountInString(clean)
	if runes == 0 || runes > MaxProfileTitleRunes {
		return "", fmt.Errorf("%w: length must be 1..%d characters", ErrInvalidProfileTitle, MaxProfileTitleRunes)
	}
	return clean, nil
}
