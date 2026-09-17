package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// WithMatchDesk is called before Start; match operations are independent of
// the Challonge API client and consume only the persisted bracket cache.
func (b *Bot) WithMatchDesk(s *application.MatchDeskService) *Bot {
	b.matchDesk = s
	b.deskWake = make(chan struct{}, 1)
	return b
}

func (b *Bot) wakeMatchDesk() {
	if b.deskWake != nil {
		select {
		case b.deskWake <- struct{}{}:
		default:
		}
	}
}

type deskHTTPClient struct {
	client tgbotapi.HTTPClient
	ctx    context.Context
}

func (c deskHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req.WithContext(c.ctx))
}

func (b *Bot) sendDeskNotice(ctx context.Context, n models.DeskNotice) error {
	msg := tgbotapi.NewMessage(n.ChatID, n.Text)
	if len(n.Buttons) > 0 {
		// The pinned Telegram SDK predates copy_text, but accepts arbitrary
		// JSON reply markup. No SDK upgrade is needed for a native copy button.
		msg.ReplyMarkup = struct {
			Keyboard [][]models.DeskButton `json:"inline_keyboard"`
		}{n.Buttons}
	}
	api := *b.bot
	api.Client = deskHTTPClient{client: b.bot.Client, ctx: ctx}
	_, err := api.Send(msg)
	return err
}

func isPermanentTelegramError(err error) bool {
	if err == nil {
		return false
	}
	var tgErr tgbotapi.Error
	if errors.As(err, &tgErr) {
		switch tgErr.Code {
		case http.StatusForbidden, http.StatusBadRequest, http.StatusUnauthorized:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "bot was blocked by the user") ||
		strings.Contains(msg, "chat not found") ||
		strings.Contains(msg, "user is deactivated")
}

func (b *Bot) startMatchDeskWorker(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		tickCtx, cancel := context.WithTimeout(ctx, updateTimeout)
		b.safely("match desk", func() { b.flushMatchDesk(tickCtx) })
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-b.deskWake:
		}
	}
}

func (b *Bot) flushMatchDesk(ctx context.Context) {
	notices, err := b.matchDesk.Pending(ctx)
	if err != nil {
		if strings.HasPrefix(err.Error(), "сетка ещё") {
			return
		}
		b.logger.Warn("telegram match desk pending: %v", err)
		return
	}
	for _, n := range notices {
		if ctx.Err() != nil {
			return
		}
		current, err := b.matchDesk.NoticeCurrent(ctx, n)
		if err != nil {
			b.logger.Warn("telegram match desk recheck: %v", err)
			return
		}
		if !current {
			continue
		}
		if err := b.sendDeskNotice(ctx, n); err != nil {
			b.logger.Warn("telegram match desk delivery %d: %v", n.ID, err)
			if isPermanentTelegramError(err) {
				if ackErr := b.matchDesk.Acknowledge(ctx, n); ackErr != nil {
					b.logger.Warn("telegram match desk acknowledge permanent failure %d: %v", n.ID, ackErr)
				}
			}
		} else if err := b.matchDesk.Acknowledge(ctx, n); err != nil {
			b.logger.Warn("telegram match desk acknowledge %d: %v", n.ID, err)
		}
		timer := time.NewTimer(broadcastInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (b *Bot) handleDeskCommand(ctx context.Context, actor int64, text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	command := fields[0]
	switch command {
	case "/match", "/judge", "/attention", "/pause_matches", "/resume_matches", "/pause_match", "/resume_match", "/match_history":
	default:
		return false
	}
	if b.matchDesk == nil {
		b.sendMessage(actor, "Управление матчами доступно после подключения сетки.", "empty")
		return true
	}
	if command == "/pause_match" || command == "/resume_match" || command == "/match_history" {
		if len(fields) != 2 {
			b.sendMessage(actor, "Используйте "+command+" <номер матча>.", "empty")
			return true
		}
		number, err := deskMatchNumber(fields[1])
		if err == nil {
			if command == "/match_history" {
				var lines []string
				lines, err = b.matchDesk.History(ctx, actor, number)
				if err == nil {
					b.sendDeskHistory(actor, lines)
				}
			} else {
				action := "pause"
				if command == "/resume_match" {
					action = "resume"
				}
				err = b.matchDesk.AdminNumberAction(ctx, actor, number, action)
				if err == nil {
					b.sendMessage(actor, "Сохранено. Капитаны получат обновлённые карточки. Общая пауза, если включена, остаётся в силе.", "empty")
					b.wakeMatchDesk()
				}
			}
		}
		if err != nil {
			b.sendMessage(actor, deskUserError(err), "empty")
		}
		return true
	}
	if len(fields) != 1 {
		b.sendMessage(actor, "Используйте "+command+" без аргументов.", "empty")
		return true
	}
	var notices []models.DeskNotice
	var err error
	switch command {
	case "/match", "/judge":
		notices, err = b.matchDesk.Cards(ctx, actor)
		if err == nil && len(notices) == 0 {
			b.sendMessage(actor, "У вашей команды пока нет открытого матча с определённым соперником.", "empty")
		}
		if command == "/judge" {
			for i := range notices {
				notices[i] = judgeReasons(notices[i])
			}
		}
	case "/attention":
		notices, err = b.matchDesk.Attention(ctx, actor)
		if err == nil && len(notices) == 0 {
			b.sendMessage(actor, "Нет матчей, требующих внимания.", "empty")
		}
	case "/pause_matches", "/resume_matches":
		action := "pause"
		label := "Таймеры всех матчей приостановлены."
		if command == "/resume_matches" {
			action = "resume"
			label = "Общая пауза снята. Матчи с индивидуальной паузой остаются на паузе."
		}
		err = b.matchDesk.AdminAction(ctx, actor, 0, 0, action)
		if err == nil {
			b.sendMessage(actor, label, "empty")
			b.wakeMatchDesk()
		}
	}
	if err != nil {
		b.logger.Warn("telegram match desk command: %v", err)
		b.sendMessage(actor, "Не удалось выполнить действие: "+deskUserError(err), "empty")
		return true
	}
	for _, n := range notices {
		if err := b.sendDeskNotice(ctx, n); err != nil {
			b.logger.Warn("telegram match desk view: %v", err)
		}
	}
	return true
}

func (b *Bot) sendDeskHistory(chatID int64, lines []string) {
	text := ""
	for _, line := range lines {
		if len(text)+len(line) > 3000 {
			b.sendMessage(chatID, text, "empty")
			text = ""
		}
		text += line + "\n"
	}
	if text != "" {
		b.sendMessage(chatID, text, "empty")
	}
}

func judgeReasons(n models.DeskNotice) models.DeskNotice {
	n.Text = "Матч: выберите причину вызова судьи.\n\n" + n.Text
	n.Buttons = nil
	for _, item := range [][2]string{{"Соперник не отвечает", "judge_noanswer"}, {"Проблема с лобби", "judge_lobby"}, {"Спор по результату", "judge_score"}} {
		n.Buttons = append(n.Buttons, []models.DeskButton{{Text: item[0], Data: fmt.Sprintf("desk:%s:%d:%d", item[1], n.MatchID, n.Generation)}})
	}
	return n
}

func (b *Bot) handleDeskCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	if b.matchDesk == nil {
		b.apiRespond(cb, "Управление матчами недоступно.", true)
		return
	}
	// All captain details stay in private chats, including forwarded cards.
	if cb.Message == nil || cb.Message.Chat == nil || !cb.Message.Chat.IsPrivate() || cb.Message.Chat.ID != cb.From.ID {
		b.apiRespond(cb, "Откройте /match в личном чате с ботом.", true)
		return
	}
	action, id, generation, err := application.ParseDeskCallback(cb.Data)
	if err != nil {
		b.apiRespond(cb, deskUserError(err), true)
		return
	}
	if action == "judge" {
		notices, err := b.matchDesk.Cards(ctx, cb.From.ID)
		if err != nil {
			b.apiRespond(cb, deskUserError(err), true)
			return
		}
		for _, n := range notices {
			if n.MatchID == id && n.Generation == generation {
				b.apiRespond(cb, "Выберите причину ниже.", false)
				if err := b.sendDeskNotice(ctx, judgeReasons(n)); err != nil {
					b.logger.Warn("telegram judge menu: %v", err)
				}
				return
			}
		}
		b.apiRespond(cb, "Карточка недоступна. Откройте /match.", true)
		return
	}
	switch action {
	case "pause", "resume", "resolve":
		err = b.matchDesk.AdminAction(ctx, cb.From.ID, id, generation, action)
	default:
		err = b.matchDesk.CaptainAction(ctx, cb.From.ID, id, generation, action)
	}
	if err != nil {
		b.logger.Warn("telegram match desk callback: %v", err)
		b.apiRespond(cb, deskUserError(err), true)
		return
	}
	b.apiRespond(cb, "Сохранено. Обновление карточек отправлено в очередь.", false)
	b.wakeMatchDesk()
}

func deskUserError(err error) string {
	// Domain errors are Russian, database/transport diagnostics are not useful
	// in Telegram and can contain operational details.
	text := err.Error()
	if strings.HasPrefix(text, "эта карточка") || strings.HasPrefix(text, "действие доступно") || strings.HasPrefix(text, "только капитан") || strings.HasPrefix(text, "матч на паузе") || strings.HasPrefix(text, "сетка ещё") || strings.HasPrefix(text, "неизвестное действие") || strings.HasPrefix(text, "доступно только") {
		return text
	}
	return "Временная ошибка. Попробуйте ещё раз."
}

// Keep command number parsing shared by the admin and history commands.
func deskMatchNumber(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(value, "#"))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("неверный номер матча")
	}
	return n, nil
}
