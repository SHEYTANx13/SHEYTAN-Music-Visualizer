package visualizer

import (
	"context"
	"io"
)

func testCtx() context.Context { return context.Background() }

// byteReader adapts a byte slice to io.Reader.
type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}
