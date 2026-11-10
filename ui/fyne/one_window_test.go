// one_window_test.go — the static architecture check protecting the
// ONE-MAIN-WINDOW invariant (run 42598929646 V14): the normal desktop
// startup path must create exactly one top-level Fyne window. This
// test scans the ui/fyne package sources for App.NewWindow calls and
// fails if any additional top-level window creation appears (native OS
// file pickers, dialogs attached to the main window, and browser
// hyperlinks are not top-level windows and are not affected).
//
// Runtime counterpart: Run() (studio.go) calls a.NewWindow exactly once
// and shows it once via ShowAndRun — this test locks that contract in
// place so future edits cannot silently open a second window.
package fyneapp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestExactlyOneTopLevelWindowCreation(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil || len(entries) == 0 {
		t.Fatalf("cannot read ui/fyne sources: %v", err)
	}
	newWindowRe := regexp.MustCompile(`\.NewWindow\(`)
	showAndRunRe := regexp.MustCompile(`\.ShowAndRun\(\)`)
	totalNewWindow := 0
	totalShowAndRun := 0
	locations := []string{}
	for _, f := range entries {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // comments are documentation, not code
			}
			if newWindowRe.MatchString(line) {
				totalNewWindow++
				locations = append(locations, f+":"+itoa1(i+1)+" NewWindow")
			}
			if showAndRunRe.MatchString(line) {
				totalShowAndRun++
				locations = append(locations, f+":"+itoa1(i+1)+" ShowAndRun")
			}
		}
	}
	if totalNewWindow != 1 {
		t.Fatalf("ui/fyne must create EXACTLY ONE top-level window; found %d:\n%s",
			totalNewWindow, strings.Join(locations, "\n"))
	}
	if totalShowAndRun != 1 {
		t.Fatalf("the single main window must be shown exactly once via ShowAndRun; found %d:\n%s",
			totalShowAndRun, strings.Join(locations, "\n"))
	}
}

// TestNoSecondDiagnosticOrHardwareWindow pins the System Integration
// placement contract: sysint.go attaches its surface to the EXISTING
// main window (u.win) — it must never construct a new window, and it
// must not spawn shells or consoles for the scan.
func TestNoSecondDiagnosticOrHardwareWindow(t *testing.T) {
	data, err := os.ReadFile("sysint.go")
	if err != nil {
		t.Fatalf("read sysint.go: %v", err)
	}
	s := string(data)
	for _, banned := range []string{"NewWindow(", "exec.Command", "os.StartProcess", "cmd.exe", "powershell", "wmic", "dxdiag"} {
		for i, line := range strings.Split(s, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, banned) {
				t.Fatalf("sysint.go:%d uses %q — the System Integration surface must stay inside the single main window and never spawn processes for detection", i+1, banned)
			}
		}
	}
	if !strings.Contains(s, "u.win)") {
		t.Fatal("sysint.go must attach its dialog to the existing main window (u.win)")
	}
}

func itoa1(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
