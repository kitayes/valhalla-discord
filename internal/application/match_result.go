package application

import (
	"context"
	"errors"
	"fmt"

	"blackwatch/internal/models"
)

// recordResult writes a decided match. With Challonge configured the result
// goes there once — a captain's result through ReportResult, a referee's
// decision (override) through SetWinner, which reopens a finished match — and
// the sync rewrites the cache. Without Challonge the cache is moved locally.
//
// It replaces propagateBracketResult on the Challonge path: that one advanced
// the winner as in single elimination, wrote to Challonge, and the caller then
// wrote the same result again; its messages told an upper-bracket loser that
// the team was out and a qualifier's decider winner that it had won the final.
func (s *TelegramServiceImpl) recordResult(ctx context.Context, matchID, winnerTeamID, winScore, loseScore int, override bool) error {
	if s.bracket == nil {
		return s.propagateBracketResult(ctx, matchID, winnerTeamID, winScore, loseScore)
	}
	before, err := s.repo.GetBracketMatches(ctx)
	if err != nil {
		return err
	}
	var m *models.BracketMatch
	for i := range before {
		if before[i].ID == matchID || before[i].PlayOrder == matchID {
			m = &before[i]
			break
		}
	}
	if m == nil {
		return errors.New("матч не найден в сетке")
	}
	if !m.Has(winnerTeamID) {
		return errors.New("команда не участвует в этом матче")
	}
	loser := m.Opponent(winnerTeamID)
	if loser == nil {
		return errors.New("у матча ещё не определены обе команды")
	}
	var change *BracketChange
	if override {
		winner, err := s.repo.GetTeamByID(ctx, winnerTeamID)
		if err != nil || winner == nil {
			return errors.New("команда-победитель не найдена")
		}
		if change, err = s.bracket.SetWinner(ctx, m.PlayOrder, winner.Name, winScore, loseScore); err != nil {
			return err
		}
	} else {
		ready, err := s.bracket.ReportResult(ctx, m.ID, winnerTeamID, winScore, loseScore)
		if err != nil {
			return err
		}
		change = &BracketChange{Ready: ready}
	}
	s.notifyResult(ctx, *m, winnerTeamID, *loser, winScore, loseScore)
	s.announceChange(ctx, change)
	return nil
}

// SetBracketChangeNotifier installs the announcer of new pairs and reset
// matches; the bot passes its own, so a result recorded here reads the same
// as one set with /set_winner.
func (s *TelegramServiceImpl) SetBracketChangeNotifier(fn func(ctx context.Context, ch *BracketChange)) {
	s.hookMu.Lock()
	s.changeHook = fn
	s.hookMu.Unlock()
}

// announceChange hands a bracket change to the bot. The sync that produced it
// marked its new pairs announced, so a change dropped here is never told.
func (s *TelegramServiceImpl) announceChange(ctx context.Context, ch *BracketChange) {
	if ch == nil || (len(ch.Ready) == 0 && len(ch.Reset) == 0) {
		return
	}
	s.hookMu.RLock()
	fn := s.changeHook
	s.hookMu.RUnlock()
	if fn != nil {
		fn(ctx, ch)
	}
}

// notifyResult tells both teams what a result means for them. The meaning
// comes from the bracket layout: after a report the sync refreshes only open
// matches, so a next match still waiting for its other side is not in the
// cache yet and cannot tell whether the winner goes on.
func (s *TelegramServiceImpl) notifyResult(ctx context.Context, m models.BracketMatch, winnerID, loserID, winScore, loseScore int) {
	if s.notifyMatchFunc == nil {
		return
	}
	after, _ := s.repo.GetBracketMatches(ctx)
	t, _ := s.repo.GetActiveTournament(ctx)
	// Only a grand-final reset is told by the cache: it opens with both teams.
	stillIn := func(team int) bool {
		for _, x := range after {
			if x.State != models.BracketComplete && x.Has(team) {
				return true
			}
		}
		return false
	}
	name := func(id int) string {
		if team, _ := s.repo.GetTeamByID(ctx, id); team != nil {
			return team.Name
		}
		return fmt.Sprintf("Команда #%d", id)
	}
	winnerName, loserName := name(winnerID), name(loserID)
	l := NewLayout(t, after)
	qualifier := t.Qualifier()
	decider := qualifier && l.Cutoff(t.AdvanceCount).Decider(m.Round)
	grandFinal := l.Double && m.Round > l.UBRounds
	final := !qualifier && ((!l.Double && m.Round == l.UBRounds) || (grandFinal && !stillIn(winnerID)))
	head := fmt.Sprintf("Матч #%d (%s) завершён: %s %d:%d %s.", m.PlayOrder, RoundTitle(t, after, m.Round), winnerName, winScore, loseScore, loserName)

	var winnerText string
	switch {
	case m.Round == 0:
		winnerText = head + fmt.Sprintf("\n\nКоманда «%s» занимает 3-е место!", winnerName)
	case decider:
		winnerText = head + fmt.Sprintf("\n\nПобеда! Команда «%s» проходит в основной этап. Итог квалификации объявим, когда сыграют все матчи.", winnerName)
	case final:
		winnerText = head + fmt.Sprintf("\n\nПобеда в финале! Команда «%s» выигрывает турнир.", winnerName)
	default:
		winnerText = head + "\n\nПобеда! Следующий матч придёт отдельной карточкой, как только определится соперник."
	}

	where := "турнира"
	if qualifier {
		where = "квалификации"
	}
	out := head + fmt.Sprintf("\n\nКоманда «%s» выбывает из %s. Спасибо за игру!", loserName, where)
	var loserText string
	switch {
	case m.Round == 0:
		loserText = head + "\n\nКоманда занимает 4-е место. Спасибо за игру!"
	case l.Double && m.Round > 0 && m.Round <= l.UBRounds:
		loserText = head + "\n\nПоражение, но вы не выбываете: команда переходит в нижнюю сетку. Следующий матч придёт отдельной карточкой."
	case grandFinal && stillIn(loserID):
		loserText = head + "\n\nГранд-финал продолжается: будет решающий матч."
	case !l.Double && !qualifier && t != nil && t.HoldThirdPlace && m.Round == l.UBRounds-1:
		loserText = head + "\n\nВпереди матч за 3-е место — карточка придёт отдельно."
	default:
		loserText = out
	}

	s.notifyTeamMembers(ctx, winnerID, winnerText)
	s.notifyTeamMembers(ctx, loserID, loserText)
}

// notifyTeamMembers sends a message to every member of a team with Telegram.
func (s *TelegramServiceImpl) notifyTeamMembers(ctx context.Context, teamID int, text string) {
	if s.notifyMatchFunc == nil {
		return
	}
	members, _ := s.repo.GetTeamMembers(ctx, teamID)
	seen := map[int64]bool{}
	for _, p := range members {
		if p.TelegramID != nil && *p.TelegramID > 0 && !seen[*p.TelegramID] {
			seen[*p.TelegramID] = true
			s.notifyMatchFunc(ctx, *p.TelegramID, text, true)
		}
	}
}
