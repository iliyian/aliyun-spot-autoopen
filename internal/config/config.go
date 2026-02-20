package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration for the application
type Config struct {
	// Aliyun credentials
	AliyunAccessKeyID     string
	AliyunAccessKeySecret string

	// Telegram settings
	TelegramEnabled  bool
	TelegramBotToken string
	TelegramChatID   string

	// Check settings
	CheckInterval int    // seconds
	CronSchedule  string // cron expression

	// Retry settings
	RetryCount    int
	RetryInterval int // seconds

	// Notification settings
	NotifyCooldown int // seconds

	// Health check settings
	HealthCheckEnabled  bool
	HealthCheckTimeout  int // seconds
	HealthCheckInterval int // seconds

	// Traffic auto-shutdown settings
	TrafficShutdownEnabled bool
	TrafficLimitChinaGB    float64 // China mainland traffic limit in GB
	TrafficLimitNonChinaGB float64 // Non-China traffic limit in GB
	TrafficCheckInterval   int     // seconds

	// GCP Credits monitoring
	GCPCreditsEnabled       bool
	GCPServiceAccountJSON   string
	GCPBillingAccountID     string
	GCPCreditsTotal         float64
	GCPCreditsAlertPercent  float64
	GCPCreditsCheckInterval int // seconds

	// Logging
	LogLevel string
	LogFile  string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		// Aliyun
		AliyunAccessKeyID:     os.Getenv("ALIYUN_ACCESS_KEY_ID"),
		AliyunAccessKeySecret: os.Getenv("ALIYUN_ACCESS_KEY_SECRET"),

		// Telegram
		TelegramEnabled:  getEnvBool("TELEGRAM_ENABLED", true),
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),

		// Check settings
		CheckInterval: getEnvInt("CHECK_INTERVAL", 60),

		// Retry settings
		RetryCount:    getEnvInt("RETRY_COUNT", 3),
		RetryInterval: getEnvInt("RETRY_INTERVAL", 30),

		// Notification settings
		NotifyCooldown: getEnvInt("NOTIFY_COOLDOWN", 300),

		// Health check settings
		HealthCheckEnabled:  getEnvBool("HEALTH_CHECK_ENABLED", true),
		HealthCheckTimeout:  getEnvInt("HEALTH_CHECK_TIMEOUT", 300),
		HealthCheckInterval: getEnvInt("HEALTH_CHECK_INTERVAL", 10),

		// Traffic auto-shutdown settings
		TrafficShutdownEnabled: getEnvBool("TRAFFIC_SHUTDOWN_ENABLED", true),
		TrafficLimitChinaGB:    getEnvFloat64("TRAFFIC_LIMIT_CHINA_GB", 19),
		TrafficLimitNonChinaGB: getEnvFloat64("TRAFFIC_LIMIT_NON_CHINA_GB", 195),
		TrafficCheckInterval:   getEnvInt("TRAFFIC_CHECK_INTERVAL", 300),

		// GCP Credits monitoring
		GCPCreditsEnabled:       getEnvBool("GCP_CREDITS_ENABLED", false),
		GCPServiceAccountJSON:   os.Getenv("GCP_SERVICE_ACCOUNT_JSON"),
		GCPBillingAccountID:     os.Getenv("GCP_BILLING_ACCOUNT_ID"),
		GCPCreditsTotal:         getEnvFloat64("GCP_CREDITS_TOTAL", 300),
		GCPCreditsAlertPercent:  getEnvFloat64("GCP_CREDITS_ALERT_PERCENT", 5),
		GCPCreditsCheckInterval: getEnvInt("GCP_CREDITS_CHECK_INTERVAL", 3600),

		// Logging
		LogLevel: getEnvString("LOG_LEVEL", "info"),
		LogFile:  os.Getenv("LOG_FILE"),
	}

	// Generate cron schedule from check interval
	cfg.CronSchedule = fmt.Sprintf("@every %ds", cfg.CheckInterval)

	// Validate required fields
	if cfg.AliyunAccessKeyID == "" {
		return nil, fmt.Errorf("ALIYUN_ACCESS_KEY_ID is required")
	}
	if cfg.AliyunAccessKeySecret == "" {
		return nil, fmt.Errorf("ALIYUN_ACCESS_KEY_SECRET is required")
	}

	if cfg.TelegramEnabled {
		if cfg.TelegramBotToken == "" {
			return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required when Telegram is enabled")
		}
		if cfg.TelegramChatID == "" {
			return nil, fmt.Errorf("TELEGRAM_CHAT_ID is required when Telegram is enabled")
		}
	}

	return cfg, nil
}

func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getEnvFloat64(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}
