package telegram

import (
	"blackwatch/internal/application"
	"blackwatch/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type deliveryDeskStore struct {
	state    models.MatchDesk
	snapshot models.DeskContext
}

func (s *deliveryDeskStore) UpdateMatchDesk(_ context.Context, f func(*models.MatchDesk, models.DeskContext) error) error {
	data, _ := json.Marshal(s.state)
	var next models.MatchDesk
	_ = json.Unmarshal(data, &next)
	if err := f(&next, s.snapshot); err != nil {
		return err
	}
	s.state = next
	return nil
}

func TestMatchDeskDeliveryRetriesOnlyFailedRecipientAndUsesNativeCopyButton(t *testing.T) {
	counts := map[string]int{}
	sawCopy := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			chat := r.Form.Get("chat_id")
			counts[chat]++
			if chat == "100" {
				var markup struct {
					Buttons [][]models.DeskButton `json:"inline_keyboard"`
				}
				if err := json.Unmarshal([]byte(r.Form.Get("reply_markup")), &markup); err != nil {
					t.Error(err)
				}
				for _, row := range markup.Buttons {
					for _, button := range row {
						if button.CopyText != nil && button.CopyText.Text == "67890 (1003)" {
							sawCopy = true
						}
					}
				}
				if counts[chat] == 1 {
					fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"retry later"}`)
					return
				}
			}
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":100,"type":"private"}}}`)
	}))
	defer server.Close()
	api, err := tgbotapi.NewBotAPIWithClient("test-token", server.URL+"/bot%s/%s", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	a, b := 10, 20
	ta, tb := int64(100), int64(200)
	store := &deliveryDeskStore{snapshot: models.DeskContext{StartsAt: time.Now().Add(time.Hour), Matches: []models.BracketMatch{{ID: 1, PlayOrder: 12, Team1ID: &a, Team2ID: &b, Team1Name: "Alpha", Team2Name: "Beta", State: models.BracketOpen}}, Captains: map[int]models.TelegramPlayer{
		a: {TelegramID: &ta, IsCaptain: true, TeamID: &a, GameID: "12345"}, b: {TelegramID: &tb, IsCaptain: true, TeamID: &b, GameID: "67890", ZoneID: "1003"},
	}}}
	bot := (&Bot{bot: api, logger: &captureLogger{}}).WithMatchDesk(application.NewMatchDeskService(store, []int64{999}, time.UTC))
	ctx := context.Background()
	bot.flushMatchDesk(ctx)
	if len(store.state.Outbox) != 1 {
		t.Fatalf("failed recipient was lost: %+v", store.state.Outbox)
	}
	bot.flushMatchDesk(ctx)
	if counts["100"] != 2 || counts["200"] != 1 || len(store.state.Outbox) != 0 {
		t.Fatalf("delivery counts=%v pending=%v", counts, store.state.Outbox)
	}
	if !sawCopy {
		t.Fatal("copy_text absent from Telegram payload")
	}
	gen := store.state.Matches[1].Generation
	bot.handleCallbackQuery(ctx, &tgbotapi.CallbackQuery{ID: "q", From: &tgbotapi.User{ID: 100}, Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: -1, Type: "group"}}, Data: fmt.Sprintf("desk:ready:1:%d", gen)})
	if store.state.Matches[1].Ready[0] {
		t.Fatal("forwarded group card changed readiness")
	}
}
