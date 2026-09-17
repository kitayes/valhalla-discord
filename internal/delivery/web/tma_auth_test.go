package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

func signInitData(botToken string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, params[k]))
	}
	dataCheckString := strings.Join(pairs, "\n")

	mac := hmac.New(sha256.New, []byte("WebAppData"))
	mac.Write([]byte(botToken))
	secretKey := mac.Sum(nil)

	h := hmac.New(sha256.New, secretKey)
	h.Write([]byte(dataCheckString))
	hash := hex.EncodeToString(h.Sum(nil))

	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	v.Set("hash", hash)
	return v.Encode()
}

func TestValidateInitData(t *testing.T) {
	botToken := "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"

	t.Run("valid signature", func(t *testing.T) {
		params := map[string]string{
			"auth_date": fmt.Sprintf("%d", time.Now().Unix()),
			"query_id":  "AAHdF6IQAAAAAN0XohDhrOrc",
			"user":      `{"id":987654,"first_name":"Valhalla","username":"valhalla_player"}`,
		}
		raw := signInitData(botToken, params)

		user, err := ValidateInitData(botToken, raw, time.Hour)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user.ID != 987654 || user.Username != "valhalla_player" || user.FirstName != "Valhalla" {
			t.Errorf("got user %+v, want ID 987654", user)
		}
	})

	t.Run("tampered signature", func(t *testing.T) {
		params := map[string]string{
			"auth_date": fmt.Sprintf("%d", time.Now().Unix()),
			"query_id":  "AAHdF6IQAAAAAN0XohDhrOrc",
			"user":      `{"id":987654,"first_name":"Valhalla"}`,
		}
		raw := signInitData(botToken, params) + "&tampered=true"

		_, err := ValidateInitData(botToken, raw, time.Hour)
		if err == nil {
			t.Fatal("expected error for tampered initData, got nil")
		}
	})

	t.Run("expired initData", func(t *testing.T) {
		params := map[string]string{
			"auth_date": fmt.Sprintf("%d", time.Now().Add(-2*time.Hour).Unix()),
			"user":      `{"id":987654,"first_name":"Valhalla"}`,
		}
		raw := signInitData(botToken, params)

		_, err := ValidateInitData(botToken, raw, time.Hour)
		if !errors.Is(err, ErrExpiredInitData) {
			t.Fatalf("expected ErrExpiredInitData, got %v", err)
		}
	})

	t.Run("empty initData", func(t *testing.T) {
		_, err := ValidateInitData(botToken, "", time.Hour)
		if !errors.Is(err, ErrEmptyInitData) {
			t.Fatalf("expected ErrEmptyInitData, got %v", err)
		}
	})
}

func TestTMAAuthMiddleware(t *testing.T) {
	botToken := "test_token"
	handler := TMAAuthMiddleware(botToken, time.Hour, func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok || u == nil {
			http.Error(w, "missing context user", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "hello %d", u.ID)
	})

	t.Run("authorized via X-Telegram-Init-Data", func(t *testing.T) {
		raw := signInitData(botToken, map[string]string{
			"auth_date": fmt.Sprintf("%d", time.Now().Unix()),
			"user":      `{"id":42,"first_name":"Arthur"}`,
		})
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.Header.Set("X-Telegram-Init-Data", raw)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != "hello 42" {
			t.Errorf("got %q, want 'hello 42'", rec.Body.String())
		}
	})

	t.Run("unauthorized without header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})
}
