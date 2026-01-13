package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"valhalla/internal/models"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

//TODO: ревью от ИИшки

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
	promptText := `Analyze this MOBA (Mobile Legends) scoreboard screenshot.
    Extract data for ALL 10 players visible in the match results.
    
    CRITICAL RULES FOR PLAYER NAMES:
    - Extract player names EXACTLY as shown, character by character
    - DO NOT add or remove any characters from the name
    - DO NOT confuse similar characters (n vs m, l vs I, 0 vs O)
    - If a name has special characters (icons, flags, symbols), include them only if clearly readable
    - If a name is partially obscured, extract only the visible portion
    - Names must be CONSISTENT - the same player should have the exact same name
    
    For each player extract: player_name, result (WIN or LOSE), kills, deaths, assists.
    
    Return a JSON array of objects with these exact keys:
    "player_name" (string - exact name as displayed), 
    "result" (string - must be "WIN" or "LOSE"), 
    "kills" (int), 
    "deaths" (int), 
    "assists" (int).`

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
