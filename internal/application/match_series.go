package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"blackwatch/internal/models"
)

// GameConfirmWindow is how long the other team has to object to a game
// before it counts.
const GameConfirmWindow = 5 * time.Minute

// seriesDraftTTL is how long screenshots wait for the "who won" answer.
const seriesDraftTTL = 15 * time.Minute

// SeriesState is a match's best-of series at its current revision.
type SeriesState struct {
	BO   int // games at most: 1, 3 or 5
	Need int // wins that take the series
	// Wins counts confirmed games per team id.
	Wins map[int]int
	// Games are the non-annulled games in order.
	Games []models.TelegramMatchGame
	// Open is the game waiting for the other team or a referee, if any.
	Open       *models.TelegramMatchGame
	WinnerID   int // set once a team has Need wins
	NextGameNo int
}

// Decided reports whether a team has taken the series.
func (st SeriesState) Decided() bool { return st.WinnerID != 0 }

// GameEvent is what a series action did, for the bot to announce.
type GameEvent struct {
	Game   models.TelegramMatchGame
	Match  models.BracketMatch
	Series SeriesState
	// Final is the report recorded when this game decided the series.
	Final *models.TelegramMatchReport
	// Queued: the series is decided but Challonge did not take the result
	// yet; the retry queue writes it.
	Queued bool
}

// SeriesPrompt asks the player who won the game the screenshots show.
type SeriesPrompt struct {
	Match  models.BracketMatch
	Series SeriesState
	Photos int
	GameNo int
}

type seriesDraft struct {
	matchID   int
	revision  int64
	photos    []string
	updatedAt time.Time
}

var (
	errNoOpenMatch   = errors.New("у вашей команды сейчас нет открытого матча — скрин катки принимается во время матча")
	errSeriesDecided = errors.New("серия уже решена, результат записан в сетку")
	errGameNotFound  = errors.New("катка не найдена")
	errGameNotOpen   = errors.New("эта катка уже рассмотрена")
	errSeriesNoDraft = errors.New("сначала пришлите скрин результата катки")
	errNotOpponent   = errors.New("подтвердить или оспорить катку может только игрок команды соперника")
)

// FormatLong spells a best-of out: "BO3 — до 2 побед".
func FormatLong(bo int) string {
	need := (bo + 1) / 2
	word := "побед"
	if need == 1 {
		word = "победы"
	}
	return fmt.Sprintf("BO%d — до %d %s", bo, need, word)
}

// SeriesLine is the series score for a message: "Счёт серии: A 1:0 B (BO3 — до 2 побед)".
func SeriesLine(m models.BracketMatch, st SeriesState) string {
	if m.Team1ID == nil || m.Team2ID == nil {
		return ""
	}
	return fmt.Sprintf("Счёт серии: %s %d:%d %s (%s)", m.Team1Name, st.Wins[*m.Team1ID], st.Wins[*m.Team2ID], m.Team2Name, FormatLong(st.BO))
}

// buildSeries works a series out of its games.
func buildSeries(bo int, games []models.TelegramMatchGame) SeriesState {
	st := SeriesState{BO: bo, Need: (bo + 1) / 2, Wins: map[int]int{}, NextGameNo: 1}
	for i := range games {
		g := games[i]
		if g.Status == models.GameAnnulled {
			continue
		}
		st.Games = append(st.Games, g)
		if g.GameNo >= st.NextGameNo {
			st.NextGameNo = g.GameNo + 1
		}
		switch g.Status {
		case models.GameConfirmed:
			st.Wins[g.WinnerTeamID]++
			if st.Wins[g.WinnerTeamID] >= st.Need && st.WinnerID == 0 {
				st.WinnerID = g.WinnerTeamID
			}
		case models.GamePending, models.GameDisputed:
			open := g
			st.Open = &open
		}
	}
	return st
}

// seriesContext loads a match from the active bracket and its series.
func (s *TelegramServiceImpl) seriesContext(ctx context.Context, matchID int) (models.BracketMatch, SeriesState, error) {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return models.BracketMatch{}, SeriesState{}, err
	}
	m := findMatch(ms, matchID)
	if m == nil {
		return models.BracketMatch{}, SeriesState{}, ErrMatchNotFound
	}
	active, _ := s.repo.GetActiveTournament(ctx)
	games, err := s.repo.ListMatchGames(ctx, m.ID, m.Revision)
	if err != nil {
		return *m, SeriesState{}, err
	}
	return *m, buildSeries(MatchFormat(active, ms, m.Round), games), nil
}

// SeriesFor is the current series of a match, for the Mini App.
func (s *TelegramServiceImpl) SeriesFor(ctx context.Context, matchID int) (*SeriesState, error) {
	_, st, err := s.seriesContext(ctx, matchID)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// playerTeam is the team of the Telegram user, or an error. Any player of the
// team plays a series in the bot: the captain is often the one in the draft
// of the next game, and a game confirmed only by an absent captain counted by
// default.
func (s *TelegramServiceImpl) playerTeam(ctx context.Context, tgID int64) (int, error) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil || p == nil || p.TeamID == nil {
		return 0, errors.New("вы не состоите в команде")
	}
	return *p.TeamID, nil
}

// AddGamePhoto collects a screenshot of the game the player's team is
// playing. Several photos (an album) make one game; the prompt says which.
func (s *TelegramServiceImpl) AddGamePhoto(ctx context.Context, tgID int64, fileID string) (*SeriesPrompt, error) {
	teamID, err := s.playerTeam(ctx, tgID)
	if err != nil {
		return nil, err
	}
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, err
	}
	m := openMatchIn(ms, teamID)
	if m == nil {
		return nil, errNoOpenMatch
	}
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	_, st, err := s.seriesContext(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	if st.Decided() {
		return nil, errSeriesDecided
	}
	if st.Open != nil {
		return nil, fmt.Errorf("катка %d ещё ждёт подтверждения — дождитесь ответа соперника или судьи", st.Open.GameNo)
	}
	now := s.now()
	d := s.seriesDrafts[tgID]
	if d == nil || d.matchID != m.ID || d.revision != m.Revision || now.Sub(d.updatedAt) > seriesDraftTTL {
		d = &seriesDraft{matchID: m.ID, revision: m.Revision}
		s.seriesDrafts[tgID] = d
	}
	d.photos = append(d.photos, fileID)
	d.updatedAt = now
	return &SeriesPrompt{Match: *m, Series: st, Photos: len(d.photos), GameNo: st.NextGameNo}, nil
}

// ClaimGame records the game the player's screenshots show and hands it to
// the other team for confirmation.
func (s *TelegramServiceImpl) ClaimGame(ctx context.Context, tgID int64, matchID int, weWon bool) (*GameEvent, error) {
	teamID, err := s.playerTeam(ctx, tgID)
	if err != nil {
		return nil, err
	}
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	m, st, err := s.seriesContext(ctx, matchID)
	if err != nil {
		return nil, err
	}
	if !m.Ready() || !m.Has(teamID) {
		return nil, errNoOpenMatch
	}
	if st.Decided() {
		return nil, errSeriesDecided
	}
	if st.Open != nil {
		return nil, fmt.Errorf("катка %d ещё ждёт подтверждения", st.Open.GameNo)
	}
	if st.NextGameNo > st.BO {
		return nil, fmt.Errorf("в формате BO%d не больше %d каток", st.BO, st.BO)
	}
	d := s.seriesDrafts[tgID]
	if d == nil || d.matchID != m.ID || d.revision != m.Revision || len(d.photos) == 0 {
		return nil, errSeriesNoDraft
	}
	winner := teamID
	if !weWon {
		winner = *m.Opponent(teamID)
	}
	expires := s.now().Add(GameConfirmWindow)
	g := &models.TelegramMatchGame{
		BracketMatchID: m.ID, MatchRevision: m.Revision, GameNo: st.NextGameNo, WinnerTeamID: winner,
		ReporterTelegramID: tgID, PhotoFileIDs: append([]string(nil), d.photos...), Status: models.GamePending, ExpiresAt: &expires,
	}
	if err := s.repo.CreateMatchGame(ctx, g); err != nil {
		// Someone else claimed this game a moment earlier: a teammate or the
		// other team.
		return nil, fmt.Errorf("катку %d уже отправили — проверьте сообщения бота", st.NextGameNo)
	}
	delete(s.seriesDrafts, tgID)
	_, st, _ = s.seriesContext(ctx, m.ID)
	return &GameEvent{Game: *g, Match: m, Series: st}, nil
}

// openGame loads a game still open on the current revision of its match.
func (s *TelegramServiceImpl) openGame(ctx context.Context, gameID int) (*models.TelegramMatchGame, models.BracketMatch, error) {
	g, err := s.repo.GetMatchGame(ctx, gameID)
	if err != nil {
		return nil, models.BracketMatch{}, err
	}
	if g == nil {
		return nil, models.BracketMatch{}, errGameNotFound
	}
	m, _, err := s.seriesContext(ctx, g.BracketMatchID)
	if err != nil {
		return nil, m, err
	}
	if m.Revision != g.MatchRevision || (g.Status != models.GamePending && g.Status != models.GameDisputed) {
		return nil, m, errGameNotOpen
	}
	return g, m, nil
}

// ConfirmGame is a player of the other team agreeing with a reported game.
func (s *TelegramServiceImpl) ConfirmGame(ctx context.Context, tgID int64, gameID int) (*GameEvent, error) {
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	g, m, err := s.openGame(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if g.Status != models.GamePending {
		return nil, errGameNotOpen
	}
	if err := s.checkOpponent(ctx, tgID, g, m); err != nil {
		return nil, err
	}
	return s.settleGame(ctx, g, m, g.WinnerTeamID, tgID)
}

// DisputeGame is a player of the other team objecting; a referee decides.
func (s *TelegramServiceImpl) DisputeGame(ctx context.Context, tgID int64, gameID int) (*GameEvent, error) {
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	g, m, err := s.openGame(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if g.Status != models.GamePending {
		return nil, errGameNotOpen
	}
	if err := s.checkOpponent(ctx, tgID, g, m); err != nil {
		return nil, err
	}
	if err := s.repo.SetMatchGameStatus(ctx, g.ID, models.GameDisputed); err != nil {
		return nil, err
	}
	g.Status = models.GameDisputed
	_, st, _ := s.seriesContext(ctx, m.ID)
	return &GameEvent{Game: *g, Match: m, Series: st}, nil
}

// JudgeGame is a referee's decision on an open game: winnerTeamID counts it
// for that team, 0 strikes it out so it is played again.
func (s *TelegramServiceImpl) JudgeGame(ctx context.Context, gameID, winnerTeamID int) (*GameEvent, error) {
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	g, m, err := s.openGame(ctx, gameID)
	if err != nil {
		return nil, err
	}
	if winnerTeamID == 0 {
		if err := s.repo.SetMatchGameStatus(ctx, g.ID, models.GameAnnulled); err != nil {
			return nil, err
		}
		g.Status = models.GameAnnulled
		_, st, _ := s.seriesContext(ctx, m.ID)
		return &GameEvent{Game: *g, Match: m, Series: st}, nil
	}
	if !m.Has(winnerTeamID) {
		return nil, ErrNotInMatch
	}
	return s.settleGame(ctx, g, m, winnerTeamID, 0)
}

// AutoConfirmExpiredGames counts the games the other team did not answer
// in time, so an absent opponent cannot hold the series.
func (s *TelegramServiceImpl) AutoConfirmExpiredGames(ctx context.Context) ([]GameEvent, error) {
	expired, err := s.repo.GetExpiredPendingGames(ctx, s.now())
	if err != nil {
		return nil, err
	}
	var out []GameEvent
	for _, e := range expired {
		s.seriesMu.Lock()
		g, m, err := s.openGame(ctx, e.ID)
		if err == nil && g.Status == models.GamePending {
			var ev *GameEvent
			ev, err = s.settleGame(ctx, g, m, g.WinnerTeamID, 0)
			if err == nil {
				out = append(out, *ev)
			}
		}
		s.seriesMu.Unlock()
		if err != nil && !errors.Is(err, errGameNotOpen) {
			s.logger.Error("telegram: auto-confirm game %d: %v", e.ID, err)
		}
	}
	return out, nil
}

// checkOpponent: only a player of the team that did not report a game may
// answer it.
func (s *TelegramServiceImpl) checkOpponent(ctx context.Context, tgID int64, g *models.TelegramMatchGame, m models.BracketMatch) error {
	teamID, err := s.playerTeam(ctx, tgID)
	if err != nil {
		return errNotOpponent
	}
	reporter, _ := s.repo.GetPlayerByTelegramID(ctx, g.ReporterTelegramID)
	if !m.Has(teamID) || (reporter != nil && reporter.TeamID != nil && *reporter.TeamID == teamID) {
		return errNotOpponent
	}
	return nil
}

// settleGame counts a game for winnerTeamID and, when that decides the
// series, records the series result in the bracket. The caller holds seriesMu.
func (s *TelegramServiceImpl) settleGame(ctx context.Context, g *models.TelegramMatchGame, m models.BracketMatch, winnerTeamID int, actor int64) (*GameEvent, error) {
	if winnerTeamID == g.WinnerTeamID {
		if err := s.repo.SetMatchGameStatus(ctx, g.ID, models.GameConfirmed); err != nil {
			return nil, err
		}
		g.Status = models.GameConfirmed
	} else {
		// A referee gave the game to the other side: strike the claim and
		// record the decision under the same number.
		if err := s.repo.SetMatchGameStatus(ctx, g.ID, models.GameAnnulled); err != nil {
			return nil, err
		}
		decided := *g
		decided.ID, decided.WinnerTeamID, decided.Status, decided.ExpiresAt = 0, winnerTeamID, models.GameConfirmed, nil
		if err := s.repo.CreateMatchGame(ctx, &decided); err != nil {
			return nil, err
		}
		g = &decided
	}
	_, st, err := s.seriesContext(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	ev := &GameEvent{Game: *g, Match: m, Series: st}
	if !st.Decided() {
		return ev, nil
	}
	rep, recorded, err := s.recordSeries(ctx, m, st, actor)
	if err != nil {
		return ev, err
	}
	ev.Final, ev.Queued = rep, !recorded
	return ev, nil
}

// recordSeries turns a decided series into a confirmed report — every game
// was already agreed — and writes it to the bracket.
func (s *TelegramServiceImpl) recordSeries(ctx context.Context, m models.BracketMatch, st SeriesState, actor int64) (*models.TelegramMatchReport, bool, error) {
	loser := *m.Opponent(st.WinnerID)
	var photos []string
	reporter := actor
	for _, g := range st.Games {
		if g.Status == models.GameConfirmed {
			photos = append(photos, g.PhotoFileIDs...)
			if reporter == 0 {
				reporter = g.ReporterTelegramID
			}
		}
	}
	// The series supersedes a report filed the old way from the Mini App.
	if open, _ := s.repo.GetOpenReportForMatch(ctx, m.ID); open != nil {
		s.logWrite("SetReportStatus", s.repo.SetReportStatus(ctx, open.ID, models.ReportOverridden))
	}
	name := func(id int) string {
		if m.Team1ID != nil && *m.Team1ID == id {
			return m.Team1Name
		}
		return m.Team2Name
	}
	matchID := m.ID
	rep := &models.TelegramMatchReport{
		ReporterTelegramID: reporter,
		WinnerTeamID:       st.WinnerID,
		WinnerTeamName:     name(st.WinnerID),
		LoserTeamID:        loser,
		LoserTeamName:      name(loser),
		Score:              fmt.Sprintf("%d:%d", st.Wins[st.WinnerID], st.Wins[loser]),
		PhotoFileIDs:       photos,
		BracketMatchID:     &matchID,
		Status:             models.ReportPending,
	}
	if err := s.repo.CreateMatchReport(ctx, rep); err != nil {
		return nil, false, fmt.Errorf("не удалось сохранить итог серии: %w", err)
	}
	recorded, err := s.applyReport(ctx, rep, models.ReportConfirmed)
	return rep, recorded, err
}

// SeriesScores is the live score of each open match's series, team 1 first
// ("1:0"), keyed by bracket match id; matches without counted games are absent.
func (s *TelegramServiceImpl) SeriesScores(ctx context.Context, ms []models.BracketMatch) map[int]string {
	out := map[int]string{}
	var ids []int
	byID := map[int]models.BracketMatch{}
	for _, m := range ms {
		if m.State == models.BracketOpen && m.Team1ID != nil && m.Team2ID != nil {
			ids = append(ids, m.ID)
			byID[m.ID] = m
		}
	}
	games, err := s.repo.ListGamesForMatches(ctx, ids)
	if err != nil {
		return out
	}
	wins := map[int][2]int{}
	for _, g := range games {
		m, ok := byID[g.BracketMatchID]
		if !ok || g.MatchRevision != m.Revision || g.Status != models.GameConfirmed {
			continue
		}
		w := wins[m.ID]
		if g.WinnerTeamID == *m.Team1ID {
			w[0]++
		} else {
			w[1]++
		}
		wins[m.ID] = w
	}
	for id, w := range wins {
		out[id] = fmt.Sprintf("%d:%d", w[0], w[1])
	}
	return out
}
