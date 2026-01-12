package domain

import "strings"

// Symbol - типизированная строка для тикеров
type Symbol string

func (s Symbol) String() string {
	return string(s)
}

// GetIndexTicker возвращает тикер для получения индекса цены.
// Логика: если это просто "BTC", для фьючерсов Bybit нужно добавить "USDT".
func (s Symbol) GetIndexTicker() string {
	target := string(s)
	if !strings.HasSuffix(target, "USDT") && !strings.HasSuffix(target, "USD") {
		return target + "USDT"
	}
	return target
}