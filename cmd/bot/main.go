package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	"github.com/romanzzaa/bybit-options-roller/internal/worker"
)

func main() {
	// 1. Setup Logger (Шаг 1)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger) // Глобальный логгер

	// 2. Load Config
	cfg := config.MustLoad()
	logger.Info("Config loaded", slog.String("env", cfg.Env))

	// 3. Infrastructure
	dbConn, err := database.NewConnection(cfg.Database)
	if err != nil {
		logger.Error("DB connection failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer dbConn.Close()

	var encryptor *crypto.Encryptor
	if cfg.Crypto.EncryptionKey != "" {
		encryptor, err = crypto.NewEncryptor(cfg.Crypto.EncryptionKey)
		if err != nil {
			panic(err)
		}
	}

	// Repositories
	taskRepo := database.NewTaskRepository(dbConn, logger)
	apiKeyRepo := database.NewAPIKeyRepository(dbConn, encryptor)

	// Clients (Шаг 2: передаем таймаут)
	bybitClient := bybit.NewClient(cfg.BybitTestnet, cfg.Bybit.Timeout)

	// 4. UseCase
	rollerService := usecase.NewRollerService(bybitClient, taskRepo, logger)

	// 5. Worker Manager (Шаг 4)
	manager := worker.NewManager(
		rollerService, 
		taskRepo, 
		apiKeyRepo, 
		logger, 
		5*time.Second, // Interval check
	)

	// 6. Graceful Shutdown & Run
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Запуск воркера в отдельной горутине, чтобы не блокировать main для обработки сигналов
	go func() {
		manager.Run(ctx)
	}()

	logger.Info("Bot started")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop

	logger.Info("Shutting down initiated...")
	cancel() // Сигнал отмены контекста (воркер начнет завершаться)

	// Даем время на завершение активных операций (waitGroup в менеджере делает основную работу, 
	// но здесь жесткий таймаут для процесса)
	time.Sleep(2 * time.Second) 
	logger.Info("Bye")
}