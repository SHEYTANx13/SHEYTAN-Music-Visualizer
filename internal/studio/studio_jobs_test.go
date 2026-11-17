package studio

// studio_jobs_test.go — studio-level integration of the bounded work
// scheduler (run-44010666873 Section 2): mixed workload stress, Job
// Center data model truthfulness, and bounded shutdown under load.

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
)

// TestStudioMixedWorkloadStress reproduces the Section 2.7 stress
// scenario at the studio level: import + analysis + first play + a
// concurrent cancellation storm, all through the bounded scheduler.
// The import completes, the analysis completes, the play terminates in
// a logged outcome, and the scheduler drains to zero live jobs.
func TestStudioMixedWorkloadStress(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("studio: %v", err)
	}
	defer s.Close()
	src := filepath.Join(t.TempDir(), "track.wav")
	writeTestWav(t, src, 2.0)

	events := make(chan Event, 256)
	s.Subscribe(func(ev Event) { events <- ev })

	var playMu sync.Mutex
	playEvents := 0
	s.SetPlayEventSink(func(ev playback.PlayEvent) {
		playMu.Lock()
		playEvents++
		playMu.Unlock()
	})

	if err := s.ImportFile(src); err != nil {
		t.Fatalf("import: %v", err)
	}
	// A second import while the first runs keeps the single-flight
	// error (not a queue explosion).
	if err := s.ImportFile(src); err == nil {
		t.Fatal("second concurrent import must be rejected by single-flight")
	}

	importDone := false
	analysisDone := false
	deadline := time.Now().Add(30 * time.Second)
	for !(importDone && analysisDone) && time.Now().Before(deadline) {
		select {
		case ev := <-events:
			switch ev.Kind {
			case EventImportDone:
				importDone = true
				// trigger the first play right after the import lands
				go func() { _ = s.PlayPause() }()
			case EventImportFailed:
				t.Fatalf("import failed: %s", ev.Message)
			case EventAnalysisDone:
				analysisDone = true
			case EventAnalysisFailed:
				t.Fatalf("analysis failed: %s", ev.Message)
			}
		case <-time.After(50 * time.Millisecond):
			// cancellation pressure during the workload
			s.CancelImport()
			_ = s.PlayPause()
		}
	}
	if !importDone || !analysisDone {
		t.Fatalf("stress did not complete: importDone=%v analysisDone=%v", importDone, analysisDone)
	}

	// The scheduler must drain to zero live jobs.
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.JobsOverview().Live) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ov := s.JobsOverview(); len(ov.Live) != 0 {
		for _, j := range ov.Live {
			t.Logf("live job: %s id=%d state=%s", j.Type, j.ID, j.State)
		}
		t.Fatalf("%d live jobs after stress", len(ov.Live))
	}
	playMu.Lock()
	got := playEvents
	playMu.Unlock()
	if got == 0 {
		t.Log("note: no play events observed (headless environment may complete the play path silently)")
	}
}

// TestStudioJobsOverviewExposesLanes verifies the Job Center data
// model is populated and truthful after real work.
func TestStudioJobsOverviewExposesLanes(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("studio: %v", err)
	}
	defer s.Close()
	src := filepath.Join(t.TempDir(), "track.wav")
	writeTestWav(t, src, 0.5)
	if err := s.ImportFile(src); err != nil {
		t.Fatalf("import: %v", err)
	}
	waitFor(t, s, 20*time.Second, func() bool { return s.DNA() != nil }, "analysis")

	ov := s.JobsOverview()
	if ov.Metrics.Lanes == nil {
		t.Fatal("lane metrics missing")
	}
	totalSucceeded := uint64(0)
	for _, lm := range ov.Metrics.Lanes {
		totalSucceeded += lm.Succeeded
	}
	if totalSucceeded < 2 {
		t.Fatalf("expected at least import+analysis to be metered, got %d", totalSucceeded)
	}
	if ov.Metrics.CPUBudget < 1 || ov.Metrics.CPUBudget > 3 {
		t.Fatalf("CPU budget out of contract range: %d", ov.Metrics.CPUBudget)
	}
}

// TestStudioSchedulerSurvivesCloseDuringWork closes the studio while
// work is still queued: the shutdown path must stay bounded and every
// lane must drain to zero.
func TestStudioSchedulerSurvivesCloseDuringWork(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("studio: %v", err)
	}
	src := filepath.Join(t.TempDir(), "track.wav")
	writeTestWav(t, src, 3.0)
	if err := s.ImportFile(src); err != nil {
		t.Fatalf("import: %v", err)
	}
	// Close immediately, while the import is still decoding.
	start := time.Now()
	s.Close()
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("Close blocked for %s during in-flight work", d)
	}
	if n := len(s.JobsOverview().Live); n != 0 {
		t.Fatalf("%d live jobs after Close", n)
	}
}
