package application

import (
	"blackwatch/internal/challonge"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"sync"
	"time"
)

// BracketProvider is the slice of Challonge the bracket needs. *challonge.Client
// satisfies it; tests use an in-memory engine.
type BracketProvider interface {
	CreateTournament(ctx context.Context, params challonge.CreateTournamentParams) (challonge.Tournament, error)
	BulkAddParticipants(ctx context.Context, tournamentID int64, ps []challonge.NewParticipant) ([]challonge.Participant, error)
	Start(ctx context.Context, tournamentID int64) error
	ListMatches(ctx context.Context, tournamentID int64) ([]challonge.Match, error)
	ListOpenMatches(ctx context.Context, tournamentID int64) ([]challonge.Match, error)
	GetMatch(ctx context.Context, tournamentID, matchID int64) (challonge.Match, error)
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
	settingQuotaBlocked  = "challonge_quota_blocked_until"
)

var (
	ErrBracketNotBuilt = errors.New("сетка ещё не построена")
	ErrTooFewTeams     = errors.New("для сетки нужно минимум две команды")
	ErrMatchNotFound   = errors.New("матч не найден")
	ErrMatchNotOpen    = errors.New("матч не открыт: обе стороны ещё не определены или он уже сыгран")
	ErrNotInMatch      = errors.New("команда не участвует в этом матче")
	ErrHasResults      = errors.New("в сетке уже есть результаты")
	ErrResultConflict  = errors.New("результат в Challonge отличается от локального отчёта")
)

// BracketService keeps the Challonge bracket and its local cache in step.
// Challonge is the source of truth; writes go there first and then merge the
// completed result plus the server-filtered open matches into the local cache.
// Normal reads never touch the API.
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

func mainRosterSize(team models.TelegramTeam) int {
	n := 0
	for _, player := range team.Players {
		if !player.IsSubstitute {
			n++
		}
	}
	return n
}

// SeedTeams orders active teams by the average stars of the main roster
// (substitutes excluded); ties go to the earlier-registered team. Byes in
// Challonge go to the top seeds, so the strongest teams skip round 1.
func SeedTeams(teams []models.TelegramTeam) []SeededTeam {
	return SeedTeamsWithStrategy(teams, models.SeedingTypeStars)
}

// SeedTeamsWithStrategy orders active teams by the given strategy ("stars" or "random").
func SeedTeamsWithStrategy(teams []models.TelegramTeam, strategy string) []SeededTeam {
	var out []SeededTeam
	for _, t := range teams {
		if t.Status == models.TeamStatusDisqualified {
			continue
		}
		if mainRosterSize(t) < MainRosterSlots {
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
	if strategy == models.SeedingTypeRandom {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		r.Shuffle(len(out), func(i, j int) {
			out[i], out[j] = out[j], out[i]
		})
	} else {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].AvgStars != out[j].AvgStars {
				return out[i].AvgStars > out[j].AvgStars
			}
			return out[i].Team.ID < out[j].Team.ID
		})
	}
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
	// Later holds matches beyond round 1 that are already ready to play — two
	// byes meeting in round 2. Sync marked them notified, so the announcer
	// must ping their captains itself.
	Later []models.BracketMatch
	// Incomplete teams were deliberately excluded from the bracket.
	Incomplete []models.TelegramTeam
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

// CanAttempt reports whether the persisted quota circuit breaker is open.
// Persisting it prevents a process restart from resuming one request a minute.
func (s *BracketService) CanAttempt(ctx context.Context, now time.Time) bool {
	value, _ := s.repo.GetSetting(ctx, settingQuotaBlocked)
	until, err := time.Parse(time.RFC3339, value)
	return err != nil || !now.Before(until)
}

func (s *BracketService) recordQuota(ctx context.Context, err error) {
	if !errors.Is(err, challonge.ErrQuotaExceeded) {
		return
	}
	delay := 5 * time.Minute
	var quota *challonge.QuotaError
	if errors.As(err, &quota) && quota.RetryAfter > 0 {
		delay = quota.RetryAfter
	}
	until := s.now().Add(delay).UTC().Format(time.RFC3339)
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingQuotaBlocked, until))
}

// RecordAPIError updates persistent provider health state for delivery-layer
// operations such as admin overrides and full synchronizations.
func (s *BracketService) RecordAPIError(ctx context.Context, err error) {
	s.recordQuota(ctx, err)
}

func (s *BracketService) clearQuotaBlock(ctx context.Context) {
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingQuotaBlocked, ""))
}

// Matches is the cached bracket, ordered by round and play order.
func (s *BracketService) Matches(ctx context.Context) ([]models.BracketMatch, error) {
	return s.repo.GetBracketMatches(ctx)
}

// Walkover returns the configured technical-defeat score.
func (s *BracketService) Walkover() (int, int) {
	return s.walkoverWin, s.walkoverLose
}

// CaptainChatIDs returns the Telegram chats to notify for a team.
func (s *BracketService) CaptainChatIDs(ctx context.Context, teamID int) []int64 {
	members, err := s.repo.GetTeamMembers(ctx, teamID)
	if err != nil {
		s.logger.Warn("bracket: get captains for team %d: %v", teamID, err)
		return nil
	}
	var ids []int64
	for _, member := range members {
		if member.IsCaptain && member.TelegramID != nil {
			ids = append(ids, *member.TelegramID)
		}
	}
	return ids
}

// TeamChatIDs returns the Telegram chats to notify for all members of a team.
func (s *BracketService) TeamChatIDs(ctx context.Context, teamID int) []int64 {
	members, err := s.repo.GetTeamMembers(ctx, teamID)
	if err != nil {
		s.logger.Warn("bracket: get team members for team %d: %v", teamID, err)
		return nil
	}
	var ids []int64
	seen := make(map[int64]bool)
	for _, member := range members {
		if member.TelegramID != nil && *member.TelegramID > 0 && !seen[*member.TelegramID] {
			seen[*member.TelegramID] = true
			ids = append(ids, *member.TelegramID)
		}
	}
	return ids
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
		s.recordQuota(ctx, err)
		n := s.BuildFailures(ctx) + 1
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingBuildFailures, strconv.Itoa(n)))
		// The id stays so the next attempt deletes the half-made tournament;
		// "for" is cleared so IsBuiltFor says no and the worker retries.
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingChallongeFor, ""))
		return nil, err
	}
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, settingBuildFailures, "0"))
	s.clearQuotaBlock(ctx)
	return built, nil
}

func (s *BracketService) build(ctx context.Context, forTournament time.Time) (*BracketBuilt, error) {
	activeTourney, _ := s.repo.GetActiveTournament(ctx)
	var teams []models.TelegramTeam
	if activeTourney != nil {
		tTeams, err := s.repo.GetTournamentTeams(ctx, activeTourney.ID)
		if err == nil && len(tTeams) > 0 {
			teams = tTeams
		}
	}
	if len(teams) == 0 {
		var err error
		teams, err = s.repo.GetAllTeams(ctx)
		if err != nil {
			return nil, err
		}
	}
	seedingType := models.SeedingTypeStars
	tournamentType := models.TournamentTypeSingleElimination
	holdThirdPlace := false
	if activeTourney != nil {
		if activeTourney.SeedingType != "" {
			seedingType = activeTourney.SeedingType
		}
		if activeTourney.TournamentType != "" {
			tournamentType = activeTourney.TournamentType
		}
		holdThirdPlace = activeTourney.HoldThirdPlace
	}
	seeded := SeedTeamsWithStrategy(teams, seedingType)
	var incomplete []models.TelegramTeam
	for _, team := range teams {
		if team.Status != models.TeamStatusDisqualified && mainRosterSize(team) < MainRosterSlots {
			incomplete = append(incomplete, team)
		}
	}
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
	if activeTourney != nil {
		_ = s.repo.ClearTournamentTeamParticipantIDs(ctx, activeTourney.ID)
		_ = s.repo.ReplaceBracketMatchesForTournament(ctx, activeTourney.ID, nil)
	} else {
		if err := s.repo.ReplaceBracketMatches(ctx, nil); err != nil {
			return nil, err
		}
	}

	slug := "valhalla_" + s.now().UTC().Format("20060102_150405")
	name := "Valhalla " + forTournament.Format("02.01.2006")
	if activeTourney != nil && activeTourney.Name != "" {
		name = activeTourney.Name
		if activeTourney.Slug != "" {
			slug = activeTourney.Slug
		}
	}
	tr, err := s.provider.CreateTournament(ctx, challonge.CreateTournamentParams{
		Name:                name,
		Slug:                slug,
		TournamentType:      tournamentType,
		HoldThirdPlaceMatch: holdThirdPlace,
	})
	if err != nil {
		return nil, err
	}
	// Remember the id before anything else can fail, so a retry deletes it.
	// If this write itself fails, nothing will ever find the tournament to
	// delete it, so we must do it ourselves before giving up.
	if err := s.repo.SetSetting(ctx, settingChallongeID, strconv.FormatInt(tr.ID, 10)); err != nil {
		if delErr := s.provider.DeleteTournament(ctx, tr.ID); delErr != nil {
			s.logger.Warn("bracket: delete orphaned tournament %d: %v", tr.ID, delErr)
		}
		return nil, err
	}
	if err := s.repo.SetSetting(ctx, settingChallongeURL, tr.URL); err != nil {
		return nil, err
	}
	if err := s.repo.SetSetting(ctx, settingChallongeFor, forTournament.UTC().Format(time.RFC3339)); err != nil {
		return nil, err
	}
	if activeTourney != nil {
		activeTourney.ChallongeID = &tr.ID
		activeTourney.ChallongeURL = tr.URL
		nowFor := forTournament.UTC()
		activeTourney.ChallongeFor = &nowFor
		activeTourney.Status = models.TournamentStatusActive
		_ = s.repo.UpdateTournament(ctx, activeTourney)
	}

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
		if activeTourney != nil {
			_ = s.repo.SetTournamentTeamParticipantID(ctx, activeTourney.ID, teamID, p.ID)
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
	var round1, later []models.BracketMatch
	for _, m := range ready {
		if m.Round == 1 {
			inRound1[*m.Team1ID] = true
			inRound1[*m.Team2ID] = true
			round1 = append(round1, m)
		} else {
			later = append(later, m)
		}
	}
	var byes []models.TelegramTeam
	for _, st := range seeded {
		if !inRound1[st.Team.ID] {
			byes = append(byes, st.Team)
		}
	}
	return &BracketBuilt{URL: tr.URL, Round1: round1, Byes: byes, Later: later, Incomplete: incomplete}, nil
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

func (s *BracketService) sync(ctx context.Context, tID int64) ([]models.BracketMatch, error) {
	raw, err := s.provider.ListMatches(ctx, tID)
	if err != nil {
		return nil, err
	}
	activeTourney, _ := s.repo.GetActiveTournament(ctx)
	var teams []models.TelegramTeam
	if activeTourney != nil {
		tTeams, err := s.repo.GetTournamentTeams(ctx, activeTourney.ID)
		if err == nil && len(tTeams) > 0 {
			teams = tTeams
		}
	}
	if len(teams) == 0 {
		teams, err = s.repo.GetAllTeams(ctx)
		if err != nil {
			return nil, err
		}
	}
	byPID := make(map[int64]int, len(teams))
	for _, t := range teams {
		if t.ChallongeParticipantID != nil {
			byPID[*t.ChallongeParticipantID] = t.ID
		}
	}
	var tIDPtr *int
	if activeTourney != nil {
		tIDPtr = &activeTourney.ID
	}
	ms := make([]models.BracketMatch, 0, len(raw))
	for _, m := range raw {
		bm := bracketMatchFromRemote(m, byPID)
		bm.TournamentID = tIDPtr
		ms = append(ms, bm)
	}
	return s.replaceAndReady(ctx, ms)
}

func bracketMatchFromRemote(m challonge.Match, byPID map[int64]int) models.BracketMatch {
	toTeam := func(pid int64) *int {
		if id, ok := byPID[pid]; ok {
			return &id
		}
		return nil
	}
	return models.BracketMatch{ChallongeMatchID: m.ID, Round: m.Round, PlayOrder: m.PlayOrder, Team1ID: toTeam(m.Player1ID), Team2ID: toTeam(m.Player2ID), WinnerID: toTeam(m.WinnerID), State: m.State, ScoresCSV: m.Scores}
}

func (s *BracketService) replaceAndReady(ctx context.Context, ms []models.BracketMatch) ([]models.BracketMatch, error) {
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

// syncAfterReport merges the completed match and Challonge's currently open
// matches into the existing full cache. Unlike ListMatches this is always one
// page for a <=128-team single-elimination bracket, saving one API request per
// reported match at the largest supported size.
func (s *BracketService) syncAfterReport(ctx context.Context, tID int64, reported *models.BracketMatch, winnerTeamID, winnerScore, loserScore int) ([]models.BracketMatch, error) {
	open, err := s.provider.ListOpenMatches(ctx, tID)
	if err != nil {
		s.recordQuota(ctx, err)
		return nil, err
	}
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	byPID := make(map[int64]int, len(teams))
	for _, team := range teams {
		if team.ChallongeParticipantID != nil {
			byPID[*team.ChallongeParticipantID] = team.ID
		}
	}
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	byRemoteID := make(map[int64]int, len(ms))
	for i := range ms {
		byRemoteID[ms[i].ChallongeMatchID] = i
		if ms[i].ID == reported.ID {
			winner := winnerTeamID
			team1Score, team2Score := winnerScore, loserScore
			if reported.Team2ID != nil && *reported.Team2ID == winnerTeamID {
				team1Score, team2Score = loserScore, winnerScore
			}
			ms[i].WinnerID = &winner
			ms[i].State = models.BracketComplete
			ms[i].ScoresCSV = fmt.Sprintf("%d - %d", team1Score, team2Score)
		}
	}
	for _, remote := range open {
		m := bracketMatchFromRemote(remote, byPID)
		if i, ok := byRemoteID[remote.ID]; ok {
			m.ID = ms[i].ID
			ms[i] = m
		}
	}
	return s.replaceAndReady(ctx, ms)
}

func (s *BracketService) logWrite(op string, err error) {
	if err != nil {
		s.logger.Error("bracket: %s failed: %v", op, err)
	}
}

// OpenMatchFor is the team's current playable match, nil when it has none
// (eliminated, waiting for an opponent, or the bracket is not built).
func (s *BracketService) OpenMatchFor(ctx context.Context, teamID int) (*models.BracketMatch, error) {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	return openMatchIn(ms, teamID), nil
}

func openMatchIn(ms []models.BracketMatch, teamID int) *models.BracketMatch {
	for i := range ms {
		if ms[i].Ready() && ms[i].Has(teamID) {
			m := ms[i]
			return &m
		}
	}
	return nil
}

func findMatch(ms []models.BracketMatch, id int) *models.BracketMatch {
	for i := range ms {
		if ms[i].ID == id {
			m := ms[i]
			return &m
		}
	}
	return nil
}

// participantIDs resolves both sides of a match to Challonge participant ids.
func (s *BracketService) participantIDs(ctx context.Context, winnerTeamID, loserTeamID int) (int64, int64, error) {
	w, err := s.repo.GetTeamByID(ctx, winnerTeamID)
	if err != nil || w == nil || w.ChallongeParticipantID == nil {
		return 0, 0, fmt.Errorf("bracket: team %d has no Challonge participant", winnerTeamID)
	}
	l, err := s.repo.GetTeamByID(ctx, loserTeamID)
	if err != nil || l == nil || l.ChallongeParticipantID == nil {
		return 0, 0, fmt.Errorf("bracket: team %d has no Challonge participant", loserTeamID)
	}
	return *w.ChallongeParticipantID, *l.ChallongeParticipantID, nil
}

// report is the PUT for one match; the caller holds mu and syncs afterwards.
func (s *BracketService) report(ctx context.Context, tID int64, m *models.BracketMatch, winnerTeamID, winnerScore, loserScore int) error {
	if !m.Ready() {
		return ErrMatchNotOpen
	}
	if !m.Has(winnerTeamID) {
		return ErrNotInMatch
	}
	loserID := *m.Opponent(winnerTeamID)
	wPID, lPID, err := s.participantIDs(ctx, winnerTeamID, loserID)
	if err != nil {
		return err
	}
	err = s.provider.ReportMatch(ctx, tID, m.ChallongeMatchID, wPID, lPID, winnerScore, loserScore)
	if err == nil {
		s.clearQuotaBlock(ctx)
		return nil
	}
	if errors.Is(err, challonge.ErrQuotaExceeded) {
		s.recordQuota(ctx, err)
		return err
	}
	// A timed-out PUT is ambiguous: Challonge may have committed it before the
	// connection broke. Read the match before allowing any caller to retry.
	remote, readErr := s.provider.GetMatch(ctx, tID, m.ChallongeMatchID)
	if readErr != nil {
		return fmt.Errorf("%w; reconciliation failed: %w", err, readErr)
	}
	if resultMatches(remote, wPID, winnerScore, loserScore) {
		return nil
	}
	if remote.State == models.BracketComplete {
		return fmt.Errorf("%w: match #%d", ErrResultConflict, m.PlayOrder)
	}
	return err
}

func resultMatches(remote challonge.Match, winnerPID int64, winnerScore, loserScore int) bool {
	if remote.State != models.BracketComplete || remote.WinnerID != winnerPID {
		return false
	}
	first, second, _, ok := parseScore(remote.Scores)
	if !ok {
		return false
	}
	// Challonge's compact scores field follows participant order, while older
	// responses/fixtures may preserve submission order. The winner id makes
	// accepting either representation unambiguous.
	return (first == winnerScore && second == loserScore) || (first == loserScore && second == winnerScore)
}

// ReportResult records a played match and returns the matches that became
// ready because of it.
func (s *BracketService) ReportResult(ctx context.Context, matchID, winnerTeamID, winnerScore, loserScore int) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, ErrBracketNotBuilt
	}
	if !s.CanAttempt(ctx, s.now()) {
		return nil, challonge.ErrQuotaExceeded
	}
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	m := findMatch(ms, matchID)
	if m == nil {
		return nil, ErrMatchNotFound
	}
	if err := s.report(ctx, tID, m, winnerTeamID, winnerScore, loserScore); err != nil {
		return nil, err
	}
	return s.syncAfterReport(ctx, tID, m, winnerTeamID, winnerScore, loserScore)
}

// ForfeitDisqualified hands every open match of a disqualified team to its
// opponent. A pass reports every such match, then syncs once; a double
// no-show opens the next match for the "winner", so passes repeat until
// nothing changes. Idempotent: a team with no open match costs no request.
func (s *BracketService) ForfeitDisqualified(ctx context.Context) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, nil
	}
	if !s.CanAttempt(ctx, s.now()) {
		return nil, nil
	}
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	var out []models.BracketMatch
	const maxPasses = 8 // deeper than any bracket this bot runs
	for pass := 0; pass < maxPasses; pass++ {
		ms, err := s.repo.GetBracketMatches(ctx)
		if err != nil {
			return nil, err
		}
		reported := 0
		done := map[int]bool{} // match ids reported this pass
		for _, t := range teams {
			if t.Status != models.TeamStatusDisqualified {
				continue
			}
			m := openMatchIn(ms, t.ID)
			if m == nil || done[m.ID] {
				continue
			}
			opp := *m.Opponent(t.ID)
			if err := s.report(ctx, tID, m, opp, s.walkoverWin, s.walkoverLose); err != nil {
				return out, err
			}
			done[m.ID] = true
			reported++
		}
		if reported == 0 {
			break
		}
		ready, err := s.sync(ctx, tID)
		if err != nil {
			return s.stillReady(ctx, out), err
		}
		out = append(out, ready...)
	}
	return s.stillReady(ctx, out), nil
}

// stillReady drops matches that a later pass of the cascade closed again
// (a double no-show opens the next match and walks it over in one sweep),
// so nobody is pinged about a match that no longer exists to be played.
func (s *BracketService) stillReady(ctx context.Context, ms []models.BracketMatch) []models.BracketMatch {
	if len(ms) == 0 {
		return nil
	}
	cur, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return ms
	}
	var out []models.BracketMatch
	for _, m := range ms {
		if c := findMatch(cur, m.ID); c != nil && c.Ready() {
			out = append(out, *c)
		}
	}
	return out
}

// BracketChange is what an admin override did to the bracket. Reset holds
// matches as they were before: played or ready, now cleared by Challonge's
// branch reset. Ready holds matches that became playable.
type BracketChange struct {
	Reset []models.BracketMatch
	Ready []models.BracketMatch
}

// SetWinner forces the outcome of a match. A finished match is reopened
// first; Challonge then resets everything downstream, and the diff of the
// cache before and after says whom to tell.
func (s *BracketService) SetWinner(ctx context.Context, playOrder int, teamName string, winnerScore, loserScore int) (*BracketChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, ErrBracketNotBuilt
	}
	team, err := s.repo.GetTeamByName(ctx, teamName)
	if err != nil || team == nil {
		return nil, fmt.Errorf("команда '%s' не найдена", teamName)
	}
	before, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	var m *models.BracketMatch
	for i := range before {
		if before[i].PlayOrder == playOrder {
			m = &before[i]
		}
	}
	if m == nil {
		return nil, ErrMatchNotFound
	}
	if m.Team1ID == nil || m.Team2ID == nil {
		return nil, ErrMatchNotOpen
	}
	if !m.Has(team.ID) {
		return nil, ErrNotInMatch
	}
	return s.override(ctx, tID, before, m, team.ID, winnerScore, loserScore)
}

func (s *BracketService) override(ctx context.Context, tID int64, before []models.BracketMatch, m *models.BracketMatch, winnerTeamID, winnerScore, loserScore int) (*BracketChange, error) {
	if m.State == models.BracketComplete {
		if err := s.provider.ReopenMatch(ctx, tID, m.ChallongeMatchID); err != nil {
			return nil, err
		}
		m.State = models.BracketOpen
		m.WinnerID = nil
	}
	if err := s.report(ctx, tID, m, winnerTeamID, winnerScore, loserScore); err != nil {
		return nil, err
	}
	ready, err := s.sync(ctx, tID)
	if err != nil {
		return nil, err
	}
	after, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	afterByID := make(map[int]models.BracketMatch, len(after))
	for _, a := range after {
		afterByID[a.ID] = a
	}
	ch := &BracketChange{Ready: ready}
	for _, b := range before {
		if b.ID == m.ID {
			continue
		}
		wasLive := b.State == models.BracketComplete || b.Ready()
		if !wasLive {
			continue
		}
		a, ok := afterByID[b.ID]
		stillSame := ok && a.State == b.State && samePairIDs(a, b)
		if !stillSame {
			ch.Reset = append(ch.Reset, b)
		}
	}
	return ch, nil
}

func samePairIDs(a, b models.BracketMatch) bool {
	eq := func(x, y *int) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	return eq(a.Team1ID, b.Team1ID) && eq(a.Team2ID, b.Team2ID)
}

// Reinstate undoes a technical defeat: the team's lost match is reopened and
// handed back to it. Nil change when the team has no lost match.
func (s *BracketService) Reinstate(ctx context.Context, teamName string) (*BracketChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, nil
	}
	team, err := s.repo.GetTeamByName(ctx, teamName)
	if err != nil || team == nil {
		return nil, fmt.Errorf("команда '%s' не найдена", teamName)
	}
	teamID := team.ID
	before, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	lost := eliminationMatch(before, teamID)
	if lost == nil {
		return nil, nil
	}
	if err := s.provider.ReopenMatch(ctx, tID, lost.ChallongeMatchID); err != nil {
		return nil, err
	}
	ready, err := s.sync(ctx, tID)
	if err != nil {
		return nil, err
	}
	ch := &BracketChange{Ready: ready}
	after, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	afterByID := make(map[int]models.BracketMatch, len(after))
	for _, a := range after {
		afterByID[a.ID] = a
	}
	// The reopened match keeps its pair, so both_notified survived the sync
	// and it is not in ready; the captains still need to hear it is back on.
	if a, ok := afterByID[lost.ID]; ok && a.Ready() {
		already := false
		for _, r := range ready {
			already = already || r.ID == a.ID
		}
		if !already {
			ch.Ready = append(ch.Ready, a)
		}
	}
	for _, b := range before {
		if b.ID == lost.ID || (!b.Ready() && b.State != models.BracketComplete) {
			continue
		}
		if a, ok := afterByID[b.ID]; !ok || a.State != b.State || !samePairIDs(a, b) {
			ch.Reset = append(ch.Reset, b)
		}
	}
	return ch, nil
}

// FlushPendingReports pushes reports saved while Challonge was unreachable.
// Before retrying a write, the remote match is read: an already-applied result
// is reconciled without another PUT, while a conflicting completed result is
// removed from automatic retries and surfaced for an administrator.
func (s *BracketService) FlushPendingReports(ctx context.Context) ([]models.BracketMatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tID := s.tournamentID(ctx)
	if tID == 0 {
		return nil, nil
	}
	if !s.CanAttempt(ctx, s.now()) {
		return nil, nil
	}
	queued, err := s.repo.GetUnsyncedReports(ctx)
	if err != nil {
		return nil, err
	}
	var out []models.BracketMatch
	for _, rep := range queued {
		if rep.BracketMatchID == nil {
			continue
		}
		ms, err := s.repo.GetBracketMatches(ctx)
		if err != nil {
			return out, err
		}
		m := findMatch(ms, *rep.BracketMatchID)
		if m == nil || !m.Has(rep.WinnerTeamID) {
			s.logger.Warn("bracket: dropping queued report %d: match no longer open", rep.ID)
			s.logWrite("SetReportSynced", s.repo.SetReportSynced(ctx, rep.ID))
			continue
		}
		w, l, _, ok := parseScore(rep.Score)
		if !ok {
			s.logger.Warn("bracket: dropping queued report %d: bad score %q", rep.ID, rep.Score)
			s.logWrite("SetReportSynced", s.repo.SetReportSynced(ctx, rep.ID))
			continue
		}
		loserID := m.Opponent(rep.WinnerTeamID)
		if loserID == nil {
			return out, ErrMatchNotOpen
		}
		wPID, _, err := s.participantIDs(ctx, rep.WinnerTeamID, *loserID)
		if err != nil {
			return out, err
		}
		remote, err := s.provider.GetMatch(ctx, tID, m.ChallongeMatchID)
		if err != nil {
			s.recordQuota(ctx, err)
			return out, err
		}
		alreadyApplied := resultMatches(remote, wPID, w, l)
		if remote.State == models.BracketComplete && !alreadyApplied {
			// There is no retry that can safely fix a conflicting completed
			// match. Remove it from the automatic queue and surface it to admins.
			if err := s.repo.SetReportSynced(ctx, rep.ID); err != nil {
				return out, err
			}
			return out, fmt.Errorf("%w: match #%d", ErrResultConflict, m.PlayOrder)
		}
		if !alreadyApplied {
			if !m.Ready() {
				return out, ErrMatchNotOpen
			}
			if err := s.report(ctx, tID, m, rep.WinnerTeamID, w, l); err != nil {
				return out, err // Challonge still down: keep the queue, try next tick
			}
		}
		ready, err := s.syncAfterReport(ctx, tID, m, rep.WinnerTeamID, w, l)
		if err != nil {
			return out, err
		}
		if err := s.repo.SetReportSynced(ctx, rep.ID); err != nil {
			return out, err
		}
		out = append(out, ready...)
	}
	return out, nil
}

// Points distribution for tournament placements in the League.
var DefaultPlacementPoints = map[int]int{
	1: 100, // 1st place
	2: 70,  // 2nd place
	3: 50,  // 3-4th place (semi-finalists)
	5: 20,  // 5-8th place (quarter-finalists)
	9: 10,  // 9-16th place
	0: 5,   // Participation (check-in / registered)
}

// CalculatePlacements derives final team placements and league points from bracket matches and team list.
func CalculatePlacements(matches []models.BracketMatch, teams []models.TelegramTeam) (map[int]int, map[int]int) {
	placements := make(map[int]int)
	points := make(map[int]int)
	if len(matches) == 0 {
		return placements, points
	}

	totalRounds := TotalRounds(matches)

	var finalMatch *models.BracketMatch
	for i := range matches {
		if matches[i].Round == totalRounds && matches[i].WinnerID != nil {
			finalMatch = &matches[i]
			break
		}
	}

	if finalMatch != nil && finalMatch.WinnerID != nil {
		winnerID := *finalMatch.WinnerID
		loserID := finalMatch.Opponent(winnerID)
		placements[winnerID] = 1
		points[winnerID] = DefaultPlacementPoints[1]
		if loserID != nil {
			placements[*loserID] = 2
			points[*loserID] = DefaultPlacementPoints[2]
		}
	}

	for r := totalRounds - 1; r >= 1; r-- {
		place := 3
		if r == totalRounds-2 {
			place = 5
		} else if r <= totalRounds-3 {
			place = 9
		}
		pts := DefaultPlacementPoints[place]
		for _, m := range matches {
			if m.Round == r && m.WinnerID != nil {
				loserID := m.Opponent(*m.WinnerID)
				if loserID != nil && placements[*loserID] == 0 {
					placements[*loserID] = place
					points[*loserID] = pts
				}
			}
		}
	}

	for _, t := range teams {
		if placements[t.ID] == 0 {
			placements[t.ID] = 17
			points[t.ID] = DefaultPlacementPoints[0]
		}
	}

	return placements, points
}
