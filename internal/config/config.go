package config

import (
	"os"
)

// Config - глобальная конфигурация бота
type Config struct {
	Env          string // "local", "prod"
	PostgresDSN  string // Ссылка для подключения к БД
	BybitTestnet bool   // Использовать ли Testnet
}

// LoadConfig - загружает настройки (пока хардкод для старта, потом прикрутим os.Getenv)
func LoadConfig() (*Config, error) {
	return &Config{
		Env:          "local",
		PostgresDSN:  os.Getenv("DATABASE_URL"), // Будем брать из ENV
		BybitTestnet: true,                      // Пока безопасный режим по умолчанию
	}, nil
}