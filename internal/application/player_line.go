package application

import (
	"fmt"
	"strings"
)

// playerLine is one roster entry as typed by the captain:
// "Ник GameID ZoneID Звёзды [@контакт]".
type playerLine struct {
	Nick    string
	GameID  string
	ZoneID  string
	Stars   int
	Contact string
}

var knownLabels = map[string]bool{
	"ник:": true, "nick:": true,
	"id:": true, "айди:": true, "gameid:": true, "game_id:": true,
	"zone:": true, "zoneid:": true, "zone_id:": true, "зона:": true,
	"звёзды:": true, "звезды:": true, "звёзд:": true, "звезд:": true, "stars:": true,
	"tg:": true, "telegram:": true, "контакт:": true,
}

// parsePlayerLine reads a roster line. Separators are spaces, newlines and/or commas;
// the nick is everything before the first numeric token and may contain
// spaces; the zone may be written as "(1234)" or "12345(6789)"; one optional "@contact" token
// may appear anywhere.
//
// The second value is a user-facing Russian sentence naming what was wrong,
// or "" on success. It is a string rather than an error on purpose: it is
// copy for the chat, not a failure to log.
func parsePlayerLine(s string) (playerLine, string) {
	// Pre-process s to handle attached parentheses such as "5374343843(6732)" -> "5374343843 (6732)"
	s = strings.ReplaceAll(s, "(", " (")
	s = strings.ReplaceAll(s, ")", ") ")

	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })

	var line playerLine
	var tokens []string
	for _, f := range fields {
		if strings.HasPrefix(f, "@") {
			if line.Contact != "" {
				return playerLine{}, "Контакт должен быть один"
			}
			line.Contact = f
			continue
		}
		if knownLabels[strings.ToLower(f)] {
			continue
		}
		tokens = append(tokens, f)
	}

	// Nick: everything up to the first numeric token.
	firstNum := -1
	for i, tok := range tokens {
		if isDigits(strings.Trim(tok, "()")) {
			firstNum = i
			break
		}
	}
	if firstNum <= 0 {
		return playerLine{}, "Не вижу ник — он должен идти первым"
	}
	line.Nick = strings.Join(tokens[:firstNum], " ")
	if len([]rune(line.Nick)) > maxTeamNameLen {
		return playerLine{}, fmt.Sprintf("Ник длиннее %d символов", maxTeamNameLen)
	}

	rest := tokens[firstNum:]
	switch len(rest) {
	case 1:
		return playerLine{}, "Не вижу Zone ID и звёзды"
	case 2:
		return playerLine{}, "Не вижу звёзды"
	}
	if len(rest) > 3 {
		return playerLine{}, fmt.Sprintf("Не понял '%s'", strings.Join(rest[3:], " "))
	}

	line.GameID = rest[0]
	line.ZoneID = strings.Trim(rest[1], "()")
	if !isDigits(line.ZoneID) {
		return playerLine{}, "Zone ID — число"
	}
	if !isDigits(rest[2]) {
		return playerLine{}, "Звёзды — число"
	}
	stars, _ := parseStars(rest[2])
	line.Stars = stars
	return line, ""
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// plausibilityWarnings flags ids that do not look like MLBB ids: game ids are
// 8–10 digits and zone ids 4–5. The line is still accepted — a new region
// could prove the rule wrong — but the captain is told before picking a role.
func plausibilityWarnings(line playerLine) []string {
	var out []string
	if n := len(line.GameID); n < 8 || n > 10 {
		out = append(out, fmt.Sprintf("Внимание: GameID %s: обычно 8–10 цифр", line.GameID))
	}
	if n := len(line.ZoneID); n < 4 || n > 5 {
		out = append(out, fmt.Sprintf("Внимание: Zone ID %s: обычно 4–5 цифр", line.ZoneID))
	}
	return out
}
