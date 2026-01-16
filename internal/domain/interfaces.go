package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type TaskRepository interface {
	CreateTask(ctx context.Context, task *Task) error
	GetTaskByID(ctx context.Context, id int64) (*Task, error)
	GetActiveTasks(ctx context.Context) ([]Task, error)

	UpdateTaskState(ctx context.Context, id int64, newState TaskState, version int64) error
	UpdateTaskSymbol(ctx context.Context, id int64, newSymbol string, newQty decimal.Decimal, version int64) error
	
	SaveError(ctx context.Context, id int64, errMessage string) error
	RegisterError(ctx context.Context, id int64, err error) error
}

type APIKeyRepository interface {
    // БЫЛО: Только GetByID
    GetByID(ctx context.Context, id int64) (*APIKey, error)
    
    // ДОБАВЛЯЕМ (эти методы используются в боте):
    Create(ctx context.Context, apiKey *APIKey) error
    GetActiveByUserID(ctx context.Context, userID int64) (*APIKey, error)
}

// ДОБАВЛЯЕМ НОВЫЙ ИНТЕРФЕЙС (его не было, а бот его использует)
type LicenseRepository interface {
    Generate(ctx context.Context, durationDays int) (*LicenseKey, error)
    Redeem(ctx context.Context, code string, userID int64) error
}

type ExchangeAdapter interface {
	GetIndexPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
	GetMarkPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, creds APIKey, symbol string) (Position, error)
	GetPositions(ctx context.Context, creds APIKey) ([]Position, error) // <--- Убедитесь, что этот тоже тут
	PlaceOrder(ctx context.Context, creds APIKey, req OrderRequest) (string, error)
	GetOptionStrikes(ctx context.Context, baseCoin string, expiryDate string) ([]decimal.Decimal, error)
}

type NotificationService interface {
	NotifyUser(userID int64, message string) error
}

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByTelegramID(ctx context.Context, telegramID int64) (*User, error)
	UpdateSubscription(ctx context.Context, telegramID int64, expiresAt time.Time) error
	IsActive(ctx context.Context, telegramID int64) (bool, error)
}

type MarketProvider interface {
    Subscribe(symbols []string) (<-chan PriceUpdate, error)
    Close() error
}

type MarketStreamer interface {
    Subscribe(symbols []string) (<-chan PriceUpdateEvent, error)
	AddSubscriptions(symbols []string) error
}