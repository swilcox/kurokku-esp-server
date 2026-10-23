package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
)

// decodeQOI is an independent reference decoder for round-trip tests.
func decodeQOI(t *testing.T, b []byte) *image.RGBA {
	t.Helper()
	if string(b[:4]) != "qoif" {
		t.Fatal("bad magic")
	}
	w, h := int(binary.BigEndian.Uint32(b[4:])), int(binary.BigEndian.Uint32(b[8:]))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	var index [64][4]uint8
	px := [4]uint8{0, 0, 0, 255}
	p, run := 14, 0
	for i := 0; i < w*h; i++ {
		if run > 0 {
			run--
		} else {
			op := b[p]
			p++
			switch {
			case op == 0xFE:
				px[0], px[1], px[2] = b[p], b[p+1], b[p+2]
				p += 3
			case op&0xC0 == 0x00:
				px = index[op]
			case op&0xC0 == 0x40:
				px[0] += (op>>4)&3 - 2
				px[1] += (op>>2)&3 - 2
				px[2] += op&3 - 2
			case op&0xC0 == 0x80:
				dg := op&0x3F - 32
				b2 := b[p]
				p++
				px[0] += dg + b2>>4 - 8
				px[1] += dg
				px[2] += dg + b2&0x0F - 8
			default:
				run = int(op & 0x3F)
			}
			index[(int(px[0])*3+int(px[1])*5+int(px[2])*7+int(px[3])*11)%64] = px
		}
		copy(out.Pix[i*4:], px[:])
	}
	if !bytes.Equal(b[p:], []byte{0, 0, 0, 0, 0, 0, 0, 1}) {
		t.Fatalf("missing end marker at %d of %d", p, len(b))
	}
	return out
}

func TestQOIRoundTrip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 97, 61))
	rng := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		switch {
		case i%4 == 3:
			img.Pix[i] = 255
		case i < len(img.Pix)/3: // gradients: DIFF/LUMA
			img.Pix[i] = uint8(i / 40)
		case i < 2*len(img.Pix)/3: // flat: RUN/INDEX
			img.Pix[i] = 200
		default: // noise: RGB and wrapping deltas
			img.Pix[i] = uint8(rng.Intn(256))
		}
	}
	got := decodeQOI(t, EncodeQOI(img))
	if !bytes.Equal(got.Pix, img.Pix) {
		t.Fatal("round trip mismatch")
	}
}

func encodePNG(t *testing.T, img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCoverCropsCenterAndContainLetterboxes(t *testing.T) {
	// 300x100: red left third, green middle, blue right.
	src := image.NewRGBA(image.Rect(0, 0, 300, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 300; x++ {
			c := [3]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}[x/100]
			src.Set(x, y, c)
		}
	}
	res, err := Convert(bytes.NewReader(encodePNG(t, src)), Cover)
	if err != nil {
		t.Fatal(err)
	}
	cover := decodeQOI(t, res.QOI)
	if cover.Rect.Dx() != Size || cover.Rect.Dy() != Size {
		t.Fatalf("size %v", cover.Rect)
	}
	if c := cover.RGBAAt(10, 240); c.G < 250 || c.R > 5 {
		t.Errorf("cover should show only the green middle, got %v", c)
	}

	res, err = Convert(bytes.NewReader(encodePNG(t, src)), Contain)
	if err != nil {
		t.Fatal(err)
	}
	contain := decodeQOI(t, res.QOI)
	if c := contain.RGBAAt(240, 20); c != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("contain should letterbox on black, got %v", c)
	}
	if c := contain.RGBAAt(10, 240); c.R < 250 {
		t.Errorf("contain should keep the red left edge, got %v", c)
	}
	if _, err := jpeg.Decode(bytes.NewReader(res.Thumb)); err != nil {
		t.Errorf("thumbnail: %v", err)
	}
}

func TestOrientationRotates(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1)) // [red, blue]
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	src.Set(1, 0, color.RGBA{0, 0, 255, 255})
	r := orient(src, 6) // rotate 90° clockwise: 1x2, red on top
	if b := r.Bounds(); b.Dx() != 1 || b.Dy() != 2 {
		t.Fatalf("bounds %v", b)
	}
	if c := color.RGBAModel.Convert(r.At(0, 0)).(color.RGBA); c.R != 255 {
		t.Errorf("top should be red, got %v", c)
	}
}

func TestExifOrientationParsing(t *testing.T) {
	// Minimal JPEG prefix: SOI, APP1 Exif with one IFD0 entry (0x0112 = 6).
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, 0, 0, 0, 0, 0, 0}
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	jpg = binary.BigEndian.AppendUint16(jpg, uint16(len(app1)+2))
	jpg = append(jpg, app1...)
	jpg = append(jpg, 0xFF, 0xDA, 0, 2)
	if o := exifOrientation(jpg); o != 6 {
		t.Fatalf("orientation = %d, want 6", o)
	}
	if o := exifOrientation([]byte("not a jpeg")); o != 1 {
		t.Fatalf("non-jpeg orientation = %d, want 1", o)
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, err := Convert(bytes.NewReader([]byte("hello")), Cover); err == nil {
		t.Fatal("expected error")
	}
}
