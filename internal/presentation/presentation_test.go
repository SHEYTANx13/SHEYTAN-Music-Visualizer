package presentation

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
)

func TestDestinationCatalogComplete(t *testing.T) {
	cat := DestinationCatalog()
	if len(cat) != 12 {
		t.Fatalf("catalog has %d destinations, want 12", len(cat))
	}
	for i, d := range cat {
		if d.Order != i+1 {
			t.Errorf("destination %s order %d, want %d", d.ID, d.Order, i+1)
		}
		if err := ValidateDestination(d.ID); err != nil {
			t.Errorf("destination %s invalid: %v", d.ID, err)
		}
	}
}

func TestValidateDestinationOrderRejectsBadPermutations(t *testing.T) {
	if err := ValidateDestinationOrder([]Destination{DestHome}); err == nil {
		t.Fatal("short order accepted")
	}
	dup := append([]Destination{}, Destinations...)
	dup[3] = DestHome
	if err := ValidateDestinationOrder(dup); err == nil {
		t.Fatal("duplicate order accepted")
	}
	if err := ValidateDestinationOrder(append([]Destination{}, Destinations...)); err != nil {
		t.Fatalf("canonical order rejected: %v", err)
	}
}

func TestCommandIDsStable(t *testing.T) {
	for _, c := range []CommandID{CmdImportAudio, CmdPlayPause, CmdExportStart, CmdNavigate, CmdQuit} {
		if err := ValidateCommand(c); err != nil {
			t.Errorf("command %s invalid: %v", c, err)
		}
	}
	if err := ValidateCommand("bogus.command"); err == nil {
		t.Fatal("unknown command accepted")
	}
}

func TestEventBusCoalescesHighFrequencyKinds(t *testing.T) {
	bus := NewEventBus(16)
	for i := 0; i < 1000; i++ {
		bus.Publish(EvTransportClock, 7, TransportDTO{Session: 7})
	}
	envs, _ := bus.Drain()
	if len(envs) != 1 {
		t.Fatalf("coalesced clock produced %d envelopes, want 1", len(envs))
	}
	if envs[0].Generation != 7 {
		t.Fatalf("envelope generation %d, want 7", envs[0].Generation)
	}
}

func TestEventBusDiscreteBoundedDropOldest(t *testing.T) {
	bus := NewEventBus(16)
	for i := 0; i < 100; i++ {
		bus.Publish(EvLibraryChanged, 1, i)
	}
	envs, dropped := bus.Drain()
	if len(envs) != 16 {
		t.Fatalf("discrete queue length %d, want 16 (bounded)", len(envs))
	}
	if dropped.Count == 0 {
		t.Fatal("overflow not accounted")
	}
	// Order must be preserved with drop-oldest: last event is the newest.
	last := envs[len(envs)-1].Payload.(int)
	if last != 99 {
		t.Fatalf("newest discrete event lost: got %d", last)
	}
}

func TestEventBusGenerationResetClearsPending(t *testing.T) {
	bus := NewEventBus(16)
	bus.Publish(EvTransportState, 1, "stale")
	bus.SetGeneration(2)
	bus.Publish(EvTransportState, 2, "fresh")
	envs, _ := bus.Drain()
	if len(envs) != 1 || envs[0].Payload.(string) != "fresh" {
		t.Fatalf("stale envelope survived generation reset: %+v", envs)
	}
}

func TestEventBusConcurrentPublishNoLostUpdate(t *testing.T) {
	bus := NewEventBus(256)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				bus.Publish(EvDiagnostics, 1, i)
			}
		}()
	}
	wg.Wait()
	envs, _ := bus.Drain()
	total := len(envs)
	if total != 256 { // bounded queue, drop-oldest, exact capacity
		t.Fatalf("queue holds %d, want 256", total)
	}
}

func TestDTORoundTrip(t *testing.T) {
	lay := DefaultLayout()
	b, err := json.Marshal(lay)
	if err != nil {
		t.Fatalf("marshal layout: %v", err)
	}
	var back WorkspaceLayoutDTO
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal layout: %v", err)
	}
	if back.Version != DTOVersion || len(back.TabOrder) != 12 {
		t.Fatalf("layout round trip broken: %+v", back)
	}
}

func TestLayoutStoreRoundTripAndRecovery(t *testing.T) {
	path := t.TempDir() + "/nested/workspace-layout.json"
	store := NewLayoutStore(path)
	lay, err := store.Load()
	if err != nil || lay.ActiveTab != DestHome {
		t.Fatalf("first load: lay=%+v err=%v", lay, err)
	}
	lay.ActiveTab = DestStudio
	if err := store.Save(lay); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Load()
	if err != nil || got.ActiveTab != DestStudio {
		t.Fatalf("reload: lay=%+v err=%v", got, err)
	}
	// Corrupt state recovers to default with ErrRecovered.
	if err := writeFile(path, []byte("{corrupt json")); err != nil {
		t.Fatal(err)
	}
	rec, err := store.Load()
	if rec.ActiveTab != DestHome {
		t.Fatalf("corrupt recovery returned %+v", rec)
	}
	if err == nil || err != ErrRecovered {
		t.Fatalf("corrupt recovery err=%v, want ErrRecovered", err)
	}
	// v0 migration (fixture updated for the twelve-destination set:
	// songdna is a first-class destination since repair/run-46033044814).
	if err := writeFile(path, []byte(`{"order":["home","studio","worlds","templates","library","songdna","export","requirements","downloads","system-files","settings","diagnostics"]}`)); err != nil {
		t.Fatal(err)
	}
	mig, err := store.Load()
	if err != nil {
		t.Fatalf("v0 migration err=%v", err)
	}
	if mig.Version != 1 || mig.TabOrder[0] != DestHome {
		t.Fatalf("v0 migration result %+v", mig)
	}
}

func writeFile(path string, b []byte) error {
	return osWriteFile(path, b)
}

func osWriteFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644)
}
