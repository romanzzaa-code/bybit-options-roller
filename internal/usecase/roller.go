package usecase

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
)

type RollerService struct {
	exchange  domain.ExchangeAdapter
	taskRepo  domain.TaskRepository
	notifySvc domain.NotificationService
}

func NewRollerService(exchange domain.ExchangeAdapter, taskRepo domain.TaskRepository) *RollerService {
	return &RollerService{
		exchange: exchange,
		taskRepo: taskRepo,
	}
}

// ExecuteRoll — Основной сценарий (Saga).
func (s *RollerService) ExecuteRoll(ctx context.Context, apiKey domain.APIKey, task *domain.Task) error {
	// 1. Получаем цену БАЗОВОГО актива (Index Price), например BTCUSD
	indexPrice, err := s.exchange.GetIndexPrice(ctx, task.UnderlyingSymbol)
	if err != nil {
		return fmt.Errorf("failed to get index price for %s: %w", task.UnderlyingSymbol, err)
	}

	// 2. Спрашиваем у доменной модели: "Пора?"
	// Логика сравнения (>= или <=) теперь инкапсулирована в Task.
	if !task.ShouldRoll(indexPrice) {
		// Не спамим логами, если ничего делать не надо
		return nil
	}

	log.Printf("🚀 TRIGGER HIT! Task %d. %s Price: %s (Trigger: %s). Starting ROLL...", 
		task.ID, task.UnderlyingSymbol, indexPrice, task.TriggerPrice)

	// 3. Меняем статус на ROLL_INITIATED (блокируем задачу от других воркеров)
	if err := s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateRollInitiated, task.Version); err != nil {
		return fmt.Errorf("failed to lock task (concurrency error?): %w", err)
	}
	// Обновляем версию в памяти, так как мы только что успешно обновили БД
	task.Version++ 

	// 4. Получаем реальную позицию
	position, err := s.exchange.GetPosition(ctx, apiKey, task.CurrentOptionSymbol)
	if err != nil {
		s.handleError(ctx, task, "Failed to fetch position")
		return err
	}

	if position.Qty.IsZero() {
		s.handleError(ctx, task, "Position not found on exchange")
		return fmt.Errorf("position %s is zero/missing", task.CurrentOptionSymbol)
	}

	// 5. Парсим текущий символ, чтобы вычислить следующий
	currentSym, err := domain.ParseOptionSymbol(task.CurrentOptionSymbol)
	if err != nil {
		s.handleError(ctx, task, "Invalid symbol format")
		return err
	}
	
	// Вычисляем следующий страйк
	nextSym := currentSym.NextStrike(task.NextStrikeStep)
	log.Printf("[Roller] Plan: Close %s -> Open %s", currentSym, nextSym)

	// --- LEG 1: Closing ---
	closeSide := "Buy"
	if position.Side == "Buy" {
		closeSide = "Sell"
	}

	closeReq := domain.OrderRequest{
		Symbol:      task.CurrentOptionSymbol,
		Side:        closeSide,
		OrderType:   "Market",
		Qty:         position.Qty,
		ReduceOnly:  true,
		OrderLinkID: fmt.Sprintf("close-%d-%d", task.ID, time.Now().Unix()),
	}

	if _, err := s.exchange.PlaceOrder(ctx, apiKey, closeReq); err != nil {
		s.handleError(ctx, task, "Leg 1 failed: "+err.Error())
		return fmt.Errorf("leg 1 execution failed: %w", err)
	}

	// 6. CHECKPOINT: Сохраняем, что первая нога закрыта.
	// Это критическая точка. Если упадем здесь — Recovery Worker увидит этот статус.
	if err := s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateLeg1Closed, task.Version); err != nil {
		// Даже если не смогли записать в БД, идем дальше, так как ордер уже на бирже!
		log.Printf("⚠️ CRITICAL DB ERROR: Failed to save LEG1_CLOSED state: %v", err)
	} else {
		task.Version++
	}

	log.Printf("✅ Leg 1 Closed. Opening Leg 2...")

	// --- LEG 2: Opening ---
	openReq := domain.OrderRequest{
		Symbol:      nextSym.String(),
		Side:        position.Side, // Открываем ту же сторону (Put/Call)
		OrderType:   "Market",
		Qty:         position.Qty,
		OrderLinkID: fmt.Sprintf("open-%d-%d", task.ID, time.Now().Unix()),
	}

	if _, err := s.exchange.PlaceOrder(ctx, apiKey, openReq); err != nil {
		// ВОТ ТУТ НУЖЕН АЛЕРТ! Мы "голые".
		// Ставим статус FAILED, чтобы админ увидел.
		s.taskRepo.UpdateTaskState(ctx, task.ID, domain.TaskStateFailed, task.Version)
		return fmt.Errorf("🔥 FATAL: Leg 1 done, Leg 2 FAILED. Position is NAKED! Err: %w", err)
	}

	// 7. Финал: обновляем задачу на новый символ и сбрасываем в IDLE
	if err := s.taskRepo.UpdateTaskSymbol(ctx, task.ID, nextSym.String(), position.Qty, task.Version); err != nil {
		log.Printf("⚠️ Failed to update task to new symbol: %v", err)
		return err
	}

	log.Println("🎉 Roll sequence completed successfully.")
	return nil
}

func (s *RollerService) handleError(ctx context.Context, task *domain.Task, msg string) {
	// Сбрасываем в ERROR, чтобы воркер не долбил бесконечно одну ошибку
	_ = s.taskRepo.SaveError(ctx, task.ID, msg)
}