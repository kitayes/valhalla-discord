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
	deepseekEndpoint = "https://api.deepseek.com/v1/chat/completions"
	deepseekModel    = "deepseek-chat"
	deepseekTimeout  = 15 * time.Second
)

// DeepSeekClient is a lightweight HTTP client for the DeepSeek API (OpenAI-compatible).
type DeepSeekClient struct {
	apiKey     string
	httpClient *http.Client
}

// ChatMessage represents a message in the chat completion request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest is the request body for DeepSeek API.
type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
}

// ChatCompletionResponse is the response from DeepSeek API.
type ChatCompletionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// NewDeepSeekClient creates a new DeepSeek API client.
func NewDeepSeekClient(apiKey string) *DeepSeekClient {
	return &DeepSeekClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: deepseekTimeout,
		},
	}
}

// AnswerQuestion sends a user question with FAQ context to DeepSeek and returns the answer.
func (c *DeepSeekClient) AnswerQuestion(faqContext, userQuestion string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("deepseek: API key not configured")
	}

	systemPrompt := fmt.Sprintf(
		"Ты — официальный FAQ-ассистент киберспортивной лиги BlackWatch. "+
			"Отвечай только на основе предоставленной ниже базы знаний. "+
			"Если ответа нет в базе знаний, вежливо скажи, что не знаешь ответа, и предложи обратиться к администратору. "+
			"Отвечай кратко, по делу, на русском языке.\n\n"+
			"=== БАЗА ЗНАНИЙ ===\n%s\n=== КОНЕЦ БАЗЫ ЗНАНИЙ ===",
		faqContext,
	)

	req := ChatCompletionRequest{
		Model: deepseekModel,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userQuestion},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("deepseek: marshal error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(context.Background(), "POST", deepseekEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("deepseek: request error: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("deepseek: API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("deepseek: read error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deepseek: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("deepseek: unmarshal error: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("deepseek: API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("deepseek: empty response")
	}

	return chatResp.Choices[0].Message.Content, nil
}
