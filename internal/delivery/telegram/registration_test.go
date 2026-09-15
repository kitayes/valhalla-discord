package telegram

import (
	"blackwatch/internal/application"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestParseRegCallback(t *testing.T) {
	cases := []struct {
		in          string
		action, arg string
		ok          bool
	}{
		{"reg:role:Mid", "role", "Mid", true},
		{"reg:skip", "skip", "", true},
		{"reg:fix:3", "fix", "3", true},
		{"reg:fix", "fix", "", true},
		{"reg", "", "", false},
		{"bet:a:10:5", "", "", false},
		{"reg:role:Mid:extra", "", "", false},
		{"reg::", "", "", false},
	}
	for _, tc := range cases {
		action, arg, ok := parseRegCallback(tc.in)
		if action != tc.action || arg != tc.arg || ok != tc.ok {
			t.Errorf("%q → (%q,%q,%v), want (%q,%q,%v)", tc.in, action, arg, ok, tc.action, tc.arg, tc.ok)
		}
	}
}

func flatten(kb tgbotapi.InlineKeyboardMarkup) []string {
	var out []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil {
				out = append(out, *btn.CallbackData)
			}
		}
	}
	return out
}

func TestRegKeyboards(t *testing.T) {
	cases := []struct {
		kbType string
		want   []string
	}{
		{application.KbRegCancel, []string{"reg:cancel"}},
		{application.KbRegSkip, []string{"reg:skip", "reg:cancel"}},
		{application.KbRegRoles, []string{"reg:role:Gold", "reg:role:Exp", "reg:role:Mid", "reg:role:Roam", "reg:role:Jungle", "reg:redo", "reg:cancel"}},
		{application.KbRegConfirm + ":3", []string{"reg:confirm", "reg:fix:1", "reg:fix:2", "reg:fix:3", "reg:sub", "reg:delete"}},
		{application.KbRegConfirm + ":7", []string{"reg:confirm", "reg:fix:1", "reg:fix:2", "reg:fix:3", "reg:fix:4", "reg:fix:5", "reg:fix:6", "reg:fix:7", "reg:delete"}},
		{application.KbRegCard + ":2:sub", []string{"reg:fix:1", "reg:fix:2", "reg:sub", "reg:delete"}},
		{application.KbRegCard + ":7:", []string{"reg:fix:1", "reg:fix:2", "reg:fix:3", "reg:fix:4", "reg:fix:5", "reg:fix:6", "reg:fix:7", "reg:delete"}},
		{application.KbRegSoloConfirm, []string{"reg:confirm", "reg:fix"}},
		{application.KbRegDeleteConfirm, []string{"reg:delete_yes", "reg:delete_no"}},
		{application.KbRegPrefill, []string{"reg:prefill", "reg:retype", "reg:cancel"}},
		{application.KbRegSubFix + ":6", []string{"reg:del_sub:6", "reg:cancel"}},
		{application.KbRegProfile + ":empty", []string{"reg:prof_edit", "reg:prof_discord"}},
		{application.KbRegProfile + ":filled", []string{"reg:prof_edit", "reg:prof_discord"}},
	}
	for _, tc := range cases {
		kb, ok := regKeyboard(tc.kbType)
		if !ok {
			t.Errorf("%q: not recognised", tc.kbType)
			continue
		}
		got := flatten(kb)
		if len(got) != len(tc.want) {
			t.Errorf("%q: %v, want %v", tc.kbType, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: %v, want %v", tc.kbType, got, tc.want)
				break
			}
		}
	}
	if _, ok := regKeyboard("main_menu"); ok {
		t.Error("main_menu must not be treated as a registration keyboard")
	}
}

func TestCheckinKeyboard(t *testing.T) {
	kb, ok := regKeyboard(application.KbRegCheckin)
	if !ok {
		t.Fatal("reg_checkin not recognised")
	}
	if got := flatten(kb); len(got) != 1 || got[0] != "reg:checkin" {
		t.Errorf("buttons = %v, want [reg:checkin]", got)
	}
}
