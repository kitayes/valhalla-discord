package application

import (
	"strings"
	"testing"
)

func TestFormatPayoutSummarySeparatesTheTwoEmptyOutcomes(t *testing.T) {
	// Payouts comes back empty for two unrelated reasons, and the announcement
	// goes to a public channel. "Ставок не было" in front of people who had just
	// lost their stakes backing the other team is the one message that must not
	// be wrong.
	noBets := FormatPayoutSummary(PayoutResult{
		MatchID: 7, WinningTeam: "Team A", Payouts: map[int64]int{}, BetCount: 0,
	})
	if !strings.Contains(noBets, "Ставок не было") {
		t.Errorf("a match nobody bet on rendered as %q", noBets)
	}

	allLost := FormatPayoutSummary(PayoutResult{
		MatchID: 7, WinningTeam: "Team A", Payouts: map[int64]int{}, BetCount: 3,
	})
	if strings.Contains(allLost, "Ставок не было") {
		t.Errorf("three losing bets were announced as no bets at all: %q", allLost)
	}
	if !strings.Contains(allLost, "Team A") {
		t.Errorf("the winner is missing from %q", allLost)
	}
}

func TestFormatPayoutSummaryIsStableAcrossCalls(t *testing.T) {
	// Rendered from a map: an unsorted range reordered the lines on every call,
	// so two reports of one match read as two different results.
	res := PayoutResult{
		MatchID:     7,
		WinningTeam: "Team B",
		Payouts:     map[int64]int{500: 50, 100: 10, 300: 30, 200: 20, 400: 40},
		BetCount:    5,
	}

	first := FormatPayoutSummary(res)
	for range 20 {
		if got := FormatPayoutSummary(res); got != first {
			t.Fatalf("summary is not stable:\n%q\nvs\n%q", first, got)
		}
	}

	for _, want := range []string{"`100`", "`500`", "+10", "+50"} {
		if !strings.Contains(first, want) {
			t.Errorf("summary %q is missing %s", first, want)
		}
	}
	if a, b := strings.Index(first, "`100`"), strings.Index(first, "`500`"); a > b {
		t.Error("payout lines are not in ascending user order")
	}
}

func TestPayoutResultHadBets(t *testing.T) {
	if (PayoutResult{BetCount: 0}).HadBets() {
		t.Error("a match with no bets reports that it had some")
	}
	if !(PayoutResult{BetCount: 1}).HadBets() {
		t.Error("a match with one bet reports that it had none")
	}
}
