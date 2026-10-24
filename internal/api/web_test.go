package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/playlist"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

func newTestWebHandler(t *testing.T) *WebHandler {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	s := store.NewRedisStore(rdb)
	h, err := NewWebHandler(s, logger, "../../web/templates")
	if err != nil {
		t.Fatalf("NewWebHandler: %v", err)
	}
	return h
}

func TestWebHandler_TemplatesParse(t *testing.T) {
	newTestWebHandler(t)
}

func TestPlaylistCreate_InvalidCron_RerendersWithError(t *testing.T) {
	h := newTestWebHandler(t)

	form := url.Values{}
	form.Set("id", "test-pl")
	form.Set("name", "Test")
	form.Set("entry_type_0", "clock")
	form.Set("entry_duration_0", "30")
	form.Set("entry_cron_0", "not a cron")
	form.Set("entry_clock_24h_0", "true")

	req := httptest.NewRequest("POST", "/admin/playlists/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-render), got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "invalid cron") {
		t.Errorf("expected error message in body, got: %s", body)
	}
	if !strings.Contains(body, "not a cron") {
		t.Errorf("expected original cron value echoed in body")
	}
}

func TestPlaylistCreate_ValidForm_Redirects(t *testing.T) {
	h := newTestWebHandler(t)

	form := url.Values{}
	form.Set("id", "test-pl")
	form.Set("name", "Test")
	form.Set("entry_type_0", "message")
	form.Set("entry_duration_0", "20")
	form.Set("entry_cron_0", "*/15 * * * *")
	form.Set("entry_msg_text_0", "hello")
	form.Set("entry_msg_speed_0", "40")
	form.Set("entry_msg_repeats_0", "2")
	form.Set("entry_redis_key_0", "kurokku:temp")

	req := httptest.NewRequest("POST", "/admin/playlists/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestPlaylistCreate_AmoledFieldsSaved(t *testing.T) {
	h := newTestWebHandler(t)

	form := url.Values{}
	form.Set("id", "amoled-pl")
	form.Set("name", "AMOLED")
	form.Set("entry_type_0", "clock")
	form.Set("entry_duration_0", "20")
	form.Set("entry_clock_24h_0", "false")
	form.Set("entry_clock_style_0", "modern")
	form.Set("entry_type_1", "message")
	form.Set("entry_duration_1", "15")
	form.Set("entry_msg_text_1", "hello")
	form.Set("entry_msg_direction_1", "up")
	form.Set("entry_msg_size_1", "small")
	form.Set("entry_msg_color_1", "#00ff88")

	req := httptest.NewRequest("POST", "/admin/playlists/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d; body: %s", w.Code, w.Body.String())
	}

	pl, err := h.store.GetPlaylist(context.Background(), "amoled-pl")
	if err != nil || pl == nil || len(pl.Entries) != 2 {
		t.Fatalf("GetPlaylist = %+v, %v", pl, err)
	}
	if c := pl.Entries[0].Widget; c.Style != "modern" || c.Format24h {
		t.Errorf("clock widget = %+v", c)
	}
	if m := pl.Entries[1].Widget; m.Direction != "up" || m.Size != "small" || m.Color != "#00ff88" {
		t.Errorf("message widget = %+v", m)
	}
}

func TestPlaylistCreate_ImageAndSlideshowSaved(t *testing.T) {
	h := newTestWebHandler(t)

	form := url.Values{}
	form.Set("id", "pics")
	form.Set("name", "Pictures")
	form.Set("entry_type_0", "image")
	form.Set("entry_duration_0", "30")
	form.Set("entry_image_id_0", "abc123")
	form.Set("entry_type_1", "slideshow")
	form.Set("entry_duration_1", "60")
	form.Set("entry_slide_interval_1", "8")
	form.Set("entry_type_2", "image") // neither image nor URL: rejected

	req := httptest.NewRequest("POST", "/admin/playlists/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Entry 3: pick a library image") {
		t.Fatalf("expected validation error, got %d", w.Code)
	}

	form.Set("entry_image_url_2", "sd:/images/cat.qoi")
	req = httptest.NewRequest("POST", "/admin/playlists/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d: %s", w.Code, w.Body.String())
	}
	pl, err := h.store.GetPlaylist(context.Background(), "pics")
	if err != nil || pl == nil || len(pl.Entries) != 3 {
		t.Fatalf("GetPlaylist = %+v, %v", pl, err)
	}
	if e := pl.Entries[0].Widget; e.ImageID != "abc123" {
		t.Errorf("image widget = %+v", e)
	}
	if e := pl.Entries[1].Widget; e.Path != "sd:/images" || e.IntervalSecs != 8 {
		t.Errorf("slideshow widget = %+v (empty path should default)", e)
	}
	if e := pl.Entries[2].Widget; e.URL != "sd:/images/cat.qoi" {
		t.Errorf("url image widget = %+v", e)
	}
}

func TestPlaylistForm_ClockStyleOptions(t *testing.T) {
	h := newTestWebHandler(t)
	render := func(style string) string {
		form := url.Values{}
		form.Set("id", "test-pl")
		form.Set("name", "Test")
		form.Set("entry_type_0", "clock")
		form.Set("entry_duration_0", "30")
		form.Set("entry_cron_0", "not a cron") // forces a re-render
		form.Set("entry_clock_style_0", style)
		req := httptest.NewRequest("POST", "/admin/playlists/new", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Body.String()
	}

	body := render("railway")
	for _, s := range clockStyles {
		if !strings.Contains(body, `<option value="`+s+`"`) {
			t.Errorf("missing option %q", s)
		}
	}
	if !strings.Contains(body, `<option value="railway" selected>`) || strings.Contains(body, "(custom)") {
		t.Errorf("railway should be selected as a known style")
	}
	if !strings.Contains(render("sparkle"), `<option value="sparkle" selected>sparkle (custom)</option>`) {
		t.Errorf("unknown style should be kept as custom")
	}
}

func TestImagesPage_Renders(t *testing.T) {
	h := newTestWebHandler(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/images", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No images uploaded yet") {
		t.Fatalf("images page: %d", w.Code)
	}
}

func TestBuildStatusView_AmoledPreviewAndQuiet(t *testing.T) {
	now := time.Now()
	always, bad := "* * * * *", "nope"
	status := &model.DeviceStatus{
		LastSeen:        now,
		LastInstruction: &model.Instruction{Type: "image", URL: playlist.ImagePath("abc123")},
	}
	device := &model.Device{ID: "d", DisplayType: model.DisplayAMOLED480, PollMs: 5000}

	v := buildStatusView(device, status, nil, now)
	if v.VirtualMode != "image" || v.ImageSrc != "/api/v1/images/abc123.jpg" {
		t.Errorf("library image: mode %q src %q", v.VirtualMode, v.ImageSrc)
	}
	if v.Quiet || v.QuietLabel != "" {
		t.Errorf("no quiet cron: Quiet %v label %q", v.Quiet, v.QuietLabel)
	}

	status.LastInstruction = &model.Instruction{Type: "image", URL: "https://example.com/a.qoi"}
	if v := buildStatusView(device, status, nil, now); v.ImageSrc != "" || v.ImageURL == "" {
		t.Errorf("external image: src %q url %q", v.ImageSrc, v.ImageURL)
	}

	device.QuietCron = &always
	if v := buildStatusView(device, status, nil, now); !v.Quiet || v.QuietLabel != "active" {
		t.Errorf("quiet: Quiet %v label %q", v.Quiet, v.QuietLabel)
	}
	device.QuietCron = &bad
	if v := buildStatusView(device, status, nil, now); v.Quiet || v.QuietLabel != "invalid cron" {
		t.Errorf("invalid: Quiet %v label %q", v.Quiet, v.QuietLabel)
	}
}

func TestDeviceStatusFragment_RendersAmoledClock(t *testing.T) {
	h := newTestWebHandler(t)
	ctx := context.Background()
	quiet := "0 0 1 1 *"
	device := &model.Device{ID: "amoled0", DisplayType: model.DisplayAMOLED480, PollMs: 5000, QuietCron: &quiet}
	if err := h.store.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	f := true
	if err := h.store.SetDeviceStatus(ctx, "amoled0", &model.DeviceStatus{
		LastSeen:        time.Now(),
		LastInstruction: &model.Instruction{Type: "clock", Format24h: &f, Style: "nixie"},
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/admin/devices/amoled0/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{"virtual-amoled480", "v-face-nixie", "nixie face", "Quiet hours", "inactive"} {
		if !strings.Contains(body, want) {
			t.Errorf("status fragment missing %q", want)
		}
	}
}
