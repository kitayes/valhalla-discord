package discord

import (
	"errors"
	"strings"
	"testing"

	"blackwatch/internal/application"
	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

func TestDraftCustomIDRoundTrip(t *testing.T) {
	for _, prefix := range []string{mixPickPrefix, mixCancelPrefix} {
		customID := draftCustomID(prefix, 37)
		if len(customID) > 100 {
			t.Errorf("%q is over Discord's 100-character custom ID limit", customID)
		}
		id, ok := parseDraftID(customID, prefix)
		if !ok || id != 37 {
			t.Errorf("parseDraftID(%q) = (%d, %v), want (37, true)", customID, id, ok)
		}
	}
}

func TestParseDraftIDRejectsGarbage(t *testing.T) {
	for _, customID := range []string{mixPickPrefix, mixPickPrefix + "x", mixPickPrefix + "-3", mixCancelPrefix + "5"} {
		if id, ok := parseDraftID(customID, mixPickPrefix); ok {
			t.Errorf("parseDraftID(%q) = %d, want failure", customID, id)
		}
	}
}

func testDraftView() application.DraftView {
	return application.DraftView{
		ID:   7,
		Turn: 1,
		Sides: [2]application.DraftSide{
			{Captain: models.Player{ID: 1, Name: "Альфа"}, Picks: []models.Player{{ID: 3, Name: "Гамма"}}},
			{Captain: models.Player{ID: 2, Name: "Бета"}},
		},
	}
}

func TestDraftEmbedShowsTeamsAndTurn(t *testing.T) {
	e := draftEmbed(testDraftView())

	if !strings.Contains(e.Title, "Альфа") || !strings.Contains(e.Title, "Бета") {
		t.Errorf("title %q does not name both captains", e.Title)
	}
	if !strings.Contains(e.Description, "Бета") || !strings.Contains(e.Description, "7") {
		t.Errorf("description %q does not say it is Бета's turn with 7 picks left", e.Description)
	}
	if len(e.Fields) != 2 || !strings.Contains(e.Fields[0].Value, "Гамма") {
		t.Errorf("fields %+v do not list team A's pick", e.Fields)
	}
}

func TestDraftComponentsSearchMembersAndCarryTheDraft(t *testing.T) {
	rows := draftComponents(7)

	picker := rows[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if picker.MenuType != discordgo.UserSelectMenu {
		t.Errorf("picker menu type = %v, want a user select (no 25-option limit)", picker.MenuType)
	}
	if id, ok := parseDraftID(picker.CustomID, mixPickPrefix); !ok || id != 7 {
		t.Errorf("picker custom ID %q does not carry draft 7", picker.CustomID)
	}
	cancel := rows[1].(discordgo.ActionsRow).Components[1].(discordgo.Button)
	if id, ok := parseDraftID(cancel.CustomID, mixCancelPrefix); !ok || id != 7 {
		t.Errorf("cancel custom ID %q does not carry draft 7", cancel.CustomID)
	}
}

// Every refusal has its own words: "not your turn" and "not in the lobby" send
// the captain to different places.
func TestDraftErrorMessagesAreDistinct(t *testing.T) {
	seen := map[string]error{}
	for _, err := range []error{
		domain.ErrDraftNotFound, domain.ErrDraftComplete, domain.ErrNotCaptain,
		domain.ErrNotYourTurn, domain.ErrNotInLobby, errors.New("boom"),
	} {
		msg := draftErrorMessage(err)
		if prev, dup := seen[msg]; dup {
			t.Errorf("%v and %v share the message %q", prev, err, msg)
		}
		seen[msg] = err
	}
}

func TestJoinMessageExplainsDraft(t *testing.T) {
	if msg := joinMessage(domain.ErrInDraft); !strings.Contains(msg, "драфт") {
		t.Errorf("joinMessage(ErrInDraft) = %q, want it to mention the draft", msg)
	}
}
