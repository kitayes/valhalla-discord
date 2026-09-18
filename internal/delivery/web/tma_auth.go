package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrEmptyInitData   = errors.New("empty initData")
	ErrMissingHash     = errors.New("missing hash in initData")
	ErrInvalidHash     = errors.New("invalid signature hash")
	ErrExpiredInitData = errors.New("initData has expired")
	ErrMissingUser     = errors.New("missing user in initData")
)

type contextKey string

const userCtxKey contextKey = "tma_user"

// TelegramUser represents the user payload parsed from validated Telegram WebApp initData.
type TelegramUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
	IsPremium    bool   `json:"is_premium,omitempty"`
}

// ValidateInitData verifies that initData was produced and signed by Telegram for botToken.
// If maxAge > 0, requests with auth_date older than maxAge are rejected.
func ValidateInitData(botToken string, rawInitData string, maxAge time.Duration) (*TelegramUser, error) {
	if strings.TrimSpace(rawInitData) == "" {
		return nil, ErrEmptyInitData
	}

	values, err := url.ParseQuery(rawInitData)
	if err != nil {
		return nil, fmt.Errorf("malformed initData query: %w", err)
	}

	expectedHash := values.Get("hash")
	if expectedHash == "" {
		return nil, ErrMissingHash
	}

	// 1. Check auth_date expiration if maxAge is specified
	authDateStr := values.Get("auth_date")
	if authDateStr != "" && maxAge > 0 {
		authTimestamp, err := strconv.ParseInt(authDateStr, 10, 64)
		if err == nil {
			issuedAt := time.Unix(authTimestamp, 0)
			if time.Since(issuedAt) > maxAge {
				return nil, ErrExpiredInitData
			}
		}
	}

	// 2. Prepare data-check-string (sort keys, skip hash)
	keys := make([]string, 0, len(values))
	for k := range values {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, values.Get(k)))
	}
	dataCheckString := strings.Join(pairs, "\n")

	// 3. Secret key = HMAC-SHA256("WebAppData", botToken)
	mac := hmac.New(sha256.New, []byte("WebAppData"))
	mac.Write([]byte(botToken))
	secretKey := mac.Sum(nil)

	// 4. Hash = HMAC-SHA256(secretKey, dataCheckString)
	h := hmac.New(sha256.New, secretKey)
	h.Write([]byte(dataCheckString))
	calculatedHash := hex.EncodeToString(h.Sum(nil))

	// 5. Constant-time comparison
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expectedHash)), []byte(strings.ToLower(calculatedHash))) != 1 {
		return nil, ErrInvalidHash
	}

	// 6. Extract user object
	userJSON := values.Get("user")
	if userJSON == "" {
		return nil, ErrMissingUser
	}

	var user TelegramUser
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return nil, fmt.Errorf("failed to decode user json: %w", err)
	}

	return &user, nil
}

// parseUnverifiedInitData parses TelegramUser from initData without HMAC validation (for test/dev use).
func parseUnverifiedInitData(rawInitData string) (*TelegramUser, error) {
	values, err := url.ParseQuery(rawInitData)
	if err != nil {
		return nil, fmt.Errorf("malformed initData query: %w", err)
	}
	userJSON := values.Get("user")
	if userJSON == "" {
		return nil, ErrMissingUser
	}
	var user TelegramUser
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return nil, fmt.Errorf("failed to decode user json: %w", err)
	}
	return &user, nil
}

// TMAAuthMiddleware verifies Telegram WebApp initData on API endpoints.
// Checks X-Telegram-Init-Data header, Authorization: tma <initData>, or query param init_data.
func TMAAuthMiddleware(botToken string, maxAge time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.Header.Get("X-Telegram-Init-Data")
		if initData == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(authHeader), "tma ") {
				initData = strings.TrimSpace(authHeader[4:])
			}
		}
		if initData == "" {
			initData = r.URL.Query().Get("init_data")
		}

		if initData == "" {
			if botToken == "" {
				if uidStr := r.Header.Get("X-Telegram-User-ID"); uidStr != "" {
					uid, _ := strconv.ParseInt(uidStr, 10, 64)
					u := &TelegramUser{
						ID:        uid,
						Username:  r.Header.Get("X-Telegram-Username"),
						FirstName: r.Header.Get("X-Telegram-First-Name"),
					}
					ctx := context.WithValue(r.Context(), userCtxKey, u)
					next(w, r.WithContext(ctx))
					return
				}
			}
			http.Error(w, `{"error":"unauthorized: missing initData"}`, http.StatusUnauthorized)
			return
		}

		var user *TelegramUser
		var err error
		if botToken == "" {
			user, err = parseUnverifiedInitData(initData)
		} else {
			user, err = ValidateInitData(botToken, initData, maxAge)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"unauthorized: %s"}`, err.Error()), http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userCtxKey, user)
		next(w, r.WithContext(ctx))
	}
}

// UserFromContext retrieves the authenticated TelegramUser from request context.
func UserFromContext(ctx context.Context) (*TelegramUser, bool) {
	u, ok := ctx.Value(userCtxKey).(*TelegramUser)
	return u, ok && u != nil
}

