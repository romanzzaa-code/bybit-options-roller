package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
	"github.com/romanzzaa/bybit-options-roller/internal/infrastructure/crypto"
	"github.com/shopspring/decimal"
)

type TaskRepository struct {
	db        *DB
	encryptor *crypto.Encryptor
}

func NewTaskRepository(db *DB, encryptor *crypto.Encryptor) *TaskRepository {
	return &TaskRepository{
		db:        db,
		encryptor: encryptor,
	}
}

func (r *TaskRepository) CreateTask(ctx context.Context, task *domain.Task) error {
	query := `
		INSERT INTO tasks (
			user_id, api_key_id, target_symbol, current_qty,
			trigger_price, next_strike_step, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING id
	`

	err := r.db.QueryRowContext(
		ctx, query,
		task.UserID, task.APIKeyID, task.TargetSymbol, task.CurrentQty,
		task.TriggerPrice, task.NextStrikeStep, task.Status,
	).Scan(&task.ID)

	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	return nil
}

func (r *TaskRepository) GetTaskByID(ctx context.Context, id int64) (*domain.Task, error) {
	query := `
		SELECT id, user_id, api_key_id, target_symbol, current_qty,
			   trigger_price, next_strike_step, status, last_error,
			   created_at, updated_at
		FROM tasks
		WHERE id = $1
	`

	task := &domain.Task{}
	var lastError sql.NullString

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&task.ID, &task.UserID, &task.APIKeyID, &task.TargetSymbol, &task.CurrentQty,
		&task.TriggerPrice, &task.NextStrikeStep, &task.Status, &lastError,
		&task.CreatedAt, &task.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	if lastError.Valid {
		task.LastError = lastError.String
	}

	return task, nil
}

func (r *TaskRepository) GetTasksBySymbol(ctx context.Context, symbol string) ([]domain.Task, error) {
	query := `
		SELECT id, user_id, api_key_id, target_symbol, current_qty,
			   trigger_price, next_strike_step, status, last_error,
			   created_at, updated_at
		FROM tasks
		WHERE target_symbol = $1 AND status = 'active'
	`

	rows, err := r.db.QueryContext(ctx, query, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks by symbol: %w", err)
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		var task domain.Task
		var lastError sql.NullString

		err := rows.Scan(
			&task.ID, &task.UserID, &task.APIKeyID, &task.TargetSymbol, &task.CurrentQty,
			&task.TriggerPrice, &task.NextStrikeStep, &task.Status, &lastError,
			&task.CreatedAt, &task.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		if lastError.Valid {
			task.LastError = lastError.String
		}

		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return tasks, nil
}

func (r *TaskRepository) GetActiveTasks(ctx context.Context) ([]domain.Task, error) {
	query := `
		SELECT id, user_id, api_key_id, target_symbol, current_qty,
			   trigger_price, next_strike_step, status, last_error,
			   created_at, updated_at
		FROM tasks
		WHERE status = 'active'
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active tasks: %w", err)
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		var task domain.Task
		var lastError sql.NullString

		err := rows.Scan(
			&task.ID, &task.UserID, &task.APIKeyID, &task.TargetSymbol, &task.CurrentQty,
			&task.TriggerPrice, &task.NextStrikeStep, &task.Status, &lastError,
			&task.CreatedAt, &task.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		if lastError.Valid {
			task.LastError = lastError.String
		}

		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return tasks, nil
}

func (r *TaskRepository) UpdateTaskStatus(ctx context.Context, id int64, status domain.TaskState, errMessage string) error {
	query := `
		UPDATE tasks
		SET status = $1, last_error = $2, updated_at = NOW()
		WHERE id = $3
	`

	var lastErr *string
	if errMessage != "" {
		lastErr = &errMessage
	}

	result, err := r.db.ExecContext(ctx, query, status, lastErr, id)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("task not found: %d", id)
	}

	return nil
}

func (r *TaskRepository) UpdateTaskSymbol(ctx context.Context, id int64, newSymbol string, newQty decimal.Decimal) error {
	query := `
		UPDATE tasks
		SET target_symbol = $1, current_qty = $2, updated_at = NOW()
		WHERE id = $3
	`

	result, err := r.db.ExecContext(ctx, query, newSymbol, newQty, id)
	if err != nil {
		return fmt.Errorf("failed to update task symbol: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("task not found: %d", id)
	}

	return nil
}

type APIKeyRepository struct {
	db        *DB
	encryptor *crypto.Encryptor
}

func NewAPIKeyRepository(db *DB, encryptor *crypto.Encryptor) *APIKeyRepository {
	return &APIKeyRepository{
		db:        db,
		encryptor: encryptor,
	}
}

func (r *APIKeyRepository) Create(ctx context.Context, apiKey *domain.APIKey) error {
	keyEnc, err := r.encryptor.Encrypt(apiKey.Key)
	if err != nil {
		return fmt.Errorf("failed to encrypt key: %w", err)
	}

	secretEnc, err := r.encryptor.Encrypt(apiKey.Secret)
	if err != nil {
		return fmt.Errorf("failed to encrypt secret: %w", err)
	}

	query := `
		INSERT INTO api_keys (user_id, key_enc, secret_enc, label, is_valid, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING id
	`

	err = r.db.QueryRowContext(
		ctx, query,
		apiKey.UserID, keyEnc, secretEnc, apiKey.Label, apiKey.IsValid,
	).Scan(&apiKey.ID)

	if err != nil {
		return fmt.Errorf("failed to create api key: %w", err)
	}

	return nil
}

func (r *APIKeyRepository) GetByID(ctx context.Context, id int64) (*domain.APIKey, error) {
	query := `
		SELECT id, user_id, key_enc, secret_enc, label, is_valid, created_at
		FROM api_keys
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, id)

	ak := &domain.APIKey{}
	var keyEnc, secretEnc string

	err := row.Scan(
		&ak.ID, &ak.UserID, &keyEnc, &secretEnc, &ak.Label, &ak.IsValid, &ak.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get api key: %w", err)
	}

	ak.Key, err = r.encryptor.Decrypt(keyEnc)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key: %w", err)
	}

	ak.Secret, err = r.encryptor.Decrypt(secretEnc)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt secret: %w", err)
	}

	return ak, nil
}

func (r *APIKeyRepository) GetByUserID(ctx context.Context, userID int64) ([]domain.APIKey, error) {
	query := `
		SELECT id, user_id, key_enc, secret_enc, label, is_valid, created_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get api keys: %w", err)
	}
	defer rows.Close()

	var keys []domain.APIKey
	for rows.Next() {
		ak := &domain.APIKey{}
		var keyEnc, secretEnc string

		err := rows.Scan(
			&ak.ID, &ak.UserID, &keyEnc, &secretEnc, &ak.Label, &ak.IsValid, &ak.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}

		ak.Key, _ = r.encryptor.Decrypt(keyEnc)
		ak.Secret, _ = r.encryptor.Decrypt(secretEnc)

		keys = append(keys, *ak)
	}

	return keys, nil
}

func (r *APIKeyRepository) Invalidate(ctx context.Context, id int64) error {
	query := `UPDATE api_keys SET is_valid = FALSE WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to invalidate api key: %w", err)
	}

	return nil
}

type UserRepository struct {
	db *DB
}

func NewUserRepository(db *DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	query := `
		INSERT INTO users (telegram_id, username, expires_at, is_banned, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id
	`

	err := r.db.QueryRowContext(
		ctx, query,
		user.TelegramID, user.Username, user.ExpiresAt, user.IsBanned,
	).Scan(&user.ID)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

func (r *UserRepository) GetByTelegramID(ctx context.Context, telegramID int64) (*domain.User, error) {
	query := `
		SELECT id, telegram_id, username, expires_at, is_banned, created_at
		FROM users
		WHERE telegram_id = $1
	`

	row := r.db.QueryRowContext(ctx, query, telegramID)

	user := &domain.User{}
	err := row.Scan(
		&user.ID, &user.TelegramID, &user.Username, &user.ExpiresAt, &user.IsBanned, &user.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

func (r *UserRepository) UpdateSubscription(ctx context.Context, telegramID int64, expiresAt time.Time) error {
	query := `UPDATE users SET expires_at = $1 WHERE telegram_id = $2`

	_, err := r.db.ExecContext(ctx, query, expiresAt, telegramID)
	if err != nil {
		return fmt.Errorf("failed to update subscription: %w", err)
	}

	return nil
}

func (r *UserRepository) IsActive(ctx context.Context, telegramID int64) (bool, error) {
	query := `SELECT expires_at FROM users WHERE telegram_id = $1`

	var expiresAt time.Time
	err := r.db.QueryRowContext(ctx, query, telegramID).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check subscription: %w", err)
	}

	return time.Now().Before(expiresAt), nil
}
