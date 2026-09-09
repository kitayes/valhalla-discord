package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"blackwatch/internal/domain"
	"blackwatch/internal/repository"
)

// bindRepo is a stand-in for the parts of repository.Match that binding uses.
//
// It returns the same sentinels the Postgres implementation does. Binding is
// fail-closed on lookup failures, so a stub that answers "missing" with an
// anonymous error would be testing a contract the real repository does not have.
type bindRepo struct {
	repository.Match

	names     map[int]string
	byDiscord map[string]int // discordID -> playerID
	setCalls  int
}

func newBindRepo() *bindRepo {
	return &bindRepo{
		names:     map[int]string{1: "Alpha", 2: "Bravo"},
		byDiscord: map[string]int{},
	}
}

func (r *bindRepo) GetPlayerNameByID(_ context.Context, id int) (string, error) {
	name, ok := r.names[id]
	if !ok {
		return "", fmt.Errorf("player %d: %w", id, domain.ErrPlayerNotFound)
	}
	return name, nil
}

func (r *bindRepo) GetPlayerByDiscordID(_ context.Context, discordID string) (int, string, error) {
	id, ok := r.byDiscord[discordID]
	if !ok {
		return 0, "", fmt.Errorf("discord_id %s: %w", discordID, domain.ErrPlayerNotFound)
	}
	return id, r.names[id], nil
}

func (r *bindRepo) GetDiscordIDByPlayerID(_ context.Context, playerID int) (string, error) {
	for discordID, id := range r.byDiscord {
		if id == playerID {
			return discordID, nil
		}
	}
	return "", fmt.Errorf("player %d: %w", playerID, domain.ErrDiscordNotLinked)
}

func (r *bindRepo) SetDiscordID(_ context.Context, playerID int, discordID string) error {
	if existing, ok := r.byDiscord[discordID]; ok && existing != playerID {
		return repository.ErrDiscordIDTaken
	}
	for d, id := range r.byDiscord {
		if id == playerID {
			delete(r.byDiscord, d)
		}
	}
	r.byDiscord[discordID] = playerID
	r.setCalls++
	return nil
}

func (r *bindRepo) ClearDiscordID(_ context.Context, playerID int) error {
	for d, id := range r.byDiscord {
		if id == playerID {
			delete(r.byDiscord, d)
			return nil
		}
	}
	return fmt.Errorf("player %d: %w", playerID, domain.ErrPlayerNotFound)
}

func newBindService(repo *bindRepo) *MatchServiceImpl {
	return &MatchServiceImpl{repo: repo, logger: nopLogger{}}
}

func TestBindClaimsFreeProfile(t *testing.T) {
	repo := newBindRepo()
	svc := newBindService(repo)

	name, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false)
	if err != nil {
		t.Fatalf("binding a free profile failed: %v", err)
	}
	if name != "Alpha" {
		t.Errorf("bound name = %q, want Alpha", name)
	}
	if repo.byDiscord["discord-a"] != 1 {
		t.Error("binding was not persisted")
	}
}

// The profile is what the lobby resolves users through, so a claimed one must
// not be silently taken over by somebody else.
func TestBindRefusesClaimedProfile(t *testing.T) {
	repo := newBindRepo()
	svc := newBindService(repo)

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false); err != nil {
		t.Fatalf("setup bind failed: %v", err)
	}

	_, err := svc.BindDiscordID(context.Background(), 1, "discord-b", false)
	if err == nil {
		t.Fatal("a second account claimed an already bound profile")
	}
	// Asserted on the sentinel, not on a Russian substring of the message:
	// rewording the user-facing text must not silently disable the test.
	if !errors.Is(err, domain.ErrProfileTaken) {
		t.Errorf("claimed profile returned %v, want ErrProfileTaken", err)
	}
}

func TestBindRefusesSecondProfileForSameAccount(t *testing.T) {
	repo := newBindRepo()
	svc := newBindService(repo)

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false); err != nil {
		t.Fatalf("setup bind failed: %v", err)
	}

	_, err := svc.BindDiscordID(context.Background(), 2, "discord-a", false)
	if err == nil {
		t.Fatal("one account collected two profiles")
	}
	if !errors.Is(err, domain.ErrDiscordAlreadyBound) {
		t.Errorf("double bind returned %v, want ErrDiscordAlreadyBound", err)
	}
}

// Re-running the same bind is a no-op, not an error: users will press it twice.
func TestBindIsIdempotent(t *testing.T) {
	repo := newBindRepo()
	svc := newBindService(repo)

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false); err != nil {
		t.Fatalf("first bind failed: %v", err)
	}
	before := repo.setCalls

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false); err != nil {
		t.Fatalf("re-binding the same pair failed: %v", err)
	}
	if repo.setCalls != before {
		t.Error("a redundant bind wrote to the database again")
	}
}

// force is how an admin fixes a wrong claim.
func TestForceBindOverwritesExistingClaim(t *testing.T) {
	repo := newBindRepo()
	svc := newBindService(repo)

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false); err != nil {
		t.Fatalf("setup bind failed: %v", err)
	}

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-b", true); err != nil {
		t.Fatalf("admin force bind failed: %v", err)
	}
	if repo.byDiscord["discord-b"] != 1 {
		t.Error("force bind did not move the profile to the new account")
	}
}

func TestBindRejectsUnknownPlayer(t *testing.T) {
	svc := newBindService(newBindRepo())

	if _, err := svc.BindDiscordID(context.Background(), 999, "discord-a", false); err == nil {
		t.Error("bound a player that does not exist")
	}
}

func TestUnbindReleasesProfile(t *testing.T) {
	repo := newBindRepo()
	svc := newBindService(repo)

	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-a", false); err != nil {
		t.Fatalf("setup bind failed: %v", err)
	}

	name, err := svc.UnbindDiscordID(context.Background(), 1)
	if err != nil {
		t.Fatalf("unbind failed: %v", err)
	}
	if name != "Alpha" {
		t.Errorf("unbound name = %q, want Alpha", name)
	}

	// The profile must be claimable again afterwards.
	if _, err := svc.BindDiscordID(context.Background(), 1, "discord-b", false); err != nil {
		t.Errorf("released profile could not be re-claimed: %v", err)
	}
}
