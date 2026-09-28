package logsx

import (
	"testing"
	"time"
)

// The composite must read every line exactly as the lens it hands the line to
// would, and then hold both kinds in one stream, the way `docker logs` hands
// over an nginx container's: the access log on stdout and the error log on
// stderr, interleaved. The stream's lines are nginx 1.26.3's on this project's
// host, each request and the error line it caused, then a start-up notice and
// a refused configuration.
func TestLensNginx(t *testing.T) {
	webLensCheck(t, "nginx", webHTTPAccessLines())
	webLensCheck(t, "nginx", webNginxErrorLines())
	at := func(second int) time.Time { return time.Date(2026, 9, 27, 13, 25, second, 0, time.Local) }
	webLensCheck(t, "nginx", []webLensLine{
		{
			text:  `127.0.0.1 - - [27/Sep/2026:13:25:41 +0000] "GET /refused HTTP/1.1" 502 157 "-" "curl/8.12.1"`,
			level: "error", at: time.Date(2026, 9, 27, 13, 25, 41, 0, time.UTC), event: "server_error",
			attrs: map[string]string{
				"method": "GET", "path": "/refused", "proto": "HTTP/1.1", "status": "502", "class": "5xx",
				"bytes": "157", "client": "127.0.0.1", "agent": "curl", "bot": "1",
			},
		},
		{
			text:  `2026/09/27 13:25:41 [error] 214668#214668: *3 connect() failed (111: Connection refused) while connecting to upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /refused HTTP/1.1", upstream: "http://127.0.0.1:18901/refused", host: "127.0.0.1:18980"`,
			level: "error", at: at(41), event: "upstream_refused",
			attrs: map[string]string{
				"pid": "214668", "conn": "3", "code": "111", "client": "127.0.0.1", "host": "127.0.0.1:18980",
				"method": "GET", "path": "/refused", "upstream": "127.0.0.1:18901",
			},
		},
		{
			text:  `127.0.0.1 - - [27/Sep/2026:13:25:42 +0000] "POST / HTTP/1.1" 413 183 "-" "curl/8.12.1"`,
			level: "warn", at: time.Date(2026, 9, 27, 13, 25, 42, 0, time.UTC), event: "client_error",
			attrs: map[string]string{
				"method": "POST", "path": "/", "proto": "HTTP/1.1", "status": "413", "class": "4xx", "bytes": "183",
				"client": "127.0.0.1", "agent": "curl", "bot": "1",
			},
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *19 client intended to send too large body: 10240 bytes, client: 127.0.0.1, server: sandbox.test, request: "POST / HTTP/1.1", host: "127.0.0.1:18980"`,
			level: "error", at: at(42), event: "body_too_large",
			attrs: map[string]string{
				"pid": "214668", "conn": "19", "client": "127.0.0.1", "host": "127.0.0.1:18980", "method": "POST",
				"path": "/",
			},
		},
		{
			text:  `2026/09/27 13:25:42 [notice] 214667#214667: start worker processes`,
			level: "info", at: at(42),
			attrs: map[string]string{"pid": "214667"},
		},
		{
			text:  `nginx: [emerg] unknown directive "proxy_bufer_size" in /srv/lens/bad.conf:12`,
			level: "critical", event: "config",
		},
	})
}

func BenchmarkLensNginx(b *testing.B) {
	lines := []string{
		`127.0.0.1 - - [27/Sep/2026:13:25:41 +0000] "GET /refused HTTP/1.1" 502 157 "-" "curl/8.12.1"`,
		`2026/09/27 13:25:41 [error] 214668#214668: *3 connect() failed (111: Connection refused) while connecting to upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /refused HTTP/1.1", upstream: "http://127.0.0.1:18901/refused", host: "127.0.0.1:18980"`,
		`186.236.254.56 - - [20/Aug/2026:03:28:45 +0000] "GET /wp-login.php HTTP/1.1" 404 196 "-" "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"`,
		`2026/09/27 13:25:42 [notice] 214667#214667: start worker processes`,
	}
	parsed := make([]Line, len(lines))
	for i, text := range lines {
		parsed[i] = ParseLine(text, "")
	}
	r := lenses["nginx"].New()
	b.ReportAllocs()
	for b.Loop() {
		for _, l := range parsed {
			r.Read(&l)
		}
	}
}
