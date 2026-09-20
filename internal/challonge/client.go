// Package challonge is a thin client for the Challonge API v2.1, covering
// only what the tournament bracket needs. Request and response shapes follow
// https://challonge.apidog.io; TestLive checks them against the real service.
package challonge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.challonge.com/v2.1"
	// perPage is the page size for ListMatches. A 128-team bracket has 127
	// matches, so two pages cover any tournament this bot runs.
	perPage = 100
)

// ErrQuotaExceeded is returned on HTTP 429: the free plan allows 500
// requests a month and the service refuses the rest.
var ErrQuotaExceeded = errors.New("challonge: monthly request quota exceeded")

// QuotaError preserves the server's Retry-After hint while remaining
// compatible with errors.Is(err, ErrQuotaExceeded).
type QuotaError struct {
	RetryAfter time.Duration
}

func (e *QuotaError) Error() string { return ErrQuotaExceeded.Error() }
func (e *QuotaError) Unwrap() error { return ErrQuotaExceeded }

type CreateTournamentParams struct {
	Name                string
	Slug                string
	TournamentType      string // "single elimination", "double elimination", "round robin", "swiss"
	HoldThirdPlaceMatch bool
}

type Tournament struct {
	ID   int64
	Slug string
	URL  string // public bracket page
}

type NewParticipant struct {
	Name string
	Seed int
}

type Participant struct {
	ID   int64
	Name string
	Seed int
}

// Match mirrors one Challonge match. Zero ids mean the slot is undecided.
type Match struct {
	ID        int64
	Round     int
	PlayOrder int
	State     string // pending | open | complete
	Player1ID int64
	Player2ID int64
	WinnerID  int64
	Scores    string
}

type Client struct {
	http      *http.Client
	baseURL   string
	apiKey    string
	subdomain string
	sleep     func(context.Context, time.Duration) error
}

func New(apiKey, subdomain string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{http: httpClient, baseURL: defaultBaseURL, apiKey: apiKey, subdomain: subdomain, sleep: sleepContext}
}

type envelope[T any] struct {
	Data   T          `json:"data"`
	Errors []apiError `json:"errors"`
}

type apiError struct {
	Detail string `json:"detail"`
	Status string `json:"status"`
}

type resource[A any] struct {
	ID            string        `json:"id"`
	Attributes    A             `json:"attributes"`
	Relationships relationships `json:"relationships"`
}

type relationships struct {
	Player1 relation `json:"player1"`
	Player2 relation `json:"player2"`
}

type relation struct {
	Data *struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (r relation) id() int64 {
	if r.Data == nil {
		return 0
	}
	id, _ := strconv.ParseInt(r.Data.ID, 10, 64)
	return id
}

type tournamentAttrs struct {
	URL string `json:"url"`
}

type participantAttrs struct {
	Name string `json:"name"`
	Seed int    `json:"seed"`
}

// matchAttrs carries relationships too: the documented example nests them
// under attributes, the JSON:API layout puts them beside. Whichever is set wins.
type matchAttrs struct {
	State              string         `json:"state"`
	Round              int            `json:"round"`
	SuggestedPlayOrder int            `json:"suggested_play_order"`
	Scores             string         `json:"scores"`
	WinnerID           *int64         `json:"winner_id"`
	Relationships      *relationships `json:"relationships"`
}

// --- requests ---------------------------------------------------------------

func (c *Client) CreateTournament(ctx context.Context, params CreateTournamentParams) (Tournament, error) {
	tType := params.TournamentType
	if tType == "" {
		tType = "single elimination"
	}
	attrs := map[string]any{
		"name":            params.Name,
		"url":             params.Slug,
		"tournament_type": tType,
		"private":         false,
	}
	if params.HoldThirdPlaceMatch && tType == "single elimination" {
		attrs["hold_third_place_match"] = true
	}
	body := map[string]any{"data": map[string]any{"type": "tournament", "attributes": attrs}}
	var out envelope[resource[tournamentAttrs]]
	if err := c.do(ctx, http.MethodPost, "/tournaments.json", body, &out); err != nil {
		return Tournament{}, err
	}
	id, _ := strconv.ParseInt(out.Data.ID, 10, 64)
	return Tournament{ID: id, Slug: out.Data.Attributes.URL, URL: c.publicURL(out.Data.Attributes.URL)}, nil
}

func (c *Client) publicURL(slug string) string {
	if c.subdomain != "" {
		return "https://" + c.subdomain + ".challonge.com/" + slug
	}
	return "https://challonge.com/" + slug
}

func (c *Client) BulkAddParticipants(ctx context.Context, tournamentID int64, ps []NewParticipant) ([]Participant, error) {
	items := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		items = append(items, map[string]any{"name": p.Name, "seed": p.Seed})
	}
	body := map[string]any{"data": map[string]any{"type": "Participants", "attributes": map[string]any{"participants": items}}}
	var out envelope[[]resource[participantAttrs]]
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/tournaments/%d/participants/bulk_add.json", tournamentID), body, &out); err != nil {
		return nil, err
	}
	res := make([]Participant, 0, len(out.Data))
	for _, r := range out.Data {
		id, _ := strconv.ParseInt(r.ID, 10, 64)
		res = append(res, Participant{ID: id, Name: r.Attributes.Name, Seed: r.Attributes.Seed})
	}
	return res, nil
}

func (c *Client) Start(ctx context.Context, tournamentID int64) error {
	body := map[string]any{"data": map[string]any{"type": "TournamentState", "attributes": map[string]any{"state": "start"}}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tournaments/%d/change_state.json", tournamentID), body, nil)
}

func (c *Client) ListMatches(ctx context.Context, tournamentID int64) ([]Match, error) {
	return c.listMatches(ctx, tournamentID, "")
}

// ListOpenMatches asks Challonge to filter before pagination. A single-
// elimination bracket can have at most half its participants open at once,
// so this stays one request even for 128 teams.
func (c *Client) ListOpenMatches(ctx context.Context, tournamentID int64) ([]Match, error) {
	return c.listMatches(ctx, tournamentID, "open")
}

func (c *Client) listMatches(ctx context.Context, tournamentID int64, state string) ([]Match, error) {
	var all []Match
	for page := 1; ; page++ {
		path := fmt.Sprintf("/tournaments/%d/matches.json?page=%d&per_page=%d", tournamentID, page, perPage)
		if state != "" {
			path += "&state=" + url.QueryEscape(state)
		}
		var out envelope[[]resource[matchAttrs]]
		if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		for _, r := range out.Data {
			all = append(all, matchFromResource(r))
		}
		if len(out.Data) < perPage {
			return all, nil
		}
	}
}

// GetMatch reads one match and is used to resolve an ambiguous write: if the
// PUT response was lost, the caller can see whether Challonge applied it.
func (c *Client) GetMatch(ctx context.Context, tournamentID, matchID int64) (Match, error) {
	var out envelope[resource[matchAttrs]]
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/tournaments/%d/matches/%d.json", tournamentID, matchID), nil, &out); err != nil {
		return Match{}, err
	}
	return matchFromResource(out.Data), nil
}

func matchFromResource(r resource[matchAttrs]) Match {
	id, _ := strconv.ParseInt(r.ID, 10, 64)
	rel := r.Relationships
	if r.Attributes.Relationships != nil {
		rel = *r.Attributes.Relationships
	}
	m := Match{ID: id, Round: r.Attributes.Round, PlayOrder: r.Attributes.SuggestedPlayOrder, State: r.Attributes.State, Player1ID: rel.Player1.id(), Player2ID: rel.Player2.id(), Scores: r.Attributes.Scores}
	if r.Attributes.WinnerID != nil {
		m.WinnerID = *r.Attributes.WinnerID
	}
	return m
}

// ReportMatch closes a match. Scores are one "set" per side — the map count
// of a Bo3 — so Challonge renders "2 - 0".
func (c *Client) ReportMatch(ctx context.Context, tournamentID, matchID, winnerPID, loserPID int64, winnerScore, loserScore int) error {
	body := map[string]any{"data": map[string]any{"type": "match", "attributes": map[string]any{
		"match": []map[string]any{
			{"participant_id": strconv.FormatInt(winnerPID, 10), "score_set": strconv.Itoa(winnerScore), "rank": 1, "advancing": true},
			{"participant_id": strconv.FormatInt(loserPID, 10), "score_set": strconv.Itoa(loserScore), "rank": 2, "advancing": false},
		},
		"tie": false,
	}}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tournaments/%d/matches/%d.json", tournamentID, matchID), body, nil)
}

// ReopenMatch clears a result. Challonge resets every match branching from it.
func (c *Client) ReopenMatch(ctx context.Context, tournamentID, matchID int64) error {
	body := map[string]any{"data": map[string]any{"type": "MatchState", "attributes": map[string]any{"state": "reopen"}}}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tournaments/%d/matches/%d/change_state.json", tournamentID, matchID), body, nil)
}

func (c *Client) DeleteTournament(ctx context.Context, tournamentID int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/tournaments/%d.json", tournamentID), nil, nil)
}

// --- transport ---------------------------------------------------------------

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return err
	}
	if c.subdomain != "" {
		q := u.Query()
		q.Set("community_id", c.subdomain)
		u.RawQuery = q.Encode()
	}
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyBytes = b
	}
	attempts := 1
	if method == http.MethodGet {
		attempts = 3
	}
	for attempt := 0; attempt < attempts; attempt++ {
		var payload io.Reader
		if bodyBytes != nil {
			payload = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), payload)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization-Type", "v1")
		req.Header.Set("Authorization", c.apiKey)
		req.Header.Set("Content-Type", "application/vnd.api+json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			if attempt+1 < attempts {
				if err := c.sleep(ctx, retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("challonge: %s %s: %w", method, path, err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			return &QuotaError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt+1 < attempts && retryableStatus(resp.StatusCode) {
				if err := c.sleep(ctx, retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			var env envelope[json.RawMessage]
			_ = json.Unmarshal(raw, &env)
			detail := ""
			for _, e := range env.Errors {
				detail += " " + e.Detail
			}
			return fmt.Errorf("challonge: %s %s: HTTP %d%s", method, path, resp.StatusCode, detail)
		}
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("challonge: %s %s: decode: %w", method, path, err)
		}
		return nil
	}
	return fmt.Errorf("challonge: %s %s: retries exhausted", method, path)
}

func retryableStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryDelay(attempt int) time.Duration {
	return 100 * time.Millisecond * time.Duration(1<<uint(attempt))
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}
