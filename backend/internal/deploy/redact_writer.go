package deploy

import (
	"io"
	"sort"
	"strings"
	"sync"
)

// secretWriter retains incomplete matches across writes. Command stdout and
// stderr can arrive concurrently and may split a secret at any byte boundary.
type secretWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	secrets []string
	pending string
}

func newSecretWriter(dst io.Writer, env map[string]string) *secretWriter {
	w := &secretWriter{dst: dst}
	for _, value := range env {
		if value != "" {
			w.secrets = append(w.secrets, value)
		}
	}
	sort.Slice(w.secrets, func(i, j int) bool { return len(w.secrets[i]) > len(w.secrets[j]) })
	return w
}
func (w *secretWriter) sanitize(text string) string {
	for _, secret := range w.secrets {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}
func (w *secretWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(p)
	if err := w.flush(false); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (w *secretWriter) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.flush(true) }
func (w *secretWriter) flush(final bool) error {
	var output strings.Builder
	for len(w.pending) > 0 {
		incomplete := false
		if !final {
			for _, secret := range w.secrets {
				if strings.HasPrefix(secret, w.pending) {
					incomplete = true
					break
				}
			}
		}
		if incomplete {
			break
		}
		matched := false
		for _, secret := range w.secrets {
			if strings.HasPrefix(w.pending, secret) {
				output.WriteString("[REDACTED]")
				w.pending = w.pending[len(secret):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		output.WriteByte(w.pending[0])
		w.pending = w.pending[1:]
	}
	if output.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(w.dst, output.String())
	return err
}
