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
	exchange   domain.ExchangeAdapter
	taskRepo   domain.TaskRepository
	notifySvc  domain.NotificationService
}

func NewRollerService(exchange domain.ExchangeAdapter) *RollerService {
	return &RollerService{
		exchange: exchange,
	}
}

func (s *RollerService) WithTaskRepo(repo domain.TaskRepository) *RollerService {
	s.taskRepo = repo
	return s
}

func (s *RollerService) WithNotifySvc(svc domain.NotificationService) *RollerService {
	s.notifySvc = svc
	return s
}

func (s *RollerService) ExecuteRoll(ctx context.Context, apiKey domain.APIKey, task *domain.Task) error {
	log.Printf("[Roller] Checking task for %s. Trigger: %s", task.TargetSymbol, task.TriggerPrice)

	markPrice, err := s.exchange.GetMarkPrice(ctx, task.TargetSymbol)
	if err != nil {
		return fmt.Errorf("failed to get mark price: %w", err)
	}

	isCall := strings.HasSuffix(task.TargetSymbol, "-C")
	shouldRoll := false

	if isCall {
		if markPrice.GreaterThanOrEqual(task.TriggerPrice) {
			shouldRoll = true
		}
	} else {
		if markPrice.LessThanOrEqual(task.TriggerPrice) {
			shouldRoll = true
		}
	}

	if !shouldRoll {
		log.Printf("[Roller] Price %s is safe (Trigger %s). No action.", markPrice, task.TriggerPrice)
		return nil
	}

	log.Printf("🚨 TRIGGER HIT! %s MarkPrice: %s. Initiating ROLL sequence...", task.TargetSymbol, markPrice)

	position, err := s.exchange.GetPosition(ctx, apiKey, task.TargetSymbol)
	if err != nil {
		return fmt.Errorf("failed to get position info: %w", err)
	}

	if position.Qty.IsZero() {
		return fmt.Errorf("position %s not found on exchange, nothing to close", task.TargetSymbol)
	}

	log.Printf("[Leg 1] Closing old position: %s, Qty: %s", task.TargetSymbol, position.Qty)

	closeSide := "Buy" 
	if position.Side == "Buy" {
		closeSide = "Sell"
	}

	closeReq := domain.OrderRequest{
		Symbol:      task.TargetSymbol,
		Side:        closeSide,
		OrderType:   "Market",
		Qty:         position.Qty,
		ReduceOnly:  true,
		OrderLinkID: fmt.Sprintf("close-%d-%d", task.ID, time.Now().Unix()),
	}

	orderID1, err := s.exchange.PlaceOrder(ctx, apiKey, closeReq)
	if err != nil {
		return fmt.Errorf("failed to close Leg 1: %w", err)
	}
	log.Printf("✅ Leg 1 Closed. OrderID: %s", orderID1)

	nextSymbol, err := s.calculateNextSymbol(task.TargetSymbol, task.NextStrikeStep, isCall)
	if err != nil {
		return fmt.Errorf("failed to calculate next symbol: %w", err)
	}
	log.Printf("[Leg 2] Opening new position: %s", nextSymbol)

	openSide := position.Side

	openReq := domain.OrderRequest{
		Symbol:      nextSymbol,
		Side:        openSide,
		OrderType:   "Market",
		Qty:         position.Qty,
		OrderLinkID: fmt.Sprintf("open-%d-%d", task.ID, time.Now().Unix()),
	}

	orderID2, err := s.exchange.PlaceOrder(ctx, apiKey, openReq)
	if err != nil {
		return fmt.Errorf("🔥 CRITICAL: Leg 1 closed but Leg 2 FAILED! Manual check needed. Err: %w", err)
	}
	log.Printf("✅ Leg 2 Opened. OrderID: %s", orderID2)
	log.Println("🎉 Roll execution completed successfully.")

	if s.taskRepo != nil {
		if err := s.taskRepo.UpdateTaskSymbol(ctx, task.ID, nextSymbol, position.Qty); err != nil {
			log.Printf("[Roller] Failed to update task symbol: %v", err)
		}
	}

	return nil
}

func (s *RollerService) calculateNextSymbol(currentSymbol string, step decimal.Decimal, isCall bool) (string, error) {
	re := regexp.MustCompile(`^([A-Z]+)-(\d{1,2}[A-Z]{3}\d{2})-(\d+)-([CP])$`)
	matches := re.FindStringSubmatch(currentSymbol)

	if len(matches) != 5 {
		return "", fmt.Errorf("invalid symbol format: %s", currentSymbol)
	}

	prefix := matches[1]
	date := matches[2]
	strikeStr := matches[3]
	typeSuffix := matches[4]

	strike, err := decimal.NewFromString(strikeStr)
	if err != nil {
		return "", fmt.Errorf("invalid strike: %s", strikeStr)
	}

	var newStrike decimal.Decimal
	if isCall {
		newStrike = strike.Add(step)
	} else {
		newStrike = strike.Sub(step)
	}

	return fmt.Sprintf("%s-%s-%s-%s", prefix, date, newStrike.String(), typeSuffix), nil
}