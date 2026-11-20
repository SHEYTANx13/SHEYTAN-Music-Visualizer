package gogui

// one_window_test.go — source-level enforcement of the one-window /
// one-shell rule. The gio window cannot be instantiated in unit tests
// (no display in CI), so the strongest display-less guarantee is a
// source scan: exactly ONE app.Window construction may exist in the
// shell package, no second window of any kind, and no foreign shell
// imports (no Wails, no Fyne, no widget-tree sharing).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExactlyOneWindowConstructionInSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	windowDecls := []string{}
	forks := []string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(b)
		for _, pat := range []string{"&app.Window{", "new(app.Window)", "app.NewWindow("} {
			if i := strings.Count(src, pat); i > 0 {
				windowDecls = append(windowDecls, filepath.Join(e.Name(), pat)+":"+itoa1(i))
			}
		}
		for _, pat := range []string{"transport/wails", "fyne.io/fyne", "gioui.org/app/headless"} {
			if strings.Contains(src, pat) {
				forks = append(forks, e.Name()+" imports "+pat)
			}
		}
	}
	if len(windowDecls) != 1 {
		t.Errorf("exactly one window construction is required in the native shell, found %d: %v",
			len(windowDecls), windowDecls)
	}
	if len(forks) != 0 {
		t.Errorf("the native shell must not import another shell or toolkit: %v", forks)
	}
}

func itoa1(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
