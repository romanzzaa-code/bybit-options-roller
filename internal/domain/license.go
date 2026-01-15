package domain

import "time"

type LicenseKey struct {
	ID           int64
	Code         string
	DurationDays int
	IsRedeemed   bool
	RedeemedBy   *int64
	RedeemedAt   *time.Time
	CreatedBy    string
	CreatedAt    time.Time
}