package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/alert"
	"github.com/swilcox/kurokku-esp-server/internal/api"
	"github.com/swilcox/kurokku-esp-server/internal/config"
	"github.com/swilcox/kurokku-esp-server/internal/playlist"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

func main() {
	cfg := config.Load()

	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr,
	})
	defer rdb.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("redis not reachable", "addr", cfg.RedisAddr, "error", err)
		os.Exit(1)
	}

	redisStore := store.NewRedisStore(rdb)
	resolver := playlist.NewResolver(rdb, logger, playlist.Options{
		LowPriorityCron:        cfg.LowPriorityAlertCron,
		LowPriorityThreshold:   cfg.LowPriorityThreshold,
		TM1637AlertScrollSpeed: cfg.TM1637AlertScrollSpeed,
		TM1637AlertRepeats:     cfg.TM1637AlertRepeats,
	})

	apiHandler := api.NewHandler(redisStore, resolver, logger)

	webHandler, err := api.NewWebHandler(redisStore, logger, cfg.TemplateDir)
	if err != nil {
		logger.Error("loading templates", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)
	mux.Handle("/admin", webHandler)
	mux.Handle("/admin/", webHandler)
	mux.Handle("/", http.RedirectHandler("/admin", http.StatusFound))

	onAlert := func(ctx context.Context) error {
		devices, err := redisStore.ListDevices(ctx)
		if err != nil {
			return err
		}
		for _, d := range devices {
			if d.PlaylistID == "" {
				continue
			}
			pl, err := redisStore.GetPlaylist(ctx, d.PlaylistID)
			if err != nil {
				logger.Error("fetching playlist for alert reset", "device", d.ID, "error", err)
				continue
			}
			if err := resolver.ResetToAlert(ctx, d.ID, pl); err != nil {
				logger.Error("resetting playlist for alert", "device", d.ID, "error", err)
			}
		}
		return nil
	}

	alertListener := alert.NewListener(rdb, onAlert, logger)
	go func() {
		if err := alertListener.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("alert listener stopped", "error", err)
		}
	}()

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	logger.Info("server starting", "addr", cfg.ListenAddr, "redis", cfg.RedisAddr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}
