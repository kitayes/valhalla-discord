package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{
		DiscordToken:    "token",
		GuildID:         "123",
		AIProvider:      "ocr",
		OCRServiceURL:   "http://ocr:8000",
		HTTPTimeoutSec:  10,
		PlayerCacheSize: 1000,
		AdminUserIDs:    []string{"42"},
		WebAdminPort:    "8080",
		BetAmounts:      []int{25, 50, 100},
		BetMax:          250,
	}
}

func TestValidateAcceptsTheMinimalConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("the documented minimum was rejected: %v", err)
	}
}

func TestValidateRequiresSomebodyWithPermissions(t *testing.T) {
	t.Run("both empty is refused", func(t *testing.T) {
		c := validConfig()
		c.AdminUserIDs = nil
		c.RefereeRoleID = ""
		assertRejects(t, c, "ADMIN_USER_IDS or REFEREE_ROLE_ID")
	})

	t.Run("a list of blanks is not a list of admins", func(t *testing.T) {
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
		{"no ocr url when ai=ocr", func(c *Config) { c.OCRServiceURL = "" }, "OCR_SERVICE_URL"},
		{"invalid ocr url when ai=ocr", func(c *Config) { c.OCRServiceURL = "not-a-url" }, "OCR_SERVICE_URL"},
		{"no gemini key when ai=gemini", func(c *Config) { c.AIProvider = "gemini"; c.GeminiKey = "" }, "GEMINI_KEY"},
		{"unknown ai provider", func(c *Config) { c.AIProvider = "magic" }, "AI_PROVIDER"},
		{"zero http timeout", func(c *Config) { c.HTTPTimeoutSec = 0 }, "HTTP_TIMEOUT_SEC"},
		{"zero cache size", func(c *Config) { c.PlayerCacheSize = 0 }, "PLAYER_CACHE_SIZE"},
		{"clan tag on, no role", func(c *Config) { c.ClanTagEnabled = true }, "CLAN_TAG_ROLE_ID"},
		{"dashboard key, no port", func(c *Config) { c.WebAdminKey = "k"; c.WebAdminPort = "" }, "WEB_ADMIN_PORT"},
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
	c := &Config{HTTPTimeoutSec: 10, PlayerCacheSize: 1, WebAdminPort: "8080"}
	err := c.Validate()
	if err == nil {
		t.Fatal("an empty configuration was accepted")
	}
	for _, want := range []string{"DISCORD_TOKEN", "GUILD_ID", "OCR_SERVICE_URL", "ADMIN_USER_IDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() did not mention %s:\n%v", want, err)
		}
	}
}

func TestValidateAcceptsGeminiModeWithKey(t *testing.T) {
	c := validConfig()
	c.AIProvider = "gemini"
	c.OCRServiceURL = ""
	c.GeminiKey = "secret-gemini-key"
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() rejected valid gemini config: %v", err)
	}
}

func TestWebAdminIsOffWithoutAKey(t *testing.T) {
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

func TestValidateRejectsStakesAboveTheCap(t *testing.T) {
	c := validConfig()
	c.BetAmounts = []int{10, 500}
	c.BetMax = 100
	assertRejects(t, c, "exceeds BET_MAX")
}

func TestValidateRejectsUnusableBetSettings(t *testing.T) {
	t.Run("no positive stake leaves the keyboard empty", func(t *testing.T) {
		c := validConfig()
		c.BetAmounts = []int{0, -5}
		assertRejects(t, c, "BET_AMOUNTS")
	})

	t.Run("a non-positive cap refuses every bet", func(t *testing.T) {
		c := validConfig()
		c.BetMax = 0
		assertRejects(t, c, "BET_MAX")
	})
}

func TestStakeOptionsAreSortedAndDeduplicated(t *testing.T) {
	c := validConfig()
	c.BetAmounts = []int{50, 10, 25, 10, 0, -3}

	got := c.StakeOptions()
	want := []int{10, 25, 50}

	if len(got) != len(want) {
		t.Fatalf("StakeOptions() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("StakeOptions() = %v, want %v", got, want)
		}
	}
}
