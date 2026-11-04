// Package buildinfo is the single version/build metadata authority for
// SHEYTAN Music Visualizer.
//
// The Version, Commit and BuildDate variables may be set at link time:
//
//	go build -ldflags "-X github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo.Version=0.1.0 \
//	                   -X .../buildinfo.Commit=abcdef -X .../buildinfo.BuildDate=2026-11-04T00:00:00Z"
//
// Values are normalized when read: unset or empty values fall back to safe
// development defaults, and whitespace is trimmed. There must be no second
// competing version constant anywhere else in the product.
package buildinfo

import (
	"errors"
	"runtime"
	"strings"
)

// Link-time variables. Defaults describe an untagged development build.
var (
	Version   = "0.1.0-dev" // default dev version authority (recorded in ai-notes.md)
	Commit    = "unknown"
	BuildDate = "unknown"
)

const (
	defaultVersion   = "0.1.0-dev"
	defaultTimestamp = "unknown"
)

// Info is the immutable build identity snapshot.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

// Get returns the normalized build info.
func Get() Info {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = defaultVersion
	}
	c := strings.TrimSpace(Commit)
	if c == "" {
		c = defaultTimestamp
	}
	d := strings.TrimSpace(BuildDate)
	if d == "" {
		d = defaultTimestamp
	}
	return Info{
		Version:   v,
		Commit:    c,
		BuildDate: d,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
}

// String renders the canonical single-line version banner used by --version.
func (i Info) String() string {
	return i.Version + " (commit " + i.Commit + ", built " + i.BuildDate +
		", " + i.GoVersion + ", " + i.GOOS + "/" + i.GOARCH + ")"
}

// Validate reports whether the build metadata is internally consistent.
// It is part of the --self-check contract: it must never fail for the
// default development values, and must never pass with fabricated empties.
func Validate() error {
	i := Get()
	if i.Version == "" {
		return errors.New("buildinfo: version is empty")
	}
	if i.Commit == "" {
		return errors.New("buildinfo: commit is empty")
	}
	if i.BuildDate == "" {
		return errors.New("buildinfo: build date is empty")
	}
	if i.GoVersion == "" {
		return errors.New("buildinfo: go version unavailable")
	}
	return nil
}
