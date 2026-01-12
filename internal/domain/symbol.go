package domain

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Symbol - типизированная строка для тикеров
type Symbol string

func (s Symbol) String() string {
	return string(s)
}

func (s Symbol) GetIndexTicker() string {
	target := string(s)
	if !strings.HasSuffix(target, "USDT") && !strings.HasSuffix(target, "USD") {
		return target + "USDT"
	}
	return target
}

// OptionSymbol помогает разбирать и модифицировать тикеры опционов
// Пример: BTC-29DEC23-30000-C
type OptionSymbol struct {
	Raw        string
	Underlying string
	Date       string
	Strike     decimal.Decimal
	Type       string // C or P
}

func ParseOptionSymbol(raw string) (OptionSymbol, error) {
	parts := strings.Split(raw, "-")
	if len(parts) != 4 {
		return OptionSymbol{}, fmt.Errorf("invalid option symbol format: %s", raw)
	}

	strike, err := decimal.NewFromString(parts[2])
	if err != nil {
		return OptionSymbol{}, fmt.Errorf("invalid strike price: %s", parts[2])
	}

	return OptionSymbol{
		Raw:        raw,
		Underlying: parts[0],
		Date:       parts[1],
		Strike:     strike,
		Type:       parts[3],
	}, nil
}

// NextStrike возвращает новый символ с измененным страйком
func (os OptionSymbol) NextStrike(step decimal.Decimal) Symbol {
	// Для примера просто добавляем шаг.
	// В реальной логике нужно учитывать Call/Put (для Put возможно вычитание)
	// и направление роллирования, но здесь реализуем механику из roller.go
	newStrike := os.Strike.Add(step)
	
	newSymbol := fmt.Sprintf("%s-%s-%s-%s", os.Underlying, os.Date, newStrike.String(), os.Type)
	return Symbol(newSymbol)
}