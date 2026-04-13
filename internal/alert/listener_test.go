package alert

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/model"
)

func TestFilterAndSortAlerts_Empty(t *testing.T) {
	result := FilterAndSortAlerts(nil, "* * * * *", 10, time.Now())
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestFilterAndSortAlerts_AllBelowThreshold(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Priority: 1, Message: "high"},
		{ID: "a2", Priority: 5, Message: "medium"},
	}
	result := FilterAndSortAlerts(alerts, "* * * * *", 10, time.Now())
	if len(result) != 2 {
		t.Errorf("expected 2 alerts, got %d", len(result))
	}
}

func TestFilterAndSortAlerts_SortOrder(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Priority: 5, Message: "medium"},
		{ID: "a2", Priority: 1, Message: "high"},
		{ID: "a3", Priority: 3, Message: "mid"},
	}
	result := FilterAndSortAlerts(alerts, "* * * * *", 10, time.Now())
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
	if result[0].ID != "a2" {
		t.Errorf("first alert = %s, want a2 (priority 1)", result[0].ID)
	}
	if result[1].ID != "a3" {
		t.Errorf("second alert = %s, want a3 (priority 3)", result[1].ID)
	}
}

func TestFilterAndSortAlerts_LowPriorityFiltered(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Priority: 1, Message: "high"},
		{ID: "a2", Priority: 15, Message: "low"},
	}
	// Use a cron that won't match now (midnight Jan 1)
	result := FilterAndSortAlerts(alerts, "0 0 1 1 *", 10, time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))
	if len(result) != 1 {
		t.Fatalf("expected 1 (low filtered), got %d", len(result))
	}
	if result[0].ID != "a1" {
		t.Errorf("remaining alert = %s, want a1", result[0].ID)
	}
}

func TestFilterAndSortAlerts_LowPriorityKeptWhenCronMatches(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Priority: 1, Message: "high"},
		{ID: "a2", Priority: 15, Message: "low"},
	}
	// Every-minute cron always matches
	result := FilterAndSortAlerts(alerts, "* * * * *", 10, time.Now())
	if len(result) != 2 {
		t.Errorf("expected 2 (cron matches, all kept), got %d", len(result))
	}
}

func TestBuildAlertInstruction_Nil(t *testing.T) {
	if BuildAlertInstruction(nil) != nil {
		t.Error("expected nil for empty alerts")
	}
}

func TestBuildAlertInstruction_Single(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Message: "Storm warning", DisplayDurationStr: "30s"},
	}
	ai := BuildAlertInstruction(alerts)
	if ai == nil {
		t.Fatal("expected non-nil")
	}
	if ai.Text != "Storm warning" {
		t.Errorf("text = %q, want %q", ai.Text, "Storm warning")
	}
	if ai.DurationSec != 30 {
		t.Errorf("duration = %d, want 30", ai.DurationSec)
	}
}

func TestBuildAlertInstruction_Multiple(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Message: "Storm", DisplayDurationStr: "10s"},
		{ID: "a2", Message: "Flood", DisplayDurationStr: "20s"},
	}
	ai := BuildAlertInstruction(alerts)
	if ai == nil {
		t.Fatal("expected non-nil")
	}
	if ai.Text != "Storm /// Flood" {
		t.Errorf("text = %q, want %q", ai.Text, "Storm /// Flood")
	}
	if ai.DurationSec != 30 {
		t.Errorf("duration = %d, want 30", ai.DurationSec)
	}
}

func TestBuildAlertInstruction_FallbackDuration(t *testing.T) {
	alerts := []model.AlertConfig{
		{ID: "a1", Message: "No duration"},
		{ID: "a2", Message: "Also none"},
	}
	ai := BuildAlertInstruction(alerts)
	if ai.DurationSec != 20 {
		t.Errorf("fallback duration = %d, want 20 (10s per alert)", ai.DurationSec)
	}
}

func TestFetchAlerts(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	ctx := context.Background()

	a1 := model.AlertConfig{ID: "storm-1", Message: "Storm warning", Priority: 1, DisplayDurationStr: "30s"}
	a2 := model.AlertConfig{ID: "flood-1", Message: "Flood watch", Priority: 5, DisplayDurationStr: "20s"}

	data1, _ := json.Marshal(a1)
	data2, _ := json.Marshal(a2)
	rdb.Set(ctx, AlertKeyPrefix+"storm-1", data1, 0)
	rdb.Set(ctx, AlertKeyPrefix+"flood-1", data2, 0)

	alerts, err := FetchAlerts(ctx, rdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want 2", len(alerts))
	}
}

func TestFetchAlerts_Empty(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	alerts, err := FetchAlerts(context.Background(), rdb)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}
}
