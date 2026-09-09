package discord

import (
	"testing"
	"unicode/utf8"

	"blackwatch/pkg/config"

	"github.com/bwmarrin/discordgo"
)

// testLogger satisfies application.Logger without writing anywhere.
type testLogger struct{}

func (testLogger) Error(string, ...interface{}) {}
func (testLogger) Warn(string, ...interface{})  {}
func (testLogger) Info(string, ...interface{})  {}
func (testLogger) Debug(string, ...interface{}) {}

// initBot runs the real Init. discordgo.New only builds a struct — nothing
// reaches the network until Open — so the whole command surface can be built
// and inspected offline.
func initBot(t *testing.T) *Bot {
	t.Helper()
	cfg := &config.Config{DiscordToken: "test", GuildID: "1"}
	b := NewBot(cfg, nil, testLogger{})
	if err := b.Init(); err != nil {
		t.Fatalf("Init() failed: %v", err)
	}
	t.Cleanup(b.Stop)
	return b
}

func commandsByName(t *testing.T, b *Bot) map[string]*discordgo.ApplicationCommand {
	t.Helper()
	byName := make(map[string]*discordgo.ApplicationCommand, len(b.commands))
	for _, c := range b.commands {
		if _, dup := byName[c.Name]; dup {
			t.Errorf("command %q is registered twice", c.Name)
		}
		byName[c.Name] = c
	}
	return byName
}

func TestSelfServiceCommandsTakeNoForeignIdentifier(t *testing.T) {
	// /link handed out a Telegram link code for any player id, and /unlink
	// released any profile's binding — both public, neither checking ownership.
	// Chained, they were a two-command profile takeover. The profile now comes
	// from the caller's own /bind binding, so these commands must carry no
	// argument naming somebody else.
	byName := commandsByName(t, initBot(t))

	for _, name := range []string{"link", "unlink"} {
		cmd, ok := byName[name]
		if !ok {
			t.Fatalf("/%s is not registered", name)
		}
		if len(cmd.Options) != 0 {
			t.Errorf("/%s takes %d option(s); it must act on the caller's own profile only",
				name, len(cmd.Options))
		}
	}

	// The admin escape hatch is where naming another profile belongs.
	unlinkPlayer, ok := byName["unlink_player"]
	if !ok {
		t.Fatal("/unlink_player is not registered: admins have no way to release a binding")
	}
	if len(unlinkPlayer.Options) == 0 || !unlinkPlayer.Options[0].Required {
		t.Error("/unlink_player must take a required player id")
	}
}

func TestTelegramProfileArgumentIsOptional(t *testing.T) {
	// A required player_name made looking up a stranger the default. It is now
	// optional: no argument means your own profile, and naming someone else is
	// gated on admin in the handler.
	cmd, ok := commandsByName(t, initBot(t))["telegram_profile"]
	if !ok {
		t.Fatal("/telegram_profile is not registered")
	}
	for _, opt := range cmd.Options {
		if opt.Required {
			t.Errorf("option %q is required; naming another player must be opt-in", opt.Name)
		}
	}
}

func TestBindTakesANicknameNotAnID(t *testing.T) {
	// A players row is only born from a parsed match screenshot, so a newcomer
	// has no id to give: the lobby needed a binding, the binding needed a
	// profile, and the profile needed a match they could not join. Taking a
	// nickname is what breaks that cycle — an Integer option here restores it.
	byName := commandsByName(t, initBot(t))

	for _, name := range []string{"bind", "bind_player"} {
		cmd, ok := byName[name]
		if !ok {
			t.Fatalf("/%s is not registered", name)
		}
		if len(cmd.Options) == 0 {
			t.Fatalf("/%s takes no options", name)
		}
		first := cmd.Options[0]
		if first.Type != discordgo.ApplicationCommandOptionString {
			t.Errorf("/%s first option %q is %v, want a String nickname",
				name, first.Name, first.Type)
		}
		if first.Name != "nickname" {
			t.Errorf("/%s first option is %q, want \"nickname\"", name, first.Name)
		}
	}
}

func TestReportCommandsDefaultToTheCaller(t *testing.T) {
	// Nobody knows their own numeric id, and it used to be the only way to ask.
	// No arguments now means "me"; a required option would put the id problem
	// back, in a different shape.
	byName := commandsByName(t, initBot(t))

	for _, name := range []string{"profile", "history"} {
		cmd, ok := byName[name]
		if !ok {
			t.Fatalf("/%s is not registered", name)
		}
		for _, opt := range cmd.Options {
			if opt.Required {
				t.Errorf("/%s option %q is required; the command must answer about the caller with no arguments",
					name, opt.Name)
			}
			if opt.Name == "id" {
				t.Errorf("/%s still takes a numeric id", name)
			}
		}
	}
}

func TestNicknameOptionsOfferAutocomplete(t *testing.T) {
	// A free-typed nickname is how a typo spawns a phantom profile. Every
	// nickname argument suggests the names that already exist.
	for _, cmd := range initBot(t).commands {
		for _, opt := range cmd.Options {
			if opt.Name == "nickname" && !opt.Autocomplete {
				t.Errorf("/%s: nickname option has no autocomplete", cmd.Name)
			}
		}
	}
}

func TestQueueBanDurationIsAChoice(t *testing.T) {
	// Free text went to a parser that could only reject a typo after the fact.
	cmd, ok := commandsByName(t, initBot(t))["queue_ban"]
	if !ok {
		t.Fatal("/queue_ban is not registered")
	}
	for _, opt := range cmd.Options {
		if opt.Name == "duration" && len(opt.Choices) == 0 {
			t.Error("/queue_ban duration is still free text")
		}
	}
}

func TestEveryCommandIsDescribedAndNamed(t *testing.T) {
	// Discord rejects the whole bulk registration if any single command is
	// malformed, which shows up at boot as "Failed to register commands" with
	// every command missing — not as a problem with the one that is wrong.
	for _, c := range initBot(t).commands {
		if c.Name == "" {
			t.Error("a command has no name")
			continue
		}
		// Discord counts characters, not bytes, and every description here is
		// Cyrillic — two bytes per character. len() would fail nine commands
		// that are well inside the limit.
		if n := utf8.RuneCountInString(c.Name); n > 32 {
			t.Errorf("command %q: name is %d chars, Discord allows 32", c.Name, n)
		}
		if c.Description == "" {
			t.Errorf("command %q has no description", c.Name)
		}
		if n := utf8.RuneCountInString(c.Description); n > 100 {
			t.Errorf("command %q: description is %d chars, Discord allows 100", c.Name, n)
		}
		seenOptional := false
		for _, opt := range c.Options {
			if opt.Description == "" {
				t.Errorf("command %q, option %q has no description", c.Name, opt.Name)
			}
			// Discord requires every required option to precede the optional
			// ones; the order here is the order they are sent in.
			if opt.Required && seenOptional {
				t.Errorf("command %q: required option %q follows an optional one", c.Name, opt.Name)
			}
			if !opt.Required {
				seenOptional = true
			}
		}
	}
}
