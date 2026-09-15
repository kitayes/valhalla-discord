package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MatchDeskStore interface {
	// A failed callback must roll back all state changes. Implementations lock
	// the desk and pairings for the whole callback; no network I/O belongs here.
	UpdateMatchDesk(context.Context, func(*models.MatchDesk, models.DeskContext) error) error
}

type MatchDeskService struct {
	store    MatchDeskStore
	admins   map[int64]bool
	location *time.Location
	now      func() time.Time
}

func NewMatchDeskService(store MatchDeskStore, admins []int64, location *time.Location) *MatchDeskService {
	if location == nil {
		location = time.UTC
	}
	s := &MatchDeskService{store: store, admins: map[int64]bool{}, location: location, now: time.Now}
	for _, id := range admins {
		s.admins[id] = true
	}
	return s
}

func (s *MatchDeskService) update(ctx context.Context, action func(*models.MatchDesk, models.DeskContext) error) error {
	return s.store.UpdateMatchDesk(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		now := s.now()
		for _, b := range c.Matches {
			if m:=d.Matches[b.ID]; m!=nil && b.Ready() && m.BracketRevision != c.Revisions[b.ID] {
				m.Active = false
			}
		}
		syncDesk(d, c.Matches, c.StartsAt, now)
		for id, m := range d.Matches {
			m.BracketRevision = c.Revisions[id]
		}
		if action != nil {
			if err := action(d, c); err != nil {
				return err
			}
		}
		s.queueNotices(d, c, now)
		return nil
	})
}

func (s *MatchDeskService) Tick(ctx context.Context) error { return s.update(ctx, nil) }

func (s *MatchDeskService) CaptainAction(ctx context.Context, actor int64, id int, generation int64, action string) error {
	return s.update(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		m := d.Matches[id]
		if m == nil || !m.Active || m.Generation != generation {
			return ErrDeskStale
		}
		for team, p := range c.Captains {
			if p.IsCaptain && p.TelegramID != nil && *p.TelegramID == actor {
				return deskCaptainAction(m, team, action, s.now())
			}
		}
		return errors.New("действие доступно только капитану команды")
	})
}

func (s *MatchDeskService) AdminAction(ctx context.Context, actor int64, id int, generation int64, action string) error {
	if !s.admins[actor] {
		return errors.New("действие доступно только администратору")
	}
	return s.update(ctx, func(d *models.MatchDesk, _ models.DeskContext) error {
		now := s.now()
		if id == 0 && (action == "pause" || action == "resume") {
			d.GlobalPaused = action == "pause"
			for _, m := range d.Matches {
				if m.Active {
					setDeskPause(m, m.LocalPaused, d.GlobalPaused, now)
				}
			}
			return nil
		}
		m := d.Matches[id]
		if m == nil || (!m.Active && action!="resolve") || m.Generation != generation {
			return ErrDeskStale
		}
		switch action {
		case "pause":
			setDeskPause(m, true, d.GlobalPaused, now)
		case "resume":
			setDeskPause(m, false, d.GlobalPaused, now)
		case "resolve":
			if m.Issues != [2]string{} {
				m.Issues = [2]string{}
				deskEvent(m, now, fmt.Sprintf("Администратор %d закрыл обращения", actor))
			}
		default:
			return errors.New("неизвестное действие администратора")
		}
		return nil
	})
}

func (s *MatchDeskService) Pending(ctx context.Context) ([]models.DeskNotice, error) {
	var out []models.DeskNotice
	err := s.update(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		s.queueNotices(d, c, s.now())
		out = append(out, d.Outbox...)
		return nil
	})
	return out, err
}

func (s *MatchDeskService) Acknowledge(ctx context.Context, notice models.DeskNotice) error {
	return s.update(ctx, func(d *models.MatchDesk, _ models.DeskContext) error {
		if notice.Tournament != d.Tournament {
			return nil
		}
		for i, n := range d.Outbox {
			if n.ID == notice.ID && n.MatchID == notice.MatchID && n.Generation == notice.Generation {
				d.Outbox = append(d.Outbox[:i], d.Outbox[i+1:]...)
				break
			}
		}
		return nil
	})
}

// NoticeCurrent drops messages superseded while a large delivery batch was
// in flight. Never hold a database transaction open while calling Telegram.
func (s *MatchDeskService) NoticeCurrent(ctx context.Context, notice models.DeskNotice) (bool, error) {
	current := false
	err := s.update(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		s.queueNotices(d, c, s.now())
		if notice.Tournament != d.Tournament {
			return nil
		}
		for _, n := range d.Outbox {
			if n.ID == notice.ID && n.MatchID == notice.MatchID && n.Generation == notice.Generation {
				current = true
				break
			}
		}
		return nil
	})
	return current, err
}

func (s *MatchDeskService) Cards(ctx context.Context, actor int64) ([]models.DeskNotice, error) {
	var out []models.DeskNotice
	err := s.update(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		for _, m := range sortedDeskMatches(d) {
			if m.Active {
				for side, team := range m.Teams {
					p := c.Captains[team]
					if p.TelegramID != nil && *p.TelegramID == actor {
						out = append(out, s.card(m, side, c))
					}
				}
			}
		}
		return nil
	})
	return out, err
}

func (s *MatchDeskService) Attention(ctx context.Context, actor int64) ([]models.DeskNotice, error) {
	if !s.admins[actor] {
		return nil, errors.New("доступно только администратору")
	}
	var out []models.DeskNotice
	err := s.update(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		for _, m := range sortedDeskMatches(d) {
			problems := deskAllProblems(m, c, s.now())
			if m.Active && !m.PausedAt.IsZero() {
				problems = append(problems, "Таймеры на паузе")
			}
			if len(problems) > 0 {
				out = append(out, s.attentionCard(m, c, problems, actor))
			}
		}
		return nil
	})
	return out, err
}

func sortedDeskMatches(d *models.MatchDesk) []*models.DeskMatch {
	out := make([]*models.DeskMatch, 0, len(d.Matches))
	for _, m := range d.Matches {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Number < out[j].Number || out[i].Number == out[j].Number && out[i].ID < out[j].ID
	})
	return out
}

func deskCaptainLabel(p models.TelegramPlayer) string {
	if p.TelegramUsername != "" {
		return "@" + strings.TrimPrefix(p.TelegramUsername, "@")
	}
	if p.TelegramID != nil {
		return fmt.Sprintf("%s (Telegram ID: %d)", p.GameNickname, *p.TelegramID)
	}
	return "Контакт капитана недоступен"
}

func (s *MatchDeskService) card(m *models.DeskMatch, side int, c models.DeskContext) models.DeskNotice {
	opponent := c.Captains[m.Teams[1-side]]
	gameID := opponent.GameID
	if opponent.ZoneID != "" {
		gameID += " (" + opponent.ZoneID + ")"
	}
	if gameID == "" {
		gameID = "Не указан"
	}
	text := fmt.Sprintf("Матч #%d: %s vs %s\nКапитан соперника: %s\nGame ID капитана соперника: %s\nСтатус: %s", m.Number, m.Names[0], m.Names[1], deskCaptainLabel(opponent), gameID, deskStatus(m))
	if !m.Ready[0] || !m.Ready[1] {
		if m.PausedAt.IsZero() {
			text += "\nГотовность до: " + m.Deadline.In(s.location).Format("02.01 15:04 MST")
		} else {
			text += "\nПосле продолжения получите новый дедлайн."
		}
	}
	for i, r := range m.Ready {
		label := "ожидаем"
		if r {
			label = "готовы"
		}
		text += fmt.Sprintf("\n%s: %s", m.Names[i], label)
	}
	if m.Issues[side] != "" {
		text += "\nВаше обращение судье: " + m.Issues[side]
	}
	buttons := [][]models.DeskButton{}
	if opponent.GameID != "" {
		buttons = append(buttons, []models.DeskButton{{Text: "Скопировать ID", CopyText: &models.DeskCopyText{Text: gameID}}})
	}
	if !m.Ready[side] && m.PausedAt.IsZero() {
		buttons = append(buttons, []models.DeskButton{{Text: "Мы в лобби / Готовы", Data: DeskCallback(m, "ready")}})
	}
	buttons = append(buttons, []models.DeskButton{{Text: "Вызвать судью", Data: DeskCallback(m, "judge")}})
	chatID := int64(0)
	if p := c.Captains[m.Teams[side]]; p.TelegramID != nil {
		chatID = *p.TelegramID
	}
	return models.DeskNotice{MatchID: m.ID, Generation: m.Generation, Revision: m.Revision, ChatID: chatID, Kind: "card", Text: text, Buttons: buttons}
}

func (s *MatchDeskService) attentionCard(m *models.DeskMatch, c models.DeskContext, problems []string, actor int64) models.DeskNotice {
	text := fmt.Sprintf("Матч #%d: %s vs %s\n%s\n\n%s: %s\n%s: %s", m.Number, m.Names[0], m.Names[1], strings.Join(problems, "\n"), m.Names[0], deskCaptainLabel(c.Captains[m.Teams[0]]), m.Names[1], deskCaptainLabel(c.Captains[m.Teams[1]]))
	pauseAction, pauseText := "pause", "Пауза матча"
	if m.LocalPaused {
		pauseAction, pauseText = "resume", "Продолжить матч"
	}
	if m.GlobalPaused {
		text += "\nОбщая пауза: /resume_matches"
	}
	buttons := [][]models.DeskButton{}
	if m.Active {buttons=append(buttons,[]models.DeskButton{{Text:pauseText,Data:DeskCallback(m,pauseAction)}})}
	if m.Issues != [2]string{} {
		buttons = append(buttons, []models.DeskButton{{Text: "Обращения решены", Data: DeskCallback(m, "resolve")}})
	}
	return models.DeskNotice{MatchID: m.ID, Generation: m.Generation, Revision: m.Revision, Kind: "alert", ChatID: actor, Text: text, Buttons: buttons}
}

func (s *MatchDeskService) queueNotices(d *models.MatchDesk, c models.DeskContext, now time.Time) {
	enqueue := func(n models.DeskNotice) {
		d.NextNotice++
		n.ID = d.NextNotice
		n.Tournament = d.Tournament
		d.Outbox = append(d.Outbox, n)
	}
	for _, m := range sortedDeskMatches(d) {
		var keys [2]string
		for side, team := range m.Teams {
			p := c.Captains[team]
			if p.TelegramID != nil {
				keys[side] = fmt.Sprintf("%d:%s:%s:%s", *p.TelegramID, p.TelegramUsername, p.GameID, p.ZoneID)
			}
		}
		if m.CaptainKeys != keys {
			m.CaptainKeys = keys
			m.Revision++
		}
		for side, team := range m.Teams {
			if p := c.Captains[team]; m.Active && p.TelegramID != nil && m.CardRevision[side] != m.Revision {
				enqueue(s.card(m, side, c))
				m.CardRevision[side] = m.Revision
			}
		}
		problems := deskAllProblems(m, c, now)
		key := strings.Join(problems, "\n")
		if key != m.AlertKey {
			m.AlertKey = key
			if key != "" {
				for id := range s.admins {
					n := s.attentionCard(m, c, problems, id)
					n.AlertKey = key
					enqueue(n)
				}
			}
		}
	}
	pending := d.Outbox[:0]
	for _, n := range d.Outbox {
		m := d.Matches[n.MatchID]
		if m == nil || (!m.Active && n.Kind!="alert") || m.Generation != n.Generation {
			continue
		}
		if n.Kind == "card" && n.Revision != m.Revision {
			continue
		}
		if n.Kind == "alert" && n.AlertKey != m.AlertKey {
			continue
		}
		pending = append(pending, n)
	}
	d.Outbox = pending
}

func deskAllProblems(m *models.DeskMatch, c models.DeskContext, now time.Time) []string {
	problems := deskProblems(m, now)
	if !m.Active {
		if len(problems)>0{problems=append(problems,"Матч закрыт; обращение требует решения")}
		return problems
	}
	for side, team := range m.Teams {
		if c.Captains[team].TelegramID == nil {
			problems = append(problems, m.Names[side]+": нет контакта капитана")
		}
	}
	return problems
}
