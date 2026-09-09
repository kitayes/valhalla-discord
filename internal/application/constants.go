package application

import "time"

// sheetSyncTimeout bounds a detached Google Sheets sync. The callback that
// triggers it has no request to inherit a deadline from, so it needs its own.
const sheetSyncTimeout = 2 * time.Minute

const (
	defaultStatsCacheTTL = 60 // seconds

	defaultHistoryLimit = 20

	maxImageDownloadSize = 10 * 1024 * 1024 // 10MB

	signatureSeparator = "|"

	minDeathsForKDA = 1
)
