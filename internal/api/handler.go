package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

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

	h.mux.HandleFunc("GET /api/v1/admin/playlists", h.handleListPlaylists)
	h.mux.HandleFunc("GET /api/v1/admin/playlists/{playlist_id}", h.handleGetPlaylist)
	h.mux.HandleFunc("PUT /api/v1/admin/playlists/{playlist_id}", h.handleUpsertPlaylist)
	h.mux.HandleFunc("DELETE /api/v1/admin/playlists/{playlist_id}", h.handleDeletePlaylist)
}

func (h *Handler) handleDeviceInstruction(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("device_id")
	ctx := r.Context()

	h.logger.Debug("device instruction request", "device_id", deviceID, "remote", r.RemoteAddr)

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

	h.jsonResponse(w, http.StatusOK, resp)
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

func (h *Handler) jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) serverError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, "error", err)
	h.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}
