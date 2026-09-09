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
