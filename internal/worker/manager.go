package worker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/usecase"
	
	"github.com/shopspring/decimal"
)

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
	
	// --- Hot Reload State ---
	activeTasks []domain.Task // Кэш задач в памяти
	mu          sync.RWMutex  // Замок для защиты activeTasks от гонки данных
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
		jobChan:  make(chan jobDTO, 100),
	}
}

// ReloadTasks вызывает Handler, когда пользователь добавил задачу
func (m *Manager) ReloadTasks(ctx context.Context) error {
	m.logger.Info("🔄 Hot Reloading tasks...")

	// 1. Идем в базу за свежим списком
	newTasks, err := m.repo.GetActiveTasks(ctx)
	if err != nil {
		return err
	}

	// 2. Обновляем кэш под замком (Thread-Safe)
	m.mu.Lock()
	m.activeTasks = newTasks
	m.mu.Unlock()

	// 3. Собираем символы для подписки
	symbolMap := make(map[string]bool)
	for _, task := range newTasks {
		symbolMap[task.UnderlyingSymbol] = true
	}
	var symbols []string
	for sym := range symbolMap {
		symbols = append(symbols, sym)
	}

	// 4. Динамически подписываемся на WebSocket
	// Внимание: Этот метод требует обновления в интерфейсе MarketStreamer (см. Шаг 2 и 3)
	if len(symbols) > 0 {
		if err := m.streamer.AddSubscriptions(symbols); err != nil {
			m.logger.Error("Failed to add subscriptions", "err", err)
			return err
		}
	}
	
	m.logger.Info("✅ Tasks reloaded", "count", len(newTasks))
	return nil
}

func (m *Manager) Run(ctx context.Context) {
	m.logger.Info("Starting Manager: Event-Driven Mode")

	// Первичная загрузка
	if err := m.ReloadTasks(ctx); err != nil {
		m.logger.Error("Initial task load failed", "err", err)
	}

	// Подписка (даже если список пуст, запускаем слушателя)
	m.mu.RLock()
	initialSymbols := make([]string, 0)
	for _, t := range m.activeTasks {
		initialSymbols = append(initialSymbols, t.UnderlyingSymbol)
	}
	m.mu.RUnlock()

	priceUpdates, err := m.streamer.Subscribe(initialSymbols)
	if err != nil {
		m.logger.Error("CRITICAL: Failed to initialize stream", "err", err)
		return
	}

	// Воркеры
	for i := 0; i < 5; i++ {
		go m.worker(ctx, i)
	}

	// Loop
	m.logger.Info("Manager loop started.")
	for {
		select {
		case event, ok := <-priceUpdates:
			if !ok {
				return
			}

			// Читаем задачи под R-замком (параллельное чтение разрешено)
			m.mu.RLock()
			var affectedTasks []*domain.Task
			// Важно: activeTasks теперь актуален всегда
			for i := range m.activeTasks {
				// Берем указатель на задачу в слайсе, чтобы не копировать
				task := &m.activeTasks[i] 
				if task.UnderlyingSymbol == event.Symbol && task.ShouldRoll(event.Price) {
					affectedTasks = append(affectedTasks, task)
				}
			}
			m.mu.RUnlock()

			for _, task := range affectedTasks {
				select {
				case m.jobChan <- jobDTO{Task: task, Price: event.Price}:
				default:
					m.logger.Warn("Worker pool overloaded", "task_id", task.ID)
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) worker(ctx context.Context, id int) {
	for {
		select {
		case job := <-m.jobChan:
			apiKey, err := m.keyRepo.GetByID(ctx, job.Task.APIKeyID)
			if err != nil {
				continue
			}
			_ = m.roller.ExecuteRoll(ctx, *apiKey, job.Task, job.Price)
		case <-ctx.Done():
			return
		}
	}
}