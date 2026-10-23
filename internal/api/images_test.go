package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swilcox/kurokku-esp-server/internal/model"
)

func pngUpload(t *testing.T, name string) (*bytes.Buffer, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	img.Set(0, 0, color.White)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	if err := png.Encode(fw, img); err != nil {
		t.Fatal(err)
	}
	mw.WriteField("fit", "contain")
	mw.Close()
	return &body, mw.FormDataContentType()
}

func TestImages_UploadServeDelete(t *testing.T) {
	h, _ := setupHandler(t)

	body, ctype := pngUpload(t, "sunset.png")
	req := httptest.NewRequest("POST", "/api/v1/admin/images", body)
	req.Header.Set("Content-Type", ctype)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var img model.Image
	if err := json.Unmarshal(w.Body.Bytes(), &img); err != nil {
		t.Fatal(err)
	}
	if img.ID == "" || img.Name != "sunset" || img.Fit != "contain" || img.Bytes == 0 {
		t.Fatalf("image = %+v", img)
	}

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/api/v1/images/" + img.ID + ".qoi"); w.Code != 200 || w.Header().Get("Content-Type") != "image/qoi" || !bytes.HasPrefix(w.Body.Bytes(), []byte("qoif")) {
		t.Fatalf("qoi: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := get("/api/v1/images/" + img.ID + ".jpg"); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb: %d", w.Code)
	}
	if w := get("/api/v1/images/" + img.ID + ".gif"); w.Code != 404 {
		t.Fatalf("unknown extension: %d", w.Code)
	}
	if w := get("/api/v1/admin/images"); !bytes.Contains(w.Body.Bytes(), []byte(img.ID)) {
		t.Fatalf("list: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/admin/images/"+img.ID, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := get("/api/v1/images/" + img.ID + ".qoi"); w.Code != 404 {
		t.Fatalf("after delete: %d", w.Code)
	}
}

func TestImages_RejectsNonImage(t *testing.T) {
	h, _ := setupHandler(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	fw.Write([]byte("not an image"))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/admin/images", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}
