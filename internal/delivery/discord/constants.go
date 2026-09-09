package discord

import "time"

// bettingWindow is how long bets stay open after a match goes live.
const bettingWindow = 5 * time.Minute

// bettingSweepInterval is how often expired betting windows are reconciled in
// the database, independently of the per-match timer goroutines.
const bettingSweepInterval = time.Minute

// lobbySweepInterval is how often idle players are warned or removed and
// expired queue bans are dropped. It only needs to be well under
// inactivityGrace for the warning to land before the removal does.
const lobbySweepInterval = 30 * time.Second

const (
	// teamSize is how many players make up one side of a match.
	teamSize = 5

	topPlayersLimit      = 10
	maxMessageLength     = 2000
	maxMessageTruncation = 1990

	// Screenshot upload budget: 5 in a row per user refilling at 1/second, with
	// a 30-upload burst for the whole guild refilling at 5/second.
	uploadBurstPerUser  = 5
	uploadRefillPerUser = 1
	uploadBurstGlobal   = 30
	uploadRefillGlobal  = 5

	// FAQ auto-answers cost a DeepSeek call each. A user may ask 3 in a row and
	// then one every 30 seconds; the whole guild shares a 20-answer burst.
	faqBurstPerUser  = 3
	faqRefillPerUser = 1.0 / 30.0
	faqBurstGlobal   = 20
	faqRefillGlobal  = 1

	winRateExcellent = 75.0
	winRateGood      = 60.0
	winRatePoor      = 40.0

	colorGold         = 0xFFD700
	colorGreen        = 0x2ECC71
	colorPurple       = 0x9B59B6
	colorRed          = 0xE74C3C
	colorGray         = 0x95A5A6
	colorBlue         = 0x3498DB
	colorTelegramBlue = 0x0088CC
)
