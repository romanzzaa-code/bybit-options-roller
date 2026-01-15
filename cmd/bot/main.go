package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/joho/godotenv/autoload"

	"github.com/romanzzaa/bybit-options-roller/internal/bot"
	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	"github.com/romanzzaa/bybit-options-roller/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	dbConnConfig := database.Config{
		Host:     cfg.Database.Host,
		Port:     cfg.Database.Port,
		User:     cfg.Database.User,
		Password: cfg.Database.Password,
		DBName:   cfg.Database.DBName,
		SSLMode:  cfg.Database.SSLMode,
	}

	db, err := database.NewConnection(dbConnConfig)
	if err != nil {
		logger.Error("failed to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	taskRepo := database.NewTaskRepository(db, logger)

	encryptor, err := crypto.NewEncryptor(cfg.Crypto.EncryptionKey)
	if err != nil {
		logger.Error("failed to create encryptor", slog.String("error", err.Error()))
		os.Exit(1)
	}

	keyRepo := database.NewAPIKeyRepository(db, encryptor)
	userRepo := database.NewUserRepository(db)
	licRepo := database.NewLicenseRepository(db)

	bybitClient := bybit.NewClient(cfg.BybitTestnet, cfg.Bybit.Timeout)
	rollerService := usecase.NewRollerService(bybitClient, taskRepo, logger)

	marketStream := bybit.NewMarketStream(cfg.BybitTestnet)

	manager := worker.NewManager(taskRepo, keyRepo, rollerService, marketStream, logger)

	tgBot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		logger.Error("failed to init telegram bot", slog.String("error", err.Error()))
		os.Exit(1)
	}

	tgBot.Debug = false
	logger.Info("Telegram bot authorized", slog.String("username", tgBot.Self.UserName))

	botHandler := bot.NewHandler(tgBot, userRepo, keyRepo, taskRepo, licRepo, bybitClient, cfg.Telegram.AdminID, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("Starting bot...",
		slog.String("env", cfg.Env),
		slog.Bool("testnet", cfg.BybitTestnet))

	go manager.Run(ctx)
	go botHandler.Start(ctx)

	<-ctx.Done()
	logger.Info("Bot stopped gracefully")
}