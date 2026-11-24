package ai

import (
	"strings"
	"testing"

	"blackwatch/internal/models"
)

func validTenPlayerMatch() *models.Match {
	winKills := 20
	loseKills := 12
	return &models.Match{
		MVP:             "P1",
		SVP:             "P6",
		HeaderWinKills:  &winKills,
		HeaderLoseKills: &loseKills,
		Players: []models.PlayerResult{
			{PlayerName: "P1", Result: "WIN", Kills: 8, Deaths: 2, Assists: 6, Confidence: 0.95},
			{PlayerName: "P2", Result: "WIN", Kills: 5, Deaths: 2, Assists: 7, Confidence: 0.95},
			{PlayerName: "P3", Result: "WIN", Kills: 3, Deaths: 3, Assists: 9, Confidence: 0.95},
			{PlayerName: "P4", Result: "WIN", Kills: 2, Deaths: 2, Assists: 10, Confidence: 0.95},
			{PlayerName: "P5", Result: "WIN", Kills: 2, Deaths: 3, Assists: 8, Confidence: 0.95},
			{PlayerName: "P6", Result: "LOSE", Kills: 6, Deaths: 3, Assists: 4, Confidence: 0.95},
			{PlayerName: "P7", Result: "LOSE", Kills: 2, Deaths: 4, Assists: 5, Confidence: 0.95},
			{PlayerName: "P8", Result: "LOSE", Kills: 2, Deaths: 5, Assists: 4, Confidence: 0.95},
			{PlayerName: "P9", Result: "LOSE", Kills: 1, Deaths: 4, Assists: 6, Confidence: 0.95},
			{PlayerName: "P10", Result: "LOSE", Kills: 1, Deaths: 4, Assists: 5, Confidence: 0.95},
		},
	}
}

func TestValidateScoreboardCleanMatch(t *testing.T) {
	if got := ValidateScoreboard(validTenPlayerMatch()); len(got) != 0 {
		t.Fatalf("clean match produced warnings: %v", got)
	}
}

func TestValidateScoreboardDetectsAnomalies(t *testing.T) {
	t.Run("header mismatch", func(t *testing.T) {
		m := validTenPlayerMatch()
		wrong := 25
		m.HeaderWinKills = &wrong
		warnings := ValidateScoreboard(m)
		assertContainsWarning(t, warnings, "шапке")
	})

	t.Run("kills exceed enemy deaths", func(t *testing.T) {
		m := validTenPlayerMatch()
		m.Players[0].Kills = 20 // winKills becomes 32 > loseDeaths 20
		warnings := ValidateScoreboard(m)
		assertContainsWarning(t, warnings, "превышают смерти проигравших")
	})

	t.Run("impossible assists", func(t *testing.T) {
		m := validTenPlayerMatch()
		m.Players[0].Assists = 18 // > 20 - 8 = 12
		warnings := ValidateScoreboard(m)
		assertContainsWarning(t, warnings, "ассистов")
	})

	t.Run("low OCR confidence and missing medals", func(t *testing.T) {
		m := validTenPlayerMatch()
		m.Players[2].Confidence = 0.62
		m.MVP = ""
		m.SVP = ""
		warnings := ValidateScoreboard(m)
		assertContainsWarning(t, warnings, "низкая уверенность")
		assertContainsWarning(t, warnings, "MVP")
		assertContainsWarning(t, warnings, "SVPG")
	})

	t.Run("wrong row count and sides", func(t *testing.T) {
		m := validTenPlayerMatch()
		m.Players = m.Players[:9]
		warnings := ValidateScoreboard(m)
		assertContainsWarning(t, warnings, "вместо 10")
	})
}

func assertContainsWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("expected a warning containing %q, got %v", substr, warnings)
}
