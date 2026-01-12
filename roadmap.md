
Техническая Спецификация: Bybit Options Roller SaaS
Версия: 1.0
Статус: Draft
Автор: Архитектор (Критик кода)
Язык реализации: Go (Golang 1.22+)

1. Обзор Системы (Executive Summary)
Проект представляет собой SaaS-бота для автоматического управления опционными позициями на бирже Bybit (Unified Trading Account).
Ключевая ценность: Автоматическое "роллирование" (перекладывание) проданных опционов при достижении цены базового актива определенного уровня, с защитой от ликвидации в режиме Portfolio Margin (PM).
Система спроектирована как мульти-тенантная (многопользовательская), где каждый пользователь работает изолированно. Управление осуществляется через Telegram Bot. Монетизация реализована через систему Лицензионных ключей.

2. Архитектура (High-Level Architecture)
Используется паттерн Modular Monolith с применением принципов Clean Architecture.
2.1. Компоненты системы
Interface Layer (Interfaces):
Telegram Adapter: Обработка команд от пользователей, вывод логов и уведомлений.
HTTP Server (Optional): Для Health checks и метрик (Prometheus).
Application Layer (Use Cases):
Auth & Billing: Управление подписками, активация ключей.
Strategy Engine: Оркестрация воркеров.
Market Stream: Единый источник котировок.
Domain Layer (Enterprise Rules):
Логика роллирования.
Правила PM (Portfolio Margin Guard).
Сущности: User, Position, Task.
Infrastructure Layer (Adapters):
Bybit Adapter: Обертка над V5 API (REST + WebSocket).
PostgreSQL: Персистентное хранение данных.
Memory Cache: Оперативная память для быстрых проверок подписок.
2.2. Модель Конкурентности и Обработка Событий (Event-Driven Concurrency)
Вместо наивной модели "One User = One Thread", которая приводит к исчерпанию ресурсов (O(N)), система использует паттерн Fan-Out / Fan-In с централизованным диспетчером. Это обеспечивает масштабируемость до тысяч активных задач при минимальном потреблении CPU.
Компоненты конвейера:
Market Stream (Singleton Ingestor):
Единственный процесс (goroutine), поддерживающий постоянное WebSocket-соединение с Bybit Public V5 API.
Отвечает за получение обновлений цены (Ticker/MarkPrice).
Дедупликация: Если 500 пользователей следят за ETH, стрим подписывается на тикер ETH только один раз.
Task Dispatcher (Event Bus):
Получает событие PriceUpdate от Market Stream.
Использует in-memory индекс (Thread-safe Map: map[Symbol][]TaskID) для мгновенного (O(1)) поиска задач, затронутых изменением цены.
Генерирует Job (контекст задачи + текущая цена) и отправляет в канал обработки.
Worker Pool:
Фиксированный набор воркеров (например, N = 50), которые разбирают очередь задач.
Воркер загружает агрегат задачи (Task Aggregate) из репозитория.
Выполняет "чистую" проверку условий (Domain Logic).
При необходимости инициирует транзакцию роллирования.
Global Rate Limiter:
Реализует алгоритм Token Bucket.
Гарантирует, что суммарное количество запросов от всех воркеров не превысит лимиты Bybit API (например, 10 req/sec на IP или API Key).





2.3. Контракты Доменного Уровня (Domain Contracts)
Согласно принципу инверсии зависимостей (DIP), бизнес-логика зависит только от абстракций. Реализация БД и API адаптеров пишется после утверждения этих интерфейсов.
Файл: internal/domain/interfaces.go
package domain

import (
	"context"
	"[github.com/shopspring/decimal](https://github.com/shopspring/decimal)" // Использовать для денег, никогда не float64
)

// --- Repository Interfaces (Порты к Хранилищу) ---

// TaskRepository управляет жизненным циклом задач.
// Реализация: PostgreSQL / In-Memory (для тестов).
type TaskRepository interface {
	// GetTasksBySymbol возвращает список ID задач, ожидающих триггера по данному символу.
	// Используется Dispatcher'ом для быстрого лукапа.
	GetTasksBySymbol(ctx context.Context, symbol string) ([]int64, error)

	// GetTaskByID загружает полный агрегат задачи (включая настройки пользователя).
	GetTaskByID(ctx context.Context, taskID int64) (*Task, error)

	// UpdateTaskState атомарно переводит задачу в новое состояние.
	// Обязательна поддержка Optimistic Locking (через version).
	UpdateTaskState(ctx context.Context, taskID int64, newState State, version int) error

	// SaveExecutionLog сохраняет историю переходов (audit trail).
	SaveExecutionLog(ctx context.Context, logEntry ExecutionLog) error
}

// --- Infrastructure Interfaces (Порты к Бирже) ---

// ExchangeAdapter абстрагирует API биржи.
// Никаких типов из bybit-sdk в аргументах! Только доменные DTO.
type ExchangeAdapter interface {
	// GetMarketPrice возвращает актуальную Mark Price.
	GetMarketPrice(ctx context.Context, symbol string) (decimal.Decimal, error)

	// GetPosition возвращает текущий размер позиции.
	// Возвращает (Qty=0), если позиции нет, а не ошибку.
	GetPosition(ctx context.Context, userID string, symbol string) (Position, error)

	// PlaceOrder отправляет ордер на биржу.
	// idempotencyKey обязателен для защиты от дублей при ретраях.
	PlaceOrder(ctx context.Context, req OrderRequest) (externalOrderID string, err error)

	// GetUnifiedMarginBalance возвращает данные о марже (MMR, Equity).
	GetUnifiedMarginBalance(ctx context.Context, userID string) (MarginInfo, error)
}

// --- Domain Services ---

// RiskEngine инкапсулирует правила безопасности (PM Guard).
type RiskEngine interface {
    // CanTrade проверяет, позволяет ли здоровье аккаунта совершать сделки.
    CanTrade(ctx context.Context, userID string) error
}




3. Модель Данных (Database Schema)
Используется PostgreSQL.
3.1. Таблица users (Клиенты)
Хранит состояние аккаунта и подписки.
SQL
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    telegram_id BIGINT UNIQUE NOT NULL, -- ID из Telegram
    username VARCHAR(100),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL, -- Дата окончания доступа
    is_banned BOOLEAN DEFAULT FALSE,    -- "Килл-свитч" от Админа
    created_at TIMESTAMP DEFAULT NOW()
);
CREATE INDEX idx_users_expires ON users(expires_at);


3.2. Таблица api_keys (Доступы)
Связь 1:N (Один юзер может иметь несколько аккаунтов Bybit).
SQL
CREATE TABLE api_keys (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    api_key VARCHAR(100) NOT NULL,
    api_secret_enc VARCHAR(500) NOT NULL, -- ENCRYPTED (AES-GCM)
    label VARCHAR(50), -- Например "Main Account"
    is_valid BOOLEAN DEFAULT TRUE -- Флаг валидности (выставляется ботом при проверке)
);


3.3. Таблица watch_tasks (Задачи на роллирование)
Хранит инструкции для робота.
SQL
CREATE TABLE watch_tasks (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE CASCADE,
    
    -- Целевая позиция
    target_symbol VARCHAR(100) NOT NULL, -- "ETH-30JAN26-3400-C"
    current_qty DECIMAL(20, 8) NOT NULL, -- Сколько контрактов
    
    -- Параметры стратегии
    trigger_price DECIMAL(20, 8) NOT NULL, -- Цена БА, при которой роллируем (3400)
    next_strike_step DECIMAL(20, 8) NOT NULL, -- Шаг следующего страйка (+100)
    
    -- Состояние
    status VARCHAR(20) DEFAULT 'ACTIVE', -- ACTIVE, ROLLING, COMPLETED, FAILED, PAUSED
    parent_task_id BIGINT REFERENCES watch_tasks(id), -- Ссылка на предка (история роллов)
    
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);


3.4. Таблица license_keys (Биллинг)
SQL
CREATE TABLE license_keys (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(100) UNIQUE NOT NULL, -- "PRO-30D-XXXX-YYYY"
    duration_days INT NOT NULL,        -- 30, 90, 365
    is_redeemed BOOLEAN DEFAULT FALSE,
    redeemed_by BIGINT REFERENCES users(id),
    redeemed_at TIMESTAMP,
    created_by VARCHAR(50) DEFAULT 'ADMIN'
);



4. Бизнес-Логика (Domain Logic)
4.1. Логика Воркера (UserWorker)
Воркер запускается при старте системы или при добавлении задачи.
Цикл работы:
Subscription Check (In-Memory):
Если time.Now() > worker.ExpiresAt -> Остановка.
Если канал stopChan получил сигнал -> Мгновенная остановка (Бан).
Price Update: Получение цены из общего стрима.
Condition Check: Если CurrentPrice >= TriggerPrice (для Call) или CurrentPrice <= TriggerPrice (для Put), инициировать процедуру ROLL.
4.2. Алгоритм Роллирования (State Machine / Saga)
Процесс роллирования является распределенной транзакцией, которая может быть прервана сетевым сбоем. Для обеспечения надежности используется Конечный Автомат (FSM). Состояние персистентно хранится в БД.
Диаграмма Состояний (States):
IDLE: Задача активна, ожидает триггера цены.
ROLL_INITIATED: Цена достигла триггера. Задача заблокирована для других воркеров.
CHECKING_MARGIN: Проверка Portfolio Margin (MMR < Limit).
LEG1_CLOSING: Отправлен ордер на закрытие текущей позиции (Buy-to-Close).
LEG1_CLOSED: Checkpoint. Старая позиция гарантированно закрыта.
LEG2_OPENING: Отправлен ордер на продажу новой позиции (Sell-to-Open).
COMPLETED: Роллирование завершено успешно. Задача переходит в IDLE с новыми параметрами.
FAILED_RETRYABLE: Сбой (сеть, timeout). Требуется повтор с текущего шага.
FAILED_FATAL: Критическая ошибка (нехватка маржи после закрытия). Требуется ручное вмешательство.
Логика переходов (Happy Path):
Event: Цена LastPrice >= TriggerPrice.
Action: Воркер переводит статус IDLE -> ROLL_INITIATED.
Step 1 (Risk): Запрос баланса. Если MMR > 85%, переход в FAILED_FATAL (алерт юзеру). Иначе -> LEG1_CLOSING.
Step 2 (Close Leg):
Формирование ордера: Symbol=OldStrike, Side=Buy, Type=Market, ReduceOnly=True.
Отправка в API.
Если успех -> LEG1_CLOSED.
Step 3 (Open Leg):
Формирование ордера: Symbol=NewStrike, Side=Sell, Type=Market.
Отправка в API.
Если успех -> COMPLETED.
Обработка Сбоев (Failure Recovery):
Сценарий А: Обрыв связи после отправки LEG1_CLOSING.
Воркер падает по таймауту. Состояние в БД остается LEG1_CLOSING.
Recovery Worker (фоновый процесс) видит зависшую задачу (> 1 мин).
Запрашивает статус ордера по orderLinkId (Idempotency Key).
Если Filled -> переводит в LEG1_CLOSED и продолжает процесс.
Если Cancelled/NotFound -> переводит обратно в IDLE.
Сценарий Б: Ошибка при открытии второй ноги (LEG2_OPENING).
Первая нога закрыта (убыток зафиксирован), но новая не открыта (нет премии). Это опасное состояние "Naked".
Система видит статус LEG1_CLOSED.
Действие: Бесконечные попытки открыть вторую ногу (Exponential Backoff) и немедленный критический алерт в Telegram пользователю. Останавливаться нельзя.
4.3. Система Ваучеров
Генерация: Только Админ (через ENV-переменную ADMIN_ID). Использует crypto/rand для создания строки.
Активация:
Транзакция БД с блокировкой строки (SELECT FOR UPDATE).
Идемпотентность: Нельзя активировать ключ дважды.
Защита от брутфорса: 3 ошибки подряд = временный бан на ввод команд.

5. Интерфейс (Telegram Bot UX)
5.1. Роли
Админ: Идентификация по Telegram ID (из конфига). Полный доступ.
Пользователь: Идентификация по записи в таблице users.
5.2. Команды Админа
/gen [days] — Создать ключ (например, /gen 30 -> KEY-30D-X7Z1).
/ban [id] — Забанить пользователя (мгновенный килл-свитч).
/stats — Статистика (сколько юзеров, сколько активных задач).
/logs [id] — Читать сырые логи конкретного юзера.
5.3. Команды Пользователя
User: /add
Bot: Запрашивает список позиций с Bybit (GET /v5/position/list).
Bot: Показывает кнопки с позициями:
[ETH-30JAN-3400-C (2.0)]
[BTC-29DEC-45000-P (0.5)]
User: Нажимает кнопку.
Bot: "При какой цене базового актива (Index Price) нужно выполнить роллирование? (Напишите число)"
User: 3410
Bot: "Какой шаг следующего страйка? (Например, 100 для ETH или 1000 для BTC)"
User: 100
Bot: "Задача принята. Слежу за 3400-С. Триггер: 3410. Следующий: 3500-С."


/status — Список активных задач и текущие цены.
/logs — Последние 10 сообщений о действиях робота (для спокойствия).

6. Безопасность (Security Requirements)
Zero Trust к БД: Админские права не хранятся в базе.
Шифрование: api_secret хранится в БД в зашифрованном виде (AES-256-GCM). Ключ шифрования лежит в ENV сервера.
Rate Limiting:
Telegram: Ограничение на ввод команд (чтобы не спамили).
Bybit: Лимитер на уровне приложения (10 req/sec на ключ).
Logging: В логи никогда не пишутся сырые API Secrets или ключи лицензий.

7. План реализации (Roadmap)
Этап 1: Ядро (Core)
[x] Настройка проекта Go (Clean Arch).
[x] Подключение к Bybit V5 (получение позиций, цены).
[x] Реализация логики ExecuteRoll (без базы данных, хардкод параметров).
Этап 2: База и Воркеры
[x] Поднятие PostgreSQL.
[x] Реализация UserWorker и менеджера горутин.
[ ] Интеграция с БД (чтение задач).
Этап 3: Telegram и Безопасность
[ ] Бот: команды /start, /keys.
[ ] Реализация шифрования ключей.
[ ] Система ваучеров и биллинг.
Этап 4: Тестирование и PM Guard
[ ] Тесты на Testnet Bybit.
[ ] Симуляция нехватки маржи (PM Guard).
[ ] Релиз v1.0.
