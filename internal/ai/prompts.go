package ai

import "fmt"

const (
	geminiModel      = "gemini-3.5-flash"
	aiTemperature    = 0.1
	responseMIMEType = "application/json"
)

const ParseImagePrompt = `Analyze this MOBA (Mobile Legends) scoreboard screenshot.
    Extract data for ALL 10 players visible in the match results.
    
    CRITICAL RULES FOR PLAYER NAMES:
    - Extract player names EXACTLY as shown, character by character
    - DO NOT add or remove any characters from the name
    - DO NOT confuse similar characters (n vs m, l vs I, 0 vs O)
    - If a name has special characters (icons, flags, symbols), include them only if clearly readable
    - If a name is partially obscured, extract only the visible portion
    - Names must be CONSISTENT - the same player should have the exact same name
    
    For each player extract: player_name, result (WIN or LOSE), kills, deaths, assists, champion (if visible).
    Also identify which player received the MVP medal (top performer on winning team) and which received the SVPG medal (top performer on losing team).
    
    Return a JSON object with keys:
    "players" (array of player objects with keys: "player_name", "result", "kills", "deaths", "assists", "champion"),
    "mvp" (string - player_name of the MVP on the winning team, or null if not visible),
    "svp" (string - player_name of the SVPG/SVP on the losing team, or null if not visible).`

const parseImagePromptWithPlayersTemplate = `Analyze this MOBA (Mobile Legends) scoreboard screenshot.
    The following 10 players are known to be in this match (use these names as reference for OCR):
    %s
    
    CRITICAL RULES FOR PLAYER NAMES:
    - Match each scoreboard entry to the closest name from the list above
    - If a name on screen is clearly one of the expected players, use the EXACT spelling from the list
    - If a name is partially obscured, use the expected player name that best matches
    - DO NOT confuse similar characters (n vs m, l vs I, 0 vs O)
    - Names must be CONSISTENT with the provided list
    
    For each player extract: player_name, result (WIN or LOSE), kills, deaths, assists.
    
    Return a JSON array of objects with these exact keys:
    "player_name" (string - exact name as displayed), 
    "result" (string - must be "WIN" or "LOSE"), 
    "kills" (int), 
    "deaths" (int), 
    "assists" (int).`

// BuildParseImagePrompt constructs the AI prompt, optionally injecting expected player names
// to improve OCR accuracy for known lobby matches.
func BuildParseImagePrompt(expectedPlayers []string) string {
	if len(expectedPlayers) == 0 {
		return ParseImagePrompt
	}
	playerList := ""
	for i, name := range expectedPlayers {
		playerList += fmt.Sprintf("%d. %s\n", i+1, name)
	}
	return fmt.Sprintf(parseImagePromptWithPlayersTemplate, playerList)
}
