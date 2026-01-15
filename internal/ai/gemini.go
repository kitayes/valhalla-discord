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

	model := client.GenerativeModel(geminiModel)
	model.ResponseMIMEType = responseMIMEType
	model.SetTemperature(aiTemperature)

	return &GeminiClient{model: model}, nil
}

func (g *GeminiClient) ParseImage(data []byte) (*models.Match, error) {
	prompt := []genai.Part{
		genai.ImageData("png", data),
		genai.Text(ParseImagePrompt),
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
