package config

import (
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	cfg := Load()

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.RedisAddr != "localhost:6379" {
		t.Errorf("RedisAddr = %q, want %q", cfg.RedisAddr, "localhost:6379")
	}
	if cfg.TemplateDir != "web/templates" {
		t.Errorf("TemplateDir = %q, want %q", cfg.TemplateDir, "web/templates")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("KUROKKU_LISTEN_ADDR", ":9090")
	t.Setenv("KUROKKU_REDIS_ADDR", "redis:6380")
	t.Setenv("KUROKKU_LOG_LEVEL", "debug")

	cfg := Load()

	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
	if cfg.RedisAddr != "redis:6380" {
		t.Errorf("RedisAddr = %q, want %q", cfg.RedisAddr, "redis:6380")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	// TemplateDir should still be default
	if cfg.TemplateDir != "web/templates" {
		t.Errorf("TemplateDir = %q, want default %q", cfg.TemplateDir, "web/templates")
	}
}
