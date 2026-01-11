-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    telegram_id BIGINT NOT NULL UNIQUE,
    username VARCHAR(255),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW() + INTERVAL '30 days',
    is_banned BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_telegram_id ON users(telegram_id);
CREATE INDEX idx_users_expires_at ON users(expires_at);

CREATE TABLE IF NOT EXISTS api_keys (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_enc VARCHAR(512) NOT NULL,
    secret_enc VARCHAR(512) NOT NULL,
    label VARCHAR(255),
    is_valid BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_api_keys_is_valid ON api_keys(is_valid);

CREATE TABLE IF NOT EXISTS tasks (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    target_symbol VARCHAR(50) NOT NULL,
    current_qty NUMERIC(20, 10) NOT NULL,
    trigger_price NUMERIC(20, 10) NOT NULL,
    next_strike_step NUMERIC(20, 10) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tasks_user_id ON tasks(user_id);
CREATE INDEX idx_tasks_api_key_id ON tasks(api_key_id);
CREATE INDEX idx_tasks_target_symbol ON tasks(target_symbol);
CREATE INDEX idx_tasks_status ON tasks(status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;

-- +goose StatementEnd