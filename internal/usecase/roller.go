package usecase

import (
	"context"
	"fmt"
	"log/slog"

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
	// Больше никакого s.exchange.GetIndexPrice() здесь!
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
	// 3. ВЫПОЛНЕНИЕ LEG 1 (CLOSE OLD POSITION)
	// ---------------------------------------------------------
	if err := s.processLeg1(ctx, apiKey, task, log); err != nil {
		s.handleError(ctx, task, fmt.Errorf("leg 1 failed: %w", err))
		return err
	}

	// ---------------------------------------------------------
	// 4. ВЫПОЛНЕНИЕ LEG 2 (OPEN NEW POSITION)
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
	// 1. Получаем реальную позицию с биржи
	position, err := s.exchange.GetPosition(ctx, apiKey, task.CurrentOptionSymbol)
	if err != nil {
		return fmt.Errorf("fetch position: %w", err)
	}

	if position.Qty.IsZero() {
		return fmt.Errorf("position %s not found or zero qty", task.CurrentOptionSymbol)
	}

	// Обновляем qty в задаче, чтобы Leg 2 знал, сколько открывать, 
	// если вдруг произойдет сбой и перезагрузка.
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
	// Важно: в идеале тут нужно сохранить и CurrentQty в БД, если оно изменилось, 
	// но пока предполагаем, что taskRepo просто меняет статус.
	if err := s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateLeg1Closed, task.Version); err != nil {
		// Если БД упала, но ордер ушел - это проблема, но мы продолжаем выполнение в памяти,
		// пытаясь открыть вторую ногу. Recovery Worker потом разберется с версиями.
		log.Error("CRITICAL DB ERROR: Failed to save LEG1_CLOSED", slog.String("err", err.Error()))
	} else {
		task.Version++
	}

	return nil
}

// processLeg2: Вычисляет следующий страйк и открывает новую позицию.
func (s *RollerService) processLeg2(ctx context.Context, apiKey domain.APIKey, task *domain.Task, log *slog.Logger) error {
	// 1. Вычисляем следующий символ
	currentSym, err := domain.ParseOptionSymbol(task.CurrentOptionSymbol)
	if err != nil {
		return fmt.Errorf("parse symbol error: %w", err)
	}
	
	nextSym := currentSym.NextStrike(task.NextStrikeStep)
	
	log.Info("Executing Leg 2 (Open)", 
		slog.String("old_symbol", task.CurrentOptionSymbol),
		slog.String("new_symbol", nextSym.String()),
		slog.String("qty", task.CurrentQty.String()))

	// 2. Открываем новую позицию
	// Используем task.CurrentQty (которое мы получили из processLeg1 или из БД при восстановлении)
	
	// Предполагаем, что сторона (Call/Put) сохраняется, и мы всегда ПОКУПАЕМ или ПРОДАЕМ так же, как было.
	// Для простоты примера: если мы закрывали Sell (покупали), то открывать новый Sell мы будем снова продажей.
	// Тут нужна бизнес-логика определения Side. Допустим, стратегия "Short Put" -> мы всегда Sell.
	// Если стратегия динамическая, нам нужно знать Side изначальной позиции.
	// В рамках этого фикса допустим, мы роллим ту же сторону.
	targetSide := task.TargetSide
	
	orderLinkID := fmt.Sprintf("open-%d-v%d", task.ID, task.Version)

	_, err = s.exchange.PlaceOrder(ctx, apiKey, domain.OrderRequest{
		Symbol:      nextSym.String(),
		Side:        string(targetSide), 
		OrderType:   domain.OrderTypeMarket,
		Qty:         task.CurrentQty,
		OrderLinkID: orderLinkID,
	})
	if err != nil {
		return err
	}

	// 3. Финализация: Обновляем задачу на новый символ и сбрасываем в IDLE
	if err := s.taskRepo.UpdateTaskSymbol(ctx, task.ID, nextSym.String(), task.CurrentQty, task.Version); err != nil {
		log.Error("Failed to update task final state", slog.String("err", err.Error()))
		// Не возвращаем ошибку, так как фактически ролл выполнен успешно
		return nil 
	}

	return nil
}

func (s *RollerService) handleError(ctx context.Context, task *domain.Task, err error) {
	_ = s.taskRepo.RegisterError(ctx, task.ID, err)
}