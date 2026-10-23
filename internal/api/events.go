package api

import (
	"encoding/json"
	"net/http"
)

// deviceEventSkips maps device events to playlist moves.
var deviceEventSkips = map[string]int{
	"next": 1,
	"prev": -1,
}

// handleDeviceEvent receives an input event from a device, e.g. a swipe
// asking for the next playlist entry. The device's next poll returns the
// result, so the response has no body.
//
//	POST /api/v1/devices/{device_id}/events  {"event": "next"}
func (h *Handler) handleDeviceEvent(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("device_id")
	ctx := r.Context()

	var body struct {
		Event string `json:"event"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	delta, ok := deviceEventSkips[body.Event]
	if !ok {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "unknown event; want next or prev"})
		return
	}

	device, err := h.store.GetDevice(ctx, deviceID)
	if err != nil {
		h.serverError(w, "fetching device", err)
		return
	}
	if device == nil {
		h.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	h.logger.Info("device event", "device_id", deviceID, "event", body.Event)

	if device.PlaylistID != "" {
		pl, err := h.store.GetPlaylist(ctx, device.PlaylistID)
		if err != nil {
			h.serverError(w, "fetching playlist", err)
			return
		}
		if err := h.resolver.Skip(ctx, deviceID, pl, delta); err != nil {
			h.serverError(w, "skipping playlist entry", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
