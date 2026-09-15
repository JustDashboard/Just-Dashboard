package httpx

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack forwards to the underlying writer. Without it a wrapped writer
// silently breaks every WebSocket route in the dashboard: the upgrader needs
// the raw connection, and not every version of it goes through
// http.ResponseController to ask for one.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return h.Hijack()
}

// Flush keeps long-lived streaming responses (log exports, archive downloads)
// from being buffered until the handler returns.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		f.Flush()
	}
}

func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					log.Error("panic serving request",
						"method", r.Method, "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
					JSON(w, http.StatusInternalServerError, map[string]any{
						"error": map[string]string{"code": "internal", "message": "internal error"},
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			log.Debug("request",
				"method", r.Method, "path", r.URL.Path, "status", sw.Status(),
				"bytes", sw.bytes, "dur", time.Since(start).String(), "ip", ClientIP(r))
		})
	}
}

// SecurityHeaders keeps the dashboard from being framed or sniffed. The API
// defaults to JSON and streams. The deployment preview wrapper overrides its
// own frame policy explicitly; dashboard documents remain unframeable.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// RealIP resolves the peer address, consulting X-Forwarded-For only when the
// immediate peer is a configured trusted proxy. Without that check any client
// could spoof its way past the allowlist by setting the header itself.
func RealIP(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := peerIP(r)
			resolved := peer
			if peer != nil && ipInAny(peer, trusted) {
				if fwd := forwardedFor(r); fwd != nil {
					resolved = fwd
				}
			}
			if resolved != nil {
				r = r.WithContext(withIP(r.Context(), resolved.String()))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AllowlistCIDRs rejects any request whose resolved client address is outside
// the operator's allowlist. This runs before authentication so that an
// unauthenticated attacker on the open internet cannot even reach the login
// handler, let alone brute-force it.
func AllowlistCIDRs(allowed []*net.IPNet, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := ClientIP(r)
			ip := net.ParseIP(raw)
			if ip == nil || !ipInAny(ip, allowed) {
				log.Warn("blocked by network allowlist", "ip", raw, "path", r.URL.Path)
				JSON(w, http.StatusForbidden, map[string]any{
					"error": map[string]string{
						"code":    "network_denied",
						"message": "your network is not permitted to reach this dashboard",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func peerIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func forwardedFor(r *http.Request) net.IP {
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP")))
	}
	parts := strings.Split(xff, ",")
	// The right-most entry is the one our trusted proxy observed; entries to
	// the left are attacker-controlled.
	return net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
}

func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
