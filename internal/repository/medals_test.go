package repository

import (
	"testing"

	"blackwatch/internal/models"
)

func roster(names ...string) []models.PlayerResult {
	players := make([]models.PlayerResult, len(names))
	for i, n := range names {
		players[i] = models.PlayerResult{PlayerName: n}
	}
	return players
}

func TestResolveMedalPlayerID(t *testing.T) {
	players := roster("Thorin", "Loki", "Freya")
	ids := []int{11, 22, 33}

	tests := []struct {
		name  string
		medal string
		want  int
	}{
		{name: "exact match", medal: "Loki", want: 22},
		{name: "different case", medal: "THORIN", want: 11},
		{name: "surrounding whitespace", medal: "  Freya  ", want: 33},
		{name: "empty medal", medal: "", want: 0},
		{name: "whitespace only", medal: "   ", want: 0},
		// The medal name is OCR output. A player who is not on this scoreboard
		// must never be awarded, and must never be created.
		{name: "player not in this match", medal: "Odin", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveMedalPlayerID(tt.medal, players, ids); got != tt.want {
				t.Errorf("resolveMedalPlayerID(%q) = %d, want %d", tt.medal, got, tt.want)
			}
		})
	}
}

func TestResolveMedalPlayerIDMismatchedSlices(t *testing.T) {
	// Guard against awarding the wrong player if the roster and the resolved
	// IDs ever drift out of sync.
	if got := resolveMedalPlayerID("Thorin", roster("Thorin", "Loki"), []int{11}); got != 0 {
		t.Errorf("expected no award on length mismatch, got %d", got)
	}
}

func TestNullableID(t *testing.T) {
	if got := nullableID(0); got != nil {
		t.Errorf("nullableID(0) = %v, want nil so the column stays NULL", got)
	}
	if got := nullableID(42); got != 42 {
		t.Errorf("nullableID(42) = %v, want 42", got)
	}
}
