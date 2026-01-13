package application

import (
	"fmt"
	"valhalla/internal/models"
	"valhalla/internal/repository"
)

type ProfileLinkService interface {
	GenerateLinkCode(playerName string) (string, error)
	GenerateLinkCodeByID(playerID int) (string, error)
	LinkTelegramAccount(code string, telegramID int64, telegramUsername string) error
	GetLinkedProfile(playerName string) (*LinkedProfile, error)
	GetLinkedProfileByTelegram(telegramID int64) (*LinkedProfile, error)
	UpdateTelegramData(telegramID int64, nickname, gameID, zoneID string, stars int, role string) error
	UnlinkByDiscordPlayer(playerName string) error
	UnlinkByTelegram(telegramID int64) error
}

type LinkedProfile struct {
	DiscordPlayerName string
	TelegramID        *int64
	TelegramUsername  string
	GameNickname      string
	GameID            string
	ZoneID            string
	Stars             int
	MainRole          string

	Wins    int
	Losses  int
	Kills   int
	Deaths  int
	Assists int
}

type ProfileLinkServiceImpl struct {
	profileRepo repository.ProfileLink
	matchRepo   repository.Match
	logger      Logger
}

func NewProfileLinkServiceImpl(profileRepo repository.ProfileLink, matchRepo repository.Match, logger Logger) *ProfileLinkServiceImpl {
	return &ProfileLinkServiceImpl{
		profileRepo: profileRepo,
		matchRepo:   matchRepo,
		logger:      logger,
	}
}

func (s *ProfileLinkServiceImpl) GenerateLinkCode(playerName string) (string, error) {
	playerID, err := s.matchRepo.EnsurePlayerExists(playerName)
	if err != nil {
		return "", fmt.Errorf("не удалось создать игрока: %w", err)
	}

	return s.GenerateLinkCodeByID(playerID)
}

func (s *ProfileLinkServiceImpl) GenerateLinkCodeByID(playerID int) (string, error) {
	existingLink, err := s.profileRepo.GetLinkByDiscordPlayer(playerID)
	if err != nil {
		return "", fmt.Errorf("ошибка проверки связи: %w", err)
	}
	if existingLink != nil && existingLink.TelegramID != nil {
		return "", fmt.Errorf("профиль уже привязан к Telegram @%s", existingLink.TelegramUsername)
	}

	code, err := s.profileRepo.CreateLinkCode(playerID)
	if err != nil {
		return "", fmt.Errorf("не удалось создать код: %w", err)
	}

	s.logger.Info("Generated link code for player ID: %d", playerID)
	return code, nil
}

func (s *ProfileLinkServiceImpl) LinkTelegramAccount(code string, telegramID int64, telegramUsername string) error {
	playerID, err := s.profileRepo.ValidateLinkCode(code)
	if err != nil {
		return err
	}

	existingByTelegram, err := s.profileRepo.GetLinkByTelegramID(telegramID)
	if err != nil {
		return fmt.Errorf("ошибка проверки Telegram: %w", err)
	}
	if existingByTelegram != nil {
		return fmt.Errorf("этот Telegram аккаунт уже привязан к другому профилю")
	}

	link := &models.ProfileLink{
		DiscordPlayerID:  playerID,
		TelegramID:       &telegramID,
		TelegramUsername: telegramUsername,
	}

	if err := s.profileRepo.CreateProfileLink(link); err != nil {
		return fmt.Errorf("не удалось создать связь: %w", err)
	}

	s.logger.Info("Linked Telegram %d (@%s) to Discord player ID %d", telegramID, telegramUsername, playerID)
	return nil
}

func (s *ProfileLinkServiceImpl) GetLinkedProfile(playerName string) (*LinkedProfile, error) {
	playerID, err := s.profileRepo.GetPlayerIDByName(playerName)
	if err != nil {
		return nil, fmt.Errorf("игрок не найден")
	}

	link, err := s.profileRepo.GetLinkByDiscordPlayer(playerID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil
	}

	wins, losses, kills, deaths, assists, err := s.profileRepo.GetDiscordStatsByPlayerID(playerID)
	if err != nil {
		s.logger.Warn("Failed to get Discord stats: %v", err)
	}

	return &LinkedProfile{
		DiscordPlayerName: playerName,
		TelegramID:        link.TelegramID,
		TelegramUsername:  link.TelegramUsername,
		GameNickname:      link.GameNickname,
		GameID:            link.GameID,
		ZoneID:            link.ZoneID,
		Stars:             link.Stars,
		MainRole:          link.MainRole,
		Wins:              wins,
		Losses:            losses,
		Kills:             kills,
		Deaths:            deaths,
		Assists:           assists,
	}, nil
}

func (s *ProfileLinkServiceImpl) GetLinkedProfileByTelegram(telegramID int64) (*LinkedProfile, error) {
	link, err := s.profileRepo.GetLinkByTelegramID(telegramID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil
	}

	playerName, err := s.matchRepo.GetPlayerNameByID(link.DiscordPlayerID)
	if err != nil {
		return nil, fmt.Errorf("Discord профиль не найден")
	}

	wins, losses, kills, deaths, assists, err := s.profileRepo.GetDiscordStatsByPlayerID(link.DiscordPlayerID)
	if err != nil {
		s.logger.Warn("Failed to get Discord stats: %v", err)
	}

	return &LinkedProfile{
		DiscordPlayerName: playerName,
		TelegramID:        link.TelegramID,
		TelegramUsername:  link.TelegramUsername,
		GameNickname:      link.GameNickname,
		GameID:            link.GameID,
		ZoneID:            link.ZoneID,
		Stars:             link.Stars,
		MainRole:          link.MainRole,
		Wins:              wins,
		Losses:            losses,
		Kills:             kills,
		Deaths:            deaths,
		Assists:           assists,
	}, nil
}

func (s *ProfileLinkServiceImpl) UpdateTelegramData(telegramID int64, nickname, gameID, zoneID string, stars int, role string) error {
	return s.profileRepo.UpdateTelegramProfile(telegramID, nickname, gameID, zoneID, stars, role)
}

func (s *ProfileLinkServiceImpl) UnlinkByDiscordPlayer(playerName string) error {
	playerID, err := s.profileRepo.GetPlayerIDByName(playerName)
	if err != nil {
		return fmt.Errorf("игрок не найден")
	}

	if err := s.profileRepo.DeleteLinkByDiscordPlayer(playerID); err != nil {
		return err
	}

	s.logger.Info("Unlinked Discord player: %s", playerName)
	return nil
}

func (s *ProfileLinkServiceImpl) UnlinkByTelegram(telegramID int64) error {
	if err := s.profileRepo.DeleteLinkByTelegramID(telegramID); err != nil {
		return err
	}

	s.logger.Info("Unlinked Telegram account: %d", telegramID)
	return nil
}
