package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	
	"github.com/shopspring/decimal"
)

// jobDTO связывает задачу и цену, которая её вызвала
type jobDTO struct {
	Task  *domain.Task
	Price decimal.Decimal
}

type Manager struct {
	repo     domain.TaskRepository
	keyRepo  domain.APIKeyRepository
	roller   *usecase.RollerService
	streamer domain.MarketStreamer
	logger   *slog.Logger

	jobChan chan jobDTO
	// Кэш для активных задач, чтобы не дергать БД на каждый тик (Опционально для v2)
	mu sync.RWMutex
}

func NewManager(
	tr domain.TaskRepository,
	kr domain.APIKeyRepository,
	roller *usecase.RollerService,
	streamer domain.MarketStreamer,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		repo:     tr,
		keyRepo:  kr,
		roller:   roller,
		streamer: streamer,
		logger:   logger,
		// Буфер 100, чтобы скачки цены не блокировали WebSocket
		jobChan: make(chan jobDTO, 100),
	}
}

func (m *Manager) Run(ctx context.Context) {
	m.logger.Info("Starting Manager: Event-Driven Mode")

	// 1. Получаем список активных задач для подписки
	// В продакшене этот список нужно обновлять динамически (Hot Reload)
	activeTasks, err := m.repo.GetActiveTasks(ctx)
	if err != nil {
		m.logger.Error("Failed to get active tasks", "err", err)
		return
	}

	if len(activeTasks) == 0 {
		m.logger.Warn("No active tasks found. Manager is idle.")
		// Не выходим, так как могут появиться задачи (нужен механизм обновления подписки)
	}

	// Извлекаем уникальные символы для подписки
	symbolMap := make(map[string]bool)
	for _, task := range activeTasks {
		symbolMap[task.UnderlyingSymbol] = true
	}
	activeSymbols := make([]string, 0, len(symbolMap))
	for symbol := range symbolMap {
		activeSymbols = append(activeSymbols, symbol)
	}

	// 2. Подписываемся на поток
	priceUpdates, err := m.streamer.Subscribe(activeSymbols)
	if err != nil {
		// Критическая ошибка, если не можем даже попытаться подписаться
		m.logger.Error("CRITICAL: Failed to initialize stream", "err", err)
		return
	}

	// 3. Запускаем пул воркеров (5 шт)
	for i := 0; i < 5; i++ {
		go m.worker(ctx, i)
	}

	// 4. Главный цикл диспетчера (Distributor)
	m.logger.Info("Manager loop started. Waiting for market events...")
	for {
		select {
		case event, ok := <-priceUpdates:
			if !ok {
				m.logger.Error("Market stream channel closed externally. Stopping Manager.")
				return
			}

			// Логируем для отладки (в проде убрать level debug)
			// m.logger.Debug("Price Update", "symbol", event.Symbol, "price", event.Price)

			// Ищем задачи, которые сработали (фильтруем в памяти)
			var affectedTasks []*domain.Task
			for _, task := range activeTasks {
				if task.UnderlyingSymbol == event.Symbol && task.ShouldRoll(event.Price) {
					affectedTasks = append(affectedTasks, &task)
				}
			}

			if len(affectedTasks) > 0 {
				m.logger.Info("Trigger Fired!", "symbol", event.Symbol, "price", event.Price, "count", len(affectedTasks))
			}

			for _, task := range affectedTasks {
				// Отправляем в канал без блокировки (если воркеры захлебнулись, лучше пропустить тик, чем положить стрим)
				select {
				case m.jobChan <- jobDTO{Task: task, Price: event.Price}:
				default:
					m.logger.Warn("Worker pool overloaded! Dropping task execution.", "task_id", task.ID)
				}
			}

		case <-ctx.Done():
			m.logger.Info("Manager stopping...")
			return
		}
	}
}

// worker исполняет бизнес-логику
func (m *Manager) worker(ctx context.Context, id int) {
	m.logger.Debug("Worker started", "worker_id", id)
	for {
		select {
		case job := <-m.jobChan:
			m.logger.Info("Worker processing task", "worker_id", id, "task_id", job.Task.ID)

			// Получаем ключи (расшифровка внутри репо)
			apiKey, err := m.keyRepo.GetByID(ctx, job.Task.APIKeyID)
			if err != nil {
				m.logger.Error("Failed to get API key", "task_id", job.Task.ID, "err", err)
				continue
			}

			// Запускаем UseCase (Роллирование)
			// Важно: ExecuteRoll должен быть идемпотентным!
			err = m.roller.ExecuteRoll(ctx, *apiKey, job.Task, job.Price)
			if err != nil {
				m.logger.Error("Roll execution failed", "task_id", job.Task.ID, "err", err)
			} else {
				m.logger.Info("Roll executed successfully", "task_id", job.Task.ID)
			}

		case <-ctx.Done():
			return
		}
	}
}