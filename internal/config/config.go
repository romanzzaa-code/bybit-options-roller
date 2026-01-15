package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env          string
	BybitTestnet bool
	Bybit        BybitConfig
	Database     DatabaseConfig
	Crypto       CryptoConfig
	Telegram     TelegramConfig
}

type BybitConfig struct {
	BaseURL string
	Timeout time.Duration
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

type CryptoConfig struct {
	EncryptionKey string
}

type TelegramConfig struct {
	BotToken string
	AdminID  int64
}

func (d *DatabaseConfig) ConnectString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode,
	)
}

func LoadConfig() (*Config, error) {
	env := getEnv("ENV", "local")
	testnet := getEnvBool("BYBIT_TESTNET", true)

	timeoutStr := getEnv("BYBIT_TIMEOUT_SECONDS", "5")
	timeoutSec, _ := strconv.Atoi(timeoutStr)
	if timeoutSec == 0 {
		timeoutSec = 5
	}

	bybitConfig := BybitConfig{
		Timeout: time.Duration(timeoutSec) * time.Second,
	}

	dbConfig := DatabaseConfig{
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnvInt("DB_PORT", 5432),
		User:     getEnv("DB_USER", "bybit_roller"),
		Password: getEnv("DB_PASSWORD", "secret_password"),
		DBName:   getEnv("DB_NAME", "bybit_roller"),
		SSLMode:  getEnv("DB_SSLMODE", "disable"),
	}

	cryptoConfig := CryptoConfig{
		EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
	}

	telegramConfig := TelegramConfig{
		BotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		AdminID:  getEnvInt64("ADMIN_TELEGRAM_ID", 0),
	}

	return &Config{
		Env:          env,
		BybitTestnet: testnet,
		Bybit:        bybitConfig,
		Database:     dbConfig,
		Crypto:       cryptoConfig,
		Telegram:     telegramConfig,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		b, err := strconv.ParseBool(value)
		if err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		v, err := strconv.Atoi(value)
		if err == nil {
			return v
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		v, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return v
		}
	}
	return defaultValue
}