package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	model := client.GenerativeModel("gemini-1.5-flash")
	return &GeminiClient{model: model}, nil
}

func (g *GeminiClient) AnalyzeScreenshot(data []byte) ([]models.PlayerResult, error) {
	prompt := []genai.Part{
		genai.ImageData("png", data),
		genai.Text(`Analyze this MOBA scoreboard. Return JSON array ONLY: 
        [{"player_name":"...","result":"WIN" or "LOSE","kills":0,"deaths":0,"assists":0,"champion":"..."}]`),
	}

	resp, err := g.model.GenerateContent(context.Background(), prompt...)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from AI")
	}

	raw := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimSuffix(raw, "```")

	var results []models.PlayerResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return nil, err
	}
	return results, nil
}
