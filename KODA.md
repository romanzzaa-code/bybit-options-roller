# KODA.md — Инструкции для работы с проектом Bybit Options Roller

## 1. Общая информация о проекте

### Назначение проекта
**Bybit Options Roller** — SaaS-бот для хеджирования и роллирования опционов.
**Критическая важность:** Работа с реальными депозитами. Любая ошибка в логике, задержка или потеря состояния недопустимы.

### Текущий статус
- **Этап:** 2.5 (Refactoring & Core Hardening)
- **Архитектура:** Modular Monolith / Clean Architecture
- **Состояние:** Ядро (Domain/UseCase/DB) переписано и готово. Ожидается реализация Event Engine.

---

## 2. Архитектура (Strict Guidelines)

⚠️ **ВНИМАНИЕ:** Архитектура была изменена для обеспечения безопасности.
**ЗАПРЕЩЕНО:**
1. Возвращать polling (цикл `for` с `sleep`) для проверки цен. Только **WebSocket**.
2. Убирать Optimistic Locking (`version`) из репозитория.
3. Использовать `float64` для денег (только `decimal.Decimal`).
4. Менять логику `ShouldRoll`: триггер всегда проверяется по **Index Price** (Underlying), а не по цене опциона.

### Структура (Обновленная)
bybit-options-roller/ ├── cmd/bot/main.go # Init -> WaitForSignal (No loops here!) ├── internal/ │ ├── domain/ # PURE GO. No deps. State Machine logic. │ ├── infrastructure/ │ │ ├── bybit/ # API V5 (GetIndexPrice, PlaceOrder) │ │ └── database/ # PostgreSQL with Optimistic Locking │ ├── usecase/ # Saga Pattern (ExecuteRoll with checkpoints) │ └── engine/ # [TODO] WebSocket & Dispatcher


---

## 3. Ключевые Механизмы (Реализовано)

### 3.1. Принятие решений (Domain)
Логика "Роллировать или нет" находится в `internal/domain/models.go`:
```go
func (t *Task) ShouldRoll(indexPrice decimal.Decimal) bool
Бот следит за UnderlyingSymbol (BTC), а торгует CurrentOptionSymbol (BTC-29DEC-C).

3.2. Безопасность данных (Concurrency)

В БД используется Optimistic Locking. При каждом обновлении: UPDATE tasks SET status = $1, version = version + 1 WHERE id = $2 AND version = $3 Если RowsAffected == 0, значит другой воркер перехватил задачу. Транзакция отменяется.

3.3. State Machine (Saga)

Роллирование — это не одно действие, а цепочка:

IDLE -> Триггер сработал -> ROLL_INITIATED (Lock)

ROLL_INITIATED -> PlaceOrder(Close) -> LEG1_CLOSED (Checkpoint)

LEG1_CLOSED -> PlaceOrder(Open) -> IDLE (New Symbol)

Если бот упадет на статусе LEG1_CLOSED, при рестарте Recovery Service должен продолжить работу.

4. План работ (Roadmap)
✅ Сделано (Done)

[x] Clean Architecture Layers (Domain, UseCase, Infra)

[x] Database Schema v2 (UnderlyingSymbol, Version)

[x] API Key Encryption (AES-256)

[x] Roller Logic: Saga Pattern + Safety Checks

[x] Bybit Client: GetIndexPrice implemented

⏳ Нужно сделать (To Do)

Market Stream Engine:

Подключение к Bybit WebSocket (Public Tickers).

Трансляция цен в Go-каналы.

Task Dispatcher:

In-Memory Map для быстрого поиска задач по символу.

Запуск RollerService.ExecuteRoll в отдельных горутинах.

Recovery Service:

Проверка "зависших" задач при старте (LEG1_CLOSED).

5. Правила для AI-агента
Не ломай интерфейсы. Если меняешь TaskRepository, проверь usecase.

Сначала тесты/компиляция. Перед тем как сказать "готово", запусти go build ./....

Безопасность превыше всего. Если видишь место, где можно потерять деньги (race condition, naked position) — кричи об этом.

Последнее обновление: 11.01.2026 by Code Critic
не пиши комментарии в коде