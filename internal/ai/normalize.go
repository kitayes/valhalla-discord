package ai

import (
	"regexp"
	"strings"
	"unicode"
)

var nonAlphaNumericRegex = regexp.MustCompile(`[^\p{L}\p{N}\s._-]`)

func NormalizeName(name string) string {
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

	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	distance := levenshteinDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	return 1.0 - float64(distance)/float64(maxLen)
}

func levenshteinDistance(a, b string) int {
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

func min(nums ...int) int {
	minVal := nums[0]
	for _, n := range nums[1:] {
		if n < minVal {
			minVal = n
		}
	}
	return minVal
}
