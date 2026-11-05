package visualizer

// config_layer_test.go — clamping and validation for the layered
// composition fields (roadmap Phase 2 #6).

import "testing"

func TestClampBackgroundLayer(t *testing.T) {
	cases := []struct {
		in   Mode
		want Mode
	}{
		{"", ModeNone},
		{ModeNone, ModeNone},
		{ModeBars, ModeBars},
		{"bogus-mode", ModeNone},
	}
	for _, tc := range cases {
		c := Config{Mode: ModeRadial, BackgroundLayer: tc.in, BackgroundIntensity: 0.5}
		c.Clamp()
		if c.BackgroundLayer != tc.want {
			t.Fatalf("layer %q → %q, want %q", tc.in, c.BackgroundLayer, tc.want)
		}
	}
}

// A background layer equal to the primary mode is pointless and must
// normalize to none.
func TestClampBackgroundLayerSameAsMode(t *testing.T) {
	c := Config{Mode: ModeWaveform, BackgroundLayer: ModeWaveform, BackgroundIntensity: 0.5}
	c.Clamp()
	if c.BackgroundLayer != ModeNone || c.BackgroundIntensity != 0 {
		t.Fatalf("same-mode layer survived clamp: %q/%v", c.BackgroundLayer, c.BackgroundIntensity)
	}
}

func TestClampBackgroundIntensity(t *testing.T) {
	c := Config{Mode: ModeBars, BackgroundLayer: ModeWaveform, BackgroundIntensity: 5}
	c.Clamp()
	if c.BackgroundIntensity != MaxBackgroundIntensity {
		t.Fatalf("intensity %v above max survived", c.BackgroundIntensity)
	}
	c = Config{Mode: ModeBars, BackgroundLayer: ModeWaveform, BackgroundIntensity: -1}
	c.Clamp()
	if c.BackgroundIntensity != 0 {
		t.Fatalf("negative intensity survived: %v", c.BackgroundIntensity)
	}
	// With no layer, intensity is meaningless and reset.
	c = Config{Mode: ModeBars, BackgroundLayer: ModeNone, BackgroundIntensity: 0.5}
	c.Clamp()
	if c.BackgroundIntensity != 0 {
		t.Fatalf("intensity with layer=none survived: %v", c.BackgroundIntensity)
	}
}
