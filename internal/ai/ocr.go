package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"blackwatch/internal/models"
)

const defaultOCRTimeout = 30 * time.Second

// OCRClient calls the local RapidOCR sidecar service over HTTP to parse MLBB
// scoreboard screenshots without external vision APIs.
type OCRClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewOCRClient creates an HTTP client targeting the RapidOCR sidecar.
func NewOCRClient(baseURL string, timeout time.Duration) *OCRClient {
	if timeout <= 0 {
		timeout = defaultOCRTimeout
	}
	return &OCRClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *OCRClient) ParseImage(ctx context.Context, data []byte) (*models.Match, error) {
	return c.ParseImageWithPlayers(ctx, data, nil)
}

// ParseImageWithPlayers sends the screenshot and optional expected lobby
// player names to the RapidOCR sidecar, then augments any parser warnings with
// deterministic scoreboard invariant checks.
func (c *OCRClient) ParseImageWithPlayers(ctx context.Context, data []byte, expectedPlayers []string) (*models.Match, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("image", "scoreboard.png")
	if err != nil {
		return nil, fmt.Errorf("ocr: create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("ocr: write image bytes: %w", err)
	}

	if len(expectedPlayers) > 0 {
		encoded, err := json.Marshal(expectedPlayers)
		if err != nil {
			return nil, fmt.Errorf("ocr: marshal expected players: %w", err)
		}
		if err := writer.WriteField("expected_players", string(encoded)); err != nil {
			return nil, fmt.Errorf("ocr: write expected_players field: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("ocr: close multipart body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/parse", &body)
	if err != nil {
		return nil, fmt.Errorf("ocr: build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("ocr: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocr: service returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed models.AIImageResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("ocr: invalid json response: %w", err)
	}

	match := &models.Match{
		Players:         parsed.Players,
		HeaderWinKills:  parsed.HeaderWinKills,
		HeaderLoseKills: parsed.HeaderLoseKills,
	}
	if parsed.MVP != nil {
		match.MVP = *parsed.MVP
	}
	if parsed.SVP != nil {
		match.SVP = *parsed.SVP
	}

	match.Warnings = mergeUniqueWarnings(parsed.Warnings, ValidateScoreboard(match))
	return match, nil
}

func mergeUniqueWarnings(groups ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, g := range groups {
		for _, w := range g {
			clean := strings.TrimSpace(w)
			if clean == "" {
				continue
			}
			if _, ok := seen[clean]; ok {
				continue
			}
			seen[clean] = struct{}{}
			out = append(out, clean)
		}
	}
	return out
}
