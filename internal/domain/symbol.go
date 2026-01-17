package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// OptionSymbol - разобранная структура тикера
type OptionSymbol struct {
	Original string
	BaseCoin string          // BTC
	Expiry   string          // 30JAN24
	Strike   decimal.Decimal // 2200
	Side     string          // C or P
}

// ParseOptionSymbol разбирает строку вида "ETH-30JAN24-2200-C"
func ParseOptionSymbol(symbol string) (OptionSymbol, error) {
	parts := strings.Split(symbol, "-")
    // ИЗМЕНЕНО: Разрешаем 4 или 5 частей
	if len(parts) < 4 {
		return OptionSymbol{}, fmt.Errorf("invalid symbol format: %s", symbol)
	}

	// Strike всегда на 3-й позиции (индекс 2)
	strike, err := decimal.NewFromString(parts[2])
	if err != nil {
		return OptionSymbol{}, fmt.Errorf("invalid strike: %s", parts[2])
	}

	return OptionSymbol{
		Original: symbol,
		BaseCoin: parts[0], // Всегда берем первую часть (ETH, BTC)
		Expiry:   parts[1],
		Strike:   strike,
		Side:     parts[3],
	}, nil
}

// ParseExpirationFromSymbol - оставляет старую логику для совместимости
func ParseExpirationFromSymbol(symbol string) (time.Time, error) {
	os, err := ParseOptionSymbol(symbol)
	if err != nil {
		return time.Time{}, err
	}
	// Layout: 02Jan06
	t, err := time.Parse("02Jan06", os.Expiry)
	if err != nil {
		return time.Time{}, err
	}
	return t.Add(8 * time.Hour), nil
}

// FindNextStrike выбирает следующий страйк из доступного списка
// strikesList должен быть списком ВСЕХ доступных страйков для этой даты
func (os OptionSymbol) FindNextStrike(strikesList []decimal.Decimal) (string, error) {
	// 1. Сортируем список (на всякий случай)
	sort.Slice(strikesList, func(i, j int) bool {
		return strikesList[i].LessThan(strikesList[j])
	})

	// 2. Ищем текущий индекс
	currentIndex := -1
	for i, s := range strikesList {
		if s.Equal(os.Strike) {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		// Текущего страйка нет в списке? (Странно, но может быть если он только что исчез)
		// Ищем ближайший сверху
		for _, s := range strikesList {
			if s.GreaterThan(os.Strike) {
				return fmt.Sprintf("%s-%s-%s-%s", os.BaseCoin, os.Expiry, s.String(), os.Side), nil
			}
		}
		return "", fmt.Errorf("no higher strike available for %s", os.Original)
	}

	// 3. Берем следующий
	if currentIndex+1 >= len(strikesList) {
		return "", fmt.Errorf("already at highest strike")
	}

	nextStrike := strikesList[currentIndex+1]
	
	// Собираем тикер обратно: ETH-30JAN24-2400-C
	return fmt.Sprintf("%s-%s-%s-%s", os.BaseCoin, os.Expiry, nextStrike.String(), os.Side), nil
}