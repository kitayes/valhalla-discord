package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"blackwatch/internal/ai"
	"blackwatch/internal/application"
	"blackwatch/internal/delivery/discord"
	"blackwatch/internal/delivery/web"
	"blackwatch/internal/repository"
	"blackwatch/migrations"
	"blackwatch/pkg/config"
	"blackwatch/pkg/logger"
	"blackwatch/pkg/sheets"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	_ "time/tzdata"
)

const (
	shutdownTimeout = 10 * time.Second
	startupTimeout  = 60 * time.Second
)

func main() {
	_ = godotenv.Load()

	cfg := config.Config{}
	if err := config.ReadEnvConfig(&cfg); err != nil {
		panic(err)
	}

	log := logger.NewLogger(&logger.Config{Level: cfg.LogLevel})

	startupCtx, startupCancel := context.WithTimeout(context.Background(), startupTimeout)
	defer startupCancel()

	db, err := repository.NewPostgresDB(startupCtx, &cfg.Repo)
	if err != nil {
		log.Error("failed to init db: %s", err.Error())
		return
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("failed to close db: %s", err.Error())
		}
	}()

	log.Info("Running migrations...")
	if err := repository.RunMigrations(db, migrations.FS); err != nil {
		log.Error("failed to run migrations: %s", err.Error())
		return
	}
	log.Info("Migrations applied successfully")

	repos, err := repository.NewRepository(startupCtx, &cfg.Repo, db, cfg.PlayerCacheSize)
	if err != nil {
		log.Error("failed to init repository: %s", err.Error())
		return
	}

	var aiProvider application.AIProvider
	switch cfg.EffectiveAIProvider() {
	case "gemini":
		aiProvider, err = ai.NewGeminiClient(cfg.GeminiKey)
		if err != nil {
			log.Error("failed to init gemini: %s", err.Error())
			return
		}
		log.Info("Scoreboard AI provider: Gemini")
	default:
		ocrTimeout := max(30*time.Second, time.Duration(cfg.HTTPTimeoutSec)*time.Second)
		aiProvider = ai.NewOCRClient(cfg.OCRServiceURL, ocrTimeout)
		log.Info("Scoreboard AI provider: RapidOCR (%s)", cfg.OCRServiceURL)
	}

	var sheetsClient sheets.Client
	if _, err := os.Stat(cfg.GoogleCredentialsPath); err == nil {
		sheetsClient, err = sheets.NewGoogleSheetsClient(cfg.GoogleCredentialsPath)
		if err != nil {
			log.Error("failed to init google sheets: %s", err.Error())
		} else if cfg.SpreadsheetID == "" {
			sheetsClient = nil
			log.Warn("GOOGLE_SHEET_ID is not set, sheets integration disabled")
		} else {
			log.Info("Google Sheets service initialized for sheet %s", cfg.SpreadsheetID)
		}
	} else {
		log.Warn("%s not found, sheets integration disabled", cfg.GoogleCredentialsPath)
	}

	services := application.NewService(repos, aiProvider, sheetsClient, cfg.GoogleOwnerEmail, cfg.SpreadsheetID, cfg.HTTPTimeoutSec, log)

	if cfg.DeepSeekKey != "" {
		deepseek := ai.NewDeepSeekClient(cfg.DeepSeekKey)
		faqService, err := application.NewFAQService(log, deepseek, cfg.FAQFilePath)
		if err != nil {
			log.Error("failed to init FAQ service: %s", err.Error())
		} else {
			services.SetFAQService(faqService)
			log.Info("DeepSeek FAQ service initialized")
		}
	} else {
		log.Warn("DEEPSEEK_KEY not set, FAQ assistant disabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discordBot := discord.NewBot(&cfg, services, log)
	if err := discordBot.Init(); err != nil {
		log.Error("failed to init discord bot: %s", err.Error())
		return
	}

	go func() {
		if err := discordBot.Run(ctx); err != nil {
			log.Error("discord bot run error: %s", err.Error())
		}
	}()

	var adminServer *web.AdminServer
	if cfg.WebAdminEnabled() {
		adminServer, err = web.NewAdminServer(services, log, cfg.WebAdminPort, cfg.WebAdminKey, cfg.WebAdminTrustedProxies)
		if err != nil {
			log.Error("failed to init web admin dashboard: %s", err.Error())
			adminServer = nil
		} else {
			srv := adminServer
			go func() {
				if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("web admin server error: %s", err.Error())
				}
			}()
			log.Info("Web admin dashboard enabled on :%s", cfg.WebAdminPort)
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("Shutdown signal received, stopping...")
	cancel()

	if adminServer != nil {
		adminCtx, adminCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := adminServer.Shutdown(adminCtx); err != nil {
			log.Error("web admin shutdown error: %s", err.Error())
		}
		adminCancel()
	}

	discordBot.Stop()

	syncCtx, syncCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer syncCancel()
	if err := services.MatchService.Shutdown(syncCtx); err != nil {
		log.Error("match service shutdown error: %s", err.Error())
	}

	log.Info("Shutdown complete")
}
