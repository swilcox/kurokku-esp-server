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

	"github.com/swilcox/kurokku-esp-server/internal/imaging"
	"github.com/swilcox/kurokku-esp-server/internal/model"
	"github.com/swilcox/kurokku-esp-server/internal/store"
)

// createImage converts an upload and adds it to the image library.
func createImage(ctx context.Context, s *store.RedisStore, name string, fit imaging.Fit, r io.Reader) (*model.Image, error) {
	res, err := imaging.Convert(r, fit)
	if err != nil {
		return nil, err
	}
	var id [4]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	img := &model.Image{
		ID:        hex.EncodeToString(id[:]),
		Name:      strings.TrimSpace(name),
		Fit:       string(fit),
		Bytes:     len(res.QOI),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.SaveImage(ctx, img, res.QOI, res.Thumb); err != nil {
		return nil, err
	}
	return img, nil
}

// uploadFromRequest reads the "file" part of a multipart upload plus the
// optional "name" and "fit" fields.
func uploadFromRequest(r *http.Request) (io.ReadCloser, string, imaging.Fit, error) {
	if err := r.ParseMultipartForm(imaging.MaxUpload); err != nil {
		return nil, "", "", fmt.Errorf("reading upload: %w", err)
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return nil, "", "", fmt.Errorf("missing file: %w", err)
	}
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, filepathExt(hdr.Filename))
	}
	return f, name, imaging.ParseFit(r.FormValue("fit")), nil
}

func filepathExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}

// serveImage handles GET /api/v1/images/{file}: "{id}.qoi" for devices,
// "{id}.jpg" for the admin UI's thumbnails. Images never change once
// stored, so they're cacheable forever.
func serveImage(s *store.RedisStore, w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	id, ext, _ := strings.Cut(file, ".")
	var thumb bool
	var ctype string
	switch ext {
	case "qoi":
		ctype = "image/qoi"
	case "jpg":
		thumb, ctype = true, "image/jpeg"
	default:
		http.NotFound(w, r)
		return
	}
	data, err := s.GetImageData(r.Context(), id, thumb)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Write(data)
}
