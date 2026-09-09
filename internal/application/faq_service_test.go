package application

import (
	"strings"
	"testing"
)

const testFAQ = `# Привязка Telegram

Чтобы привязать Telegram, используйте команду /link с вашим игровым ID.
Код действует 10 минут.

# Ставки и очки

Ставки принимаются в течение 5 минут после старта матча.
Стартовый баланс — 100 очков.

# Ранги и MMR

Ранг зависит от MMR. Обсидиан выдаётся с 2200 MMR.
`

// fallbackSearch used to ignore both of its arguments and return a fixed blurb,
// so a DeepSeek outage meant no answers at all even when the base had one.
func TestFallbackSearchFindsRelevantSection(t *testing.T) {
	svc := &FAQService{logger: nopLogger{}}

	answer := svc.fallbackSearch("как привязать телеграм?", testFAQ)

	if !strings.Contains(answer, "/link") {
		t.Errorf("fallback did not return the Telegram section:\n%s", answer)
	}
	if strings.Contains(answer, "Обсидиан") {
		t.Errorf("fallback returned an unrelated section:\n%s", answer)
	}
}

func TestFallbackSearchPicksBettingSection(t *testing.T) {
	svc := &FAQService{logger: nopLogger{}}

	answer := svc.fallbackSearch("сколько времени принимаются ставки", testFAQ)

	if !strings.Contains(answer, "5 минут") {
		t.Errorf("fallback did not return the betting section:\n%s", answer)
	}
}

func TestFallbackSearchReportsNoMatch(t *testing.T) {
	svc := &FAQService{logger: nopLogger{}}

	answer := svc.fallbackSearch("погода завтра восход солнца", testFAQ)

	if !strings.Contains(answer, "Не нашёл ответа") {
		t.Errorf("fallback should admit it found nothing, got:\n%s", answer)
	}
}

func TestExtractKeywordsDropsShortWordsAndPunctuation(t *testing.T) {
	got := extractKeywords("Как мне, привязать telegram?!")

	for _, kw := range got {
		if strings.ContainsAny(kw, ",?!") {
			t.Errorf("keyword %q kept punctuation", kw)
		}
	}
	if !contains(got, "привязать") || !contains(got, "telegram") {
		t.Errorf("expected the meaningful words, got %v", got)
	}
	if contains(got, "как") || contains(got, "мне") {
		t.Errorf("short filler words were kept: %v", got)
	}
}

// A base without markdown headings still has to be searchable.
func TestBestFAQSectionWithoutHeadings(t *testing.T) {
	faq := "Ставки открыты 5 минут.\n\nПривязка через команду link."

	section, score := bestFAQSection("когда закрываются ставки", faq)

	if score == 0 {
		t.Fatal("no section matched in a heading-less knowledge base")
	}
	if !strings.Contains(section, "Ставки") {
		t.Errorf("wrong section matched: %q", section)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
