package domain

import "time"

// MarketEvent - маркерный интерфейс
type MarketEvent interface {
    GetSymbol() string
    GetTime() time.Time
}

// PriceUpdateEvent - событие изменения цены, которое триггерит проверку
type PriceUpdateEvent struct {
    Symbol    string
    Price     float64
    Timestamp time.Time
}

func (e PriceUpdateEvent) GetSymbol() string { return e.Symbol }
func (e PriceUpdateEvent) GetTime() time.Time { return e.Timestamp }