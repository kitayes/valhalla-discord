package config

import (
	"valhalla/internal/repository"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	Repo         repository.Config `envPrefix:"REPO_"`
	DiscordToken string            `env:"DISCORD_TOKEN" envDefault:""`
	GeminiKey    string            `env:"GEMINI_KEY" envDefault:""`
	LogLevel     string            `env:"LOGGER_LEVEL" envDefault:"debug"`

	AllowedChannelID string   `env:"ALLOWED_CHANNEL_ID" envDefault:""`
	AdminUserIDs     []string `env:"ADMIN_USER_IDS" envSeparator:"," envDefault:""`

	GoogleOwnerEmail string `env:"GOOGLE_OWNER_EMAIL" envDefault:""`
}

func ReadEnvConfig(cfg *Config) error {
	return env.Parse(cfg)
}
