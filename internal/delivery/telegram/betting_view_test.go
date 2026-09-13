package telegram

import (
	"strings"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

var testStakes = []int{10, 25, 50}

// The stake in callback data is client-supplied — the library says so outright.
// Everything that is not a button the keyboard actually drew must be refused,
// because the alternative is a modified client naming its own stake.
func TestParseBetCallbackRejectsUnofferedStake(t *testing.T) {
	if _, ok := parseBetCallback("bet:a:9999:47", testStakes); ok {
		t.Fatal("accepted a stake that no button offers")
	}
	if _, ok := parseBetCallback("bet:a:11:47", testStakes); ok {
		t.Fatal("accepted a stake between two offered ones")
	}
	if _, ok := parseBetCallback("bet:a:-25:47", testStakes); ok {
		t.Fatal("accepted a negative stake")
	}
}

func TestParseBetCallbackAcceptsOfferedStake(t *testing.T) {
	req, ok := parseBetCallback("bet:b:25:47", testStakes)
	if !ok {
		t.Fatal("rejected a stake the keyboard offers")
	}
	if req.matchID != 47 {
		t.Errorf("matchID = %d, want 47", req.matchID)
	}
	if req.team != domain.TeamB {
		t.Errorf("team = %q, want %q", req.team, domain.TeamB)
	}
	if req.stake != 25 {
		t.Errorf("stake = %d, want 25", req.stake)
	}
	if req.max {
		t.Error("max set on a fixed stake")
	}
}

// "Макс" carries no number: the amount is resolved server-side from the balance
// and the cap, so there is nothing in the callback for a client to inflate.
func TestParseBetCallbackMaxCarriesNoAmount(t *testing.T) {
	req, ok := parseBetCallback("bet:a:max:47", testStakes)
	if !ok {
		t.Fatal("rejected the max button")
	}
	if !req.max {
		t.Error("max not set")
	}
	if req.stake != 0 {
		t.Errorf("stake = %d, want 0 — max must not carry an amount", req.stake)
	}
}

// A mangled side must not fall through to Team A: a stake landing on the wrong
// team is money taken, not a display glitch.
func TestParseBetCallbackRejectsMalformed(t *testing.T) {
	for _, data := range []string{
		"",
		"bet",
		"bet:a:25",
		"bet:a:25:47:extra",
		"bet:c:25:47",
		"bet::25:47",
		"bet:a:25:0",
		"bet:a:25:-1",
		"bet:a:25:abc",
		"pool:47",
		"bet_team_a_47",
	} {
		if _, ok := parseBetCallback(data, testStakes); ok {
			t.Errorf("accepted malformed callback %q", data)
		}
	}
}

// Every button the keyboard draws must survive the parser, or a tap that looks
// legitimate is refused.
func TestKeyboardButtonsRoundTrip(t *testing.T) {
	post := livePost{captainA: "Gunnar", captainB: "Bjorn", deadline: time.Now()}
	kb := buildKeyboard(47, post, testStakes)

	stakesSeen := map[string]int{}
	for _, row := range kb.InlineKeyboard {
		if len(row) > 8 {
			t.Errorf("row of %d buttons exceeds what Telegram lays out", len(row))
		}
		for _, btn := range row {
			if btn.CallbackData == nil {
				t.Fatalf("button %q has no callback data", btn.Text)
			}
			data := *btn.CallbackData
			// Telegram caps callback data at 64 bytes and drops the button
			// silently past it.
			if len(data) > 64 {
				t.Errorf("callback data %q is %d bytes, over the 64-byte cap", data, len(data))
			}
			if strings.HasPrefix(data, callbackPoolPrefix+callbackSep) {
				continue
			}
			req, ok := parseBetCallback(data, testStakes)
			if !ok {
				t.Errorf("keyboard drew %q, which the parser rejects", data)
				continue
			}
			stakesSeen[req.team]++
		}
	}

	// Each side gets every configured stake plus "Макс".
	for _, team := range []string{domain.TeamA, domain.TeamB} {
		if got, want := stakesSeen[team], len(testStakes)+1; got != want {
			t.Errorf("%s has %d stake buttons, want %d", team, got, want)
		}
	}
}

// A long stake menu must wrap instead of producing one unreadable row.
func TestKeyboardWrapsLongStakeMenu(t *testing.T) {
	post := livePost{captainA: "A", captainB: "B", deadline: time.Now()}
	kb := buildKeyboard(1, post, []int{1, 2, 3, 4, 5, 6, 7, 8, 9})

	for _, row := range kb.InlineKeyboard {
		if len(row) > buttonsPerRow {
			t.Fatalf("row of %d buttons exceeds buttonsPerRow (%d)", len(row), buttonsPerRow)
		}
	}
}

// An empty side has no coefficient to quote. Rendering it as x0.00 would read as
// "this pays nothing", when a lone bettor there takes the whole pool.
func TestFormatOddsEmptySide(t *testing.T) {
	if got := formatOdds(0); got != "—" {
		t.Errorf("formatOdds(0) = %q, want a dash", got)
	}
	if got := formatOdds(1.3456); got != "x1.35" {
		t.Errorf("formatOdds(1.3456) = %q, want x1.35", got)
	}
}

func TestBetsWord(t *testing.T) {
	cases := map[int]string{
		0: "ставок", 1: "ставка", 2: "ставки", 4: "ставки", 5: "ставок",
		11: "ставок", 12: "ставок", 14: "ставок", 21: "ставка", 22: "ставки",
		25: "ставок", 101: "ставка", 111: "ставок",
	}
	for n, want := range cases {
		if got := betsWord(n); got != want {
			t.Errorf("betsWord(%d) = %q, want %q", n, got, want)
		}
	}
}

// The post states a coefficient that later bets will move. Saying so is not
// decoration: without it the number reads as a promise settlement will not keep.
func TestRenderPostWarnsCoefficientIsProvisional(t *testing.T) {
	post := livePost{captainA: "Gunnar", captainB: "Bjorn", deadline: time.Now()}
	pool := models.BetPool{MatchID: 47, AmountA: 340, CountA: 7, AmountB: 120, CountB: 3}

	text := renderPost(47, post, pool)
	if !strings.Contains(text, "плавающий") {
		t.Error("post quotes a coefficient without saying it moves")
	}
	if !strings.Contains(text, "x1.35") {
		t.Errorf("Team A odds missing from post:\n%s", text)
	}
	if !strings.Contains(text, "x3.83") {
		t.Errorf("Team B odds missing from post:\n%s", text)
	}
	if !strings.Contains(text, "460") {
		t.Errorf("total pool missing from post:\n%s", text)
	}
}

// An opening post has no bets in it. Printing "Банк: 0 · x0.00" would look like
// a broken market rather than an empty one.
func TestRenderPostEmptyPool(t *testing.T) {
	post := livePost{captainA: "Gunnar", captainB: "Bjorn", deadline: time.Now()}
	text := renderPost(47, post, models.BetPool{MatchID: 47})

	if !strings.Contains(text, "Банк пуст") {
		t.Errorf("empty market not stated plainly:\n%s", text)
	}
	if strings.Contains(text, "x0.00") {
		t.Errorf("empty side rendered as a zero coefficient:\n%s", text)
	}
}

// By the time a match settles its bets are settled too, so the live pool reads
// zero. The closing text must not quote it.
func TestRenderClosedPostQuotesNoPool(t *testing.T) {
	post := livePost{captainA: "Gunnar", captainB: "Bjorn", deadline: time.Now()}
	text := renderClosedPost(47, post)

	if strings.Contains(text, "Банк") {
		t.Errorf("closed post quotes a pool that is already settled:\n%s", text)
	}
	if !strings.Contains(text, "Gunnar") || !strings.Contains(text, "Bjorn") {
		t.Errorf("closed post lost the captains:\n%s", text)
	}
}
