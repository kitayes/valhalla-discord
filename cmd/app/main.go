package main

import (
	"context"
	"embed"
	"os"
	"os/signal"
	"syscall"

	"valhalla/internal/ai"
	"valhalla/internal/application"
	"valhalla/internal/delivery/discord"
	"valhalla/internal/repository"
	"valhalla/pkg/config"
	"valhalla/pkg/logger"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func main() {
	_ = godotenv.Load()

	cfg := config.Config{}
	if err := config.ReadEnvConfig(&cfg); err != nil {
		panic(err)
	}

	log := logger.NewLogger(&logger.Config{Level: cfg.LogLevel})

	db, err := repository.NewPostgresDB(&cfg.Repo)
	if err != nil {
		log.Error("failed to init db: %s", err.Error())
		return
	}
	defer db.Close()

	log.Info("Running migrations...")
	if err := repository.RunMigrations(db, migrationFS); err != nil {
		log.Error("failed to run migrations: %s", err.Error())
		return
	}
	log.Info("Migrations applied successfully")

	repos := repository.NewRepository(&cfg.Repo, db)
	if err != nil {
		log.Error("failed to init db: %s", err.Error())
		return
	}
	defer db.Close()

	gemini, err := ai.NewGeminiClient(cfg.GeminiKey)
	if err != nil {
		log.Error("failed to init gemini: %s", err.Error())
		return
	}

	services := application.NewService(repos, gemini, log)

	bot := discord.NewBot(&cfg, services, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bot.Init(); err != nil {
		log.Error("failed to init bot: %s", err.Error())
		return
	}

	go func() {
		if err := bot.Run(ctx); err != nil {
			log.Error("bot run error: %s", err.Error())
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	bot.Stop()
	log.Info("Bot Stopped")
}
