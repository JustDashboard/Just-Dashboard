package dockerx

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
)

// maxLogLine bounds a single line so a container emitting a gigabyte without a
// newline cannot exhaust the dashboard's memory.
const maxLogLine = 256 * 1024

func scanLines(ctx context.Context, r io.Reader, stream string, out chan<- LogLine) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLogLine)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- LogLine{Stream: stream, Text: line}:
		}
	}
	return sc.Err()
}

type logReadCloser struct {
	io.ReadCloser
	mu  sync.Mutex
	err error
}

func (r *logReadCloser) record(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = errors.Join(r.err, err)
}

func (r *logReadCloser) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// LogReadError preserves failures discovered after opening the asynchronous
// reader. A closed channel alone cannot distinguish EOF from a failed scan.
func LogReadError(closer io.Closer) error {
	if reader, ok := closer.(interface{ Err() error }); ok {
		return reader.Err()
	}
	return nil
}

// Docker's demultiplexer treats EOF inside a frame as success. Track only
// the framing state so an incomplete response cannot certify a full export.
type completeLogFrames struct {
	io.Reader
	header    [8]byte
	held      int
	remaining uint32
}

func (r *completeLogFrames) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	for chunk := p[:n]; len(chunk) > 0; {
		if r.remaining > 0 {
			take := len(chunk)
			if uint64(take) > uint64(r.remaining) {
				take = int(r.remaining)
			}
			r.remaining -= uint32(take)
			chunk = chunk[take:]
			continue
		}
		take := copy(r.header[r.held:], chunk)
		r.held += take
		chunk = chunk[take:]
		if r.held == len(r.header) {
			r.remaining = binary.BigEndian.Uint32(r.header[4:])
			r.held = 0
		}
	}
	if err == io.EOF && (r.held != 0 || r.remaining != 0) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
