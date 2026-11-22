package dsp

// analysis_test.go — §6.11/§6.12/§8.2 tests of the v3 orchestrator:
// chunk-size independence, determinism, document validation, safe
// cache-version invalidation, honest warnings, and bounded memory
// (long-track benchmarks live in analysis_bench_test.go).

import (
	"bytes"
	"context"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
)

// chunkedInput replays one interleaved stream in fixed-size chunks.
type chunkedInput struct {
	data []float32
	size int
	pos  int
	eof  bool
}

func (c *chunkedInput) ReadChunk() ([]float32, error) {
	if c.pos >= len(c.data) {
		return nil, io.EOF
	}
	end := c.pos + c.size
	if end > len(c.data) {
		end = len(c.data)
	}
	out := c.data[c.pos:end]
	c.pos = end
	return out, nil
}

func analyzeChunks(tr StereoTrack, chunkSamples int) *Analysis {
	in := &chunkedInput{data: tr.Interleaved(), size: chunkSamples}
	a, err := AnalyzeFull(context.Background(), in, "fp-test", "test-decoder")
	if err != nil {
		panic(err)
	}
	return a
}

func TestAnalyzeChunkIndependence(t *testing.T) {
	tr := DropStructure(17)
	a1 := analyzeChunks(tr, 960)       // 10 ms chunks
	a2 := analyzeChunks(tr, 48000)     // 1 s chunks
	a3 := analyzeChunks(tr, 1_500_000) // everything at once
	if !reflect.DeepEqual(a1.Stereo, a2.Stereo) {
		t.Fatal("stereo series differ across chunk sizes")
	}
	if !reflect.DeepEqual(a1.Bands, a2.Bands) || !reflect.DeepEqual(a2.Bands, a3.Bands) {
		t.Fatal("band series differ across chunk sizes")
	}
	if !reflect.DeepEqual(a1.Spectral, a3.Spectral) {
		t.Fatal("spectral series differ across chunk sizes")
	}
	if !reflect.DeepEqual(a1.HPSS, a3.HPSS) {
		t.Fatal("hpss series differ across chunk sizes")
	}
	if !reflect.DeepEqual(a1.Legacy.Frames, a3.Legacy.Frames) {
		t.Fatal("legacy frames differ across chunk sizes")
	}
	if a1.Rhythm.BPM != a3.Rhythm.BPM || a1.Rhythm.TempoConfidence != a3.Rhythm.TempoConfidence {
		t.Fatal("rhythm differs across chunk sizes")
	}
}

func TestAnalyzeDeterministic(t *testing.T) {
	tr := DropStructure(4)
	a1 := analyzeChunks(tr, 4096)
	a2 := analyzeChunks(tr, 4096)
	b1, err := a1.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	b2, err := a2.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("analysis not deterministic (serialized bytes differ)")
	}
}

func TestAnalyzeValidateAndRoundTrip(t *testing.T) {
	tr := DropStructure(9)
	a := analyzeChunks(tr, 8192)
	if err := a.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	blob, err := a.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadAnalysis(blob)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.SchemaVersion != AnalysisSchemaVersion {
		t.Fatalf("schema version = %d", loaded.SchemaVersion)
	}
	if loaded.SourceFingerprint != "fp-test" {
		t.Fatalf("fingerprint = %q", loaded.SourceFingerprint)
	}
	if !reflect.DeepEqual(loaded.Bands, a.Bands) {
		t.Fatal("round trip lost band data")
	}
}

func TestLoadAnalysisRejectsWrongVersion(t *testing.T) {
	tr, _ := CorpusFixture("tone-440", 1, 1)
	a := analyzeChunks(tr, 4096)
	blob, _ := a.Serialize()
	// Corrupt the schema version: the sidecar must be REJECTED, never
	// reinterpreted with new semantics (§6.11).
	bad := bytes.Replace(blob, []byte("\"schemaVersion\":3"), []byte("\"schemaVersion\":2"), 1)
	if _, err := LoadAnalysis(bad); err == nil {
		t.Fatal("version mismatch must be an error")
	} else if !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("uninformative error: %v", err)
	}
}

func TestAnalyzeAntiPhaseWarning(t *testing.T) {
	tr, _ := CorpusFixture("antiphase", 2, 1)
	a := analyzeChunks(tr, 4096)
	found := false
	for _, w := range a.Warnings {
		if strings.Contains(w, "anti-phase") && strings.Contains(w, "MUST NOT be interpreted as silence") {
			found = true
		}
	}
	if !found {
		t.Fatalf("anti-phase warning missing: %v", a.Warnings)
	}
	// The stereo layer must NOT read the material as silence.
	var maxL float64
	for _, v := range a.Stereo.LEnergy {
		if float64(v) > maxL {
			maxL = float64(v)
		}
	}
	if maxL < 0.05 {
		t.Fatalf("anti-phase L energy reads as silence (max %.4f)", maxL)
	}
}

func TestAnalyzeStereoPreservedNotAveraged(t *testing.T) {
	// Panned-left: pure L energy with a quiet R must remain visible in
	// the per-channel series (the mono-averaged legacy path destroys it).
	tr := Panned(2, 440, -0.8, 0.5)
	a := analyzeChunks(tr, 4096)
	var meanL, meanR float64
	n := len(a.Stereo.LEnergy)
	for i := 0; i < n; i++ {
		meanL += float64(a.Stereo.LEnergy[i])
		meanR += float64(a.Stereo.REnergy[i])
	}
	meanL /= float64(n)
	meanR /= float64(n)
	if meanL <= meanR*3 {
		t.Fatalf("panned-left not reflected: meanL=%.4f meanR=%.4f", meanL, meanR)
	}
	// Correlation of a hard-panned tone stays near +1 (both channels
	// carry the same coherent signal where both have energy) — width
	// however must be clearly positive.
	var meanW float64
	for _, v := range a.Stereo.Width {
		meanW += float64(v)
	}
	meanW /= float64(n)
	if meanW < 0.05 {
		t.Fatalf("panned width = %.4f, want > 0.05", meanW)
	}
}

func TestAnalyzeDurationAndFrames(t *testing.T) {
	tr, _ := CorpusFixture("tone-440", 3, 1)
	a := analyzeChunks(tr, 4096)
	want := 3.0
	if math.Abs(a.Duration-want) > 0.01 {
		t.Fatalf("duration = %.4f, want %.1f", a.Duration, want)
	}
	if a.FrameCount != len(a.Legacy.Frames) {
		t.Fatalf("frame count mismatch: %d vs %d", a.FrameCount, len(a.Legacy.Frames))
	}
	if a.AnalyzerID != AnalyzerIDV3 {
		t.Fatalf("analyzer id = %q", a.AnalyzerID)
	}
	if a.SampleRate != SampleRate || a.Channels != 2 {
		t.Fatalf("format = %d/%d", a.SampleRate, a.Channels)
	}
}

func TestAnalyzeCancellationContext(t *testing.T) {
	tr := DropStructure(2)
	in := &chunkedInput{data: tr.Interleaved(), size: 4096}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeFull(ctx, in, "fp", "dec"); err == nil {
		t.Fatal("canceled context must abort the analysis")
	}
}

func TestAnalyzeSilenceHonest(t *testing.T) {
	tr, _ := CorpusFixture("silence", 3, 1)
	a := analyzeChunks(tr, 4096)
	if err := a.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if a.Rhythm != nil && len(a.Rhythm.Transients) != 0 {
		t.Fatal("silence produced transients")
	}
	if a.Dynamics.IntegratedLUFS != -70 {
		t.Fatalf("silence integrated = %v", a.Dynamics.IntegratedLUFS)
	}
	if len(a.Vocal.Phrases) != 0 {
		t.Fatal("silence produced vocal phrases")
	}
}

func TestAnalyzeReaderInputMatchesChunks(t *testing.T) {
	tr := DropStructure(6)
	inter := tr.Interleaved()
	// Raw float32-LE reader.
	raw := make([]byte, 0, 4*len(inter))
	for _, v := range inter {
		bits := math.Float32bits(v)
		raw = append(raw, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	}
	viaReader, err := AnalyzeFullReader(context.Background(), bytes.NewReader(raw), "fp", "dec")
	if err != nil {
		t.Fatal(err)
	}
	viaChunks := analyzeChunks(tr, 8192)
	if !reflect.DeepEqual(viaReader.Bands, viaChunks.Bands) {
		t.Fatal("reader input and chunked input disagree")
	}
}

func TestAnalyzeBoundedWarnings(t *testing.T) {
	// Every warning must be a bounded ASCII English sentence.
	tr, _ := CorpusFixture("antiphase", 2, 1)
	a := analyzeChunks(tr, 4096)
	for _, w := range a.Warnings {
		if len(w) > 1024 {
			t.Fatalf("warning too long: %d bytes", len(w))
		}
		for i := 0; i < len(w); i++ {
			if w[i] >= 0x80 {
				t.Fatalf("non-ASCII warning byte at %d: %q", i, w)
			}
		}
	}
}
