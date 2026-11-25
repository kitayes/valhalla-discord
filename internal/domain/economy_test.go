package domain

import "testing"

func TestLevelCurveAndProgression(t *testing.T) {
	if got := XPForNextLevel(1); got != 150 {
		t.Fatalf("XPForNextLevel(1) = %d, want 150", got)
	}
	if got := XPForNextLevel(2); got != 265 {
		t.Fatalf("XPForNextLevel(2) = %d, want 265", got)
	}

	lvl, cur, need := LevelFromXP(0)
	if lvl != 1 || cur != 0 || need != 150 {
		t.Fatalf("LevelFromXP(0) = (%d,%d,%d), want (1,0,150)", lvl, cur, need)
	}

	lvl, cur, need = LevelFromXP(150)
	if lvl != 2 || cur != 0 || need != 265 {
		t.Fatalf("LevelFromXP(150) = (%d,%d,%d), want (2,0,265)", lvl, cur, need)
	}

	lvl, cur, need = LevelFromXP(414)
	if lvl != 2 || cur != 264 || need != 265 {
		t.Fatalf("LevelFromXP(414) = (%d,%d,%d), want (2,264,265)", lvl, cur, need)
	}

	lvl, cur, _ = LevelFromXP(TotalXPForLevel(10) + 42)
	if lvl != 10 || cur != 42 {
		t.Fatalf("LevelFromXP(TotalXPForLevel(10)+42) = (%d,%d), want (10,42)", lvl, cur)
	}
}

func TestLevelUpCoinReward(t *testing.T) {
	cases := []struct {
		level int
		want  int
	}{
		{1, 0},
		{2, 50},
		{3, 60},
		{4, 70},
		{5, 155},  // 80 + 75 milestone
		{10, 205}, // 130 + 75 milestone
		{30, 275}, // 200 cap + 75 milestone
		{31, 200}, // 200 cap
	}
	for _, tc := range cases {
		if got := LevelUpCoinReward(tc.level); got != tc.want {
			t.Errorf("LevelUpCoinReward(%d) = %d, want %d", tc.level, got, tc.want)
		}
	}
}

func TestDailyRewardAndTipBurn(t *testing.T) {
	if got := DailyRewardForStreak(1); got != 25 {
		t.Errorf("streak 1 = %d, want 25", got)
	}
	if got := DailyRewardForStreak(4); got != 40 {
		t.Errorf("streak 4 = %d, want 40", got)
	}
	if got := DailyRewardForStreak(99); got != 60 {
		t.Errorf("streak 99 = %d, want 60 (capped)", got)
	}
	if got := CalculateTipBurn(100); got != 5 {
		t.Errorf("burn(100) = %d, want 5", got)
	}
	if got := BetInsuranceRefund(25); got != 12 {
		t.Errorf("insurance(25) = %d, want 12", got)
	}
}

func TestParseHexColorAndSanitizeTitle(t *testing.T) {
	c, err := ParseHexColor("#F1C40F")
	if err != nil || c != 0xF1C40F {
		t.Fatalf("ParseHexColor(#F1C40F) = (%x, %v)", c, err)
	}
	if _, err := ParseHexColor("GGGGGG"); err == nil {
		t.Fatal("expected error for invalid hex")
	}

	title, err := SanitizeProfileTitle("  Бог   Валгаллы \n ")
	if err != nil || title != "Бог Валгаллы" {
		t.Fatalf("SanitizeProfileTitle = (%q, %v)", title, err)
	}
	if _, err := SanitizeProfileTitle("@everyone hack"); err == nil {
		t.Fatal("expected error for @everyone")
	}
}
