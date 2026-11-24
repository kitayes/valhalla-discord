package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestOCRClientLiveContainer(t *testing.T) {
	ocrURL := os.Getenv("OCR_E2E_URL")
	if ocrURL == "" {
		t.Skip("OCR_E2E_URL is not set")
	}

	tmpPNG := filepath.Join(t.TempDir(), "en_victory.png")
	cmd := exec.CommandContext(context.Background(), "python3", "../../ocr/tests/e2e_synthetic_render.py", "--save-en", tmpPNG)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render synthetic PNG failed: %v\n%s", err, string(out))
	}

	imgBytes, err := os.ReadFile(tmpPNG)
	if err != nil {
		t.Fatalf("read rendered PNG: %v", err)
	}

	expected := []string{"Bjorn", "Thorin", "Freya", "Arthas", "Kael", "Gunnar", "Loki", "Astrid", "Soren", "Ragnar"}
	client := NewOCRClient(ocrURL, 60*time.Second)
	match, err := client.ParseImageWithPlayers(context.Background(), imgBytes, expected)
	if err != nil {
		t.Fatalf("ParseImageWithPlayers against live container: %v", err)
	}

	if len(match.Players) != 10 {
		t.Fatalf("len(Players) = %d, want 10 (warnings=%v)", len(match.Players), match.Warnings)
	}
	if match.HeaderWinKills == nil || *match.HeaderWinKills != 20 {
		t.Errorf("HeaderWinKills = %v, want 20", match.HeaderWinKills)
	}
	if match.HeaderLoseKills == nil || *match.HeaderLoseKills != 12 {
		t.Errorf("HeaderLoseKills = %v, want 12", match.HeaderLoseKills)
	}
	if !strings.EqualFold(match.MVP, "Bjorn") {
		t.Errorf("MVP = %q, want Bjorn", match.MVP)
	}
	if !strings.EqualFold(match.SVP, "Gunnar") {
		t.Errorf("SVP = %q, want Gunnar", match.SVP)
	}
	if len(match.Warnings) != 0 {
		t.Errorf("expected 0 warnings on clean synthetic scoreboard, got: %v", match.Warnings)
	}
}

func TestOCRClientLiveRealDir(t *testing.T) {
	ocrURL := os.Getenv("OCR_E2E_URL")
	realDir := os.Getenv("OCR_E2E_REAL_DIR")
	if ocrURL == "" || realDir == "" {
		t.Skip("OCR_E2E_URL and OCR_E2E_REAL_DIR must both be set")
	}

	entries, err := os.ReadDir(realDir)
	if err != nil {
		t.Fatalf("read dir %s: %v", realDir, err)
	}

	client := NewOCRClient(ocrURL, 60*time.Second)
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			continue
		}
		imgBytes, err := os.ReadFile(filepath.Join(realDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		match, err := client.ParseImage(context.Background(), imgBytes)
		if err != nil {
			t.Errorf("%s: ParseImage error: %v", entry.Name(), err)
			continue
		}
		checked++
		if len(match.Players) != 10 {
			t.Errorf("%s: len(Players) = %d, want 10", entry.Name(), len(match.Players))
		}
		if len(match.Warnings) != 0 {
			t.Errorf("%s: unexpected warnings: %v", entry.Name(), match.Warnings)
		}
		t.Logf("%s: score=%v:%v mvp=%q svp=%q warnings=%v",
			entry.Name(), match.HeaderWinKills, match.HeaderLoseKills, match.MVP, match.SVP, match.Warnings)
	}
	if checked == 0 {
		t.Fatalf("no images found in %s", realDir)
	}
}
