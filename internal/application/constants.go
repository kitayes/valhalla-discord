package application

const (
	// Cache TTL
	defaultStatsCacheTTL = 60 // seconds

	// History and query limits
	defaultHistoryLimit = 20

	// File size limits
	maxImageDownloadSize = 10 * 1024 * 1024 // 10MB

	// Message limits
	maxMessageLength     = 2000
	maxMessageTruncation = 1900

	// Display limits
	topPlayersLimit = 10

	// Match signature generation
	signatureSeparator = "|"

	// Concurrency limits
	maxConcurrentImageUploads = 3

	// Colors
	colorGold         = 0xFFD700
	colorBlue         = 0x3498DB
	colorTelegramBlue = 0x0088CC

	// Google Sheets configuration
	sheetsHeaderColor     = "FFD700" // Gold
	sheetsTextColor       = "000000" // Black
	sheetsBackgroundColor = "FFFFFF" // White
	sheetsPermissionRole  = "reader"
	sheetsPermissionType  = "anyone"

	// Player statistics
	minDeathsForKDA = 1

	// Excel report configuration
	excelSheetName       = "Статистика"
	excelDefaultRowCount = 1000
)
