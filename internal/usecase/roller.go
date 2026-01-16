package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time" // <--- 1. Импорт добавлен

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/shopspring/decimal"
)

type RollerService struct {
	exchange domain.ExchangeAdapter
	taskRepo domain.TaskRepository
	logger   *slog.Logger
}

func NewRollerService(exchange domain.ExchangeAdapter, taskRepo domain.TaskRepository, logger *slog.Logger) *RollerService {
	return &RollerService{
		exchange: exchange,
		taskRepo: taskRepo,
		logger:   logger,
	}
}

func (s *RollerService) ExecuteRoll(ctx context.Context, apiKey domain.APIKey, task *domain.Task, currentPrice decimal.Decimal) error {
	log := s.logger.With(
		slog.Int64("task_id", task.ID),
		slog.String("symbol", task.UnderlyingSymbol),
	)

	// 1. RECOVERY MODE (не требует проверки цены)
	if task.Status == domain.TaskStateLeg1Closed {
		log.Warn("⚠️ RECOVERY MODE: Resuming to prevent naked position.")
		return s.processLeg2(ctx, apiKey, task, log)
	}

	// 2. TRIGGER CHECK (на основе ПЕРЕДАННОЙ цены)
	if !task.ShouldRoll(currentPrice) {
		return nil
	}

	log.Info("🚀 Trigger hit", 
		slog.String("price", currentPrice.String()), 
		slog.String("trigger", task.TriggerPrice.String()))

	// 3. Блокировка и выполнение (Optimistic Locking)
	if err := s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateRollInitiated, task.Version); err != nil {
		return nil // Кто-то другой уже начал ролл
	}
	task.Version++

	// ---------------------------------------------------------
	// 4. ВЫПОЛНЕНИЕ LEG 1 (CLOSE OLD POSITION)
	// ---------------------------------------------------------
	if err := s.processLeg1(ctx, apiKey, task, log); err != nil {
		s.handleError(ctx, task, fmt.Errorf("leg 1 failed: %w", err))
		return err
	}

	// ---------------------------------------------------------
	// 5. ВЫПОЛНЕНИЕ LEG 2 (OPEN NEW POSITION)
	// ---------------------------------------------------------
	// Сразу переходим ко второй ноге без прерывания
	if err := s.processLeg2(ctx, apiKey, task, log); err != nil {
		// Это фатальная ошибка: мы закрыли старую, но не открыли новую.
		// Ставим статус FAILED, чтобы админ вмешался.
		_ = s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateFailed, task.Version)
		return fmt.Errorf("🔥 FATAL: Leg 2 failed after Leg 1 closed! Position is naked. Err: %w", err)
	}

	log.Info("🎉 Roll sequence completed successfully")
	return nil
}

// processLeg1: Получает текущую позицию, закрывает её и обновляет статус в БД.
func (s *RollerService) processLeg1(ctx context.Context, apiKey domain.APIKey, task *domain.Task, log *slog.Logger) error {
	if task.TargetSide == "" {
		s.logger.Warn("TargetSide is empty in Leg 2 (likely after restart), defaulting to SELL")
		task.TargetSide = domain.SideSell
	}
	// --- НАЧАЛО: Проверка экспирации ---
	// Пытаемся понять, жив ли еще опцион
	expiryTime, err := domain.ParseExpirationFromSymbol(task.CurrentOptionSymbol) // <--- Правильное поле
	if err == nil {
		// Добавляем буфер 5 минут на всякий случай
		safeZone := expiryTime.Add(5 * time.Minute)

		if time.Now().UTC().After(safeZone) {
			s.logger.Info("Task expired based on ticker date. Closing task.",
				"task_id", task.ID,
				"symbol", task.CurrentOptionSymbol,
				"expiry_utc", expiryTime)

			// <--- ВАЖНО: Передаем 4 аргумента: context, ID, State, Version
			return s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateCompleted, task.Version)
		}
	} else {
		// Если не смогли распарсить дату, просто ворним и работаем дальше
		s.logger.Warn("Could not parse expiry date from symbol", 
			"symbol", task.CurrentOptionSymbol, 
			"err", err)
	}
	// --- КОНЕЦ: Проверка экспирации ---


	// 1. Получаем реальную позицию с биржи
	position, err := s.exchange.GetPosition(ctx, apiKey, task.CurrentOptionSymbol)
	if err != nil {
		return fmt.Errorf("fetch position: %w", err)
	}

	// Если позиция 0, возможно ее закрыли руками или ликвидировало
	if position.Qty.IsZero() {
		log.Info("Position not found (qty is 0), completing task", "task_id", task.ID)
		// Тоже считаем задачу выполненной, раз позиции нет
		return s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateCompleted, task.Version)
	}

	markPrice, err := s.exchange.GetMarkPrice(ctx, task.CurrentOptionSymbol)
	if err != nil {
		return fmt.Errorf("failed to get mark price for leg1: %w", err)
	}
	closeSide := domain.SideBuy
	if position.Side == domain.SideBuy {
		closeSide = domain.SideSell
	}
	if task.TargetSide == "" {
		task.TargetSide = domain.Side(position.Side) 
	}

	// Рассчитываем агрессивную цену
	safePrice := s.calculateSafeLimitPrice(string(closeSide), markPrice)

	log.Info("Executing Leg 1 (Close) with Aggressive Limit", 
		slog.String("symbol", task.CurrentOptionSymbol),
		slog.String("qty", position.Qty.String()),
		slog.String("side", string(closeSide)),
		slog.String("mark_price", markPrice.String()),
		slog.String("limit_price", safePrice.String()))

	// 2. Формируем ордер на закрытие (Aggressive Limit IOC)
	// Идемпотентный ID
	orderLinkID := fmt.Sprintf("close-%d-v%d", task.ID, task.Version)

	_, err = s.exchange.PlaceOrder(ctx, apiKey, domain.OrderRequest{
		Symbol:      task.CurrentOptionSymbol,
		Side:        closeSide,
		OrderType:   domain.OrderTypeLimit, // <--- ИЗМЕНЕНО
		Price:       safePrice,             // <--- НОВОЕ
		TimeInForce: "IOC",                 // <--- НОВОЕ (Immediate Or Cancel)
		Qty:         position.Qty,
		ReduceOnly:  true,
		OrderLinkID: orderLinkID,
	})
	if err != nil {
		return err
	}

	// 3. CHECKPOINT: Сохраняем статус LEG1_CLOSED
	if err := s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateLeg1Closed, task.Version); err != nil {
		log.Error("CRITICAL DB ERROR: Failed to save LEG1_CLOSED", slog.String("err", err.Error()))
	} else {
		task.Version++
	}

	return nil
}

// processLeg2: Вычисляет следующий страйк и открывает новую позицию.
// processLeg2: Вычисляет следующий доступный страйк через API биржи и открывает новую позицию.
func (s *RollerService) processLeg2(ctx context.Context, apiKey domain.APIKey, task *domain.Task, log *slog.Logger) error {
	// 1. Разбираем текущий символ
	currentSym, err := domain.ParseOptionSymbol(task.CurrentOptionSymbol)
	if err != nil {
		return fmt.Errorf("parse symbol error: %w", err)
	}

	// 2. ЗАПРАШИВАЕМ РЕАЛЬНЫЕ СТРАЙКИ С БИРЖИ
	// Вместо математики (current + step), мы спрашиваем биржу: "Какие страйки есть?"
	strikes, err := s.exchange.GetOptionStrikes(ctx, currentSym.BaseCoin, currentSym.Expiry)
	if err != nil {
		return fmt.Errorf("failed to fetch option chain: %w", err)
	}

	// 3. Ищем следующий реальный страйк
	nextSymbolStr, err := currentSym.FindNextStrike(strikes)
	if err != nil {
		return fmt.Errorf("failed to find next strike: %w", err)
	}

	log.Info("Executing Leg 2 (Open)",
		slog.String("method", "SmartStrikeSelection"), // пометка в логах
		slog.String("old_symbol", task.CurrentOptionSymbol),
		slog.String("new_symbol", nextSymbolStr),
		slog.String("qty", task.CurrentQty.String()))
	
	nextMarkPrice, err := s.exchange.GetMarkPrice(ctx, nextSymbolStr)
	if err != nil {
		return fmt.Errorf("failed to get mark price for leg2 (%s): %w", nextSymbolStr, err)
	}

	// Рассчитываем агрессивную цену для открытия
	safeOpenPrice := s.calculateSafeLimitPrice(string(task.TargetSide), nextMarkPrice)

	log.Info("Executing Leg 2 (Open) with Aggressive Limit",
		slog.String("method", "SmartStrikeSelection"),
		slog.String("old_symbol", task.CurrentOptionSymbol),
		slog.String("new_symbol", nextSymbolStr),
		slog.String("mark_price", nextMarkPrice.String()),
		slog.String("limit_price", safeOpenPrice.String()),
		slog.String("qty", task.CurrentQty.String()))

	// 4. Открываем новую позицию (Aggressive Limit IOC)
	orderLinkID := fmt.Sprintf("open-%d-v%d", task.ID, task.Version)

	_, err = s.exchange.PlaceOrder(ctx, apiKey, domain.OrderRequest{
		Symbol:      nextSymbolStr,
		Side:        string(task.TargetSide),
		OrderType:   domain.OrderTypeLimit, // <--- ИЗМЕНЕНО
		Price:       safeOpenPrice,         // <--- НОВОЕ
		TimeInForce: "IOC",                 // <--- НОВОЕ
		Qty:         task.CurrentQty,
		OrderLinkID: orderLinkID,
	})
	if err != nil {
		return err
	}

	// 5. Финализация
	if err := s.taskRepo.UpdateTaskSymbol(ctx, task.ID, nextSymbolStr, task.CurrentQty, task.Version); err != nil {
		log.Error("Failed to update task final state", slog.String("err", err.Error()))
		return nil
	}
	retryCount := 0
	for {
		// Проверяем, не выключается ли бот (Graceful Shutdown)
		if ctx.Err() != nil {
			log.Warn("Context cancelled during Leg 2 retry loop. Task remains in LEG1_CLOSED state.")
			return ctx.Err()
		}

		err := s.processLeg2(ctx, apiKey, task, log)
		if err == nil {
			// УСПЕХ! Выходим из цикла.
			break
		}

		retryCount++
		// Логируем ошибку, но НЕ меняем статус на FAILED.
		// Мы будем долбить биржу до победного.
		log.Error("⚠️ Leg 2 failed, retrying...",
			slog.Int("attempt", retryCount),
			slog.String("err", err.Error()))

		// Ждем перед повтором (Backoff strategy)
		// Можно сделать экспоненциальную задержку, но для начала хватит фиксированной.
		// Важно использовать select с ctx.Done, чтобы не зависнуть при выключении.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			// Продолжаем цикл
		}
	}

	log.Info("🎉 Roll sequence completed successfully")
	return nil

}

func (s *RollerService) handleError(ctx context.Context, task *domain.Task, err error) {
	_ = s.taskRepo.RegisterError(ctx, task.ID, err)
}

// calculateSafeLimitPrice рассчитывает цену для Агрессивной Лимитки.
// Если мы ПОКУПАЕМ (Close Short / Open Long), мы готовы купить дороже (MarkPrice + 20%).
// Если мы ПРОДАЕМ (Open Short / Close Long), мы готовы продать дешевле (MarkPrice - 20%).
func (s *RollerService) calculateSafeLimitPrice(side string, markPrice decimal.Decimal) decimal.Decimal {
	// 20% "запаса" для гарантии исполнения
	slippageFactor := decimal.NewFromFloat(0.20) 

	if side == domain.SideBuy {
		// Хотим купить: ставим лимитку ВЫШЕ рынка (Mark * 1.2)
		// Ордер исполнится мгновенно по лучшим ценам стакана, но не дороже этого потолка.
		return markPrice.Mul(decimal.NewFromInt(1).Add(slippageFactor))
	}

	// Хотим продать: ставим лимитку НИЖЕ рынка (Mark * 0.8)
	// Ордер исполнится мгновенно, но не дешевле этого пола.
	return markPrice.Mul(decimal.NewFromInt(1).Sub(slippageFactor))
}