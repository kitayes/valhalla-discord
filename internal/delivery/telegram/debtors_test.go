package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/models"
	"fmt"
	"strings"
	"testing"
)

func TestFormatCaptainMention(t *testing.T) {
	tgID := int64(12345)
	teamWithUsername := models.TelegramTeam{
		ID:   1,
		Name: "Alpha",
		Players: []models.TelegramPlayer{
			{
				ID:               1,
				TelegramID:       &tgID,
				TelegramUsername: "alphacap",
				GameNickname:     "AlphaLeader",
				IsCaptain:        true,
			},
		},
	}

	if got := formatCaptainMention(teamWithUsername); got != "@alphacap (AlphaLeader)" {
		t.Errorf("formatCaptainMention() = %q, want %q", got, "@alphacap (AlphaLeader)")
	}

	teamWithoutUsername := models.TelegramTeam{
		ID:   2,
		Name: "Beta",
		Players: []models.TelegramPlayer{
			{
				ID:           2,
				TelegramID:   &tgID,
				GameNickname: "BetaLeader",
				IsCaptain:    true,
			},
		},
	}

	if got := formatCaptainMention(teamWithoutUsername); got != "BetaLeader" {
		t.Errorf("formatCaptainMention() = %q, want %q", got, "BetaLeader")
	}

	teamWithoutCaptain := models.TelegramTeam{
		ID:   3,
		Name: "Gamma",
	}

	if got := formatCaptainMention(teamWithoutCaptain); got != "Капитан не указан" {
		t.Errorf("formatCaptainMention() = %q, want %q", got, "Капитан не указан")
	}
}

func TestBuildDebtorsChatAnnouncement(t *testing.T) {
	tgID := int64(111)
	pending := []models.TelegramTeam{
		{
			ID:   1,
			Name: "Navi",
			Players: []models.TelegramPlayer{
				{TelegramID: &tgID, TelegramUsername: "@dendi", GameNickname: "Dendi", IsCaptain: true},
			},
		},
	}

	incomplete := []models.TelegramTeam{
		{
			ID:   2,
			Name: "SoloSquad",
			Players: []models.TelegramPlayer{
				{TelegramID: &tgID, TelegramUsername: "lonely", GameNickname: "Lonely", IsCaptain: true},
				{TelegramID: nil, GameNickname: "P2"},
			},
		},
	}

	ann := buildDebtorsChatAnnouncement(pending, incomplete, "Поторопитесь!", "BlackwatchBot")

	expectedSubstrings := []string{
		"ВНИМАНИЕ, ДОЛЖНИКИ ТУРНИРА! ⚠️",
		"Ожидают Check-in (1):",
		"1. Navi — @dendi (Dendi)",
		"Неполный состав (1):",
		fmt.Sprintf("1. SoloSquad (2/%d) — @lonely (Lonely)", application.MainRosterSlots),
		"Сообщение от организаторов:\nПоторопитесь!",
		"@BlackwatchBot",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(ann, sub) {
			t.Errorf("announcement missing %q\nFull output:\n%s", sub, ann)
		}
	}
}

func TestBuildDebtorsAdminReport(t *testing.T) {
	tgID := int64(222)
	pending := []models.TelegramTeam{
		{
			ID:   1,
			Name: "Navi",
			Players: []models.TelegramPlayer{
				{TelegramID: &tgID, TelegramUsername: "dendi", GameNickname: "Dendi", IsCaptain: true},
			},
		},
	}
	incomplete := []models.TelegramTeam{
		{
			ID:   2,
			Name: "SoloSquad",
			Players: []models.TelegramPlayer{
				{TelegramID: &tgID, TelegramUsername: "lonely", GameNickname: "Lonely", IsCaptain: true},
			},
		},
	}

	report := buildDebtorsAdminReport(pending, incomplete, 2, 0, true)

	expectedSubstrings := []string{
		"📢 Пинг должников завершён:",
		"• Ожидают Check-in: 1",
		"• Неполный состав: 1",
		"• Доставлено в ЛС: 2",
		"• Турнирный чат: объявление с тегами отправлено",
		"[-] Navi: @dendi (Dendi)",
		"[!] SoloSquad (1/5): @lonely (Lonely)",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(report, sub) {
			t.Errorf("report missing %q\nFull output:\n%s", sub, report)
		}
	}
}
