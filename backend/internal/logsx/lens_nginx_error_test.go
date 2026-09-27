package logsx

import (
	"testing"
	"time"
)

func TestLensNginxError(t *testing.T) {
	webLensCheck(t, "nginx-error", webNginxErrorLines())
}

// webNginxErrorLines are shared with the nginx composite. The bind failures
// are this project's public host, where nginx loses port 80 to Caddy at every
// boot; the timeout and rate-limit lines are the research's; the rest were
// provoked from nginx 1.26.3 on the same host — a closed port, a silent one,
// one that hangs up, a TLS upstream that is not, a dead upstream group, a
// body limit, a rate limit, a deny rule, a reload, a killed worker, a
// misspelt directive — with the scratch prefix of their paths shortened to
// /srv/lens.
func webNginxErrorLines() []webLensLine {
	at := func(hour, minute, second int) time.Time {
		return time.Date(2026, 9, 27, hour, minute, second, 0, time.Local)
	}
	sandbox := func(conn, path string) map[string]string {
		return map[string]string{
			"pid": "214668", "conn": conn, "client": "127.0.0.1", "host": "127.0.0.1:18980", "method": "GET",
			"path": path,
		}
	}
	with := func(attrs map[string]string, more ...string) map[string]string {
		for i := 0; i+1 < len(more); i += 2 {
			attrs[more[i]] = more[i+1]
		}
		return attrs
	}
	return []webLensLine{
		{
			// A port someone else holds is the configuration's to fix.
			text:  `2026/09/27 07:20:01 [emerg] 1088#1088: bind() to 0.0.0.0:80 failed (98: Address already in use)`,
			level: "critical", at: at(7, 20, 1), event: "config",
			attrs: map[string]string{"pid": "1088", "code": "98"},
		},
		{
			text:  `2026/09/27 07:20:01 [emerg] 1088#1088: still could not bind()`,
			level: "critical", at: at(7, 20, 1), event: "config",
			attrs: map[string]string{"pid": "1088"},
		},
		{
			text:  `2021/07/12 00:00:20 [error] 26211#26211: *698772 upstream timed out (110: Connection timed out) while reading response header from upstream, client: 1.2.3.4, server: example.com, request: "GET /a HTTP/1.1", upstream: "http://127.0.0.1:808/a", host: "example.com"`,
			level: "error", at: time.Date(2021, 7, 12, 0, 0, 20, 0, time.Local), event: "upstream_timeout",
			attrs: map[string]string{
				"pid": "26211", "conn": "698772", "code": "110", "client": "1.2.3.4", "host": "example.com",
				"method": "GET", "path": "/a", "upstream": "127.0.0.1:808",
			},
		},
		{
			text:  `2024/12/28 15:46:30 [error] 19273#19273: *2543 limiting requests, excess: 0.561 by zone "one", client: 192.168.8.1, server: localhost, request: "GET /222 HTTP/1.1", host: "192.168.8.121:8080"`,
			level: "error", at: time.Date(2024, 12, 28, 15, 46, 30, 0, time.Local), event: "rate_limited",
			attrs: map[string]string{
				"pid": "19273", "conn": "2543", "client": "192.168.8.1", "host": "192.168.8.121:8080",
				"method": "GET", "path": "/222",
			},
		},
		{
			text:  `2026/09/27 13:25:40 [notice] 214666#214666: nginx/1.26.3 (Ubuntu)`,
			level: "info", at: at(13, 25, 40), event: "startup",
			attrs: map[string]string{"pid": "214666"},
		},
		{
			text:  `2026/09/27 13:25:40 [notice] 214666#214666: getrlimit(RLIMIT_NOFILE): 1048576:1073741816`,
			level: "info", at: at(13, 25, 40),
			attrs: map[string]string{"pid": "214666"},
		},
		{
			text:  `2026/09/27 13:25:41 [info] 214668#214668: *1 client 127.0.0.1 closed keepalive connection`,
			level: "info", at: at(13, 25, 41),
			attrs: map[string]string{"pid": "214668", "conn": "1"},
		},
		{
			text:  `2026/09/27 13:25:41 [error] 214668#214668: *2 open() "/srv/lens/html/missing" failed (2: No such file or directory), client: 127.0.0.1, server: sandbox.test, request: "GET /missing HTTP/1.1", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 41), event: "not_found",
			attrs: with(sandbox("2", "/missing"), "code", "2"),
		},
		{
			text:  `2026/09/27 13:25:41 [error] 214668#214668: *3 connect() failed (111: Connection refused) while connecting to upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /refused HTTP/1.1", upstream: "http://127.0.0.1:18901/refused", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 41), event: "upstream_refused",
			attrs: with(sandbox("3", "/refused"), "code", "111", "upstream", "127.0.0.1:18901"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *5 upstream timed out (110: Connection timed out) while reading response header from upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /slow HTTP/1.1", upstream: "http://127.0.0.1:18902/slow", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "upstream_timeout",
			attrs: with(sandbox("5", "/slow"), "code", "110", "upstream", "127.0.0.1:18902"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *7 upstream prematurely closed connection while reading response header from upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /closer HTTP/1.1", upstream: "http://127.0.0.1:18903/closer", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "upstream_closed",
			attrs: with(sandbox("7", "/closer"), "upstream", "127.0.0.1:18903"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *9 peer closed connection in SSL handshake while SSL handshaking to upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /tlsup HTTP/1.1", upstream: "https://127.0.0.1:18903/tlsup", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "ssl_error",
			attrs: with(sandbox("9", "/tlsup"), "upstream", "127.0.0.1:18903"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *16 limiting requests, excess: 1.000 by zone "one", client: 127.0.0.1, server: sandbox.test, request: "GET /limited HTTP/1.1", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "rate_limited",
			attrs: sandbox("16", "/limited"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *17 access forbidden by rule, client: 127.0.0.1, server: sandbox.test, request: "GET /deny HTTP/1.1", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "forbidden",
			attrs: sandbox("17", "/deny"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *18 directory index of "/srv/lens/html/dir/" is forbidden, client: 127.0.0.1, server: sandbox.test, request: "GET /dir/ HTTP/1.1", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "forbidden",
			attrs: sandbox("18", "/dir/"),
		},
		{
			text:  `2026/09/27 13:25:42 [error] 214668#214668: *19 client intended to send too large body: 10240 bytes, client: 127.0.0.1, server: sandbox.test, request: "POST / HTTP/1.1", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 42), event: "body_too_large",
			attrs: with(sandbox("19", "/"), "method", "POST"),
		},
		{
			// A client handshake names no Host, so the server block stands in.
			text:  `2026/09/27 13:25:42 [info] 214668#214668: *20 SSL_do_handshake() failed (SSL: error:0A000102:SSL routines::unsupported protocol) while SSL handshaking, client: 127.0.0.1, server: 127.0.0.1:18943`,
			level: "info", at: at(13, 25, 42), event: "ssl_error",
			attrs: map[string]string{"pid": "214668", "conn": "20", "client": "127.0.0.1", "host": "127.0.0.1:18943"},
		},
		{
			text:  `2026/09/27 13:25:42 [info] 214668#214668: *21 client sent plain HTTP request to HTTPS port while reading client request headers, client: 127.0.0.1, server: sandbox.test, request: "GET / HTTP/1.1", host: "127.0.0.1:18943"`,
			level: "info", at: at(13, 25, 42),
			attrs: map[string]string{
				"pid": "214668", "conn": "21", "client": "127.0.0.1", "host": "127.0.0.1:18943", "method": "GET",
				"path": "/",
			},
		},
		{
			text:  `2026/09/27 13:25:42 [notice] 214667#214667: signal 1 (SIGHUP) received from 214766, reconfiguring`,
			level: "info", at: at(13, 25, 42),
			attrs: map[string]string{"pid": "214667"},
		},
		{
			text:  `2026/09/27 13:25:42 [notice] 214667#214667: reconfiguring`,
			level: "info", at: at(13, 25, 42), event: "reload",
			attrs: map[string]string{"pid": "214667"},
		},
		{
			text:  `2026/09/27 13:25:59 [warn] 215481#215481: *22 upstream server temporarily disabled while connecting to upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /group HTTP/1.1", upstream: "http://127.0.0.1:18901/group", host: "127.0.0.1:18980"`,
			level: "warn", at: at(13, 25, 59),
			attrs: with(sandbox("22", "/group"), "pid", "215481", "upstream", "127.0.0.1:18901"),
		},
		{
			// The upstream is a named block, and its name is the address.
			text:  `2026/09/27 13:25:59 [error] 215481#215481: *25 no live upstreams while connecting to upstream, client: 127.0.0.1, server: sandbox.test, request: "GET /group HTTP/1.1", upstream: "http://dead/group", host: "127.0.0.1:18980"`,
			level: "error", at: at(13, 25, 59), event: "no_live_upstreams",
			attrs: with(sandbox("25", "/group"), "pid", "215481", "upstream", "dead"),
		},
		{
			text:  `2026/09/27 13:25:59 [alert] 214667#214667: worker process 215481 exited on signal 9`,
			level: "critical", at: at(13, 25, 59), event: "error",
			attrs: map[string]string{"pid": "214667"},
		},
		{
			text:  `2026/09/27 13:26:09 [emerg] 215686#215686: unknown directive "proxy_bufer_size" in /srv/lens/bad.conf:12`,
			level: "critical", at: at(13, 26, 9), event: "config",
			attrs: map[string]string{"pid": "215686"},
		},
		{
			// The same refusal as `nginx -t` prints it: no stamp, no pid.
			text:  `nginx: [emerg] unknown directive "proxy_bufer_size" in /srv/lens/bad.conf:12`,
			level: "critical", event: "config",
		},
		{
			text: `nginx: configuration file /srv/lens/bad.conf test failed`,
		},
	}
}

func BenchmarkLensNginxError(b *testing.B) {
	lines := []string{
		`2021/07/12 00:00:20 [error] 26211#26211: *698772 upstream timed out (110: Connection timed out) while reading response header from upstream, client: 1.2.3.4, server: example.com, request: "GET /a HTTP/1.1", upstream: "http://127.0.0.1:808/a", host: "example.com"`,
		`2026/09/27 13:25:41 [info] 214668#214668: *1 client 127.0.0.1 closed keepalive connection`,
		`2026/09/27 07:20:01 [emerg] 1088#1088: bind() to 0.0.0.0:80 failed (98: Address already in use)`,
		`2026/09/27 13:25:41 [error] 214668#214668: *2 open() "/srv/lens/html/missing" failed (2: No such file or directory), client: 127.0.0.1, server: sandbox.test, request: "GET /missing HTTP/1.1", host: "127.0.0.1:18980"`,
	}
	parsed := make([]Line, len(lines))
	for i, text := range lines {
		parsed[i] = ParseLine(text, "error.log")
	}
	r := lenses["nginx-error"].New()
	b.ReportAllocs()
	for b.Loop() {
		for _, l := range parsed {
			r.Read(&l)
		}
	}
}
