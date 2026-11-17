//go:build !unix

package fileaccess

// mkfifoUnix is unavailable off Unix; the special-file test skips.
func mkfifoUnix(path string) error {
	return errNoFifo
}

var errNoFifo = errorString("fifo unavailable on this platform")

type errorString string

func (e errorString) Error() string { return string(e) }
