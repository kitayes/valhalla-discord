package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOCRClientParseImageWithPlayersSuccess(t *testing.T) {
	var gotExpectedPlayers string
	var gotImageBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/parse" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		gotExpectedPlayers = r.FormValue("expected_players")
		f, _, err := r.FormFile("image")
		if err != nil {
			t.Fatalf("FormFile(image): %v", err)
		}
		defer f.Close()
		gotImageBytes, _ = io.ReadAll(f)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"players": []map[string]any{
				{"player_name": "Bjorn", "result": "WIN", "kills": 10, "deaths": 1, "assists": 5, "rating": 11.4, "confidence": 0.96},
				{"player_name": "P2", "result": "WIN", "kills": 3, "deaths": 2, "assists": 7, "rating": 8.0, "confidence": 0.95},
				{"player_name": "P3", "result": "WIN", "kills": 2, "deaths": 2, "assists": 8, "rating": 7.8, "confidence": 0.95},
				{"player_name": "P4", "result": "WIN", "kills": 2, "deaths": 2, "assists": 6, "rating": 7.1, "confidence": 0.95},
				{"player_name": "P5", "result": "WIN", "kills": 1, "deaths": 3, "assists": 9, "rating": 7.5, "confidence": 0.95},
				{"player_name": "P6", "result": "LOSE", "kills": 4, "deaths": 3, "assists": 2, "rating": 8.9, "confidence": 0.95},
				{"player_name": "P7", "result": "LOSE", "kills": 2, "deaths": 4, "assists": 3, "rating": 6.0, "confidence": 0.95},
				{"player_name": "P8", "result": "LOSE", "kills": 2, "deaths": 4, "assists": 4, "rating": 5.8, "confidence": 0.95},
				{"player_name": "P9", "result": "LOSE", "kills": 1, "deaths": 4, "assists": 3, "rating": 5.0, "confidence": 0.95},
				{"player_name": "P10", "result": "LOSE", "kills": 1, "deaths": 3, "assists": 2, "rating": 4.9, "confidence": 0.95},
			},
			"mvp":               "Bjorn",
			"svp":               "P6",
			"header_win_kills":  18,
			"header_lose_kills": 10,
			"warnings":          []string{"OCR side warning"},
		})
	}))
	defer srv.Close()

	client := NewOCRClient(srv.URL+"/", 5*time.Second)
	match, err := client.ParseImageWithPlayers(context.Background(), []byte("fake-png-bytes"), []string{"Bjorn", "Gunnar"})
	if err != nil {
		t.Fatalf("ParseImageWithPlayers: %v", err)
	}
	if string(gotImageBytes) != "fake-png-bytes" {
		t.Errorf("uploaded bytes = %q, want fake-png-bytes", string(gotImageBytes))
	}
	if !strings.Contains(gotExpectedPlayers, "Bjorn") || !strings.Contains(gotExpectedPlayers, "Gunnar") {
		t.Errorf("expected_players field = %q, want Bjorn and Gunnar", gotExpectedPlayers)
	}
	if len(match.Players) != 10 {
		t.Fatalf("len(Players) = %d, want 10", len(match.Players))
	}
	if match.MVP != "Bjorn" || match.SVP != "P6" {
		t.Errorf("medals = (%q, %q), want (Bjorn, P6)", match.MVP, match.SVP)
	}
	if match.Players[0].Rating != 11.4 {
		t.Errorf("rating = %v, want 11.4", match.Players[0].Rating)
	}
	if len(match.Warnings) != 1 || match.Warnings[0] != "OCR side warning" {
		t.Errorf("Warnings = %v, want [OCR side warning]", match.Warnings)
	}
}

func TestOCRClientErrors(t *testing.T) {
	t.Run("server 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "ocr crashed", http.StatusInternalServerError)
		}))
		defer srv.Close()

		client := NewOCRClient(srv.URL, 5*time.Second)
		if _, err := client.ParseImage(context.Background(), []byte("img")); err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want status 500 error", err)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{not json"))
		}))
		defer srv.Close()

		client := NewOCRClient(srv.URL, 5*time.Second)
		if _, err := client.ParseImage(context.Background(), []byte("img")); err == nil || !strings.Contains(err.Error(), "json") {
			t.Errorf("err = %v, want json error", err)
		}
	})
}
