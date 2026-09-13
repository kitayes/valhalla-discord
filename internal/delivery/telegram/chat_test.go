package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// The bot registers whoever writes to it under the chat id. Added to a group,
// it would register the group itself as a player and answer registration
// prompts in front of everyone.
func TestOnlyPrivateChatsAreServed(t *testing.T) {
	cases := []struct {
		chat *tgbotapi.Chat
		want bool
	}{
		{&tgbotapi.Chat{Type: "private"}, true},
		{&tgbotapi.Chat{Type: "group"}, false},
		{&tgbotapi.Chat{Type: "supergroup"}, false},
		{&tgbotapi.Chat{Type: "channel"}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := servesChat(tc.chat); got != tc.want {
			t.Errorf("servesChat(%+v) = %v, want %v", tc.chat, got, tc.want)
		}
	}
}
