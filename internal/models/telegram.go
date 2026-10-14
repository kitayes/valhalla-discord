package models

import "time"

const (
	StateIdle              = ""
	StateWaitingTeamName   = "waiting_team_name"
	StateWaitingReport     = "waiting_report" // legacy report state
	StateReportOpponent    = "report_opponent"
	StateReportScore       = "report_score"
	StateReportScreenshots = "report_screenshots"

	StateSoloLine    = "solo_line"
	StateSoloRole    = "solo_role"
	StateSoloConfirm = "solo_confirm"

	StateProfileLine = "profile_line"
	StateProfileRole = "profile_role"

	StateTeamConfirm = "team_confirm"
	// Slot-bearing team states are "<prefix><slot>", e.g. "team_line_3".
	StateTeamLinePrefix     = "team_line_"
	StateTeamRolePrefix     = "team_role_"
	StateTeamFixPrefix      = "team_fix_"
	StateTeamFixRolePrefix  = "team_fixrole_"
	StateTeamEditPrefix     = "team_edit_"
	StateTeamEditRolePrefix = "team_editrole_"
)

// Team statuses. A disqualified team stays in the database so the list of
// who is out is a query, not a chat message, and an admin can put it back.
const (
	TeamStatusActive       = "active"
	TeamStatusDisqualified = "disqualified"
)

// Tournament statuses.
const (
	TournamentStatusDraft        = "draft"
	TournamentStatusRegistration = "registration"
	TournamentStatusActive       = "active"
	TournamentStatusCompleted    = "completed"
	TournamentStatusArchived     = "archived"

	TournamentTypeSingleElimination = "single elimination"
	TournamentTypeDoubleElimination = "double elimination"
	TournamentTypeRoundRobin        = "round robin"
	TournamentTypeSwiss             = "swiss"

	SeedingTypeStars  = "stars"
	SeedingTypeRandom = "random"
)

// Tournament stages. A qualifier stops once AdvanceCount teams are left and
// they move on to the main stage, which only qualified and invited teams
// enter. Standalone is a plain tournament played to the final.
const (
	StageStandalone = "standalone"
	StageQualifier  = "qualifier"
	StageMain       = "main"
)

// Tournament entry statuses and sources.
const (
	EntryRegistered = "registered"
	EntryCheckedIn  = "checked_in"
	EntryQualified  = "qualified" // advanced out of a qualifier
	EntryNoShow     = "no_show"   // missed check-in, left out of the bracket

	SourceRegistered = "registered" // the captain entered the team
	SourceQualified  = "qualified"  // carried over from a qualifier
	SourceInvited    = "invited"    // added by an admin
)

// Default best-of formats for a tournament's rounds.
const (
	DefaultFormatRounds    = 1
	DefaultFormatSemifinal = 3
	DefaultFormatFinal     = 3
)

type TelegramTournament struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Status         string `json:"status"`
	TournamentType string `json:"tournament_type"`
	HoldThirdPlace bool   `json:"hold_third_place"`
	SeedingType    string `json:"seeding_type"`
	Stage          string `json:"stage"`
	AdvanceCount   int    `json:"advance_count"`
	// Best-of per round kind: 1, 3 or 5. In a qualifier FormatFinal applies
	// to the deciding round, the one that sends teams to the main stage.
	FormatRounds    int `json:"format_rounds"`
	FormatSemifinal int `json:"format_semifinal"`
	FormatFinal     int `json:"format_final"`
	// BracketSize is the Challonge bracket size, a power of two, set at build.
	BracketSize    int        `json:"bracket_size"`
	TournamentTime *time.Time `json:"tournament_time,omitempty"`
	ChallongeID    *int64     `json:"challonge_id,omitempty"`
	ChallongeURL   string     `json:"challonge_url"`
	ChallongeFor   *time.Time `json:"challonge_for,omitempty"`
	WinnerTeamID   *int       `json:"winner_team_id,omitempty"`
	WinnerTeamName string     `json:"winner_team_name,omitempty"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Qualifier reports whether the tournament stops at AdvanceCount teams.
func (t *TelegramTournament) Qualifier() bool {
	return t != nil && t.Stage == StageQualifier && t.AdvanceCount > 0
}

// ApplyDefaults fills zero-valued settings of a tournament row.
func (t *TelegramTournament) ApplyDefaults() {
	if t.TournamentType == "" {
		t.TournamentType = TournamentTypeSingleElimination
	}
	if t.SeedingType == "" {
		t.SeedingType = SeedingTypeStars
	}
	if t.Stage == "" {
		t.Stage = StageStandalone
	}
	if t.FormatRounds == 0 {
		t.FormatRounds = DefaultFormatRounds
	}
	if t.FormatSemifinal == 0 {
		t.FormatSemifinal = DefaultFormatSemifinal
	}
	if t.FormatFinal == 0 {
		t.FormatFinal = DefaultFormatFinal
	}
}

type TournamentTeam struct {
	ID                     int    `json:"id"`
	TournamentID           int    `json:"tournament_id"`
	TeamID                 int    `json:"team_id"`
	TeamName               string `json:"team_name,omitempty"`
	IsCheckedIn            bool   `json:"is_checked_in"`
	Status                 string `json:"status"`
	ChallongeParticipantID *int64 `json:"challonge_participant_id,omitempty"`
	Seed                   *int   `json:"seed,omitempty"`
	Placement              *int   `json:"placement,omitempty"`
	Points                 int    `json:"points"`
	Source                 string `json:"source"`
	QualifiedFrom          *int   `json:"qualified_from,omitempty"`
	// CheckedInAt/By record the last check-in change: when, and the telegram
	// id of whoever made it (0 for an admin).
	CheckedInAt *time.Time `json:"checked_in_at,omitempty"`
	CheckedInBy *int64     `json:"checked_in_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type TelegramTeam struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	IsCheckedIn bool   `json:"is_checked_in"`
	Status      string `json:"status"`
	// ChallongeParticipantID is the team's id in the Challonge bracket, nil
	// until a bracket is built.
	ChallongeParticipantID *int64           `json:"challonge_participant_id"`
	Players                []TelegramPlayer `json:"players"`
}

type TelegramPlayer struct {
	ID               int    `json:"id"`
	TelegramID       *int64 `json:"telegram_id"`
	TelegramUsername string `json:"telegram_username"`
	FirstName        string `json:"first_name"`
	GameNickname     string `json:"game_nickname"`
	GameID           string `json:"game_id"`
	ZoneID           string `json:"zone_id"`
	Stars            int    `json:"stars"`
	MainRole         string `json:"main_role"`
	IsCaptain        bool   `json:"is_captain"`
	IsSubstitute     bool   `json:"is_substitute"`
	FSMState         string `json:"fsm_state"`
	TeamID           *int   `json:"team_id"`
}

type TelegramMatchReport struct {
	ID                 int      `json:"id"`
	ReporterTelegramID int64    `json:"reporter_telegram_id"`
	WinnerTeamID       int      `json:"winner_team_id"`
	WinnerTeamName     string   `json:"winner_team_name,omitempty"`
	LoserTeamID        int      `json:"loser_team_id"`
	LoserTeamName      string   `json:"loser_team_name,omitempty"`
	Score              string   `json:"score"`
	PhotoFileIDs       []string `json:"photo_file_ids"`
	// BracketMatchID links the report to the cached bracket match; nil for
	// reports made without a bracket.
	BracketMatchID *int `json:"bracket_match_id"`
	// SyncedAt is when the result reached Challonge; nil means still queued.
	SyncedAt  *time.Time `json:"synced_at"`
	CreatedAt time.Time  `json:"created_at"`
	// Status is one of the Report* constants. A report filed from the mini
	// app starts pending and moves the bracket only once the other captain
	// confirms it or ExpiresAt passes.
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Match report statuses.
const (
	ReportPending       = "pending"        // waiting for the opposing captain
	ReportConfirmed     = "confirmed"      // opposing captain agreed (or legacy direct report)
	ReportAutoConfirmed = "auto_confirmed" // confirmation window passed
	ReportDisputed      = "disputed"       // opposing captain objected; referee decides
	ReportOverridden    = "overridden"     // closed by a referee's decision
)

// Open reports whether the report still awaits an outcome.
func (r TelegramMatchReport) Open() bool {
	return r.Status == ReportPending || r.Status == ReportDisputed
}

// Game statuses within a series.
const (
	GamePending   = "pending"   // waiting for the other captain
	GameConfirmed = "confirmed" // counts toward the series
	GameDisputed  = "disputed"  // the other captain objected; a referee decides
	GameAnnulled  = "annulled"  // struck out by a referee
)

// TelegramMatchGame is one game of a best-of series, reported in the bot with
// screenshots.
type TelegramMatchGame struct {
	ID                 int        `json:"id"`
	BracketMatchID     int        `json:"bracket_match_id"`
	MatchRevision      int64      `json:"match_revision"`
	GameNo             int        `json:"game_no"`
	WinnerTeamID       int        `json:"winner_team_id"`
	ReporterTelegramID int64      `json:"-"`
	PhotoFileIDs       []string   `json:"-"`
	Status             string     `json:"status"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
}

// Bracket match states, as Challonge reports them.
const (
	BracketPending  = "pending" // waiting for an earlier match
	BracketOpen     = "open"    // both sides known, not played
	BracketComplete = "complete"
)

// BracketMatch is one cached Challonge match. Team ids are nil while the
// slot is not decided yet.
type BracketMatch struct {
	ID               int    `json:"id"`
	TournamentID     *int   `json:"tournament_id,omitempty"`
	ChallongeMatchID int64  `json:"challonge_match_id"`
	Round            int    `json:"round"`
	PlayOrder        int    `json:"play_order"`
	Team1ID          *int   `json:"team1_id"`
	Team2ID          *int   `json:"team2_id"`
	WinnerID         *int   `json:"winner_id"`
	Team1Name        string `json:"team1_name,omitempty"`
	Team2Name        string `json:"team2_name,omitempty"`
	State            string `json:"state"`
	ScoresCSV        string `json:"scores_csv"`
	BothNotified     bool   `json:"both_notified"`
	// Revision is the row's desk_revision: it moves whenever the pair or the
	// state changes, so a reopened match starts a fresh series.
	Revision int64 `json:"revision"`
}

// Has reports whether the team plays in this match.
func (m BracketMatch) Has(teamID int) bool {
	return (m.Team1ID != nil && *m.Team1ID == teamID) || (m.Team2ID != nil && *m.Team2ID == teamID)
}

// Opponent returns the other side for teamID, or nil if unknown.
func (m BracketMatch) Opponent(teamID int) *int {
	if m.Team1ID != nil && *m.Team1ID == teamID {
		return m.Team2ID
	}
	if m.Team2ID != nil && *m.Team2ID == teamID {
		return m.Team1ID
	}
	return nil
}

// Ready reports whether both sides are known and the match is unplayed.
func (m BracketMatch) Ready() bool {
	return m.State == BracketOpen && m.Team1ID != nil && m.Team2ID != nil
}

type CheckInSummary struct {
	TotalTeams        int          `json:"total_teams"`
	CheckedInCount    int          `json:"checked_in_count"`
	PendingCount      int          `json:"pending_count"`
	IncompleteCount   int          `json:"incomplete_count"`
	DisqualifiedCount int          `json:"disqualified_count"`
	Debtors           []DebtorTeam `json:"debtors"`
}

type DebtorTeam struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	CaptainName       string `json:"captain_name"`
	CaptainUsername   string `json:"captain_username"`
	CaptainTelegramID *int64 `json:"captain_telegram_id,omitempty"`
	PlayersCount      int    `json:"players_count"`
	Status            string `json:"status"` // "pending", "incomplete", "disqualified"
}
