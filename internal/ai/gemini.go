package ai

import (
	"blackwatch/internal/models"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type GeminiClient struct {
	model *genai.GenerativeModel
}

func NewGeminiClient(apiKey string) (*GeminiClient, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}

	model := client.GenerativeModel(geminiModel)
	model.ResponseMIMEType = responseMIMEType
	model.SetTemperature(aiTemperature)

	return &GeminiClient{model: model}, nil
}

func (g *GeminiClient) ParseImage(data []byte) (*models.Match, error) {
	return g.ParseImageWithPlayers(data, nil)
}

// ParseImageWithPlayers analyzes a scoreboard screenshot with optional expected player names
// for dynamic prompt substitution, improving OCR accuracy.
func (g *GeminiClient) ParseImageWithPlayers(data []byte, expectedPlayers []string) (*models.Match, error) {
	processor := NewImageProcessor()
	optimizedData, err := processor.OptimizeForAI(data)
	if err != nil {
		optimizedData = data
	}

	promptText := BuildParseImagePrompt(expectedPlayers)

	prompt := []genai.Part{
		genai.ImageData("jpeg", optimizedData),
		genai.Text(promptText),
	}

	resp, err := g.model.GenerateContent(context.Background(), prompt...)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from AI")
	}

	rawText, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return nil, fmt.Errorf("unexpected response format")
	}

	// Try parsing new format first: {"players": [...], "mvp": "...", "svp": "..."}
	var aiResp models.AIImageResponse
	if err := json.Unmarshal([]byte(rawText), &aiResp); err == nil && len(aiResp.Players) > 0 {
		match := &models.Match{Players: aiResp.Players}
		if aiResp.MVP != nil {
			match.MVP = *aiResp.MVP
		}
		if aiResp.SVP != nil {
			match.SVP = *aiResp.SVP
		}
		return match, nil
	}

	// Fallback: parse old format — plain array of PlayerResult
	var results []models.PlayerResult
	if err := json.Unmarshal([]byte(rawText), &results); err != nil {
		return nil, fmt.Errorf("json unmarshal error: %w | raw: %s", err, rawText)
	}

	return &models.Match{
		Players: results,
	}, nil
}
