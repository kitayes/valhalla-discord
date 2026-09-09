package config

import (
	"strings"
	"testing"
)

// validConfig is the smallest configuration Validate accepts. Each test starts
// from it and breaks exactly one thing, so a failure names its own cause.
func validConfig() *Config {
	return &Config{
		DiscordToken:    "token",
		GuildID:         "123",
		GeminiKey:       "key",
		HTTPTimeoutSec:  10,
		PlayerCacheSize: 1000,
		AdminUserIDs:    []string{"42"},
		WebAdminPort:    "8080",
	}
}

func TestValidateAcceptsTheMinimalConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("the documented minimum was rejected: %v", err)
	}
}

func TestValidateRequiresSomebodyWithPermissions(t *testing.T) {
	// Permission checks resolve through ADMIN_USER_IDS and REFEREE_ROLE_ID and
	// nothing else. With both empty the bot starts, connects, registers its
	// commands — and then refuses every one of them to everybody.
	t.Run("both empty is refused", func(t *testing.T) {
		c := validConfig()
		c.AdminUserIDs = nil
		c.RefereeRoleID = ""
		assertRejects(t, c, "ADMIN_USER_IDS or REFEREE_ROLE_ID")
	})

	t.Run("a list of blanks is not a list of admins", func(t *testing.T) {
		// An unset comma-separated variable parses to one empty string, which
		// len() alone would read as "configured".
		c := validConfig()
		c.AdminUserIDs = []string{"", "  "}
		c.RefereeRoleID = ""
		assertRejects(t, c, "ADMIN_USER_IDS or REFEREE_ROLE_ID")
	})

	t.Run("referee role alone is enough", func(t *testing.T) {
		c := validConfig()
		c.AdminUserIDs = nil
		c.RefereeRoleID = "role-1"
		if err := c.Validate(); err != nil {
			t.Errorf("a referee role with no admins was rejected: %v", err)
		}
	})
}

func TestValidateRejectsEnabledSubsystemsWithNoSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"no discord token", func(c *Config) { c.DiscordToken = "" }, "DISCORD_TOKEN"},
		{"no guild", func(c *Config) { c.GuildID = "" }, "GUILD_ID"},
		{"no gemini key", func(c *Config) { c.GeminiKey = "" }, "GEMINI_KEY"},
		{"zero http timeout", func(c *Config) { c.HTTPTimeoutSec = 0 }, "HTTP_TIMEOUT_SEC"},
		{"zero cache size", func(c *Config) { c.PlayerCacheSize = 0 }, "PLAYER_CACHE_SIZE"},
		{"clan tag on, no role", func(c *Config) { c.ClanTagEnabled = true }, "CLAN_TAG_ROLE_ID"},
		{"dashboard key, no port", func(c *Config) { c.WebAdminKey = "k"; c.WebAdminPort = "" }, "WEB_ADMIN_PORT"},
		{"telegram channel, no token", func(c *Config) { c.TelegramChannelID = "-100" }, "TELEGRAM_CHANNEL_ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(c)
			assertRejects(t, c, tt.want)
		})
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	// An operator fixing one variable per restart is the reason these are joined
	// rather than returned on the first failure.
	c := &Config{HTTPTimeoutSec: 10, PlayerCacheSize: 1, WebAdminPort: "8080"}
	err := c.Validate()
	if err == nil {
		t.Fatal("an empty configuration was accepted")
	}
	for _, want := range []string{"DISCORD_TOKEN", "GUILD_ID", "GEMINI_KEY", "ADMIN_USER_IDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() did not mention %s:\n%v", want, err)
		}
	}
}

func TestWebAdminIsOffWithoutAKey(t *testing.T) {
	// The dashboard used to fall back to a literal default password.
	c := validConfig()
	if c.WebAdminEnabled() {
		t.Error("dashboard reports enabled with no WEB_ADMIN_KEY")
	}
	c.WebAdminKey = "secret"
	if !c.WebAdminEnabled() {
		t.Error("dashboard reports disabled with WEB_ADMIN_KEY set")
	}
}

func assertRejects(t *testing.T, c *Config, want string) {
	t.Helper()
	err := c.Validate()
	if err == nil {
		t.Fatalf("Validate() accepted a config missing %s", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("Validate() = %q, want it to mention %s", err, want)
	}
}
