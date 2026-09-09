package application

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// minKeywordLength drops words too short to discriminate between FAQ sections.
const minKeywordLength = 4

// FAQService manages the FAQ knowledge base and answers user questions via DeepSeek/Discord-answers.
type FAQService struct {
	logger    Logger
	faqClient FAQClient
	faqPath   string
	cachedFAQ string
	mu        sync.RWMutex
}

// FAQClient is the interface for answering questions (DeepSeek API or fallback).
type FAQClient interface {
	AnswerQuestion(ctx context.Context, faqContext, userQuestion string) (string, error)
}

// NewFAQService creates a new FAQ service.
func NewFAQService(logger Logger, faqClient FAQClient, faqPath string) (*FAQService, error) {
	svc := &FAQService{
		logger:    logger,
		faqClient: faqClient,
		faqPath:   faqPath,
	}
	if err := svc.ReloadFAQ(); err != nil {
		return nil, fmt.Errorf("faq: failed to load knowledge base: %w", err)
	}
	return svc, nil
}

// ReloadFAQ reloads the FAQ file from disk into the in-memory cache.
func (s *FAQService) ReloadFAQ() error {
	data, err := os.ReadFile(s.faqPath)
	if err != nil {
		return fmt.Errorf("faq: cannot read %s: %w", s.faqPath, err)
	}
	s.mu.Lock()
	s.cachedFAQ = string(data)
	s.mu.Unlock()
	s.logger.Info("faq: knowledge base reloaded (%d bytes)", len(data))
	return nil
}

// AnswerQuestion looks up the FAQ and asks DeepSeek (or in-memory search) for an answer.
func (s *FAQService) AnswerQuestion(ctx context.Context, userQuestion string) string {
	s.mu.RLock()
	faqContext := s.cachedFAQ
	s.mu.RUnlock()

	if faqContext == "" {
		return "⚠️ База знаний пуста. Попробуйте позже или используйте /faq_reload."
	}

	if s.faqClient != nil {
		answer, err := s.faqClient.AnswerQuestion(ctx, faqContext, userQuestion)
		if err != nil {
			s.logger.Error("faq: DeepSeek error: %v", err)
			return s.fallbackSearch(userQuestion, faqContext)
		}
		return answer
	}

	return s.fallbackSearch(userQuestion, faqContext)
}

// fallbackSearch performs a keyword match against the FAQ when DeepSeek is
// unavailable. It used to ignore both arguments and return a fixed blurb, so an
// outage meant nobody could get an answer even when the knowledge base held one.
//
// The FAQ is treated as markdown sections separated by headings; the section
// sharing the most keywords with the question wins.
func (s *FAQService) fallbackSearch(question, faq string) string {
	best, score := bestFAQSection(question, faq)
	if score == 0 {
		return "⚠️ Не нашёл ответа в базе знаний, а ИИ-ассистент сейчас недоступен.\n\n" +
			"Попробуйте переформулировать вопрос или обратитесь к администратору."
	}

	return "ℹ️ **Из базы знаний** (ИИ-ассистент недоступен, показываю ближайший раздел):\n\n" + best
}

// bestFAQSection returns the FAQ section matching the most question keywords,
// along with the number of keywords it matched.
func bestFAQSection(question, faq string) (string, int) {
	keywords := extractKeywords(question)
	if len(keywords) == 0 {
		return "", 0
	}

	var best string
	bestScore := 0
	for _, section := range splitFAQSections(faq) {
		lowered := strings.ToLower(section)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(lowered, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore, best = score, section
		}
	}

	if bestScore == 0 {
		return "", 0
	}
	return strings.TrimSpace(best), bestScore
}

// splitFAQSections breaks the knowledge base at markdown headings, falling back
// to blank-line paragraphs when it has no headings.
func splitFAQSections(faq string) []string {
	var sections []string
	var current strings.Builder

	hasHeading := false
	for _, line := range strings.Split(faq, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			hasHeading = true
			if strings.TrimSpace(current.String()) != "" {
				sections = append(sections, current.String())
			}
			current.Reset()
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	if strings.TrimSpace(current.String()) != "" {
		sections = append(sections, current.String())
	}

	if !hasHeading {
		return strings.Split(faq, "\n\n")
	}
	return sections
}

// extractKeywords lowercases the question and drops punctuation and words too
// short to carry meaning.
func extractKeywords(question string) []string {
	fields := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var keywords []string
	for _, f := range fields {
		if utf8.RuneCountInString(f) >= minKeywordLength {
			keywords = append(keywords, f)
		}
	}
	return keywords
}
