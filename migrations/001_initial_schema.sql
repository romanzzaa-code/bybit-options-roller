-- Users: Клиенты бота
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    telegram_id BIGINT NOT NULL UNIQUE,
    username VARCHAR(255),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    is_banned BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- API Keys: Ключи биржи (шифрованные)
CREATE TABLE IF NOT EXISTS api_keys (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_enc VARCHAR(512) NOT NULL,
    secret_enc VARCHAR(512) NOT NULL,
    label VARCHAR(255),
    is_valid BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Tasks: Задачи на роллирование
CREATE TABLE IF NOT EXISTS tasks (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    
    -- То, чем торгуем (BTC-29DEC23-40000-C)
    target_symbol VARCHAR(50) NOT NULL,
    
    -- То, за чем следим (BTC) -- НОВОЕ ПОЛЕ
    underlying_symbol VARCHAR(20) NOT NULL,
    
    current_qty NUMERIC(32, 18) NOT NULL, -- Увеличил точность
    trigger_price NUMERIC(32, 18) NOT NULL,
    next_strike_step NUMERIC(32, 18) NOT NULL,
    
    status VARCHAR(20) NOT NULL DEFAULT 'IDLE',
    
    -- Для Optimistic Locking -- НОВОЕ ПОЛЕ
    version BIGINT NOT NULL DEFAULT 1,
    
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Индексы для быстрого поиска задач диспетчером
CREATE INDEX IF NOT EXISTS idx_tasks_status_underlying ON tasks(status, underlying_symbol);
CREATE INDEX IF NOT EXISTS idx_tasks_user_id ON tasks(user_id);