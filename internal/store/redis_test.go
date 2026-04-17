package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/model"
)

func setupStore(t *testing.T) *RedisStore {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewRedisStore(rdb)
}

// --- Devices ---

func TestGetDevice_NotFound(t *testing.T) {
	s := setupStore(t)
	d, err := s.GetDevice(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Error("expected nil for missing device")
	}
}

func TestUpsertAndGetDevice(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	lat := 40.7128
	lon := -74.006
	d := &model.Device{
		ID:              "dev-1",
		Name:            "Test Device",
		DisplayType:     model.DisplayMAX7219,
		Location:        "office",
		Brightness:      10,
		Latitude:        &lat,
		Longitude:       &lon,
		BrightnessDay:   15,
		BrightnessNight: 2,
		PollMs:          5000,
		PlaylistID:      "pl-1",
	}

	if err := s.UpsertDevice(ctx, d); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected device, got nil")
	}
	if got.Name != "Test Device" {
		t.Errorf("name = %q, want %q", got.Name, "Test Device")
	}
	if got.Brightness != 10 {
		t.Errorf("brightness = %d, want 10", got.Brightness)
	}
	if got.Latitude == nil || *got.Latitude != 40.7128 {
		t.Error("latitude not preserved")
	}
	if got.BrightnessDay != 15 {
		t.Errorf("brightness_day = %d, want 15", got.BrightnessDay)
	}
	if got.BrightnessNight != 2 {
		t.Errorf("brightness_night = %d, want 2", got.BrightnessNight)
	}
}

func TestListDevices_Empty(t *testing.T) {
	s := setupStore(t)
	devices, err := s.ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if devices != nil {
		t.Errorf("expected nil, got %d devices", len(devices))
	}
}

func TestListDevices_Multiple(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", Name: "A"})
	s.UpsertDevice(ctx, &model.Device{ID: "dev-2", Name: "B"})

	devices, err := s.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Errorf("got %d devices, want 2", len(devices))
	}
}

func TestDeleteDevice(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", Name: "A"})

	if err := s.DeleteDevice(ctx, "dev-1"); err != nil {
		t.Fatal(err)
	}

	d, err := s.GetDevice(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Error("expected nil after delete")
	}

	devices, _ := s.ListDevices(ctx)
	if len(devices) != 0 {
		t.Errorf("expected 0 devices after delete, got %d", len(devices))
	}
}

// --- Playlists ---

func TestGetPlaylist_NotFound(t *testing.T) {
	s := setupStore(t)
	p, err := s.GetPlaylist(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Error("expected nil for missing playlist")
	}
}

func TestUpsertPlaylist_Versioning(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	pl := &model.Playlist{
		ID:   "pl-1",
		Name: "Test",
		Entries: []model.PlaylistEntry{
			{ID: "e1", Position: 0, DurationSec: 30, Widget: model.Widget{Type: "clock"}},
		},
	}

	if err := s.UpsertPlaylist(ctx, pl); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetPlaylist(ctx, "pl-1")
	if got.Version != 1 {
		t.Errorf("first version = %d, want 1", got.Version)
	}

	// Update - version should increment
	pl.Name = "Updated"
	if err := s.UpsertPlaylist(ctx, pl); err != nil {
		t.Fatal(err)
	}

	got, _ = s.GetPlaylist(ctx, "pl-1")
	if got.Version != 2 {
		t.Errorf("second version = %d, want 2", got.Version)
	}
	if got.Name != "Updated" {
		t.Errorf("name = %q, want %q", got.Name, "Updated")
	}
}

func TestUpsertAndGetPlaylist_Entries(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	pl := &model.Playlist{
		ID:   "pl-1",
		Name: "Full",
		Entries: []model.PlaylistEntry{
			{ID: "e1", Position: 0, DurationSec: 30, Widget: model.Widget{Type: "clock", Format24h: true}},
			{ID: "e2", Position: 1, DurationSec: 15, Widget: model.Widget{Type: "message", Text: "hello"}},
		},
	}

	s.UpsertPlaylist(ctx, pl)
	got, _ := s.GetPlaylist(ctx, "pl-1")

	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(got.Entries))
	}
	if got.Entries[0].Widget.Type != "clock" {
		t.Errorf("entry 0 type = %s, want clock", got.Entries[0].Widget.Type)
	}
	if got.Entries[1].Widget.Text != "hello" {
		t.Errorf("entry 1 text = %q, want %q", got.Entries[1].Widget.Text, "hello")
	}
}

func TestListPlaylists(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.UpsertPlaylist(ctx, &model.Playlist{ID: "pl-1", Name: "A"})
	s.UpsertPlaylist(ctx, &model.Playlist{ID: "pl-2", Name: "B"})

	playlists, err := s.ListPlaylists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlists) != 2 {
		t.Errorf("got %d playlists, want 2", len(playlists))
	}
}

// --- OTA ---

func TestOTA_QueuePeekPop(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// PopOTA on missing key returns nil
	got, err := s.PopOTA(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for missing OTA")
	}

	// Queue + peek preserves
	if err := s.QueueOTA(ctx, "dev-1", "https://example.com/fw.bin"); err != nil {
		t.Fatal(err)
	}
	peeked, err := s.PeekOTA(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if peeked == nil || peeked.URL != "https://example.com/fw.bin" {
		t.Fatalf("peek returned %+v", peeked)
	}
	// Peeking again should still return it
	peeked2, _ := s.PeekOTA(ctx, "dev-1")
	if peeked2 == nil {
		t.Error("peek should not consume")
	}

	// Pop returns it and deletes
	popped, err := s.PopOTA(ctx, "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if popped == nil || popped.URL != "https://example.com/fw.bin" {
		t.Fatalf("pop returned %+v", popped)
	}
	popped2, _ := s.PopOTA(ctx, "dev-1")
	if popped2 != nil {
		t.Error("second pop should return nil")
	}
}

func TestOTA_Cancel(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.QueueOTA(ctx, "dev-1", "https://example.com/fw.bin")
	if err := s.CancelOTA(ctx, "dev-1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.PeekOTA(ctx, "dev-1")
	if got != nil {
		t.Error("expected nil after cancel")
	}
}

func TestOTA_QueueOverwrites(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.QueueOTA(ctx, "dev-1", "https://example.com/old.bin")
	s.QueueOTA(ctx, "dev-1", "https://example.com/new.bin")
	got, _ := s.PeekOTA(ctx, "dev-1")
	if got == nil || got.URL != "https://example.com/new.bin" {
		t.Errorf("expected new url, got %+v", got)
	}
}

func TestDeleteDevice_ClearsPendingOTA(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.UpsertDevice(ctx, &model.Device{ID: "dev-1"})
	s.QueueOTA(ctx, "dev-1", "https://example.com/fw.bin")
	s.DeleteDevice(ctx, "dev-1")

	got, _ := s.PeekOTA(ctx, "dev-1")
	if got != nil {
		t.Error("expected OTA cleared on device delete")
	}
}

// --- Firmware URL ---

func TestFirmwareURL_RoundTrip(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	// Missing returns empty, no error
	got, err := s.GetFirmwareURL(ctx, "max7219")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Set + get
	if err := s.SetFirmwareURL(ctx, "max7219", "https://example.com/fw.bin"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFirmwareURL(ctx, "max7219")
	if got != "https://example.com/fw.bin" {
		t.Errorf("got %q", got)
	}

	// Empty string clears the entry
	s.SetFirmwareURL(ctx, "max7219", "")
	got, _ = s.GetFirmwareURL(ctx, "max7219")
	if got != "" {
		t.Errorf("expected cleared, got %q", got)
	}
}

func TestDeletePlaylist(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	s.UpsertPlaylist(ctx, &model.Playlist{ID: "pl-1", Name: "A"})

	if err := s.DeletePlaylist(ctx, "pl-1"); err != nil {
		t.Fatal(err)
	}

	p, _ := s.GetPlaylist(ctx, "pl-1")
	if p != nil {
		t.Error("expected nil after delete")
	}
}
