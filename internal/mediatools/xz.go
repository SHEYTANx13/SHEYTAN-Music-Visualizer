package mediatools

// xz.go — the xz stream decoder for the pinned Linux .tar.xz pack.
// The dependency is pure Go, pinned in go.mod, and used only for the
// app-managed optional download (never on any startup path).

import (
	"io"
	"os"

	"github.com/ulikunitz/xz"
)

func newXZReader(r io.Reader) (io.ReadCloser, error) {
	xr, err := xz.NewReader(r)
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{
		Reader: xr,
		Closer: noopCloser{},
	}, nil
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// ensure os stays referenced on platforms where the file helpers are
// only used by tests (compile-time anchor).
var _ = os.Stat
