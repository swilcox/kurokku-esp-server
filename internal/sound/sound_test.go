package sound

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// wav builds a minimal WAV with the given format and `samples` bytes of data.
func wav(tag, channels uint16, rate uint32, bits uint16, samples int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF\x00\x00\x00\x00WAVE")
	b.WriteString("LIST\x04\x00\x00\x00info") // a chunk to skip
	b.WriteString("fmt ")
	for _, v := range []any{uint32(16), tag, channels, rate, rate * uint32(channels) * 2, channels * 2, bits} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(samples))
	b.Write(make([]byte, samples))
	return b.Bytes()
}

func TestCheck_AcceptsPlayableWAV(t *testing.T) {
	raw := wav(1, 1, 24000, 16, 48000)
	data, info, err := Check(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(raw) || info != (Info{SampleRate: 24000, Channels: 1, DurationMs: 1000}) {
		t.Errorf("got %d bytes, %+v", len(data), info)
	}
}

func TestCheck_RejectsWhatTheDeviceCantPlay(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  []byte
		want string
	}{
		"not wav":   {[]byte("ID3 this is an mp3"), "not a WAV"},
		"8-bit":     {wav(1, 1, 24000, 8, 100), "16-bit PCM"},
		"float":     {wav(3, 1, 24000, 16, 100), "16-bit PCM"},
		"5.1":       {wav(1, 6, 48000, 16, 100), "mono or stereo"},
		"odd rate":  {wav(1, 2, 96000, 16, 100), "isn't supported"},
		"truncated": {[]byte("RIFF\x00\x00\x00\x00WAVEfmt "), "no data"},
	} {
		if _, _, err := Check(bytes.NewReader(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, tc.want)
		}
	}
}
