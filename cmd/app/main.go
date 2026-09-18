package main

import (
	"blackwatch/migrations"
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"blackwatch/internal/ai"
	"blackwatch/internal/application"
	"blackwatch/internal/challonge"
	"blackwatch/internal/delivery/discord"
	"blackwatch/internal/delivery/telegram"
	"blackwatch/internal/delivery/web"
	"blackwatch/internal/repository"
	"blackwatch/pkg/config"
	"blackwatch/pkg/logger"
	"blackwatch/pkg/sheets"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	_ "time/tzdata"
)

// shutdownTimeout bounds how long in-flight admin requests may finish.
const shutdownTimeout = 10 * time.Second

// startupTimeout bounds database connection, migrations and cache warm-up, so a
// database that never answers fails the boot instead of hanging it.
const startupTimeout = 60 * time.Second

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

	gemini, err := ai.NewGeminiClient(cfg.GeminiKey)
	if err != nil {
		log.Error("failed to init gemini: %s", err.Error())
		return
	}

	var sheetsClient sheets.Client
	if _, err := os.Stat(cfg.GoogleCredentialsPath); err == nil {
		sheetsClient, err = sheets.NewGoogleSheetsClient(cfg.GoogleCredentialsPath)
		if err != nil {
			log.Error("failed to init google sheets: %s", err.Error())
		} else if cfg.SpreadsheetID == "" {
			// Credentials without a target sheet make every sync fail at the API
			// with an empty ID, which only ever showed up as a logged error.
			sheetsClient = nil
			log.Warn("GOOGLE_SHEET_ID is not set, sheets integration disabled")
		} else {
			log.Info("Google Sheets service initialized for sheet %s", cfg.SpreadsheetID)
		}
	} else {
		log.Warn("%s not found, sheets integration disabled", cfg.GoogleCredentialsPath)
	}

	services := application.NewService(repos, gemini, sheetsClient, cfg.GoogleOwnerEmail, cfg.SpreadsheetID, cfg.HTTPTimeoutSec, log)
	if cfg.BracketEnabled() {
		walkoverWin, walkoverLose, _ := config.ParseWalkoverScore(cfg.BracketWalkoverScore)
		challongeHTTP := &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second}
		provider := challonge.New(cfg.ChallongeAPIKey, cfg.ChallongeSubdomain, challongeHTTP)
		services.SetBracketService(application.NewBracketService(repos.Telegram, provider, walkoverWin, walkoverLose, log))
		log.Info("Challonge bracket service initialized")
	}

	// Initialize DeepSeek FAQ service if API key is configured
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

	// The Telegram bot is built and its callbacks are installed BEFORE the
	// Discord bot starts serving. Wiring them afterwards raced: discordgo
	// dispatches handlers on its own goroutines, and those handlers read the
	// very callback fields main was still assigning.
	var telegramBot *telegram.Bot
	if cfg.TelegramToken != "" {
		// Validated at config load; a failure here is unreachable.
		tournamentLoc, _ := cfg.TournamentLocation()
		telegramBot, err = telegram.NewBot(cfg.TelegramToken, cfg.TelegramAdminIDs, services.TelegramService, services.ProfileLinkService, services.BettingService, cfg.TelegramChannelID, cfg.TelegramTournamentChatID, telegram.BetSettings{Stakes: cfg.StakeOptions(), Max: cfg.BetMax}, services.Bracket, tournamentLoc, log)
		if err != nil {
			log.Error("failed to init telegram bot: %s", err.Error())
		} else if betting := telegramBot.BettingBot(); betting != nil {
			services.Lobby.SetMatchLiveCallback(betting.NotifyMatchLive)
			services.BettingService.SetPayoutCallback(betting.NotifyMatchResult)
			services.BettingService.SetRefundCallback(betting.NotifyMatchCancelled)
		}
	} else {
		log.Warn("TELEGRAM_TOKEN not set, telegram bot disabled")
	}

	location, _ := cfg.TournamentLocation()
	matchDesk := application.NewMatchDeskService(repository.NewTelegramPostgres(db), cfg.TelegramAdminIDs, location)
	if cfg.WebAppURL != "" {
		matchDesk.WithWebAppURL(cfg.WebAppURL)
	}
	services.SetMatchDeskService(matchDesk)
	if telegramBot != nil {
		telegramBot.WithMatchDesk(matchDesk)
		if cfg.WebAppURL != "" {
			telegramBot.WithWebAppURL(cfg.WebAppURL)
		}
		if services.TelegramService != nil {
			services.TelegramService.SetMatchNotifier(func(ctx context.Context, chatID int64, text string, hasWebAppBtn bool) {
				telegramBot.SendMatchNotification(chatID, text, hasWebAppBtn)
			})
		}
	}
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

	if telegramBot != nil {
		go telegramBot.Start(ctx)
		log.Info("Telegram bot started")
	}

	var adminServer *web.AdminServer
	if cfg.WebAdminPort != "" {
		adminServer, err = web.NewAdminServer(services, log, cfg.WebAdminPort, cfg.WebAdminKey, cfg.WebAdminTrustedProxies, cfg.TelegramToken)
		if err != nil {
			log.Error("failed to init web server: %s", err.Error())
			adminServer = nil
		} else {
			adminServer.WithAdminIDs(cfg.TelegramAdminIDs)
			if telegramBot != nil {
				adminServer.WithDebtorNotifier(func(ctx context.Context, adminChatID int64, msg string) error {
					return telegramBot.PingDebtors(ctx, adminChatID, msg)
				})
				adminServer.WithPhotoUploader(telegramBot.UploadPhoto)
			}
			srv := adminServer
			go func() {
				if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("web server error: %s", err.Error())
				}
			}()
			log.Info("Web server (Mini App & API) started on :%s", cfg.WebAdminPort)
			if cfg.WebAdminKey != "" {
				log.Info("Web admin dashboard enabled on :%s", cfg.WebAdminPort)
			}
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("Shutdown signal received, stopping...")
	cancel()

	// Each stage gets its own deadline. Sharing one budget meant a stuck admin
	// connection could burn the whole window and hand the Sheets drain a context
	// that had already expired — exactly the case the drain exists for.
	if adminServer != nil {
		adminCtx, adminCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := adminServer.Shutdown(adminCtx); err != nil {
			log.Error("web admin shutdown error: %s", err.Error())
		}
		adminCancel()
	}

	discordBot.Stop()
	if telegramBot != nil {
		telegramBot.Stop()
	}

	// Wait for in-flight Google Sheets syncs before closing the DB.
	syncCtx, syncCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer syncCancel()
	if err := services.MatchService.Shutdown(syncCtx); err != nil {
		log.Error("match service shutdown error: %s", err.Error())
	}

	log.Info("Bots Stopped")
}
