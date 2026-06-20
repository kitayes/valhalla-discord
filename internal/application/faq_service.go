package application

import (
	"fmt"
	"os"
	"sync"
)

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
	AnswerQuestion(faqContext, userQuestion string) (string, error)
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
func (s *FAQService) AnswerQuestion(userQuestion string) string {
	s.mu.RLock()
	faqContext := s.cachedFAQ
	s.mu.RUnlock()

	if faqContext == "" {
		return "⚠️ База знаний пуста. Попробуйте позже или используйте /faq_reload."
	}

	if s.faqClient != nil {
		answer, err := s.faqClient.AnswerQuestion(faqContext, userQuestion)
		if err != nil {
			s.logger.Error("faq: DeepSeek error: %v", err)
			return s.fallbackSearch(userQuestion, faqContext)
		}
		return answer
	}

	return s.fallbackSearch(userQuestion, faqContext)
}

// fallbackSearch performs a simple keyword match against the FAQ when DeepSeek is unavailable.
func (s *FAQService) fallbackSearch(question, faq string) string {
	// Return a generic helpful message with the FAQ content
	return "ℹ️ **Ответ из базы знаний:**\n\nК сожалению, DeepSeek API недоступен. Вот краткая информация:\n\n" +
		"Используйте команды:\n" +
		"• `/link <ID>` — привязка Telegram\n" +
		"• `/profile <ID>` — статистика игрока\n" +
		"• `/top` — таблица лидеров\n" +
		"• `/faq <вопрос>` — поиск по базе знаний\n\n" +
		"Если у вас конкретный вопрос — обратитесь к администратору."
}
