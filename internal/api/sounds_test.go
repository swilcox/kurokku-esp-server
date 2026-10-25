package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/swilcox/kurokku-esp-server/internal/model"
)

// testWAV is half a second of silence, 16-bit mono at 24 kHz.
func testWAV() []byte {
	const rate, n = 24000, 24000
	var b bytes.Buffer
	b.WriteString("RIFF\x00\x00\x00\x00WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(n))
	b.Write(make([]byte, n))
	return b.Bytes()
}

func soundUpload(t *testing.T, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	return &body, mw.FormDataContentType()
}

func TestSounds_UploadServeDelete(t *testing.T) {
	h, _ := setupHandler(t)

	body, ctype := soundUpload(t, "chime.wav", testWAV())
	req := httptest.NewRequest("POST", "/api/v1/admin/sounds", body)
	req.Header.Set("Content-Type", ctype)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var snd model.Sound
	if err := json.Unmarshal(w.Body.Bytes(), &snd); err != nil {
		t.Fatal(err)
	}
	if snd.ID == "" || snd.Name != "chime" || snd.SampleRate != 24000 || snd.Channels != 1 || snd.DurationMs != 500 {
		t.Fatalf("sound = %+v", snd)
	}

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/api/v1/sounds/" + snd.ID + ".wav"); w.Code != 200 || w.Header().Get("Content-Type") != "audio/wav" || !bytes.Equal(w.Body.Bytes(), testWAV()) {
		t.Fatalf("wav: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := get("/api/v1/sounds/" + snd.ID + ".mp3"); w.Code != 404 {
		t.Fatalf("unknown extension: %d", w.Code)
	}
	if w := get("/api/v1/admin/sounds"); !bytes.Contains(w.Body.Bytes(), []byte(snd.ID)) {
		t.Fatalf("list: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/admin/sounds/"+snd.ID, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := get("/api/v1/sounds/" + snd.ID + ".wav"); w.Code != 404 {
		t.Fatalf("after delete: %d", w.Code)
	}
}

func TestSounds_RejectsWhatDevicesCantPlay(t *testing.T) {
	h, _ := setupHandler(t)
	body, ctype := soundUpload(t, "song.mp3", []byte("ID3 not a wav"))
	req := httptest.NewRequest("POST", "/api/v1/admin/sounds", body)
	req.Header.Set("Content-Type", ctype)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not a WAV") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestHandleDeviceInstruction_VolumeOnlyForAmoled(t *testing.T) {
	h, s := setupHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()
	vol := 35
	s.UpsertDevice(ctx, &model.Device{ID: "amoled", DisplayType: model.DisplayAMOLED480, PollMs: 5000, Volume: &vol})
	s.UpsertDevice(ctx, &model.Device{ID: "led", DisplayType: model.DisplayMAX7219, PollMs: 5000, Volume: &vol})

	poll := func(id string) map[string]any {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/devices/"+id+"/instruction", nil))
		var resp map[string]any
		json.NewDecoder(w.Body).Decode(&resp)
		return resp
	}
	if v := poll("amoled")["volume"]; v != 35.0 {
		t.Errorf("amoled volume = %v, want 35", v)
	}
	if v, ok := poll("led")["volume"]; ok {
		t.Errorf("led response has volume %v", v)
	}
}

func TestSoundsPageAndForms(t *testing.T) {
	h := newTestWebHandler(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/sounds", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No sounds uploaded yet") {
		t.Fatalf("sounds page: %d", w.Code)
	}

	// A playlist entry keeps its sound; a device keeps its volume.
	pl := h.playlistFromForm(formRequest(map[string]string{
		"entry_type_0": "image", "entry_image_id_0": "img1", "entry_sound_0": "snd1",
		"entry_type_1": "clock",
	}), "pl")
	if pl.Entries[0].Widget.SoundID != "snd1" || pl.Entries[1].Widget.SoundID != "" {
		t.Fatalf("entries = %+v", pl.Entries)
	}
	d := h.deviceFromForm(formRequest(map[string]string{"display_type": "amoled480", "volume": "140"}))
	if d.Volume == nil || *d.Volume != 100 {
		t.Fatalf("volume = %v, want clamped to 100", d.Volume)
	}
	if d := h.deviceFromForm(formRequest(map[string]string{"display_type": "amoled480"})); d.Volume != nil {
		t.Fatalf("blank volume = %v, want nil", *d.Volume)
	}
}

func formRequest(fields map[string]string) *http.Request {
	form := make([]string, 0, len(fields))
	for k, v := range fields {
		form = append(form, k+"="+v)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(strings.Join(form, "&")))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}
