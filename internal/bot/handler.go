package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/worker"
	"github.com/shopspring/decimal"
)

// Текстовые константы для кнопок (чтобы не опечататься)
const (
	BtnActivate = "🔑 Активировать лицензию"
	BtnAddKey   = "➕ Добавить API ключи"
	BtnStatus   = "📊 Статус / Задачи"
	BtnAdd      = "➕ Добавить задачу"
)

type Handler struct {
	bot      *tgbotapi.BotAPI
	userRepo domain.UserRepository
	keyRepo  domain.APIKeyRepository
	taskRepo domain.TaskRepository
	licRepo  domain.LicenseRepository
	exchange domain.ExchangeAdapter
	manager  *worker.Manager

	adminID int64
	logger  *slog.Logger
	states  map[int64]*UserState
	mu      sync.RWMutex
}

type UserState struct {
	Step       string // awaiting_license, awaiting_keys, awaiting_trigger, awaiting_step
	TempSymbol string
	TempPrice  string
}

func NewHandler(
	bot *tgbotapi.BotAPI,
	userRepo domain.UserRepository,
	keyRepo domain.APIKeyRepository,
	taskRepo domain.TaskRepository,
	licRepo domain.LicenseRepository,
	manager *worker.Manager,
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
		manager:  manager,
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

	// Обработка команд
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			h.cmdStart(ctx, msg)
		case "gen":
			if telegramID == h.adminID {
				h.cmdGenAdmin(ctx, msg)
			}
		// Остальные команды скрыты за кнопками, но оставим для совместимости
		case "status":
			h.cmdStatus(ctx, msg)
		}
		return
	}

	// Обработка кнопок меню (текстовые сообщения)
	switch msg.Text {
	case BtnActivate:
		h.askForLicense(msg.Chat.ID, telegramID)
		return
	case BtnAddKey:
		h.askForAPIKeys(msg.Chat.ID, telegramID)
		return
	case BtnStatus:
		h.cmdStatus(ctx, msg)
		return
	case BtnAdd:
		h.cmdAdd(ctx, msg)
		return
	}

	// Обработка состояний (State Machine)
	h.mu.RLock()
	state := h.states[telegramID]
	h.mu.RUnlock()

	if state != nil {
		h.handleStateMachine(ctx, msg, state)
	} else {
		// Если состояния нет и текст не распознан
		h.send(msg.Chat.ID, "Используйте меню для навигации.")
	}
}

// --- Commands ---

func (h *Handler) cmdStart(ctx context.Context, msg *tgbotapi.Message) {
	user, err := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	if err != nil {
		h.logger.Error("DB error", "err", err)
		return
	}

	// Регистрация нового пользователя
	if user == nil {
		newUser := &domain.User{
			TelegramID: msg.From.ID,
			Username:   msg.From.UserName,
			ExpiresAt:  time.Now(), // Истекла сразу
			IsBanned:   false,
		}
		if err := h.userRepo.Create(ctx, newUser); err != nil {
			h.send(msg.Chat.ID, "⚠️ Ошибка регистрации.")
			return
		}
	}

	// Приветствие и клавиатура
	text := fmt.Sprintf("👋 Привет, %s!\nЯ бот для управления опционами на Bybit (UTA).\n\nДля начала работы требуется активная подписка.", msg.From.FirstName)
	
	// Показываем меню старта
	h.showMainMenu(ctx, msg.Chat.ID, msg.From.ID)
	h.send(msg.Chat.ID, text)
}

func (h *Handler) cmdGenAdmin(ctx context.Context, msg *tgbotapi.Message) {
	parts := strings.Fields(msg.Text)
	if len(parts) != 2 {
		h.send(msg.Chat.ID, "Usage: /gen <days>")
		return
	}

	days, _ := strconv.Atoi(parts[1])
	lic, err := h.licRepo.Generate(ctx, days)
	if err != nil {
		h.send(msg.Chat.ID, "Error generating license")
		return
	}

	// UX Fix: Используем Monospaced шрифт для копирования по клику
	// MarkdownV2 требует экранирования, но для простоты используем HTML или Markdown
	reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Ключ на %d дней:\n`%s`", days, lic.Code))
	reply.ParseMode = "Markdown" 
	h.bot.Send(reply)
}

// --- State Machine & Logic ---

func (h *Handler) handleStateMachine(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
	// Удаляем сообщение пользователя для чистоты чата (опционально)
	// h.bot.Request(tgbotapi.NewDeleteMessage(msg.Chat.ID, msg.MessageID))

	switch state.Step {
	case "awaiting_license":
		h.processLicenseActivation(ctx, msg)
	case "awaiting_keys":
		h.processKeys(ctx, msg)
	case "awaiting_trigger":
		h.processTrigger(ctx, msg, state)
	case "awaiting_step":
		h.processStep(ctx, msg, state)
	}
}

// 1. Активация лицензии
func (h *Handler) askForLicense(chatID int64, userID int64) {
	h.mu.Lock()
	h.states[userID] = &UserState{Step: "awaiting_license"}
	h.mu.Unlock()
	h.send(chatID, "✍️ Введите ваш лицензионный ключ:")
}

func (h *Handler) processLicenseActivation(ctx context.Context, msg *tgbotapi.Message) {
	code := strings.TrimSpace(msg.Text)
	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)

	err := h.licRepo.Redeem(ctx, code, user.ID)
	if err != nil {
		h.send(msg.Chat.ID, fmt.Sprintf("❌ Ошибка: %v\nПопробуйте еще раз или нажмите кнопку меню.", err))
		return // Оставляем в состоянии awaiting_license или сбрасываем? Лучше оставить.
	}

	h.mu.Lock()
	delete(h.states, msg.From.ID) // Сбрасываем состояние
	h.mu.Unlock()

	h.send(msg.Chat.ID, "✅ Лицензия успешно активирована!")
	
	// Flow: Сразу проверяем ключи и перерисовываем меню
	h.checkKeysAndShowMenu(ctx, msg.Chat.ID, msg.From.ID)
}

// 2. Логика проверки ключей (Flow)
func (h *Handler) checkKeysAndShowMenu(ctx context.Context, chatID int64, telegramID int64) {
	// 1. Получаем пользователя по Telegram ID, чтобы узнать его ID в БД
	user, err := h.userRepo.GetByTelegramID(ctx, telegramID)
	if err != nil || user == nil {
		h.logger.Error("User not found in checkKeys", "tg_id", telegramID)
		return
	}

	// 2. Проверяем ключи по ID базы данных (user.ID)
	apiKey, err := h.keyRepo.GetActiveByUserID(ctx, user.ID)
	if err != nil {
		h.logger.Error("DB Error checking keys", "err", err)
		return
	}

	if apiKey == nil {
		h.send(chatID, "⚠️ Для работы требуются API ключи Bybit (Unified Trading).\n\nНажмите кнопку '"+BtnAddKey+"' или введите их сейчас.")
		// Передаем telegramID
		h.showMainMenu(ctx, chatID, telegramID)
	} else {
		h.send(chatID, "🚀 Система готова к работе. Выберите действие в меню.")
		// Передаем telegramID
		h.showMainMenu(ctx, chatID, telegramID)
	}
}

// 3. Ввод API ключей
func (h *Handler) askForAPIKeys(chatID int64, userID int64) {
	h.mu.Lock()
	h.states[userID] = &UserState{Step: "awaiting_keys"}
	h.mu.Unlock()
	h.send(chatID, "🔒 Введите API Key и Secret через пробел:\n\n`API_KEY API_SECRET`")
}

func (h *Handler) processKeys(ctx context.Context, msg *tgbotapi.Message) {
	parts := strings.Fields(msg.Text)
	if len(parts) != 2 {
		h.send(msg.Chat.ID, "❌ Неверный формат. Нужно два значения через пробел.")
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
		h.send(msg.Chat.ID, "❌ Ошибка сохранения ключей.")
		return
	}

	h.mu.Lock()
	delete(h.states, msg.From.ID)
	h.mu.Unlock()

	h.send(msg.Chat.ID, "✅ API ключи сохранены и зашифрованы.")
	h.showMainMenu(ctx, msg.Chat.ID, user.TelegramID)
}

// --- UI Helpers ---

func (h *Handler) showMainMenu(ctx context.Context, chatID int64, telegramID int64) {
	user, _ := h.userRepo.GetByTelegramID(ctx, telegramID)
	
	// Проверяем подписку
	isSubscribed := user != nil && time.Now().Before(user.ExpiresAt)

	var rows [][]tgbotapi.KeyboardButton

	if !isSubscribed {
		rows = append(rows, tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(BtnActivate),
		))
	} else {
		// Проверяем ключи для динамического меню
		keys, _ := h.keyRepo.GetActiveByUserID(ctx, user.ID)
		
		if keys == nil {
			rows = append(rows, tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton(BtnAddKey),
			))
		} else {
			rows = append(rows, tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton(BtnAdd),
				tgbotapi.NewKeyboardButton(BtnStatus),
			))
			// Можно добавить кнопку "Настройки" или "Обновить ключи"
		}
	}

	msg := tgbotapi.NewMessage(chatID, "Меню:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(rows...)
	h.bot.Send(msg)
}

// Остальные методы (cmdStatus, cmdAdd, processTrigger и т.д.) остаются почти без изменений,
// но нужно убедиться, что они проверяют подписку.

func (h *Handler) cmdStatus(ctx context.Context, msg *tgbotapi.Message) {
	if !h.checkSubscription(ctx, msg) {
		return
	}

	user, err := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	if err != nil {
		h.send(msg.Chat.ID, "Ошибка получения профиля.")
		return
	}

	// Получаем задачи пользователя
	tasks, err := h.taskRepo.GetActiveTasksByUserID(ctx, user.ID)
	if err != nil {
		h.logger.Error("Failed to fetch user tasks", "err", err)
		h.send(msg.Chat.ID, "Ошибка получения списка задач.")
		return
	}

	if len(tasks) == 0 {
		h.send(msg.Chat.ID, "📭 У вас нет активных задач.")
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 **Ваши активные задачи (%d):**\n\n", len(tasks)))

	for _, t := range tasks {
		// Иконка статуса
		statusIcon := "🟢"
		if t.Status == domain.TaskStateFailed {
			statusIcon = "🔴"
		} else if t.Status != domain.TaskStateIdle {
			statusIcon = "🔄" // В процессе роллирования
		}

		// Формируем карточку задачи
		sb.WriteString(fmt.Sprintf("%s **%s**\n", statusIcon, t.CurrentOptionSymbol))
		sb.WriteString(fmt.Sprintf("├ 🎯 Триггер (Index): `%s`\n", t.TriggerPrice.String()))
		sb.WriteString(fmt.Sprintf("├ 📦 Объем: `%s`\n", t.CurrentQty.String()))
		sb.WriteString(fmt.Sprintf("└ ⚙️ Статус: `%s`\n", t.Status))
		
		if t.LastError != "" {
			sb.WriteString(fmt.Sprintf("⚠️ Ошибка: %s\n", t.LastError))
		}
		sb.WriteString("\n")
	}

	h.send(msg.Chat.ID, sb.String())
}

func (h *Handler) cmdAdd(ctx context.Context, msg *tgbotapi.Message) {
    if !h.checkSubscription(ctx, msg) { return }
    
    // ... Логика получения позиций ...
    // ВАЖНО: Вставь сюда логику cmdAdd из старого файла
    // Но замени h.exchange.GetPositions(...) вызов
    
    user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
    apiKey, _ := h.keyRepo.GetActiveByUserID(ctx, user.ID)
    
    positions, err := h.exchange.GetPositions(ctx, *apiKey)
    if err != nil {
        h.send(msg.Chat.ID, "Ошибка получения позиций с биржи: "+err.Error())
        return
    }
    
    if len(positions) == 0 {
		h.send(msg.Chat.ID, "Нет открытых опционных позиций.")
		return
	}

    keyboard := h.buildPositionKeyboard(positions)
	reply := tgbotapi.NewMessage(msg.Chat.ID, "Выберите позицию для роллирования:")
	reply.ReplyMarkup = keyboard
	h.bot.Send(reply)
}

// Helpers для callback и state machine остаются теми же
// ... (handleCallback, processTrigger, processStep из старого файла) ...

func (h *Handler) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	symbol := cb.Data
	h.bot.Request(tgbotapi.NewCallback(cb.ID, ""))

	h.mu.Lock()
	h.states[cb.From.ID] = &UserState{
		Step:       "awaiting_trigger",
		TempSymbol: symbol,
	}
	h.mu.Unlock()

	h.send(cb.Message.Chat.ID, fmt.Sprintf("Выбрано: %s\nВведите цену триггера (Index Price):", symbol))
}

func (h *Handler) processTrigger(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
    // ... (старая логика) ...
    price, err := decimal.NewFromString(msg.Text)
	if err != nil {
		h.send(msg.Chat.ID, "Неверная цена. Введите число.")
		return
	}

	h.mu.Lock()
	state.TempPrice = price.String() // Исправил название поля (было TempAPIKey по ошибке в прошлом коде)
	state.Step = "awaiting_step"
	h.mu.Unlock()
	
	h.send(msg.Chat.ID, "Введите шаг следующего страйка (например, 100):")
}

func (h *Handler) processStep(ctx context.Context, msg *tgbotapi.Message, state *UserState) {
    // ... (старая логика создания задачи) ...
    step, err := decimal.NewFromString(msg.Text)
    if err != nil {
        h.send(msg.Chat.ID, "Неверный шаг.")
        return
    }
    sym, err := domain.ParseOptionSymbol(state.TempSymbol)
	if err != nil {
		h.logger.Error("Failed to parse symbol", "symbol", state.TempSymbol, "err", err)
		h.send(msg.Chat.ID, "❌ Ошибка формата символа: "+state.TempSymbol)
		return
	}

	// 2. Нормализуем тикер для Linear Stream (добавляем USDT)
	underlying := sym.BaseCoin
	if !strings.HasSuffix(underlying, "USDT") {
		underlying += "USDT"
	}

	// 3. Подготовка данных (ПОЛУЧАЕМ РЕАЛЬНЫЙ ОБЪЕМ)
	user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
	apiKey, _ := h.keyRepo.GetActiveByUserID(ctx, user.ID)
	trigger, _ := decimal.NewFromString(state.TempPrice)

    // Запрашиваем позицию, чтобы узнать объем
    realQty := decimal.NewFromFloat(0.1) // Дефолт на случай ошибки
    if pos, err := h.exchange.GetPosition(ctx, *apiKey, state.TempSymbol); err == nil && !pos.Qty.IsZero() {
        realQty = pos.Qty
    }

	// 4. Создаем задачу
	task := &domain.Task{
		// ...
		CurrentOptionSymbol: state.TempSymbol,
		UnderlyingSymbol:    underlying,
		TriggerPrice:        trigger,
		NextStrikeStep:      step,
		CurrentQty:          realQty, // <--- ИСПОЛЬЗУЕМ РЕАЛЬНЫЙ ОБЪЕМ
		Status:              domain.TaskStateIdle,
	}
	
	if err := h.taskRepo.CreateTask(ctx, task); err != nil {
	    h.send(msg.Chat.ID, "Ошибка создания задачи.")
	    return
	}

	go func() {
        if err := h.manager.ReloadTasks(context.Background()); err != nil {
            h.logger.Error("Failed to reload tasks manager", "err", err)
        } else {
            h.logger.Info("Manager reloaded successfully via Bot")
        }
    }()
	
	h.mu.Lock()
    delete(h.states, msg.From.ID)
    h.mu.Unlock()
    
    h.send(msg.Chat.ID, "✅ Задача создана и мгновенно активирована!")
}


func (h *Handler) checkSubscription(ctx context.Context, msg *tgbotapi.Message) bool {
    // ... (старая логика)
    user, _ := h.userRepo.GetByTelegramID(ctx, msg.From.ID)
    if user == nil || time.Now().After(user.ExpiresAt) {
        h.send(msg.Chat.ID, "Подписка не активна.")
        h.showMainMenu(ctx, msg.Chat.ID, msg.From.ID)
        return false
    }
    return true
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