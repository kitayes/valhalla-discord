package models

import "time"

// MatchDesk is persisted atomically with its notification outbox. It is local
// match operations state; Challonge remains authoritative for results.
type MatchDesk struct {
	Tournament     string
	GlobalPaused   bool
	NextGeneration int64
	NextNotice     int64
	Matches        map[int]*DeskMatch
	Outbox         []DeskNotice
}

type DeskMatch struct {
	BracketRevision int64
	ID              int
	Number          int
	Generation      int64
	Teams           [2]int
	Names           [2]string
	Active          bool
	Ready           [2]bool
	NotBefore       time.Time
	Deadline        time.Time
	StartedAt       time.Time
	LocalPaused     bool
	GlobalPaused    bool
	PausedAt        time.Time
	Issues          [2]string
	Revision        int64
	CardRevision    [2]int64
	CaptainKeys     [2]string
	AlertKey        string
	// AlertSince is when the current problems were first alerted; an alert
	// still unresolved after a while is sent once more (AlertRepeated).
	AlertSince    time.Time
	AlertRepeated bool
	// Reminded counts the "press ready" reminders sent to each side since the
	// lobby deadline was last set.
	Reminded [2]int
	History  []DeskEvent
}

type DeskEvent struct {
	At   time.Time
	Text string
}

type DeskButton struct {
	Text     string        `json:"text"`
	Data     string        `json:"callback_data,omitempty"`
	CopyText *DeskCopyText `json:"copy_text,omitempty"`
	WebApp   *DeskWebApp   `json:"web_app,omitempty"`
}

type DeskCopyText struct {
	Text string `json:"text"`
}

type DeskWebApp struct {
	URL string `json:"url"`
}

type DeskNotice struct {
	Tournament string
	ID         int64
	MatchID    int
	Generation int64
	Revision   int64
	ChatID     int64
	Kind       string
	AlertKey   string
	Text       string
	Buttons    [][]DeskButton
}

// DeskContext is a consistent snapshot read in the store transaction.
type DeskContext struct {
	// Tournament is the active tournament the matches belong to; nil in
	// tests that do not care about formats and round names.
	Tournament *TelegramTournament
	Revisions  map[int]int64
	Matches    []BracketMatch
	Captains   map[int]TelegramPlayer
	// Members are the Telegram chats of every player per team id.
	Members map[int][]int64
	// Series is the reported games of each open match at its current
	// revision, by bracket match id; absent when no game was reported.
	Series   map[int]DeskSeries
	StartsAt time.Time
}

// DeskSeries is what the desk needs to know of a match's games.
type DeskSeries struct {
	// LastActivity is the latest game report, confirmation, dispute or
	// referee decision.
	LastActivity time.Time
	// DisputedGame is the number of a game waiting for a referee, 0 if none;
	// DisputedAt is when it was disputed.
	DisputedGame int
	DisputedAt   time.Time
}
