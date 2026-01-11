package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type TaskRepository interface {
	CreateTask(ctx context.Context, task *Task) error
	GetTaskByID(ctx context.Context, id int64) (*Task, error)
	GetTasksBySymbol(ctx context.Context, symbol string) ([]Task, error)
	GetActiveTasks(ctx context.Context) ([]Task, error)
	UpdateTaskStatus(ctx context.Context, id int64, status TaskState, errMessage string) error
	UpdateTaskSymbol(ctx context.Context, id int64, newSymbol string, newQty decimal.Decimal) error
}

type APIKeyRepository interface {
	Create(ctx context.Context, apiKey *APIKey) error
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	GetByUserID(ctx context.Context, userID int64) ([]APIKey, error)
	Invalidate(ctx context.Context, id int64) error
}

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByTelegramID(ctx context.Context, telegramID int64) (*User, error)
	UpdateSubscription(ctx context.Context, telegramID int64, expiresAt time.Time) error
	IsActive(ctx context.Context, telegramID int64) (bool, error)
}

type ExchangeAdapter interface {
	GetMarkPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
	GetPosition(ctx context.Context, creds APIKey, symbol string) (Position, error)
	PlaceOrder(ctx context.Context, creds APIKey, req OrderRequest) (string, error)
	GetMarginInfo(ctx context.Context, creds APIKey) (MarginInfo, error)
}

type NotificationService interface {
	NotifyUser(userID int64, message string) error
	NotifyAdmin(message string) error
}