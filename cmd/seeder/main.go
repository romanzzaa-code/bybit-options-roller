package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/shopspring/decimal"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	if cfg.Env != "local" {
		log.Fatal("Seeder allowed only in local environment")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	db, err := database.NewConnection(database.Config{
		Host: cfg.Database.Host, Port: cfg.Database.Port, User: cfg.Database.User,
		Password: cfg.Database.Password, DBName: cfg.Database.DBName, SSLMode: cfg.Database.SSLMode,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	repo := database.NewTaskRepository(db, logger)
	
	// Создание тестовых данных
	createTestTask(context.Background(), repo)
}

func createTestTask(ctx context.Context, repo domain.TaskRepository) {
	tasks, _ := repo.GetActiveTasks(ctx)
	if len(tasks) > 0 {
		log.Printf("[Seeder] Found %d active tasks. Skipping.", len(tasks))
		return
	}

	log.Println("[Seeder] Creating test task...")
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
		log.Printf("⚠️ Failed: %v", err)
	} else {
		log.Printf("✅ Task created! ID: %d", newTask.ID)
	}
}