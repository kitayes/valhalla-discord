package ai

import (
	"math"
	"testing"
)

func TestNormalizeNameNFKCAndPunctuation(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "  𝐀𝐛𝐜  ", want: "Abc"},
		{in: "ᵃᵇᶜ", want: "abc"},
		{in: "ＦｕｌｌＷｉｄｔｈ", want: "FullWidth"},
		{in: "★Бьорн★  _99", want: "Бьорн _99"},
		{in: "  alpha   beta ", want: "alpha beta"},
	}

	for _, tc := range tests {
		if got := NormalizeName(tc.in); got != tc.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSimilarityScoreRunesAndCyrillic(t *testing.T) {
	if got := SimilarityScore("𝐀𝐥𝐩𝐡𝐚", "alpha"); got != 1.0 {
		t.Errorf("NFKC similarity = %v, want 1.0", got)
	}

	// Cyrillic 5-rune names differing by 1 rune: distance 1 / 5 = 0.8 similarity.
	got := SimilarityScore("Бьорн", "Бьерн")
	if math.Abs(got-0.8) > 0.001 {
		t.Errorf("Cyrillic rune similarity = %v, want 0.8", got)
	}

	if got := SimilarityScore("", "abc"); got != 0.0 {
		t.Errorf("empty vs non-empty = %v, want 0.0", got)
	}
}
