package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"blackwatch/internal/models"
)

// TournamentSettings are the admin-editable fields of a tournament.
type TournamentSettings struct {
	Name           string
	Slug           string
	TournamentType string
	SeedingType    string
	Stage          string
	HoldThirdPlace bool
	// TournamentTime is used on creation only; the start time has its own
	// setter because moving it also moves a built bracket.
	TournamentTime  *time.Time
	AdvanceCount    int
	FormatRounds    int
	FormatSemifinal int
	FormatFinal     int
}

// FinishResult is what finishing a tournament produced.
type FinishResult struct {
	Tournament *models.TelegramTournament
	// Qualified holds the teams that advanced, for a qualifier.
	Qualified []models.TelegramTeam
}

var (
	errMainStageClosed  = errors.New("на основной этап попадают только команды, прошедшие квалификацию, и приглашённые организаторами")
	errAlreadyQualified = errors.New("команда уже прошла в основной этап через другую квалификацию")
)

// SetEventHook installs the live-update publisher (SSE for the Mini App).
func (s *TelegramServiceImpl) SetEventHook(fn func(event string, payload map[string]any)) {
	s.hookMu.Lock()
	s.eventHook = fn
	s.hookMu.Unlock()
}

func (s *TelegramServiceImpl) emit(event string, payload map[string]any) {
	s.hookMu.RLock()
	fn := s.eventHook
	s.hookMu.RUnlock()
	if fn != nil {
		fn(event, payload)
	}
}

func validFormat(bo int) bool { return bo == 1 || bo == 3 || bo == 5 }

// normalizeSettings applies defaults and checks the combination.
func normalizeSettings(set *TournamentSettings) error {
	set.Name = strings.TrimSpace(set.Name)
	if set.Name == "" {
		return errors.New("название турнира не может быть пустым")
	}
	if set.TournamentType == "" {
		set.TournamentType = models.TournamentTypeSingleElimination
	}
	switch set.TournamentType {
	case models.TournamentTypeSingleElimination, models.TournamentTypeDoubleElimination, models.TournamentTypeRoundRobin, models.TournamentTypeSwiss:
	default:
		return fmt.Errorf("неизвестный тип сетки %q", set.TournamentType)
	}
	if set.SeedingType == "" {
		set.SeedingType = models.SeedingTypeStars
	}
	if set.SeedingType != models.SeedingTypeStars && set.SeedingType != models.SeedingTypeRandom {
		return fmt.Errorf("неизвестный посев %q", set.SeedingType)
	}
	if set.Stage == "" {
		set.Stage = models.StageStandalone
	}
	switch set.Stage {
	case models.StageStandalone, models.StageMain:
		set.AdvanceCount = 0
	case models.StageQualifier:
		double := set.TournamentType == models.TournamentTypeDoubleElimination
		if !double && set.TournamentType != models.TournamentTypeSingleElimination {
			return errors.New("квалификация играется только на выбывание: single или double elimination")
		}
		minAdvance := 2
		if double {
			minAdvance = 4
		}
		if set.AdvanceCount < minAdvance || set.AdvanceCount&(set.AdvanceCount-1) != 0 {
			return fmt.Errorf("из квалификации должно выходить %d, 8, 16… команд (степень двойки не меньше %d)", minAdvance, minAdvance)
		}
	default:
		return fmt.Errorf("неизвестный этап %q", set.Stage)
	}
	if set.FormatRounds == 0 {
		set.FormatRounds = models.DefaultFormatRounds
	}
	if set.FormatSemifinal == 0 {
		set.FormatSemifinal = models.DefaultFormatSemifinal
	}
	if set.FormatFinal == 0 {
		set.FormatFinal = models.DefaultFormatFinal
	}
	if !validFormat(set.FormatRounds) || !validFormat(set.FormatSemifinal) || !validFormat(set.FormatFinal) {
		return errors.New("формат матча: BO1, BO3 или BO5")
	}
	return nil
}

func applySettings(t *models.TelegramTournament, set TournamentSettings) {
	t.Name = set.Name
	t.TournamentType = set.TournamentType
	t.SeedingType = set.SeedingType
	t.HoldThirdPlace = set.HoldThirdPlace
	t.Stage = set.Stage
	t.AdvanceCount = set.AdvanceCount
	t.FormatRounds = set.FormatRounds
	t.FormatSemifinal = set.FormatSemifinal
	t.FormatFinal = set.FormatFinal
}

func (s *TelegramServiceImpl) CreateTournament(ctx context.Context, set TournamentSettings) (*models.TelegramTournament, error) {
	if err := normalizeSettings(&set); err != nil {
		return nil, err
	}
	if err := s.activeUnderWay(ctx, 0); err != nil {
		return nil, err
	}
	slug, err := normalizeChallongeSlug(set.Slug)
	if err != nil {
		return nil, err
	}
	if slug == "" {
		slug = "tourney_" + time.Now().UTC().Format("20060102_150405")
	}
	t := &models.TelegramTournament{
		Slug:           slug,
		Status:         models.TournamentStatusRegistration,
		TournamentTime: set.TournamentTime,
		IsActive:       true,
	}
	applySettings(t, set)

	created, err := s.repo.CreateTournament(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать турнир: %w", err)
	}

	s.mu.Lock()
	if set.TournamentTime != nil {
		s.tournamentTime = *set.TournamentTime
	} else {
		s.tournamentTime = time.Time{}
	}
	s.mu.Unlock()
	timeSetting := ""
	if set.TournamentTime != nil {
		timeSetting = set.TournamentTime.Format(time.RFC3339)
	}
	_ = s.repo.SetSetting(ctx, "tournament_time", timeSetting)
	_ = s.repo.SetSetting(ctx, settingChallongeID, "")
	_ = s.repo.SetSetting(ctx, settingChallongeURL, "")
	_ = s.repo.SetSetting(ctx, settingChallongeFor, "")
	return created, nil
}

// CreateNextQualifier opens the qualifier after a finished one, with its
// settings and the next number in the name, as a draft taking registrations
// without a start time. It refuses once any tournament was created after the
// finished one, so a second tap or another admin's does not open two.
func (s *TelegramServiceImpl) CreateNextQualifier(ctx context.Context, finishedID int) (*models.TelegramTournament, error) {
	prev, err := s.repo.GetTournamentByID(ctx, finishedID)
	if err != nil {
		return nil, err
	}
	if prev == nil || !prev.Qualifier() {
		return nil, errors.New("это не квалификация")
	}
	if prev.Status != models.TournamentStatusCompleted {
		return nil, fmt.Errorf("квалификация «%s» ещё не завершена", prev.Name)
	}
	all, err := s.repo.GetAllTournaments(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range all {
		if t.ID > prev.ID {
			return nil, fmt.Errorf("после «%s» уже создан турнир «%s»", prev.Name, t.Name)
		}
	}
	return s.CreateTournament(ctx, TournamentSettings{
		Name:            NextQualifierName(prev.Name),
		TournamentType:  prev.TournamentType,
		SeedingType:     prev.SeedingType,
		Stage:           models.StageQualifier,
		HoldThirdPlace:  prev.HoldThirdPlace,
		AdvanceCount:    prev.AdvanceCount,
		FormatRounds:    prev.FormatRounds,
		FormatSemifinal: prev.FormatSemifinal,
		FormatFinal:     prev.FormatFinal,
	})
}

// NextQualifierName counts the trailing number up: "Квалификация 1" is
// followed by "Квалификация 2", a name without a number by "<name> 2".
func NextQualifierName(name string) string {
	name = strings.TrimSpace(name)
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if n, err := strconv.Atoi(name[i:]); err == nil && i > 0 && name[i-1] == ' ' {
		return name[:i] + strconv.Itoa(n+1)
	}
	return name + " 2"
}

// activeUnderWay refuses to replace the active tournament while its bracket
// is being played. Creating or activating another one used to mark it
// completed without finishing it: a qualifier's teams were never recorded as
// qualified and its last match could no longer be decided, since the bot
// only works on the active bracket. keep is a tournament id the caller may
// switch to anyway (itself), 0 for none. A tournament still taking
// registrations has nothing to lose and may be replaced.
func (s *TelegramServiceImpl) activeUnderWay(ctx context.Context, keep int) error {
	t, err := s.repo.GetActiveTournament(ctx)
	if err != nil || t == nil || t.ID == keep || t.Status != models.TournamentStatusActive {
		return err
	}
	if t.Qualifier() {
		matches, err := s.repo.GetBracketMatchesForTournament(ctx, t.ID)
		if err != nil {
			return err
		}
		entries, err := s.repo.GetTournamentEntries(ctx, t.ID)
		if err != nil {
			return err
		}
		if out := QualifierStatus(t, matches, bracketParticipants(entries, matches)); len(out.Pending) > 0 {
			nums := make([]string, 0, len(out.Pending))
			for _, m := range out.Pending {
				nums = append(nums, fmt.Sprintf("#%d", m.PlayOrder))
			}
			return fmt.Errorf("квалификация «%s» не доиграна: не сыграны матчи %s. Когда судья их решит, квала закроется сама и объявит прошедших — после этого создавайте новый турнир",
				t.Name, strings.Join(nums, ", "))
		}
		return fmt.Errorf("квалификация «%s» не завершена: завершите её в админке, чтобы прошедшие команды были записаны, затем создавайте новый турнир", t.Name)
	}
	return fmt.Errorf("турнир «%s» ещё идёт: завершите его в админке, чтобы записать итоги, затем создавайте новый", t.Name)
}

// UpdateTournamentSettings edits an existing tournament. The stage and the
// advance count are fixed once the tournament is finished, and the bracket
// type once the bracket is built.
func (s *TelegramServiceImpl) UpdateTournamentSettings(ctx context.Context, id int, set TournamentSettings) (*models.TelegramTournament, error) {
	t, err := s.repo.GetTournamentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("турнир не найден")
	}
	if err := normalizeSettings(&set); err != nil {
		return nil, err
	}
	// A finished tournament may still become a qualifier — the first one ran
	// before stages existed — but a finished qualifier keeps its line.
	if t.Status == models.TournamentStatusCompleted && t.Stage == models.StageQualifier &&
		(set.Stage != t.Stage || set.AdvanceCount != t.AdvanceCount) {
		return nil, errors.New("квалификация завершена: этап и число прошедших уже не меняются")
	}
	if t.ChallongeID != nil && set.TournamentType != t.TournamentType {
		return nil, errors.New("сетка уже построена: тип сетки менять нельзя")
	}
	if slug, err := normalizeChallongeSlug(set.Slug); err != nil {
		return nil, err
	} else if slug != "" && t.ChallongeID == nil {
		t.Slug = slug
	}
	applySettings(t, set)

	matches, err := s.repo.GetBracketMatchesForTournament(ctx, id)
	if err != nil {
		return nil, err
	}
	// A bracket built before its size was recorded: read the size while the
	// cache still holds every round, before a qualifier's cut drops them.
	if t.BracketSize == 0 && len(matches) > 0 {
		if l := NewLayout(t, matches); !l.Other && l.UBRounds > 0 {
			t.BracketSize = 1 << l.UBRounds
		}
	}
	if err := s.repo.UpdateTournament(ctx, t); err != nil {
		return nil, err
	}
	if t.Qualifier() && len(matches) > 0 && t.Status != models.TournamentStatusCompleted {
		if err := s.repo.ReplaceBracketMatchesForTournament(ctx, id, FilterStage(t, matches)); err != nil {
			return nil, err
		}
	}
	s.emit("tournaments_update", map[string]any{"action": "updated", "id": id})
	return t, nil
}

// FinishTournament closes a tournament. A qualifier must have every match
// before its advance line played; its surviving teams become qualified and
// the matches past the line leave the cache. Other tournaments get final
// placements and league points.
func (s *TelegramServiceImpl) FinishTournament(ctx context.Context, id int) (*FinishResult, error) {
	// The worker's auto-close and the admin's button can land in the same
	// second; without this both passed the "already finished" check and the
	// qualified teams were announced twice.
	s.finishMu.Lock()
	defer s.finishMu.Unlock()
	t, err := s.repo.GetTournamentByID(ctx, id)
	if err != nil || t == nil {
		return nil, errors.New("турнир не найден")
	}
	matches, err := s.repo.GetBracketMatchesForTournament(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить матчи турнира: %w", err)
	}
	entries, err := s.repo.GetTournamentEntries(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить команды турнира: %w", err)
	}
	if t.Status == models.TournamentStatusCompleted && !finishableQualifier(t, entries) {
		return nil, errors.New("турнир уже завершён")
	}
	names := make(map[int]string, len(entries))
	for _, e := range entries {
		names[e.TeamID] = e.TeamName
	}

	wasActive := t.IsActive
	res := &FinishResult{Tournament: t}
	if t.Qualifier() {
		participants := bracketParticipants(entries, matches)
		if len(participants) == 0 {
			return nil, errors.New("сетка квалификации не построена")
		}
		out := QualifierStatus(t, matches, participants)
		if !out.Done {
			nums := make([]string, 0, len(out.Pending))
			for _, m := range out.Pending {
				nums = append(nums, fmt.Sprintf("#%d", m.PlayOrder))
			}
			return nil, fmt.Errorf("квалификация не доиграна: не сыграны матчи %s", strings.Join(nums, ", "))
		}
		if out.Err != nil {
			return nil, out.Err
		}
		for _, teamID := range out.Qualified {
			if err := s.repo.SetTournamentTeamStatus(ctx, id, teamID, models.EntryQualified); err != nil {
				return nil, err
			}
			res.Qualified = append(res.Qualified, models.TelegramTeam{ID: teamID, Name: names[teamID]})
		}
		s.closeCutReports(ctx, t, matches)
		if err := s.repo.ReplaceBracketMatchesForTournament(ctx, id, FilterStage(t, matches)); err != nil {
			return nil, err
		}
		// A qualifier gives no league points; a tournament finished as a
		// plain one before it was marked a qualifier had been given some.
		if err := s.repo.ClearTournamentPlacements(ctx, id); err != nil {
			return nil, err
		}
		if err := s.repo.FinishTournamentRecord(ctx, id, nil, ""); err != nil {
			return nil, fmt.Errorf("не удалось завершить турнир: %w", err)
		}
	} else {
		ids := make([]int, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.TeamID)
		}
		placements, points := Placements(t, matches, ids)
		s.logWrite("UpdateTournamentPlacements", s.repo.UpdateTournamentPlacements(ctx, id, placements, points))
		var winnerID *int
		var winnerName string
		for teamID, place := range placements {
			if place == 1 {
				w := teamID
				winnerID, winnerName = &w, names[teamID]
			}
		}
		if err := s.repo.FinishTournamentRecord(ctx, id, winnerID, winnerName); err != nil {
			return nil, fmt.Errorf("не удалось завершить турнир: %w", err)
		}
	}
	if wasActive {
		s.clearCurrentBracket(ctx)
	}
	s.emit("tournaments_update", map[string]any{"action": "finished", "id": id})
	s.emit("bracket_update", map[string]any{"action": "finished"})
	return res, nil
}

// bracketParticipants are the teams seeded into a tournament's bracket: entries
// with a Challonge participant id, plus every team in the cached matches. A
// bracket built before participant ids were kept per tournament has entries
// without them, while its matches still name the teams.
func bracketParticipants(entries []models.TournamentTeam, matches []models.BracketMatch) []int {
	seen := map[int]bool{}
	var out []int
	add := func(id int) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, e := range entries {
		if e.ChallongeParticipantID != nil {
			add(e.TeamID)
		}
	}
	for _, m := range matches {
		if m.Team1ID != nil {
			add(*m.Team1ID)
		}
		if m.Team2ID != nil {
			add(*m.Team2ID)
		}
	}
	return out
}

// closeCutReports closes the open reports of matches past a qualifier's
// advance line. Their matches leave the cache, and left open the reports were
// later auto-confirmed and announced as results of games that were cancelled.
func (s *TelegramServiceImpl) closeCutReports(ctx context.Context, t *models.TelegramTournament, matches []models.BracketMatch) {
	cut := NewLayout(t, matches).Cutoff(t.AdvanceCount)
	if cut.All {
		return
	}
	for _, m := range matches {
		if cut.InStage(m.Round) {
			continue
		}
		rep, err := s.repo.GetOpenReportForMatch(ctx, m.ID)
		if err != nil || rep == nil {
			continue
		}
		s.logWrite("SetReportStatus", s.repo.SetReportStatus(ctx, rep.ID, models.ReportOverridden))
	}
}

// finishableQualifier: a completed tournament can still be finished as a
// qualifier once, when it was closed as a plain tournament before it was
// marked a qualifier, so nobody has qualified from it yet.
func finishableQualifier(t *models.TelegramTournament, entries []models.TournamentTeam) bool {
	if !t.Qualifier() {
		return false
	}
	for _, e := range entries {
		if e.Status == models.EntryQualified {
			return false
		}
	}
	return true
}

// clearCurrentBracket detaches the finished tournament from the background
// worker: without a bracket id and start time it no longer syncs, forfeits or
// reminds anyone about it.
func (s *TelegramServiceImpl) clearCurrentBracket(ctx context.Context) {
	for _, key := range []string{settingChallongeID, settingChallongeURL, settingChallongeFor, "tournament_time"} {
		s.logWrite("SetSetting", s.repo.SetSetting(ctx, key, ""))
	}
	s.mu.Lock()
	s.tournamentTime = time.Time{}
	s.mu.Unlock()
}

// TryFinishQualifier closes the active qualifier once its last match before
// the advance line is played. It returns nil while there is nothing to do.
func (s *TelegramServiceImpl) TryFinishQualifier(ctx context.Context) (*FinishResult, error) {
	t, err := s.repo.GetActiveTournament(ctx)
	if err != nil || !t.Qualifier() || t.Status != models.TournamentStatusActive {
		return nil, err
	}
	matches, err := s.repo.GetBracketMatchesForTournament(ctx, t.ID)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	entries, err := s.repo.GetTournamentEntries(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	out := QualifierStatus(t, matches, bracketParticipants(entries, matches))
	if !out.Done {
		return nil, nil
	}
	if out.Err != nil {
		return nil, out.Err
	}
	return s.FinishTournament(ctx, t.ID)
}

func (s *TelegramServiceImpl) openMainStage(ctx context.Context, id int) (*models.TelegramTournament, error) {
	t, err := s.repo.GetTournamentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("турнир не найден")
	}
	if t.Stage != models.StageMain {
		return nil, errors.New("добавлять прошедших и приглашённых можно только в основной этап")
	}
	if t.Status != models.TournamentStatusRegistration && t.Status != models.TournamentStatusDraft {
		return nil, errors.New("сетка основного этапа уже построена")
	}
	return t, nil
}

// ImportQualified enters every team that advanced out of the given finished
// qualifiers into the main stage. Returns how many teams were entered.
func (s *TelegramServiceImpl) ImportQualified(ctx context.Context, mainID int, fromIDs []int) (int, error) {
	if _, err := s.openMainStage(ctx, mainID); err != nil {
		return 0, err
	}
	if len(fromIDs) == 0 {
		return 0, errors.New("выберите хотя бы одну квалификацию")
	}
	n := 0
	for _, from := range fromIDs {
		q, err := s.repo.GetTournamentByID(ctx, from)
		if err != nil {
			return n, err
		}
		if q == nil || q.Stage != models.StageQualifier {
			return n, fmt.Errorf("турнир %d — не квалификация", from)
		}
		if q.Status != models.TournamentStatusCompleted {
			return n, fmt.Errorf("квалификация «%s» ещё не завершена", q.Name)
		}
		entries, err := s.repo.GetTournamentEntries(ctx, from)
		if err != nil {
			return n, err
		}
		for _, e := range entries {
			if e.Status != models.EntryQualified {
				continue
			}
			fromID := from
			if err := s.repo.EnterTeam(ctx, mainID, e.TeamID, models.SourceQualified, &fromID); err != nil {
				return n, err
			}
			n++
		}
	}
	s.emit("tournaments_update", map[string]any{"action": "entries", "id": mainID})
	s.emit("team_update", map[string]any{"action": "entries"})
	return n, nil
}

// InviteTeam enters a team into the main stage by an organiser's decision.
func (s *TelegramServiceImpl) InviteTeam(ctx context.Context, mainID, teamID int) error {
	if _, err := s.openMainStage(ctx, mainID); err != nil {
		return err
	}
	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if err := s.repo.EnterTeam(ctx, mainID, teamID, models.SourceInvited, nil); err != nil {
		return err
	}
	s.emit("tournaments_update", map[string]any{"action": "entries", "id": mainID})
	s.emit("team_update", map[string]any{"action": "invited", "team_id": teamID})
	return nil
}

// entryRefusal is why a captain cannot enter the team into t, or nil. The
// main stage takes only qualified and invited teams; a team that already
// qualified cannot take a second qualifier's place.
func (s *TelegramServiceImpl) entryRefusal(ctx context.Context, t *models.TelegramTournament, teamID int) error {
	switch t.Stage {
	case models.StageMain:
		return errMainStageClosed
	case models.StageQualifier:
		has, err := s.repo.HasQualifiedEntry(ctx, teamID, t.ID)
		if err != nil {
			return err
		}
		if has {
			return errAlreadyQualified
		}
	}
	return nil
}

// TeamQualifiedElsewhere reports whether the captain's team already advanced
// out of a qualifier other than tournamentID, and so takes no part in it.
func (s *TelegramServiceImpl) TeamQualifiedElsewhere(ctx context.Context, tgID int64, tournamentID int) bool {
	p, err := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil || p == nil || p.TeamID == nil {
		return false
	}
	has, _ := s.repo.HasQualifiedEntry(ctx, *p.TeamID, tournamentID)
	return has
}

// RegistrationInvite is one captain to tell that a tournament's registration
// is open.
type RegistrationInvite struct {
	ChatID   int64
	TeamName string
	// Qualified teams already advanced from another qualifier: they are told
	// they need not enter.
	Qualified bool
}

// RegistrationInvites lists every team captain for a tournament's
// registration announcement. A main stage takes no registrations.
func (s *TelegramServiceImpl) RegistrationInvites(ctx context.Context, tournamentID int) (*models.TelegramTournament, []RegistrationInvite, error) {
	t, err := s.repo.GetTournamentByID(ctx, tournamentID)
	if err != nil {
		return nil, nil, err
	}
	if t == nil {
		return nil, nil, errors.New("турнир не найден")
	}
	if !tournamentAcceptsEntries(t) {
		return nil, nil, errors.New("регистрация на этот турнир закрыта")
	}
	if t.Stage == models.StageMain {
		return nil, nil, errMainStageClosed
	}
	captains, err := s.repo.GetAllCaptains(ctx)
	if err != nil {
		return nil, nil, err
	}
	var out []RegistrationInvite
	seen := map[int64]bool{}
	for _, c := range captains {
		if c.TelegramID == nil || *c.TelegramID <= 0 || c.TeamID == nil || seen[*c.TelegramID] {
			continue
		}
		seen[*c.TelegramID] = true
		team, _ := s.repo.GetTeamByID(ctx, *c.TeamID)
		if team == nil {
			continue
		}
		inv := RegistrationInvite{ChatID: *c.TelegramID, TeamName: team.Name}
		if t.Stage == models.StageQualifier {
			inv.Qualified, _ = s.repo.HasQualifiedEntry(ctx, team.ID, t.ID)
		}
		out = append(out, inv)
	}
	return t, out, nil
}
