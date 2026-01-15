package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "github.com/joho/godotenv/autoload"
	"github.com/romanzzaa/bybit-options-roller/internal/config" // Импортируем конфиг
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	"github.com/romanzzaa/bybit-options-roller/internal/worker"
)

func main() {
	// 0. Логгер (всегда первый)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 1. Загрузка конфигурации (Используем ваш пакет config!)
	// Это критическое изменение. Мы не читаем os.Getenv здесь.
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// 2. Инфраструктура (Драйверы)
	// Используем конфиг, полученный из пакета config.
    // Обратите внимание: нам нужно маппить config.DatabaseConfig в database.Config,
    // если они в разных пакетах и не идентичны, либо использовать структуру из config везде.
    // В вашем коде internal/infrastructure/database/connection.go ожидает свою структуру Config.
    
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
		// Panic в main допустим при старте, но slog.Error + os.Exit чище.
		logger.Error("failed to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}
    defer db.Close() // Не забывайте закрывать соединения!

	// 3. Репозитории (Адаптеры)
	taskRepo := database.NewTaskRepository(db, logger)
	
	// Используем ключ из конфига
	encryptor, err := crypto.NewEncryptor(cfg.Crypto.EncryptionKey)
	if err != nil {
		logger.Error("failed to create encryptor", slog.String("error", err.Error()))
		os.Exit(1)
	}
	
	keyRepo := database.NewAPIKeyRepository(db, encryptor)

	// 4. Доменная логика (Use Cases)
	// Используем таймауты и настройки из конфига
	bybitClient := bybit.NewClient(cfg.BybitTestnet, cfg.Bybit.Timeout) 
	rollerService := usecase.NewRollerService(bybitClient, taskRepo, logger)

	// 5. Рыночные данные (Infrastructure)
	marketStream := bybit.NewMarketStream(cfg.BybitTestnet)

	// 6. Оркестратор
	manager := worker.NewManager(taskRepo, keyRepo, rollerService, marketStream, logger)

	// 7. Запуск
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("Starting bot...", 
        slog.String("env", cfg.Env), 
        slog.Bool("testnet", cfg.BybitTestnet))
        
	go manager.Run(ctx)

	<-ctx.Done()
	logger.Info("Bot stopped gracefully")
}