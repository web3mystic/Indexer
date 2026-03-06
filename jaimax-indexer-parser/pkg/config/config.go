package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config for your jaimax-1 PoA chain
type Config struct {
	GRPCEndpoint string
	ChainID      string

	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	// Indexer settings
	StartHeight   int64
	BatchSize     int
	WorkerCount   int
	RetryAttempts int
	RetryDelayMs  int

	// PoA-specific settings
	ValidatorSyncInterval int  // Sync validators every N blocks (default: 100)
	EnablePoATracking     bool // Enable PoA authority tracking
}

// LoadFromEnv loads configuration from environment variables
// Matches your exact .env file
func LoadFromEnv() (*Config, error) {
	cfg := &Config{
		// Chain settings
		GRPCEndpoint: getEnv("GRPC_ENDPOINT", "localhost:9090"),
		ChainID:      getEnv("CHAIN_ID", "jaimax-1"),

		// Database settings
		DBHost:     getEnv("DB_HOST", "/var/run/postgresql"),
		DBPort:     getEnvInt("DB_PORT", 5432),
		DBUser:     getEnv("DB_USER", "jai"),
		DBPassword: getEnv("DB_PASSWORD", "password"),
		DBName:     getEnv("DB_NAME", "jaimax_indexer"),

		// Indexer settings
		StartHeight:   getEnvInt64("START_HEIGHT", 1),
		BatchSize:     getEnvInt("BATCH_SIZE", 100),
		WorkerCount:   getEnvInt("WORKER_COUNT", 4),
		RetryAttempts: getEnvInt("RETRY_ATTEMPTS", 3),
		RetryDelayMs:  getEnvInt("RETRY_DELAY_MS", 1000),

		// PoA settings
		ValidatorSyncInterval: getEnvInt("VALIDATOR_SYNC_INTERVAL", 100),
		EnablePoATracking:     getEnvBool("ENABLE_POA_TRACKING", true),
	}

	return cfg, cfg.Validate()
}

// Validate checks if configuration is valid
func (c *Config) Validate() error {
	if c.GRPCEndpoint == "" {
		return fmt.Errorf("GRPC_ENDPOINT is required")
	}
	if c.DBName == "" {
		return fmt.Errorf("DB_NAME is required")
	}
	if c.StartHeight < 1 {
		return fmt.Errorf("START_HEIGHT must be >= 1")
	}
	return nil
}

// ConnectionString returns PostgreSQL connection string
// For Unix socket with peer authentication (your setup)
func (c *Config) ConnectionString() string {
	if c.DBPassword == "" {
		// Unix socket connection with peer auth (no password)
		return fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=disable",
			c.DBHost, c.DBPort, c.DBUser, c.DBName,
		)
	}

	// TCP connection with password (fallback)
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}
