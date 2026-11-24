package ai

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var nonAlphaNumericRegex = regexp.MustCompile(`[^\p{L}\p{N}\s._-]`)

func NormalizeName(name string) string {
	name = norm.NFKC.String(name)
	name = strings.TrimSpace(name)

	name = nonAlphaNumericRegex.ReplaceAllString(name, "")

	name = strings.TrimSpace(name)

	var result strings.Builder
	prevSpace := false
	for _, r := range name {
		if unicode.IsSpace(r) {
			if !prevSpace {
				result.WriteRune(' ')
				prevSpace = true
			}
		} else {
			result.WriteRune(r)
			prevSpace = false
		}
	}

	return strings.TrimSpace(result.String())
}

func SimilarityScore(a, b string) float64 {
	if a == b {
		return 1.0
	}

	a = strings.ToLower(NormalizeName(a))
	b = strings.ToLower(NormalizeName(b))

	if a == b {
		return 1.0
	}

	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0.0
	}

	distance := levenshteinDistanceRunes(ra, rb)
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}

	return 1.0 - float64(distance)/float64(maxLen)
}

func levenshteinDistanceRunes(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			matrix[i][j] = min(
				matrix[i-1][j]+1,
				matrix[i][j-1]+1,
				matrix[i-1][j-1]+cost,
			)
		}
	}

	return matrix[len(a)][len(b)]
}
