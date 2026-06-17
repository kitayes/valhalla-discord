package config

import (
	"blackwatch/internal/repository"

	"github.com/caarlos0/env/v11"
)

//TODO: на каждый слой свой конфиг

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

	PlayerCacheSize int `env:"PLAYER_CACHE_SIZE" envDefault:"100000"`

	SpreadsheetID  string `env:"SPREADSHEET_ID" envDefault:""`
	HTTPTimeoutSec int    `env:"HTTP_TIMEOUT_SEC" envDefault:"10"`

	ClanTagRoleID  string `env:"CLAN_TAG_ROLE_ID" envDefault:""`
	ClanTagEnabled bool   `env:"CLAN_TAG_ENABLED" envDefault:"false"`
	GuildID        string `env:"GUILD_ID" envDefault:""`
}

func ReadEnvConfig(cfg *Config) error {
	return env.Parse(cfg)
}
