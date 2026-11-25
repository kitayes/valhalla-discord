package models

import "time"

// PlayerEconomy holds a player's currency, activity XP/level, and cosmetic settings.
type PlayerEconomy struct {
	PlayerID          int        `json:"player_id"`
	Name              string     `json:"name"`
	DiscordID         string     `json:"discord_id,omitempty"`
	Points            int        `json:"points"`
	XP                int64      `json:"xp"`
	Level             int        `json:"level"`
	VoiceSeconds      int64      `json:"voice_seconds"`
	MessagesCount     int64      `json:"messages_count"`
	LastDailyAt       *time.Time `json:"last_daily_at,omitempty"`
	DailyStreak       int        `json:"daily_streak"`
	ProfileTitle      string     `json:"profile_title"`
	ProfileColor      int        `json:"profile_color"`
	BetInsuranceCount int        `json:"bet_insurance_count"`
}

// LedgerEntry is a single currency balance movement in currency_ledger.
type LedgerEntry struct {
	ID        int       `json:"id"`
	PlayerID  int       `json:"player_id"`
	Amount    int       `json:"amount"`
	Balance   int       `json:"balance"`
	Reason    string    `json:"reason"`
	RefKey    string    `json:"ref_key,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// DailyClaimResult describes a completed /daily claim.
type DailyClaimResult struct {
	CoinsAwarded int       `json:"coins_awarded"`
	NewBalance   int       `json:"new_balance"`
	Streak       int       `json:"streak"`
	NextClaimAt  time.Time `json:"next_claim_at"`
}

// XPGrantResult reports the outcome of granting chat or voice XP to a player.
type XPGrantResult struct {
	PlayerID     int   `json:"player_id"`
	XPGained     int64 `json:"xp_gained"`
	NewXP        int64 `json:"new_xp"`
	OldLevel     int   `json:"old_level"`
	NewLevel     int   `json:"new_level"`
	CoinsAwarded int   `json:"coins_awarded"`
	NewBalance   int   `json:"new_balance"`
}

// LeveledUp reports whether the grant pushed the player to at least one higher level.
func (r XPGrantResult) LeveledUp() bool {
	return r.NewLevel > r.OldLevel
}

// RichPlayer is a row in the /rich or XP leaderboard.
type RichPlayer struct {
	PlayerID     int    `json:"player_id"`
	Name         string `json:"name"`
	DiscordID    string `json:"discord_id,omitempty"`
	Points       int    `json:"points"`
	Level        int    `json:"level"`
	XP           int64  `json:"xp"`
	VoiceSeconds int64  `json:"voice_seconds"`
	ProfileTitle string `json:"profile_title,omitempty"`
}

// TipResult summarizes a completed /tip transfer.
type TipResult struct {
	SenderID        int `json:"sender_id"`
	ReceiverID      int `json:"receiver_id"`
	AmountSent      int `json:"amount_sent"`
	AmountReceived  int `json:"amount_received"`
	Burned          int `json:"burned"`
	SenderBalance   int `json:"sender_balance"`
	ReceiverBalance int `json:"receiver_balance"`
}

// PlayerBetStats summarizes a player's betting track record.
type PlayerBetStats struct {
	TotalBets    int `json:"total_bets"`
	WonBets      int `json:"won_bets"`
	TotalWagered int `json:"total_wagered"`
	TotalPayout  int `json:"total_payout"`
}

// MatchCoinReward records the coins and XP awarded to one participant after a match finishes.
type MatchCoinReward struct {
	PlayerID int    `json:"player_id"`
	Coins    int    `json:"coins"`
	XPGained int64  `json:"xp_gained"`
	Won      bool   `json:"won"`
	IsMVP    bool   `json:"is_mvp"`
	IsSVP    bool   `json:"is_svp"`
	Skipped  bool   `json:"skipped"` // true if already awarded earlier (idempotent replay)
	Balance  int    `json:"balance"`
	Note     string `json:"note"`
}

// ShopItem represents an entry in the ValCoins shop catalog.
type ShopItem struct {
	ID          int       `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Kind        string    `json:"kind"`
	Price       int       `json:"price"`
	Payload     string    `json:"payload"`
	Stock       *int      `json:"stock,omitempty"`
	Enabled     bool      `json:"enabled"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
}

// ShopOrder represents a completed or pending shop purchase.
type ShopOrder struct {
	ID          int        `json:"id"`
	PlayerID    int        `json:"player_id"`
	PlayerName  string     `json:"player_name,omitempty"`
	DiscordID   string     `json:"discord_id,omitempty"`
	ItemID      int        `json:"item_id"`
	ItemCode    string     `json:"item_code,omitempty"`
	ItemName    string     `json:"item_name"`
	ItemKind    string     `json:"item_kind"`
	ItemPayload string     `json:"item_payload,omitempty"`
	PricePaid   int        `json:"price_paid"`
	CustomInput string     `json:"custom_input,omitempty"`
	Status      string     `json:"status"`
	HandledBy   string     `json:"handled_by,omitempty"`
	NewBalance  int        `json:"new_balance"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// MatchRevertSummary reports everything undone when an admin runs /revert_match.
type MatchRevertSummary struct {
	MatchID            int         `json:"match_id"`
	OldWinner          string      `json:"old_winner"`
	RevertedMMRCount   int         `json:"reverted_mmr_count"`
	RestoredMMRs       map[int]int `json:"restored_mmrs"`
	RevertedMatchCoins int         `json:"reverted_match_coins"`
	RevertedBetsCount  int         `json:"reverted_bets_count"`
	DeletedScoreboards int         `json:"deleted_scoreboards"`
	ThreadID           string      `json:"thread_id,omitempty"`
}
