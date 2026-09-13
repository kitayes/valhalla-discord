package application

import (
	"strings"
	"testing"
)

func TestParsePlayerLine(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    playerLine
		wantErr string // substring of the problem text (case-insensitive), "" for success
	}{
		{"plain", "Kitayes 123456789 1234 25", playerLine{Nick: "Kitayes", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"zone in parens", "Kitayes 123456789 (1234) 25", playerLine{Nick: "Kitayes", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"commas", "Kitayes, 123456789, 1234, 25", playerLine{Nick: "Kitayes", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"two-word nick", "Big Boss 123456789 1234 25", playerLine{Nick: "Big Boss", GameID: "123456789", ZoneID: "1234", Stars: 25}, ""},
		{"contact at end", "Vasya 123456789 1234 25 @vasya", playerLine{Nick: "Vasya", GameID: "123456789", ZoneID: "1234", Stars: 25, Contact: "@vasya"}, ""},
		{"contact in middle", "Vasya @vasya 123456789 1234 25", playerLine{Nick: "Vasya", GameID: "123456789", ZoneID: "1234", Stars: 25, Contact: "@vasya"}, ""},
		{"zero stars", "Newbie 123456789 1234 0", playerLine{Nick: "Newbie", GameID: "123456789", ZoneID: "1234", Stars: 0}, ""},
		{"missing zone and stars", "Kitayes 123456789", playerLine{}, "Zone ID"},
		{"missing nick", "123456789 1234 25", playerLine{}, "ник"},
		{"stars not a number", "Kitayes 123456789 1234 много", playerLine{}, "Звёзды"},
		{"trailing garbage", "Kitayes 123456789 1234 25 лишнее", playerLine{}, "лишнее"},
		{"two contacts", "Vasya 123456789 1234 25 @a @b", playerLine{}, "контакт"},
		{"empty", "   ", playerLine{}, "ник"},
		{"nick too long", strings.Repeat("x", 65) + " 123456789 1234 25", playerLine{}, "64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := parsePlayerLine(tc.in)
			if tc.wantErr != "" {
				if !strings.Contains(strings.ToLower(problem), strings.ToLower(tc.wantErr)) {
					t.Fatalf("problem = %q, want containing %q", problem, tc.wantErr)
				}
				return
			}
			if problem != "" {
				t.Fatalf("unexpected problem: %q", problem)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// MLBB ids are 8–10 digits and zones 4–5; anything else is most likely a
// typo, but the bot only warns — a new region could prove it wrong.
func TestPlausibilityWarnings(t *testing.T) {
	cases := []struct {
		line playerLine
		want []string // substrings, one per warning; nil = no warnings
	}{
		{playerLine{GameID: "123456789", ZoneID: "1234"}, nil},
		{playerLine{GameID: "12345678", ZoneID: "12345"}, nil},
		{playerLine{GameID: "1234567890", ZoneID: "1234"}, nil},
		{playerLine{GameID: "123", ZoneID: "1234"}, []string{"GameID 123"}},
		{playerLine{GameID: "123456789", ZoneID: "1"}, []string{"Zone ID 1"}},
		{playerLine{GameID: "12345678901", ZoneID: "123456"}, []string{"GameID", "Zone ID"}},
	}
	for _, tc := range cases {
		got := plausibilityWarnings(tc.line)
		if len(got) != len(tc.want) {
			t.Errorf("%+v: %v, want %d warning(s)", tc.line, got, len(tc.want))
			continue
		}
		for i := range got {
			if !strings.Contains(got[i], tc.want[i]) {
				t.Errorf("%+v: warning %q lacks %q", tc.line, got[i], tc.want[i])
			}
		}
	}
}
