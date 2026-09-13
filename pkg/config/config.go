package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"blackwatch/internal/domain"
	"blackwatch/internal/repository"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Repo          repository.Config `envPrefix:"REPO_"`
	DiscordToken  string            `env:"DISCORD_TOKEN" envDefault:""`
	TelegramToken string            `env:"TELEGRAM_TOKEN" envDefault:""`
	GeminiKey     string            `env:"GEMINI_KEY" envDefault:""`
	LogLevel      string            `env:"LOGGER_LEVEL" envDefault:"debug"`

	AllowedChannelID string   `env:"ALLOWED_CHANNEL_ID" envDefault:""`
	AdminUserIDs     []string `env:"ADMIN_USER_IDS" envSeparator:"," envDefault:""`

	TelegramAdminIDs []int64 `env:"TELEGRAM_ADMIN_IDS" envSeparator:"," envDefault:""`

	GoogleOwnerEmail string `env:"GOOGLE_OWNER_EMAIL" envDefault:""`
	// GoogleCredentialsPath used to be a bare literal in main. Where a file is
	// read from is configuration, not a constant in the entrypoint.
	GoogleCredentialsPath string `env:"GOOGLE_CREDENTIALS_PATH" envDefault:"google-credentials.json"`

	PlayerCacheSize int `env:"PLAYER_CACHE_SIZE" envDefault:"100000"`

	// SpreadsheetID is read from GOOGLE_SHEET_ID, which is what .env and the
	// README have always used. The code used to read SPREADSHEET_ID instead, so
	// the ID was empty and every sheet sync failed silently.
	SpreadsheetID string `env:"GOOGLE_SHEET_ID" envDefault:""`

	HTTPTimeoutSec int `env:"HTTP_TIMEOUT_SEC" envDefault:"10"`

	ClanTagRoleID     string `env:"CLAN_TAG_ROLE_ID" envDefault:""`
	ClanTagEnabled    bool   `env:"CLAN_TAG_ENABLED" envDefault:"false"`
	GuildID           string `env:"GUILD_ID" envDefault:""`
	RefereeRoleID     string `env:"REFEREE_ROLE_ID" envDefault:""`
	TelegramChannelID string `env:"TELEGRAM_CHANNEL_ID" envDefault:""`
	// BetAmounts are the stake buttons drawn under a match's betting post.
	// Sent to Telegram in ascending order with duplicates dropped; see
	// StakeOptions.
	BetAmounts []int `env:"BET_AMOUNTS" envSeparator:"," envDefault:"10,25,50"`
	// BetMax caps a single stake, the "Макс" button included. It is enforced
	// server-side on every bet: callback data is client-supplied and a modified
	// client can send whatever number it likes.
	BetMax       int    `env:"BET_MAX" envDefault:"100"`
	DeepSeekKey  string `env:"DEEPSEEK_KEY" envDefault:""`
	FAQChannelID string `env:"FAQ_CHANNEL_ID" envDefault:""`
	FAQFilePath  string `env:"FAQ_FILE_PATH" envDefault:"assets/faq.md"`
	WebAdminPort string `env:"WEB_ADMIN_PORT" envDefault:"8080"`
	// WebAdminKey has no default on purpose. It used to fall back to a literal
	// "blackwatch-admin", which meant the dashboard came up with a publicly
	// known password on every deployment that never set the variable. Empty
	// means the dashboard does not start at all.
	WebAdminKey string `env:"WEB_ADMIN_KEY" envDefault:""`
	// WebAdminTrustedProxies lists the reverse proxies (CIDRs or bare IPs)
	// allowed to set X-Forwarded-For and X-Forwarded-Proto for the dashboard.
	// Empty — the default — means the header is ignored and the direct peer
	// address is used, because a caller who can reach the port directly would
	// otherwise pick their own address and walk straight past the login throttle.
	WebAdminTrustedProxies []string `env:"WEB_ADMIN_TRUSTED_PROXIES" envSeparator:"," envDefault:""`

	// Rank roles granted automatically after each match. Leave a role empty to
	// skip that tier; leave them all empty to disable role syncing entirely.
	TierRoleObsidian string `env:"TIER_ROLE_OBSIDIAN" envDefault:""`
	TierRoleOnyx     string `env:"TIER_ROLE_ONYX" envDefault:""`
	TierRoleCarbon   string `env:"TIER_ROLE_CARBON" envDefault:""`
	TierRoleGraphite string `env:"TIER_ROLE_GRAPHITE" envDefault:""`
	// TierAnnounceChannelID receives promotion/demotion embeds. When empty, the
	// channel the match was closed in is used.
	TierAnnounceChannelID string `env:"TIER_ANNOUNCE_CHANNEL_ID" envDefault:""`
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
//
// The order is fixed here rather than left to the operator so the keyboard
// cannot come out as "50 10 25": the buttons sit in one row and a bettor picks
// by position as much as by reading.
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

// nonEmpty drops blank entries from a comma-separated list. An unset variable
// parses to a slice holding one empty string, which len() alone reads as
// "configured".
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// WebAdminEnabled reports whether the dashboard should be started.
func (c *Config) WebAdminEnabled() bool {
	return c.WebAdminKey != ""
}

// Validate rejects a configuration the process cannot run correctly with.
//
// These checks used to be scattered through main as ad-hoc `if x == ""` guards,
// each with its own idea of what to do about it, and GUILD_ID had no check at
// all — a deployment that forgot it silently registered its slash commands into
// a guild ID hardcoded in the Discord package.
func (c *Config) Validate() error {
	var errs []error

	if c.DiscordToken == "" {
		errs = append(errs, errors.New("DISCORD_TOKEN is required"))
	}
	if c.GuildID == "" {
		errs = append(errs, errors.New("GUILD_ID is required: slash commands are registered per guild"))
	}
	if c.GeminiKey == "" {
		errs = append(errs, errors.New("GEMINI_KEY is required: screenshot parsing does not work without it"))
	}
	if c.HTTPTimeoutSec <= 0 {
		errs = append(errs, fmt.Errorf("HTTP_TIMEOUT_SEC must be positive, got %d", c.HTTPTimeoutSec))
	}
	if c.PlayerCacheSize <= 0 {
		errs = append(errs, fmt.Errorf("PLAYER_CACHE_SIZE must be positive, got %d", c.PlayerCacheSize))
	}

	// A stake the bettor cannot afford to be offered is worse than no button:
	// the tap is accepted by Telegram and refused by the database, which reads
	// as a broken bot. Both bounds are checked here so that never ships.
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

	// Nobody to administer the bot is a configuration error, not a deployment
	// with no admins. Permission checks resolve through these two and nothing
	// else, so with both empty every admin command and the entire match
	// lifecycle — create, balance, cancel, and the WIN buttons — answers "У вас
	// нет прав" to everyone, with nothing in the log to say why.
	if len(nonEmpty(c.AdminUserIDs)) == 0 && c.RefereeRoleID == "" {
		errs = append(errs, errors.New(
			"ADMIN_USER_IDS or REFEREE_ROLE_ID is required: without either, nobody can run admin commands or manage matches"))
	}

	// An enabled subsystem with missing settings is a startup error, not a
	// silent skip: the operator asked for it and would otherwise never find out
	// it was not running.
	if c.WebAdminEnabled() && c.WebAdminPort == "" {
		errs = append(errs, errors.New("WEB_ADMIN_PORT must be set when WEB_ADMIN_KEY is configured"))
	}
	if c.ClanTagEnabled && c.ClanTagRoleID == "" {
		errs = append(errs, errors.New("CLAN_TAG_ROLE_ID must be set when CLAN_TAG_ENABLED is true"))
	}
	if c.TelegramChannelID != "" && c.TelegramToken == "" {
		errs = append(errs, errors.New("TELEGRAM_CHANNEL_ID is set but TELEGRAM_TOKEN is empty"))
	}

	return errors.Join(errs...)
}

func ReadEnvConfig(cfg *Config) error {
	if err := env.Parse(cfg); err != nil {
		return err
	}
	return cfg.Validate()
}
