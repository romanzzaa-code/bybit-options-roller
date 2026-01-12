package config

import (
	"time"
	"os"
	"strconv"
)

type Config struct {
	Env          string
	BybitTestnet bool
	Bybit        BybitConfig
	Database     DatabaseConfig
	Crypto       CryptoConfig
}

type BybitConfig struct{
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

func (d *DatabaseConfig) ConnectString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode,
	)
}

type CryptoConfig struct {
	EncryptionKey string
}

func LoadConfig() (*Config, error) {
	env := getEnv("ENV", "local")
	testnet := getEnvBool("BYBIT_TESTNET", true)

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

	return &Config{
		Env:          env,
		BybitTestnet: testnet,
		Database:     dbConfig,
		Crypto:       cryptoConfig,
	}, nil
}

func MustLoad() *Config {
	// Заглушка для загрузки из ENV
	timeoutStr := os.Getenv("BYBIT_TIMEOUT_SECONDS")
	timeoutSec, _ := strconv.Atoi(timeoutStr)
	if timeoutSec == 0 {
		timeoutSec = 5 // Default
	}

	return &Config{
		Env: "local",
		Bybit: BybitConfig{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
		BybitTestnet: true, // Example
		// ... init other fields
	}
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