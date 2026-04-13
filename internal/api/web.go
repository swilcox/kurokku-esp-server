package api

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

type WebHandler struct {
	store       *store.RedisStore
	logger      *slog.Logger
	tmpls       map[string]*template.Template
	templateDir string
	mux         *http.ServeMux
}

func NewWebHandler(store *store.RedisStore, logger *slog.Logger, templateDir string) (*WebHandler, error) {
	w := &WebHandler{
		store:       store,
		logger:      logger,
		tmpls:       make(map[string]*template.Template),
		templateDir: templateDir,
		mux:         http.NewServeMux(),
	}

	funcMap := template.FuncMap{
		"inc": func(i int) int { return i + 1 },
		"entryConfig": func(e model.PlaylistEntry) string {
			return widgetToConfigString(&e.Widget)
		},
		"entryCron": func(e model.PlaylistEntry) string {
			return e.CronExpr
		},
		"entryRedisKey": func(e model.PlaylistEntry) string {
			return e.Widget.RedisKey
		},
		"fmtFloat": func(f *float64) string {
			if f == nil {
				return ""
			}
			return strconv.FormatFloat(*f, 'f', -1, 64)
		},
	}

	layoutFile := templateDir + "/layout.html"
	pages := []string{
		"dashboard.html",
		"devices.html",
		"device_form.html",
		"playlists.html",
		"playlist_form.html",
	}

	for _, page := range pages {
		t, err := template.New("").Funcs(funcMap).ParseFiles(layoutFile, templateDir+"/"+page)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", page, err)
		}
		w.tmpls[page] = t
	}

	w.registerRoutes()
	return w, nil
}

func (w *WebHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mux.ServeHTTP(rw, r)
}

func (w *WebHandler) registerRoutes() {
	w.mux.HandleFunc("GET /admin", w.handleDashboard)
	w.mux.HandleFunc("GET /admin/", w.handleDashboard)

	w.mux.HandleFunc("GET /admin/devices", w.handleDevicesList)
	w.mux.HandleFunc("GET /admin/devices/new", w.handleDeviceNew)
	w.mux.HandleFunc("POST /admin/devices/new", w.handleDeviceCreate)
	w.mux.HandleFunc("GET /admin/devices/{id}", w.handleDeviceEdit)
	w.mux.HandleFunc("POST /admin/devices/{id}", w.handleDeviceUpdate)
	w.mux.HandleFunc("DELETE /admin/devices/{id}", w.handleDeviceDelete)

	w.mux.HandleFunc("GET /admin/playlists", w.handlePlaylistsList)
	w.mux.HandleFunc("GET /admin/playlists/new", w.handlePlaylistNew)
	w.mux.HandleFunc("POST /admin/playlists/new", w.handlePlaylistCreate)
	w.mux.HandleFunc("GET /admin/playlists/{id}", w.handlePlaylistEdit)
	w.mux.HandleFunc("POST /admin/playlists/{id}", w.handlePlaylistUpdate)
	w.mux.HandleFunc("DELETE /admin/playlists/{id}", w.handlePlaylistDelete)
}

// --- Dashboard ---

func (w *WebHandler) handleDashboard(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	devices, _ := w.store.ListDevices(ctx)
	playlists, _ := w.store.ListPlaylists(ctx)

	w.render(rw, "dashboard.html", map[string]any{
		"Devices":   devices,
		"Playlists": playlists,
	})
}

// --- Devices ---

func (w *WebHandler) handleDevicesList(rw http.ResponseWriter, r *http.Request) {
	devices, _ := w.store.ListDevices(r.Context())
	w.render(rw, "devices.html", map[string]any{"Devices": devices})
}

func (w *WebHandler) handleDeviceNew(rw http.ResponseWriter, r *http.Request) {
	playlists, _ := w.store.ListPlaylists(r.Context())
	w.render(rw, "device_form.html", map[string]any{
		"IsNew":     true,
		"Device":    &model.Device{Brightness: 8, BrightnessDay: 15, BrightnessNight: 1, PollMs: 5000, DisplayType: model.DisplayMAX7219},
		"Playlists": playlists,
	})
}

func (w *WebHandler) handleDeviceCreate(rw http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	device := w.deviceFromForm(r)
	device.ID = r.FormValue("id")

	if device.ID == "" {
		http.Redirect(rw, r, "/admin/devices/new", http.StatusSeeOther)
		return
	}

	if err := w.store.UpsertDevice(r.Context(), device); err != nil {
		w.logger.Error("creating device", "error", err)
	}
	http.Redirect(rw, r, "/admin/devices", http.StatusSeeOther)
}

func (w *WebHandler) handleDeviceEdit(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	device, err := w.store.GetDevice(ctx, id)
	if err != nil || device == nil {
		http.Redirect(rw, r, "/admin/devices", http.StatusSeeOther)
		return
	}

	playlists, _ := w.store.ListPlaylists(ctx)
	w.render(rw, "device_form.html", map[string]any{
		"IsNew":     false,
		"Device":    device,
		"Playlists": playlists,
	})
}

func (w *WebHandler) handleDeviceUpdate(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	device := w.deviceFromForm(r)
	device.ID = id

	if err := w.store.UpsertDevice(r.Context(), device); err != nil {
		w.logger.Error("updating device", "error", err)
	}
	http.Redirect(rw, r, "/admin/devices", http.StatusSeeOther)
}

func (w *WebHandler) handleDeviceDelete(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := w.store.DeleteDevice(r.Context(), id); err != nil {
		w.logger.Error("deleting device", "error", err)
	}
	rw.WriteHeader(http.StatusOK)
}

func (w *WebHandler) deviceFromForm(r *http.Request) *model.Device {
	brightness, _ := strconv.Atoi(r.FormValue("brightness"))
	pollMs, _ := strconv.Atoi(r.FormValue("poll_ms"))
	brightnessDay, _ := strconv.Atoi(r.FormValue("brightness_day"))
	brightnessNight, _ := strconv.Atoi(r.FormValue("brightness_night"))

	d := &model.Device{
		Name:            r.FormValue("name"),
		DisplayType:     model.DisplayType(r.FormValue("display_type")),
		Location:        r.FormValue("location"),
		Brightness:      brightness,
		BrightnessDay:   brightnessDay,
		BrightnessNight: brightnessNight,
		PollMs:          pollMs,
		PlaylistID:      r.FormValue("playlist_id"),
	}

	if latStr := r.FormValue("latitude"); latStr != "" {
		if lat, err := strconv.ParseFloat(latStr, 64); err == nil {
			d.Latitude = &lat
		}
	}
	if lonStr := r.FormValue("longitude"); lonStr != "" {
		if lon, err := strconv.ParseFloat(lonStr, 64); err == nil {
			d.Longitude = &lon
		}
	}

	return d
}

// --- Playlists ---

func (w *WebHandler) handlePlaylistsList(rw http.ResponseWriter, r *http.Request) {
	playlists, _ := w.store.ListPlaylists(r.Context())
	w.render(rw, "playlists.html", map[string]any{"Playlists": playlists})
}

func (w *WebHandler) handlePlaylistNew(rw http.ResponseWriter, r *http.Request) {
	w.render(rw, "playlist_form.html", map[string]any{
		"IsNew":    true,
		"Playlist": &model.Playlist{},
	})
}

func (w *WebHandler) handlePlaylistCreate(rw http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	pl := w.playlistFromForm(r)
	pl.ID = r.FormValue("id")

	if pl.ID == "" {
		http.Redirect(rw, r, "/admin/playlists/new", http.StatusSeeOther)
		return
	}

	if err := w.store.UpsertPlaylist(r.Context(), pl); err != nil {
		w.logger.Error("creating playlist", "error", err)
	}
	http.Redirect(rw, r, "/admin/playlists", http.StatusSeeOther)
}

func (w *WebHandler) handlePlaylistEdit(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pl, err := w.store.GetPlaylist(r.Context(), id)
	if err != nil || pl == nil {
		http.Redirect(rw, r, "/admin/playlists", http.StatusSeeOther)
		return
	}

	w.render(rw, "playlist_form.html", map[string]any{
		"IsNew":    false,
		"Playlist": pl,
	})
}

func (w *WebHandler) handlePlaylistUpdate(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.ParseForm()
	pl := w.playlistFromForm(r)
	pl.ID = id

	if err := w.store.UpsertPlaylist(r.Context(), pl); err != nil {
		w.logger.Error("updating playlist", "error", err)
	}
	http.Redirect(rw, r, "/admin/playlists", http.StatusSeeOther)
}

func (w *WebHandler) handlePlaylistDelete(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := w.store.DeletePlaylist(r.Context(), id); err != nil {
		w.logger.Error("deleting playlist", "error", err)
	}
	rw.WriteHeader(http.StatusOK)
}

func (w *WebHandler) playlistFromForm(r *http.Request) *model.Playlist {
	pl := &model.Playlist{
		Name: r.FormValue("name"),
	}

	for i := 0; ; i++ {
		typeKey := fmt.Sprintf("entry_type_%d", i)
		entryType := r.FormValue(typeKey)
		if entryType == "" {
			break
		}

		configKey := fmt.Sprintf("entry_config_%d", i)
		durationKey := fmt.Sprintf("entry_duration_%d", i)
		cronKey := fmt.Sprintf("entry_cron_%d", i)
		redisKeyKey := fmt.Sprintf("entry_redis_key_%d", i)

		duration, _ := strconv.Atoi(r.FormValue(durationKey))
		if duration < 1 {
			duration = 30
		}

		widget := configStringToWidget(entryType, r.FormValue(configKey))
		if entryType == "message" {
			widget.RedisKey = strings.TrimSpace(r.FormValue(redisKeyKey))
		}

		pl.Entries = append(pl.Entries, model.PlaylistEntry{
			ID:          fmt.Sprintf("%s-e%d", pl.ID, i),
			Position:    i,
			DurationSec: duration,
			CronExpr:    strings.TrimSpace(r.FormValue(cronKey)),
			Widget:      widget,
		})
	}

	return pl
}

func configStringToWidget(widgetType, config string) model.Widget {
	w := model.Widget{Type: widgetType}

	switch widgetType {
	case "clock":
		w.Format24h = config != "12h"
	case "message":
		parts := strings.SplitN(config, "|", 3)
		w.Text = parts[0]
		if len(parts) > 1 {
			w.ScrollSpeedMs, _ = strconv.Atoi(parts[1])
		}
		if len(parts) > 2 {
			w.Repeats, _ = strconv.Atoi(parts[2])
		}
	case "animation":
		w.Animation = config
		if w.Animation == "" {
			w.Animation = "static"
		}
	}

	return w
}

func widgetToConfigString(w *model.Widget) string {
	switch w.Type {
	case "clock":
		if w.Format24h {
			return "24h"
		}
		return "12h"
	case "message":
		if w.ScrollSpeedMs > 0 || w.Repeats > 0 {
			return fmt.Sprintf("%s|%d|%d", w.Text, w.ScrollSpeedMs, w.Repeats)
		}
		return w.Text
	case "animation":
		return w.Animation
	default:
		return ""
	}
}

func (w *WebHandler) render(rw http.ResponseWriter, name string, data any) {
	t, ok := w.tmpls[name]
	if !ok {
		w.logger.Error("template not found", "name", name)
		http.Error(rw, "template not found", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(rw, "layout.html", data); err != nil {
		w.logger.Error("rendering template", "name", name, "error", err)
		http.Error(rw, "template error", http.StatusInternalServerError)
	}
}
