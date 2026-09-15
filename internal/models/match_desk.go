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
	History         []DeskEvent
}

type DeskEvent struct {
	At   time.Time
	Text string
}

type DeskButton struct {
	Text     string        `json:"text"`
	Data     string        `json:"callback_data,omitempty"`
	CopyText *DeskCopyText `json:"copy_text,omitempty"`
}

type DeskCopyText struct {
	Text string `json:"text"`
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
	Revisions map[int]int64
	Matches   []BracketMatch
	Captains  map[int]TelegramPlayer
	StartsAt  time.Time
}
