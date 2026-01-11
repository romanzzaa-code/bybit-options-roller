package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// TaskRepository — методы работы с задачами в БД
type TaskRepository interface {
	CreateTask(ctx context.Context, task *Task) error
	GetTaskByID(ctx context.Context, id int64) (*Task, error)
	GetActiveTasks(ctx context.Context) ([]Task, error)

	// UpdateTaskState — атомарный переход состояния
	UpdateTaskState(ctx context.Context, id int64, newState TaskState, version int64) error
	
	// UpdateTaskSymbol — обновление тикера после ролла
	UpdateTaskSymbol(ctx context.Context, id int64, newSymbol string, newQty decimal.Decimal, version int64) error
	
	SaveError(ctx context.Context, id int64, errMessage string) error
}

// APIKeyRepository — методы работы с ключами
type APIKeyRepository interface {
	GetByID(ctx context.Context, id int64) (*APIKey, error)
}

// ExchangeAdapter — методы работы с биржей
type ExchangeAdapter interface {
	// GetIndexPrice — цена базового актива (BTC), а не опциона
	GetIndexPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
	
	// GetMarkPrice — цена опциона
	GetMarkPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
	
	GetPosition(ctx context.Context, creds APIKey, symbol string) (Position, error)
	PlaceOrder(ctx context.Context, creds APIKey, req OrderRequest) (string, error)
}

// NotificationService — уведомления
type NotificationService interface {
	NotifyUser(userID int64, message string) error
}

// UserRepository — методы работы с юзерами
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByTelegramID(ctx context.Context, telegramID int64) (*User, error)
	UpdateSubscription(ctx context.Context, telegramID int64, expiresAt time.Time) error
	IsActive(ctx context.Context, telegramID int64) (bool, error)
}