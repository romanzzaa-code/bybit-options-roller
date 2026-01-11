package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type TaskState string

const (
	TaskStatusActive   TaskState = "active"
	TaskStatusPaused   TaskState = "paused"
	TaskStatusError    TaskState = "error"
	TaskStatusDisabled TaskState = "disabled"
)

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

type Task struct {
	ID              int64
	UserID          int64
	APIKeyID        int64
	TargetSymbol    string
	CurrentQty      decimal.Decimal
	TriggerPrice    decimal.Decimal
	NextStrikeStep  decimal.Decimal
	Status          TaskState
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Position struct {
	Symbol          string
	Side            string
	Qty             decimal.Decimal
	EntryPrice      decimal.Decimal
	MarkPrice       decimal.Decimal
	UnrealizedPnL   decimal.Decimal
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