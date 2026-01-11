package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// --- Enums & Constants ---

type TaskState string

const (
	TaskStateActive       TaskState = "ACTIVE"
	TaskStateRollInit     TaskState = "ROLL_INITIATED" // Цена достигла триггера
	TaskStateLeg1Closing  TaskState = "LEG1_CLOSING"   // Закрываем старую позицию
	TaskStateLeg1Closed   TaskState = "LEG1_CLOSED"    // Старая закрыта успешно
	TaskStateLeg2Opening  TaskState = "LEG2_OPENING"   // Открываем новую
	TaskStateCompleted    TaskState = "COMPLETED"      // Успех, ждем новый триггер
	TaskStateFailed       TaskState = "FAILED"         // Ошибка (сеть, API)
	TaskStateFatal        TaskState = "FATAL"          // Критическая ошибка (маржа)
	TaskStatePaused       TaskState = "PAUSED"
)

// --- Entities (Сущности БД) ---

// User - пользователь бота
type User struct {
	ID         int64
	TelegramID int64
	Username   string
	ExpiresAt  time.Time // До какого числа оплачена подписка
	IsBanned   bool
	CreatedAt  time.Time
}

// APIKey - ключи от Bybit (шифруются в базе)
type APIKey struct {
	ID           int64
	UserID       int64
	Key          string
	SecretEnc    string // Зашифрованный секрет
	Label        string
	IsValid      bool
}

// Task - задача на роллирование (WatchTask)
type Task struct {
	ID             int64
	UserID         int64
	APIKeyID       int64
	
	// Что роллируем
	TargetSymbol   string          // Например "ETH-30JAN26-3400-C"
	CurrentQty     decimal.Decimal // Размер позиции
	
	// Параметры стратегии
	TriggerPrice   decimal.Decimal // Цена БА, при которой роллируем
	NextStrikeStep decimal.Decimal // Шаг страйка (+100, +500)
	
	// Состояние (State Machine)
	Status         TaskState
	LastError      string
	
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// --- Value Objects (Объекты-значения для логики) ---

// Position - текущая позиция на бирже
type Position struct {
	Symbol      string
	Side        string // "Buy" или "Sell"
	Qty         decimal.Decimal
	EntryPrice  decimal.Decimal
	MarkPrice   decimal.Decimal
	UnrealizedPnL decimal.Decimal
}

// MarginInfo - данные о здоровье аккаунта
type MarginInfo struct {
	TotalEquity        decimal.Decimal
	TotalMarginBalance decimal.Decimal
	MMR                decimal.Decimal // Maintenance Margin Rate (должно быть < 100%)
}

// OrderRequest - DTO для отправки ордера
type OrderRequest struct {
	Symbol      string
	Side        string // "Buy", "Sell"
	OrderType   string // "Market", "Limit"
	Qty         decimal.Decimal
	Price       decimal.Decimal // Опционально для Market
	ReduceOnly  bool
	OrderLinkID string // Idempotency key
}