package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// AliyunAccount represents a single Aliyun account with credentials and label
type AliyunAccount struct {
	Label           string // display label for this account
	AccessKeyID     string
	AccessKeySecret string
}

// Config holds all configuration for the application
type Config struct {
	// Aliyun credentials (multi-account)
	AliyunAccounts []AliyunAccount

	// GCP settings
	GCPEnabled         bool
	GCPProjectID       string   // optional, empty = auto-discover all projects
	GCPCredentialsJSON string   // service account JSON content
	GCPZones           []string // specific zones to monitor, empty = auto-discover

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

	// Logging
	LogLevel string
	LogFile  string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		// GCP
		GCPEnabled:         getEnvBool("GCP_ENABLED", false),
		GCPProjectID:       os.Getenv("GCP_PROJECT_ID"),
		GCPCredentialsJSON: loadGCPCredentials(),

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

		// Logging
		LogLevel: getEnvString("LOG_LEVEL", "info"),
		LogFile:  os.Getenv("LOG_FILE"),
	}

	// Generate cron schedule from check interval
	cfg.CronSchedule = fmt.Sprintf("@every %ds", cfg.CheckInterval)

	// Parse GCP zones
	if zonesStr := os.Getenv("GCP_ZONES"); zonesStr != "" {
		for _, z := range strings.Split(zonesStr, ",") {
			z = strings.TrimSpace(z)
			if z != "" {
				cfg.GCPZones = append(cfg.GCPZones, z)
			}
		}
	}

	// Parse Aliyun accounts (comma-separated, one-to-one correspondence)
	cfg.AliyunAccounts = parseAliyunAccounts()

	// Validate required fields - Aliyun is optional when GCP is enabled
	if !cfg.GCPEnabled {
		if len(cfg.AliyunAccounts) == 0 {
			return nil, fmt.Errorf("ALIYUN_ACCESS_KEY_ID and ALIYUN_ACCESS_KEY_SECRET are required (support comma-separated multiple accounts)")
		}
	} else {
		if cfg.GCPProjectID == "" {
			return nil, fmt.Errorf("GCP_PROJECT_ID is required when GCP is enabled")
		}
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

// parseAliyunAccounts parses comma-separated Aliyun credentials into account list.
// ALIYUN_ACCESS_KEY_ID=id1,id2,id3
// ALIYUN_ACCESS_KEY_SECRET=secret1,secret2,secret3
// ALIYUN_ACCOUNT_LABELS=标签1,标签2,标签3  (optional, defaults to "账号1", "账号2", ...)
func parseAliyunAccounts() []AliyunAccount {
	keyIDStr := strings.TrimSpace(os.Getenv("ALIYUN_ACCESS_KEY_ID"))
	keySecretStr := strings.TrimSpace(os.Getenv("ALIYUN_ACCESS_KEY_SECRET"))
	labelsStr := strings.TrimSpace(os.Getenv("ALIYUN_ACCOUNT_LABELS"))

	if keyIDStr == "" || keySecretStr == "" {
		return nil
	}

	keyIDs := splitAndTrim(keyIDStr, ",")
	keySecrets := splitAndTrim(keySecretStr, ",")

	// Must have same count
	if len(keyIDs) != len(keySecrets) {
		log.Errorf("ALIYUN_ACCESS_KEY_ID count (%d) does not match ALIYUN_ACCESS_KEY_SECRET count (%d), skipping Aliyun accounts",
			len(keyIDs), len(keySecrets))
		return nil
	}

	// Parse labels
	var labels []string
	if labelsStr != "" {
		labels = splitAndTrim(labelsStr, ",")
	}

	accounts := make([]AliyunAccount, 0, len(keyIDs))
	for i := 0; i < len(keyIDs); i++ {
		if keyIDs[i] == "" || keySecrets[i] == "" {
			continue
		}
		label := fmt.Sprintf("账号%d", i+1)
		if i < len(labels) && labels[i] != "" {
			label = labels[i]
		}
		// When there's only one account, use empty label (no need to distinguish)
		if len(keyIDs) == 1 {
			label = ""
		}
		accounts = append(accounts, AliyunAccount{
			Label:           label,
			AccessKeyID:     keyIDs[i],
			AccessKeySecret: keySecrets[i],
		})
	}

	return accounts
}

// splitAndTrim splits a string by separator and trims whitespace from each part
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = strings.TrimSpace(p)
	}
	return result
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

// loadGCPCredentials loads GCP service account JSON from file (GCP_CREDENTIALS_FILE)
// or falls back to the inline env var (GCP_CREDENTIALS_JSON), unescaping \n in the latter.
// Using a file is strongly recommended when running under systemd, because
// systemd's EnvironmentFile parser does not support multi-line values or JSON.
func loadGCPCredentials() string {
	if filePath := os.Getenv("GCP_CREDENTIALS_FILE"); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err == nil {
			return string(data)
		}
	}
	// Fall back to inline JSON, replacing literal \n with real newlines
	return strings.ReplaceAll(os.Getenv("GCP_CREDENTIALS_JSON"), `\n`, "\n")
}
