package config

import (
	"os"
)

type Config struct {
	ListenAddr           string
	RedisAddr            string
	TemplateDir          string
	LogLevel             string
	LowPriorityAlertCron string // cron expression for when to show low-priority alerts
	LowPriorityThreshold int    // alerts with priority >= this are considered low-priority
}

func Load() *Config {
	threshold := 10
	return &Config{
		ListenAddr:           envOrDefault("KUROKKU_LISTEN_ADDR", ":8080"),
		RedisAddr:            envOrDefault("KUROKKU_REDIS_ADDR", "localhost:6379"),
		TemplateDir:          envOrDefault("KUROKKU_TEMPLATE_DIR", "web/templates"),
		LogLevel:             envOrDefault("KUROKKU_LOG_LEVEL", "info"),
		LowPriorityAlertCron: envOrDefault("KUROKKU_LOW_PRIORITY_ALERT_CRON", "*/15 * * * *"),
		LowPriorityThreshold: threshold,
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
