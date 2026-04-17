package playlist

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/model"
)

func setupResolver(t *testing.T) (*Resolver, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewResolver(rdb, logger, Options{
		LowPriorityCron:        "* * * * *",
		LowPriorityThreshold:   10,
		TM1637AlertScrollSpeed: 150,
		TM1637AlertRepeats:     3,
	}), mr
}

func makeDevice(id string) *model.Device {
	return &model.Device{
		ID:         id,
		Brightness: 8,
		PollMs:     5000,
	}
}

func makePlaylist(entries ...model.PlaylistEntry) *model.Playlist {
	return &model.Playlist{
		ID:      "pl-1",
		Version: 1,
		Entries: entries,
	}
}

func clockEntry(durationSec int) model.PlaylistEntry {
	return model.PlaylistEntry{
		ID:          "e-clock",
		DurationSec: durationSec,
		Widget:      model.Widget{Type: "clock", Format24h: true},
	}
}

func messageEntry(text string, durationSec int) model.PlaylistEntry {
	return model.PlaylistEntry{
		ID:          "e-msg",
		DurationSec: durationSec,
		Widget:      model.Widget{Type: "message", Text: text, ScrollSpeedMs: 50, Repeats: 2},
	}
}

func TestResolve_NilPlaylist(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")

	resp, err := r.Resolve(ctx, device, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil {
		t.Fatal("expected default clock instruction")
	}
	if resp.Instruction.Type != "clock" {
		t.Errorf("type = %s, want clock", resp.Instruction.Type)
	}
	if *resp.Brightness != 8 {
		t.Errorf("brightness = %d, want 8", *resp.Brightness)
	}
}

func TestResolve_EmptyPlaylist(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist()

	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil || resp.Instruction.Type != "clock" {
		t.Error("expected default clock instruction for empty playlist")
	}
}

func TestResolve_SingleEntry_FirstPoll(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist(clockEntry(30))

	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil {
		t.Fatal("expected instruction on first poll")
	}
	if resp.Instruction.Type != "clock" {
		t.Errorf("type = %s, want clock", resp.Instruction.Type)
	}
	if resp.Instruction.DurationSecs != 30 {
		t.Errorf("duration = %d, want 30", resp.Instruction.DurationSecs)
	}
}

func TestResolve_SingleEntry_NoChangeWithinDuration(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist(clockEntry(30))

	// First poll - sets state
	_, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}

	// Second poll within duration - no instruction
	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction != nil {
		t.Error("expected no instruction within duration")
	}
	if resp.Brightness == nil || *resp.Brightness != 8 {
		t.Error("expected brightness in no-change response")
	}
}

func TestResolve_MultiEntry_Advancement(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist(clockEntry(10), messageEntry("hello", 10))

	// First poll - gets clock entry
	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction.Type != "clock" {
		t.Errorf("first poll: type = %s, want clock", resp.Instruction.Type)
	}

	// Pre-seed state with StartedAt in the past to simulate elapsed time
	r.setState(ctx, "dev-1", &model.PlaylistState{
		PlaylistVersion: 1,
		CurrentIndex:    0,
		StartedAt:       time.Now().Add(-11 * time.Second),
	})

	// Second poll - should advance to message
	resp, err = r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil {
		t.Fatal("expected instruction after duration elapsed")
	}
	if resp.Instruction.Type != "message" {
		t.Errorf("second poll: type = %s, want message", resp.Instruction.Type)
	}
}

func TestResolve_VersionChange_ResetsToZero(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist(clockEntry(30), messageEntry("hi", 30))

	// First poll with version 1
	r.Resolve(ctx, device, pl)

	// Update playlist version
	pl.Version = 2
	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil {
		t.Fatal("expected instruction on version change")
	}
	if resp.Instruction.Type != "clock" {
		t.Errorf("version change should reset to index 0 (clock), got %s", resp.Instruction.Type)
	}
}

func TestResolve_Wrapping(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")
	pl := makePlaylist(clockEntry(5), messageEntry("wrap", 5))

	// Poll 1: clock (index 0)
	r.Resolve(ctx, device, pl)

	// Seed state at index 1 with expired duration
	r.setState(ctx, "dev-1", &model.PlaylistState{
		PlaylistVersion: 1,
		CurrentIndex:    1,
		StartedAt:       time.Now().Add(-6 * time.Second),
	})

	// Poll should wrap back to clock (index 0)
	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil || resp.Instruction.Type != "clock" {
		instrType := "<nil>"
		if resp.Instruction != nil {
			instrType = resp.Instruction.Type
		}
		t.Errorf("expected wrap back to clock, got %s", instrType)
	}
}

func TestResolve_CronInactive_Skipped(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")

	// Entry with a cron that won't match (minute 59, hour 3, Jan 1 only)
	cronEntry := messageEntry("cron-only", 10)
	cronEntry.CronExpr = "59 3 1 1 *" // only matches 3:59 AM on Jan 1
	pl := makePlaylist(cronEntry, clockEntry(10))

	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil {
		t.Fatal("expected instruction")
	}
	// Should skip the cron-inactive entry and land on clock
	if resp.Instruction.Type != "clock" {
		t.Errorf("type = %s, want clock (cron entry should be skipped)", resp.Instruction.Type)
	}
}

func TestResolve_ForceAdvance(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	device := makeDevice("dev-1")

	alertEntry := model.PlaylistEntry{
		ID:          "e-alert",
		DurationSec: 30,
		Widget:      model.Widget{Type: "clock", Format24h: true},
	}
	pl := makePlaylist(clockEntry(30), alertEntry)

	// Pre-seed state at index 1 with ForceAdvance
	r.setState(ctx, "dev-1", &model.PlaylistState{
		PlaylistVersion: 1,
		CurrentIndex:    1,
		StartedAt:       time.Now(),
		ForceAdvance:    true,
	})

	// Next poll should honor force advance and return the entry at index 1
	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil {
		t.Fatal("expected instruction on force advance")
	}
	if resp.Instruction.Type != "clock" {
		t.Errorf("type = %s, want clock (force advance to index 1)", resp.Instruction.Type)
	}
}

func TestResolve_BrightnessWithLatLon(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()

	lat := 40.7128
	lon := -74.0060
	device := &model.Device{
		ID:              "dev-solar",
		Brightness:      8,
		Latitude:        &lat,
		Longitude:       &lon,
		BrightnessDay:   15,
		BrightnessNight: 2,
		PollMs:          5000,
	}

	resp, err := r.Resolve(ctx, device, nil)
	if err != nil {
		t.Fatal(err)
	}

	// We can't predict exact brightness without knowing test run time,
	// but it should be either day or night, not the static value
	b := *resp.Brightness
	if b != 15 && b != 2 {
		t.Errorf("brightness = %d, want 15 (day) or 2 (night)", b)
	}
}

func TestResolve_BrightnessWithoutLatLon(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()

	device := &model.Device{
		ID:              "dev-static",
		Brightness:      10,
		BrightnessDay:   15,
		BrightnessNight: 2,
		PollMs:          5000,
	}

	resp, err := r.Resolve(ctx, device, nil)
	if err != nil {
		t.Fatal(err)
	}

	if *resp.Brightness != 10 {
		t.Errorf("brightness = %d, want 10 (static fallback)", *resp.Brightness)
	}
}

func TestEffectiveBrightness_NilLatLon(t *testing.T) {
	device := &model.Device{Brightness: 7}
	if b := effectiveBrightness(device, time.Now()); b != 7 {
		t.Errorf("got %d, want 7", b)
	}
}

func TestEffectiveBrightness_OnlyLatSet(t *testing.T) {
	lat := 40.0
	device := &model.Device{Brightness: 7, Latitude: &lat}
	if b := effectiveBrightness(device, time.Now()); b != 7 {
		t.Errorf("got %d, want 7 (only lat set, should fallback)", b)
	}
}

func TestEffectiveBrightness_Daytime(t *testing.T) {
	lat := 40.7128
	lon := -74.0060
	loc, _ := time.LoadLocation("America/New_York")
	noon := time.Date(2025, 6, 20, 12, 0, 0, 0, loc)
	device := &model.Device{
		Brightness:      8,
		Latitude:        &lat,
		Longitude:       &lon,
		BrightnessDay:   15,
		BrightnessNight: 1,
	}
	if b := effectiveBrightness(device, noon); b != 15 {
		t.Errorf("noon brightness = %d, want 15", b)
	}
}

func TestEffectiveBrightness_Nighttime(t *testing.T) {
	lat := 40.7128
	lon := -74.0060
	loc, _ := time.LoadLocation("America/New_York")
	midnight := time.Date(2025, 6, 20, 2, 0, 0, 0, loc)
	device := &model.Device{
		Brightness:      8,
		Latitude:        &lat,
		Longitude:       &lon,
		BrightnessDay:   15,
		BrightnessNight: 1,
	}
	if b := effectiveBrightness(device, midnight); b != 1 {
		t.Errorf("midnight brightness = %d, want 1", b)
	}
}

func TestCronMatchesNow(t *testing.T) {
	// Every minute - should always match
	if !cronMatchesNow("* * * * *", time.Now()) {
		t.Error("every-minute cron should match")
	}
}

func TestCronMatchesNow_InvalidExpr(t *testing.T) {
	// Invalid cron should return true (fail-open)
	if !cronMatchesNow("invalid", time.Now()) {
		t.Error("invalid cron should match (fail-open)")
	}
}

func TestResolve_AlertWidget_DeviceCronOverride_GatesLowPriority(t *testing.T) {
	// Global cron "* * * * *" would let every priority through. The device
	// override below is a cron that never matches, so priority-3 alerts should
	// be filtered while priority-0 still shows.
	r, mr := setupResolver(t)
	ctx := context.Background()

	mr.Set("kurokku:alert:low", `{"id":"low","message":"LOW","priority":3,"display_duration":"5s"}`)
	mr.Set("kurokku:alert:high", `{"id":"high","message":"HIGH","priority":0,"display_duration":"5s"}`)

	neverMatch := "59 3 1 1 *"
	thresh := 3
	device := &model.Device{
		ID:                   "dev-1",
		Brightness:           8,
		PollMs:               5000,
		LowPriorityAlertCron: &neverMatch,
		LowPriorityThreshold: &thresh,
	}

	alertEntry := model.PlaylistEntry{ID: "e-alert", DurationSec: 10, Widget: model.Widget{Type: "alert"}}
	pl := makePlaylist(alertEntry)

	resp, err := r.Resolve(ctx, device, pl)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instruction == nil || resp.Instruction.Type != "message" {
		t.Fatalf("expected alert message, got %+v", resp.Instruction)
	}
	if !strings.Contains(resp.Instruction.Text, "HIGH") {
		t.Errorf("expected HIGH alert in text, got %q", resp.Instruction.Text)
	}
	if strings.Contains(resp.Instruction.Text, "LOW") {
		t.Errorf("expected LOW alert to be filtered by device override, got %q", resp.Instruction.Text)
	}
}

func TestResetToAlert_FindsAlertWidget(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	alertEntry := model.PlaylistEntry{
		ID:          "e-alert",
		DurationSec: 10,
		Widget:      model.Widget{Type: "alert"},
	}
	pl := makePlaylist(clockEntry(30), alertEntry)

	err := r.ResetToAlert(ctx, "dev-1", pl)
	if err != nil {
		t.Fatal(err)
	}

	state, err := r.getState(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentIndex != 1 {
		t.Errorf("index = %d, want 1 (alert position)", state.CurrentIndex)
	}
	if !state.ForceAdvance {
		t.Error("expected ForceAdvance to be true")
	}
}

func TestResetToAlert_NoAlertWidget(t *testing.T) {
	r, _ := setupResolver(t)
	ctx := context.Background()
	pl := makePlaylist(clockEntry(30))

	err := r.ResetToAlert(ctx, "dev-1", pl)
	if err != nil {
		t.Fatal(err)
	}

	state, err := r.getState(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentIndex != 0 {
		t.Errorf("index = %d, want 0 (no alert widget, defaults to 0)", state.CurrentIndex)
	}
}
