package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/playlist"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

func setupHandler(t *testing.T) (*Handler, *store.RedisStore) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.NewRedisStore(rdb)
	r := playlist.NewResolver(rdb, logger, playlist.Options{
		LowPriorityCron:        "* * * * *",
		LowPriorityThreshold:   10,
		TM1637AlertScrollSpeed: 150,
		TM1637AlertRepeats:     3,
	})
	return NewHandler(s, r, logger), s
}

func TestHandleDeviceInstruction_NotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/devices/nonexistent/instruction", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleDeviceInstruction_NoPlaylist(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	s.UpsertDevice(ctx, &model.Device{
		ID:         "dev-1",
		Brightness: 8,
		PollMs:     5000,
	})

	req := httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp model.ServerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Instruction == nil || resp.Instruction.Type != "clock" {
		t.Error("expected default clock instruction")
	}
}

func TestHandleDeviceInstruction_WithPlaylist(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	s.UpsertPlaylist(ctx, &model.Playlist{
		ID:   "pl-1",
		Name: "Test",
		Entries: []model.PlaylistEntry{
			{ID: "e1", Position: 0, DurationSec: 30, Widget: model.Widget{Type: "clock", Format24h: true}},
		},
	})
	s.UpsertDevice(ctx, &model.Device{
		ID:         "dev-1",
		Brightness: 8,
		PollMs:     5000,
		PlaylistID: "pl-1",
	})

	req := httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp model.ServerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Instruction == nil {
		t.Fatal("expected instruction")
	}
	if resp.Instruction.Type != "clock" {
		t.Errorf("type = %s, want clock", resp.Instruction.Type)
	}
}

func TestHandleDeviceInstruction_PendingOTAShortCircuits(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	s.UpsertPlaylist(ctx, &model.Playlist{
		ID:   "pl-1",
		Name: "Test",
		Entries: []model.PlaylistEntry{
			{ID: "e1", Position: 0, DurationSec: 30, Widget: model.Widget{Type: "message", Text: "hi"}},
		},
	})
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", PollMs: 5000, PlaylistID: "pl-1", DisplayType: model.DisplayMAX7219})
	s.QueueOTA(ctx, "dev-1", "https://example.com/fw.bin")

	req := httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction?display_type=max7219&firmware_version=1.2.3+abc", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp model.ServerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Instruction == nil || resp.Instruction.Type != "ota" {
		t.Fatalf("expected ota instruction, got %+v", resp.Instruction)
	}
	if resp.Instruction.URL != "https://example.com/fw.bin" {
		t.Errorf("url = %q", resp.Instruction.URL)
	}

	// Pending should be consumed after one dispatch
	peeked, _ := s.PeekOTA(ctx, "dev-1")
	if peeked != nil {
		t.Error("pending OTA should have been consumed (GETDEL)")
	}

	// Next poll returns normal playlist instruction, not OTA
	req2 := httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction?display_type=max7219&firmware_version=1.2.3+abc", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	var resp2 model.ServerResponse
	json.NewDecoder(w2.Body).Decode(&resp2)
	if resp2.Instruction != nil && resp2.Instruction.Type == "ota" {
		t.Error("second poll should not return OTA")
	}

	// DeviceStatus should have captured firmware version + last OTA
	status, _ := s.GetDeviceStatus(ctx, "dev-1")
	if status == nil {
		t.Fatal("expected status")
	}
	if status.FirmwareVersion != "1.2.3+abc" {
		t.Errorf("firmware version = %q", status.FirmwareVersion)
	}
	if status.LastOtaURL != "https://example.com/fw.bin" {
		t.Errorf("last ota url = %q", status.LastOtaURL)
	}
	if status.LastOtaAt.IsZero() {
		t.Error("last ota at should be set")
	}
}

func TestHandleDeviceInstruction_FirmwareVersionCaptured(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", PollMs: 5000})

	req := httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction?firmware_version=0.4.2+deadbeef", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	status, _ := s.GetDeviceStatus(ctx, "dev-1")
	if status == nil || status.FirmwareVersion != "0.4.2+deadbeef" {
		t.Errorf("firmware version not captured: %+v", status)
	}

	// A later poll without the param should preserve the prior value.
	req2 := httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	status, _ = s.GetDeviceStatus(ctx, "dev-1")
	if status.FirmwareVersion != "0.4.2+deadbeef" {
		t.Errorf("firmware version not preserved: %q", status.FirmwareVersion)
	}
}

func TestHandleQueueOTA_JSON(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", PollMs: 5000})

	body := `{"url":"https://example.com/fw.bin"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/devices/dev-1/ota", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	pending, _ := s.PeekOTA(ctx, "dev-1")
	if pending == nil || pending.URL != "https://example.com/fw.bin" {
		t.Errorf("OTA not queued: %+v", pending)
	}
}

func TestHandleQueueOTA_RejectsBadURL(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1"})

	body := `{"url":"not-a-url"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/devices/dev-1/ota", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleCancelOTA(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1"})
	s.QueueOTA(ctx, "dev-1", "https://example.com/fw.bin")

	req := httptest.NewRequest("DELETE", "/api/v1/admin/devices/dev-1/ota", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	pending, _ := s.PeekOTA(ctx, "dev-1")
	if pending != nil {
		t.Error("expected pending cleared")
	}
}

func TestHandleFirmwareURL_RoundTrip(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{"url":"https://example.com/fw.bin"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/firmware/max7219", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/v1/admin/firmware/max7219", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["url"] != "https://example.com/fw.bin" {
		t.Errorf("got %+v", resp)
	}
}

func TestHandleListDevices_Empty(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/admin/devices", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleUpsertAndGetDevice(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{"name":"Test","display_type":"max7219","brightness":10,"poll_ms":5000}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/devices/dev-1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", w.Code, http.StatusOK)
	}

	req = httptest.NewRequest("GET", "/api/v1/admin/devices/dev-1", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", w.Code, http.StatusOK)
	}

	var d model.Device
	json.NewDecoder(w.Body).Decode(&d)
	if d.Name != "Test" {
		t.Errorf("name = %q, want %q", d.Name, "Test")
	}
}

func TestHandleDeleteDevice(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1"})

	req := httptest.NewRequest("DELETE", "/api/v1/admin/devices/dev-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	req = httptest.NewRequest("GET", "/api/v1/admin/devices/dev-1", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleUpsertAndGetPlaylist(t *testing.T) {
	h, _ := setupHandler(t)

	body := `{"name":"Test PL","entries":[{"id":"e1","position":0,"duration_secs":30,"widget":{"type":"clock"}}]}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/playlists/pl-1", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d", w.Code, http.StatusOK)
	}

	req = httptest.NewRequest("GET", "/api/v1/admin/playlists/pl-1", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var pl model.Playlist
	json.NewDecoder(w.Body).Decode(&pl)
	if pl.Name != "Test PL" {
		t.Errorf("name = %q, want %q", pl.Name, "Test PL")
	}
	if pl.Version != 1 {
		t.Errorf("version = %d, want 1", pl.Version)
	}
}

func TestHandleDeletePlaylist(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertPlaylist(ctx, &model.Playlist{ID: "pl-1", Name: "Test"})

	req := httptest.NewRequest("DELETE", "/api/v1/admin/playlists/pl-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}
