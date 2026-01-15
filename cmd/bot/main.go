package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

    "github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
    "github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
    "github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
    "github.com/romanzzaa/bybit-options-roller/internal/usecase"
    "github.com/romanzzaa/bybit-options-roller/internal/worker"
)

func main() {
	// 0. Логгер (всегда первый)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 1. Инфраструктура (Драйверы)
	dbConfig := database.Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     5432,
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
		DBName:   os.Getenv("DB_NAME"),
		SSLMode:  "disable",
	}

	db, err := database.NewConnection(dbConfig)
	if err != nil {
		panic(err)
	}

	// 2. Репозитории (Адаптеры)
	taskRepo := database.NewTaskRepository(db, logger)
	
	// Создаем encryptor для API ключей
	encryptor, err := crypto.NewEncryptor(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		panic(err)
	}
	
	keyRepo := database.NewAPIKeyRepository(db, encryptor)

	// 3. Доменная логика (Use Cases)
	// Нам нужен клиент Bybit для исполнения ордеров (REST)
	bybitClient := bybit.NewClient(true, 30*time.Second) // true для тестнета, 30 сек timeout
	rollerService := usecase.NewRollerService(bybitClient, taskRepo, logger)

	// 4. Рыночные данные (Infrastructure)
	// Создаем наш новый MarketStream (WebSocket)
	marketStream := bybit.NewMarketStream(true) // true для тестнета

	// 5. Оркестратор (Worker Manager)
	// Теперь передаем ВСЁ необходимое
	manager := worker.NewManager(taskRepo, keyRepo, rollerService, marketStream, logger)

	// 6. Запуск
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Запускаем менеджер (Run вместо Start)
	go manager.Run(ctx)

	// Ждем сигнал остановки
	<-ctx.Done()
	logger.Info("Bot stopped gracefully")
}