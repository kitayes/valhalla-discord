package constants

const (
	TopPlayersLimit      = 10
	MaxMessageLength     = 2000
	MaxMessageTruncation = 1990

	WinRateExcellent = 75.0
	WinRateGood      = 60.0
	WinRatePoor      = 40.0

	ColorGold         = 0xFFD700
	ColorGreen        = 0x2ECC71
	ColorPurple       = 0x9B59B6
	ColorRed          = 0xE74C3C
	ColorGray         = 0x95A5A6
	ColorBlue         = 0x3498DB
	ColorTelegramBlue = 0x0088CC

	MinDeathsForKDA = 1

	DefaultStatsCacheTTLSeconds = 60

	DefaultHistoryLimit = 20

	MaxImageDownloadSize = 10 * 1024 * 1024 // 10MB

	SignatureSeparator = "|"

	MaxConcurrentImageUploads = 3
	MaxConcurrentSyncs        = 2

	SheetsHeaderColor     = "FFD700"
	SheetsTextColor       = "000000"
	SheetsBackgroundColor = "FFFFFF"
	SheetsPermissionRole  = "reader"
	SheetsPermissionType  = "anyone"

	ExcelSheetName       = "Статистика"
	ExcelDefaultRowCount = 1000
)
