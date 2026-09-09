package application

import (
	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"context"
	"errors"
	"fmt"
)

type ProfileLinkService interface {
	// Registering a player by name lives in MatchService.BindDiscordByName now:
	// the by-name variant here had no callers and created profiles nothing was
	// bound to.
	GenerateLinkCodeByID(ctx context.Context, playerID int) (string, error)
	LinkTelegramAccount(ctx context.Context, code string, telegramID int64, telegramUsername string) error
	GetLinkedProfile(ctx context.Context, playerName string) (*LinkedProfile, error)
	GetLinkedProfileByTelegram(ctx context.Context, telegramID int64) (*LinkedProfile, error)
	UpdateTelegramData(ctx context.Context, telegramID int64, nickname, gameID, zoneID string, stars int, role string) error
	UnlinkPlayerID(ctx context.Context, playerID int) error
	UnlinkByTelegram(ctx context.Context, telegramID int64) error
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

func (s *ProfileLinkServiceImpl) GenerateLinkCodeByID(ctx context.Context, playerID int) (string, error) {
	existingLink, err := s.profileRepo.GetLinkByDiscordPlayer(ctx, playerID)
	if err != nil {
		return "", fmt.Errorf("ошибка проверки связи: %w", err)
	}
	if existingLink != nil && existingLink.TelegramID != nil {
		return "", fmt.Errorf("профиль уже привязан к Telegram @%s", existingLink.TelegramUsername)
	}

	code, err := s.profileRepo.CreateLinkCode(ctx, playerID)
	if err != nil {
		return "", fmt.Errorf("не удалось создать код: %w", err)
	}

	s.logger.Info("Generated link code for player ID: %d", playerID)
	return code, nil
}

func (s *ProfileLinkServiceImpl) LinkTelegramAccount(ctx context.Context, code string, telegramID int64, telegramUsername string) error {
	playerID, err := s.profileRepo.ValidateLinkCode(ctx, code)
	if err != nil {
		return err
	}

	existingByTelegram, err := s.profileRepo.GetLinkByTelegramID(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("ошибка проверки Telegram: %w", err)
	}
	if existingByTelegram != nil {
		return fmt.Errorf("этот Telegram аккаунт уже привязан к другому профилю")
	}

	// players.tg_id is written first, and a failure here aborts the link.
	//
	// It is the column the betting code resolves a bettor through; profile_links
	// only ever held the game profile alongside it. Writing just profile_links —
	// which is what this did — left players.tg_id NULL, so every bet answered
	// "Telegram not linked to any player" no matter how many times the user
	// linked. Both platforms now hang off the same players row.
	if err := s.matchRepo.SetTelegramID(ctx, playerID, telegramID); err != nil {
		if errors.Is(err, repository.ErrTelegramIDTaken) {
			return fmt.Errorf("telegram %d: %w", telegramID, domain.ErrTelegramAlreadyLinked)
		}
		return fmt.Errorf("не удалось привязать Telegram к профилю: %w", err)
	}

	link := &models.ProfileLink{
		DiscordPlayerID:  playerID,
		TelegramID:       &telegramID,
		TelegramUsername: telegramUsername,
	}

	if err := s.profileRepo.CreateProfileLink(ctx, link); err != nil {
		// Roll the identity back so the two do not disagree. The bet path reads
		// players.tg_id, so leaving it set with no profile row behind it is the
		// half-linked state this whole change exists to remove.
		if clearErr := s.matchRepo.ClearTelegramID(ctx, playerID); clearErr != nil {
			s.logger.Error("link: player %d left with tg_id %d and no profile row: %v",
				playerID, telegramID, clearErr)
		}
		return fmt.Errorf("не удалось создать связь: %w", err)
	}

	s.logger.Info("Linked Telegram %d (@%s) to Discord player ID %d", telegramID, telegramUsername, playerID)
	return nil
}

func (s *ProfileLinkServiceImpl) GetLinkedProfile(ctx context.Context, playerName string) (*LinkedProfile, error) {
	playerID, err := s.profileRepo.GetPlayerIDByName(ctx, playerName)
	if err != nil {
		return nil, fmt.Errorf("игрок не найден")
	}

	link, err := s.profileRepo.GetLinkByDiscordPlayer(ctx, playerID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil
	}

	wins, losses, kills, deaths, assists, err := s.profileRepo.GetDiscordStatsByPlayerID(ctx, playerID)
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

func (s *ProfileLinkServiceImpl) GetLinkedProfileByTelegram(ctx context.Context, telegramID int64) (*LinkedProfile, error) {
	link, err := s.profileRepo.GetLinkByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil
	}

	playerName, err := s.matchRepo.GetPlayerNameByID(ctx, link.DiscordPlayerID)
	if err != nil {
		return nil, fmt.Errorf("linked player %d: %w", link.DiscordPlayerID, err)
	}

	wins, losses, kills, deaths, assists, err := s.profileRepo.GetDiscordStatsByPlayerID(ctx, link.DiscordPlayerID)
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

func (s *ProfileLinkServiceImpl) UpdateTelegramData(ctx context.Context, telegramID int64, nickname, gameID, zoneID string, stars int, role string) error {
	return s.profileRepo.UpdateTelegramProfile(ctx, telegramID, nickname, gameID, zoneID, stars, role)
}

// UnlinkPlayerID releases both halves of a player's Telegram binding: the
// identity on players.tg_id and the profile row behind it.
//
// It takes a player id rather than a name because every caller now resolves the
// player first — a self-service unlink through the caller's own Discord binding,
// or an admin naming the profile explicitly. The old name-only variant was
// reachable by anyone for any profile.
func (s *ProfileLinkServiceImpl) UnlinkPlayerID(ctx context.Context, playerID int) error {
	if err := s.matchRepo.ClearTelegramID(ctx, playerID); err != nil {
		return fmt.Errorf("failed to clear tg_id for player %d: %w", playerID, err)
	}

	if err := s.profileRepo.DeleteLinkByDiscordPlayer(ctx, playerID); err != nil {
		return err
	}

	s.logger.Info("Unlinked Telegram from player ID %d", playerID)
	return nil
}

// UnlinkByTelegram is the Telegram-side entry: the caller proves ownership by
// holding the account. It clears players.tg_id as well, so the identity does not
// survive the profile row it was created with.
func (s *ProfileLinkServiceImpl) UnlinkByTelegram(ctx context.Context, telegramID int64) error {
	playerID, _, err := s.matchRepo.GetPlayerByTelegramID(ctx, telegramID)
	switch {
	case err == nil:
		if clearErr := s.matchRepo.ClearTelegramID(ctx, playerID); clearErr != nil {
			return fmt.Errorf("failed to clear tg_id for player %d: %w", playerID, clearErr)
		}
	case !errors.Is(err, domain.ErrPlayerNotFound):
		return fmt.Errorf("failed to resolve telegram %d: %w", telegramID, err)
	}
	// ErrPlayerNotFound means the identity was already gone; the profile row
	// below may still be there, so keep going rather than reporting success.

	if err := s.profileRepo.DeleteLinkByTelegramID(ctx, telegramID); err != nil {
		return err
	}

	s.logger.Info("Unlinked Telegram account: %d", telegramID)
	return nil
}
