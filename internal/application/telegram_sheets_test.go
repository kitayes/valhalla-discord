package application

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"blackwatch/internal/models"
)

// fakeSheetsClient records what the export asked Google to do. Ranges matter
// as much as values here: the stats sync writes to the first tab with bare
// ranges, so an export that forgets its tab prefix silently destroys it.
type fakeSheetsClient struct {
	ensured  []string
	cleared  []string
	written  map[string][][]interface{}
	ensueErr error
}

func newFakeSheetsClient() *fakeSheetsClient {
	return &fakeSheetsClient{written: map[string][][]interface{}{}}
}

func (c *fakeSheetsClient) CreateSpreadsheet(string) (string, string, error) { return "", "", nil }
func (c *fakeSheetsClient) AddPermission(string, string, string) error       { return nil }
func (c *fakeSheetsClient) MakePublic(string) error                          { return nil }

func (c *fakeSheetsClient) EnsureSheet(_ string, title string) error {
	if c.ensueErr != nil {
		return c.ensueErr
	}
	c.ensured = append(c.ensured, title)
	return nil
}

func (c *fakeSheetsClient) ClearRange(_ string, rangeStr string) error {
	c.cleared = append(c.cleared, rangeStr)
	return nil
}

func (c *fakeSheetsClient) UpdateValues(_ string, rangeStr string, values [][]interface{}) error {
	c.written[rangeStr] = values
	return nil
}

// rowsFor returns the values written to the tab with this title, whatever
// cell anchor the export chose.
func (c *fakeSheetsClient) rowsFor(t *testing.T, tab string) [][]interface{} {
	t.Helper()
	for rangeStr, rows := range c.written {
		if strings.HasPrefix(rangeStr, tab+"!") {
			return rows
		}
	}
	t.Fatalf("nothing written to tab %q; wrote to %v", tab, c.writtenRanges())
	return nil
}

func (c *fakeSheetsClient) writtenRanges() []string {
	var out []string
	for r := range c.written {
		out = append(out, r)
	}
	return out
}

// cell renders a written cell for comparison without caring about its type.
func cell(v interface{}) string { return fmt.Sprint(v) }

// column returns the values of one named column, header row excluded.
func column(t *testing.T, rows [][]interface{}, header string) []string {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("no rows written")
	}
	idx := -1
	for i, h := range rows[0] {
		if cell(h) == header {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatalf("no column %q in header %v", header, rows[0])
	}
	var out []string
	for _, row := range rows[1:] {
		if idx < len(row) {
			out = append(out, cell(row[idx]))
		}
	}
	return out
}

// seedTeam registers a team of n players. Every player carries an in-game id
// and zone so the confidentiality test has something real to look for.
func seedTeam(r *fakeTelegramRepo, name string, n int) *models.TelegramTeam {
	ctx := context.Background()
	team, _ := r.CreateTeam(ctx, name)
	team.Status = models.TeamStatusActive
	team.IsCheckedIn = true
	for i := 0; i < n; i++ {
		id := team.ID
		_ = r.CreateTeammate(ctx, &models.TelegramPlayer{
			TeamID:       &id,
			GameNickname: fmt.Sprintf("%s_p%d", name, i+1),
			GameID:       fmt.Sprintf("10000%d%d", team.ID, i),
			ZoneID:       "8888",
			MainRole:     "mid",
			IsCaptain:    i == 0,
			IsSubstitute: i >= 5,
		})
	}
	return team
}

func exportService(repo *fakeTelegramRepo, client *fakeSheetsClient) *TelegramServiceImpl {
	return NewTelegramServiceImpl(repo, nopLogger{}).WithSheets(client, "sheet-1")
}

func TestExportTeamsToSheetSkipsIncompleteRosters(t *testing.T) {
	repo := newFakeTelegramRepo()
	seedTeam(repo, "Full", 5)
	seedTeam(repo, "Deep", 6)
	seedTeam(repo, "Short", 4)
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("ExportTeamsToSheet: %v", err)
	}

	names := column(t, client.rowsFor(t, "Teams"), "Team")
	for _, n := range names {
		if n == "Short" {
			t.Errorf("a 4-player roster reached the sheet: %v", names)
		}
	}
	if !strings.Contains(strings.Join(names, ","), "Full") || !strings.Contains(strings.Join(names, ","), "Deep") {
		t.Errorf("teams = %v, want Full and Deep present", names)
	}
}

func TestExportTeamsToSheetOmitsGameIDAndZone(t *testing.T) {
	repo := newFakeTelegramRepo()
	team := seedTeam(repo, "Full", 5)
	members, _ := repo.GetTeamMembers(context.Background(), team.ID)
	secret := members[0].GameID
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("ExportTeamsToSheet: %v", err)
	}

	for rangeStr, rows := range client.written {
		for _, row := range rows {
			for _, v := range row {
				got := cell(v)
				if got == secret || got == "8888" {
					t.Errorf("%s leaked in-game identity %q", rangeStr, got)
				}
				if got == "ID" || got == "Zone" {
					t.Errorf("%s still has an %q column", rangeStr, got)
				}
			}
		}
	}
}

func TestExportTeamsToSheetRecordsElimination(t *testing.T) {
	repo := newFakeTelegramRepo()
	out := seedTeam(repo, "Fallen", 5)
	winner := seedTeam(repo, "Victor", 5)
	// Four rounds of bracket exist, so round 2 is the quarter-final.
	repo.bracket = []models.BracketMatch{
		{ID: 1, Round: 1, PlayOrder: 1, Team1ID: &out.ID, Team2ID: &winner.ID, Team1Name: "Fallen", Team2Name: "Victor",
			WinnerID: &out.ID, State: models.BracketComplete, ScoresCSV: "2 - 0"},
		{ID: 2, Round: 2, PlayOrder: 1, Team1ID: &out.ID, Team2ID: &winner.ID, Team1Name: "Fallen", Team2Name: "Victor",
			WinnerID: &winner.ID, State: models.BracketComplete, ScoresCSV: "1 - 2"},
		{ID: 3, Round: 4, PlayOrder: 1, State: models.BracketPending},
	}
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("ExportTeamsToSheet: %v", err)
	}

	rows := client.rowsFor(t, "Teams")
	teams := column(t, rows, "Team")
	stages := column(t, rows, "Stage")
	by := column(t, rows, "Eliminated by")
	for i, name := range teams {
		if name != "Fallen" {
			continue
		}
		if stages[i] != "1/4" {
			t.Errorf("Fallen stage = %q, want %q", stages[i], "1/4")
		}
		if by[i] != "Victor" {
			t.Errorf("Fallen eliminated by %q, want Victor", by[i])
		}
	}
}

func TestExportTeamsToSheetMarksDisqualifiedWithoutOpponent(t *testing.T) {
	repo := newFakeTelegramRepo()
	team := seedTeam(repo, "Gone", 5)
	repo.teams[team.ID].Status = models.TeamStatusDisqualified
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("ExportTeamsToSheet: %v", err)
	}

	rows := client.rowsFor(t, "Teams")
	if got := column(t, rows, "Stage")[0]; got != "Дисквалифицирована" {
		t.Errorf("stage = %q, want Дисквалифицирована", got)
	}
	if got := column(t, rows, "Eliminated by")[0]; got != "" {
		t.Errorf("eliminated by = %q, want empty: a technical defeat has no opponent", got)
	}
}

func TestExportTeamsToSheetSurvivesEmptyBracket(t *testing.T) {
	repo := newFakeTelegramRepo()
	seedTeam(repo, "Full", 5)
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("export before the bracket is built must still work: %v", err)
	}
	if got := column(t, client.rowsFor(t, "Teams"), "Stage")[0]; got != "" {
		t.Errorf("stage = %q, want empty before a bracket exists", got)
	}
}

func TestExportTeamsToSheetWritesOnlyToItsOwnTabs(t *testing.T) {
	repo := newFakeTelegramRepo()
	seedTeam(repo, "Full", 5)
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("ExportTeamsToSheet: %v", err)
	}

	for _, r := range append(client.writtenRanges(), client.cleared...) {
		if !strings.HasPrefix(r, "Teams!") && !strings.HasPrefix(r, "Matches!") {
			t.Errorf("range %q is unqualified — it would overwrite the stats tab", r)
		}
	}
	if len(client.ensured) != 2 {
		t.Errorf("ensured tabs = %v, want both Teams and Matches created", client.ensured)
	}
}

func TestExportTeamsToSheetErrorsWhenSheetsNotConfigured(t *testing.T) {
	repo := newFakeTelegramRepo()
	seedTeam(repo, "Full", 5)

	svc := NewTelegramServiceImpl(repo, nopLogger{})
	if _, err := svc.ExportTeamsToSheet(context.Background()); err == nil {
		t.Error("export without a configured client returned nil error — the admin would see silence")
	}
}

func TestExportMatchesSheetCoversBothSidesAndByes(t *testing.T) {
	repo := newFakeTelegramRepo()
	a := seedTeam(repo, "Alpha", 5)
	b := seedTeam(repo, "Beta", 5)
	repo.bracket = []models.BracketMatch{
		{ID: 1, Round: 1, PlayOrder: 1, Team1ID: &a.ID, Team2ID: &b.ID, Team1Name: "Alpha", Team2Name: "Beta",
			WinnerID: &a.ID, State: models.BracketComplete, ScoresCSV: "2 - 1"},
		{ID: 2, Round: 2, PlayOrder: 1, Team1ID: &a.ID, Team1Name: "Alpha",
			WinnerID: &a.ID, State: models.BracketComplete},
	}
	client := newFakeSheetsClient()

	if _, err := exportService(repo, client).ExportTeamsToSheet(context.Background()); err != nil {
		t.Fatalf("ExportTeamsToSheet: %v", err)
	}

	rows := client.rowsFor(t, "Matches")
	teams := column(t, rows, "Team")
	opps := column(t, rows, "Opponent")
	results := column(t, rows, "Result")

	// The head-to-head appears once per side, so filtering the sheet by team
	// gives that team's whole run.
	var alphaOpps, betaOpps []string
	for i, name := range teams {
		switch name {
		case "Alpha":
			alphaOpps = append(alphaOpps, opps[i])
		case "Beta":
			betaOpps = append(betaOpps, opps[i])
		}
	}
	if len(alphaOpps) != 2 {
		t.Errorf("Alpha has %d match rows (%v), want 2", len(alphaOpps), alphaOpps)
	}
	if len(betaOpps) != 1 || betaOpps[0] != "Alpha" {
		t.Errorf("Beta match rows = %v, want one against Alpha", betaOpps)
	}

	var sawBye bool
	for _, r := range results {
		if r == "автопроход" {
			sawBye = true
		}
	}
	if !sawBye {
		t.Errorf("results = %v, want a bye marked as автопроход", results)
	}
}
