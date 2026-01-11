package domain

import (
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

// OptionSymbol — Value Object для безопасной работы с тикерами Bybit
type OptionSymbol struct {
	Underlying string          // BTC
	Date       string          // 29DEC23
	Strike     decimal.Decimal // 40000
	Type       string          // C or P
}

// ParseOptionSymbol разбирает строку в структуру
func ParseOptionSymbol(raw string) (OptionSymbol, error) {
	re := regexp.MustCompile(`^([A-Z]+)-(\d{1,2}[A-Z]{3}\d{2})-(\d+)-([CP])$`)
	matches := re.FindStringSubmatch(raw)

	if len(matches) != 5 {
		return OptionSymbol{}, fmt.Errorf("invalid symbol format: %s", raw)
	}

	strike, err := decimal.NewFromString(matches[3])
	if err != nil {
		return OptionSymbol{}, fmt.Errorf("invalid strike number: %s", matches[3])
	}

	return OptionSymbol{
		Underlying: matches[1],
		Date:       matches[2],
		Strike:     strike,
		Type:       matches[4],
	}, nil
}

// String собирает тикер обратно
func (s OptionSymbol) String() string {
	return fmt.Sprintf("%s-%s-%s-%s", s.Underlying, s.Date, s.Strike.String(), s.Type)
}

// NextStrike рассчитывает новый тикер
func (s OptionSymbol) NextStrike(step decimal.Decimal) OptionSymbol {
	newSym := s
	if s.Type == "C" {
		newSym.Strike = s.Strike.Add(step)
	} else {
		newSym.Strike = s.Strike.Sub(step)
	}
	return newSym
}