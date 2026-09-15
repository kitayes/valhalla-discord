package application

import (
	"blackwatch/internal/models"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrDeskStale = errors.New("эта карточка устарела; откройте /match")

func syncDesk(d *models.MatchDesk, matches []models.BracketMatch, tournament, now time.Time) {
	if d.Matches == nil {
		d.Matches = make(map[int]*models.DeskMatch)
	}
	seen := make(map[int]bool)
	for _, b := range matches {
		seen[b.ID] = true
		m := d.Matches[b.ID]
		if !b.Ready() {
			if m != nil && m.Active {
				m.Active = false
				deskEvent(m, now, "Матч закрыт или ожидает определения пары")
			}
			continue
		}
		pair := [2]int{*b.Team1ID, *b.Team2ID}
		if m == nil || !m.Active || m.Teams != pair {
			var history []models.DeskEvent
			if m != nil {
				history = m.History
			}
			d.NextGeneration++
			start := now
			if tournament.After(start) {
				start = tournament
			}
			m = &models.DeskMatch{ID: b.ID, Number: b.PlayOrder, Generation: d.NextGeneration, Teams: pair, Names: [2]string{b.Team1Name, b.Team2Name}, Active: true, NotBefore: start, Deadline: start.Add(10 * time.Minute), History: history}
			d.Matches[b.ID] = m
			deskEvent(m, now, "Объявлена пара: "+b.Team1Name+" — "+b.Team2Name)
			if d.GlobalPaused {
				setDeskPause(m, false, true, now)
			}
		}
		m.Number = b.PlayOrder
		m.Names = [2]string{b.Team1Name, b.Team2Name}
	}
	for id, m := range d.Matches {
		if !seen[id] && m.Active {
			m.Active = false
			deskEvent(m, now, "Матч удалён из сетки")
		}
	}
	// Old cards must not be delivered after a changed pair or a finished match.
	pending := d.Outbox[:0]
	for _, n := range d.Outbox {
		m := d.Matches[n.MatchID]
		if m == nil || (!m.Active && n.Kind!="alert") || m.Generation != n.Generation {
			continue
		}
		if n.Kind == "card" && n.Revision != m.Revision {
			continue
		}
		pending = append(pending, n)
	}
	d.Outbox = pending
}

func deskEvent(m *models.DeskMatch, now time.Time, text string) {
	m.Revision++
	m.History = append(m.History, models.DeskEvent{At: now, Text: text})
}

func deskCaptainAction(m *models.DeskMatch, teamID int, action string, now time.Time) error {
	side := -1
	for i, id := range m.Teams {
		if id == teamID {
			side = i
		}
	}
	if side < 0 {
		return errors.New("только капитан команды этого матча может выполнить действие")
	}
	if !m.Active {
		return ErrDeskStale
	}
	switch action {
	case "ready":
		if !m.PausedAt.IsZero() {
			return errors.New("матч на паузе; дождитесь продолжения")
		}
		if m.Ready[side] {
			return nil
		}
		m.Ready[side] = true
		deskEvent(m, now, m.Names[side]+": готовы")
		if m.Ready[0] && m.Ready[1] {
			m.StartedAt = now
			if m.NotBefore.After(now) {
				m.StartedAt = m.NotBefore
			}
		}
	case "judge_noanswer", "judge_lobby", "judge_score":
		reasons := map[string]string{"judge_noanswer": "Соперник не отвечает", "judge_lobby": "Проблема с лобби", "judge_score": "Спор по результату"}
		if m.Issues[side] != "" {
			return nil
		}
		m.Issues[side] = reasons[action]
		deskEvent(m, now, m.Names[side]+": вызван судья — "+m.Issues[side])
	default:
		return errors.New("неизвестное действие")
	}
	return nil
}

func setDeskPause(m *models.DeskMatch, local, global bool, now time.Time) {
	if m.LocalPaused == local && m.GlobalPaused == global {
		return
	}
	wasPaused := !m.PausedAt.IsZero()
	m.LocalPaused, m.GlobalPaused = local, global
	if local || global {
		if !wasPaused {
			m.PausedAt = now
			deskEvent(m, now, "Таймеры матча приостановлены")
		}
	} else if wasPaused {
		// A pause before tournament start only shifts the portion overlapping
		// the timer; otherwise a pre-start pause would grant unearned extra time.
		from := m.PausedAt
		if m.NotBefore.After(from) {
			from = m.NotBefore
		}
		if now.After(from) {
			shift := now.Sub(from)
			m.Deadline = m.Deadline.Add(shift)
			if !m.StartedAt.IsZero() {
				m.StartedAt = m.StartedAt.Add(shift)
			}
		}
		m.PausedAt = time.Time{}
		deskEvent(m, now, "Таймеры матча продолжены")
	}
}

func deskProblems(m *models.DeskMatch, now time.Time) []string {
	var out []string
	for i, issue := range m.Issues {
		if issue != "" {
			out = append(out, m.Names[i]+": "+issue)
		}
	}
	if !m.Active || !m.PausedAt.IsZero() {
		return out
	}
	if !now.Before(m.Deadline) && (!m.Ready[0] || !m.Ready[1]) {
		for i, r := range m.Ready {
			if !r {
				out = append(out, m.Names[i]+": не подтвердили готовность")
			}
		}
	}
	return out
}

func deskStatus(m *models.DeskMatch) string {
	if !m.Active {
		return "Матч закрыт"
	}
	if !m.PausedAt.IsZero() {
		return "На паузе"
	}
	if m.Ready[0] && m.Ready[1] {
		return "Обе команды готовы"
	}
	return "Ожидание начала"
}

func DeskCallback(m *models.DeskMatch, action string) string {
	return fmt.Sprintf("desk:%s:%d:%d", action, m.ID, m.Generation)
}

func ParseDeskCallback(data string) (string, int, int64, error) {
	p := strings.Split(data, ":")
	if len(p) != 4 || p[0] != "desk" {
		return "", 0, 0, ErrDeskStale
	}
	id, e1 := strconv.Atoi(p[2])
	generation, e2 := strconv.ParseInt(p[3], 10, 64)
	if e1 != nil || e2 != nil || id <= 0 || generation <= 0 {
		return "", 0, 0, ErrDeskStale
	}
	return p[1], id, generation, nil
}
