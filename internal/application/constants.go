package application

const (
	defaultStatsCacheTTL = 60 // seconds

	defaultHistoryLimit = 20

	maxImageDownloadSize = 10 * 1024 * 1024 // 10MB

	maxMessageLength     = 2000
	maxMessageTruncation = 1900

	topPlayersLimit = 10

	signatureSeparator = "|"

	maxConcurrentImageUploads = 3

	colorGold         = 0xFFD700
	colorBlue         = 0x3498DB
	colorTelegramBlue = 0x0088CC

	sheetsHeaderColor     = "FFD700" // Gold
	sheetsTextColor       = "000000" // Black
	sheetsBackgroundColor = "FFFFFF" // White
	sheetsPermissionRole  = "reader"
	sheetsPermissionType  = "anyone"

	minDeathsForKDA = 1

	excelSheetName       = "Статистика"
	excelDefaultRowCount = 1000
)
