package application

import (
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// BracketProvider is the slice of Challonge the bracket needs. *challonge.Client
// satisfies it; tests use an in-memory engine.
type BracketProvider interface {
	CreateTournament(ctx context.Context, name, slug string) (challonge.Tournament, error)
	BulkAddParticipants(ctx context.Context, tournamentID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error)
	Start(ctx context.Context, tournamentID int64) error
	ListMatches(ctx context.Context, tournamentID int64) ([]challonge.Match, error)
	ReportMatch(ctx context.Context, tournamentID, matchID, winnerPID, loserPID int64, winnerScore, loserScore int) error
	ReopenMatch(ctx context.Context, tournamentID, matchID int64) error
	DeleteTournament(ctx context.Context, tournamentID int64) error
}

// Settings keys, stored next to tournament_time.
const (
	settingChallongeID   = "challonge_tournament_id"
	settingChallongeURL  = "challonge_tournament_url"
	settingChallongeFor  = "challonge_tournament_for" // RFC3339 tournament time the bracket was built for
	settingBuildFailures = "bracket_build_failures"
)

var (
	ErrBracketNotBuilt = errors.New("сетка ещё не построена")
	ErrTooFewTeams     = errors.New("для сетки нужно минимум две команды")
	ErrMatchNotFound   = errors.New("матч не найден")
	ErrMatchNotOpen    = errors.New("матч не открыт: обе стороны ещё не определены или он уже сыгран")
	ErrNotInMatch      = errors.New("команда не участвует в этом матче")
	ErrHasResults      = errors.New("в сетке уже есть результаты")
)

// BracketService keeps the Challonge bracket and its local cache in step.
// Challonge is the source of truth; every write goes there first and is
// followed by exactly one ListMatches that rewrites the cache. Reads never
// touch the API.
type BracketService struct {
	repo     repository.Telegram
	provider BracketProvider
	logger   Logger
	// walkoverWin/Lose is the score reported for a technical defeat.
	walkoverWin, walkoverLose int
	now                       func() time.Time
	// mu serialises writes: two captains reporting at once must not
	// interleave a PUT with the other's cache rewrite.
	mu sync.Mutex
}

func NewBracketService(repo repository.Telegram, provider BracketProvider, walkoverWin, walkoverLose int, logger Logger) *BracketService {
	return &BracketService{repo: repo, provider: provider, logger: logger, walkoverWin: walkoverWin, walkoverLose: walkoverLose, now: time.Now}
}

// SeededTeam is a team with its bracket seed: 1 is the strongest.
type SeededTeam struct {
	Team     models.TelegramTeam
	Seed     int
	AvgStars float64
}

// SeedTeams orders active teams by the average stars of the main roster
// (substitutes excluded); ties go to the earlier-registered team. Byes in
// Challonge go to the top seeds, so the strongest teams skip round 1.
func SeedTeams(teams []models.TelegramTeam) []SeededTeam {
	var out []SeededTeam
	for _, t := range teams {
		if t.Status == models.TeamStatusDisqualified {
			continue
		}
		sum, n := 0, 0
		for _, p := range t.Players {
			if p.IsSubstitute {
				continue
			}
			sum += p.Stars
			n++
		}
		avg := 0.0
		if n > 0 {
			avg = float64(sum) / float64(n)
		}
		out = append(out, SeededTeam{Team: t, AvgStars: avg})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AvgStars != out[j].AvgStars {
			return out[i].AvgStars > out[j].AvgStars
		}
		return out[i].Team.ID < out[j].Team.ID
	})
	for i := range out {
		out[i].Seed = i + 1
	}
	return out
}

// BracketBuilt is what the bot announces after a build.
type BracketBuilt struct {
	URL    string
	Round1 []models.BracketMatch // round-1 matches with both teams
	Byes   []models.TelegramTeam // teams that skip round 1
}

func (s *BracketService) tournamentID(ctx context.Context) int64 {
	v, _ := s.repo.GetSetting(ctx, settingChallongeID)
	id, _ := strconv.ParseInt(v, 10, 64)
	return id
}

// URL is the public bracket page, empty when nothing is built.
func (s *BracketService) URL(ctx context.Context) string {
	v, _ := s.repo.GetSetting(ctx, settingChallongeURL)
	return v
}

// IsBuiltFor reports whether a bracket exists for this tournament time. A
// bracket from last month's tournament does not count.
func (s *BracketService) IsBuiltFor(ctx context.Context, t time.Time) bool {
	if s.tournamentID(ctx) == 0 {
		return false
	}
	v, _ := s.repo.GetSetting(ctx, settingChallongeFor)
	return v == t.UTC().Format(time.RFC3339)
}

// BuildFailures is how many consecutive builds failed since the last success.
func (s *BracketService) BuildFailures(ctx context.Context) int {
	v, _ := s.repo.GetSetting(ctx, settingBuildFailures)
	n, _ := strconv.Atoi(v)
	return n
}

// Matches is the cached bracket, ordered by round and play order.
func (s *BracketService) Matches(ctx context.Context) ([]models.BracketMatch, error) {
	return s.repo.GetBracketMatches(ctx)
}

// HasResults reports whether any match has been played. Byes complete
// matches too, so only matches with both teams count.
func (s *BracketService) HasResults(ctx context.Context) (bool, error) {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		if m.State == models.BracketComplete && m.Team1ID != nil && m.Team2ID != nil {
			return true, nil
		}
	}
	return false, nil
}

// Build creates the tournament in Challonge from every active team, seeded
// by strength, starts it and fills the cache. A previous tournament — a
// failed attempt or an admin rebuild — is deleted first.
func (s *BracketService) Build(ctx context.Context, forTournament time.Time) (*BracketBuilt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	built, err := s.build(ctx, forTournament)
	if err != nil {
		n := s.BuildFailures(ctx) + 1
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingBuildFailures, strconv.Itoa(n)))
		// The id stays so the next attempt deletes the half-made tournament;
		// "for" is cleared so IsBuiltFor says no and the worker retries.
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeFor, ""))
		return nil, err
	}
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingBuildFailures, "0"))
	return built, nil
}

func (s *BracketService) build(ctx context.Context, forTournament time.Time) (*BracketBuilt, error) {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	seeded := SeedTeams(teams)
	if len(seeded) < 2 {
		return nil, ErrTooFewTeams
	}

	if old := s.tournamentID(ctx); old != 0 {
		if err := s.provider.DeleteTournament(ctx, old); err != nil {
			// Not fatal: an orphan in Challonge costs nothing here.
			s.logger.Warn("bracket: delete previous tournament %d: %v", old, err)
		}
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeID, ""))
	}
	if err := s.repo.ClearTeamParticipantIDs(ctx); err != nil {
		return nil, err
	}
	if err := s.repo.ReplaceBracketMatches(ctx, nil); err != nil {
		return nil, err
	}

	slug := "valhalla_" + s.now().UTC().Format("20060102_150405")
	name := "Valhalla " + forTournament.Format("02.01.2006")
	tr, err := s.provider.CreateTournament(ctx, name, slug)
	if err != nil {
		return nil, err
	}
	// Remember the id before anything else can fail, so a retry deletes it.
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeID, strconv.FormatInt(tr.ID, 10)))
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeURL, tr.URL))
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeFor, forTournament.UTC().Format(time.RFC3339)))

	ps := make([]challonge.NewParticipant, 0, len(seeded))
	byName := make(map[string]int, len(seeded))
	for _, st := range seeded {
		ps = append(ps, challonge.NewParticipant{Name: st.Team.Name, Seed: st.Seed})
		byName[st.Team.Name] = st.Team.ID
	}
	created, err := s.provider.BulkAddParticipants(ctx, tr.ID, ps)
	if err != nil {
		return nil, err
	}
	for _, p := range created {
		teamID, ok := byName[p.Name]
		if !ok {
			return nil, fmt.Errorf("bracket: participant %q matches no team", p.Name)
		}
		if err := s.repo.SetTeamParticipantID(ctx, teamID, p.ID); err != nil {
			return nil, err
		}
	}
	if err := s.provider.Start(ctx, tr.ID); err != nil {
		return nil, err
	}

	ready, err := s.sync(ctx, tr.ID)
	if err != nil {
		return nil, err
	}
	inRound1 := map[int]bool{}
	var round1 []models.BracketMatch
	for _, m := range ready {
		if m.Round == 1 {
			inRound1[*m.Team1ID] = true
			inRound1[*m.Team2ID] = true
			round1 = append(round1, m)
		}
	}
	var byes []models.TelegramTeam
	for _, st := range seeded {
		if !inRound1[st.Team.ID] {
			byes = append(byes, st.Team)
		}
	}
	return &BracketBuilt{URL: tr.URL, Round1: round1, Byes: byes}, nil
}

// Sync pulls the bracket from Challonge, rewrites the cache and returns the
// matches that became ready to play since the last sync, marking them so
// they are reported once.
func (s *BracketService) Sync(ctx context.Context) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, ErrBracketNotBuilt
	}
	return s.sync(ctx, tID)
}

// sync is Sync without the lock, for callers that already hold it.
func (s *BracketService) sync(ctx context.Context, tID int64) ([]models.BracketMatch, error) {
	raw, err := s.provider.ListMatches(ctx, tID)
	if err != nil {
		return nil, err
	}
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	byPID := make(map[int64]int, len(teams))
	for _, t := range teams {
		if t.ChallongeParticipantID != nil {
			byPID[*t.ChallongeParticipantID] = t.ID
		}
	}
	toTeam := func(pid int64) *int {
		if id, ok := byPID[pid]; ok {
			return &id
		}
		return nil
	}
	ms := make([]models.BracketMatch, 0, len(raw))
	for _, m := range raw {
		ms = append(ms, models.BracketMatch{
			ChallongeMatchID: m.ID,
			Round:            m.Round,
			PlayOrder:        m.PlayOrder,
			Team1ID:          toTeam(m.Player1ID),
			Team2ID:          toTeam(m.Player2ID),
			WinnerID:         toTeam(m.WinnerID),
			State:            m.State,
			ScoresCSV:        m.Scores,
		})
	}
	if err := s.repo.ReplaceBracketMatches(ctx, ms); err != nil {
		return nil, err
	}
	cached, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	var ready []models.BracketMatch
	var ids []int
	for _, m := range cached {
		if m.Ready() && !m.BothNotified {
			ready = append(ready, m)
			ids = append(ids, m.ID)
		}
	}
	if err := s.repo.MarkBracketNotified(ctx, ids); err != nil {
		return nil, err
	}
	return ready, nil
}

func (s *BracketService) logWrite(op string, err error) {
	if err != nil {
		s.logger.Error("bracket: %s failed: %v", op, err)
	}
}
