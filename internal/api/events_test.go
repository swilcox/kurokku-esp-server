package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/swilcox/kurokku-esp-server/internal/model"
)

func TestHandleDeviceEvent_NextAdvancesPlaylist(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertPlaylist(ctx, &model.Playlist{
		ID: "pl-1",
		Entries: []model.PlaylistEntry{
			{ID: "e1", Position: 0, DurationSec: 300, Widget: model.Widget{Type: "clock"}},
			{ID: "e2", Position: 1, DurationSec: 300, Widget: model.Widget{Type: "message", Text: "hi"}},
		},
	})
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", PollMs: 5000, PlaylistID: "pl-1"})

	poll := func() *model.Instruction {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/devices/dev-1/instruction", nil))
		var resp model.ServerResponse
		json.NewDecoder(w.Body).Decode(&resp)
		return resp.Instruction
	}
	if inst := poll(); inst == nil || inst.Type != "clock" {
		t.Fatalf("first poll: %+v, want clock", inst)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/devices/dev-1/events", strings.NewReader(`{"event":"next"}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if inst := poll(); inst == nil || inst.Type != "message" {
		t.Fatalf("poll after next: %+v, want message", inst)
	}
}

func TestHandleDeviceEvent_Errors(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	s.UpsertDevice(ctx, &model.Device{ID: "dev-1", PollMs: 5000})

	cases := []struct {
		device, body string
		want         int
	}{
		{"dev-1", `{"event":"explode"}`, http.StatusBadRequest},
		{"dev-1", `not json`, http.StatusBadRequest},
		{"nope", `{"event":"next"}`, http.StatusNotFound},
		// No playlist: nothing to skip, still accepted.
		{"dev-1", `{"event":"prev"}`, http.StatusNoContent},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/devices/"+c.device+"/events", strings.NewReader(c.body)))
		if w.Code != c.want {
			t.Errorf("%s %s: status = %d, want %d", c.device, c.body, w.Code, c.want)
		}
	}
}
