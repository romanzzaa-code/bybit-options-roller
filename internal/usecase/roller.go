package usecase

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/shopspring/decimal"
)

type RollerService struct {
	exchange domain.ExchangeAdapter
}

func NewRollerService(exchange domain.ExchangeAdapter) *RollerService {
	return &RollerService{
		exchange: exchange,
	}
}

// ExecuteRoll - основная бизнес-логика.
// Принимает задачу и ключи, проверяет условия и выполняет роллирование.
func (s *RollerService) ExecuteRoll(ctx context.Context, apiKey domain.APIKey, task *domain.Task) error {
	log.Printf("[Roller] Checking task for %s. Trigger: %s", task.TargetSymbol, task.TriggerPrice)

	// 1. Получаем текущую цену (Mark Price)
	markPrice, err := s.exchange.GetMarkPrice(ctx, task.TargetSymbol)
	if err != nil {
		return fmt.Errorf("failed to get mark price: %w", err)
	}

	// 2. Проверяем условие (Триггер)
	isCall := strings.HasSuffix(task.TargetSymbol, "-C")
	shouldRoll := false

	if isCall {
		// Для Call: если цена >= триггера (рынок вырос против нас)
		if markPrice.GreaterThanOrEqual(task.TriggerPrice) {
			shouldRoll = true
		}
	} else {
		// Для Put: если цена <= триггера (рынок упал против нас)
		if markPrice.LessThanOrEqual(task.TriggerPrice) {
			shouldRoll = true
		}
	}

	if !shouldRoll {
		log.Printf("[Roller] Price %s is safe (Trigger %s). No action.", markPrice, task.TriggerPrice)
		return nil
	}

	log.Printf("🚨 TRIGGER HIT! %s MarkPrice: %s. Initiating ROLL sequence...", task.TargetSymbol, markPrice)

	// --- НАЧАЛО ТРАНЗАКЦИИ РОЛЛИРОВАНИЯ ---

	// Шаг 0: Получаем реальный размер позиции, чтобы не закрыть лишнего
	position, err := s.exchange.GetPosition(ctx, apiKey, task.TargetSymbol)
	if err != nil {
		return fmt.Errorf("failed to get position info: %w", err)
	}

	if position.Qty.IsZero() {
		return fmt.Errorf("position %s not found on exchange, nothing to close", task.TargetSymbol)
	}

	log.Printf("[Leg 1] Closing old position: %s, Qty: %s", task.TargetSymbol, position.Qty)

	// Шаг 1: Закрытие текущей позиции (Leg 1)
	// Side: если мы продавали (Short), то закрываем покупкой (Buy)
	closeSide := "Buy" 
	if position.Side == "Buy" {
		closeSide = "Sell" // Если вдруг мы были в лонге
	}

	closeReq := domain.OrderRequest{
		Symbol:      task.TargetSymbol,
		Side:        closeSide,
		OrderType:   "Market", // Market для гарантии выхода. В проде можно Limit + Chase
		Qty:         position.Qty,
		ReduceOnly:  true, // Обязательно! Чтобы не открыть случайно лонг
		OrderLinkID: fmt.Sprintf("close-%d-%d", task.ID, time.Now().Unix()),
	}

	orderID1, err := s.exchange.PlaceOrder(ctx, apiKey, closeReq)
	if err != nil {
		return fmt.Errorf("failed to close Leg 1: %w", err)
	}
	log.Printf("✅ Leg 1 Closed. OrderID: %s", orderID1)

	// Шаг 2: Вычисление нового символа (Leg 2)
	nextSymbol, err := s.calculateNextSymbol(task.TargetSymbol, task.NextStrikeStep, isCall)
	if err != nil {
		return fmt.Errorf("failed to calculate next symbol: %w", err)
	}
	log.Printf("[Leg 2] Opening new position: %s", nextSymbol)

	// Шаг 3: Открытие новой позиции (Leg 2)
	// Открываем ту же сторону, что была изначально (обычно Sell)
	openSide := position.Side 

	openReq := domain.OrderRequest{
		Symbol:      nextSymbol,
		Side:        openSide,
		OrderType:   "Market",
		Qty:         position.Qty, // Роллируем тот же объем
		OrderLinkID: fmt.Sprintf("open-%d-%d", task.ID, time.Now().Unix()),
	}

	orderID2, err := s.exchange.PlaceOrder(ctx, apiKey, openReq)
	if err != nil {
		// ⚠️ CRITICAL ALERT: Мы закрыли позицию в убыток, но не открыли новую!
		// Это состояние "Naked". Тут нужно слать алерт админу в телеграм.
		return fmt.Errorf("🔥 CRITICAL: Leg 1 closed but Leg 2 FAILED! Manual check needed. Err: %w", err)
	}
	log.Printf("✅ Leg 2 Opened. OrderID: %s", orderID2)
	log.Println("🎉 Roll execution completed successfully.")

	return nil
}

// calculateNextSymbol парсит тикер и меняет страйк
// Пример: ETH-30JAN26-3400-C, step=100 -> ETH-30JAN26-3500-C
func (s *RollerService) calculateNextSymbol(currentSymbol string, step decimal.Decimal, isCall bool) (string, error) {
	// Регулярка для разбора тикера Bybit: ASSET-DATE-STRIKE-TYPE
	re := regexp.MustCompile(`^([A-Z]+)-(\d{1,2}[A-Z]{3}\d{2})-(\d+)-([CP])$`)
	matches := re.FindStringSubmatch(currentSymbol)

	if len(matches) != 5 {
		return "", fmt.Errorf("invalid symbol format: %s", currentSymbol)
	}

	// matches[0] - весь строки
	prefix := matches[1] // BTC
	date := matches[2]   // 29DEC23
	strikeStr := matches[3] // 50000
	typeSuffix := matches[4] // C

	strike, err := decimal.NewFromString(strikeStr)
	if err != nil {
		return "", fmt.Errorf("invalid strike: %s", strikeStr)
	}

	// Логика сдвига:
	// Если Call, мы роллируем ВВЕРХ (убегаем от цены), прибавляем шаг.
	// Если Put, мы роллируем ВНИЗ (убегаем от цены), вычитаем шаг.
	var newStrike decimal.Decimal
	if isCall {
		newStrike = strike.Add(step)
	} else {
		newStrike = strike.Sub(step)
	}

	return fmt.Sprintf("%s-%s-%s-%s", prefix, date, newStrike.String(), typeSuffix), nil
}