package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config contains application settings
type Config struct {
	APIURL   string
	APIKey   string
	Model    string
}

// Load loads configuration from various sources
func Load() (*Config, error) {
	// Configure viper for configuration loading
	viper.SetConfigName("hint")
	viper.SetConfigType("yaml")
	
	// Paths to search for configuration files
	homeDir, err := os.UserHomeDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(homeDir, ".config"))
	}
	viper.AddConfigPath(".")
	
	// Setting environment variables
	viper.SetEnvPrefix("HINT")
	viper.AutomaticEnv()
	
	// Loading configuration from file (if exists)
	_ = viper.ReadInConfig()
	
	// Creating configuration
	cfg := &Config{
		APIURL:   viper.GetString("api_url"),
		APIKey:   viper.GetString("api_key"),
		Model:    viper.GetString("model"),
	}
	
	// Setting default values
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.openai.com/v1"
	}
	
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("OPENAI_API_KEY")
	}
	
	if cfg.Model == "" {
		cfg.Model = "gpt-4"
	}
	
	// Checking required parameters
	if cfg.APIKey == "" {
		return nil, errors.New("API key must be specified using --api-key, HINT_API_KEY, or OPENAI_API_KEY")
	}
	
	return cfg, nil
} 