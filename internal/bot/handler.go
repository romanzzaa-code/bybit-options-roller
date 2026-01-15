package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/database"
	"github.com/shopspring/decimal"
)

type Handler struct {
	bot        *tgbotapi.BotAPI
	userRepo   *database.UserRepository
	keyRepo    *database.APIKeyRepository
	taskRepo   *database.TaskRepository
	licRepo    *database.LicenseRepository
	exchange   domain.ExchangeAdapter
	adminID    int64
	logger     *slog.Logger
	states     map[int64]*UserState
}

type UserState struct {
	Step       string
	TempAPIKey string
	TempSymbol string
}

func NewHandler(
	bot *tgbotapi.BotAPI,
	userRepo *database.UserRepository,
	keyRepo *database.APIKeyRepository,
	taskRepo *database.TaskRepository,
	licRepo *database.LicenseRepository,
	exchange domain.ExchangeAdapter,
	adminID int64,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		bot:      bot,
		userRepo: userRepo,
		keyRepo:  keyRepo,
		taskRepo: taskRepo,
		licRepo:  licRepo,
		exchange: exchange,
		adminID:  adminID,
		logger:   logger,
		states:   make(map[int64]*UserState),
	}
}

func (h *Handler) Start(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := h.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			go h.handleMessage(ctx, update.Message)
		} else if update.CallbackQuery != nil {
			go h.handleCallback(ctx, update.CallbackQuery)
		}
	}
}

func (h *Handler) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	telegramID := msg.From.ID

	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			h.cmdStart(ctx, msg)
		case "keys":
			h.cmdKeys(ctx, msg)
		case "add":
			h.cmdAdd(ctx, msg)
		case "status":
			h.cmdStatus(ctx, msg)
		case "activate":
			h.cmdActivate(ctx, msg)
		case "gen":
			if telegramID == h.adminID {
				h.cmdGenAdmin(ctx, msg)
			}
		case "ban":
			if telegramID == h.adminID {
				h.cmdBanAdmin(ctx, msg)
			}
		case "stats":
			if telegramID == h.adminID {
				h.cmdStatsAdmin(ctx, msg)
			}
		default:
			h.send(msg.Chat.ID, "Unknown command")
		}
		return
	}

	state := h.states[telegramID]
	if state != nil {
		h.handleStateMachine(ctx, msg, state)
	}
}

func (h *Handler) cmdStart(ctx context.Context, msg *tgbotapi.Message) {
	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	if user == nil {
		newUser := &domain.User{
			TelegramID: msg.From.ID,
			Username:   msg.From.UserName,
			ExpiresAt:  time.Now(),
			IsBanned:   false,
		}
		h.userRepo.Create(ctx, newUser)
		h.send(msg.Chat.ID, "Welcome! Use /activate <code> to activate subscription.")
		return
	}

	if time.Now().After(user.ExpiresAt) {
		h.send(msg.Chat.ID, "Your subscription expired. Use /activate <code>")
		return
	}

	h.send(msg.Chat.ID, fmt.Sprintf("Active until %s\nCommands:\n/keys - Add API keys\n/add - Create task\n/status - View tasks", user.ExpiresAt.Format("2006-01-02")))
}

func (h *Handler) cmdKeys(ctx context.Context, msg *tgbotapi.Message) {
	if !h.checkSubscription(ctx, msg) {
		return
	}

	h.send(msg.Chat.ID, "Send your Bybit API Key and Secret in format:\nKEY SECRET")
	h.states[msg.From.ID] = &UserState{Step: "awaiting_keys"}
}

func (h *Handler) cmdAdd(ctx context.Context, msg *tgbotapi.Message) {
	if !h.checkSubscription(ctx, msg) {
		return
	}

	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	apiKey, err := h.keyRepo.GetActiveByUserID(ctx, user.ID)
	if err != nil || apiKey == nil {
		h.send(msg.Chat.ID, "No API keys found. Use /keys first.")
		return
	}

	positions, err := h.getOptionPositions(ctx, *apiKey)
	if err != nil {
		h.send(msg.Chat.ID, fmt.Sprintf("Failed to fetch positions: %v", err))
		return
	}

	if len(positions) == 0 {
		h.send(msg.Chat.ID, "No option positions found on exchange.")
		return
	}

	keyboard := h.buildPositionKeyboard(positions)
	reply := tgbotapi.NewMessage(msg.Chat.ID, "Select position to roll:")
	reply.ReplyMarkup = keyboard
	h.bot.Send(reply)
}

func (h *Handler) cmdStatus(ctx context.Context, msg *tgbotapi.Message) {
	if !h.checkSubscription(ctx, msg) {
		return
	}

	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	tasks, err := h.taskRepo.GetActiveTasks(ctx)
	if err != nil {
		h.send(msg.Chat.ID, "Failed to fetch tasks")
		return
	}

	var userTasks []domain.Task
	for _, t := range tasks {
		if t.UserID == user.ID {
			userTasks = append(userTasks, t)
		}
	}

	if len(userTasks) == 0 {
		h.send(msg.Chat.ID, "No active tasks")
		return
	}

	var sb strings.Builder
	for _, t := range userTasks {
		sb.WriteString(fmt.Sprintf("🔹 %s\nTrigger: %s | Status: %s\n\n", t.CurrentOptionSymbol, t.TriggerPrice, t.Status))
	}
	h.send(msg.Chat.ID, sb.String())
}

func (h *Handler) cmdActivate(ctx context.Context, msg *tgbotapi.Message) {
	parts := strings.Fields(msg.Text)
	if len(parts) != 2 {
		h.send(msg.Chat.ID, "Usage: /activate <code>")
		return
	}

	code := parts[1]
	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	if user == nil {
		h.send(msg.Chat.ID, "User not found. Use /start first.")
		return
	}

	err := h.licRepo.Redeem(ctx, code, user.ID)
	if err != nil {
		h.send(msg.Chat.ID, fmt.Sprintf("Activation failed: %v", err))
		return
	}

	h.send(msg.Chat.ID, "✅ License activated!")
}

func (h *Handler) cmdGenAdmin(ctx context.Context, msg *tgbotapi.Message) {
	parts := strings.Fields(msg.Text)
	if len(parts) != 2 {
		h.send(msg.Chat.ID, "Usage: /gen <days>")
		return
	}

	days, err := strconv.Atoi(parts[1])
	if err != nil || days <= 0 {
		h.send(msg.Chat.ID, "Invalid days")
		return
	}

	lic, err := h.licRepo.Generate(ctx, days)
	if err != nil {
		h.send(msg.Chat.ID, "Failed to generate")
		return
	}

	h.send(msg.Chat.ID, fmt.Sprintf("License: `%s`", lic.Code))
}

func (h *Handler) cmdBanAdmin(ctx context.Context, msg *tgbotapi.Message) {
	h.send(msg.Chat.ID, "Ban feature: TBD")
}

func (h *Handler) cmdStatsAdmin(ctx context.Context, msg *tgbotapi.Message) {
	h.send(msg.Chat.ID, "Stats feature: TBD")
}

func (h *Handler) handleStateMachine(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
	defer h.bot.Request(tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID))

	switch state.Step {
	case "awaiting_keys":
		h.processKeys(ctx, msg, state)
	case "awaiting_trigger":
		h.processTrigger(ctx, msg, state)
	case "awaiting_step":
		h.processStep(ctx, msg, state)
	}
}

func (h *Handler) processKeys(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
	parts := strings.Fields(msg.Text)
	if len(parts) != 2 {
		h.send(msg.Chat.ID, "Invalid format. Expected: KEY SECRET")
		return
	}

	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	apiKey := &domain.APIKey{
		UserID:  user.ID,
		Key:     parts[0],
		Secret:  parts[1],
		Label:   "Main",
		IsValid: true,
	}

	if err := h.keyRepo.Create(ctx, apiKey); err != nil {
		h.send(msg.Chat.ID, "Failed to save keys")
		return
	}

	delete(h.states, msg.From.ID)
	h.send(msg.Chat.ID, "✅ Keys saved")
}

func (h *Handler) processTrigger(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
	price, err := decimal.NewFromString(msg.Text)
	if err != nil {
		h.send(msg.Chat.ID, "Invalid price")
		return
	}

	state.TempAPIKey = price.String()
	state.Step = "awaiting_step"
	h.send(msg.Chat.ID, "Enter strike step (e.g., 100 for ETH, 1000 for BTC):")
}

func (h *Handler) processStep(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
	step, err := decimal.NewFromString(msg.Text)
	if err != nil {
		h.send(msg.Chat.ID, "Invalid step")
		return
	}

	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	apiKey, _ := h.keyRepo.GetActiveByUserID(ctx, user.ID)

	sym, _ := domain.ParseOptionSymbol(state.TempSymbol)
	trigger, _ := decimal.NewFromString(state.TempAPIKey)

	task := &domain.Task{
		UserID:              user.ID,
		APIKeyID:            apiKey.ID,
		CurrentOptionSymbol: state.TempSymbol,
		UnderlyingSymbol:    sym.BaseCoin,
		TriggerPrice:        trigger,
		NextStrikeStep:      step,
		CurrentQty:          decimal.NewFromFloat(0.1),
		Status:              domain.TaskStateIdle,
	}

	if err := h.taskRepo.CreateTask(ctx, task); err != nil {
		h.send(msg.Chat.ID, "Failed to create task")
		return
	}

	delete(h.states, msg.From.ID)
	h.send(msg.Chat.ID, fmt.Sprintf("✅ Task created!\nSymbol: %s\nTrigger: %s", state.TempSymbol, trigger))
}

func (h *Handler) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	symbol := cb.Data

	h.bot.Request(tgbotapi.NewCallback(cb.ID, ""))

	h.states[cb.From.ID] = &UserState{
		Step:       "awaiting_trigger",
		TempSymbol: symbol,
	}

	h.send(cb.Message.Chat.ID, fmt.Sprintf("Selected: %s\nEnter trigger price (Index Price of underlying):", symbol))
}

func (h *Handler) checkSubscription(ctx context.Context, msg *tgbotapi.Message) bool {
	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	if user == nil || time.Now().After(user.ExpiresAt) {
		h.send(msg.Chat.ID, "Subscription required. Use /activate <code>")
		return false
	}
	return true
}

func (h *Handler) getOptionPositions(ctx context.Context, apiKey domain.APIKey) ([]domain.Position, error) {
	return nil, fmt.Errorf("not implemented: fetch positions via ExchangeAdapter")
}

func (h *Handler) buildPositionKeyboard(positions []domain.Position) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, p := range positions {
		btn := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%s (%s)", p.Symbol, p.Qty),
			p.Symbol,
		)
		rows = append(rows, []tgbotapi.InlineKeyboardButton{btn})
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (h *Handler) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.bot.Send(msg)
}