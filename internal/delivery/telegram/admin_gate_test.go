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

// Every admin command handleAdminCommand serves must pass the gate. The
// tournament commands were missing: /new_tourney, /select_tourney,
// /finish_tourney and /tournaments answered as unknown.
func TestAdminGateCoversTournamentCommands(t *testing.T) {
	for _, cmd := range []string{"/tournaments", "/new_tourney Квалификация 2", "/select_tourney 2", "/finish_tourney", "/next_qual"} {
		if !isAdminCommand(cmd) {
			t.Errorf("%s is not gated as an admin command", cmd)
		}
	}
}
