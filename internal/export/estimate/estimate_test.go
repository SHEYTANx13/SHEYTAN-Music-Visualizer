package estimate

import (
	"testing"
	"time"
)

func TestBitrateFormula(t *testing.T) {
	// 10 s at 8 Mbps video + 192 kbps audio, +1.5% container overhead.
	got := Bitrate(10*time.Second, 8_000_000, 192_000)
	want := int64((8_000_000 + 192_000) * 10 / 8 * 1.015)
	if got != want {
		t.Fatalf("Bitrate = %d, want %d", got, want)
	}
	if Bitrate(0, 8_000_000, 192_000) != 0 {
		t.Fatal("zero duration must estimate zero")
	}
	if Bitrate(time.Second, 0, 192_000) != 0 {
		t.Fatal("zero video bitrate must estimate zero")
	}
}

func TestFromSampleExtrapolatesRange(t *testing.T) {
	r := FromSample(1_000_000, 2*time.Second, 60*time.Second)
	if r.Expected != 30_000_000 {
		t.Fatalf("expected = %d, want 30000000", r.Expected)
	}
	if r.Low != int64(30_000_000*0.7) || r.High != int64(30_000_000*1.3) {
		t.Fatalf("band = %d..%d", r.Low, r.High)
	}
	if r.Method == "" || r.Note == "" {
		t.Fatal("method/note must be populated for honest labeling")
	}
	if bad := FromSample(0, time.Second, time.Minute); bad.Expected != 0 {
		t.Fatal("zero sample must produce no estimate")
	}
}

func TestHumanBytes(t *testing.T) {
	if HumanBytes(512) != "512 B" {
		t.Fatalf("HumanBytes(512) = %s", HumanBytes(512))
	}
	if HumanBytes(2<<20) != "2.0 MiB" {
		t.Fatalf("HumanBytes(2MiB) = %s", HumanBytes(2<<20))
	}
}
