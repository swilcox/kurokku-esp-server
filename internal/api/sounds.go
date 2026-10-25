package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/sound"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

// createSound checks an upload and adds it to the sound library.
func createSound(ctx context.Context, s *store.RedisStore, name string, r io.Reader) (*model.Sound, error) {
	wav, info, err := sound.Check(r)
	if err != nil {
		return nil, err
	}
	var id [4]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	snd := &model.Sound{
		ID:         hex.EncodeToString(id[:]),
		Name:       strings.TrimSpace(name),
		Bytes:      len(wav),
		SampleRate: info.SampleRate,
		Channels:   info.Channels,
		DurationMs: info.DurationMs,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.SaveSound(ctx, snd, wav); err != nil {
		return nil, err
	}
	return snd, nil
}

// soundUploadFromRequest reads the "file" part of a multipart upload plus
// the optional "name" field.
func soundUploadFromRequest(r *http.Request) (io.ReadCloser, string, error) {
	if err := r.ParseMultipartForm(sound.MaxUpload); err != nil {
		return nil, "", fmt.Errorf("reading upload: %w", err)
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, "", fmt.Errorf("missing file: %w", err)
	}
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, filepathExt(hdr.Filename))
	}
	return f, name, nil
}

// serveSound handles GET /api/v1/sounds/{id}.wav. Sounds never change once
// stored, so they're cacheable forever.
func serveSound(s *store.RedisStore, w http.ResponseWriter, r *http.Request) {
	id, ok := strings.CutSuffix(r.PathValue("file"), ".wav")
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := s.GetSoundData(r.Context(), id)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Write(data)
}

func (h *Handler) handleListSounds(w http.ResponseWriter, r *http.Request) {
	sounds, err := h.store.ListSounds(r.Context())
	if err != nil {
		h.serverError(w, "listing sounds", err)
		return
	}
	if sounds == nil {
		sounds = []model.Sound{}
	}
	h.jsonResponse(w, http.StatusOK, sounds)
}

// handleCreateSound accepts a multipart upload: "file" (16-bit PCM WAV) and
// an optional "name".
func (h *Handler) handleCreateSound(w http.ResponseWriter, r *http.Request) {
	f, name, err := soundUploadFromRequest(r)
	if err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	snd, err := createSound(r.Context(), h.store, name, f)
	if err != nil {
		h.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.jsonResponse(w, http.StatusCreated, snd)
}

func (h *Handler) handleDeleteSound(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteSound(r.Context(), r.PathValue("sound_id")); err != nil {
		h.serverError(w, "deleting sound", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
