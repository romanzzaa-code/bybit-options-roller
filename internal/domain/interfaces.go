package domain

import (
	"context"

	"github.com/shopspring/decimal"
)

// TaskRepository - управление задачами в БД
type TaskRepository interface {
	// Получить задачи, которые нужно проверить для данного символа (быстрый поиск)
	GetTasksBySymbol(ctx context.Context, symbol string) ([]Task, error)
	
	// Получить задачу по ID с блокировкой (для транзакций)
	GetTaskByID(ctx context.Context, id int64) (*Task, error)
	
	// Обновить статус задачи
	UpdateTaskStatus(ctx context.Context, id int64, status TaskState, errMessage string) error
	
	// Сохранить новую задачу
	CreateTask(ctx context.Context, task *Task) error
}

// ExchangeAdapter - адаптер к бирже (Bybit V5)
type ExchangeAdapter interface {
	// Получить текущую цену (Mark Price)
	GetMarkPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
	
	// Получить позицию пользователя. Если позиции нет, вернуть пустую структуру, но не ошибку.
	GetPosition(ctx context.Context, creds APIKey, symbol string) (Position, error)
	
	// Отправить ордер
	PlaceOrder(ctx context.Context, creds APIKey, req OrderRequest) (string, error)
	
	// Проверить маржу (для RiskEngine)
	GetMarginInfo(ctx context.Context, creds APIKey) (MarginInfo, error)
}

// NotificationService - уведомления в Telegram
type NotificationService interface {
	NotifyUser(userID int64, message string) error
	NotifyAdmin(message string) error
}