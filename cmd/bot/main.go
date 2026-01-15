package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"bybit-options-roller/internal/infrastructure/bybit"
	"bybit-options-roller/internal/infrastructure/database"
	"bybit-options-roller/internal/usecase"
	"bybit-options-roller/internal/worker"
)

func main() {
	// 0. Логгер (всегда первый)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 1. Инфраструктура (Драйверы)
	db, err := database.NewConnection() // Твой метод подключения к БД
	if err != nil {
		panic(err)
	}

	// 2. Репозитории (Адаптеры)
	taskRepo := database.NewRepository(db)
	keyRepo := database.NewAPIKeyRepository(db) // Реализуй его, если еще нет

	// 3. Доменная логика (Use Cases)
	// Нам нужен клиент Bybit для исполнения ордеров (REST)
	bybitClient := bybit.NewClient(os.Getenv("BYBIT_API_KEY"), os.Getenv("BYBIT_API_SECRET"), true)
	rollerService := usecase.NewRollerService(taskRepo, bybitClient, logger)

	// 4. Рыночные данные (Infrastructure)
	// Создаем наш новый MarketStream (WebSocket)
	marketStream := bybit.NewMarketStream(true) // true для тестнета

	// 5. Оркестратор (Worker Manager)
	// Теперь передаем ВСЁ необходимое
	manager := worker.NewManager(taskRepo, keyRepo, rollerService, marketStream, logger)

	// 6. Запуск
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		logger.Error("Manager stopped with error", "err", err)
	}
}