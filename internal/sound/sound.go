// Package sound checks uploaded WAV files for amoled480 displays.
//
// The firmware streams plain 16-bit PCM WAV (mono or stereo) at the sample
// rates its codec supports, so uploads are validated against that and stored
// unchanged.
package sound

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

// MaxUpload bounds how much of an upload is read (about 20 s of 48 kHz
// stereo).
const MaxUpload = 4 << 20

// Rates are the sample rates the firmware can play.
var Rates = []int{8000, 11025, 16000, 22050, 24000, 32000, 44100, 48000}

// Info describes a valid WAV.
type Info struct {
	SampleRate int
	Channels   int
	DurationMs int
}

// Check reads a WAV upload and returns its bytes and format, or an error
// saying why the device couldn't play it.
func Check(r io.Reader) ([]byte, Info, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxUpload+1))
	if err != nil {
		return nil, Info{}, err
	}
	if len(raw) > MaxUpload {
		return nil, Info{}, fmt.Errorf("sound larger than %d MB", MaxUpload>>20)
	}
	info, err := parse(raw)
	if err != nil {
		return nil, Info{}, err
	}
	return raw, info, nil
}

func parse(b []byte) (Info, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return Info{}, errors.New("not a WAV file")
	}
	var (
		haveFmt            bool
		tag, channels, bps uint16
		rate               uint32
	)
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8]))
		body := b[p+8:]
		switch id {
		case "fmt ":
			if n < 16 || len(body) < 16 {
				return Info{}, errors.New("short WAV fmt chunk")
			}
			tag = binary.LittleEndian.Uint16(body[0:])
			channels = binary.LittleEndian.Uint16(body[2:])
			rate = binary.LittleEndian.Uint32(body[4:])
			bps = binary.LittleEndian.Uint16(body[14:])
			haveFmt = true
		case "data":
			if !haveFmt {
				return Info{}, errors.New("WAV data before its format")
			}
			// 1 = PCM; 0xFFFE = WAVE_FORMAT_EXTENSIBLE (PCM at 16 bits here).
			if (tag != 1 && tag != 0xFFFE) || bps != 16 {
				return Info{}, fmt.Errorf("WAV is format %#x at %d bits; it must be 16-bit PCM", tag, bps)
			}
			if channels < 1 || channels > 2 {
				return Info{}, fmt.Errorf("WAV has %d channels; it must be mono or stereo", channels)
			}
			if !slices.Contains(Rates, int(rate)) {
				return Info{}, fmt.Errorf("WAV sample rate %d Hz isn't supported (use one of %v)", rate, Rates)
			}
			n = min(n, len(body))
			return Info{
				SampleRate: int(rate),
				Channels:   int(channels),
				DurationMs: int(int64(n) * 1000 / (int64(rate) * 2 * int64(channels))),
			}, nil
		}
		p += 8 + n + n&1
	}
	return Info{}, errors.New("WAV has no data")
}
