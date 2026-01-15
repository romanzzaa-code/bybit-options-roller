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

	// Обновляем qty в задаче, чтобы Leg 2 знал, сколько открывать
	task.CurrentQty = position.Qty

	// 2. Формируем ордер на закрытие
	closeSide := domain.SideBuy
	if position.Side == domain.SideBuy {
		closeSide = domain.SideSell
	}

	// Идемпотентный ID
	orderLinkID := fmt.Sprintf("close-%d-v%d", task.ID, task.Version)

	log.Info("Executing Leg 1 (Close)", 
		slog.String("symbol", task.CurrentOptionSymbol),
		slog.String("qty", position.Qty.String()),
		slog.String("side", string(closeSide)))

	_, err = s.exchange.PlaceOrder(ctx, apiKey, domain.OrderRequest{
		Symbol:      task.CurrentOptionSymbol,
		Side:        closeSide,
		OrderType:   domain.OrderTypeMarket,
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

	// 4. Открываем новую позицию
	orderLinkID := fmt.Sprintf("open-%d-v%d", task.ID, task.Version)

	_, err = s.exchange.PlaceOrder(ctx, apiKey, domain.OrderRequest{
		Symbol:      nextSymbolStr,
		Side:        string(task.TargetSide),
		OrderType:   domain.OrderTypeMarket,
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

	return nil
}

func (s *RollerService) handleError(ctx context.Context, task *domain.Task, err error) {
	_ = s.taskRepo.RegisterError(ctx, task.ID, err)
}