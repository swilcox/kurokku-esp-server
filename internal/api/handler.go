package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/playlist"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

type Handler struct {
	store    *store.RedisStore
	resolver *playlist.Resolver
	logger   *slog.Logger
	mux      *http.ServeMux
}

func NewHandler(store *store.RedisStore, resolver *playlist.Resolver, logger *slog.Logger) *Handler {
	h := &Handler{
		store:    store,
		resolver: resolver,
		logger:   logger,
		mux:      http.NewServeMux(),
	}
	h.registerRoutes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("GET /api/v1/devices/{device_id}/instruction", h.handleDeviceInstruction)

	h.mux.HandleFunc("GET /api/v1/admin/devices", h.handleListDevices)
	h.mux.HandleFunc("GET /api/v1/admin/devices/{device_id}", h.handleGetDevice)
	h.mux.HandleFunc("PUT /api/v1/admin/devices/{device_id}", h.handleUpsertDevice)
	h.mux.HandleFunc("DELETE /api/v1/admin/devices/{device_id}", h.handleDeleteDevice)

	h.mux.HandleFunc("POST /api/v1/admin/devices/{device_id}/ota", h.handleQueueOTA)
	h.mux.HandleFunc("DELETE /api/v1/admin/devices/{device_id}/ota", h.handleCancelOTA)

	h.mux.HandleFunc("GET /api/v1/admin/firmware/{display_type}", h.handleGetFirmwareURL)
	h.mux.HandleFunc("PUT /api/v1/admin/firmware/{display_type}", h.handleSetFirmwareURL)

	h.mux.HandleFunc("GET /api/v1/admin/playlists", h.handleListPlaylists)
	h.mux.HandleFunc("GET /api/v1/admin/playlists/{playlist_id}", h.handleGetPlaylist)
	h.mux.HandleFunc("PUT /api/v1/admin/playlists/{playlist_id}", h.handleUpsertPlaylist)
	h.mux.HandleFunc("DELETE /api/v1/admin/playlists/{playlist_id}", h.handleDeletePlaylist)
}

func (h *Handler) handleDeviceInstruction(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("device_id")
	ctx := r.Context()
	// Go decodes query `+` as space (form-encoding). The firmware uses `+`
	// as a semver build-metadata separator in FIRMWARE_VERSION and doesn't
	// percent-encode it, so swap spaces back. Safe because firmware versions
	// never contain literal spaces.
	firmwareVersion := strings.ReplaceAll(r.URL.Query().Get("firmware_version"), " ", "+")

	h.logger.Debug("device instruction request",
		"device_id", deviceID,
		"remote", r.RemoteAddr,
		"firmware_version", firmwareVersion,
	)

	device, err := h.store.GetDevice(ctx, deviceID)
	if err != nil {
		h.serverError(w, "fetching device", err)
		return
	}
	if device == nil {
		h.logger.Debug("device not found", "device_id", deviceID)
		h.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}

	// OTA short-circuit: if the admin has queued a firmware update, deliver
	// it on this poll and skip playlist resolution entirely. GETDEL ensures
	// the command fires exactly once.
	if pending, err := h.store.PopOTA(ctx, deviceID); err != nil {
		h.logger.Warn("reading pending OTA", "device_id", deviceID, "error", err)
	} else if pending != nil {
		h.logger.Info("dispatching OTA",
			"device_id", deviceID,
			"url", pending.URL,
			"queued_at", pending.QueuedAt,
		)
		pollMs := device.PollMs
		resp := &model.ServerResponse{
			Instruction: &model.Instruction{Type: "ota", URL: pending.URL},
			PollMs:      &pollMs,
		}
		h.recordDeviceStatusWithOTA(ctx, deviceID, r.RemoteAddr, firmwareVersion, resp.Instruction, pending)
		h.jsonResponse(w, http.StatusOK, resp)
		return
	}

	var pl *model.Playlist
	if device.PlaylistID != "" {
		pl, err = h.store.GetPlaylist(ctx, device.PlaylistID)
		if err != nil {
			h.serverError(w, "fetching playlist", err)
			return
		}
		if pl == nil {
			h.logger.Debug("playlist not found", "device_id", deviceID, "playlist_id", device.PlaylistID)
		}
	} else {
		h.logger.Debug("device has no playlist", "device_id", deviceID)
	}

	resp, err := h.resolver.Resolve(ctx, device, pl)
	if err != nil {
		h.serverError(w, "resolving playlist", err)
		return
	}

	hasInstruction := resp.Instruction != nil
	h.logger.Debug("device instruction response",
		"device_id", deviceID,
		"has_instruction", hasInstruction,
	)

	h.recordDeviceStatusWithOTA(ctx, deviceID, r.RemoteAddr, firmwareVersion, resp.Instruction, nil)

	h.jsonResponse(w, http.StatusOK, resp)
}

// recordDeviceStatusWithOTA updates the device's LastSeen/LastInstruction,
// optionally captures a reported firmware version, and records OTA dispatch
// metadata when pending is non-nil. When the resolver returns a nil
// instruction (no change), the prior LastInstruction is preserved so the
// admin UI can keep showing what the device is actually displaying.
// Firmware version and OTA history are likewise preserved from the prior
// status when no new value is provided.
func (h *Handler) recordDeviceStatusWithOTA(
	ctx context.Context,
	deviceID, remoteAddr, firmwareVersion string,
	instruction *model.Instruction,
	pending *model.PendingOTA,
) {
	now := time.Now()
	status := &model.DeviceStatus{
		LastSeen:        now,
		LastRemoteAddr:  remoteAddr,
		FirmwareVersion: firmwareVersion,
	}

	prior, err := h.store.GetDeviceStatus(ctx, deviceID)
	if err != nil {
		h.logger.Warn("reading prior device status", "device_id", deviceID, "error", err)
	}

	if instruction != nil {
		status.LastInstruction = instruction
		status.LastInstructionAt = now
	} else if prior != nil {
		status.LastInstruction = prior.LastInstruction
		status.LastInstructionAt = prior.LastInstructionAt
	}

	if firmwareVersion == "" && prior != nil {
		status.FirmwareVersion = prior.FirmwareVersion
	}

	if pending != nil {
		status.LastOtaAt = now
		status.LastOtaURL = pending.URL
	} else if prior != nil {
		status.LastOtaAt = prior.LastOtaAt
		status.LastOtaURL = prior.LastOtaURL
	}

	if err := h.store.SetDeviceStatus(ctx, deviceID, status); err != nil {
		h.logger.Warn("recording device status", "device_id", deviceID, "error", err)
	}
}

func (h *Handler) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.store.ListDevices(r.Context())
	if err != nil {
		h.serverError(w, "listing devices", err)
		return
	}
	h.jsonResponse(w, http.StatusOK, devices)
}

func (h *Handler) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	device, err := h.store.GetDevice(r.Context(), id)
	if err != nil {
		h.serverError(w, "fetching device", err)
		return
	}
	if device == nil {
		h.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	h.jsonResponse(w, http.StatusOK, device)
}

func (h *Handler) handleUpsertDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	var device model.Device
	if err := json.NewDecoder(r.Body).Decode(&device); err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	device.ID = id

	if err := h.store.UpsertDevice(r.Context(), &device); err != nil {
		h.serverError(w, "upserting device", err)
		return
	}
	h.jsonResponse(w, http.StatusOK, device)
}

func (h *Handler) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	if err := h.store.DeleteDevice(r.Context(), id); err != nil {
		h.serverError(w, "deleting device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListPlaylists(w http.ResponseWriter, r *http.Request) {
	playlists, err := h.store.ListPlaylists(r.Context())
	if err != nil {
		h.serverError(w, "listing playlists", err)
		return
	}
	h.jsonResponse(w, http.StatusOK, playlists)
}

func (h *Handler) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("playlist_id")
	pl, err := h.store.GetPlaylist(r.Context(), id)
	if err != nil {
		h.serverError(w, "fetching playlist", err)
		return
	}
	if pl == nil {
		h.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
		return
	}
	h.jsonResponse(w, http.StatusOK, pl)
}

func (h *Handler) handleUpsertPlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("playlist_id")
	var pl model.Playlist
	if err := json.NewDecoder(r.Body).Decode(&pl); err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	pl.ID = id

	if err := h.store.UpsertPlaylist(r.Context(), &pl); err != nil {
		h.serverError(w, "upserting playlist", err)
		return
	}

	saved, err := h.store.GetPlaylist(r.Context(), id)
	if err != nil {
		h.serverError(w, "fetching saved playlist", err)
		return
	}
	h.jsonResponse(w, http.StatusOK, saved)
}

func (h *Handler) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("playlist_id")
	if err := h.store.DeletePlaylist(r.Context(), id); err != nil {
		h.serverError(w, "deleting playlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- OTA admin ---

func (h *Handler) handleQueueOTA(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")

	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if !isValidFirmwareURL(body.URL) {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid url"})
		return
	}

	ctx := r.Context()
	device, err := h.store.GetDevice(ctx, id)
	if err != nil {
		h.serverError(w, "fetching device", err)
		return
	}
	if device == nil {
		h.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}

	if err := h.store.QueueOTA(ctx, id, body.URL); err != nil {
		h.serverError(w, "queueing OTA", err)
		return
	}
	h.logger.Info("OTA queued", "device_id", id, "url", body.URL)
	h.jsonResponse(w, http.StatusOK, map[string]string{"status": "queued", "url": body.URL})
}

func (h *Handler) handleCancelOTA(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("device_id")
	if err := h.store.CancelOTA(r.Context(), id); err != nil {
		h.serverError(w, "cancelling OTA", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleGetFirmwareURL(w http.ResponseWriter, r *http.Request) {
	dt := r.PathValue("display_type")
	url, err := h.store.GetFirmwareURL(r.Context(), dt)
	if err != nil {
		h.serverError(w, "fetching firmware url", err)
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]string{"display_type": dt, "url": url})
}

func (h *Handler) handleSetFirmwareURL(w http.ResponseWriter, r *http.Request) {
	dt := r.PathValue("display_type")
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.URL != "" && !isValidFirmwareURL(body.URL) {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid url"})
		return
	}
	if err := h.store.SetFirmwareURL(r.Context(), dt, body.URL); err != nil {
		h.serverError(w, "saving firmware url", err)
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]string{"display_type": dt, "url": body.URL})
}

// isValidFirmwareURL does a minimal sanity check: http/https scheme and a
// non-empty host. The device is trusted to fetch whatever the admin provides.
func isValidFirmwareURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

func (h *Handler) jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) serverError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, "error", err)
	h.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}
