package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
	"github.com/swilcox/kurokku-esp-server/internal/model"
)

const (
	AlertKeyPrefix       = "kurokku:alert:"
	AlertKeyspacePattern = "__keyspace@0__:" + AlertKeyPrefix + "*"
)

// Listener subscribes to Redis keyspace notifications for alert key changes
// and triggers playlist resets on connected devices.
type Listener struct {
	rdb     *redis.Client
	onAlert func(ctx context.Context) error
	logger  *slog.Logger
}

func NewListener(rdb *redis.Client, onAlert func(ctx context.Context) error, logger *slog.Logger) *Listener {
	return &Listener{
		rdb:     rdb,
		onAlert: onAlert,
		logger:  logger,
	}
}

// Run blocks and listens for alert key changes via keyspace notifications.
func (l *Listener) Run(ctx context.Context) error {
	// Enable keyspace notifications.
	if err := l.rdb.ConfigSet(ctx, "notify-keyspace-events", "KEA").Err(); err != nil {
		l.logger.Warn("could not set notify-keyspace-events", "error", err)
	}

	sub := l.rdb.PSubscribe(ctx, AlertKeyspacePattern)
	defer sub.Close()

	if _, err := sub.Receive(ctx); err != nil {
		return fmt.Errorf("psubscribe %s: %w", AlertKeyspacePattern, err)
	}

	ch := sub.Channel()
	l.logger.Info("alert listener started", "pattern", AlertKeyspacePattern)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			l.logger.Info("alert key change", "channel", msg.Channel, "payload", msg.Payload)
			// "expire" just means a TTL was attached — not actionable.
			// We care about "set" (alert created) and "expired"/"del" (alert gone).
			if msg.Payload == "expire" {
				l.logger.Debug("ignoring expire event (TTL attached, not yet expired)")
				continue
			}
			if err := l.onAlert(ctx); err != nil {
				l.logger.Error("handling alert", "error", err)
			}
		}
	}
}

// FetchAlerts scans for all keys matching kurokku:alert:* and returns parsed AlertConfigs.
func FetchAlerts(ctx context.Context, rdb *redis.Client) ([]model.AlertConfig, error) {
	var alerts []model.AlertConfig
	iter := rdb.Scan(ctx, 0, AlertKeyPrefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		raw, err := rdb.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", key, err)
		}
		var ac model.AlertConfig
		if err := json.Unmarshal([]byte(raw), &ac); err != nil {
			slog.Warn("skipping malformed alert", "key", key, "error", err)
			continue
		}
		if ac.ID == "" {
			ac.ID = key[len(AlertKeyPrefix):]
		}
		alerts = append(alerts, ac)
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("SCAN %s*: %w", AlertKeyPrefix, err)
	}
	return alerts, nil
}

// FilterAndSortAlerts returns alerts sorted by priority (lower = more urgent),
// filtering out low-priority alerts when the cron expression doesn't match.
func FilterAndSortAlerts(alerts []model.AlertConfig, lowPriorityCron string, lowPriorityThreshold int, now time.Time) []model.AlertConfig {
	var result []model.AlertConfig
	for _, a := range alerts {
		if a.Priority >= lowPriorityThreshold && !cronMatchesNow(lowPriorityCron, now) {
			continue
		}
		result = append(result, a)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority < result[j].Priority
		}
		return i < j // preserve scan order for same priority
	})

	return result
}

// AlertInstruction holds the computed text and duration for an alert display.
type AlertInstruction struct {
	Text        string
	DurationSec int
}

// BuildAlertInstruction concatenates alert messages sorted by priority into a
// single scrolling string and computes the total display duration from the sum
// of each alert's DisplayDuration. Returns nil if no alerts are active.
func BuildAlertInstruction(alerts []model.AlertConfig) *AlertInstruction {
	if len(alerts) == 0 {
		return nil
	}

	var totalDuration time.Duration
	msgs := make([]string, len(alerts))
	for i, a := range alerts {
		msgs[i] = a.Message
		if d, err := time.ParseDuration(a.DisplayDurationStr); err == nil {
			totalDuration += d
		}
	}

	text := strings.Join(msgs, " /// ")

	durationSec := int(totalDuration.Seconds())
	if durationSec < 1 {
		durationSec = 10 * len(alerts) // fallback: 10s per alert
	}

	return &AlertInstruction{
		Text:        text,
		DurationSec: durationSec,
	}
}

func cronMatchesNow(expr string, now time.Time) bool {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return true // invalid cron = always active
	}
	startOfMinute := now.Truncate(time.Minute)
	prev := startOfMinute.Add(-1 * time.Second)
	next := sched.Next(prev)
	return next.Equal(startOfMinute) || next.Before(startOfMinute.Add(time.Minute))
}
