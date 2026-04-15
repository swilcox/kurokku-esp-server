package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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
