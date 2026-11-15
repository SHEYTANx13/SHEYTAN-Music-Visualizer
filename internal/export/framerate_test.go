package export

// framerate_test.go — exact rational frame rates, resolution ID
// uniqueness (the ONE-4K contract) and codec-aware encoder selection
// (run 43712071111 P1-A/D).

import (
	"strings"
	"testing"
)

func TestParseFrameRateCommonAndCustom(t *testing.T) {
	cases := []struct {
		in       string
		num, den uint32
	}{
		{"24", 24, 1},
		{"30", 30, 1},
		{"60", 60, 1},
		{"120", 120, 1},
		{"23.976", 24000, 1001},
		{"29.97", 30000, 1001},
		{"59.94", 60000, 1001},
		{"119.88", 120000, 1001},
		{"24000/1001", 24000, 1001},
		{"30000/1001", 30000, 1001},
		{"50", 50, 1},
		{"12.5", 25, 2},
	}
	for _, c := range cases {
		r, err := ParseFrameRate(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if r.Num != c.num || r.Den != c.den {
			t.Errorf("%q: got %d/%d, want %d/%d", c.in, r.Num, r.Den, c.num, c.den)
		}
	}
}

func TestParseFrameRateRejectsBadValues(t *testing.T) {
	for _, bad := range []string{"", "0", "-30", "abc", "0/1", "1/0", "1000/1", "121", "0.5", "nan", "1e3"} {
		if _, err := ParseFrameRate(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestFrameRateStringAndLabel(t *testing.T) {
	if s := (FrameRate{30000, 1001}).String(); s != "30000/1001" {
		t.Fatalf("string = %s", s)
	}
	if s := (FrameRate{30, 1}).String(); s != "30" {
		t.Fatalf("string = %s", s)
	}
	if l := (FrameRate{24000, 1001}).Label(); !strings.Contains(l, "23.976") {
		t.Fatalf("label = %s", l)
	}
}

func TestResolutionsUniqueWithSingle4K(t *testing.T) {
	ids := map[string]bool{}
	labels := map[string]bool{}
	for _, r := range Resolutions {
		if ids[r.ID] {
			t.Fatalf("duplicate resolution ID %q", r.ID)
		}
		ids[r.ID] = true
		if labels[r.Name] {
			t.Fatalf("duplicate resolution label %q", r.Name)
		}
		labels[r.Name] = true
	}
	fourK := 0
	for _, r := range Resolutions {
		if strings.HasPrefix(r.Name, "4K") {
			fourK++
			if r.Name != "4K UHD" || r.Width != 3840 || r.Height != 2160 {
				t.Fatalf("the single 4K entry must be 4K UHD 3840x2160, got %s %dx%d", r.Name, r.Width, r.Height)
			}
		}
	}
	if fourK != 1 {
		t.Fatalf("exactly one 4K entry allowed, found %d", fourK)
	}
	if w, h, ok := ResolutionByID("4k-uhd"); !ok || w != 3840 || h != 2160 {
		t.Fatalf("ResolutionByID(4k-uhd) = %d,%d,%v", w, h, ok)
	}
	if _, _, ok := ResolutionByID("4k"); ok {
		t.Fatal("there must be no generic '4k' ID")
	}
}

func TestResolveEncoderCodecHEVC(t *testing.T) {
	env := &Environment{
		FFmpegPath: "/x/ffmpeg", Version: "ffmpeg version n-test",
		Encoders: map[string]bool{
			"libx264": true, "libx265": true, "aac": true,
		},
	}
	// HEVC auto: software libx265 when no proven hardware encoder.
	enc, err := ResolveEncoderCodec(env, "auto", CodecHEVC)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Name != "libx265" {
		t.Fatalf("HEVC auto = %s, want libx265", enc.Name)
	}
	// HEVC without libx265: honest error.
	noHEVC := &Environment{FFmpegPath: "/x/ffmpeg", Encoders: map[string]bool{"libx264": true, "aac": true}}
	if _, err := ResolveEncoderCodec(noHEVC, "auto", CodecHEVC); err == nil {
		t.Fatal("HEVC must be unavailable without libx265 or a proven hardware encoder")
	}
	// H.264 default unchanged.
	enc, err = ResolveEncoderCodec(env, "auto", CodecH264)
	if err != nil || enc.Name != "libx264" {
		t.Fatalf("H.264 auto = %v, %v", enc, err)
	}
}

func TestQualityCRFPerCodec(t *testing.T) {
	cases := []struct {
		preset, codec, want string
	}{
		{"high", CodecH264, "18"}, {"balanced", CodecH264, "21"}, {"compat", CodecH264, "23"},
		{"high", CodecHEVC, "20"}, {"balanced", CodecHEVC, "23"}, {"compat", CodecHEVC, "26"},
	}
	for _, c := range cases {
		if got := qualityCRF(c.preset, c.codec); got != c.want {
			t.Errorf("qualityCRF(%s,%s) = %s, want %s", c.preset, c.codec, got, c.want)
		}
	}
}
