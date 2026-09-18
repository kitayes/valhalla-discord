package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"blackwatch/internal/application"
)

func TestInviteTokenFromStart(t *testing.T) {
	cases := []struct {
		payload string
		token   string
		ok      bool
	}{
		{"join_7-AbCd1234", "7-AbCd1234", true},
		{"  join_7-AbCd1234 \n", "7-AbCd1234", true},
		{"join_", "", false},
		{"", "", false},
		{"7-AbCd1234", "", false},
		{"ref_123", "", false},
	}
	for _, c := range cases {
		got, ok := InviteTokenFromStart(c.payload)
		if ok != c.ok || got != c.token {
			t.Errorf("InviteTokenFromStart(%q) = %q,%v; want %q,%v", c.payload, got, ok, c.token, c.ok)
		}
	}
	if got := InviteStartPayload("7-AbCd1234"); got != "join_7-AbCd1234" {
		t.Errorf("InviteStartPayload = %q", got)
	}
}

type inviteSvc struct {
	application.TelegramService
}

func (inviteSvc) GenerateInviteToken(context.Context, int64) (string, error) { return "7-AbCd1234", nil }

// The invite endpoint hands out a t.me deep link once the bot's username is
// known, and only the bare token before that.
func TestGenerateInviteLink(t *testing.T) {
	server, err := NewAdminServer(&application.Service{TelegramService: inviteSvc{}}, &captureLogger{}, "8080", "", nil, "tok")
	if err != nil {
		t.Fatal(err)
	}
	call := func() (token, link string) {
		ctx := context.WithValue(context.Background(), userCtxKey, &TelegramUser{ID: 1})
		req := httptest.NewRequest(http.MethodPost, "/api/team/invite", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		server.handleGenerateInvite(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Token string `json:"token"`
			Link  string `json:"link"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Token, out.Link
	}

	if token, link := call(); token != "7-AbCd1234" || link != "" {
		t.Errorf("without bot username: token=%q link=%q", token, link)
	}
	server.WithBotUsername("@valhalla_bot")
	if _, link := call(); link != "https://t.me/valhalla_bot?start=join_7-AbCd1234" {
		t.Errorf("with bot username: link=%q", link)
	}
}
