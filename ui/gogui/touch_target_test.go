package gogui

import (
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
)

// TestTouchTargetForWidth verifies the token-driven minimum interactive
// target resolution across every responsive class (run 45097038674
// touch-safety repair): the token document declares touchTargetMinDp
// per class (48 phone/tablet, 32 desktop/wide) and the theme must
// resolve exactly that, never zero.
func TestTouchTargetForWidth(t *testing.T) {
	doc := design.MustLoad()
	th := NewShellTheme(doc)

	cases := []struct {
		widthDp float32
		want    float32
	}{
		{widthDp: 360, want: 48},  // phone
		{widthDp: 599, want: 48},  // phone edge
		{widthDp: 600, want: 48},  // tablet
		{widthDp: 959, want: 48},  // tablet edge
		{widthDp: 960, want: 32},  // desktop
		{widthDp: 1679, want: 32}, // desktop edge
		{widthDp: 1680, want: 32}, // wide
		{widthDp: 3840, want: 32}, // far wide (big screens stay touch-friendly)
	}
	for _, c := range cases {
		if got := th.TouchTargetForWidth(c.widthDp); got != c.want {
			t.Errorf("TouchTargetForWidth(%v) = %v, want %v", c.widthDp, got, c.want)
		}
	}

	// The stored target follows the width class and starts safe (48).
	if got := th.TouchTargetDp(); got != 48 {
		t.Fatalf("initial TouchTargetDp = %v, want 48 (safe default)", got)
	}
	th.SetTouchTargetForWidth(960)
	if got := th.TouchTargetDp(); got != 32 {
		t.Fatalf("TouchTargetDp after desktop width = %v, want 32", got)
	}
	th.SetTouchTargetForWidth(400)
	if got := th.TouchTargetDp(); got != 48 {
		t.Fatalf("TouchTargetDp after phone width = %v, want 48", got)
	}
}
