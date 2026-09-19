package application

import (
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"blackwatch/pkg/sheets"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const KbNone = "empty"

const (
	// maxTeamNameLen mirrors telegram_teams.name VARCHAR(64).
	maxTeamNameLen = 64
	// Roster: slot 1 is captain, 2–5 main roster (5 total required), 6–7 optional substitutes.
	MainRosterSlots     = 5
	mainRosterSlots     = MainRosterSlots
	firstSubstituteSlot = 6
	maxTeamSlots        = 7
)

type TelegramService interface {
	RegisterUser(ctx context.Context, tgID int64, username, firstName string) string
	HandleUserInput(ctx context.Context, tgID int64, input string) (string, string)
	HandleRegAction(ctx context.Context, tgID int64, action, arg string) (string, string)

	StartSoloRegistration(ctx context.Context, tgID int64) (string, string)
	StartTeamRegistration(ctx context.Context, tgID int64) (string, string)
	StartEditPlayer(ctx context.Context, tgID int64, slot int) (string, string)
	StartProfileEdit(ctx context.Context, tgID int64) (string, string)
	GetPlayer(ctx context.Context, tgID int64) (*models.TelegramPlayer, error)
	StartReport(ctx context.Context, tgID int64) (string, string)
	SelectReportOpponent(ctx context.Context, tgID int64, opponentTeamID int) (string, string)
	SelectReportOpponentByName(ctx context.Context, tgID int64, name string) (string, string)
	SetReportScore(ctx context.Context, tgID int64, scoreStr string) (string, string)
	AddReportPhoto(ctx context.Context, tgID int64, photoFileID string) (string, string, int)
	SubmitReport(ctx context.Context, tgID int64) (string, string, *models.TelegramMatchReport, []models.BracketMatch)
	CancelReport(ctx context.Context, tgID int64) (string, string)
	ResetReportPhotos(ctx context.Context, tgID int64) (string, string)
	GetRecentMatchReports(ctx context.Context, limit int) (string, error)
	GetEligibleOpponents(ctx context.Context, tgID int64) ([]models.TelegramTeam, error)
	GetReportDraft(tgID int64) *MatchReportDraft

	DeleteTeam(ctx context.Context, tgID int64) string
	GetTeamInfo(ctx context.Context, tgID int64) (string, string)
	ToggleCheckIn(ctx context.Context, tgID int64) string

	SetRegistrationOpen(ctx context.Context, isOpen bool)
	RegistrationStatus(ctx context.Context) (open bool, reason string)
	GenerateTeamsCSV(ctx context.Context) ([]byte, error)
	ExportTeamsToSheet(ctx context.Context) (string, error)
	GetBroadcastList(ctx context.Context) ([]int64, error)
	AdminDeleteTeam(ctx context.Context, teamName string) string
	AdminResetUser(ctx context.Context, tgID int64) string
	HandleReport(ctx context.Context, tgID int64, photoFileID, caption string) string

	SetTournamentTime(ctx context.Context, t time.Time)
	GetTournamentTime(ctx context.Context) time.Time
	GetUncheckedTeams(ctx context.Context) ([]models.TelegramTeam, error)
	DisqualifyUnchecked(ctx context.Context) ([]models.TelegramTeam, error)
	AdminReinstateTeam(ctx context.Context, name string) string

	GetTeamsList(ctx context.Context) string
	AdminGetTeamDetails(ctx context.Context, name string) string
	GetCheckInStatus(ctx context.Context) string

	GenerateSoloPlayersCSV(ctx context.Context) ([]byte, error)
	GetSoloPlayersList(ctx context.Context) string

	GetBracket(ctx context.Context) ([]models.BracketMatch, error)
	GetTeamForPlayer(ctx context.Context, tgID int64) (*models.TelegramTeam, []models.TelegramPlayer, error)
	UpdateTeamPlayer(ctx context.Context, captainTgID int64, playerID int, nick, gameID, zoneID, role string) error
	GetCheckInSummary(ctx context.Context) (*models.CheckInSummary, error)
	ReportMatchDirect(ctx context.Context, reporterTgID int64, matchID int, myScore, oppScore int, photoFileIDs []string) (*models.TelegramMatchReport, error)
	// Two-sided result confirmation: see telegram_report_confirm.go.
	GetOpenReportForMatch(ctx context.Context, bracketMatchID int) (*models.TelegramMatchReport, error)
	ConfirmReport(ctx context.Context, actorTgID int64, reportID int) error
	DisputeReport(ctx context.Context, actorTgID int64, reportID int) error
	AutoConfirmExpiredReports(ctx context.Context) ([]models.TelegramMatchReport, error)
	SetWinnerDirect(ctx context.Context, matchID int, winnerTeamName string, winScore, loseScore int) error
	RollbackMatch(ctx context.Context, matchID int) error
	ChangeWinnerDirect(ctx context.Context, matchID int, newWinnerTeamName string, winScore, loseScore int) error

	// In-app registration & roster management (Пачка 4)
	CreateTeamInApp(ctx context.Context, captainTgID int64, teamName, nick, gameID, zoneID, role string) error
	AddTeamPlayer(ctx context.Context, captainTgID int64, nick, gameID, zoneID, role string, isSubstitute bool) (*models.TelegramPlayer, error)
	GenerateInviteToken(ctx context.Context, captainTgID int64) (string, error)
	JoinTeamByToken(ctx context.Context, playerTgID int64, token string) error
	KickTeamPlayer(ctx context.Context, captainTgID int64, playerID int) error
	TransferCaptain(ctx context.Context, captainTgID int64, playerID int) error
	DeleteTeamInApp(ctx context.Context, captainTgID int64) error
	LeaveTeam(ctx context.Context, playerTgID int64) error

	GetTeamCaptains(ctx context.Context, teamID int) ([]models.TelegramPlayer, error)
	SetMatchNotifier(fn func(ctx context.Context, chatID int64, text string, hasWebAppBtn bool))
	GetTeamDetails(ctx context.Context, teamID int) (*models.TelegramTeam, []models.TelegramPlayer, error)
	GetBracketMatchDetails(ctx context.Context, matchID int) (*BracketMatchDetails, error)

	// League & Tournaments
	CreateTournament(ctx context.Context, name, slug string, tTime *time.Time) (*models.TelegramTournament, error)
	GetActiveTournament(ctx context.Context) (*models.TelegramTournament, error)
	GetTournamentByID(ctx context.Context, id int) (*models.TelegramTournament, error)
	GetAllTournaments(ctx context.Context) ([]models.TelegramTournament, error)
	SetActiveTournament(ctx context.Context, id int) error
	FinishTournament(ctx context.Context, id int) error
	GetLeagueStandings(ctx context.Context) ([]models.LeagueStanding, error)
	RegisterTeamForTournament(ctx context.Context, captainTgID int64, tournamentID int) error
	UnregisterTeamFromTournament(ctx context.Context, captainTgID int64, tournamentID int) error
	GetBracketForTournament(ctx context.Context, tournamentID int) ([]models.BracketMatch, error)
	GetTournamentTeamStatus(ctx context.Context, captainTgID int64, tournamentID int) (*models.TournamentTeam, error)
}

type BracketMatchDetails struct {
	Match         models.BracketMatch     `json:"match"`
	ScheduledTime string                  `json:"scheduled_time,omitempty"`
	RoundName     string                  `json:"round_name,omitempty"`
	MatchFormat   string                  `json:"match_format,omitempty"`
	Team1         *models.TelegramTeam    `json:"team1,omitempty"`
	Team1Members  []models.TelegramPlayer `json:"team1_members,omitempty"`
	Team2         *models.TelegramTeam    `json:"team2,omitempty"`
	Team2Members  []models.TelegramPlayer `json:"team2_members,omitempty"`
}

type TelegramServiceImpl struct {
	repo           repository.Telegram
	mu             sync.RWMutex
	tournamentTime time.Time
	logger         Logger
	// now is swapped in tests to move the clock around the tournament date.
	now func() time.Time
	// profiles is optional: with it, a captain linked to a Discord profile is
	// offered that profile's in-game data instead of typing it again.
	profiles ProfileLookup
	// bracket is optional: with it, /report is bound to the team's current
	// open match instead of accepting an arbitrary opponent.
	bracket *BracketService
	// sheets is optional: with it and spreadsheetID, rosters and bracket runs
	// can be published to Google Sheets.
	sheets        sheets.Client
	spreadsheetID string

	reportMu     sync.RWMutex
	reportDrafts map[int64]*MatchReportDraft

	notifyMatchFunc func(ctx context.Context, chatID int64, text string, hasWebAppBtn bool)
}

// ProfileLookup is the slice of the profile-link repository registration
// needs: who is this Telegram account, in game?
type ProfileLookup interface {
	GetLinkByTelegramID(ctx context.Context, telegramID int64) (*models.ProfileLink, error)
	UpdateTelegramProfile(ctx context.Context, telegramID int64, nickname, gameID, zoneID string, stars int, role string) error
}

// WithProfileLookup enables prefilling from linked Discord profiles.
func (s *TelegramServiceImpl) WithProfileLookup(p ProfileLookup) *TelegramServiceImpl {
	s.profiles = p
	return s
}

// WithBracket binds match reports to the active tournament bracket.
func (s *TelegramServiceImpl) WithBracket(b *BracketService) *TelegramServiceImpl {
	s.bracket = b
	return s
}

func NewTelegramServiceImpl(repo repository.Telegram, logger Logger) *TelegramServiceImpl {
	return &TelegramServiceImpl{
		repo:         repo,
		logger:       logger,
		now:          time.Now,
		reportDrafts: make(map[int64]*MatchReportDraft),
	}
}

func (s *TelegramServiceImpl) SetMatchNotifier(fn func(ctx context.Context, chatID int64, text string, hasWebAppBtn bool)) {
	s.notifyMatchFunc = fn
}

func (s *TelegramServiceImpl) GetTeamCaptains(ctx context.Context, teamID int) ([]models.TelegramPlayer, error) {
	members, err := s.repo.GetTeamMembers(ctx, teamID)
	if err != nil {
		return nil, err
	}
	var caps []models.TelegramPlayer
	for _, m := range members {
		if m.IsCaptain && m.TelegramID != nil && *m.TelegramID > 0 {
			caps = append(caps, m)
		}
	}
	return caps, nil
}

// logWrite reports a failed repository write.
//
// The registration flow is a state machine persisted in the database, and every
// write here used to discard its error: a failed save advanced the user to the
// next question anyway, so their answer was lost with no trace. The flow still
// continues — interrupting a half-finished registration would be worse — but the
// failure is now visible.
func (s *TelegramServiceImpl) logWrite(op string, err error) {
	if err != nil {
		s.logger.Error("telegram: %s failed: %v", op, err)
	}
}

func (s *TelegramServiceImpl) RegisterUser(ctx context.Context, tgID int64, username, firstName string) string {
	p := &models.TelegramPlayer{TelegramID: &tgID, TelegramUsername: username, FirstName: firstName}
	s.logWrite("CreateOrUpdatePlayer", s.repo.CreateOrUpdatePlayer(ctx, p))
	return fmt.Sprintf("Привет, %s!", firstName)
}

func (s *TelegramServiceImpl) HandleUserInput(ctx context.Context, tgID int64, input string) (string, string) {
	player, err := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil {
		s.logger.Error("telegram: GetPlayerByTelegramID failed for %d: %v", tgID, err)
		return "Произошла ошибка при загрузке данных. Попробуйте снова или используйте /start.", KbNone
	}
	if player == nil {
		return "Используйте /start для начала.", KbNone
	}

	if input == "Отмена" || input == "/cancel" {
		if isRegistrationState(player.FSMState) {
			return s.HandleRegAction(ctx, tgID, "cancel", "")
		}
		if isReportState(player.FSMState) {
			return s.CancelReport(ctx, tgID)
		}
		s.setState(ctx, tgID, models.StateIdle)
		return "Действие отменено. Возврат в меню.", KbNone
	}

	if isLegacyState(player.FSMState) {
		s.setState(ctx, tgID, models.StateIdle)
		return msgLegacyReset, KbNone
	}
	if isRegistrationState(player.FSMState) {
		return s.handleRegistrationText(ctx, player, input)
	}
	if isReportState(player.FSMState) {
		return s.handleReportText(ctx, player, input)
	}
	return "Используйте меню для управления.", KbNone
}

func (s *TelegramServiceImpl) StartSoloRegistration(ctx context.Context, tgID int64) (string, string) {
	if open, reason := s.RegistrationStatus(ctx); !open {
		return reason, KbNone
	}
	if name := s.currentTeamName(ctx, tgID); name != "" {
		return fmt.Sprintf("Вы уже в команде '%s'. Соло-регистрация недоступна, пока вы в команде (/delete_team).", name), KbNone
	}
	s.setState(ctx, tgID, models.StateSoloLine)
	if known, ok := s.knownProfile(ctx, tgID); ok {
		return prefillPrompt(known), KbRegPrefill
	}
	return soloLinePrompt, KbRegCancel
}

func (s *TelegramServiceImpl) StartTeamRegistration(ctx context.Context, tgID int64) (string, string) {
	if open, reason := s.RegistrationStatus(ctx); !open {
		return reason, KbNone
	}
	// A second /reg_team used to create a fresh team and repoint the captain,
	// leaving the old roster orphaned.
	if name := s.currentTeamName(ctx, tgID); name != "" {
		return fmt.Sprintf("Вы уже в команде '%s'. Чтобы зарегистрировать новую, сначала удалите её: /delete_team", name), KbNone
	}
	s.setState(ctx, tgID, models.StateWaitingTeamName)
	return "Введите название команды:", KbRegCancel
}

func (s *TelegramServiceImpl) StartEditPlayer(ctx context.Context, tgID int64, slot int) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде.", KbNone
	}
	if !p.IsCaptain {
		return "Только капитан может редактировать состав.", KbNone
	}
	if p.FSMState != models.StateIdle {
		return "Сначала завершите текущее действие или нажмите Отмена.", KbNone
	}
	members := s.roster(ctx, *p.TeamID)
	if slot < 1 || slot > len(members) {
		return fmt.Sprintf("Игрок №%d не найден. В команде %d игрок(ов).", slot, len(members)), KbNone
	}
	// /edit_player N is the typed form of the slot N button on the /my_team card.
	return s.HandleRegAction(ctx, tgID, "fix", strconv.Itoa(slot))
}

func (s *TelegramServiceImpl) GetPlayer(ctx context.Context, tgID int64) (*models.TelegramPlayer, error) {
	return s.repo.GetPlayerByTelegramID(ctx, tgID)
}

func (s *TelegramServiceImpl) GetTeamInfo(ctx context.Context, tgID int64) (string, string) {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return "Вы не в команде.", KbNone
	}
	return s.teamCard(ctx, p)
}

func (s *TelegramServiceImpl) ToggleCheckIn(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может делать Check-in."
	}
	t, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || t == nil {
		return "Команда не найдена."
	}
	if t.Status == models.TeamStatusDisqualified {
		return fmt.Sprintf("Команда '%s' снята с турнира (тех. поражение). Вернуть её могут только организаторы.", t.Name)
	}
	if !t.IsCheckedIn {
		members := s.roster(ctx, t.ID)
		if len(members) < mainRosterSlots {
			return fmt.Sprintf("Check-in невозможен: в команде %d из %d обязательных игроков. Доукомплектуйте состав (минимум %d игроков).", len(members), mainRosterSlots, mainRosterSlots)
		}
	}
	checkedIn := !t.IsCheckedIn
	if activeTourney, _ := s.repo.GetActiveTournament(ctx); activeTourney != nil {
		_ = s.repo.SetTournamentCheckIn(ctx, activeTourney.ID, t.ID, checkedIn)
	}
	if err := s.repo.SetCheckIn(ctx, t.ID, checkedIn); err != nil {
		s.logWrite("SetCheckIn", err)
		return "Не удалось изменить статус. Попробуйте ещё раз."
	}
	// It is a toggle, so the reply must say which way it went: a captain who
	// tapped twice used to un-check silently and collect a technical defeat.
	if checkedIn {
		return fmt.Sprintf("Check-in подтверждён. Команда '%s' участвует в турнире.", t.Name)
	}
	return fmt.Sprintf("Check-in снят. Команда '%s' НЕ подтверждена — нажмите кнопку ещё раз, чтобы подтвердить.", t.Name)
}

func (s *TelegramServiceImpl) DeleteTeam(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil || !p.IsCaptain {
		return "Только капитан может удалить команду."
	}
	id := *p.TeamID
	team, _ := s.repo.GetTeamByID(ctx, id)
	name := ""
	if team != nil {
		name = team.Name
	}
	s.logWrite("ReleaseTeamMembers", s.repo.ReleaseTeamMembers(ctx, id))
	s.logWrite("DeleteTeam", s.repo.DeleteTeam(ctx, id))
	if name != "" {
		return fmt.Sprintf("Команда '%s' удалена.", name)
	}
	return "Команда удалена."
}

func (s *TelegramServiceImpl) UpdateTeamPlayer(ctx context.Context, callerTgID int64, playerID int, nick, gameID, zoneID, role string) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, callerTgID)
	if err != nil || p == nil || p.TeamID == nil {
		return errors.New("вы не состоите в команде")
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return errors.New("редактирование заблокировано: команда уже прошла Check-in")
	}
	if open, _ := s.RegistrationStatus(ctx); !open {
		return errors.New("редактирование состава заблокировано: регистрация на турнир закрыта")
	}

	members, err := s.repo.GetTeamMembers(ctx, team.ID)
	if err != nil {
		return errors.New("не удалось получить состав команды")
	}

	var targetMember *models.TelegramPlayer
	for _, m := range members {
		if m.ID == playerID {
			targetMember = &m
			break
		}
	}
	if targetMember == nil {
		return errors.New("игрок не найден в составе вашей команды")
	}

	// Permission: captain can edit anyone; regular member can edit only themselves
	isSelf := targetMember.TelegramID != nil && *targetMember.TelegramID == callerTgID
	if !p.IsCaptain && !isSelf {
		return errors.New("только капитан команды может редактировать других участников")
	}

	nick = strings.TrimSpace(nick)
	gameID = strings.TrimSpace(gameID)
	zoneID = strings.TrimSpace(zoneID)
	role = strings.TrimSpace(role)

	if nick == "" {
		return errors.New("никнейм игрока не может быть пустым")
	}
	if gameID == "" {
		return errors.New("game ID игрока не может быть пустым")
	}

	if dup := s.duplicateGameID(ctx, gameID, playerID); dup != "" {
		return errors.New(dup)
	}

	s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, playerID, "game_nickname", nick))
	s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, playerID, "game_id", gameID))
	s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, playerID, "zone_id", zoneID))
	if role != "" {
		s.logWrite("UpdatePlayerFieldByID", s.repo.UpdatePlayerFieldByID(ctx, playerID, "main_role", role))
	}
	return nil
}

// CreateTeamInApp creates a new team, assigns the caller as captain, and populates captain details.
func (s *TelegramServiceImpl) CreateTeamInApp(ctx context.Context, captainTgID int64, teamName, nick, gameID, zoneID, role string) error {
	if open, reason := s.RegistrationStatus(ctx); !open {
		return errors.New(reason)
	}
	teamName = strings.TrimSpace(teamName)
	if teamName == "" {
		return errors.New("название команды не может быть пустым")
	}
	if len(teamName) > maxTeamNameLen {
		return fmt.Errorf("название команды слишком длинное (максимум %d символов)", maxTeamNameLen)
	}

	nick = strings.TrimSpace(nick)
	gameID = strings.TrimSpace(gameID)
	zoneID = strings.TrimSpace(zoneID)
	role = strings.TrimSpace(role)
	if nick == "" {
		return errors.New("укажите ваш игровой никнейм")
	}
	if gameID == "" {
		return errors.New("укажите ваш Game ID")
	}
	if zoneID == "" {
		return errors.New("укажите ваш Zone ID (Сервер)")
	}
	if role == "" {
		role = "Mid"
	}

	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil {
		return fmt.Errorf("не удалось получить данные игрока: %w", err)
	}
	if p == nil {
		p = &models.TelegramPlayer{TelegramID: &captainTgID}
		if err := s.repo.CreateOrUpdatePlayer(ctx, p); err != nil {
			return fmt.Errorf("не удалось создать профиль игрока: %w", err)
		}
		p, err = s.repo.GetPlayerByTelegramID(ctx, captainTgID)
		if err != nil || p == nil {
			return errors.New("не удалось инициализировать профиль игрока")
		}
	}
	if p.TeamID != nil {
		return errors.New("вы уже состоите в команде. Сначала покиньте или удалите текущую команду")
	}

	if dup := s.duplicateGameID(ctx, gameID, p.ID); dup != "" {
		return errors.New(dup)
	}

	team, err := s.repo.CreateTeam(ctx, teamName)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return errors.New("команда с таким названием уже существует")
		}
		return fmt.Errorf("не удалось создать команду: %w", err)
	}
	if err := s.repo.UpdatePlayerField(ctx, captainTgID, "team_id", team.ID); err != nil {
		return fmt.Errorf("не удалось присвоить капитана: %w", err)
	}
	if err := s.repo.UpdatePlayerField(ctx, captainTgID, "is_captain", true); err != nil {
		return fmt.Errorf("не удалось установить флаг капитана: %w", err)
	}
	_ = s.repo.UpdatePlayerField(ctx, captainTgID, "game_nickname", nick)
	_ = s.repo.UpdatePlayerField(ctx, captainTgID, "game_id", gameID)
	_ = s.repo.UpdatePlayerField(ctx, captainTgID, "zone_id", zoneID)
	_ = s.repo.UpdatePlayerField(ctx, captainTgID, "main_role", role)
	return nil
}

// AddTeamPlayer adds a teammate directly to the captain's team (roster row).
func (s *TelegramServiceImpl) AddTeamPlayer(ctx context.Context, captainTgID int64, nick, gameID, zoneID, role string, isSubstitute bool) (*models.TelegramPlayer, error) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil || !p.IsCaptain {
		return nil, errors.New("только капитан команды может добавлять игроков")
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return nil, errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return nil, errors.New("добавление игроков заблокировано: команда уже прошла Check-in")
	}
	if open, _ := s.RegistrationStatus(ctx); !open {
		return nil, errors.New("добавление игроков заблокировано: регистрация на турнир закрыта")
	}

	members, err := s.repo.GetTeamMembers(ctx, team.ID)
	if err != nil {
		return nil, errors.New("не удалось получить состав команды")
	}
	if len(members) >= maxTeamSlots {
		return nil, fmt.Errorf("в команде уже максимальное количество игроков (%d)", maxTeamSlots)
	}

	nick = strings.TrimSpace(nick)
	gameID = strings.TrimSpace(gameID)
	zoneID = strings.TrimSpace(zoneID)
	role = strings.TrimSpace(role)

	if nick == "" {
		return nil, errors.New("никнейм игрока не может быть пустым")
	}
	if gameID == "" {
		return nil, errors.New("game ID игрока не может быть пустым")
	}
	if zoneID == "" {
		return nil, errors.New("zone ID игрока не может быть пустым")
	}
	if role == "" {
		role = "Roam"
	}

	if dup := s.duplicateGameID(ctx, gameID, 0); dup != "" {
		return nil, errors.New(dup)
	}

	newPlayer := &models.TelegramPlayer{
		TeamID:       &team.ID,
		GameNickname: nick,
		GameID:       gameID,
		ZoneID:       zoneID,
		MainRole:     role,
		IsSubstitute: isSubstitute,
	}
	if err := s.repo.CreateTeammate(ctx, newPlayer); err != nil {
		return nil, fmt.Errorf("не удалось добавить игрока: %w", err)
	}
	return newPlayer, nil
}

// inviteKey returns the settings key used to store a team's active invite token.
func inviteKey(teamID int) string {
	return fmt.Sprintf("invite:%d", teamID)
}

// GenerateInviteToken creates (or rotates) the invite link token for the captain's team.
func (s *TelegramServiceImpl) GenerateInviteToken(ctx context.Context, captainTgID int64) (string, error) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil {
		return "", errors.New("вы не состоите в команде")
	}
	if !p.IsCaptain {
		return "", errors.New("только капитан может генерировать ссылку-приглашение")
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return "", errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return "", errors.New("приглашения заблокированы: команда уже прошла Check-in")
	}

	// 8 random characters prefixed by team ID for lookup. The token is the
	// whole secret behind a join link, so it comes from crypto/rand: the
	// previous clock-derived bytes were guessable from the issue time.
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("не удалось сгенерировать токен: %w", err)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	token := fmt.Sprintf("%d-%s", *p.TeamID, string(b))
	if err := s.repo.SetSetting(ctx, inviteKey(*p.TeamID), token); err != nil {
		return "", fmt.Errorf("не удалось сохранить токен: %w", err)
	}
	return token, nil
}

// JoinTeamByToken joins the caller to a team using a previously generated invite token.
func (s *TelegramServiceImpl) JoinTeamByToken(ctx context.Context, playerTgID int64, token string) error {
	if open, reason := s.RegistrationStatus(ctx); !open {
		return errors.New(reason)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("токен не может быть пустым")
	}
	// Token format: "<teamID>-<8chars>"
	parts := strings.SplitN(token, "-", 2)
	if len(parts) != 2 {
		return errors.New("неверный формат токена")
	}
	teamID, err := strconv.Atoi(parts[0])
	if err != nil || teamID <= 0 {
		return errors.New("неверный токен")
	}

	stored, err := s.repo.GetSetting(ctx, inviteKey(teamID))
	if err != nil || stored == "" {
		return errors.New("ссылка-приглашение недействительна или устарела")
	}
	if stored != token {
		return errors.New("ссылка-приглашение недействительна")
	}

	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return errors.New("команда уже прошла Check-in — присоединиться нельзя")
	}

	p, err := s.repo.GetPlayerByTelegramID(ctx, playerTgID)
	if err != nil {
		return fmt.Errorf("не удалось получить данные игрока: %w", err)
	}
	if p == nil {
		p = &models.TelegramPlayer{TelegramID: &playerTgID}
		if err := s.repo.CreateOrUpdatePlayer(ctx, p); err != nil {
			return fmt.Errorf("не удалось создать профиль игрока: %w", err)
		}
		p, err = s.repo.GetPlayerByTelegramID(ctx, playerTgID)
		if err != nil || p == nil {
			return errors.New("не удалось инициализировать профиль игрока")
		}
	}
	if p.TeamID != nil {
		if *p.TeamID == teamID {
			return errors.New("вы уже состоите в этой команде")
		}
		return errors.New("вы уже состоите в другой команде")
	}

	members, err := s.repo.GetTeamMembers(ctx, teamID)
	if err != nil {
		return fmt.Errorf("не удалось получить состав команды: %w", err)
	}
	if len(members) >= maxTeamSlots {
		return fmt.Errorf("команда заполнена (максимум %d игроков)", maxTeamSlots)
	}

	if err := s.repo.UpdatePlayerField(ctx, playerTgID, "team_id", teamID); err != nil {
		return fmt.Errorf("не удалось присоединиться к команде: %w", err)
	}
	return nil
}

// KickTeamPlayer removes a non-captain player from the captain's team.
func (s *TelegramServiceImpl) KickTeamPlayer(ctx context.Context, captainTgID int64, playerID int) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil || !p.IsCaptain {
		return errors.New("только капитан команды может исключить игрока")
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return errors.New("исключение заблокировано: команда уже прошла Check-in")
	}

	target, err := s.repo.GetPlayerByID(ctx, playerID)
	if err != nil || target == nil {
		return errors.New("игрок не найден")
	}
	if target.IsCaptain {
		return errors.New("нельзя исключить капитана. Сначала передайте капитанство другому игроку")
	}
	if target.TeamID == nil || *target.TeamID != *p.TeamID {
		return errors.New("этот игрок не состоит в вашей команде")
	}

	// Players with a Telegram account: detach. Roster-only rows: delete.
	if target.TelegramID != nil {
		if err := s.repo.UpdatePlayerFieldByID(ctx, playerID, "team_id", nil); err != nil {
			return fmt.Errorf("не удалось исключить игрока: %w", err)
		}
	} else {
		if err := s.repo.DeleteTeammate(ctx, playerID); err != nil {
			return fmt.Errorf("не удалось исключить игрока: %w", err)
		}
	}
	return nil
}

// TransferCaptain moves the captain role from the current captain to another team member.
func (s *TelegramServiceImpl) TransferCaptain(ctx context.Context, captainTgID int64, playerID int) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil || !p.IsCaptain {
		return errors.New("только капитан команды может передать капитанство")
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return errors.New("передача капитанства заблокирована: команда уже прошла Check-in")
	}

	target, err := s.repo.GetPlayerByID(ctx, playerID)
	if err != nil || target == nil {
		return errors.New("игрок не найден")
	}
	if target.TeamID == nil || *target.TeamID != *p.TeamID {
		return errors.New("этот игрок не состоит в вашей команде")
	}
	if target.TelegramID == nil {
		return errors.New("капитанство можно передать только игроку с аккаунтом Telegram")
	}
	if playerID == p.ID {
		return errors.New("вы уже являетесь капитаном")
	}

	// Strip old captain
	if err := s.repo.UpdatePlayerField(ctx, captainTgID, "is_captain", false); err != nil {
		return fmt.Errorf("не удалось снять флаг капитана: %w", err)
	}
	// Assign new captain
	if err := s.repo.UpdatePlayerFieldByID(ctx, playerID, "is_captain", true); err != nil {
		// Rollback: restore old captain
		_ = s.repo.UpdatePlayerField(ctx, captainTgID, "is_captain", true)
		return fmt.Errorf("не удалось назначить нового капитана: %w", err)
	}
	return nil
}

// DeleteTeamInApp removes the team and detaches all members.
func (s *TelegramServiceImpl) DeleteTeamInApp(ctx context.Context, captainTgID int64) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil {
		return errors.New("вы не состоите в команде")
	}
	if !p.IsCaptain {
		return errors.New("только капитан команды может удалить команду")
	}
	teamID := *p.TeamID
	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return errors.New("удаление заблокировано: сначала снимите Check-in")
	}
	s.logWrite("ReleaseTeamMembers", s.repo.ReleaseTeamMembers(ctx, teamID))
	if err := s.repo.DeleteTeam(ctx, teamID); err != nil {
		return fmt.Errorf("не удалось удалить команду: %w", err)
	}
	return nil
}

// LeaveTeam detaches a non-captain player from their current team.
func (s *TelegramServiceImpl) LeaveTeam(ctx context.Context, playerTgID int64) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, playerTgID)
	if err != nil || p == nil || p.TeamID == nil {
		return errors.New("вы не состоите в команде")
	}
	if p.IsCaptain {
		return errors.New("капитан не может покинуть команду. Передайте капитанство другому игроку или удалите команду")
	}
	team, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || team == nil {
		return errors.New("команда не найдена")
	}
	if team.IsCheckedIn {
		return errors.New("выход заблокирован: команда уже прошла Check-in")
	}
	return s.repo.UpdatePlayerFieldByID(ctx, p.ID, "team_id", nil)
}


// RegistrationCloseLead is how long before the tournament registration shuts
// itself: late sign-ups have no time to check in anyway.
const RegistrationCloseLead = time.Hour

// Values of the registration_open setting. The migration seeds "true", so a
// plain "true" means "automatic"; /open_reg writes "forced" to override the
// pre-tournament auto-close until /close_reg.
const (
	registrationAuto   = "true"
	registrationForced = "forced"
	registrationClosed = "false"
)

func (s *TelegramServiceImpl) SetRegistrationOpen(ctx context.Context, isOpen bool) {
	val := registrationClosed
	if isOpen {
		val = registrationForced
	}
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, "registration_open", val))
}

// RegistrationStatus reports whether sign-ups are accepted and, if not, the
// sentence to show the user.
func (s *TelegramServiceImpl) RegistrationStatus(ctx context.Context) (bool, string) {
	val, _ := s.repo.GetSetting(ctx, "registration_open")
	switch val {
	case registrationClosed:
		return false, "Регистрация закрыта."
	case registrationForced:
		return true, ""
	case registrationAuto, "":
	}
	start := s.GetTournamentTime(ctx)
	if !start.IsZero() && !s.now().Before(start.Add(-RegistrationCloseLead)) {
		return false, fmt.Sprintf("Регистрация закрыта: до турнира меньше часа (старт %s).", start.Format("15:04"))
	}
	return true, ""
}

func (s *TelegramServiceImpl) AdminDeleteTeam(ctx context.Context, name string) string {
	t, err := s.repo.GetTeamByName(ctx, name)
	if err != nil {
		return "Не найдена."
	}
	s.logWrite("ReleaseTeamMembers", s.repo.ReleaseTeamMembers(ctx, t.ID))
	s.logWrite("DeleteTeam", s.repo.DeleteTeam(ctx, t.ID))
	return "Удалена."
}

func (s *TelegramServiceImpl) AdminResetUser(ctx context.Context, id int64) string {
	s.logWrite("UpdatePlayerState", s.repo.UpdatePlayerState(ctx, id, models.StateIdle))
	return "Сброшен."
}

func (s *TelegramServiceImpl) GetBroadcastList(ctx context.Context) ([]int64, error) {
	caps, err := s.repo.GetAllCaptains(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, c := range caps {
		if c.TelegramID != nil {
			ids = append(ids, *c.TelegramID)
		}
	}
	return ids, nil
}

func (s *TelegramServiceImpl) GenerateTeamsCSV(ctx context.Context) ([]byte, error) {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return nil, err
	}
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write([]string{"Team", "Status", "CheckIn", "Nick", "ID", "Zone", "Role"})
	for _, t := range teams {
		for _, m := range t.Players {
			_ = w.Write([]string{t.Name, teamStatusLabel(t.Status), strconv.FormatBool(t.IsCheckedIn), m.GameNickname, m.GameID, m.ZoneID, m.MainRole})
		}
	}
	w.Flush()
	// csv.Writer buffers errors until Flush; without this check a failed export
	// came back as a silently truncated file.
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("failed to write teams CSV: %w", err)
	}
	return b.Bytes(), nil
}

func (s *TelegramServiceImpl) HandleReport(ctx context.Context, tgID int64, fileID, caption string) string {
	msg, _, _ := s.AddReportPhoto(ctx, tgID, fileID)
	return msg
}

func (s *TelegramServiceImpl) SetTournamentTime(ctx context.Context, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tournamentTime = t
	s.logWrite("SetSetting", s.repo.SetSetting(ctx, "tournament_time", t.Format(time.RFC3339)))
	if active, _ := s.repo.GetActiveTournament(ctx); active != nil {
		active.TournamentTime = &t
		_ = s.repo.UpdateTournament(ctx, active)
	}
}

func (s *TelegramServiceImpl) GetTournamentTime(ctx context.Context) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.tournamentTime.IsZero() {
		return s.tournamentTime
	}
	if active, _ := s.repo.GetActiveTournament(ctx); active != nil && active.TournamentTime != nil {
		return *active.TournamentTime
	}
	val, _ := s.repo.GetSetting(ctx, "tournament_time")
	if val != "" {
		t, _ := time.Parse(time.RFC3339, val)
		return t
	}
	return time.Time{}
}

func (s *TelegramServiceImpl) GetUncheckedTeams(ctx context.Context) ([]models.TelegramTeam, error) {
	var allTeams []models.TelegramTeam
	if active, _ := s.repo.GetActiveTournament(ctx); active != nil {
		tTeams, err := s.repo.GetTournamentTeams(ctx, active.ID)
		if err == nil && len(tTeams) > 0 {
			allTeams = tTeams
		}
	}
	if len(allTeams) == 0 {
		var err error
		allTeams, err = s.repo.GetAllTeams(ctx)
		if err != nil {
			return nil, err
		}
	}
	var unchecked []models.TelegramTeam
	for _, t := range allTeams {
		if !t.IsCheckedIn && t.Status != models.TeamStatusDisqualified {
			unchecked = append(unchecked, t)
		}
	}
	return unchecked, nil
}

// DisqualifyUnchecked marks every active team that missed check-in and
// returns them for the notifications. Already-disqualified teams are not
// reported again, so a repeated sweep is quiet.
func (s *TelegramServiceImpl) DisqualifyUnchecked(ctx context.Context) ([]models.TelegramTeam, error) {
	teams, err := s.GetUncheckedTeams(ctx)
	if err != nil {
		return nil, err
	}
	active, _ := s.repo.GetActiveTournament(ctx)
	for _, t := range teams {
		s.logWrite("SetTeamStatus", s.repo.SetTeamStatus(ctx, t.ID, models.TeamStatusDisqualified))
		if active != nil {
			_ = s.repo.SetTournamentTeamStatus(ctx, active.ID, t.ID, models.TeamStatusDisqualified)
		}
	}
	return teams, nil
}

// AdminReinstateTeam undoes a technical defeat. The admin has verified the
// team is present, so it comes back checked in.
func (s *TelegramServiceImpl) AdminReinstateTeam(ctx context.Context, name string) string {
	t, err := s.repo.GetTeamByName(ctx, name)
	if err != nil || t == nil {
		return fmt.Sprintf("Команда '%s' не найдена.", name)
	}
	s.logWrite("SetTeamStatus", s.repo.SetTeamStatus(ctx, t.ID, models.TeamStatusActive))
	s.logWrite("SetCheckIn", s.repo.SetCheckIn(ctx, t.ID, true))
	return fmt.Sprintf("Команда '%s' возвращена в турнир и отмечена как прошедшая check-in.", t.Name)
}

func teamStatusLabel(status string) string {
	if status == models.TeamStatusDisqualified {
		return "disqualified"
	}
	return "active"
}

func (s *TelegramServiceImpl) GetTeamsList(ctx context.Context) string {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return "Ошибка при получении списка команд."
	}
	if len(teams) == 0 {
		return "Команд пока нет."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Список команд (%d):\n\n", len(teams)))
	for i, t := range teams {
		check := "[ ]"
		switch {
		case t.Status == models.TeamStatusDisqualified:
			check = "[ТП]"
		case t.IsCheckedIn:
			check = "[+]"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s\n", i+1, check, t.Name))
	}
	return sb.String()
}

func (s *TelegramServiceImpl) AdminGetTeamDetails(ctx context.Context, name string) string {
	team, err := s.repo.GetTeamByName(ctx, name)
	if err != nil {
		return fmt.Sprintf("Команда '%s' не найдена.", name)
	}

	status := "Не подтверждена"
	switch {
	case team.Status == models.TeamStatusDisqualified:
		status = "Снята с турнира (тех. поражение) — вернуть: /reinstate " + team.Name
	case team.IsCheckedIn:
		status = "Подтверждена"
	}

	res := fmt.Sprintf("Команда: %s\nСтатус: %s\nID команды: %d\n\n", team.Name, status, team.ID)
	for i, m := range team.Players {
		role := "Основа"
		if m.IsSubstitute {
			role = "Замена"
		}
		res += fmt.Sprintf("%d. %s [%s]\n   ID: %s (%s)\n   TG: %s\n\n", i+1, m.GameNickname, role, m.GameID, m.ZoneID, m.TelegramUsername)
	}
	return res
}

func (s *TelegramServiceImpl) GetCheckInStatus(ctx context.Context) string {
	teams, err := s.repo.GetAllTeams(ctx)
	if err != nil {
		return "Ошибка при получении данных команд."
	}
	if len(teams) == 0 {
		return "Команд пока нет."
	}

	var checkedIn, pending, incomplete, disqualified []models.TelegramTeam

	for _, t := range teams {
		switch {
		case t.Status == models.TeamStatusDisqualified:
			disqualified = append(disqualified, t)
		case len(t.Players) < mainRosterSlots:
			incomplete = append(incomplete, t)
		case t.IsCheckedIn:
			checkedIn = append(checkedIn, t)
		default:
			pending = append(pending, t)
		}
	}

	totalEligible := len(checkedIn) + len(pending)
	percent := 0.0
	if totalEligible > 0 {
		percent = float64(len(checkedIn)) / float64(totalEligible) * 100
	}

	var sb strings.Builder
	sb.WriteString("Дашборд Check-in:\n\n")
	sb.WriteString(fmt.Sprintf("Готовы к игре: %d из %d (%.0f%%)\n", len(checkedIn), totalEligible, percent))
	if len(pending) > 0 {
		sb.WriteString(fmt.Sprintf("Ожидают подтверждения: %d\n", len(pending)))
	}
	if len(incomplete) > 0 {
		sb.WriteString(fmt.Sprintf("Не укомплектованы: %d\n", len(incomplete)))
	}

	formatCaptain := func(t models.TelegramTeam) string {
		for _, m := range t.Players {
			if m.IsCaptain {
				nick := m.GameNickname
				if nick == "" {
					nick = m.FirstName
				}
				if m.TelegramUsername != "" {
					return fmt.Sprintf("%s (@%s)", nick, strings.TrimPrefix(m.TelegramUsername, "@"))
				}
				if nick != "" {
					return nick
				}
				return "Капитан"
			}
		}
		return "Не назначен"
	}

	formatPlayers := func(n int) string {
		if n > mainRosterSlots {
			return fmt.Sprintf("%d чел. (+%d зам.)", n, n-mainRosterSlots)
		}
		return fmt.Sprintf("%d чел.", n)
	}

	if len(checkedIn) > 0 {
		sb.WriteString(fmt.Sprintf("\nПодтвердили участие (%d):\n", len(checkedIn)))
		for i, t := range checkedIn {
			sb.WriteString(fmt.Sprintf("%d. [+] %s — Кап: %s (%s)\n", i+1, t.Name, formatCaptain(t), formatPlayers(len(t.Players))))
		}
	}

	if len(pending) > 0 {
		sb.WriteString(fmt.Sprintf("\nНе подтвердили (%d):\n", len(pending)))
		for i, t := range pending {
			sb.WriteString(fmt.Sprintf("%d. [-] %s — Кап: %s (%s)\n", i+1, t.Name, formatCaptain(t), formatPlayers(len(t.Players))))
		}
	}

	if len(incomplete) > 0 {
		sb.WriteString(fmt.Sprintf("\nНеполный состав (%d):\n", len(incomplete)))
		for i, t := range incomplete {
			sb.WriteString(fmt.Sprintf("%d. [!] %s — Кап: %s (состав: %d/%d)\n", i+1, t.Name, formatCaptain(t), len(t.Players), mainRosterSlots))
		}
	}

	if len(disqualified) > 0 {
		sb.WriteString(fmt.Sprintf("\nДисквалифицированы (%d):\n", len(disqualified)))
		for i, t := range disqualified {
			sb.WriteString(fmt.Sprintf("%d. [ТП] %s\n", i+1, t.Name))
		}
	}

	return sb.String()
}

func (s *TelegramServiceImpl) GenerateSoloPlayersCSV(ctx context.Context) ([]byte, error) {
	players, err := s.repo.GetSoloPlayers(ctx)
	if err != nil {
		return nil, err
	}

	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	_ = w.Write([]string{"TG Username", "Nickname", "Game ID", "Zone ID", "Stars", "Role", "First Name"})

	for _, p := range players {
		record := []string{
			p.TelegramUsername,
			p.GameNickname,
			p.GameID,
			p.ZoneID,
			fmt.Sprintf("%d", p.Stars),
			p.MainRole,
			p.FirstName,
		}
		_ = w.Write(record)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("failed to write solo players CSV: %w", err)
	}
	return b.Bytes(), nil
}

func (s *TelegramServiceImpl) GetSoloPlayersList(ctx context.Context) string {
	players, err := s.repo.GetSoloPlayers(ctx)
	if err != nil || len(players) == 0 {
		return "Соло-игроков пока нет."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Соло-игроки (%d):\n\n", len(players)))
	for i, p := range players {
		sb.WriteString(fmt.Sprintf("%d. %s (@%s) — %s\n", i+1, p.GameNickname, p.TelegramUsername, p.MainRole))
	}
	return sb.String()
}

// currentTeamName returns the name of the team the user belongs to, or "".
func (s *TelegramServiceImpl) currentTeamName(ctx context.Context, tgID int64) string {
	p, _ := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if p == nil || p.TeamID == nil {
		return ""
	}
	t, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || t == nil {
		return ""
	}
	return t.Name
}

// parseStars accepts a non-negative integer star count. Anything else used to
// be stored as 0 without a word to the user.
func parseStars(input string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (s *TelegramServiceImpl) GetBracket(ctx context.Context) ([]models.BracketMatch, error) {
	return s.repo.GetBracketMatches(ctx)
}

func (s *TelegramServiceImpl) GetTeamForPlayer(ctx context.Context, tgID int64) (*models.TelegramTeam, []models.TelegramPlayer, error) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, tgID)
	if err != nil || p == nil || p.TeamID == nil {
		return nil, nil, err
	}
	t, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || t == nil {
		return nil, nil, err
	}
	members, err := s.repo.GetTeamMembers(ctx, *p.TeamID)
	if err != nil {
		return t, nil, err
	}
	return t, members, nil
}

func (s *TelegramServiceImpl) GetCheckInSummary(ctx context.Context) (*models.CheckInSummary, error) {
	var teams []models.TelegramTeam
	if active, _ := s.repo.GetActiveTournament(ctx); active != nil {
		tTeams, err := s.repo.GetTournamentTeams(ctx, active.ID)
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

	summary := &models.CheckInSummary{
		TotalTeams: len(teams),
		Debtors:    []models.DebtorTeam{},
	}

	for _, t := range teams {
		captainNick := ""
		captainUser := ""
		var captainTgID *int64
		for _, p := range t.Players {
			if p.IsCaptain {
				captainNick = p.GameNickname
				if captainNick == "" {
					captainNick = p.FirstName
				}
				captainUser = p.TelegramUsername
				captainTgID = p.TelegramID
				break
			}
		}

		status := "ok"
		switch {
		case t.Status == models.TeamStatusDisqualified:
			status = "disqualified"
			summary.DisqualifiedCount++
		case len(t.Players) < mainRosterSlots:
			status = "incomplete"
			summary.IncompleteCount++
		case !t.IsCheckedIn:
			status = "pending"
			summary.PendingCount++
		default:
			summary.CheckedInCount++
		}

		if status != "ok" {
			summary.Debtors = append(summary.Debtors, models.DebtorTeam{
				ID:                t.ID,
				Name:              t.Name,
				CaptainName:       captainNick,
				CaptainUsername:   captainUser,
				CaptainTelegramID: captainTgID,
				PlayersCount:      len(t.Players),
				Status:            status,
			})
		}
	}

	return summary, nil
}

// ReportMatchDirect records a result submitted from the mini app.
//
// photoFileIDs are Telegram file IDs, not raw images: the caller uploads the
// screenshots first so that everything downstream — the referee's media group,
// the archive in Postgres — keeps working with the same identifiers the bot
// flow produces.
func (s *TelegramServiceImpl) ReportMatchDirect(ctx context.Context, reporterTgID int64, matchID int, myScore, oppScore int, photoFileIDs []string) (*models.TelegramMatchReport, error) {
	if myScore < 0 || oppScore < 0 {
		return nil, errors.New("счёт не может быть отрицательным")
	}
	if myScore == oppScore {
		return nil, errors.New("в матче на выбывание счёт не может быть равным (должна быть победа одной из команд)")
	}

	p, err := s.repo.GetPlayerByTelegramID(ctx, reporterTgID)
	if err != nil || p == nil || p.TeamID == nil {
		return nil, errors.New("вы не состоите в команде")
	}
	if !p.IsCaptain {
		return nil, errors.New("только капитан команды может вносить результат матча")
	}

	myTeam, err := s.repo.GetTeamByID(ctx, *p.TeamID)
	if err != nil || myTeam == nil {
		return nil, errors.New("команда не найдена")
	}
	if myTeam.Status == models.TeamStatusDisqualified {
		return nil, errors.New("ваша команда дисквалифицирована")
	}

	var oppTeam *models.TelegramTeam
	var bracketMatch *models.BracketMatch

	// The cached bracket is the desk's source of truth whether or not a
	// Challonge client is configured; the client only matters for syncing.
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения сетки: %w", err)
	}
	if len(ms) > 0 {
		for i := range ms {
			if (matchID > 0 && ms[i].ID == matchID) || (matchID == 0 && ms[i].Ready() && ms[i].Has(myTeam.ID)) {
				bracketMatch = &ms[i]
				break
			}
		}
		if bracketMatch == nil {
			return nil, errors.New("открытый матч для вашей команды не найден в сетке")
		}
		oppID := bracketMatch.Opponent(myTeam.ID)
		if oppID == nil {
			return nil, errors.New("соперник в сетке ещё не определён")
		}
		oppTeam, err = s.repo.GetTeamByID(ctx, *oppID)
		if err != nil || oppTeam == nil {
			return nil, errors.New("команда соперника не найдена")
		}
	} else {
		opponents, err := s.GetEligibleOpponents(ctx, reporterTgID)
		if err != nil || len(opponents) == 0 {
			return nil, errors.New("нет доступных команд-соперников")
		}
		oppTeam = &opponents[0]
	}

	var winnerID, loserID int
	var winnerName, loserName string
	var winnerScore, loserScore int

	if myScore > oppScore {
		winnerID, loserID = myTeam.ID, oppTeam.ID
		winnerName, loserName = myTeam.Name, oppTeam.Name
		winnerScore, loserScore = myScore, oppScore
	} else {
		winnerID, loserID = oppTeam.ID, myTeam.ID
		winnerName, loserName = oppTeam.Name, myTeam.Name
		winnerScore, loserScore = oppScore, myScore
	}

	photoIDs := make([]string, 0, len(photoFileIDs))
	for _, id := range photoFileIDs {
		if id != "" {
			photoIDs = append(photoIDs, id)
		}
	}

	scoreStr := fmt.Sprintf("%d:%d", winnerScore, loserScore)
	rep := &models.TelegramMatchReport{
		ReporterTelegramID: reporterTgID,
		WinnerTeamID:       winnerID,
		WinnerTeamName:     winnerName,
		LoserTeamID:        loserID,
		LoserTeamName:      loserName,
		Score:              scoreStr,
		PhotoFileIDs:       photoIDs,
	}
	if bracketMatch == nil {
		// No bracket to move: the report is just a record for the referees.
		rep.Status = models.ReportConfirmed
		if err := s.repo.CreateMatchReport(ctx, rep); err != nil {
			s.logger.Error("telegram: ReportMatchDirect CreateMatchReport: %v", err)
			return nil, fmt.Errorf("ошибка сохранения отчёта: %w", err)
		}
		return rep, nil
	}

	// One captain's word does not move the bracket. The report waits for the
	// opposing captain to confirm or dispute it, or for the window to pass.
	open, err := s.repo.GetOpenReportForMatch(ctx, bracketMatch.ID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения отчётов: %w", err)
	}
	if open != nil {
		if open.Status == models.ReportDisputed {
			return nil, errors.New("результат этого матча оспорен — решение примет судья")
		}
		return nil, errors.New("результат уже внесён и ждёт подтверждения соперника")
	}
	rep.BracketMatchID = &bracketMatch.ID
	rep.Status = models.ReportPending
	expires := s.now().Add(ReportConfirmWindow)
	rep.ExpiresAt = &expires

	if err := s.repo.CreateMatchReport(ctx, rep); err != nil {
		s.logger.Error("telegram: ReportMatchDirect CreateMatchReport: %v", err)
		return nil, fmt.Errorf("ошибка сохранения отчёта: %w", err)
	}
	s.notifyPendingReport(ctx, rep, bracketMatch, myTeam.ID)
	return rep, nil
}

func (s *TelegramServiceImpl) SetWinnerDirect(ctx context.Context, matchID int, winnerTeamName string, winScore, loseScore int) error {
	team, err := s.repo.GetTeamByName(ctx, winnerTeamName)
	if err != nil || team == nil {
		return fmt.Errorf("команда '%s' не найдена", winnerTeamName)
	}
	if err := s.propagateBracketResult(ctx, matchID, team.ID, winScore, loseScore); err != nil {
		return err
	}
	s.closeOpenReport(ctx, matchID)
	return nil
}

func (s *TelegramServiceImpl) propagateBracketResult(ctx context.Context, matchID int, winnerTeamID int, winScore, loseScore int) error {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return err
	}

	var matchIdx = -1
	for i := range ms {
		if ms[i].ID == matchID || ms[i].PlayOrder == matchID {
			matchIdx = i
			break
		}
	}
	if matchIdx == -1 {
		return errors.New("матч не найден в сетке")
	}

	target := &ms[matchIdx]
	if target.Team1ID == nil || target.Team2ID == nil {
		return errors.New("у матча ещё не определены обе команды")
	}
	if *target.Team1ID != winnerTeamID && *target.Team2ID != winnerTeamID {
		return errors.New("команда не участвует в этом матче")
	}

	var loserTeamID int
	if *target.Team1ID == winnerTeamID {
		loserTeamID = *target.Team2ID
	} else {
		loserTeamID = *target.Team1ID
	}

	target.WinnerID = &winnerTeamID
	target.State = models.BracketComplete
	target.ScoresCSV = fmt.Sprintf("%d-%d", winScore, loseScore)

	// Propagate winner to next round in single elimination tree
	curRound := target.Round
	nextRound := curRound + 1

	var curRoundIndices []int
	var nextRoundIndices []int
	for i := range ms {
		if ms[i].Round == curRound {
			curRoundIndices = append(curRoundIndices, i)
		} else if ms[i].Round == nextRound {
			nextRoundIndices = append(nextRoundIndices, i)
		}
	}

	posInRound := -1
	for idx, mIdx := range curRoundIndices {
		if mIdx == matchIdx {
			posInRound = idx
			break
		}
	}

	var nextMatch *models.BracketMatch
	var nextMatchBecameReady bool
	var waitingTeamID *int

	if posInRound != -1 && len(nextRoundIndices) > 0 {
		targetNextIdx := posInRound / 2
		if targetNextIdx < len(nextRoundIndices) {
			nextMatch = &ms[nextRoundIndices[targetNextIdx]]
			if posInRound%2 == 0 {
				nextMatch.Team1ID = &winnerTeamID
			} else {
				nextMatch.Team2ID = &winnerTeamID
			}
			if nextMatch.Team1ID != nil && nextMatch.Team2ID != nil {
				nextMatch.State = models.BracketOpen
				nextMatchBecameReady = true
				if *nextMatch.Team1ID == winnerTeamID {
					waitingTeamID = nextMatch.Team2ID
				} else {
					waitingTeamID = nextMatch.Team1ID
				}
			}
		}
	}

	if err := s.repo.ReplaceBracketMatches(ctx, ms); err != nil {
		return err
	}

	if s.bracket != nil {
		wTeam, _ := s.repo.GetTeamByID(ctx, winnerTeamID)
		if wTeam != nil {
			_, _ = s.bracket.SetWinner(ctx, target.PlayOrder, wTeam.Name, winScore, loseScore)
		}
	}

	// Send Notifications to Captains
	s.sendMatchResultNotifications(ctx, target, winnerTeamID, loserTeamID, winScore, loseScore, nextMatch, nextMatchBecameReady, waitingTeamID, len(nextRoundIndices) == 0)

	return nil
}

func (s *TelegramServiceImpl) sendMatchResultNotifications(ctx context.Context, finishedMatch *models.BracketMatch, winnerID, loserID int, winScore, loseScore int, nextMatch *models.BracketMatch, nextMatchReady bool, waitingTeamID *int, isFinal bool) {
	if s.notifyMatchFunc == nil {
		return
	}

	winnerTeam, _ := s.repo.GetTeamByID(ctx, winnerID)
	loserTeam, _ := s.repo.GetTeamByID(ctx, loserID)
	winnerName := fmt.Sprintf("Команда #%d", winnerID)
	if winnerTeam != nil {
		winnerName = winnerTeam.Name
	}
	loserName := fmt.Sprintf("Команда #%d", loserID)
	if loserTeam != nil {
		loserName = loserTeam.Name
	}

	winnerMembers, _ := s.repo.GetTeamMembers(ctx, winnerID)
	loserMembers, _ := s.repo.GetTeamMembers(ctx, loserID)

	notifyTeamMembers := func(members []models.TelegramPlayer, msg string, hasWebApp bool) {
		seen := make(map[int64]bool)
		for _, m := range members {
			if m.TelegramID != nil && *m.TelegramID > 0 && !seen[*m.TelegramID] {
				seen[*m.TelegramID] = true
				s.notifyMatchFunc(ctx, *m.TelegramID, msg, hasWebApp)
			}
		}
	}

	// 1. Notify loser team members
	loserMsg := fmt.Sprintf("Матч #%d завершён.\n\nРезультат: %s vs %s (%d:%d).\nВаша команда выбывает из турнира.",
		finishedMatch.PlayOrder, winnerName, loserName, winScore, loseScore)
	notifyTeamMembers(loserMembers, loserMsg, false)

	// 2. If this was the Grand Final
	if isFinal {
		finalMsg := fmt.Sprintf("Победа в финале!\n\nКоманда «%s» одержала победу в гранд-финале турнира со счётом %d:%d.",
			winnerName, winScore, loseScore)
		notifyTeamMembers(winnerMembers, finalMsg, true)
		return
	}

	// 3. If next match is ready (both teams determined)
	if nextMatch != nil && nextMatchReady && waitingTeamID != nil {
		waitingTeam, _ := s.repo.GetTeamByID(ctx, *waitingTeamID)
		waitingName := fmt.Sprintf("Команда #%d", *waitingTeamID)
		if waitingTeam != nil {
			waitingName = waitingTeam.Name
		}
		waitingMembers, _ := s.repo.GetTeamMembers(ctx, *waitingTeamID)

		// Message to winner:
		winnerMsg := fmt.Sprintf("Победа со счётом %d:%d.\n\nКоманда «%s» выходит в Раунд %d.\nСледующий соперник: «%s» (Матч #%d).\n\nПерейдите в приложение для подтверждения готовности к игре.",
			winScore, loseScore, winnerName, nextMatch.Round, waitingName, nextMatch.PlayOrder)
		notifyTeamMembers(winnerMembers, winnerMsg, true)

		// Message to waiting team (THEIR OPPONENT JUST FINISHED!):
		waitingMsg := fmt.Sprintf("Ваш следующий соперник определился: «%s».\n\nРаунд %d, Матч #%d: «%s» vs «%s».\nМатч готов к проведению. Подтвердите готовность в приложении.",
			winnerName, nextMatch.Round, nextMatch.PlayOrder, waitingName, winnerName)
		notifyTeamMembers(waitingMembers, waitingMsg, true)
		return
	}

	// 4. Next match is not ready yet (waiting for parallel match)
	if nextMatch != nil {
		winnerWaitMsg := fmt.Sprintf("Победа со счётом %d:%d.\n\nКоманда «%s» выходит в Раунд %d (Матч #%d).\nОжидаем завершения параллельного матча соперников. Уведомление придёт после определения пары.",
			winScore, loseScore, winnerName, nextMatch.Round, nextMatch.PlayOrder)
		notifyTeamMembers(winnerMembers, winnerWaitMsg, true)
	}
}

func (s *TelegramServiceImpl) RollbackMatch(ctx context.Context, matchID int) error {
	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return err
	}

	var matchIdx = -1
	for i := range ms {
		if ms[i].ID == matchID || ms[i].PlayOrder == matchID {
			matchIdx = i
			break
		}
	}
	if matchIdx == -1 {
		return errors.New("матч не найден в сетке")
	}

	target := &ms[matchIdx]
	if target.State != models.BracketComplete && target.WinnerID == nil {
		return errors.New("матч не завершён, откат не требуется")
	}

	prevWinnerID := target.WinnerID

	// Check downstream match in next round
	curRound := target.Round
	nextRound := curRound + 1

	var curRoundIndices []int
	var nextRoundIndices []int
	for i := range ms {
		if ms[i].Round == curRound {
			curRoundIndices = append(curRoundIndices, i)
		} else if ms[i].Round == nextRound {
			nextRoundIndices = append(nextRoundIndices, i)
		}
	}

	posInRound := -1
	for idx, mIdx := range curRoundIndices {
		if mIdx == matchIdx {
			posInRound = idx
			break
		}
	}

	if posInRound != -1 && len(nextRoundIndices) > 0 {
		targetNextIdx := posInRound / 2
		if targetNextIdx < len(nextRoundIndices) {
			nextMatch := &ms[nextRoundIndices[targetNextIdx]]
			if nextMatch.State == models.BracketComplete {
				return errors.New("невозможно откатить: матч следующего раунда уже завершён")
			}
			if prevWinnerID != nil {
				if nextMatch.Team1ID != nil && *nextMatch.Team1ID == *prevWinnerID {
					nextMatch.Team1ID = nil
				}
				if nextMatch.Team2ID != nil && *nextMatch.Team2ID == *prevWinnerID {
					nextMatch.Team2ID = nil
				}
			}
			nextMatch.State = models.BracketPending
		}
	}

	target.WinnerID = nil
	target.State = models.BracketOpen
	target.ScoresCSV = ""

	if err := s.repo.ReplaceBracketMatches(ctx, ms); err != nil {
		return err
	}
	// A rollback reopens the match for a fresh report; whatever was pending is void.
	s.closeOpenReport(ctx, target.ID)
	return nil
}

func (s *TelegramServiceImpl) ChangeWinnerDirect(ctx context.Context, matchID int, newWinnerTeamName string, winScore, loseScore int) error {
	team, err := s.repo.GetTeamByName(ctx, newWinnerTeamName)
	if err != nil || team == nil {
		return fmt.Errorf("команда '%s' не найдена", newWinnerTeamName)
	}

	ms, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return err
	}

	var matchIdx = -1
	for i := range ms {
		if ms[i].ID == matchID || ms[i].PlayOrder == matchID {
			matchIdx = i
			break
		}
	}
	if matchIdx == -1 {
		return errors.New("матч не найден в сетке")
	}

	target := &ms[matchIdx]
	if target.Team1ID == nil || target.Team2ID == nil {
		return errors.New("у матча ещё не определены обе команды")
	}
	if *target.Team1ID != team.ID && *target.Team2ID != team.ID {
		return errors.New("команда не участвует в этом матче")
	}

	oldWinnerID := target.WinnerID

	// Check downstream match
	curRound := target.Round
	nextRound := curRound + 1

	var curRoundIndices []int
	var nextRoundIndices []int
	for i := range ms {
		if ms[i].Round == curRound {
			curRoundIndices = append(curRoundIndices, i)
		} else if ms[i].Round == nextRound {
			nextRoundIndices = append(nextRoundIndices, i)
		}
	}

	posInRound := -1
	for idx, mIdx := range curRoundIndices {
		if mIdx == matchIdx {
			posInRound = idx
			break
		}
	}

	if posInRound != -1 && len(nextRoundIndices) > 0 {
		targetNextIdx := posInRound / 2
		if targetNextIdx < len(nextRoundIndices) {
			nextMatch := &ms[nextRoundIndices[targetNextIdx]]
			if nextMatch.State == models.BracketComplete {
				return errors.New("невозможно изменить победителя: матч следующего раунда уже завершён")
			}
			if oldWinnerID != nil {
				if nextMatch.Team1ID != nil && *nextMatch.Team1ID == *oldWinnerID {
					nextMatch.Team1ID = &team.ID
				} else if nextMatch.Team2ID != nil && *nextMatch.Team2ID == *oldWinnerID {
					nextMatch.Team2ID = &team.ID
				}
			} else {
				if posInRound%2 == 0 {
					nextMatch.Team1ID = &team.ID
				} else {
					nextMatch.Team2ID = &team.ID
				}
			}
			if nextMatch.Team1ID != nil && nextMatch.Team2ID != nil {
				nextMatch.State = models.BracketOpen
			}
		}
	}

	target.WinnerID = &team.ID
	target.State = models.BracketComplete
	target.ScoresCSV = fmt.Sprintf("%d-%d", winScore, loseScore)

	if err := s.repo.ReplaceBracketMatches(ctx, ms); err != nil {
		return err
	}

	if s.bracket != nil {
		_, _ = s.bracket.SetWinner(ctx, target.PlayOrder, newWinnerTeamName, winScore, loseScore)
	}
	s.closeOpenReport(ctx, target.ID)

	return nil
}

func (s *TelegramServiceImpl) GetTeamDetails(ctx context.Context, teamID int) (*models.TelegramTeam, []models.TelegramPlayer, error) {
	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil {
		return nil, nil, err
	}
	members, err := s.repo.GetTeamMembers(ctx, teamID)
	if err != nil {
		return nil, nil, err
	}
	return team, members, nil
}

func (s *TelegramServiceImpl) GetBracketMatchDetails(ctx context.Context, matchID int) (*BracketMatchDetails, error) {
	matches, err := s.GetBracket(ctx)
	if err != nil {
		return nil, err
	}

	var found *models.BracketMatch
	for _, m := range matches {
		if m.ID == matchID || m.PlayOrder == matchID {
			mCopy := m
			found = &mCopy
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("match not found")
	}

	totalRounds := TotalRounds(matches)
	rName := FormatRoundTitle(found.Round, totalRounds)
	sTime := ""
	s.mu.RLock()
	tTime := s.tournamentTime
	s.mu.RUnlock()
	if !tTime.IsZero() {
		sTime = FormatScheduledTime(tTime, found.Round, time.FixedZone("MSK", 3*3600))
	}
	mFormat := "BO1"
	if found.Round >= totalRounds-1 && totalRounds > 1 {
		mFormat = "BO3"
	}

	details := &BracketMatchDetails{
		Match:         *found,
		ScheduledTime: sTime,
		RoundName:     rName,
		MatchFormat:   mFormat,
	}

	if found.Team1ID != nil && *found.Team1ID > 0 {
		t1, m1, _ := s.GetTeamDetails(ctx, *found.Team1ID)
		details.Team1 = t1
		details.Team1Members = m1
	}
	if found.Team2ID != nil && *found.Team2ID > 0 {
		t2, m2, _ := s.GetTeamDetails(ctx, *found.Team2ID)
		details.Team2 = t2
		details.Team2Members = m2
	}

	return details, nil
}

func (s *TelegramServiceImpl) CreateTournament(ctx context.Context, name, slug string, tTime *time.Time) (*models.TelegramTournament, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("название турнира не может быть пустым")
	}
	if slug == "" {
		slug = "tourney_" + time.Now().UTC().Format("20060102_150405")
	}
	t := &models.TelegramTournament{
		Name:           name,
		Slug:           slug,
		Status:         models.TournamentStatusRegistration,
		TournamentTime: tTime,
		IsActive:       false,
	}
	// If no active tournament exists, make this one active
	active, _ := s.repo.GetActiveTournament(ctx)
	if active == nil {
		t.IsActive = true
	}
	created, err := s.repo.CreateTournament(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать турнир: %w", err)
	}
	return created, nil
}

func (s *TelegramServiceImpl) GetActiveTournament(ctx context.Context) (*models.TelegramTournament, error) {
	return s.repo.GetActiveTournament(ctx)
}

func (s *TelegramServiceImpl) GetTournamentByID(ctx context.Context, id int) (*models.TelegramTournament, error) {
	return s.repo.GetTournamentByID(ctx, id)
}

func (s *TelegramServiceImpl) GetAllTournaments(ctx context.Context) ([]models.TelegramTournament, error) {
	return s.repo.GetAllTournaments(ctx)
}

func (s *TelegramServiceImpl) SetActiveTournament(ctx context.Context, id int) error {
	t, err := s.repo.GetTournamentByID(ctx, id)
	if err != nil || t == nil {
		return errors.New("турнир не найден")
	}
	if err := s.repo.SetActiveTournament(ctx, id); err != nil {
		return err
	}
	if t.TournamentTime != nil {
		s.mu.Lock()
		s.tournamentTime = *t.TournamentTime
		s.mu.Unlock()
		_ = s.repo.SetSetting(ctx, "tournament_time", t.TournamentTime.Format(time.RFC3339))
	}
	if t.ChallongeID != nil {
		_ = s.repo.SetSetting(ctx, settingChallongeID, strconv.FormatInt(*t.ChallongeID, 10))
	} else {
		_ = s.repo.SetSetting(ctx, settingChallongeID, "")
	}
	_ = s.repo.SetSetting(ctx, settingChallongeURL, t.ChallongeURL)
	if t.ChallongeFor != nil {
		_ = s.repo.SetSetting(ctx, settingChallongeFor, t.ChallongeFor.Format(time.RFC3339))
	} else {
		_ = s.repo.SetSetting(ctx, settingChallongeFor, "")
	}
	return nil
}

func (s *TelegramServiceImpl) FinishTournament(ctx context.Context, id int) error {
	tourney, err := s.repo.GetTournamentByID(ctx, id)
	if err != nil || tourney == nil {
		return errors.New("турнир не найден")
	}

	matches, err := s.repo.GetBracketMatchesForTournament(ctx, id)
	if err != nil {
		return fmt.Errorf("не удалось получить матчи турнира: %w", err)
	}

	teams, err := s.repo.GetTournamentTeams(ctx, id)
	if err != nil {
		return fmt.Errorf("не удалось получить команды турнира: %w", err)
	}

	placements, points := CalculatePlacements(matches, teams)
	if err := s.repo.UpdateTournamentPlacements(ctx, id, placements, points); err != nil {
		return fmt.Errorf("не удалось сохранить очки турнира: %w", err)
	}

	if err := s.repo.UpdateTournamentStatus(ctx, id, models.TournamentStatusCompleted); err != nil {
		return fmt.Errorf("не удалось обновить статус турнира: %w", err)
	}

	return nil
}

func (s *TelegramServiceImpl) GetLeagueStandings(ctx context.Context) ([]models.LeagueStanding, error) {
	return s.repo.GetLeagueStandings(ctx)
}

func (s *TelegramServiceImpl) RegisterTeamForTournament(ctx context.Context, captainTgID int64, tournamentID int) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil || !p.IsCaptain {
		return errors.New("только капитан может зарегистрировать команду на этап")
	}
	tourney, err := s.repo.GetTournamentByID(ctx, tournamentID)
	if err != nil || tourney == nil {
		return errors.New("турнир не найден")
	}
	if tourney.Status != models.TournamentStatusRegistration && tourney.Status != models.TournamentStatusDraft {
		return errors.New("регистрация на этот этап закрыта")
	}
	return s.repo.RegisterTeamForTournament(ctx, tournamentID, *p.TeamID)
}

func (s *TelegramServiceImpl) UnregisterTeamFromTournament(ctx context.Context, captainTgID int64, tournamentID int) error {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil || !p.IsCaptain {
		return errors.New("только капитан может снять команду с этапа")
	}
	tourney, err := s.repo.GetTournamentByID(ctx, tournamentID)
	if err != nil || tourney == nil {
		return errors.New("турнир не найден")
	}
	if tourney.Status != models.TournamentStatusRegistration && tourney.Status != models.TournamentStatusDraft {
		return errors.New("нельзя снять команду: этап уже активен")
	}
	return s.repo.UnregisterTeamFromTournament(ctx, tournamentID, *p.TeamID)
}

func (s *TelegramServiceImpl) GetBracketForTournament(ctx context.Context, tournamentID int) ([]models.BracketMatch, error) {
	return s.repo.GetBracketMatchesForTournament(ctx, tournamentID)
}

func (s *TelegramServiceImpl) GetTournamentTeamStatus(ctx context.Context, captainTgID int64, tournamentID int) (*models.TournamentTeam, error) {
	p, err := s.repo.GetPlayerByTelegramID(ctx, captainTgID)
	if err != nil || p == nil || p.TeamID == nil {
		return nil, nil
	}
	return s.repo.GetTournamentTeam(ctx, tournamentID, *p.TeamID)
}



