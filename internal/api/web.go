package api

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type WebHandler struct {
	store        *store.RedisStore
	logger       *slog.Logger
	tmpls        map[string]*template.Template
	fragmentTmpl *template.Template
	firmwareTmpl *template.Template
	templateDir  string
	mux          *http.ServeMux
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
		files := []string{layoutFile, templateDir + "/" + page}
		if page == "device_form.html" {
			files = append(files, templateDir+"/device_status.html", templateDir+"/firmware_card.html")
		}
		t, err := template.New("").Funcs(funcMap).ParseFiles(files...)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", page, err)
		}
		w.tmpls[page] = t
	}

	ft, err := template.New("").Funcs(funcMap).ParseFiles(templateDir + "/device_status.html")
	if err != nil {
		return nil, fmt.Errorf("parsing device_status fragment: %w", err)
	}
	w.fragmentTmpl = ft

	ft2, err := template.New("").Funcs(funcMap).ParseFiles(templateDir + "/firmware_card.html")
	if err != nil {
		return nil, fmt.Errorf("parsing firmware_card fragment: %w", err)
	}
	w.firmwareTmpl = ft2

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
	w.mux.HandleFunc("GET /admin/devices/{id}/status", w.handleDeviceStatus)
	w.mux.HandleFunc("GET /admin/devices/{id}/firmware", w.handleFirmwareCard)
	w.mux.HandleFunc("POST /admin/devices/{id}/ota", w.handleOTATrigger)
	w.mux.HandleFunc("DELETE /admin/devices/{id}/ota", w.handleOTACancel)

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
	status, _ := w.store.GetDeviceStatus(ctx, id)
	alerts, _ := w.store.ListActiveAlerts(ctx)
	w.render(rw, "device_form.html", map[string]any{
		"IsNew":     false,
		"Device":    device,
		"Playlists": playlists,
		"Status":    buildStatusView(device, status, alerts, time.Now()),
		"Firmware":  w.buildFirmwareView(ctx, device, status, time.Now()),
	})
}

func (w *WebHandler) handleDeviceStatus(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	device, err := w.store.GetDevice(ctx, id)
	if err != nil || device == nil {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}

	status, _ := w.store.GetDeviceStatus(ctx, id)
	alerts, _ := w.store.ListActiveAlerts(ctx)
	view := buildStatusView(device, status, alerts, time.Now())

	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := w.fragmentTmpl.ExecuteTemplate(rw, "device_status", view); err != nil {
		w.logger.Error("rendering status fragment", "device_id", id, "error", err)
		http.Error(rw, "template error", http.StatusInternalServerError)
	}
}

// firmwareView is the template data for the firmware_card fragment.
type firmwareView struct {
	Device         *model.Device
	RunningVersion string
	DefaultURL     string
	Pending        *model.PendingOTA
	PendingRel     string
}

func (w *WebHandler) buildFirmwareView(ctx context.Context, device *model.Device, status *model.DeviceStatus, now time.Time) firmwareView {
	v := firmwareView{Device: device}
	if status != nil {
		v.RunningVersion = status.FirmwareVersion
	}
	v.DefaultURL, _ = w.store.GetFirmwareURL(ctx, string(device.DisplayType))
	if pending, _ := w.store.PeekOTA(ctx, device.ID); pending != nil {
		v.Pending = pending
		v.PendingRel = relativeTime(pending.QueuedAt, now)
	}
	return v
}

func (w *WebHandler) renderFirmwareCard(rw http.ResponseWriter, view firmwareView) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := w.firmwareTmpl.ExecuteTemplate(rw, "firmware_card", view); err != nil {
		w.logger.Error("rendering firmware_card fragment", "device_id", view.Device.ID, "error", err)
		http.Error(rw, "template error", http.StatusInternalServerError)
	}
}

func (w *WebHandler) handleFirmwareCard(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	device, err := w.store.GetDevice(ctx, id)
	if err != nil || device == nil {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}
	status, _ := w.store.GetDeviceStatus(ctx, id)
	w.renderFirmwareCard(rw, w.buildFirmwareView(ctx, device, status, time.Now()))
}

func (w *WebHandler) handleOTATrigger(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	device, err := w.store.GetDevice(ctx, id)
	if err != nil || device == nil {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}

	r.ParseForm()
	url := strings.TrimSpace(r.FormValue("url"))
	if !isValidFirmwareURL(url) {
		http.Error(rw, "invalid url", http.StatusBadRequest)
		return
	}

	if r.FormValue("save_default") == "1" {
		if err := w.store.SetFirmwareURL(ctx, string(device.DisplayType), url); err != nil {
			w.logger.Error("saving firmware default url", "device_id", id, "error", err)
		}
	}

	if err := w.store.QueueOTA(ctx, id, url); err != nil {
		w.logger.Error("queueing OTA", "device_id", id, "error", err)
		http.Error(rw, "could not queue OTA", http.StatusInternalServerError)
		return
	}
	w.logger.Info("OTA queued", "device_id", id, "url", url)

	status, _ := w.store.GetDeviceStatus(ctx, id)
	w.renderFirmwareCard(rw, w.buildFirmwareView(ctx, device, status, time.Now()))
}

func (w *WebHandler) handleOTACancel(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	device, err := w.store.GetDevice(ctx, id)
	if err != nil || device == nil {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}
	if err := w.store.CancelOTA(ctx, id); err != nil {
		w.logger.Error("cancelling OTA", "device_id", id, "error", err)
		http.Error(rw, "could not cancel", http.StatusInternalServerError)
		return
	}
	status, _ := w.store.GetDeviceStatus(ctx, id)
	w.renderFirmwareCard(rw, w.buildFirmwareView(ctx, device, status, time.Now()))
}

// deviceStatusView is the template data for the status fragment.
type deviceStatusView struct {
	Device             *model.Device
	Status             *model.DeviceStatus
	HasStatus          bool
	StatusClass        string // "success", "warning", "muted"
	StatusLabel        string // "Online", "Idle", "Offline", "Never seen"
	LastSeenRel        string
	LastInstructionRel string
	LastOtaRel         string
	WidgetDetail       string

	// Virtual display inputs. VirtualMode is "clock", "message", "animation",
	// or "" (none/fallback).
	VirtualMode   string
	Format24h     bool
	Text          string
	ScrollSpeedMs int
	Repeats       int // 0/<0 => infinite loop
	Animation     string

	// ActiveAlerts is the current alert queue in Redis, independent of what
	// the device is currently displaying. Sorted by priority.
	ActiveAlerts []model.AlertConfig
}

func buildStatusView(device *model.Device, status *model.DeviceStatus, alerts []model.AlertConfig, now time.Time) deviceStatusView {
	v := deviceStatusView{
		Device:       device,
		Status:       status,
		HasStatus:    status != nil,
		ActiveAlerts: sortAlertsByPriority(alerts),
	}

	if status == nil {
		v.StatusClass = "muted"
		v.StatusLabel = "Never seen"
		return v
	}

	pollMs := device.PollMs
	if pollMs < 1000 {
		pollMs = 5000
	}
	pollInterval := time.Duration(pollMs) * time.Millisecond
	since := now.Sub(status.LastSeen)

	switch {
	case since < 2*pollInterval:
		v.StatusClass = "success"
		v.StatusLabel = "Online"
	case since < 5*pollInterval:
		v.StatusClass = "warning"
		v.StatusLabel = "Idle"
	default:
		v.StatusClass = "muted"
		v.StatusLabel = "Offline"
	}

	v.LastSeenRel = relativeTime(status.LastSeen, now)
	if !status.LastOtaAt.IsZero() {
		v.LastOtaRel = relativeTime(status.LastOtaAt, now)
	}
	if inst := status.LastInstruction; inst != nil {
		v.LastInstructionRel = relativeTime(status.LastInstructionAt, now)
		v.WidgetDetail = instructionDetail(inst)
		switch inst.Type {
		case "clock":
			v.VirtualMode = "clock"
			v.Format24h = inst.Format24h != nil && *inst.Format24h
		case "message":
			v.VirtualMode = "message"
			v.Text = inst.Text
			v.ScrollSpeedMs = inst.ScrollSpeedMs
			if v.ScrollSpeedMs <= 0 {
				v.ScrollSpeedMs = 50
			}
			v.Repeats = inst.Repeats
		case "animation":
			v.VirtualMode = "animation"
			v.Animation = inst.Animation
		}
	}
	return v
}

// sortAlertsByPriority returns alerts sorted with lowest priority number
// (most urgent) first, preserving original order for ties.
func sortAlertsByPriority(alerts []model.AlertConfig) []model.AlertConfig {
	if len(alerts) == 0 {
		return nil
	}
	out := make([]model.AlertConfig, len(alerts))
	copy(out, alerts)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// instructionDetail returns a short human-readable summary of an instruction
// for the status panel (e.g. the message text, the clock format).
func instructionDetail(inst *model.Instruction) string {
	switch inst.Type {
	case "clock":
		if inst.Format24h != nil && *inst.Format24h {
			return "24h"
		}
		return "12h"
	case "message":
		t := inst.Text
		if len(t) > 40 {
			t = t[:40] + "…"
		}
		meta := []string{}
		if inst.ScrollSpeedMs > 0 {
			meta = append(meta, fmt.Sprintf("%dms/col", inst.ScrollSpeedMs))
		}
		switch {
		case inst.Repeats > 0:
			meta = append(meta, fmt.Sprintf("×%d", inst.Repeats))
		case inst.Repeats < 0:
			meta = append(meta, "×∞")
		}
		if len(meta) > 0 {
			return fmt.Sprintf("%s  (%s)", t, strings.Join(meta, ", "))
		}
		return t
	case "animation":
		return inst.Animation
	}
	return ""
}

func relativeTime(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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

	if cronStr := strings.TrimSpace(r.FormValue("low_priority_alert_cron")); cronStr != "" {
		d.LowPriorityAlertCron = &cronStr
	}
	if threshStr := strings.TrimSpace(r.FormValue("low_priority_threshold")); threshStr != "" {
		if n, err := strconv.Atoi(threshStr); err == nil {
			d.LowPriorityThreshold = &n
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
	id := r.FormValue("id")
	if id == "" {
		http.Redirect(rw, r, "/admin/playlists/new", http.StatusSeeOther)
		return
	}
	pl := w.playlistFromForm(r, id)

	if errs := validatePlaylist(pl); len(errs) > 0 {
		w.render(rw, "playlist_form.html", map[string]any{
			"IsNew":    true,
			"Playlist": pl,
			"Errors":   errs,
		})
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
	pl := w.playlistFromForm(r, id)

	if errs := validatePlaylist(pl); len(errs) > 0 {
		w.render(rw, "playlist_form.html", map[string]any{
			"IsNew":    false,
			"Playlist": pl,
			"Errors":   errs,
		})
		return
	}

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

func (w *WebHandler) playlistFromForm(r *http.Request, id string) *model.Playlist {
	pl := &model.Playlist{
		ID:   id,
		Name: r.FormValue("name"),
	}

	for i := 0; ; i++ {
		entryType := r.FormValue(fmt.Sprintf("entry_type_%d", i))
		if entryType == "" {
			break
		}

		duration, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("entry_duration_%d", i)))
		if duration < 1 {
			duration = 30
		}

		widget := model.Widget{Type: entryType}
		switch entryType {
		case "clock":
			widget.Format24h = r.FormValue(fmt.Sprintf("entry_clock_24h_%d", i)) != "false"
		case "message":
			widget.Text = r.FormValue(fmt.Sprintf("entry_msg_text_%d", i))
			widget.ScrollSpeedMs, _ = strconv.Atoi(r.FormValue(fmt.Sprintf("entry_msg_speed_%d", i)))
			widget.Repeats, _ = strconv.Atoi(r.FormValue(fmt.Sprintf("entry_msg_repeats_%d", i)))
			widget.RedisKey = strings.TrimSpace(r.FormValue(fmt.Sprintf("entry_redis_key_%d", i)))
		case "animation":
			widget.Animation = strings.TrimSpace(r.FormValue(fmt.Sprintf("entry_anim_%d", i)))
			if widget.Animation == "" {
				widget.Animation = "static"
			}
		}

		pl.Entries = append(pl.Entries, model.PlaylistEntry{
			ID:          fmt.Sprintf("%s-e%d", pl.ID, i),
			Position:    i,
			DurationSec: duration,
			CronExpr:    strings.TrimSpace(r.FormValue(fmt.Sprintf("entry_cron_%d", i))),
			Widget:      widget,
		})
	}

	return pl
}

// validatePlaylist returns a list of human-readable errors for invalid
// entries. Currently checks cron syntax.
func validatePlaylist(pl *model.Playlist) []string {
	var errs []string
	for i, e := range pl.Entries {
		if e.CronExpr == "" {
			continue
		}
		if _, err := cronParser.Parse(e.CronExpr); err != nil {
			errs = append(errs, fmt.Sprintf("Entry %d: invalid cron %q — %v", i+1, e.CronExpr, err))
		}
	}
	return errs
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
