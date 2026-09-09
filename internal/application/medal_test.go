package application

import "testing"

func TestMedalBonus(t *testing.T) {
	tests := []struct {
		name       string
		playerName string
		mvp        string
		svp        string
		want       int
	}{
		{name: "MVP is rewarded", playerName: "Thorin", mvp: "Thorin", svp: "Loki", want: mvpBonusMMR},
		{name: "SVPG loss is softened", playerName: "Loki", mvp: "Thorin", svp: "Loki", want: svpMitigationMMR},
		{name: "no medal, no bonus", playerName: "Freya", mvp: "Thorin", svp: "Loki", want: 0},
		{name: "case-insensitive match", playerName: "THORIN", mvp: "thorin", svp: "", want: mvpBonusMMR},
		{name: "empty player never matches", playerName: "", mvp: "", svp: "", want: 0},
		{name: "no medals recognised", playerName: "Thorin", mvp: "", svp: "", want: 0},
		{name: "MVP wins when both match", playerName: "Thorin", mvp: "Thorin", svp: "Thorin", want: mvpBonusMMR},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := medalBonus(tt.playerName, tt.mvp, tt.svp); got != tt.want {
				t.Errorf("medalBonus(%q, %q, %q) = %d, want %d",
					tt.playerName, tt.mvp, tt.svp, got, tt.want)
			}
		})
	}
}

func TestSideMedalBonusDoesNotCrossTheScoreboard(t *testing.T) {
	// The medal names come out of the screenshot parser. A misread nick, or a
	// namesake on the other team, used to move the MVP bonus to a player on the
	// losing side — a thing the medal cannot describe.
	const mvp, svp = "Thorin", "Loki"

	tests := []struct {
		name       string
		playerName string
		won        bool
		want       int
	}{
		{name: "MVP on the winning team is paid", playerName: mvp, won: true, want: mvpBonusMMR},
		{name: "MVP name on the losing team is ignored", playerName: mvp, won: false, want: 0},
		{name: "SVPG on the losing team is paid", playerName: svp, won: false, want: svpMitigationMMR},
		{name: "SVPG name on the winning team is ignored", playerName: svp, won: true, want: 0},
		{name: "nobody else is paid, winner", playerName: "Freya", won: true, want: 0},
		{name: "nobody else is paid, loser", playerName: "Freya", won: false, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sideMedalBonus(tt.playerName, tt.won, mvp, svp); got != tt.want {
				t.Errorf("sideMedalBonus(%q, won=%v) = %d, want %d",
					tt.playerName, tt.won, got, tt.want)
			}
		})
	}
}

func TestSideMedalBonusAwardsAtMostOneMedalPerSide(t *testing.T) {
	// A single side may hold exactly one medal, so no player can collect both
	// even when the parser reports the same nick for MVP and SVPG.
	const nick = "Thorin"

	if got := sideMedalBonus(nick, true, nick, nick); got != mvpBonusMMR {
		t.Errorf("winner holding both names got %d, want only the MVP award %d", got, mvpBonusMMR)
	}
	if got := sideMedalBonus(nick, false, nick, nick); got != svpMitigationMMR {
		t.Errorf("loser holding both names got %d, want only the SVPG award %d", got, svpMitigationMMR)
	}
}

func TestMedalBonusIsNeverAPenalty(t *testing.T) {
	// The SVPG award used to subtract 5 MMR for players on Team A, punishing
	// the medal instead of softening the defeat it marks.
	for _, name := range []string{"mvp-holder", "svp-holder"} {
		if got := medalBonus(name, "mvp-holder", "svp-holder"); got <= 0 {
			t.Errorf("medal for %q gave %d, a medal must never cost MMR", name, got)
		}
	}
}
