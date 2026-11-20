package gogui

// one_window_test.go — source-level enforcement of the one-window /
// one-shell rule. The go-gui window cannot be opened in unit tests
// (no display in CI), so the strongest display-less guarantee is a
// source scan: exactly ONE window construction may exist in the shell
// package (the newShellWindow seam call in shell.go's Run), the seam
// must be the only way a window comes into existence, and no foreign
// shell imports (no Wails, no Fyne, no Gio) may appear.

import (
	"go/parser"
	"go/token"
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
	seamCalls := []string{}
	directConstructions := []string{}
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
		// The seam call: exactly one — inside Run.
		if i := strings.Count(src, "newShellWindow("); i > 0 {
			seamCalls = append(seamCalls, filepath.Join(e.Name(), "newShellWindow(")+":"+itoa1(i))
		}
		// Any direct window construction bypasses the seam and is a
		// contract violation. (The seam's default value is the bare
		// identifier `gui.NewWindow` without call parentheses.)
		for _, pat := range []string{"gui.NewWindow(", "gui.SimpleWindow(", "new(gui.Window)", "&gui.Window{"} {
			if i := strings.Count(src, pat); i > 0 {
				directConstructions = append(directConstructions, filepath.Join(e.Name(), pat)+":"+itoa1(i))
			}
		}
		for _, pat := range []string{"transport/wails", "fyne.io/fyne", "gioui.org"} {
			if strings.Contains(src, pat) {
				forks = append(forks, e.Name()+" imports "+pat)
			}
		}
	}
	if len(seamCalls) != 1 {
		t.Errorf("exactly one window construction is required in the native shell (the newShellWindow seam in Run), found %d: %v",
			len(seamCalls), seamCalls)
	}
	if len(directConstructions) != 0 {
		t.Errorf("windows must only be constructed through the newShellWindow seam; found direct constructions: %v",
			directConstructions)
	}
	if len(forks) != 0 {
		t.Errorf("the native shell must not import another shell or toolkit: %v", forks)
	}
}

// TestSingleGoGUIImportSurface pins the import surface: the only UI
// framework this shell may import is the go-gui module (gui, backend,
// soft); no other GUI toolkit and no gio import path may appear. The
// scan parses the real import blocks (go/parser), so string literals
// in view code cannot confuse it.
func TestSingleGoGUIImportSurface(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			if path == "" {
				continue
			}
			switch {
			case strings.HasPrefix(path, "github.com/go-gui-org/go-gui/"),
				strings.HasPrefix(path, "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/"),
				// The standard library and the core-boundary dependency
				// families the service layer already uses.
				isShellStdOrCoreDep(path):
			default:
				t.Errorf("%s imports non-contract module %q (only go-gui, this module and std/core deps are allowed)", e.Name(), path)
			}
		}
	}
}

// isShellStdOrCoreDep names the modules the shell legitimately
// consumes: the standard library (first path element carries no dot)
// and the core-boundary dependency families the service layer already
// uses.
func isShellStdOrCoreDep(path string) bool {
	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	if !strings.Contains(first, ".") {
		return true // standard library
	}
	return strings.HasPrefix(path, "golang.org/x/")
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
