package discord

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateMessageKeepsShortMessagesIntact(t *testing.T) {
	msg := "`[1]` **Игрок**\n"
	if got := truncateMessage(msg); got != msg {
		t.Errorf("truncateMessage altered a short message: %q", got)
	}
}

// Player names here are Cyrillic, so every rune is two bytes: cutting the string
// at a byte offset produced invalid UTF-8 and Discord rejected the whole reply.
func TestTruncateMessageProducesValidUTF8(t *testing.T) {
	msg := strings.Repeat("Игрок", 1000)

	got := truncateMessage(msg)

	if !utf8.ValidString(got) {
		t.Error("truncateMessage produced invalid UTF-8")
	}
	if n := utf8.RuneCountInString(got); n > maxMessageLength {
		t.Errorf("truncated message is %d characters, over Discord's %d limit", n, maxMessageLength)
	}
	if !strings.HasSuffix(got, "(список обрезан)") {
		t.Error("truncated message does not tell the user it was cut")
	}
}

// A message just over the limit must still come back under it.
func TestTruncateMessageAtBoundary(t *testing.T) {
	msg := strings.Repeat("я", maxMessageLength+1)

	got := truncateMessage(msg)

	if n := utf8.RuneCountInString(got); n > maxMessageLength {
		t.Errorf("truncated message is %d characters, over the %d limit", n, maxMessageLength)
	}
	if !utf8.ValidString(got) {
		t.Error("truncateMessage produced invalid UTF-8")
	}
}
