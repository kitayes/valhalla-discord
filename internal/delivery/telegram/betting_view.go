package telegram

import (
	"fmt"
	"strconv"
	"strings"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// teamCode is the one-letter side used in callback data. Callback data is
// capped at 64 bytes, and "Team A" spelled out would spend six of them on every
// button for no gain.
func teamCode(team string) string {
	if team == domain.TeamB {
		return "b"
	}
	return "a"
}

// teamFromCode is the inverse of teamCode. Anything else is rejected rather
// than defaulted to Team A: a stake landing on the wrong side because a byte
// was mangled is a silent theft, not a display glitch.
func teamFromCode(code string) (string, bool) {
	switch code {
	case "a":
		return domain.TeamA, true
	case "b":
		return domain.TeamB, true
	default:
		return "", false
	}
}

// formatOdds renders a multiplier, or a dash when the side has no money on it.
//
// A dash rather than "x0.00": an empty side has no coefficient to quote, and
// zero would read as "this pays nothing" when in fact a lone bettor there would
// take the whole pool.
func formatOdds(odds float64) string {
	if odds <= 0 {
		return "—"
	}
	return fmt.Sprintf("x%.2f", odds)
}

// betsWord agrees the noun with the count. Russian needs three forms, and
// "3 ставок" in a post that people read every match looks like a bug.
func betsWord(n int) string {
	mod100 := n % 100
	if mod100 >= 11 && mod100 <= 14 {
		return "ставок"
	}
	switch n % 10 {
	case 1:
		return "ставка"
	case 2, 3, 4:
		return "ставки"
	default:
		return "ставок"
	}
}

// poolLine renders one side of the market.
func poolLine(pool models.BetPool, team string) string {
	count := pool.Count(team)
	return fmt.Sprintf("%s — %d (%d %s) · %s",
		team, pool.Amount(team), count, betsWord(count), formatOdds(pool.Odds(team)))
}

// renderPost is the text of an open betting post.
//
// The floating-coefficient warning is not decoration. The number shown moves
// with every later bet, so a post that states it without saying so is making a
// promise settlement will not keep.
func renderPost(matchID int, post livePost, pool models.BetPool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "МАТЧ #%d — ставки открыты\n\n", matchID)
	fmt.Fprintf(&sb, "Team A — капитан %s\n", post.captainA)
	fmt.Fprintf(&sb, "Team B — капитан %s\n\n", post.captainB)

	if pool.Total() == 0 {
		sb.WriteString("Банк пуст — ставок ещё нет.\n\n")
	} else {
		fmt.Fprintf(&sb, "Банк: %d\n%s\n%s\n\n",
			pool.Total(), poolLine(pool, domain.TeamA), poolLine(pool, domain.TeamB))
	}

	fmt.Fprintf(&sb, "Ставки принимаются до %s.\n", post.deadline.Format("15:04"))
	sb.WriteString("Коэффициент плавающий: он меняется с каждой ставкой и становится окончательным в момент закрытия.")

	return sb.String()
}

// renderClosedPost is the text left behind once a match is settled.
//
// It carries no pool figures on purpose. By the time this runs the bets are
// settled, so the live pool reads zero — printing it would announce "Банк: 0"
// under a match people had just staked on. The payout message published
// alongside is where the numbers belong.
func renderClosedPost(matchID int, post livePost) string {
	return fmt.Sprintf("МАТЧ #%d — ставки закрыты\n\nTeam A — капитан %s\nTeam B — капитан %s\n\nРезультат и выплаты — в сообщении ниже.",
		matchID, post.captainA, post.captainB)
}

// buildKeyboard draws the stake menu: a caption row per side, then that side's
// amounts.
//
// The captions are buttons because Telegram has no inert ones. Rather than
// making them dead taps, they report the pool — see handlePoolCallback.
//
// Coefficients are deliberately absent from the labels. A button is only
// redrawn when the whole post is edited, so a multiplier baked into one would
// keep advertising a price that has already moved; the text above carries it
// instead.
func buildKeyboard(matchID int, post livePost, stakes []int) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, 2*(1+len(stakes)/buttonsPerRow+1))

	for _, side := range []struct {
		team    string
		captain string
	}{
		{domain.TeamA, post.captainA},
		{domain.TeamB, post.captainB},
	} {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s · %s", side.team, side.captain),
				fmt.Sprintf("%s%s%d", callbackPoolPrefix, callbackSep, matchID),
			),
		))
		rows = append(rows, stakeRows(matchID, side.team, stakes)...)
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// stakeRows lays one side's amounts out buttonsPerRow at a time, with "Макс"
// last. Chunking rather than one long row because Telegram shrinks a crowded
// row until the labels stop being readable on a phone.
func stakeRows(matchID int, team string, stakes []int) [][]tgbotapi.InlineKeyboardButton {
	labels := make([]tgbotapi.InlineKeyboardButton, 0, len(stakes)+1)
	for _, stake := range stakes {
		labels = append(labels, tgbotapi.NewInlineKeyboardButtonData(
			strconv.Itoa(stake),
			betCallbackData(matchID, team, strconv.Itoa(stake)),
		))
	}
	labels = append(labels, tgbotapi.NewInlineKeyboardButtonData(
		"Макс", betCallbackData(matchID, team, stakeMaxToken)))

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(labels)/buttonsPerRow+1)
	for start := 0; start < len(labels); start += buttonsPerRow {
		end := start + buttonsPerRow
		if end > len(labels) {
			end = len(labels)
		}
		rows = append(rows, labels[start:end])
	}
	return rows
}

// betCallbackData builds "bet:<side>:<stake>:<matchID>".
func betCallbackData(matchID int, team, stake string) string {
	return strings.Join([]string{callbackBetPrefix, teamCode(team), stake, strconv.Itoa(matchID)}, callbackSep)
}
