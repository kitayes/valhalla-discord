package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"valhalla/migrations"

	"valhalla/internal/ai"
	"valhalla/internal/application"
	"valhalla/internal/delivery/discord"
	"valhalla/internal/delivery/telegram"
	"valhalla/internal/repository"
	"valhalla/pkg/config"
	"valhalla/pkg/logger"
	"valhalla/pkg/sheets"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

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
	if err := repository.RunMigrations(db, migrations.FS); err != nil {
		log.Error("failed to run migrations: %s", err.Error())
		return
	}
	log.Info("Migrations applied successfully")

	repos := repository.NewRepository(&cfg.Repo, db)

	gemini, err := ai.NewGeminiClient(cfg.GeminiKey)
	if err != nil {
		log.Error("failed to init gemini: %s", err.Error())
		return
	}

	var sheetsClient sheets.Client
	if _, err := os.Stat("google-credentials.json"); err == nil {
		sheetsClient, err = sheets.NewGoogleSheetsClient("google-credentials.json")
		if err != nil {
			log.Error("failed to init google sheets: %s", err.Error())
		} else {
			log.Info("Google Sheets service initialized")
		}
	} else {
		log.Warn("google-credentials.json not found, sheets integration disabled")
	}

	services := application.NewService(repos, gemini, sheetsClient, cfg.GoogleOwnerEmail, log)

	discordBot := discord.NewBot(&cfg, services, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := discordBot.Init(); err != nil {
		log.Error("failed to init discord bot: %s", err.Error())
		return
	}

	go func() {
		if err := discordBot.Run(ctx); err != nil {
			log.Error("discord bot run error: %s", err.Error())
		}
	}()

	var telegramBot *telegram.Bot
	if cfg.TelegramToken != "" {
		telegramBot, err = telegram.NewBot(cfg.TelegramToken, cfg.TelegramAdminIDs, services.TelegramService, log)
		if err != nil {
			log.Error("failed to init telegram bot: %s", err.Error())
		} else {
			go telegramBot.Start()
			log.Info("Telegram bot started")
		}
	} else {
		log.Warn("TELEGRAM_TOKEN not set, telegram bot disabled")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	discordBot.Stop()
	if telegramBot != nil {
		telegramBot.Stop()
	}
	log.Info("Bots Stopped")
}
