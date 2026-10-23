// Package imaging converts uploaded images for amoled480 displays: decode
// (JPEG, PNG, GIF), apply EXIF orientation, scale to the 480x480 screen,
// and encode as QOI (what the firmware streams) plus a small JPEG thumbnail
// for the admin UI. Standard library only.
package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // register decoder
	"image/jpeg"
	_ "image/png" // register decoder
	"io"
)

// Size is the amoled480 screen's width and height.
const Size = 480

// ThumbSize is the admin UI thumbnail's width and height.
const ThumbSize = 160

// MaxUpload bounds how much of an upload is read.
const MaxUpload = 25 << 20

// Fit selects how a non-square image is made square.
type Fit string

const (
	// Cover scales to fill the screen and center-crops the overflow.
	Cover Fit = "cover"
	// Contain scales to fit inside the screen, letterboxed on black.
	Contain Fit = "contain"
)

// ParseFit maps a form/API value to a Fit, defaulting to Cover.
func ParseFit(s string) Fit {
	if s == string(Contain) {
		return Contain
	}
	return Cover
}

// Result is a converted image.
type Result struct {
	QOI   []byte
	Thumb []byte // JPEG
}

// Convert decodes an uploaded image and produces the screen-sized QOI and a
// thumbnail.
func Convert(r io.Reader, fit Fit) (*Result, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxUpload+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxUpload {
		return nil, fmt.Errorf("image larger than %d MB", MaxUpload>>20)
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}
	src = orient(src, exifOrientation(raw))
	screen := toScreen(src, fit)

	var thumb bytes.Buffer
	if err := jpeg.Encode(&thumb, resize(screen, ThumbSize, ThumbSize), &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return &Result{QOI: EncodeQOI(screen), Thumb: thumb.Bytes()}, nil
}

// toScreen scales src to Size x Size per fit.
func toScreen(src image.Image, fit Fit) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if fit == Contain {
		// Largest size that fits, aspect preserved.
		dw, dh := Size, h*Size/w
		if h > w {
			dw, dh = w*Size/h, Size
		}
		scaled := resize(src, max(dw, 1), max(dh, 1))
		off := image.Pt((Size-dw)/2, (Size-dh)/2)
		draw.Draw(out, scaled.Bounds().Add(off), scaled, image.Point{}, draw.Src)
		return out
	}
	// Cover: crop src to a centered square, then scale.
	side := min(w, h)
	origin := image.Pt(b.Min.X+(w-side)/2, b.Min.Y+(h-side)/2)
	sub := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(sub, sub.Bounds(), src, origin, draw.Src)
	draw.Draw(out, out.Bounds(), resize(sub, Size, Size), image.Point{}, draw.Src)
	return out
}

// resize scales src to w x h: area-averaging when shrinking, bilinear when
// growing.
func resize(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	rgba, ok := src.(*image.RGBA)
	if !ok || rgba.Rect.Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, sw, sh))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sx, sy := float64(sw)/float64(w), float64(sh)/float64(h)
	at := func(x, y int) []uint8 {
		i := y*rgba.Stride + x*4
		return rgba.Pix[i : i+4]
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var px [4]float64
			if sx >= 1 && sy >= 1 {
				// Average the source box this pixel covers.
				x0, x1 := int(float64(x)*sx), int(float64(x+1)*sx)
				y0, y1 := int(float64(y)*sy), int(float64(y+1)*sy)
				x1, y1 = max(x1, x0+1), max(y1, y0+1)
				n := 0.0
				for yy := y0; yy < min(y1, sh); yy++ {
					for xx := x0; xx < min(x1, sw); xx++ {
						p := at(xx, yy)
						for c := range 4 {
							px[c] += float64(p[c])
						}
						n++
					}
				}
				for c := range 4 {
					px[c] /= n
				}
			} else {
				fx := (float64(x)+0.5)*sx - 0.5
				fy := (float64(y)+0.5)*sy - 0.5
				x0, y0 := clamp(int(fx), 0, sw-1), clamp(int(fy), 0, sh-1)
				x1, y1 := clamp(x0+1, 0, sw-1), clamp(y0+1, 0, sh-1)
				tx, ty := fx-float64(x0), fy-float64(y0)
				tx, ty = clampF(tx), clampF(ty)
				for c := range 4 {
					top := float64(at(x0, y0)[c])*(1-tx) + float64(at(x1, y0)[c])*tx
					bot := float64(at(x0, y1)[c])*(1-tx) + float64(at(x1, y1)[c])*tx
					px[c] = top*(1-ty) + bot*ty
				}
			}
			i := y*out.Stride + x*4
			for c := range 4 {
				out.Pix[i+c] = uint8(px[c] + 0.5)
			}
		}
	}
	return out
}

func clamp(v, lo, hi int) int  { return max(lo, min(v, hi)) }
func clampF(v float64) float64 { return max(0, min(v, 1)) }

// EncodeQOI encodes img as a 3-channel QOI image (qoiformat.org).
func EncodeQOI(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, 0, w*h)
	out = append(out, 'q', 'o', 'i', 'f')
	out = binary.BigEndian.AppendUint32(out, uint32(w))
	out = binary.BigEndian.AppendUint32(out, uint32(h))
	out = append(out, 3, 0)

	var index [64][4]uint8
	prev := [4]uint8{0, 0, 0, 255}
	run := 0
	n := w * h
	for i := 0; i < n; i++ {
		p := img.Pix[(i/w)*img.Stride+(i%w)*4:]
		cur := [4]uint8{p[0], p[1], p[2], 255}
		if cur == prev {
			run++
			if run == 62 || i == n-1 {
				out = append(out, 0xC0|uint8(run-1))
				run = 0
			}
			continue
		}
		if run > 0 {
			out = append(out, 0xC0|uint8(run-1))
			run = 0
		}
		hash := (int(cur[0])*3 + int(cur[1])*5 + int(cur[2])*7 + int(cur[3])*11) % 64
		if index[hash] == cur {
			out = append(out, uint8(hash))
		} else {
			index[hash] = cur
			// Channel deltas wrap (as uint8), per the spec; their
			// differences must not.
			dr, dg, db := int(int8(cur[0]-prev[0])), int(int8(cur[1]-prev[1])), int(int8(cur[2]-prev[2]))
			drdg, dbdg := dr-dg, db-dg
			switch {
			case dr >= -2 && dr <= 1 && dg >= -2 && dg <= 1 && db >= -2 && db <= 1:
				out = append(out, 0x40|uint8(dr+2)<<4|uint8(dg+2)<<2|uint8(db+2))
			case dg >= -32 && dg <= 31 && drdg >= -8 && drdg <= 7 && dbdg >= -8 && dbdg <= 7:
				out = append(out, 0x80|uint8(dg+32), uint8(drdg+8)<<4|uint8(dbdg+8))
			default:
				out = append(out, 0xFE, cur[0], cur[1], cur[2])
			}
		}
		prev = cur
	}
	return append(out, 0, 0, 0, 0, 0, 0, 0, 1)
}

// exifOrientation returns the EXIF orientation (1-8) of a JPEG, or 1.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker, size := b[i+1], int(binary.BigEndian.Uint16(b[i+2:]))
		if marker == 0xE1 && i+4+size <= len(b) {
			if o, err := tiffOrientation(b[i+4 : i+2+size]); err == nil {
				return o
			}
		}
		if marker == 0xDA { // start of scan: no more metadata
			return 1
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(app1 []byte) (int, error) {
	if len(app1) < 14 || string(app1[:6]) != "Exif\x00\x00" {
		return 0, errors.New("not exif")
	}
	t := app1[6:]
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0, errors.New("bad byte order")
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd+2 > len(t) {
		return 0, errors.New("bad ifd")
	}
	count := int(bo.Uint16(t[ifd:]))
	for e := 0; e < count; e++ {
		off := ifd + 2 + e*12
		if off+12 > len(t) {
			break
		}
		if bo.Uint16(t[off:]) == 0x0112 {
			o := int(bo.Uint16(t[off+8:]))
			if o >= 1 && o <= 8 {
				return o, nil
			}
		}
	}
	return 0, errors.New("no orientation")
}

// orient applies an EXIF orientation so the image displays upright.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if o >= 5 {
		w, h = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			var dx, dy int
			X, Y := b.Dx()-1-x, b.Dy()-1-y
			switch o {
			case 2:
				dx, dy = X, y
			case 3:
				dx, dy = X, Y
			case 4:
				dx, dy = x, Y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = Y, x
			case 7:
				dx, dy = Y, X
			case 8:
				dx, dy = y, X
			}
			out.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}
