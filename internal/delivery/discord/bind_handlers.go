package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"blackwatch/internal/domain"

	"github.com/bwmarrin/discordgo"
)

// =====================================================================
// Discord ↔ player binding
//
// players.discord_id is what every lobby operation resolves a user through:
// the join and requeue buttons, tier role syncing, rank announcements and
// /queue_ban all look a player up by it. Nothing in the bot ever wrote to that
// column, so on a fresh database the join button could only ever answer "your
// Discord is not linked to any player" and the whole matchmaking flow was
// unreachable unless someone filled the column by hand in SQL.
// =====================================================================

// handleBind lets a user claim an unclaimed player profile.
func (b *Bot) handleBind(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	member := interactionMember(i)
	if member == nil {
		b.respondMessage(s, i, "Эта команда работает только на сервере.", true)
		return
	}

	nickname := strings.TrimSpace(i.ApplicationCommandData().Options[0].StringValue())

	playerID, created, err := b.services.MatchService.BindDiscordByName(ctx, nickname, member.User.ID)
	if err != nil {
		b.reportFailure(s, i, "bind", bindFailureMessage(err, nickname), err)
		return
	}

	if created {
		b.respondMessage(s, i, bindCreatedMessage(nickname, playerID), true)
		return
	}
	b.respondMessage(s, i, bindClaimedMessage(nickname, playerID), true)
}

// The two outcomes need different words, and both the command and the modal say
// them — so they live here rather than being written twice and drifting apart.
//
// Claiming an existing profile carries the player's history with it. A fresh one
// starts empty, and its nickname has to match the scoreboard or their results
// will never attach, which is worth saying at the moment it can still be fixed.

func bindCreatedMessage(nickname string, playerID int) string {
	return fmt.Sprintf(
		"Профиль **%s** создан и привязан к вашему Discord (ID: %d).\n\n"+
			"Внимание: ник должен совпадать с ником в игре — иначе результаты матчей не подтянутся. "+
			"Если ошиблись, админ поправит через `/rename_player`.\n\n"+
			"Теперь вам доступны кнопки лобби и ранговые роли.",
		nickname, playerID)
}

func bindClaimedMessage(nickname string, playerID int) string {
	return fmt.Sprintf(
		"Ваш Discord привязан к профилю **%s** (ID: %d). Ваша статистика подтянута.\n\n"+
			"Теперь вам доступны кнопки лобби и ранговые роли.",
		nickname, playerID)
}

// handleWhoami reports the profile bound to the caller.
func (b *Bot) handleWhoami(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	member := interactionMember(i)
	if member == nil {
		b.respondMessage(s, i, "Эта команда работает только на сервере.", true)
		return
	}

	id, name, err := b.services.MatchService.GetPlayerByDiscordID(ctx, member.User.ID)
	if err != nil {
		b.respondMessage(s, i, "Ваш Discord пока не привязан к профилю.\n\nВыполните `/bind <ваш ник в игре>`.", true)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Ваш Discord привязан к профилю **%s** (ID: %d).", name, id), true)
}

// handleBindPlayer binds another user's account. Admin-only, and allowed to
// overwrite an existing binding — this is the way to fix a wrong claim.
func (b *Bot) handleBindPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	opts := i.ApplicationCommandData().Options
	nickname := strings.TrimSpace(opts[0].StringValue())

	target := opts[1].UserValue(s)
	if target == nil {
		b.respondMessage(s, i, "Не удалось определить пользователя.", true)
		return
	}

	playerID, created, err := b.services.MatchService.BindDiscordByName(ctx, nickname, target.ID)
	if err != nil {
		b.reportFailure(s, i, "bind_player", bindFailureMessage(err, nickname), err)
		return
	}

	verb := "привязан к профилю"
	if created {
		verb = "привязан к новому профилю"
	}
	b.respondMessage(s, i, fmt.Sprintf("<@%s> %s **%s** (ID: %d).", target.ID, verb, nickname, playerID), false)
}

// handleUnbindPlayer releases a profile's binding.
func (b *Bot) handleUnbindPlayer(ctx context.Context, s *discordgo.Session, i *discordgo.Interaction) {
	playerID := int(i.ApplicationCommandData().Options[0].IntValue())

	name, err := b.services.MatchService.UnbindDiscordID(ctx, playerID)
	if err != nil {
		b.reportFailure(s, i, "unbind_player", bindFailureMessage(err, fmt.Sprintf("ID %d", playerID)), err)
		return
	}

	b.respondMessage(s, i, fmt.Sprintf("Профиль **%s** (ID: %d) отвязан от Discord.", name, playerID), false)
}

// bindFailureMessage renders a binding error for the user.
//
// The service returns typed errors, so the wording belongs here. Echoing
// err.Error() put the service's internal phrasing — and, for anything that
// reached it from the database, the driver's — into a Discord reply.
func bindFailureMessage(err error, nickname string) string {
	switch {
	case errors.Is(err, domain.ErrDiscordAlreadyBound):
		return "Ваш Discord уже привязан к другому профилю. Освободить его может админ через `/unbind_player`."
	case errors.Is(err, domain.ErrProfileTaken):
		return fmt.Sprintf("Профиль **%s** уже занят другим Discord-аккаунтом. Если это вы — обратитесь к админу.", nickname)
	case errors.Is(err, domain.ErrPlayerNameEmpty):
		return "Ник не может быть пустым."
	case errors.Is(err, domain.ErrPlayerNameTooLong):
		return "Слишком длинный ник."
	case errors.Is(err, domain.ErrInvalidPlayerName):
		return "Недопустимый ник."
	case errors.Is(err, domain.ErrPlayerNotFound):
		return fmt.Sprintf("Профиль **%s** не найден.", nickname)
	case errors.Is(err, domain.ErrDiscordNotLinked):
		return fmt.Sprintf("У профиля **%s** нет привязанного Discord-аккаунта.", nickname)
	default:
		return "Не удалось выполнить привязку. Попробуйте позже."
	}
}
