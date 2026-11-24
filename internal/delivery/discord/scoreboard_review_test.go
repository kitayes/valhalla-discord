package discord

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

func TestParseReviewCustomIDs(t *testing.T) {
	if id, ok := parseReviewMatchID("sb_confirm_42", sbConfirmPrefix); !ok || id != 42 {
		t.Errorf("parseReviewMatchID(sb_confirm_42) = (%d, %v), want (42, true)", id, ok)
	}
	if id, ok := parseReviewMatchID("sb_confirm_42abc", sbConfirmPrefix); ok || id != 0 {
		t.Errorf("parseReviewMatchID(sb_confirm_42abc) = (%d, %v), want (0, false)", id, ok)
	}
	if _, ok := parseReviewMatchID("sb_flip_0", sbFlipPrefix); ok {
		t.Error("matchID=0 should be rejected")
	}

	if mid, ridx, ok := parseReviewMatchRowID("sb_assign_42_0", sbAssignPrefix); !ok || mid != 42 || ridx != 0 {
		t.Errorf("parseReviewMatchRowID(sb_assign_42_0) = (%d, %d, %v), want (42, 0, true)", mid, ridx, ok)
	}
	if mid, ridx, ok := parseReviewMatchRowID("sb_modal_kda_15_9", sbModalKDAPrefix); !ok || mid != 15 || ridx != 9 {
		t.Errorf("parseReviewMatchRowID(sb_modal_kda_15_9) = (%d, %d, %v), want (15, 9, true)", mid, ridx, ok)
	}
	for _, bad := range []string{"sb_assign_42", "sb_assign_42_-1", "sb_assign_42_abc", "sb_modal_kda_0_1", "other_42_1"} {
		if _, _, ok := parseReviewMatchRowID(bad, sbAssignPrefix); ok {
			t.Errorf("parseReviewMatchRowID(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestBuildScoreboardPreviewComponentsStates(t *testing.T) {
	clean := &application.ScoreboardMatchResult{
		Winner:      domain.TeamA,
		MVPPlayerID: 1,
		SVPPlayerID: 6,
		Rows: []application.MatchedRow{
			{Index: 0, OCRName: "Bjorn", PlayerID: 1, PlayerName: "Bjorn", Result: "WIN", Kills: 10, Deaths: 2, Assists: 7},
			{Index: 1, OCRName: "Gunnar", PlayerID: 6, PlayerName: "Gunnar", Result: "LOSE", Kills: 3, Deaths: 8, Assists: 4},
		},
	}

	comps := buildScoreboardPreviewComponents(42, clean)
	if len(comps) != 2 {
		t.Fatalf("clean match components len = %d, want 2 (buttons + KDA select)", len(comps))
	}
	btnRow := comps[0].(discordgo.ActionsRow)
	confirmBtn := btnRow.Components[0].(discordgo.Button)
	if confirmBtn.Disabled {
		t.Error("Confirm button should be enabled when winner is known and all rows are resolved")
	}

	// With unresolved row -> Confirm disabled and Row 3 assign select present.
	unresolved := &application.ScoreboardMatchResult{
		Winner:         domain.TeamA,
		UnresolvedRows: []int{0},
		UnassignedTeamA: []application.RosterPlayer{
			{PlayerID: 1, Name: "Bjorn"},
		},
		Rows: []application.MatchedRow{
			{Index: 0, OCRName: "SomeNick", Result: "WIN", Kills: 5, Deaths: 1, Assists: 3},
			{Index: 1, OCRName: "Gunnar", PlayerID: 6, PlayerName: "Gunnar", Result: "LOSE", Kills: 2, Deaths: 5, Assists: 1},
		},
	}
	compsUnres := buildScoreboardPreviewComponents(42, unresolved)
	if len(compsUnres) != 3 {
		t.Fatalf("unresolved match components len = %d, want 3 (buttons + KDA select + assign select)", len(compsUnres))
	}
	confirmDisabledBtn := compsUnres[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if !confirmDisabledBtn.Disabled {
		t.Error("Confirm button must be disabled when unresolved rows exist")
	}
	assignMenu := compsUnres[2].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if assignMenu.CustomID != "sb_assign_42_0" {
		t.Errorf("assign menu CustomID = %q, want sb_assign_42_0", assignMenu.CustomID)
	}

	// Ambiguous winner -> Confirm disabled.
	noWinner := &application.ScoreboardMatchResult{
		Winner: "",
		Rows:   clean.Rows,
	}
	compsNoWin := buildScoreboardPreviewComponents(42, noWinner)
	if !compsNoWin[0].(discordgo.ActionsRow).Components[0].(discordgo.Button).Disabled {
		t.Error("Confirm button must be disabled when Winner is empty")
	}
}

func TestScoreboardPreviewEmbedAndRuneTruncation(t *testing.T) {
	longCyrillic := strings.Repeat("Бьорн", 30) // 150 Cyrillic runes, 300 bytes
	truncated := runeTruncate(longCyrillic, 45)
	if utf8.RuneCountInString(truncated) > 45 {
		t.Errorf("runeTruncate exceeded 45 runes: %d", utf8.RuneCountInString(truncated))
	}
	if !utf8.ValidString(truncated) {
		t.Error("runeTruncate produced invalid UTF-8")
	}

	res := &application.ScoreboardMatchResult{
		Winner:      domain.TeamA,
		MVPPlayerID: 1,
		SVPPlayerID: 2,
		Warnings:    []string{"сумма киллов не сходится"},
		Rows: []application.MatchedRow{
			{Index: 0, OCRName: "Бьорн", PlayerID: 1, PlayerName: "Бьорн", Result: "WIN", Kills: 11, Deaths: 2, Assists: 7},
			{Index: 1, OCRName: "Гуннар", PlayerID: 2, PlayerName: "Гуннар", Result: "LOSE", Kills: 5, Deaths: 4, Assists: 3},
		},
	}
	breakdowns := map[int]application.MatchMMRBreakdown{
		1: {PlayerID: 1, BaseDelta: 24, KDAMod: 5, MedalBonus: 3, TotalDelta: 32},
		2: {PlayerID: 2, BaseDelta: -24, KDAMod: 8, MedalBonus: 4, TotalDelta: -12},
	}

	embed := buildScoreboardPreviewEmbed(77, res, breakdowns)
	if !strings.Contains(embed.Title, "#77") || !strings.Contains(embed.Title, "Team A") {
		t.Errorf("unexpected preview title: %q", embed.Title)
	}
	if len(embed.Fields) != 3 {
		t.Fatalf("len(Fields) = %d, want 3 (Team A, Team B, Warnings)", len(embed.Fields))
	}
	if !strings.Contains(embed.Fields[0].Value, "+32") || !strings.Contains(embed.Fields[0].Value, "[MVP]") {
		t.Errorf("Team A preview field missing delta/MVP: %q", embed.Fields[0].Value)
	}
	if !strings.Contains(embed.Fields[1].Value, "-12") || !strings.Contains(embed.Fields[1].Value, "[SVPG]") {
		t.Errorf("Team B preview field missing delta/SVPG: %q", embed.Fields[1].Value)
	}
	if !strings.Contains(embed.Fields[2].Value, "сумма киллов") {
		t.Errorf("Warnings field missing expected text: %q", embed.Fields[2].Value)
	}
}

func TestFormatMMRHistoryLineAndMergeButtonParsing(t *testing.T) {
	ts := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

	withBreakdown := formatMMRHistoryLine(models.MMRChange{
		MatchID:    42,
		Before:     1500,
		After:      1532,
		Delta:      32,
		BaseDelta:  24,
		KDAMod:     5,
		MedalBonus: 3,
		CreatedAt:  ts,
	})
	for _, want := range []string{"`+32` → 1532", "+24 база", "+5 KDA", "+3 медаль", "матч #42", "24.11"} {
		if !strings.Contains(withBreakdown, want) {
			t.Errorf("formatMMRHistoryLine(breakdown) = %q, missing %q", withBreakdown, want)
		}
	}

	legacy := formatMMRHistoryLine(models.MMRChange{
		MatchID:   41,
		Before:    1482,
		After:     1500,
		Delta:     18,
		CreatedAt: ts,
	})
	if strings.Contains(legacy, "KDA") || !strings.Contains(legacy, "матч #41") {
		t.Errorf("formatMMRHistoryLine(legacy) = %q, unexpected breakdown", legacy)
	}

	if a, b, ok := parseTwoIDsFromPrefix("confirm_merge_player_12_34", "confirm_merge_player_"); !ok || a != 12 || b != 34 {
		t.Errorf("parseTwoIDsFromPrefix = (%d, %d, %v), want (12, 34, true)", a, b, ok)
	}
	for _, bad := range []string{"confirm_merge_player_12_12", "confirm_merge_player_0_5", "confirm_merge_player_12", "other_1_2"} {
		if _, _, ok := parseTwoIDsFromPrefix(bad, "confirm_merge_player_"); ok {
			t.Errorf("parseTwoIDsFromPrefix(%q) unexpectedly succeeded", bad)
		}
	}
}
