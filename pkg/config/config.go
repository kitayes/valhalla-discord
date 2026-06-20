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

	ClanTagRoleID     string `env:"CLAN_TAG_ROLE_ID" envDefault:""`
	ClanTagEnabled    bool   `env:"CLAN_TAG_ENABLED" envDefault:"false"`
	GuildID           string `env:"GUILD_ID" envDefault:""`
	RefereeRoleID     string `env:"REFEREE_ROLE_ID" envDefault:""`
	TelegramChannelID string `env:"TELEGRAM_CHANNEL_ID" envDefault:""`
	DeepSeekKey       string `env:"DEEPSEEK_KEY" envDefault:""`
	FAQChannelID      string `env:"FAQ_CHANNEL_ID" envDefault:""`
	FAQFilePath       string `env:"FAQ_FILE_PATH" envDefault:"assets/faq.md"`
}

func ReadEnvConfig(cfg *Config) error {
	return env.Parse(cfg)
}
