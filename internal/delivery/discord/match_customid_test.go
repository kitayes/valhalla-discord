package discord

import (
	"fmt"
	"testing"
)

// The match creation wizard threads the captain and team selections through the
// component custom IDs. Splitting those IDs on "_" without accounting for the
// underscores already present in the constant prefixes silently mangled the
// captain IDs, which then reached CreateMatch as 0 and failed the players(id)
// foreign key. These tests pin the round trip.

func TestParseCaptainBCustomID(t *testing.T) {
	customID := fmt.Sprintf("%s_%s", selectMenuCaptainB, "42")

	captainAID, ok := parseCaptainBCustomID(customID)
	if !ok {
		t.Fatalf("parseCaptainBCustomID(%q) failed", customID)
	}
	if captainAID != "42" {
		t.Errorf("captainAID = %q, want %q", captainAID, "42")
	}
}

func TestParseTeamACustomID(t *testing.T) {
	customID := fmt.Sprintf("%s_%s_%s", selectMenuTeamA, "42", "57")

	captainAID, captainBID, ok := parseTeamACustomID(customID)
	if !ok {
		t.Fatalf("parseTeamACustomID(%q) failed", customID)
	}
	if captainAID != "42" || captainBID != "57" {
		t.Errorf("got captains (%q, %q), want (%q, %q)", captainAID, captainBID, "42", "57")
	}
}

func TestParseTeamBCustomID(t *testing.T) {
	customID := fmt.Sprintf("%s_%s_%s_%s", selectMenuTeamB, "42", "57", "1-2-3-4")

	captainAID, captainBID, teamA, ok := parseTeamBCustomID(customID)
	if !ok {
		t.Fatalf("parseTeamBCustomID(%q) failed", customID)
	}
	if captainAID != "42" || captainBID != "57" {
		t.Errorf("got captains (%q, %q), want (%q, %q)", captainAID, captainBID, "42", "57")
	}
	want := []string{"1", "2", "3", "4"}
	if len(teamA) != len(want) {
		t.Fatalf("teamA = %v, want %v", teamA, want)
	}
	for i := range want {
		if teamA[i] != want[i] {
			t.Errorf("teamA[%d] = %q, want %q", i, teamA[i], want[i])
		}
	}
}

// A malformed ID must be rejected rather than panic: these handlers run on
// discordgo's own goroutines.
func TestParseCustomIDRejectsMalformed(t *testing.T) {
	if _, ok := parseCaptainBCustomID("garbage"); ok {
		t.Error("parseCaptainBCustomID accepted a foreign custom ID")
	}
	if _, _, ok := parseTeamACustomID(selectMenuTeamA + "_42"); ok {
		t.Error("parseTeamACustomID accepted an ID with a missing captain")
	}
	if _, _, _, ok := parseTeamBCustomID(selectMenuTeamB + "_42_57"); ok {
		t.Error("parseTeamBCustomID accepted an ID with no team A")
	}
}

func TestParseIDRejectsNonNumeric(t *testing.T) {
	if got := parseID("b_42"); got != 0 {
		t.Errorf("parseID(%q) = %d, want 0", "b_42", got)
	}
	if got := parseID("42"); got != 42 {
		t.Errorf("parseID(%q) = %d, want 42", "42", got)
	}
}

// The mix requeue button carries the whole roster, since a mix has no match row
// to look it up in.
func TestMixRequeueCustomIDRoundTrip(t *testing.T) {
	roster := []int{42, 7, 1000, 3}

	customID, ok := mixRequeueCustomID(roster)
	if !ok {
		t.Fatalf("mixRequeueCustomID(%v) reported the roster does not fit", roster)
	}
	got, ok := parseMixRequeueCustomID(customID)
	if !ok {
		t.Fatalf("parseMixRequeueCustomID(%q) failed", customID)
	}
	if fmt.Sprint(got) != fmt.Sprint(roster) {
		t.Errorf("roster = %v, want %v", got, roster)
	}
}

// Ten players with large IDs must still fit Discord's 100-character custom ID.
func TestMixRequeueCustomIDFitsTenPlayers(t *testing.T) {
	roster := make([]int, 10)
	for i := range roster {
		roster[i] = 1_000_000 + i
	}
	if customID, ok := mixRequeueCustomID(roster); !ok || len(customID) > maxCustomIDLength {
		t.Errorf("custom ID %q (%d chars) does not fit", customID, len(customID))
	}
}

func TestParseMixRequeueCustomIDRejectsGarbage(t *testing.T) {
	for _, customID := range []string{
		buttonMixRequeue + "_",
		buttonMixRequeue + "_1--2",
		buttonMixRequeue + "_1-abc",
		"requeue_5",
	} {
		if ids, ok := parseMixRequeueCustomID(customID); ok {
			t.Errorf("parseMixRequeueCustomID(%q) = %v, want failure", customID, ids)
		}
	}
}
