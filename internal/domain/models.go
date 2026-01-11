package domain

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// --- State Machine ---

type TaskState string

const (
	TaskStateIdle          TaskState = "IDLE"
	TaskStateRollInitiated TaskState = "ROLL_INITIATED"
	TaskStateLeg1Closed    TaskState = "LEG1_CLOSED"
	TaskStateLeg2Opening   TaskState = "LEG2_OPENING"
	TaskStateCompleted     TaskState = "COMPLETED"
	TaskStateFailed        TaskState = "FAILED"
)

// --- Aggregates ---

type Task struct {
	ID                  int64
	UserID              int64
	APIKeyID            int64
	CurrentOptionSymbol string
	UnderlyingSymbol    string // НОВОЕ ПОЛЕ
	CurrentQty          decimal.Decimal
	TriggerPrice        decimal.Decimal
	NextStrikeStep      decimal.Decimal
	Status              TaskState
	Version             int64 // НОВОЕ ПОЛЕ
	LastError           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// IsCallOption определяет тип опциона
func (t *Task) IsCallOption() bool {
	return strings.HasSuffix(t.CurrentOptionSymbol, "-C")
}

// ShouldRoll проверяет условие роллирования
func (t *Task) ShouldRoll(currentUnderlyingPrice decimal.Decimal) bool {
	if t.Status != TaskStateIdle {
		return false
	}

	if t.IsCallOption() {
		// Call: Если цена БА >= триггер
		return currentUnderlyingPrice.GreaterThanOrEqual(t.TriggerPrice)
	} else {
		// Put: Если цена БА <= триггер
		return currentUnderlyingPrice.LessThanOrEqual(t.TriggerPrice)
	}
}

// --- Entities & Value Objects ---

type User struct {
	ID         int64
	TelegramID int64
	Username   string
	ExpiresAt  time.Time
	IsBanned   bool
	CreatedAt  time.Time
}

type APIKey struct {
	ID        int64
	UserID    int64
	Key       string
	Secret    string
	Label     string
	IsValid   bool
	CreatedAt time.Time
}

type Position struct {
	Symbol        string
	Side          string
	Qty           decimal.Decimal
	EntryPrice    decimal.Decimal
	MarkPrice     decimal.Decimal
	UnrealizedPnL decimal.Decimal
}

type MarginInfo struct {
	TotalEquity        decimal.Decimal
	TotalMarginBalance decimal.Decimal
	MMR                decimal.Decimal
}

type OrderRequest struct {
	Symbol      string
	Side        string
	OrderType   string
	Qty         decimal.Decimal
	Price       decimal.Decimal
	ReduceOnly  bool
	OrderLinkID string
}