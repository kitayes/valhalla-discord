package application

import (
	"context"
	"fmt"
	"strconv"

	"blackwatch/internal/models"
	"blackwatch/pkg/sheets"
)

const (
	// teamsTab and matchesTab are separate because a roster row and a match
	// row are different things: one per player versus one per game played.
	teamsTab   = "Teams"
	matchesTab = "Matches"
	// tabClearRange is wide enough for both layouts and is always qualified
	// by a tab name, so an export never reaches the statistics sheet.
	tabClearRange = "!A1:Z2000"
	tabStartCell  = "!A1"

	// minRosterForExport is a full main lineup. A team below it never played,
	// so it would only add noise to a tournament report.
	minRosterForExport = 5
)

// WithSheets enables /export_sheet. Like the other With* options it is
// optional: without credentials or a target spreadsheet the bot runs with the
// command reporting that it is not configured.
func (s *TelegramServiceImpl) WithSheets(client sheets.Client, spreadsheetID string) *TelegramServiceImpl {
	s.sheets = client
	s.spreadsheetID = spreadsheetID
	return s
}

// teamOutcome is what the bracket says happened to one team.
type teamOutcome struct {
	wins         int
	losses       int
	stage        string
	eliminatedBy string
}

// outcomeFor reads a team's fate out of the cached bracket. An empty stage
// means there is nothing to say yet — no bracket has been built.
func outcomeFor(team models.TelegramTeam, ms []models.BracketMatch) teamOutcome {
	out := teamOutcome{}
	total := TotalRounds(ms)
	for _, m := range ms {
		if !m.Has(team.ID) || m.State != models.BracketComplete {
			continue
		}
		// A bye is a completed match with one empty slot. It advances a team
		// without being a win over anybody.
		if m.Team1ID == nil || m.Team2ID == nil {
			continue
		}
		if m.WinnerID != nil && *m.WinnerID == team.ID {
			out.wins++
		} else if m.WinnerID != nil {
			out.losses++
		}
	}

	// A technical defeat takes a team out of the tournament without a match,
	// so it outranks whatever the bracket shows.
	if team.Status == models.TeamStatusDisqualified {
		out.stage = "Дисквалифицирована"
		return out
	}

	elim := eliminationMatch(ms, team.ID)
	switch {
	case len(ms) == 0:
		out.stage = ""
	case elim != nil:
		out.stage = StageLabel(elim.Round, total)
		out.eliminatedBy = opponentName(*elim, team.ID)
	case wonFinal(ms, team.ID, total):
		out.stage = "Победитель"
	case !inBracket(ms, team.ID):
		out.stage = "Не в сетке"
	default:
		out.stage = "В игре"
	}
	return out
}

func inBracket(ms []models.BracketMatch, teamID int) bool {
	for _, m := range ms {
		if m.Has(teamID) {
			return true
		}
	}
	return false
}

func wonFinal(ms []models.BracketMatch, teamID, total int) bool {
	for _, m := range ms {
		if m.Round == total && m.State == models.BracketComplete && m.WinnerID != nil && *m.WinnerID == teamID {
			return true
		}
	}
	return false
}

// opponentName is the other side's name, empty when that slot is still open.
func opponentName(m models.BracketMatch, teamID int) string {
	if m.Opponent(teamID) == nil {
		return ""
	}
	if m.Team1ID != nil && *m.Team1ID == teamID {
		return m.Team2Name
	}
	return m.Team1Name
}

// matchResult describes a match from one team's point of view.
func matchResult(m models.BracketMatch, teamID int) string {
	switch {
	case m.State == models.BracketComplete && (m.Team1ID == nil || m.Team2ID == nil):
		return "автопроход"
	case m.State == models.BracketComplete && m.WinnerID != nil && *m.WinnerID == teamID:
		return "победа"
	case m.State == models.BracketComplete:
		return "поражение"
	case m.Ready():
		return "идёт"
	default:
		return "ожидает"
	}
}

// exportableTeams keeps the teams a tournament report is about: those that
// registered a full main lineup.
func exportableTeams(teams []models.TelegramTeam) []models.TelegramTeam {
	var out []models.TelegramTeam
	for _, t := range teams {
		if len(t.Players) >= minRosterForExport {
			out = append(out, t)
		}
	}
	return out
}

// teamsSheetRows is one row per player. The team columns repeat on every row
// of a roster, which is what makes the sheet pivot- and filter-friendly.
//
// In-game id and zone are deliberately absent: the target spreadsheet can be
// shared far more widely than the admin-only CSV, and a nickname is enough to
// identify a player in a report.
func teamsSheetRows(teams []models.TelegramTeam, ms []models.BracketMatch) [][]interface{} {
	rows := [][]interface{}{{
		"Team", "Status", "CheckIn", "Players", "W", "L", "Stage", "Eliminated by",
		"Nick", "Role", "Captain", "Sub",
	}}
	for _, t := range teams {
		o := outcomeFor(t, ms)
		for _, p := range t.Players {
			rows = append(rows, []interface{}{
				t.Name,
				teamStatusLabel(t.Status),
				strconv.FormatBool(t.IsCheckedIn),
				len(t.Players),
				o.wins,
				o.losses,
				o.stage,
				o.eliminatedBy,
				p.GameNickname,
				p.MainRole,
				strconv.FormatBool(p.IsCaptain),
				strconv.FormatBool(p.IsSubstitute),
			})
		}
	}
	return rows
}

// matchesSheetRows is one row per team per match, so every game appears twice
// — once from each side. Filtering the sheet by a team then yields that
// team's entire run through the bracket.
func matchesSheetRows(teams []models.TelegramTeam, ms []models.BracketMatch) [][]interface{} {
	rows := [][]interface{}{{"Team", "Round", "Stage", "Opponent", "Score", "Result"}}
	total := TotalRounds(ms)
	for _, t := range teams {
		for _, m := range ms {
			if !m.Has(t.ID) {
				continue
			}
			opponent := opponentName(m, t.ID)
			if opponent == "" {
				opponent = "—"
			}
			rows = append(rows, []interface{}{
				t.Name,
				m.Round,
				StageLabel(m.Round, total),
				opponent,
				m.ScoresCSV,
				matchResult(m, t.ID),
			})
		}
	}
	return rows
}

// ExportTeamsToSheet publishes rosters and bracket runs to the configured
// spreadsheet and returns its URL. It reads the local bracket cache, so it
// costs no Challonge API requests and shows the bracket as of the last sync.
func (s *TelegramServiceImpl) ExportTeamsToSheet(ctx context.Context) (string, error) {
	if s.sheets == nil || s.spreadsheetID == "" {
		return "", fmt.Errorf("google sheets не настроен: проверьте GOOGLE_CREDENTIALS_PATH и GOOGLE_SHEET_ID")
	}

	allTeams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load teams: %w", err)
	}
	// A missing bracket is normal before the tournament starts; the rosters
	// are still worth exporting, just without the stage columns filled in.
	matches, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load bracket: %w", err)
	}

	teams := exportableTeams(allTeams)
	tabs := []struct {
		title string
		rows  [][]interface{}
	}{
		{teamsTab, teamsSheetRows(teams, matches)},
		{matchesTab, matchesSheetRows(teams, matches)},
	}

	for _, tab := range tabs {
		if err := s.sheets.EnsureSheet(s.spreadsheetID, tab.title); err != nil {
			return "", fmt.Errorf("failed to prepare %s tab: %w", tab.title, err)
		}
		if err := s.sheets.ClearRange(s.spreadsheetID, tab.title+tabClearRange); err != nil {
			return "", fmt.Errorf("failed to clear %s tab: %w", tab.title, err)
		}
		if err := s.sheets.UpdateValues(s.spreadsheetID, tab.title+tabStartCell, tab.rows); err != nil {
			return "", fmt.Errorf("failed to write %s tab: %w", tab.title, err)
		}
	}

	return fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", s.spreadsheetID), nil
}
