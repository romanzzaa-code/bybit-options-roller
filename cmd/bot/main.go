package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	"github.com/shopspring/decimal"
)

func main() {
	// 1. Инициализация контекста с graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("🛑 Shutting down...")
		cancel()
	}()

	// 2. Конфиг
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	// 3. База данных
	db, err := database.NewConnection(database.Config{
		Host: cfg.Database.Host, Port: cfg.Database.Port, User: cfg.Database.User,
		Password: cfg.Database.Password, DBName: cfg.Database.DBName, SSLMode: cfg.Database.SSLMode,
	})
	if err != nil {
		log.Fatalf("DB Connection error: %v", err)
	}
	defer db.Close()

	// 4. Криптография
	var encryptor *crypto.Encryptor
	if cfg.Crypto.EncryptionKey != "" {
		encryptor, err = crypto.NewEncryptor(cfg.Crypto.EncryptionKey)
		if err != nil {
			log.Fatalf("Crypto init error: %v", err)
		}
	}

	// 5. Репозитории и Сервисы
	taskRepo := database.NewTaskRepository(db, encryptor)
	// apiKeyRepo := database.NewAPIKeyRepository(db, encryptor) // Пока не используем
	
	bybitClient := bybit.NewClient(cfg.BybitTestnet)
	
	// Внедряем зависимости в UseCase
	rollerService := usecase.NewRollerService(bybitClient, taskRepo)
	
	// ВРЕМЕННО: Используем заглушку, чтобы компилятор не ругался на неиспользуемую переменную.
	// В следующем шаге мы передадим rollerService в Event Loop.
	_ = rollerService 

	log.Println("✅ System initialized successfully. Ready for Event Loop.")

	// ВРЕМЕННЫЙ ТЕСТ: Создаем задачу, если база пуста
	if cfg.Env == "local" {
		createTestTask(ctx, taskRepo)
	}

	// Блокируем Main до получения сигнала выхода
	<-ctx.Done()
	log.Println("Bye!")
}

func createTestTask(ctx context.Context, repo domain.TaskRepository) {
	tasks, _ := repo.GetActiveTasks(ctx)
	if len(tasks) > 0 {
		log.Printf("[Test] Found %d active tasks in DB. Skipping seed.", len(tasks))
		return
	}

	log.Println("[Test] Seeding DB with a test task...")
	newTask := &domain.Task{
		UserID:           1, 
		APIKeyID:         1, 
		CurrentOptionSymbol: "BTC-29DEC23-40000-C",
		UnderlyingSymbol:    "BTC",
		TriggerPrice:        decimal.NewFromInt(42000), 
		NextStrikeStep:      decimal.NewFromInt(1000),
		CurrentQty:          decimal.NewFromFloat(0.1),
		Status:              domain.TaskStateIdle,
	}

	if err := repo.CreateTask(ctx, newTask); err != nil {
		log.Printf("⚠️ Failed to create test task (did you run migrations?): %v", err)
	} else {
		log.Printf("✅ Test task created! ID: %d", newTask.ID)
	}
}