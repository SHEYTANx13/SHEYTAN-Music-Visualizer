package appicon

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"
)

// TestPNGDecodes verifies the embedded PNG is a real, decodable,
// non-empty 512x512 opaque icon (the window icon and the gomobile
// launcher icon source).
func TestPNGDecodes(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG()))
	if err != nil {
		t.Fatalf("embedded icon.png does not decode: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 512 || b.Dy() != 512 {
		t.Fatalf("embedded icon is %dx%d, want 512x512", b.Dx(), b.Dy())
	}
	// The obsidian plate must dominate: the center pixel is the dark,
	// opaque brand field (not a blank or transparent icon).
	c := img.At(256, 256)
	r, g, bl, a := c.RGBA()
	if a>>8 != 255 {
		t.Fatalf("center pixel alpha %d, want opaque", a>>8)
	}
	if r>>8 > 64 || g>>8 > 64 || bl>>8 > 64 {
		t.Fatalf("center pixel (%d,%d,%d) is not the dark brand field", r>>8, g>>8, bl>>8)
	}
	// The inverted-pentagram stroke must put infernal red somewhere
	// above the center (an upturned horn): scan the upper third.
	found := false
	for y := 96; y < 180 && !found; y++ {
		for x := 96; x < 416; x++ {
			pr, pg, pb, pa := img.At(x, y).RGBA()
			if pa>>8 == 255 && pr>>8 > 150 && pg>>8 < 96 && pb>>8 < 96 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("no infernal-red stroke found above the center (inverted-pentagram mark missing)")
	}
	_ = image.NewRGBA(image.Rect(0, 0, 1, 1))
}

// TestICOStructure verifies the embedded ICO container: the ICONDIR
// header, the entry count and a non-empty size for every entry (the
// Windows .syso resource source).
func TestICOStructure(t *testing.T) {
	data := ICO()
	if len(data) < 6 {
		t.Fatal("ICO shorter than the ICONDIR header")
	}
	if data[0] != 0 || data[1] != 0 || data[2] != 1 || data[3] != 0 {
		t.Fatalf("ICO header not ICONDIR type=1: %v", data[:4])
	}
	count := int(data[4]) | int(data[5])<<8
	if count < 5 {
		t.Fatalf("ICO carries %d entries, want >= 5 sizes", count)
	}
	for i := 0; i < count; i++ {
		off := 6 + 16*i
		if off+16 > len(data) {
			t.Fatalf("entry %d exceeds file", i)
		}
		size := int(uint32(data[off+8]) | uint32(data[off+9])<<8 |
			uint32(data[off+10])<<16 | uint32(data[off+11])<<24)
		if size <= 0 || off+16+size > len(data) {
			t.Fatalf("entry %d size %d invalid", i, size)
		}
	}
}

// TestEmbeddedAssetsMatchTrackedSource pins the embedded copies to the
// tracked generator outputs: internal/appicon and assets/icon must stay
// byte-identical (the generator writes both from one render).
func TestEmbeddedAssetsMatchTrackedSource(t *testing.T) {
	for _, pair := range []struct {
		name     string
		embedded []byte
		tracked  string
	}{
		{"icon.png", PNG(), "../../assets/icon/icon.png"},
		{"icon.ico", ICO(), "../../assets/icon/icon.ico"},
	} {
		want, err := os.ReadFile(pair.tracked)
		if err != nil {
			t.Fatalf("tracked asset %s unreadable: %v", pair.tracked, err)
		}
		if !bytes.Equal(pair.embedded, want) {
			t.Fatalf("embedded %s differs from tracked %s; re-run scripts/assets/gen_icon.py", pair.name, pair.tracked)
		}
	}
}
