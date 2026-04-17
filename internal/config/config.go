package config

import (
	"os"
	"strconv"
)

type Config struct {
	ListenAddr             string
	RedisAddr              string
	TemplateDir            string
	LogLevel               string
	LowPriorityAlertCron   string // cron expression for when to show low-priority alerts
	LowPriorityThreshold   int    // alerts with priority >= this are considered low-priority
	TM1637AlertScrollSpeed int    // scroll speed (ms/col) for alert messages on tm1637 devices
	TM1637AlertRepeats     int    // number of times alert messages repeat on tm1637 devices
}

func Load() *Config {
	return &Config{
		ListenAddr:             envOrDefault("KUROKKU_LISTEN_ADDR", ":8080"),
		RedisAddr:              envOrDefault("KUROKKU_REDIS_ADDR", "localhost:6379"),
		TemplateDir:            envOrDefault("KUROKKU_TEMPLATE_DIR", "web/templates"),
		LogLevel:               envOrDefault("KUROKKU_LOG_LEVEL", "info"),
		LowPriorityAlertCron:   envOrDefault("KUROKKU_LOW_PRIORITY_ALERT_CRON", "*/15 * * * *"),
		LowPriorityThreshold:   envIntOrDefault("KUROKKU_LOW_PRIORITY_THRESHOLD", 3),
		TM1637AlertScrollSpeed: envIntOrDefault("KUROKKU_TM1637_ALERT_SCROLL_SPEED_MS", 150),
		TM1637AlertRepeats:     envIntOrDefault("KUROKKU_TM1637_ALERT_REPEATS", 3),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
