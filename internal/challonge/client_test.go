package challonge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestClient serves fn and records every request and body.
func newTestClient(t *testing.T, fn http.HandlerFunc) (*Client, *[]*http.Request, *[]string) {
	t.Helper()
	var reqs []*http.Request
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, r)
		bodies = append(bodies, string(b))
		fn(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New("KEY", "", srv.Client())
	c.baseURL = srv.URL
	return c, &reqs, &bodies
}

func TestHeadersAndCreateTournament(t *testing.T) {
	c, reqs, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"data":{"id":"30201","type":"tournament","attributes":{"name":"Valhalla","url":"valhalla_x","tournament_type":"single elimination"}}}`)
	})
	tr, err := c.CreateTournament(context.Background(), CreateTournamentParams{
		Name:           "Valhalla",
		Slug:           "valhalla_x",
		TournamentType: "single elimination",
	})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	if tr.ID != 30201 || tr.Slug != "valhalla_x" || tr.URL != "https://challonge.com/valhalla_x" {
		t.Errorf("tournament = %+v", tr)
	}
	r := (*reqs)[0]
	if r.Method != http.MethodPost || r.URL.Path != "/tournaments.json" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	for k, want := range map[string]string{
		"Authorization-Type": "v1", "Authorization": "KEY",
		"Content-Type": "application/vnd.api+json", "Accept": "application/json",
	} {
		if got := r.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*bodies)[0]), &body)
	attrs := body["data"].(map[string]any)["attributes"].(map[string]any)
	if attrs["tournament_type"] != "single elimination" || attrs["url"] != "valhalla_x" {
		t.Errorf("body attrs = %v", attrs)
	}
}

func TestSubdomainGoesToQueryAndURL(t *testing.T) {
	c, reqs, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"data":{"id":"1","attributes":{"url":"cup"}}}`)
	})
	c.subdomain = "valhalla"
	tr, err := c.CreateTournament(context.Background(), CreateTournamentParams{Name: "Cup", Slug: "cup"})
	if err != nil {
		t.Fatal(err)
	}
	if (*reqs)[0].URL.Query().Get("community_id") != "valhalla" {
		t.Errorf("community_id missing: %s", (*reqs)[0].URL.String())
	}
	if tr.URL != "https://valhalla.challonge.com/cup" {
		t.Errorf("URL = %s", tr.URL)
	}
}

func TestCreateTournamentOptions(t *testing.T) {
	c, _, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"data":{"id":"42","type":"tournament","attributes":{"name":"Cup","url":"cup_slug","tournament_type":"double elimination"}}}`)
	})
	_, err := c.CreateTournament(context.Background(), CreateTournamentParams{
		Name:                "Cup",
		Slug:                "cup_slug",
		TournamentType:      "double elimination",
		HoldThirdPlaceMatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*bodies)[0]), &body)
	attrs := body["data"].(map[string]any)["attributes"].(map[string]any)
	if attrs["tournament_type"] != "double elimination" {
		t.Errorf("tournament_type = %v", attrs["tournament_type"])
	}
	// hold_third_place_match should not be sent for non-single-elimination
	if _, ok := attrs["hold_third_place_match"]; ok {
		t.Errorf("hold_third_place_match should not be set for double elimination")
	}
}

func TestBulkAddSendsSeeds(t *testing.T) {
	c, _, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"id":"76","type":"participant","attributes":{"name":"A","seed":1}},{"id":"77","type":"participant","attributes":{"name":"B","seed":2}}]}`)
	})
	ps, err := c.BulkAddParticipants(context.Background(), 5, []NewParticipant{{"A", 1}, {"B", 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != 76 || ps[1].Name != "B" || ps[1].Seed != 2 {
		t.Errorf("participants = %+v", ps)
	}
	if !strings.Contains((*bodies)[0], `"type":"Participants"`) || !strings.Contains((*bodies)[0], `"seed":2`) {
		t.Errorf("body = %s", (*bodies)[0])
	}
}

func TestStartAndReopenUseChangeState(t *testing.T) {
	c, reqs, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"id":"1"}}`)
	})
	if err := c.Start(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if err := c.ReopenMatch(context.Background(), 5, 42); err != nil {
		t.Fatal(err)
	}
	if p := (*reqs)[0].URL.Path; p != "/tournaments/5/change_state.json" || !strings.Contains((*bodies)[0], `"state":"start"`) {
		t.Errorf("start: %s %s", p, (*bodies)[0])
	}
	if p := (*reqs)[1].URL.Path; p != "/tournaments/5/matches/42/change_state.json" || !strings.Contains((*bodies)[1], `"state":"reopen"`) {
		t.Errorf("reopen: %s %s", p, (*bodies)[1])
	}
}

// Challonge's documented example nests relationships inside attributes; the
// JSON:API norm puts them beside attributes. Both must parse.
func TestListMatchesParsesBothRelationshipPlacements(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[
		 {"id":"8008135","type":"match","attributes":{"state":"complete","round":1,"suggested_play_order":1,"scores":"2 - 0","winner_id":355,
		   "relationships":{"player1":{"data":{"id":"355","type":"participant"}},"player2":{"data":{"id":"354","type":"participant"}}}}},
		 {"id":"8008136","type":"match","attributes":{"state":"pending","round":2,"suggested_play_order":3,"winner_id":null},
		   "relationships":{"player1":{"data":{"id":"355","type":"participant"}},"player2":{"data":null}}}
		]}`)
	})
	ms, err := c.ListMatches(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("got %d matches", len(ms))
	}
	m := ms[0]
	if m.ID != 8008135 || m.State != "complete" || m.Round != 1 || m.PlayOrder != 1 || m.Player1ID != 355 || m.Player2ID != 354 || m.WinnerID != 355 || m.Scores != "2 - 0" {
		t.Errorf("match[0] = %+v", m)
	}
	m = ms[1]
	if m.Player1ID != 355 || m.Player2ID != 0 || m.WinnerID != 0 || m.State != "pending" {
		t.Errorf("match[1] = %+v", m)
	}
}

func TestListMatchesPaginates(t *testing.T) {
	page := 0
	c, reqs, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			var sb strings.Builder
			sb.WriteString(`{"data":[`)
			for i := 0; i < perPage; i++ {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `{"id":"%d","attributes":{"state":"open","round":1}}`, 1000+i)
			}
			sb.WriteString(`]}`)
			io.WriteString(w, sb.String())
			return
		}
		io.WriteString(w, `{"data":[{"id":"999","attributes":{"state":"open","round":2}}]}`)
	})
	ms, err := c.ListMatches(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != perPage+1 {
		t.Errorf("got %d matches, want %d", len(ms), perPage+1)
	}
	if q := (*reqs)[1].URL.Query(); q.Get("page") != "2" || q.Get("per_page") == "" {
		t.Errorf("second request query = %v", q)
	}
}

func TestReportMatchBody(t *testing.T) {
	c, reqs, bodies := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"id":"42"}}`)
	})
	if err := c.ReportMatch(context.Background(), 5, 42, 355, 354, 2, 1); err != nil {
		t.Fatal(err)
	}
	if (*reqs)[0].Method != http.MethodPut || (*reqs)[0].URL.Path != "/tournaments/5/matches/42.json" {
		t.Errorf("request = %s %s", (*reqs)[0].Method, (*reqs)[0].URL.Path)
	}
	var body struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Match []struct {
					ParticipantID string `json:"participant_id"`
					ScoreSet      string `json:"score_set"`
					Rank          int    `json:"rank"`
					Advancing     bool   `json:"advancing"`
				} `json:"match"`
				Tie bool `json:"tie"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte((*bodies)[0]), &body); err != nil {
		t.Fatal(err)
	}
	m := body.Data.Attributes.Match
	if body.Data.Type != "match" || len(m) != 2 ||
		m[0].ParticipantID != "355" || m[0].ScoreSet != "2" || m[0].Rank != 1 || !m[0].Advancing ||
		m[1].ParticipantID != "354" || m[1].ScoreSet != "1" || m[1].Rank != 2 || m[1].Advancing {
		t.Errorf("body = %s", (*bodies)[0])
	}
}

func TestQuotaAndAPIErrors(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if err := c.Start(context.Background(), 5); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("429 -> %v, want ErrQuotaExceeded", err)
	}
	c2, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"errors":[{"detail":"Url has already been taken","status":"422"}]}`)
	})
	err := c2.Start(context.Background(), 5)
	if err == nil || !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "already been taken") {
		t.Errorf("422 -> %v, want status and detail in error", err)
	}
}

func TestGetMatchRetriesTransientFailure(t *testing.T) {
	attempts := 0
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"data":{"id":"42","attributes":{"state":"complete","round":1,"suggested_play_order":7,"scores":"2 - 0","winner_id":355},"relationships":{"player1":{"data":{"id":"355"}},"player2":{"data":{"id":"354"}}}}}`)
	})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	m, err := c.GetMatch(context.Background(), 5, 42)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || m.ID != 42 || m.WinnerID != 355 || m.State != "complete" {
		t.Fatalf("attempts=%d match=%+v", attempts, m)
	}
}

func TestWriteDoesNotBlindlyRetry(t *testing.T) {
	attempts := 0
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := c.ReportMatch(context.Background(), 5, 42, 355, 354, 2, 0); err == nil {
		t.Fatal("ReportMatch succeeded")
	}
	if attempts != 1 {
		t.Fatalf("write attempts=%d, want 1", attempts)
	}
}

func TestListOpenMatchesFiltersAtServer(t *testing.T) {
	c, reqs, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[]}`)
	})
	if _, err := c.ListOpenMatches(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if got := (*reqs)[0].URL.Query().Get("state"); got != "open" {
		t.Fatalf("state=%q, want open", got)
	}
}

func TestQuotaErrorCarriesRetryAfter(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	err := c.Start(context.Background(), 5)
	var quota *QuotaError
	if !errors.As(err, &quota) || !errors.Is(err, ErrQuotaExceeded) || quota.RetryAfter != 2*time.Minute {
		t.Fatalf("quota error = %#v", err)
	}
}

// TestLive runs the whole lifecycle against the real API — the spike the spec
// asks for. It fails loudly if a shape guessed from the docs does not match.
// Needs CHALLONGE_API_KEY; skipped otherwise. Costs 7 requests of the quota.
func TestLive(t *testing.T) {
	key := os.Getenv("CHALLONGE_API_KEY")
	if key == "" {
		t.Skip("CHALLONGE_API_KEY not set")
	}
	ctx := context.Background()
	c := New(key, os.Getenv("CHALLONGE_SUBDOMAIN"), http.DefaultClient)
	slug := fmt.Sprintf("bw_live_%d", time.Now().Unix())
	tr, err := c.CreateTournament(ctx, CreateTournamentParams{Name: "blackwatch live test", Slug: slug})
	if err != nil {
		t.Fatalf("CreateTournament: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteTournament(ctx, tr.ID) })
	t.Logf("tournament %d %s", tr.ID, tr.URL)

	ps, err := c.BulkAddParticipants(ctx, tr.ID, []NewParticipant{{"A", 1}, {"B", 2}, {"C", 3}})
	if err != nil || len(ps) != 3 {
		t.Fatalf("BulkAddParticipants: %v (%d)", err, len(ps))
	}
	if err := c.Start(ctx, tr.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ms, err := c.ListMatches(ctx, tr.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("ListMatches: %v (%d)", err, len(ms))
	}
	var open *Match
	for i := range ms {
		if ms[i].State == "open" {
			open = &ms[i]
		}
	}
	if open == nil || open.Player1ID == 0 || open.Player2ID == 0 {
		t.Fatalf("no open match with both players in %+v", ms)
	}
	if err := c.ReportMatch(ctx, tr.ID, open.ID, open.Player1ID, open.Player2ID, 2, 0); err != nil {
		t.Fatalf("ReportMatch: %v", err)
	}
	ms, _ = c.ListMatches(ctx, tr.ID)
	for _, m := range ms {
		if m.ID == open.ID && (m.State != "complete" || m.WinnerID != open.Player1ID) {
			t.Errorf("after report: %+v", m)
		}
	}
	if err := c.ReopenMatch(ctx, tr.ID, open.ID); err != nil {
		t.Fatalf("ReopenMatch: %v", err)
	}
}
