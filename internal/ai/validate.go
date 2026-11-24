package ai

import (
	"fmt"
	"strings"

	"blackwatch/internal/models"
)

const (
	maxReasonableStat          = 60
	maxTowerExecutionAllowance = 4
	minConfidenceThreshold     = 0.75
)

// ValidateScoreboard runs deterministic MLBB scoreboard sanity checks and
// returns human-readable warnings in Russian for anything suspicious.
func ValidateScoreboard(m *models.Match) []string {
	if m == nil {
		return []string{"пустой результат распознавания"}
	}

	var warnings []string
	seen := make(map[string]struct{})
	addWarning := func(w string) {
		if w == "" {
			return
		}
		if _, ok := seen[w]; ok {
			return
		}
		seen[w] = struct{}{}
		warnings = append(warnings, w)
	}

	if len(m.Players) != 10 {
		addWarning(fmt.Sprintf("распознано %d строк игроков вместо 10", len(m.Players)))
	}

	var winPlayers, losePlayers []models.PlayerResult
	var winKills, winDeaths, loseKills, loseDeaths int

	for idx, p := range m.Players {
		label := strings.TrimSpace(p.PlayerName)
		if label == "" {
			label = fmt.Sprintf("строка #%d", idx+1)
		}

		if p.Kills < 0 || p.Kills > maxReasonableStat ||
			p.Deaths < 0 || p.Deaths > maxReasonableStat ||
			p.Assists < 0 || p.Assists > maxReasonableStat {
			addWarning(fmt.Sprintf("подозрительный K/D/A у %s: %d/%d/%d", label, p.Kills, p.Deaths, p.Assists))
		}
		if p.Confidence > 0 && p.Confidence < minConfidenceThreshold {
			addWarning(fmt.Sprintf("низкая уверенность OCR у %s (%.0f%%)", label, p.Confidence*100))
		}

		switch strings.ToUpper(strings.TrimSpace(p.Result)) {
		case "WIN":
			winPlayers = append(winPlayers, p)
			winKills += max(p.Kills, 0)
			winDeaths += max(p.Deaths, 0)
		case "LOSE":
			losePlayers = append(losePlayers, p)
			loseKills += max(p.Kills, 0)
			loseDeaths += max(p.Deaths, 0)
		default:
			addWarning(fmt.Sprintf("неизвестный результат стороны %q у %s", p.Result, label))
		}
	}

	if len(m.Players) == 10 && (len(winPlayers) != 5 || len(losePlayers) != 5) {
		addWarning(fmt.Sprintf("некорректное число игроков по сторонам: WIN=%d, LOSE=%d", len(winPlayers), len(losePlayers)))
	}

	if m.HeaderWinKills != nil && *m.HeaderWinKills >= 0 && winKills != *m.HeaderWinKills {
		addWarning(fmt.Sprintf("сумма киллов победителей (%d) не сходится со счётом в шапке (%d)", winKills, *m.HeaderWinKills))
	}
	if m.HeaderLoseKills != nil && *m.HeaderLoseKills >= 0 && loseKills != *m.HeaderLoseKills {
		addWarning(fmt.Sprintf("сумма киллов проигравших (%d) не сходится со счётом в шапке (%d)", loseKills, *m.HeaderLoseKills))
	}

	if winKills > loseDeaths {
		addWarning(fmt.Sprintf("киллы победителей (%d) превышают смерти проигравших (%d)", winKills, loseDeaths))
	} else if loseDeaths-winKills > maxTowerExecutionAllowance {
		addWarning(fmt.Sprintf("смерти проигравших (%d) сильно превышают киллы победителей (%d)", loseDeaths, winKills))
	}

	if loseKills > winDeaths {
		addWarning(fmt.Sprintf("киллы проигравших (%d) превышают смерти победителей (%d)", loseKills, winDeaths))
	} else if winDeaths-loseKills > maxTowerExecutionAllowance {
		addWarning(fmt.Sprintf("смерти победителей (%d) сильно превышают киллы проигравших (%d)", winDeaths, loseKills))
	}

	checkAssists := func(sidePlayers []models.PlayerResult, teamKills int) {
		if teamKills <= 0 {
			return
		}
		for idx, p := range sidePlayers {
			if p.Kills < 0 || p.Assists < 0 {
				continue
			}
			maxPossible := max(teamKills-p.Kills, 0)
			if p.Assists > maxPossible {
				label := strings.TrimSpace(p.PlayerName)
				if label == "" {
					label = fmt.Sprintf("игрок #%d", idx+1)
				}
				addWarning(fmt.Sprintf("у игрока %s ассистов (%d) больше возможных (%d)", label, p.Assists, maxPossible))
			}
		}
	}
	checkAssists(winPlayers, winKills)
	checkAssists(losePlayers, loseKills)

	if strings.TrimSpace(m.MVP) == "" {
		addWarning("не определена медаль MVP")
	}
	if strings.TrimSpace(m.SVP) == "" {
		addWarning("не определена медаль SVPG")
	}

	return warnings
}
