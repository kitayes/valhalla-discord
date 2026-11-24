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

	rawSim := 1.0 - float64(distance)/float64(maxLen)

	ha := foldOCRHomoglyphs(ra)
	hb := foldOCRHomoglyphs(rb)
	best := rawSim
	if homoglyphSim := (1.0 - float64(levenshteinDistanceRunes(ha, hb))/float64(maxLen)) * 0.97; homoglyphSim > best {
		best = homoglyphSim
	}

	// Squad tags and stylized spaced nicks ("APEX MIKAELO" vs "APEXMIKAELO",
	// "E L I Z A" vs "OELIZA") often lose spaces in OCR.
	sa := stripSpaceRunes(ha)
	sb := stripSpaceRunes(hb)
	if len(sa) > 0 && len(sb) > 0 && (len(sa) != len(ha) || len(sb) != len(hb)) {
		sMax := max(len(sa), len(sb))
		if spacelessSim := (1.0 - float64(levenshteinDistanceRunes(sa, sb))/float64(sMax)) * 0.94; spacelessSim > best {
			best = spacelessSim
		}
	}

	// Mixed CJK + Latin/Cyrillic nicks ("Fumetsu 不滅" vs "Fumetsu", "中國 Lens" vs "Lens"):
	// ESLAV OCR reads only the Latin/Cyrillic part, so compare non-CJK runes when
	// one side contains CJK and the remaining token has at least 3 runes.
	if hasCJKRunes(ra) != hasCJKRunes(rb) {
		ca := stripSpaceRunes(stripCJKRunes(ha))
		cb := stripSpaceRunes(stripCJKRunes(hb))
		if len(ca) >= 3 && len(cb) >= 3 {
			cMax := max(len(ca), len(cb))
			if cjkSim := (1.0 - float64(levenshteinDistanceRunes(ca, cb))/float64(cMax)) * 0.90; cjkSim > best {
				best = cjkSim
			}
		}
	}

	return best
}

var ocrHomoglyphs = map[rune]rune{
	'а': 'a',
	'б': 'b',
	'6': 'b',
	'в': 'b',
	'г': 'r',
	'д': 'd',
	'е': 'e',
	'ё': 'e',
	'и': 'u',
	'й': 'u',
	'к': 'k',
	'л': 'n',
	'м': 'm',
	'н': 'h',
	'о': 'o',
	'п': 'n',
	'р': 'p',
	'с': 'c',
	'т': 't',
	'у': 'y',
	'ф': 'o',
	'х': 'x',
	'ч': '4',
	'ь': 'b',
	'ъ': 'b',
	'ы': 'b',
	'я': 'r',
	'ø': 'o',
	'ξ': 'e',
	'λ': 'a',
	'ʌ': 'a',
	'ɪ': 'i',
	'ᴋ': 'k',
	'ᴀ': 'a',
	'é': 'e',
	'ë': 'e',
	'è': 'e',
	'ê': 'e',
	'ō': 'o',
	'ẹ': 'e',
	'ŵ': 'w',
	'ϟ': '4',
	'і': 'i',
	'ї': 'i',
	'қ': 'k',
}

func stripSpaceRunes(in []rune) []rune {
	out := make([]rune, 0, len(in))
	for _, r := range in {
		if !unicode.IsSpace(r) {
			out = append(out, r)
		}
	}
	return out
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func hasCJKRunes(in []rune) bool {
	for _, r := range in {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

func stripCJKRunes(in []rune) []rune {
	out := make([]rune, 0, len(in))
	for _, r := range in {
		if !isCJKRune(r) {
			out = append(out, r)
		}
	}
	return out
}

func foldOCRHomoglyphs(in []rune) []rune {
	out := make([]rune, len(in))
	for i, r := range in {
		if mapped, ok := ocrHomoglyphs[r]; ok {
			out[i] = mapped
		} else {
			out[i] = r
		}
	}
	return out
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
