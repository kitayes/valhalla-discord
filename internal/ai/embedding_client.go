package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	ollamaEmbeddingsURL  = "http://ollama:11434/api/embeddings"
	embeddingModel       = "nomic-embed-text"
	embeddingHTTPTimeout = 10 * time.Second
)

// EmbeddingClient is an HTTP client for generating embeddings via Ollama.
type EmbeddingClient struct {
	httpClient *http.Client
}

// NewEmbeddingClient creates a new EmbeddingClient.
func NewEmbeddingClient() *EmbeddingClient {
	return &EmbeddingClient{
		httpClient: &http.Client{
			Timeout: embeddingHTTPTimeout,
		},
	}
}

type embeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

// GetEmbedding sends a string (e.g., a player nickname) to the local Ollama
// container and returns the embedding vector as []float64.
//
// It takes a context: this is a network call on the player-matching path, and
// without one a hung Ollama held the caller for the client timeout regardless of
// whether the request behind it had already been abandoned.
func (c *EmbeddingClient) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	reqBody := embeddingRequest{
		Model:  embeddingModel,
		Prompt: text,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaEmbeddingsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to build embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call ollama embeddings API: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embeddings returned status %d: %s", resp.StatusCode, string(body))
	}

	var embResp embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	if len(embResp.Embedding) == 0 {
		return nil, fmt.Errorf("ollama returned empty embedding for text: %s", text)
	}

	return embResp.Embedding, nil
}

// FormatEmbeddingForPG formats a []float64 slice into a PostgreSQL vector literal string
// e.g., "[0.1,0.2,0.3]"
func FormatEmbeddingForPG(embedding []float64) string {
	if len(embedding) == 0 {
		return "[]"
	}

	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, v := range embedding {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(fmt.Sprintf("%f", v))
	}
	buf.WriteByte(']')
	return buf.String()
}
