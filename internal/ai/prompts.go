package ai

const (
	// AI Model configuration
	geminiModel      = "gemini-2.5-flash"
	aiTemperature    = 0.1
	responseMIMEType = "application/json"
)

// ParseImagePrompt contains the prompt text for analyzing MOBA screenshots
const ParseImagePrompt = `Analyze this MOBA (Mobile Legends) scoreboard screenshot.
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
