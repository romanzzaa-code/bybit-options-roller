package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
)

type RollerService interface {
	ExecuteRoll(ctx context.Context, apiKey domain.APIKey, task *domain.Task) error
}

type APIKeyRepository interface {
	GetByID(ctx context.Context, id int64) (*domain.APIKey, error)
}

type Manager struct {
	service    RollerService
	taskRepo   domain.TaskRepository
	apiKeyRepo APIKeyRepository
	logger     *slog.Logger
	interval   time.Duration
}

func NewManager(svc RollerService, tRepo domain.TaskRepository, kRepo APIKeyRepository, logger *slog.Logger, interval time.Duration) *Manager {
	return &Manager{
		service:    svc,
		taskRepo:   tRepo,
		apiKeyRepo: kRepo,
		logger:     logger,
		interval:   interval,
	}
}

func (m *Manager) Run(ctx context.Context) {
	m.logger.Info("Worker Manager started", slog.Duration("interval", m.interval))
	
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Worker Manager stopping, waiting for active rolls to finish...")
			wg.Wait()
			m.logger.Info("Worker Manager stopped")
			return

		case <-ticker.C:
			m.processBatch(ctx, &wg)
		}
	}
}

func (m *Manager) processBatch(ctx context.Context, wg *sync.WaitGroup) {
	// Создаем свой контекст для цикла выборки, чтобы не прерывать запросы к БД мгновенно
	// но роллы должны уважать глобальный ctx при graceful shutdown
	tasks, err := m.taskRepo.GetActiveTasks(ctx)
	if err != nil {
		m.logger.Error("Failed to fetch active tasks", slog.String("error", err.Error()))
		return
	}

	if len(tasks) == 0 {
		return
	}

	m.logger.Debug("Processing tasks", slog.Int("count", len(tasks)))

	for _, task := range tasks {
		wg.Add(1)
		go func(t domain.Task) {
			defer wg.Done()
			m.executeTask(ctx, t)
		}(task)
	}
}

func (m *Manager) executeTask(ctx context.Context, task domain.Task) {
	// Получаем API ключ для задачи
	// Важно: в реальном коде стоит добавить кэширование ключей, чтобы не долбить БД
	apiKey, err := m.apiKeyRepo.GetByID(ctx, task.APIKeyID)
	if err != nil {
		m.logger.Error("Failed to get API key", slog.Int64("task_id", task.ID), slog.String("error", err.Error()))
		// Если ключа нет - это фатальная ошибка
		_ = m.taskRepo.RegisterError(ctx, task.ID, err)
		return
	}
	
	if apiKey == nil {
		// Ключ не найден
		_ = m.taskRepo.RegisterError(ctx, task.ID, fmt.Errorf("api key not found"))
		return 
	}

	if err := m.service.ExecuteRoll(ctx, *apiKey, &task); err != nil {
		// Логирование уже есть внутри сервиса, здесь просто обработка завершения
		// Service.handleError уже должен был вызвать RegisterError
	}
}