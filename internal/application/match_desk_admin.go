package application

import (
	"blackwatch/internal/models"
	"context"
	"errors"
	"fmt"
)

func (s *MatchDeskService) AdminNumberAction(ctx context.Context, actor int64, number int, action string) error {
	if !s.admins[actor] {
		return errors.New("действие доступно только администратору")
	}
	if action != "pause" && action != "resume" {
		return errors.New("неизвестное действие администратора")
	}
	return s.update(ctx, func(d *models.MatchDesk, _ models.DeskContext) error {
		for _, m := range d.Matches {
			if m.Number == number && m.Active {
				setDeskPause(m, action == "pause", d.GlobalPaused, s.now())
				return nil
			}
		}
		return ErrDeskStale
	})
}

func (s *MatchDeskService) History(ctx context.Context, actor int64, number int) ([]string, error) {
	var out []string
	err := s.update(ctx, func(d *models.MatchDesk, c models.DeskContext) error {
		out = nil
		for _, m := range d.Matches {
			if m.Number == number {
				allowed := s.admins[actor]
				for _, team := range m.Teams {
					p := c.Captains[team]
					if p.TelegramID != nil && *p.TelegramID == actor {
						allowed = true
					}
				}
				if !allowed {
					return errors.New("доступно только капитанам матча и администраторам")
				}
				out = append(out, fmt.Sprintf("История матча #%d: %s vs %s", m.Number, m.Names[0], m.Names[1]))
				for _, e := range m.History {
					out = append(out, e.At.In(s.location).Format("02.01 15:04:05")+" — "+e.Text)
				}
				return nil
			}
		}
		return ErrDeskStale
	})
	return out, err
}
