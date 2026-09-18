package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// callbackBetPrefix carries a stake: "bet:<side>:<stake>:<matchID>", where
	// stake is either one of the configured amounts or stakeMaxToken.
	callbackBetPrefix = "bet"
	// callbackPoolPrefix asks for the current pool without staking anything.
	// Telegram has no inert buttons, so the team captions are buttons too and
	// this is what they do.
	callbackPoolPrefix = "pool"
	// stakeMaxToken means "as much as I can afford". The number is resolved on
	// the server from the balance and the configured cap: callback data is
	// client-supplied, and a stake sent by the client is a stake the client
	// chose.
	stakeMaxToken = "max"

	// callbackSep separates callback fields. Underscore was used before and
	// could not be told apart from the underscores inside the prefixes, so
	// parsing was a chain of TrimPrefix calls that silently produced match 0.
	callbackSep = ":"

	// postEditInterval is the shortest gap between two edits of the same
	// betting post. Telegram throttles edits per chat, and a burst of taps in
	// the opening seconds of a window would otherwise spend the whole budget
	// and get later edits dropped — including the last one, which is the only
	// one that has to be right.
	postEditInterval = 3 * time.Second

	// postEditTimeout bounds one refresh. It runs on a timer, detached from the
	// update that triggered it, so it needs a deadline of its own.
	postEditTimeout = 15 * time.Second

	// buttonsPerRow keeps a stake row within what Telegram lays out without
	// shrinking the labels past readability on a phone.
	buttonsPerRow = 4
)

// BetSettings is the stake menu offered under a betting post.
type BetSettings struct {
	// Stakes are the fixed-amount buttons, ascending. It doubles as the
	// whitelist a stake from callback data is checked against.
	Stakes []int
	// Max caps any single bet, stakeMaxToken included.
	Max int
}

// betFailureMessage turns a betting error into something the user can act on.
// The raw error used to be echoed straight into the Telegram alert, which put
// driver messages in front of players.
func betFailureMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrBettingClosed):
		return "Ставки заблокированы. Игра перешла в мид-гейм."
	case errors.Is(err, domain.ErrAlreadyBet):
		return "Вы уже поставили на этот матч. Одна ставка на матч."
	case errors.Is(err, domain.ErrBettingOnOwnMatch):
		return "Вы играете в этом матче — ставить на него нельзя."
	case errors.Is(err, domain.ErrInsufficientPoints):
		return "Недостаточно очков для ставки."
	case errors.Is(err, domain.ErrPlayerNotFound):
		return "Ваш Telegram не привязан к игроку. Используйте /link <код> в Discord."
	case errors.Is(err, domain.ErrMatchNotFound):
		return "Матч не найден."
	default:
		return "Не удалось принять ставку. Попробуйте позже."
	}
}

// livePost is what a refresh needs and cannot recompute: the captains and the
// deadline printed above the pool.
//
// Deliberately in memory only. After a restart the entry is gone and the post
// stops being refreshed — which is the right outcome, because rewriting it from
// partial data would replace correct captions with blanks. The post's own
// deadline line still tells a reader when the numbers stopped moving, and the
// message reference itself is in the database, so settlement can still close
// the post out.
type livePost struct {
	captainA string
	captainB string
	deadline time.Time
}

type BettingBot struct {
	bot            *Bot
	bettingService *application.BettingService
	logger         application.Logger
	channelID      string
	bets           BetSettings

	// mu guards every map below. They are touched from the update loop and from
	// refresh timers.
	mu sync.Mutex
	// live holds the header of each open betting post.
	live map[int]livePost
	// refreshQueued marks matches with an edit already scheduled, so a burst of
	// bets collapses into one edit that reads the pool when it fires.
	refreshQueued map[int]bool
	// refreshNotBefore is when the next edit of a match's post may go out.
	refreshNotBefore map[int]time.Time
}

func NewBettingBot(bot *Bot, bettingService *application.BettingService, channelID string, bets BetSettings, logger application.Logger) *BettingBot {
	return &BettingBot{
		bot:              bot,
		bettingService:   bettingService,
		logger:           logger,
		channelID:        channelID,
		bets:             bets,
		live:             make(map[int]livePost),
		refreshQueued:    make(map[int]bool),
		refreshNotBefore: make(map[int]time.Time),
	}
}

// NotifyMatchLive announces a match and opens the betting keyboard.
//
// window is how long bets are accepted. It is spelled out in the message
// because the window is short and closes silently: without it a player has no
// way to tell whether they have five minutes or five seconds.
func (bb *BettingBot) NotifyMatchLive(matchID int, captainA, captainB string, window time.Duration) {
	if bb.channelID == "" {
		bb.logger.Warn("betting: TELEGRAM_CHANNEL_ID not configured — skipping match notification")
		return
	}

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		bb.logger.Error("betting: failed to resolve channel ID %q", bb.channelID)
		return
	}

	post := livePost{captainA: captainA, captainB: captainB, deadline: time.Now().Add(window)}

	ctx, cancel := context.WithTimeout(context.Background(), postEditTimeout)
	defer cancel()

	// The opening pool is empty by construction: bets arrive through the buttons
	// on this very message, so none can exist before it is sent. Reading it back
	// from the database would only be a query that can never return anything.
	pool := models.BetPool{MatchID: matchID}

	// No ParseMode. The captions carry player nicknames, and legacy Markdown
	// treats an underscore in a nickname as formatting: one "Bjorn_Ironside" in
	// a lobby and Telegram rejects the whole message, so the match goes live
	// with no betting post at all.
	msg := tgbotapi.NewMessage(channelChatID, renderPost(matchID, post, pool))
	msg.ReplyMarkup = buildKeyboard(matchID, post, bb.bets.Stakes)

	sent, err := bb.bot.bot.Send(msg)
	if err != nil {
		bb.logger.Error("betting: failed to send match notification to channel: %v", err)
		return
	}

	// Persisted so settlement can still strip the keyboard off this post after a
	// restart, when the in-memory header is long gone.
	//
	// Recorded before the header is published, because every refresh needs both:
	// with the header live and the reference missing, taps would schedule edits
	// that find no message and quietly do nothing.
	if err := bb.bettingService.SaveBetPost(ctx, matchID, channelChatID, int64(sent.MessageID)); err != nil {
		bb.logger.Error("betting: post %d for match #%d will not be refreshed or closed: %v",
			sent.MessageID, matchID, err)
		return
	}

	bb.mu.Lock()
	bb.live[matchID] = post
	bb.mu.Unlock()

	bb.logger.Info("betting: match #%d published to Telegram channel", matchID)
}

func (bb *BettingBot) HandleCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	bb.logger.Debug("betting: callback from %s (%d): %s", callback.From.UserName, callback.From.ID, data)

	switch {
	case strings.HasPrefix(data, callbackBetPrefix+callbackSep):
		bb.handleBetCallback(ctx, callback)
	case strings.HasPrefix(data, callbackPoolPrefix+callbackSep):
		bb.handlePoolCallback(ctx, callback)
	default:
		// Every callback must be answered, otherwise Telegram leaves the button
		// spinning on the client until it times out. Buttons from a post
		// published by an older build land here.
		bb.bot.apiRespond(callback, "Эта кнопка больше не действует.", true)
	}
}

// betRequest is a parsed stake button.
type betRequest struct {
	matchID int
	team    string
	// stake is the chosen amount, or 0 when max is set.
	stake int
	max   bool
}

// parseBetCallback reads "bet:<side>:<stake>:<matchID>".
//
// stakes is the whitelist of amounts the keyboard actually offers. Anything
// outside it is refused: the library documents callback data as attacker
// controlled — "a bad client can send arbitrary data in this field" — so the
// number in it is a request, not a fact.
func parseBetCallback(data string, stakes []int) (betRequest, bool) {
	parts := strings.Split(data, callbackSep)
	if len(parts) != 4 || parts[0] != callbackBetPrefix {
		return betRequest{}, false
	}

	team, ok := teamFromCode(parts[1])
	if !ok {
		return betRequest{}, false
	}

	matchID, err := strconv.Atoi(parts[3])
	if err != nil || matchID <= 0 {
		return betRequest{}, false
	}

	req := betRequest{matchID: matchID, team: team}
	if parts[2] == stakeMaxToken {
		req.max = true
		return req, true
	}

	stake, err := strconv.Atoi(parts[2])
	if err != nil {
		return betRequest{}, false
	}
	for _, allowed := range stakes {
		if allowed == stake {
			req.stake = stake
			return req, true
		}
	}
	return betRequest{}, false
}

func (bb *BettingBot) handleBetCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID

	req, ok := parseBetCallback(callback.Data, bb.bets.Stakes)
	if !ok {
		bb.logger.Warn("betting: rejected callback data %q from user %d", callback.Data, userID)
		bb.bot.apiRespond(callback, "Некорректная кнопка ставки.", true)
		return
	}

	// The betting window is not pre-checked here. PlaceBet re-reads it inside
	// its own transaction anyway, and the pre-check queried a second, different
	// implementation that ignored betting_closes_at — so it answered "open" past
	// the deadline, drew the keyboard, and let PlaceBet do the refusing.
	points, err := bb.bettingService.GetPlayerPoints(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			bb.bot.apiRespond(callback, "Ваш Telegram не привязан к игроку. Используйте /link <код> в Discord.", true)
			return
		}
		bb.logger.Error("betting: failed to read points for user %d: %v", userID, err)
		bb.bot.apiRespond(callback, "Не удалось прочитать баланс. Попробуйте позже.", true)
		return
	}

	if points <= 0 {
		bb.bot.apiRespond(callback, "У вас нет очков для ставок. Заработайте очки участием в матчах.", true)
		return
	}

	amount := req.stake
	if req.max {
		amount = points
	}
	if amount > bb.bets.Max {
		amount = bb.bets.Max
	}
	// Said plainly rather than through the generic refusal: the bettor picked a
	// number off a keyboard that offered it, so "недостаточно очков" without the
	// balance reads as the bot malfunctioning.
	if amount > points {
		bb.bot.apiRespond(callback, fmt.Sprintf("Ставка %d, а на балансе %d очков.", amount, points), true)
		return
	}

	if err := bb.bettingService.PlaceBet(ctx, models.PlaceBetRequest{
		MatchID:    req.matchID,
		TgUserID:   userID,
		TeamChosen: req.team,
		Amount:     amount,
	}); err != nil {
		bb.logger.Error("betting: user %d failed to place bet on match #%d: %v", userID, req.matchID, err)
		bb.bot.apiRespond(callback, betFailureMessage(err), true)
		return
	}

	bb.bot.apiRespond(callback, bb.confirmation(ctx, req, amount, points-amount), true)
	bb.scheduleRefresh(req.matchID)

	bb.logger.Info("betting: user %d (%s) bet %d on %s (match #%d)",
		userID, callback.From.UserName, amount, req.team, req.matchID)
}

// confirmation quotes the coefficient the bet just landed on.
//
// The pool is read after the bet, so the stake is already inside it and the
// quoted multiplier is the one settlement would apply if the window closed now.
// Quoting the pool from before the bet would overstate the return, because the
// bettor's own money dilutes the side they backed.
//
// Telegram caps an alert at roughly 200 characters, so this stays terse.
func (bb *BettingBot) confirmation(ctx context.Context, req betRequest, amount, balance int) string {
	head := fmt.Sprintf("Принято: %d на %s\nБаланс: %d", amount, req.team, balance)

	pool, err := bb.bettingService.BetPool(ctx, req.matchID)
	if err != nil {
		// The bet is committed; failing to price it is not a failure to place it.
		bb.logger.Warn("betting: bet on match #%d confirmed without odds: %v", req.matchID, err)
		return head
	}

	odds := pool.Odds(req.team)
	if odds <= 0 {
		return head
	}
	return fmt.Sprintf("%s\nКоэффициент x%.2f → ~%d очков\nФинальный коэф. — на момент закрытия ставок",
		head, odds, int(float64(amount)*odds))
}

// handlePoolCallback answers a tap on a team caption with the current market.
//
// Telegram has no inert buttons: a caption drawn as a button is tappable, and a
// tap that did nothing would look broken. This also puts the bettor's balance
// somewhere they can reach without leaving the channel.
func (bb *BettingBot) handlePoolCallback(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	parts := strings.Split(callback.Data, callbackSep)
	matchID, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || matchID <= 0 {
		bb.bot.apiRespond(callback, "Некорректная кнопка.", true)
		return
	}

	pool, err := bb.bettingService.BetPool(ctx, matchID)
	if err != nil {
		bb.logger.Error("betting: failed to read pool for match #%d: %v", matchID, err)
		bb.bot.apiRespond(callback, "Не удалось прочитать банк. Попробуйте позже.", true)
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Банк: %d\n%s\n%s", pool.Total(), poolLine(pool, domain.TeamA), poolLine(pool, domain.TeamB))

	// A missing balance is not worth failing the answer over — the pool is what
	// was asked for.
	if points, err := bb.bettingService.GetPlayerPoints(ctx, callback.From.ID); err == nil {
		fmt.Fprintf(&sb, "\nВаш баланс: %d", points)
	}

	bb.bot.apiRespond(callback, sb.String(), true)
}

// scheduleRefresh queues one edit of a match's post, coalescing bursts.
//
// A queued edit reads the pool when it fires, not when it was queued, so every
// bet absorbed while it waits is still reflected. A bet arriving just after an
// edit went out schedules the next one at the throttle boundary, which is what
// guarantees the final state of the window always reaches the post.
func (bb *BettingBot) scheduleRefresh(matchID int) {
	bb.mu.Lock()
	defer bb.mu.Unlock()

	if _, open := bb.live[matchID]; !open {
		return
	}
	if bb.refreshQueued[matchID] {
		return
	}

	wait := time.Until(bb.refreshNotBefore[matchID])
	if wait < 0 {
		wait = 0
	}
	bb.refreshQueued[matchID] = true

	time.AfterFunc(wait, func() {
		bb.mu.Lock()
		delete(bb.refreshQueued, matchID)
		bb.refreshNotBefore[matchID] = time.Now().Add(postEditInterval)
		post, open := bb.live[matchID]
		bb.mu.Unlock()

		if !open {
			return
		}
		bb.refreshPost(matchID, post)
	})
}

// refreshPost rewrites a betting post with the current pool.
//
// Every failure here is logged and dropped. The bets themselves are already
// committed; a post showing stale numbers is a cosmetic problem, and turning it
// into an error would abort nothing that has not already succeeded.
func (bb *BettingBot) refreshPost(matchID int, post livePost) {
	ctx, cancel := context.WithTimeout(context.Background(), postEditTimeout)
	defer cancel()

	chatID, messageID, ok, err := bb.bettingService.BetPost(ctx, matchID)
	if err != nil {
		bb.logger.Warn("betting: cannot locate post for match #%d: %v", matchID, err)
		return
	}
	if !ok {
		return
	}

	pool, err := bb.bettingService.BetPool(ctx, matchID)
	if err != nil {
		bb.logger.Warn("betting: cannot read pool for match #%d: %v", matchID, err)
		return
	}

	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, int(messageID),
		renderPost(matchID, post, pool), buildKeyboard(matchID, post, bb.bets.Stakes))
	if _, err := bb.bot.bot.Request(edit); err != nil {
		bb.logger.Warn("betting: failed to refresh post for match #%d: %v", matchID, err)
	}
}

// closePost takes the keyboard off a settled match's post and states the final
// pool, so it stops inviting taps that can only be refused.
//
// It works off the stored message reference rather than the in-memory header,
// because settlement can happen after a restart.
func (bb *BettingBot) closePost(matchID int) {
	bb.mu.Lock()
	post, hadHeader := bb.live[matchID]
	delete(bb.live, matchID)
	delete(bb.refreshQueued, matchID)
	delete(bb.refreshNotBefore, matchID)
	bb.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), postEditTimeout)
	defer cancel()

	chatID, messageID, ok, err := bb.bettingService.BetPost(ctx, matchID)
	if err != nil {
		bb.logger.Warn("betting: cannot locate post for match #%d: %v", matchID, err)
		return
	}
	if !ok {
		return
	}

	// An empty inline_keyboard is how Telegram is told to remove one, and it has
	// to be an empty list of rows. NewInlineKeyboardMarkup with an empty row
	// builds [[]] instead — one row containing no buttons — which is not the
	// same request and leaves the buttons up on a market that is already
	// settled.
	noKeyboard := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}

	if !hadHeader {
		// Restarted since the post went up: the captions are gone, so the text
		// is left exactly as it is and only the buttons come off. Rewriting it
		// from what is still known would replace real names with blanks.
		edit := tgbotapi.NewEditMessageReplyMarkup(chatID, int(messageID), noKeyboard)
		if _, err := bb.bot.bot.Request(edit); err != nil {
			bb.logger.Warn("betting: failed to strip keyboard for match #%d: %v", matchID, err)
		}
		return
	}

	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, int(messageID),
		renderClosedPost(matchID, post), noKeyboard)
	if _, err := bb.bot.bot.Request(edit); err != nil {
		bb.logger.Warn("betting: failed to close post for match #%d: %v", matchID, err)
	}
}

func (bb *BettingBot) NotifyMatchResult(res application.PayoutResult) {
	bb.closePost(res.MatchID)

	if bb.channelID == "" {
		return
	}

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		return
	}

	msg := tgbotapi.NewMessage(channelChatID, application.FormatPayoutSummary(res))
	msg.ParseMode = "Markdown"

	if _, err := bb.bot.bot.Send(msg); err != nil {
		bb.logger.Error("betting: failed to send match result to channel: %v", err)
	} else {
		bb.logger.Info("betting: match #%d result published to Telegram channel", res.MatchID)
	}
}

func (bb *BettingBot) NotifyMatchCancelled(matchID int, refundedCount int) {
	bb.closePost(matchID)

	if bb.channelID == "" {
		return
	}

	channelChatID := bb.resolveChannelID()
	if channelChatID == 0 {
		return
	}

	text := fmt.Sprintf("*МАТЧ #%d ОТМЕНЕН СУДЬЕЙ!*\n\nВсе сделанные ставки (%d) отменены, очки возвращены на баланс.", matchID, refundedCount)
	msg := tgbotapi.NewMessage(channelChatID, text)
	msg.ParseMode = "Markdown"

	if _, err := bb.bot.bot.Send(msg); err != nil {
		bb.logger.Error("betting: failed to send cancel notification to channel: %v", err)
	} else {
		bb.logger.Info("betting: match #%d cancellation published to Telegram channel", matchID)
	}
}

// HandleBetCommand and HandleBalanceCommand were removed: nothing dispatched to
// them. They duplicated the callback flow with weaker validation (two unchecked
// Sscanf calls, their own "Team A"/"Team B" literals) and identified the bettor
// by chat id rather than From.ID, which is the group's id in a group chat.
// Betting is placed through the inline keyboard in handleBetCallback.

func (bb *BettingBot) resolveChannelID() int64 {
	if bb.channelID == "" {
		return 0
	}

	if id, err := strconv.ParseInt(bb.channelID, 10, 64); err == nil && id != 0 {
		return id
	}

	chat, err := bb.bot.bot.GetChat(tgbotapi.ChatInfoConfig{
		ChatConfig: tgbotapi.ChatConfig{
			SuperGroupUsername: strings.TrimPrefix(bb.channelID, "@"),
		},
	})
	if err != nil {
		bb.logger.Error("betting: failed to resolve channel %q: %v", bb.channelID, err)
		return 0
	}
	return chat.ID
}

func (b *Bot) apiRespond(callback *tgbotapi.CallbackQuery, text string, showAlert bool) {
	resp := tgbotapi.NewCallback(callback.ID, text)
	resp.ShowAlert = showAlert
	if _, err := b.bot.Request(resp); err != nil {
		b.logger.Error("telegram: callback response failed: %v", err)
	}
}
