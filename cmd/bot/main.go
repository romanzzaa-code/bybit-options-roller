package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/bybit"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	"github.com/shopspring/decimal"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("[Main] Received shutdown signal")
		cancel()
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("[Main] Running in %s mode", cfg.Env)

	db, err := database.NewConnection(database.Config{
		Host:     cfg.Database.Host,
		Port:     cfg.Database.Port,
		User:     cfg.Database.User,
		Password: cfg.Database.Password,
		DBName:   cfg.Database.DBName,
		SSLMode:  cfg.Database.SSLMode,
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("[Main] Connected to PostgreSQL")

	var encryptor *crypto.Encryptor
	if cfg.Crypto.EncryptionKey != "" {
		encryptor, err = crypto.NewEncryptor(cfg.Crypto.EncryptionKey)
		if err != nil {
			log.Fatalf("Failed to initialize encryptor: %v", err)
		}
		log.Println("[Main] Encryption enabled")
	} else {
		log.Println("[Main] WARNING: Encryption key not set, API keys will not be encrypted")
	}

	taskRepo := database.NewTaskRepository(db, encryptor)
	apiKeyRepo := database.NewAPIKeyRepository(db, encryptor)
	userRepo := database.NewUserRepository(db)

	bybitClient := bybit.NewClient(cfg.BybitTestnet)
	roller := usecase.NewRollerService(bybitClient)

	if cfg.Env == "local" {
		runLocalTest(ctx, roller, taskRepo)
		return
	}

	runProduction(ctx, roller, taskRepo, apiKeyRepo, userRepo)
}

func runLocalTest(ctx context.Context, roller *usecase.RollerService, taskRepo *database.TaskRepository) {
	log.Println("[Test] Running local test mode")

	testTask := &domain.Task{
		ID:              1,
		UserID:          1,
		APIKeyID:        1,
		TargetSymbol:    "BTC-29DEC23-50000-C",
		TriggerPrice:    decimal.NewFromInt(40000),
		NextStrikeStep:  decimal.NewFromInt(1000),
		CurrentQty:      decimal.NewFromFloat(0.001),
		Status:          domain.TaskStatusActive,
	}

	if err := taskRepo.CreateTask(ctx, testTask); err != nil {
		log.Printf("[Test] Failed to create task: %v", err)
	}

	tasks, err := taskRepo.GetActiveTasks(ctx)
	if err != nil {
		log.Printf("[Test] Failed to get tasks: %v", err)
		return
	}

	log.Printf("[Test] Found %d active tasks", len(tasks))

	testKeys := domain.APIKey{
		ID:     1,
		Key:    "TEST_KEY",
		Secret: "TEST_SECRET",
	}

	for _, task := range tasks {
		log.Printf("[Test] Executing roll for %s", task.TargetSymbol)
		err = roller.ExecuteRoll(ctx, testKeys, &task)
		if err != nil {
			log.Printf("[Test] Roll finished with error: %v", err)
		} else {
			log.Println("[Test] Roll finished successfully!")
		}
	}
}

func runProduction(ctx context.Context, roller *usecase.RollerService, taskRepo *database.TaskRepository, apiKeyRepo *database.APIKeyRepository, userRepo *database.UserRepository) {
	log.Println("[Main] Starting production mode")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[Main] Shutting down...")
			return
		case <-ticker.C:
			if err := processTasks(ctx, roller, taskRepo, apiKeyRepo); err != nil {
				log.Printf("[Main] Error processing tasks: %v", err)
			}
		}
	}
}

func processTasks(ctx context.Context, roller *usecase.RollerService, taskRepo *database.TaskRepository, apiKeyRepo *database.APIKeyRepository) error {
	tasks, err := taskRepo.GetActiveTasks(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active tasks: %w", err)
	}

	for _, task := range tasks {
		apiKey, err := apiKeyRepo.GetByID(ctx, task.APIKeyID)
		if err != nil {
			log.Printf("[Roller] Failed to get API key for task %d: %v", task.ID, err)
			taskRepo.UpdateTaskStatus(ctx, task.ID, domain.TaskStatusError, "API key fetch failed")
			continue
		}

		if apiKey == nil || !apiKey.IsValid {
			log.Printf("[Roller] Invalid API key for task %d", task.ID)
			taskRepo.UpdateTaskStatus(ctx, task.ID, domain.TaskStatusError, "Invalid API key")
			continue
		}

		err = roller.ExecuteRoll(ctx, *apiKey, &task)
		if err != nil {
			log.Printf("[Roller] Roll failed for task %d: %v", task.ID, err)
			taskRepo.UpdateTaskStatus(ctx, task.ID, domain.TaskStatusError, err.Error())
			continue
		}

		log.Printf("[Roller] Successfully rolled task %d", task.ID)
	}

	return nil
}