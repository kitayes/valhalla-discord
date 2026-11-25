package config

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"blackwatch/internal/domain"
	"blackwatch/internal/repository"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Repo          repository.Config `envPrefix:"REPO_"`
	DiscordToken  string            `env:"DISCORD_TOKEN" envDefault:""`
	AIProvider    string            `env:"AI_PROVIDER" envDefault:"ocr"`
	OCRServiceURL string            `env:"OCR_SERVICE_URL" envDefault:"http://ocr:8000"`
	GeminiKey     string            `env:"GEMINI_KEY" envDefault:""`
	LogLevel      string            `env:"LOGGER_LEVEL" envDefault:"debug"`

	AllowedChannelID string   `env:"ALLOWED_CHANNEL_ID" envDefault:""`
	AdminUserIDs     []string `env:"ADMIN_USER_IDS" envSeparator:"," envDefault:""`

	GoogleOwnerEmail      string `env:"GOOGLE_OWNER_EMAIL" envDefault:""`
	GoogleCredentialsPath string `env:"GOOGLE_CREDENTIALS_PATH" envDefault:"google-credentials.json"`
	SpreadsheetID         string `env:"GOOGLE_SHEET_ID" envDefault:""`

	PlayerCacheSize int `env:"PLAYER_CACHE_SIZE" envDefault:"100000"`
	HTTPTimeoutSec  int `env:"HTTP_TIMEOUT_SEC" envDefault:"10"`

	ClanTagRoleID  string `env:"CLAN_TAG_ROLE_ID" envDefault:""`
	ClanTagEnabled bool   `env:"CLAN_TAG_ENABLED" envDefault:"false"`
	GuildID        string `env:"GUILD_ID" envDefault:""`
	RefereeRoleID  string `env:"REFEREE_ROLE_ID" envDefault:""`

	// BetAmounts are the stake buttons drawn under a match's betting post.
	BetAmounts []int `env:"BET_AMOUNTS" envSeparator:"," envDefault:"25,50,100"`
	// BetMax caps a single stake, the "Макс" button included.
	BetMax int `env:"BET_MAX" envDefault:"250"`

	DeepSeekKey  string `env:"DEEPSEEK_KEY" envDefault:""`
	FAQChannelID string `env:"FAQ_CHANNEL_ID" envDefault:""`
	FAQFilePath  string `env:"FAQ_FILE_PATH" envDefault:"assets/faq.md"`

	WebAdminPort           string   `env:"WEB_ADMIN_PORT" envDefault:"8080"`
	WebAdminKey            string   `env:"WEB_ADMIN_KEY" envDefault:""`
	WebAdminTrustedProxies []string `env:"WEB_ADMIN_TRUSTED_PROXIES" envSeparator:"," envDefault:""`

	// Rank roles granted automatically after each match.
	TierRoleObsidian      string `env:"TIER_ROLE_OBSIDIAN" envDefault:""`
	TierRoleOnyx          string `env:"TIER_ROLE_ONYX" envDefault:""`
	TierRoleCarbon        string `env:"TIER_ROLE_CARBON" envDefault:""`
	TierRoleGraphite      string `env:"TIER_ROLE_GRAPHITE" envDefault:""`
	TierAnnounceChannelID string `env:"TIER_ANNOUNCE_CHANNEL_ID" envDefault:""`

	// Economy & Activity channels.
	LevelAnnounceChannelID string `env:"LEVEL_ANNOUNCE_CHANNEL_ID" envDefault:""`
	BettingChannelID       string `env:"BETTING_CHANNEL_ID" envDefault:""`
	ShopChannelID          string `env:"SHOP_CHANNEL_ID" envDefault:""`
	ShopAdminChannelID     string `env:"SHOP_ADMIN_CHANNEL_ID" envDefault:""`
}

// TierRoleIDs maps each configured tier to its Discord role ID, skipping tiers
// that were left unset.
func (c *Config) TierRoleIDs() map[domain.Tier]string {
	roles := make(map[domain.Tier]string, 4)
	for tier, id := range map[domain.Tier]string{
		domain.TierObsidian: c.TierRoleObsidian,
		domain.TierOnyx:     c.TierRoleOnyx,
		domain.TierCarbon:   c.TierRoleCarbon,
		domain.TierGraphite: c.TierRoleGraphite,
	} {
		if id != "" {
			roles[tier] = id
		}
	}
	return roles
}

// StakeOptions returns the configured stake buttons in ascending order with
// duplicates removed.
func (c *Config) StakeOptions() []int {
	seen := make(map[int]struct{}, len(c.BetAmounts))
	out := make([]int, 0, len(c.BetAmounts))
	for _, v := range c.BetAmounts {
		if v <= 0 {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// WebAdminEnabled reports whether the license admin dashboard should be started.
func (c *Config) WebAdminEnabled() bool {
	return c.WebAdminKey != ""
}

// EffectiveAIProvider returns the configured scoreboard parser backend
// ("ocr" or "gemini"), defaulting to "ocr" when empty.
func (c *Config) EffectiveAIProvider() string {
	p := strings.ToLower(strings.TrimSpace(c.AIProvider))
	if p == "" {
		return "ocr"
	}
	return p
}

// Validate rejects a configuration the process cannot run correctly with.
func (c *Config) Validate() error {
	var errs []error

	if c.DiscordToken == "" {
		errs = append(errs, errors.New("DISCORD_TOKEN is required"))
	}
	if c.GuildID == "" {
		errs = append(errs, errors.New("GUILD_ID is required: slash commands are registered per guild"))
	}
	switch c.EffectiveAIProvider() {
	case "ocr":
		u, err := url.ParseRequestURI(strings.TrimSpace(c.OCRServiceURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("OCR_SERVICE_URL must be a valid http/https URL when AI_PROVIDER=ocr"))
		}
	case "gemini":
		if c.GeminiKey == "" {
			errs = append(errs, errors.New("GEMINI_KEY is required when AI_PROVIDER=gemini"))
		}
	default:
		errs = append(errs, fmt.Errorf("AI_PROVIDER must be \"ocr\" or \"gemini\", got %q", c.AIProvider))
	}
	if c.HTTPTimeoutSec <= 0 {
		errs = append(errs, fmt.Errorf("HTTP_TIMEOUT_SEC must be positive, got %d", c.HTTPTimeoutSec))
	}
	if c.PlayerCacheSize <= 0 {
		errs = append(errs, fmt.Errorf("PLAYER_CACHE_SIZE must be positive, got %d", c.PlayerCacheSize))
	}

	if c.BetMax <= 0 {
		errs = append(errs, fmt.Errorf("BET_MAX must be positive, got %d", c.BetMax))
	}
	stakes := c.StakeOptions()
	if len(stakes) == 0 {
		errs = append(errs, errors.New("BET_AMOUNTS must list at least one positive stake"))
	}
	for _, v := range stakes {
		if c.BetMax > 0 && v > c.BetMax {
			errs = append(errs, fmt.Errorf("BET_AMOUNTS contains %d, which exceeds BET_MAX (%d)", v, c.BetMax))
		}
	}

	if len(nonEmpty(c.AdminUserIDs)) == 0 && c.RefereeRoleID == "" {
		errs = append(errs, errors.New(
			"ADMIN_USER_IDS or REFEREE_ROLE_ID is required: without either, nobody can run admin commands or manage matches"))
	}

	if c.WebAdminEnabled() && c.WebAdminPort == "" {
		errs = append(errs, errors.New("WEB_ADMIN_PORT must be set when WEB_ADMIN_KEY is configured"))
	}
	if c.ClanTagEnabled && c.ClanTagRoleID == "" {
		errs = append(errs, errors.New("CLAN_TAG_ROLE_ID must be set when CLAN_TAG_ENABLED is true"))
	}

	return errors.Join(errs...)
}

func ReadEnvConfig(cfg *Config) error {
	if err := env.Parse(cfg); err != nil {
		return err
	}
	return cfg.Validate()
}
