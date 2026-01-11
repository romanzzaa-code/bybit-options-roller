# KODA.md — Инструкции для работы с проектом Bybit Options Roller

## 1. Общая информация о проекте

### Назначение проекта
**Bybit Options Roller** — это SaaS-бот для автоматического роллирования опционных позиций на криптобирже Bybit (Unified Trading Account). Бот отслеживает достижение триггерной цены опциона и автоматически выполняет роллирование: закрывает текущую позицию (Leg 1) и открывает новую с другим страйком (Leg 2).

### Текущий статус
- **Этап разработки:** Этап 2 завершён (Database-интеграция)
- **Версия Go:** 1.25.5
- **Тестовая сеть:** Bybit Testnet (по умолчанию)
- **Текущее состояние:** PostgreSQL подключен, репозитории реализованы, API-ключи шифруются

---

## 2. Архитектура проекта

### Структура каталогов (Clean Architecture)

```
bybit-options-roller/
├── cmd/
│   └── bot/
│       └── main.go              # Точка входа, инициализация, production loop
├── internal/
│   ├── config/
│   │   └── config.go            # Загрузка конфигурации из ENV
│   ├── domain/
│   │   ├── models.go            # Сущности (Task, User, APIKey, Position)
│   │   └── interfaces.go        # Контракты (Repository, Adapter, Service)
│   ├── infrastructure/
│   │   ├── bybit/
│   │   │   ├── client.go        # Реализация ExchangeAdapter (API Bybit V5)
│   │   │   └── dto.go           # DTO для парсинга JSON-ответов API
│   │   ├── database/
│   │   │   ├── connection.go    # Подключение к PostgreSQL
│   │   │   └── repository.go    # Репозитории (Task, APIKey, User)
│   │   └── crypto/
│   │       └── encryptor.go     # Шифрование API-ключей (AES-256-GCM)
│   └── usecase/
│       └── roller.go            # Бизнес-логика роллирования
├── migrations/
│   └── 001_initial_schema.sql  # Миграции PostgreSQL
├── docker-compose.yml           # PostgreSQL
├── go.mod
└── Makefile                     # Команды сборки и запуска
```

### Ключевые технологии
| Компонент | Технология |
|-----------|------------|
| Язык программирования | Go 1.25.5+ |
| Финансовые расчёты | `github.com/shopspring/decimal` |
| API биржи | Bybit V5 REST API |
| База данных | PostgreSQL 16 |
| Драйвер БД | `github.com/lib/pq` |
| UUID | `github.com/google/uuid` |
| Аутентификация API | HMAC-SHA256 |
| Шифрование ключей | AES-256-GCM |

---

## 3. Сборка и запуск проекта

### Предварительные требования

1. **Docker** для PostgreSQL
2. **Go 1.25.5+**

### Запуск PostgreSQL

```bash
# Поднять контейнер
make docker-up

# Выполнить миграции
make migrate-up

# Остановить контейнер
make docker-down
```

**Параметры подключения:**
| Параметр | Значение |
|----------|----------|
| Host | `localhost` |
| Port | `5432` |
| User | `bybit_roller` |
| Password | `secret_password` |
| Database | `bybit_roller` |

### Основные команды

```bash
# Сборка проекта
make build

# Запуск в режиме разработки
make run

# Очистка бинарных файлов
make clean

# Форматирование кода
make fmt

# Запуск тестов
make test

# Обновление зависимостей
make deps
```

### Переменные окружения

| Переменная | Описание | По умолчанию |
|------------|----------|--------------|
| `ENV` | Режим работы (`local`/`prod`) | `local` |
| `BYBIT_TESTNET` | Использовать Testnet | `true` |
| `ENCRYPTION_KEY` | Ключ шифрования API-ключей (32 hex-символа) | `""` (обязательно для продакшена) |
| `DB_HOST` | Хост PostgreSQL | `localhost` |
| `DB_PORT` | Порт PostgreSQL | `5432` |
| `DB_USER` | Пользователь БД | `bybit_roller` |
| `DB_PASSWORD` | Пароль БД | `secret_password` |
| `DB_NAME` | Имя БД | `bybit_roller` |
| `DB_SSLMODE` | SSL режим | `disable` |

**Генерация ключа шифрования:**
```bash
export ENCRYPTION_KEY=$(openssl rand -hex 32)
```

---

## 4. Правила разработки

### Стиль кодирования

1. **Именование:**
   - Интерфейсы: `~er` суффикс (`TaskRepository`, `ExchangeAdapter`)
   - Структуры: CamelCase с заглавной буквы (экспортируемые)
   - Приватные поля: нижнее подчёркивание НЕ используется

2. **Обработка ошибок:**
   - Возвращать ошибки через `error` интерфейс
   - Использовать `fmt.Errorf` с `%w` для обёртывания контекста
   - Критические ошибки в `main()` — `log.Fatalf`

3. **Финансовые расчёты:**
   - **Обязательно** использовать `decimal.Decimal`
   - НЕ использовать `float64` для денежных сумм

4. **Логирование:**
   - Стандартный `log` пакет
   - Префиксы: `[Main]`, `[Roller]`, `[Leg 1]`, `[Leg 2]`

### Структура таблиц БД

**users** — Пользователи бота
```sql
id          BIGSERIAL PRIMARY KEY
telegram_id BIGINT NOT NULL UNIQUE
username    VARCHAR(255)
expires_at  TIMESTAMP WITH TIME ZONE NOT NULL
is_banned   BOOLEAN NOT NULL DEFAULT FALSE
created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
```

**api_keys** — API-ключи Bybit (шифруются)
```sql
id          BIGSERIAL PRIMARY KEY
user_id     BIGINT NOT NULL REFERENCES users(id)
key_enc     VARCHAR(512) NOT NULL      -- Зашифрованный API Key
secret_enc  VARCHAR(512) NOT NULL      -- Зашифрованный Secret
label       VARCHAR(255)
is_valid    BOOLEAN NOT NULL DEFAULT TRUE
created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
```

**tasks** — Задачи на роллирование
```sql
id              BIGSERIAL PRIMARY KEY
user_id         BIGINT NOT NULL REFERENCES users(id)
api_key_id      BIGINT NOT NULL REFERENCES api_keys(id)
target_symbol   VARCHAR(50) NOT NULL
current_qty     NUMERIC(20, 10) NOT NULL
trigger_price   NUMERIC(20, 10) NOT NULL
next_strike_step NUMERIC(20, 10) NOT NULL
status          VARCHAR(20) NOT NULL DEFAULT 'active'
last_error      TEXT
created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
```

---

## 5. Модули и их назначение

### 5.1 Конфигурация (`internal/config/config.go`)

**Структура `Config`:**
```go
type Config struct {
    Env          string
    BybitTestnet bool
    Database     DatabaseConfig
    Crypto       CryptoConfig
}

type DatabaseConfig struct {
    Host, Port, User, Password, DBName, SSLMode string
}

type CryptoConfig struct {
    EncryptionKey string
}
```

**Методы:**
| Метод | Описание |
|-------|----------|
| `LoadConfig()` | Загружает конфигурацию из ENV-переменных |
| `DatabaseConfig.ConnectString()` | Возвращает DSN для PostgreSQL |

### 5.2 Криптография (`internal/infrastructure/crypto/encryptor.go`)

**Структура `Encryptor`:**
```go
type Encryptor struct {
    key []byte  // 32 байта
}
```

**Методы:**
| Метод | Описание |
|-------|----------|
| `NewEncryptor(hexKey string)` | Создаёт encryptor с 32-байтовым ключом |
| `Encrypt(plaintext string)` | Шифрует строку (AES-256-GCM), возвращает hex |
| `Decrypt(ciphertextHex string)` | Расшифровывает hex-строку |

**Пример использования:**
```go
encryptor, _ := crypto.NewEncryptor(os.Getenv("ENCRYPTION_KEY"))
encrypted, _ := encryptor.Encrypt("my-secret-key")
decrypted, _ := encryptor.Decrypt(encrypted)
```

### 5.3 База данных (`internal/infrastructure/database/`)

**connection.go — Подключение:**
```go
type DB struct { *sql.DB }

func NewConnection(cfg Config) (*DB, error)
```

**repository.go — Репозитории:**

| Репозиторий | Методы |
|-------------|--------|
| `TaskRepository` | `CreateTask`, `GetTaskByID`, `GetTasksBySymbol`, `GetActiveTasks`, `UpdateTaskStatus`, `UpdateTaskSymbol` |
| `APIKeyRepository` | `Create`, `GetByID`, `GetByUserID`, `Invalidate` |
| `UserRepository` | `Create`, `GetByTelegramID`, `UpdateSubscription`, `IsActive` |

### 5.4 Домен (`internal/domain/`)

**models.go — Сущности:**
```go
type Task struct {
    ID             int64
    UserID         int64
    APIKeyID       int64
    TargetSymbol   string
    CurrentQty     decimal.Decimal
    TriggerPrice   decimal.Decimal
    NextStrikeStep decimal.Decimal
    Status         TaskState  // active, paused, error, disabled
    LastError      string
    CreatedAt, UpdatedAt time.Time
}

type APIKey struct {
    ID        int64
    UserID    int64
    Key       string      // Расшифрованный
    Secret    string      // Расшифрованный
    Label     string
    IsValid   bool
    CreatedAt time.Time
}

type User struct {
    ID, TelegramID int64
    Username       string
    ExpiresAt      time.Time
    IsBanned       bool
    CreatedAt      time.Time
}
```

**interfaces.go — Контракты:**
```go
type TaskRepository interface { /* ... */ }
type APIKeyRepository interface { /* ... */ }
type UserRepository interface { /* ... */ }
type ExchangeAdapter interface { /* ... */ }
type NotificationService interface { /* ... */ }
```

### 5.5 Бизнес-логика (`internal/usecase/roller.go`)

**Структура `RollerService`:**
```go
type RollerService struct {
    exchange  domain.ExchangeAdapter
    taskRepo  domain.TaskRepository
    notifySvc domain.NotificationService
}
```

**Методы:**
| Метод | Описание |
|-------|----------|
| `ExecuteRoll(ctx, apiKey, task)` | Выполняет роллирование позиции |
| `WithTaskRepo(repo)` | Устанавливает TaskRepository |
| `WithNotifySvc(svc)` | Устанавливает NotificationService |
| `calculateNextSymbol(...)` | Вычисляет следующий тикер |

**Алгоритм ExecuteRoll:**
1. Получить Mark Price
2. Проверить триггер (Call: ≥, Put: ≤)
3. Получить текущую позицию
4. **Leg 1:** Закрыть позицию (`ReduceOnly = true`)
5. Вычислить новый тикер
6. **Leg 2:** Открыть новую позицию
7. Обновить задачу в БД

### 5.6 Инфраструктура Bybit (`internal/infrastructure/bybit/`)

**client.go — ExchangeAdapter:**
| Метод | Описание |
|-------|----------|
| `GetMarkPrice(ctx, symbol)` | Получение Mark Price |
| `GetPosition(ctx, creds, symbol)` | Получение позиции |
| `PlaceOrder(ctx, creds, req)` | Создание ордера |
| `GetMarginInfo(ctx, creds)` | Данные о марже |

---

## 6. Текущее состояние

### ✅ Что реализовано
- Базовая архитектура Clean Architecture
- Подключение к Bybit Testnet API
- PostgreSQL + Docker Compose
- Миграции (users, api_keys, tasks)
- Репозитории с CRUD операциями
- Шифрование API-ключей (AES-256-GCM)
- Конфигурация через ENV-переменные
- Production loop с проверкой задач каждые 30 сек

### ⏳ Следующие шаги (Этап 3)
- Telegram-бот для пользователей
- Админ-панель
- Уведомления о сделках
- Реальный режим (Mainnet)

---

## 7. Рекомендации для продолжения

### При добавлении новых функций

1. **Следовать Clean Architecture:**
   - Логика → `internal/usecase/`
   - Внешние интеграции → `internal/infrastructure/`
   - Модели и интерфейсы → `internal/domain/`

2. **Все зависимости инжектировать через интерфейсы** — упрощает тестирование

3. **Тестировать бизнес-логику:**
   - Создать unit-тесты для `roller.go`
   - Мокировать `ExchangeAdapter`

4. **Не хардкодить** — все константы в конфигурацию

### Частые ошибки

| Ошибка | Как избежать |
|--------|--------------|
| Потеря позиции после Leg 1 | Leg 2 должен быть атомарным; добавить алерт при критических ошибках |
| Неточные финансовые расчёты | Всегда использовать `decimal.Decimal` |
| Пустой `ENCRYPTION_KEY` | В продакшене ключ обязателен |

---

## 8. Полезные ссылки

- **Документация Bybit V5 API:** https://bybit-exchange.github.io/docs/v5/intro
- **Библиотека decimal:** https://github.com/shopspring/decimal
- **PostgreSQL:** https://www.postgresql.org/docs/
- **Clean Architecture:** https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html

---

*Документ обновлён: 11.01.2026 (Этап 2 завершён)*
не пиши комментарии в коде