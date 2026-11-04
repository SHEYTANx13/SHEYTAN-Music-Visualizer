// Package buildinfo is the single version / build metadata authority for
// SHEYTAN Music Visualizer.
//
// Version, commit and build date may be injected at link time with
// -ldflags "-X github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo.version=...".
// Unset values fall back to safe development defaults; all values are
// normalized so consumers never see empty or multi-line fields.
package buildinfo

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"
)

// Linker-injected variables. Keep this the only competing version
// authority in the repository.
var (
	version = "0.1.0-dev"
	commit  = "unknown"
	date    = "unknown"
	builtBy = "local"
)

// Info is the normalized build metadata snapshot.
type Info struct {
	Version   string
	Commit    string
	Date      string
	BuiltBy   string
	GoVersion string
}

// sanitize collapses control characters (including newlines) and trims
// space so injected values cannot forge multi-line log records.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Normalized returns the validated build metadata. Empty injected values
// fall back to development defaults; control characters are collapsed.
func Normalized() Info {
	return Info{
		Version:   orDefault(sanitize(version), "0.1.0-dev"),
		Commit:    orDefault(sanitize(commit), "unknown"),
		Date:      orDefault(sanitize(date), "unknown"),
		BuiltBy:   orDefault(sanitize(builtBy), "local"),
		GoVersion: sanitize(runtime.Version()),
	}
}

// Line returns the single-line identity string used by --version.
func Line() string {
	i := Normalized()
	return fmt.Sprintf("%s (commit %s, built %s by %s, %s)",
		i.Version, i.Commit, i.Date, i.BuiltBy, i.GoVersion)
}
