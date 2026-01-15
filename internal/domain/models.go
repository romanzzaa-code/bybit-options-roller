package domain

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// --- Constants ---

const (
	SideBuy         = "Buy"
	SideSell        = "Sell"
	OrderTypeMarket = "Market"
	OrderTypeLimit  = "Limit"
)

// --- Types ---

type Side string

// --- State Machine ---

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
	TargetSide   		Side
	CurrentOptionSymbol string
	UnderlyingSymbol    string
	CurrentQty          decimal.Decimal
	TriggerPrice        decimal.Decimal
	NextStrikeStep      decimal.Decimal
	Status              TaskState
	Version             int64
	LastError           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (t *Task) IsCallOption() bool {
	return strings.HasSuffix(t.CurrentOptionSymbol, "-C")
}

func (t *Task) ShouldRoll(currentUnderlyingPrice decimal.Decimal) bool {
	if t.Status != TaskStateIdle {
		return false
	}

	if t.IsCallOption() {
		return currentUnderlyingPrice.GreaterThanOrEqual(t.TriggerPrice)
	} else {
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

// PriceUpdate представляет собой актуальную цену для конкретного базового актива
type PriceUpdate struct {
    Symbol string          // Например, "ETH"
    Price  decimal.Decimal // Индексная цена
    Time   time.Time
}

// PriceUpdateEvent представляет событие обновления цены для MarketStreamer
type PriceUpdateEvent struct {
    Symbol string          // Например, "ETH"
    Price  decimal.Decimal // Индексная цена
    Time   time.Time
    Source string          // Источник данных (например, "bybit-ws")
}