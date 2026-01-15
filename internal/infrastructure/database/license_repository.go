package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/romanzzaa/bybit-options-roller/internal/domain"
)

type LicenseRepository struct {
	db *DB
}

func NewLicenseRepository(db *DB) *LicenseRepository {
	return &LicenseRepository{db: db}
}

func (r *LicenseRepository) Generate(ctx context.Context, durationDays int) (*domain.LicenseKey, error) {
	code := generateLicenseCode(durationDays)

	query := `
		INSERT INTO license_keys (code, duration_days, created_by, created_at)
		VALUES ($1, $2, 'ADMIN', NOW())
		RETURNING id, created_at
	`

	lic := &domain.LicenseKey{
		Code:         code,
		DurationDays: durationDays,
		IsRedeemed:   false,
		CreatedBy:    "ADMIN",
	}

	err := r.db.QueryRowContext(ctx, query, code, durationDays).Scan(&lic.ID, &lic.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate license: %w", err)
	}

	return lic, nil
}

func (r *LicenseRepository) Redeem(ctx context.Context, code string, userID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lic domain.LicenseKey
	query := `SELECT id, duration_days, is_redeemed FROM license_keys WHERE code = $1 FOR UPDATE`
	err = tx.QueryRowContext(ctx, query, code).Scan(&lic.ID, &lic.DurationDays, &lic.IsRedeemed)
	if err == sql.ErrNoRows {
		return fmt.Errorf("license not found")
	}
	if err != nil {
		return err
	}

	if lic.IsRedeemed {
		return fmt.Errorf("license already redeemed")
	}

	updateLic := `UPDATE license_keys SET is_redeemed = TRUE, redeemed_by = $1, redeemed_at = NOW() WHERE id = $2`
	if _, err := tx.ExecContext(ctx, updateLic, userID, lic.ID); err != nil {
		return err
	}

	newExpiry := time.Now().Add(time.Duration(lic.DurationDays) * 24 * time.Hour)
	updateUser := `UPDATE users SET expires_at = $1 WHERE id = $2`
	if _, err := tx.ExecContext(ctx, updateUser, newExpiry, userID); err != nil {
		return err
	}

	return tx.Commit()
}

func generateLicenseCode(days int) string {
	entropy := make([]byte, 6)
	rand.Read(entropy)
	suffix := hex.EncodeToString(entropy)[:8]
	return fmt.Sprintf("PRO-%dD-%s", days, suffix)
}