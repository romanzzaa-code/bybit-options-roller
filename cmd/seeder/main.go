package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/config"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/shopspring/decimal"
    // Не забудьте импорт драйвера, если он не импортирован внутри database
    _ "github.com/lib/pq" 
    _ "github.com/joho/godotenv/autoload"
)

func main() {
	// 1. Config
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	if cfg.Env != "local" {
		log.Fatal("Seeder allowed only in local environment")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Database
	db, err := database.NewConnection(database.Config{
		Host: cfg.Database.Host, Port: cfg.Database.Port, User: cfg.Database.User,
		Password: cfg.Database.Password, DBName: cfg.Database.DBName, SSLMode: cfg.Database.SSLMode,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 3. Encryptor (Нужен для создания API Key)
	encryptor, err := crypto.NewEncryptor(cfg.Crypto.EncryptionKey)
	if err != nil {
		log.Fatalf("Encryptor init failed: %v", err)
	}

	// 4. Repositories
	userRepo := database.NewUserRepository(db)
	keyRepo := database.NewAPIKeyRepository(db, encryptor)
	taskRepo := database.NewTaskRepository(db, logger)

	ctx := context.Background()

	// --- ШАГ 1: Создаем Пользователя ---
	// Проверяем, есть ли уже пользователи, чтобы не дублировать
    // В реальном сидере лучше проверять по TelegramID
	
    // Создаем пользователя с TelegramID = 12345 (тестовый)
    user := &domain.User{
        TelegramID: 12345,
        Username:   "test_trader",
        ExpiresAt:  time.Now().Add(365 * 24 * time.Hour), // Подписка на год
        IsBanned:   false,
    }

    // Пытаемся найти или создать
    existingUser, _ := userRepo.GetByTelegramID(ctx, user.TelegramID)
    if existingUser != nil {
        log.Printf("[Seeder] User already exists (ID: %d). Using him.", existingUser.ID)
        user = existingUser
    } else {
        if err := userRepo.Create(ctx, user); err != nil {
            log.Fatalf("Failed to create user: %v", err)
        }
        log.Printf("✅ User created! ID: %d", user.ID)
    }

	// --- ШАГ 2: Создаем API Key ---
    // ВАЖНО: Тут должны быть валидные ключи от Testnet Bybit, 
    // если вы хотите, чтобы бот реально торговал.
    // Если просто тестируете запуск - можно фейковые.
	apiKey := &domain.APIKey{
		UserID:  user.ID,
		Key:     "YOUR_TESTNET_API_KEY",    // <--- Замените на реальный ключ тестнета
		Secret:  "YOUR_TESTNET_API_SECRET", // <--- Замените на реальный секрет
		Label:   "Auto-Seeded Key",
		IsValid: true,
	}
    
    // Упрощение: просто создаем новый ключ. В идеале надо проверять наличие.
	if err := keyRepo.Create(ctx, apiKey); err != nil {
		log.Fatalf("Failed to create api key: %v", err)
	}
	log.Printf("✅ API Key created! ID: %d", apiKey.ID)

	// --- ШАГ 3: Создаем Задачу ---
    // Проверяем, нет ли уже задач
	tasks, _ := taskRepo.GetActiveTasks(ctx)
	if len(tasks) > 0 {
		log.Printf("[Seeder] Found %d active tasks. Skipping creation.", len(tasks))
		return
	}

	log.Println("[Seeder] Creating test task...")
    
    // Используем дату в будущем, чтобы опцион существовал
	newTask := &domain.Task{
		UserID:           user.ID,     // Ссылка на созданного юзера
		APIKeyID:         apiKey.ID,   // Ссылка на созданный ключ
		CurrentOptionSymbol: "BTC-26DEC26-100000-C", // Пример символа
		UnderlyingSymbol:    "BTCUSDT",
		TriggerPrice:        decimal.NewFromInt(150000), 
		NextStrikeStep:      decimal.NewFromInt(1000),
		CurrentQty:          decimal.NewFromFloat(0.1),
		Status:              domain.TaskStateIdle,
	}

	if err := taskRepo.CreateTask(ctx, newTask); err != nil {
		log.Printf("⚠️ Failed to create task: %v", err)
	} else {
		log.Printf("✅ Task created! ID: %d", newTask.ID)
	}
}