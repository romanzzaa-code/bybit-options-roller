package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"bybit-options-roller/internal/domain"
	"bybit-options-roller/internal/usecase"
)

type Manager struct {
	repo           domain.TaskRepository
	keyRepo        domain.APIKeyRepository
	roller         *usecase.RollerService
	marketProvider domain.MarketProvider
	logger         *slog.Logger
	streamer domain.MarketStreamer // Зависимость от интерфейса!
    taskChan chan domain.Task      // Внутренняя очередь задач (Buffer)

	// activeTasks: ключ — UnderlyingSymbol (напр. "ETH"), значение — список задач
	activeTasks map[string][]*domain.Task
	mu          sync.RWMutex
}

func NewManager(
	tr domain.TaskRepository, 
	kr domain.APIKeyRepository, // Передаем второй репозиторий
	roller *usecase.RollerService, 
	mp domain.MarketProvider, 
	logger *slog.Logger,
) *Manager {
	return &Manager{
		taskRepo:       tr,
		keyRepo:        kr,
		roller:         roller,
		marketProvider: mp,
		logger:         logger,
		activeTasks:    make(map[string][]*domain.Task),
	}
}

// Start запускает жизненный цикл менеджера
func (m *Manager) Run(ctx context.Context) {
    // 1. Подписываемся на поток цен (получаем список активных символов из БД)
    activeSymbols := m.repo.GetActiveSymbols(ctx)
    priceUpdates, _ := m.streamer.Subscribe(activeSymbols)

    // 2. Запускаем Worker Pool (Например, 5 воркеров)
    for i := 0; i < 5; i++ {
        go m.worker(ctx)
    }

    // 3. Dispatcher Loop (Главный цикл)
    for {
        select {
        case event := <-priceUpdates:
            // ЛОГИКА ДИСПЕТЧЕРА:
            // Пришла цена по ETH. Ищем задачи, которым интересен ETH.
            // Это "Hot Path", здесь должно быть быстро.
            affectedTasks := m.repo.FindTasksByTrigger(ctx, event.Symbol, event.Price)
            
            for _, task := range affectedTasks {
                m.taskChan <- task // Отправляем воркерам
            }
            
        case <-ctx.Done():
            return
        }
    }
}

// handlePriceUpdate находит все задачи для пришедшего тикера и запускает их проверку
func (m *Manager) handlePriceUpdate(ctx context.Context, update domain.PriceUpdate) {
	m.mu.RLock()
	tasks, ok := m.activeTasks[update.Symbol]
	m.mu.RUnlock()

	if !ok || len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		// Здесь мы передаем в RollerService и саму задачу, и актуальную цену из воркера.
		// Нам нужно сначала получить API ключи пользователя (в реальном коде это через repo)
		apiKey, err := m.repo.GetAPIKeyByUserID(ctx, task.UserID)
		if err != nil {
			m.logger.Error("Failed to get API key for task", "task_id", task.ID, "err", err)
			continue
		}

		// Вызываем UseCase
		if err := m.roller.ExecuteRoll(ctx, apiKey, task, update.Price); err != nil {
			m.logger.Error("Roll execution failed", "task_id", task.ID, "err", err)
		}
	}
}

// refreshTasks вычитывает активные задачи из БД и обновляет внутренний кэш
func (m *Manager) refreshTasks(ctx context.Context) error {
	tasks, err := m.repo.GetActiveTasks(ctx)
	if err != nil {
		m.logger.Error("Failed to fetch tasks from DB", "err", err)
		return err
	}

	newMap := make(map[string][]*domain.Task)
	for _, t := range tasks {
		newMap[t.UnderlyingSymbol] = append(newMap[t.UnderlyingSymbol], t)
	}

	m.mu.Lock()
	m.activeTasks = newMap
	m.mu.Unlock()

	m.logger.Debug("Task cache refreshed", "active_count", len(tasks))
	return nil
}

func (m *Manager) getUniqueSymbols() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	symbols := make([]string, 0, len(m.activeTasks))
	for s := range m.activeTasks {
		symbols = append(symbols, s)
	}
	return symbols
}