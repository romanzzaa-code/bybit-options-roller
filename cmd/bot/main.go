package main

import (
	"context"
	"log"

	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	"github.com/shopspring/decimal"
)

func main() {
	// 1. Загрузка конфига
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. Инициализация (Infrastructure)
	bybitClient := bybit.NewClient(cfg.BybitTestnet)
	
	// 3. Инициализация (Use Case)
	roller := usecase.NewRollerService(bybitClient)

	// 4. ТЕСТОВЫЙ ПРОГОН (без БД)
	// Создаем фейковую задачу в памяти
	testTask := &domain.Task{
		ID:           1,
		TargetSymbol: "BTC-29DEC23-50000-C", // Вставь сюда реальный тикер с Testnet!
		TriggerPrice: decimal.NewFromInt(40000), // Ставим низкий триггер, чтобы сработал (> 40k)
		NextStrikeStep: decimal.NewFromInt(1000),
	}
	
	// Фейковые ключи (они не сработают, но мы увидим попытку)
	testKeys := domain.APIKey{
		Key:       "TEST_KEY",
		SecretEnc: "TEST_SECRET",
	}

	log.Println("--- STARTING MANUAL TEST ---")
	err = roller.ExecuteRoll(context.Background(), testKeys, testTask)
	if err != nil {
		// Мы ожидаем ошибку, так как тикер старый или ключи неверные
		log.Printf("Test finished with expected error: %v", err)
	} else {
		log.Println("Test finished successfully!")
	}
}