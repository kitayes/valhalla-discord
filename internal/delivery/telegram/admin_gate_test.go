package telegram

import "testing"

// The admin gate is a list. A command handled in handleAdminCommand but
// missing from the list does not fail loudly — it falls through to the user
// handler and answers as if it did not exist.
func TestAdminGateCoversExportCommands(t *testing.T) {
	for _, cmd := range []string{"/export", "/export_solo", "/export_sheet"} {
		if !isAdminCommand(cmd) {
			t.Errorf("%s is not gated as an admin command", cmd)
		}
	}
}

func TestAdminGateRejectsUserCommands(t *testing.T) {
	for _, cmd := range []string{"/start", "/help", "/report", "/exporter"} {
		if isAdminCommand(cmd) {
			t.Errorf("%s reached the admin handler", cmd)
		}
	}
}
