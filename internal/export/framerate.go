package export

// framerate.go — exact rational frame rates (run 43712071111 P1-A/D):
// common cinema/broadcast rates are expressed as numerator/denominator
// pairs (23.976 = 24000/1001) and passed to FFmpeg verbatim
// (-framerate 24000/1001), never through floating-point equality. The
// product accepts safe rates between 1 and 120 fps; zero, negative,
// NaN, infinite and unsupported values are rejected at job creation.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Frame rate bounds for this product target.
const (
	MinFPS = 1
	MaxFPS = 120
)

// FrameRate is an exact rational frame rate (Num/Den frames per second).
type FrameRate struct {
	Num, Den uint32
}

// CommonFrameRates are the standard rates offered by the UI.
var CommonFrameRates = []struct {
	Label string
	Rate  FrameRate
}{
	{"23.976 fps (24000/1001)", FrameRate{24000, 1001}},
	{"24 fps", FrameRate{24, 1}},
	{"25 fps (PAL)", FrameRate{25, 1}},
	{"29.97 fps (30000/1001)", FrameRate{30000, 1001}},
	{"30 fps", FrameRate{30, 1}},
	{"48 fps", FrameRate{48, 1}},
	{"50 fps (PAL)", FrameRate{50, 1}},
	{"59.94 fps (60000/1001)", FrameRate{60000, 1001}},
	{"60 fps", FrameRate{60, 1}},
	{"119.88 fps (120000/1001)", FrameRate{120000, 1001}},
	{"120 fps", FrameRate{120, 1}},
}

// IntFrameRate converts an integer fps to a FrameRate.
func IntFrameRate(fps int) FrameRate { return FrameRate{uint32(fps), 1} }

// Float returns the rate as float64 (display only — never scheduling).
func (r FrameRate) Float() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

// String renders the FFmpeg argument form: "24000/1001" or "30".
func (r FrameRate) String() string {
	if r.Den == 1 {
		return strconv.FormatUint(uint64(r.Num), 10)
	}
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

// Label renders the human display form with the exact rate.
func (r FrameRate) Label() string {
	f := r.Float()
	switch {
	case r.Den == 1:
		return fmt.Sprintf("%d fps", r.Num)
	case math.Abs(f-23.976) < 0.01:
		return "23.976 fps"
	case math.Abs(f-29.97) < 0.01:
		return "29.97 fps"
	case math.Abs(f-59.94) < 0.01:
		return "59.94 fps"
	case math.Abs(f-119.88) < 0.01:
		return "119.88 fps"
	default:
		return fmt.Sprintf("%.3f fps (%s)", f, r.String())
	}
}

// Valid reports whether the rate is inside the product bounds.
func (r FrameRate) Valid() bool {
	if r.Num == 0 || r.Den == 0 {
		return false
	}
	f := r.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return f >= MinFPS && f <= MaxFPS
}

// ParseFrameRate parses a user-supplied rate: "30", "29.97",
// "23.976", "24000/1001" or "30000/1001". Decimal inputs that match a
// known common rate snap to the exact rational; other decimals are
// converted to an exact fraction via a short rational reconstruction.
func ParseFrameRate(s string) (FrameRate, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return FrameRate{}, fmt.Errorf("export: empty frame rate")
	}
	if num, den, ok := strings.Cut(t, "/"); ok {
		n, err1 := strconv.ParseUint(strings.TrimSpace(num), 10, 32)
		d, err2 := strconv.ParseUint(strings.TrimSpace(den), 10, 32)
		if err1 != nil || err2 != nil || n == 0 || d == 0 {
			return FrameRate{}, fmt.Errorf("export: invalid rational frame rate %q", t)
		}
		r := FrameRate{uint32(n), uint32(d)}
		if !r.Valid() {
			return FrameRate{}, fmt.Errorf("export: frame rate %q outside the supported %d-%d fps range", t, MinFPS, MaxFPS)
		}
		return r, nil
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return FrameRate{}, fmt.Errorf("export: invalid frame rate %q", t)
	}
	// Snap to the nearest common rate when within 0.015 fps — 29.97
	// becomes exactly 30000/1001 instead of a noisy reconstruction,
	// while 30 stays 30/1 (never snapped onto the 29.97 neighbor).
	for _, c := range CommonFrameRates {
		if math.Abs(c.Rate.Float()-f) <= 0.015 {
			return c.Rate, nil
		}
	}
	if f < MinFPS || f > MaxFPS {
		return FrameRate{}, fmt.Errorf("export: frame rate %q outside the supported %d-%d fps range", t, MinFPS, MaxFPS)
	}
	// Exact reconstruction: up to 3 decimal digits -> fraction.
	num := uint64(math.Round(f * 1000))
	const den = uint64(1000)
	g := gcd(num, den)
	if g == 0 {
		return FrameRate{}, fmt.Errorf("export: invalid frame rate %q", t)
	}
	r := FrameRate{uint32(num / g), uint32(den / g)}
	if !r.Valid() {
		return FrameRate{}, fmt.Errorf("export: frame rate %q outside the supported %d-%d fps range", t, MinFPS, MaxFPS)
	}
	return r, nil
}

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
