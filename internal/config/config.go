package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config содержит настройки приложения
type Config struct {
	APIURL   string
	APIKey   string
	Model    string
}

// Load загружает конфигурацию из различных источников
func Load() (*Config, error) {
	// Настройка viper для загрузки конфигурации
	viper.SetConfigName("hint")
	viper.SetConfigType("yaml")
	
	// Пути для поиска файлов конфигурации
	homeDir, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(homeDir, ".config"))
	}
	viper.AddConfigPath(".")
	
	// Установка переменных окружения
	viper.SetEnvPrefix("HINT")
	viper.AutomaticEnv()
	
	// Загрузка конфигурации из файла (если есть)
	_ = viper.ReadInConfig()
	
	// Создание конфигурации
	cfg := &Config{
		APIURL:   viper.GetString("api_url"),
		APIKey:   viper.GetString("api_key"),
		Model:    viper.GetString("model"),
	}
	
	// Установка значений по умолчанию
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.openai.com/v1"
	}
	
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("OPENAI_API_KEY")
	}
	
	if cfg.Model == "" {
		cfg.Model = "gpt-4"
	}
	
	// Проверка обязательных параметров
	if cfg.APIKey == "" {
		return nil, errors.New("необходимо указать API-ключ через --api-key, HINT_API_KEY или OPENAI_API_KEY")
	}
	
	return cfg, nil
} 