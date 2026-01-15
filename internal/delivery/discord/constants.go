package discord

const (
	// Display limits
	topPlayersLimit      = 10
	maxMessageLength     = 2000
	maxMessageTruncation = 1990

	// Win rate thresholds for color coding
	winRateExcellent = 75.0
	winRateGood      = 60.0
	winRatePoor      = 40.0

	// Embed colors
	colorGold         = 0xFFD700 // Leaderboard
	colorGreen        = 0x2ECC71 // Good win rate
	colorPurple       = 0x9B59B6 // Excellent win rate
	colorRed          = 0xE74C3C // Poor win rate
	colorGray         = 0x95A5A6 // Default/neutral
	colorBlue         = 0x3498DB // Info/history
	colorTelegramBlue = 0x0088CC // Telegram-specific

	// Guild configuration
	defaultGuildID = "1458104409677627576"
)
