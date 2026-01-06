package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"valhalla/internal/models"

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

	model := client.GenerativeModel("gemini-2.5-flash")

	model.ResponseMIMEType = "application/json"
	model.SetTemperature(0.1)

	return &GeminiClient{model: model}, nil
}

func (g *GeminiClient) ParseImage(data []byte) (*models.Match, error) {
	promptText := `Analyze this MOBA scoreboard screenshot.
    Extract data for ALL players visible in the list.
    For each player extract: player_name, result (WIN or LOSE), kills, deaths, assists.
    
    Return a JSON array of objects with these exact keys:
    "player_name" (string), "result" (string), "kills" (int), "deaths" (int), "assists" (int).`

	prompt := []genai.Part{
		genai.ImageData("png", data),
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

	var results []models.PlayerResult
	if err := json.Unmarshal([]byte(rawText), &results); err != nil {
		return nil, fmt.Errorf("json unmarshal error: %w | raw: %s", err, rawText)
	}

	return &models.Match{
		Players: results,
	}, nil
}
