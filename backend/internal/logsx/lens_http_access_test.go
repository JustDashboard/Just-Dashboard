package logsx

import (
	"maps"
	"testing"
	"time"
)

// webLensLine is one real line and everything a lens should read out of it.
// A zero time means the line carries no stamp the lens or the generic parse
// could read.
type webLensLine struct {
	text  string
	level string
	at    time.Time
	event string
	attrs map[string]string
}

// webLensCheck reads the lines through one reader in order, as a follow or a
// search would, and compares every field a lens may set. Stamps are compared
// to the microsecond: a float of unix seconds, which is how Caddy writes them,
// holds about a quarter of one.
func webLensCheck(t *testing.T, id string, lines []webLensLine) {
	t.Helper()
	texts := make([]string, len(lines))
	for i, want := range lines {
		texts[i] = want.text
	}
	for i, got := range readThrough(t, id, texts...) {
		want := lines[i]
		name := want.text
		if len(name) > 72 {
			name = name[:72] + "…"
		}
		if got.Event != want.event {
			t.Errorf("%s\n  event = %q, want %q", name, got.Event, want.event)
		}
		if got.Level != want.level {
			t.Errorf("%s\n  level = %q, want %q", name, got.Level, want.level)
		}
		if got.Cont {
			t.Errorf("%s\n  marked as continuing the line above", name)
		}
		switch {
		case want.at.IsZero() && got.Timestamp != nil:
			t.Errorf("%s\n  timestamp = %v, want none", name, got.Timestamp)
		case !want.at.IsZero() && got.Timestamp == nil:
			t.Errorf("%s\n  no timestamp, want %v", name, want.at)
		case !want.at.IsZero() && got.Timestamp.Sub(want.at).Abs() > time.Microsecond:
			t.Errorf("%s\n  timestamp = %v, want %v", name, got.Timestamp.UTC(), want.at.UTC())
		}
		if !maps.Equal(got.Attrs, want.attrs) {
			t.Errorf("%s\n  attrs = %v\n   want %v", name, got.Attrs, want.attrs)
		}
	}
}

func TestLensHTTPAccess(t *testing.T) {
	webLensCheck(t, "http-access", webHTTPAccessLines())
}

// webHTTPAccessLines are shared with the nginx composite, which must read an
// access line exactly as this lens does. The nginx lines are this project's
// own public host's access log, the Caddy ones its deployment ingress's; the
// Apache line is the one its manual uses to define the common format, and the
// IPv6 one is morgan's combined as the research recorded it.
func webHTTPAccessLines() []webLensLine {
	return []webLensLine{
		{
			text:  `93.123.109.228 - - [26/Aug/2026:00:08:38 +0000] "GET / HTTP/1.1" 200 14313 "-" "l9tcpid/v1.1.0"`,
			level: "info", at: time.Date(2026, 8, 26, 0, 8, 38, 0, time.UTC), event: "request",
			attrs: map[string]string{
				"method": "GET", "path": "/", "proto": "HTTP/1.1", "status": "200", "class": "2xx",
				"bytes": "14313", "client": "93.123.109.228", "agent": "l9tcpid", "bot": "1",
			},
		},
		{
			// A refused scanner is a probe, not a broken link.
			text:  `93.123.109.228 - - [26/Aug/2026:00:08:38 +0000] "GET /.git/config HTTP/1.1" 404 2797 "-" "l9explore/1.2.2"`,
			level: "warn", at: time.Date(2026, 8, 26, 0, 8, 38, 0, time.UTC), event: "probe",
			attrs: map[string]string{
				"method": "GET", "path": "/.git/config", "proto": "HTTP/1.1", "status": "404", "class": "4xx",
				"bytes": "2797", "client": "93.123.109.228", "agent": "l9explore", "bot": "1",
			},
		},
		{
			// A browser's agent does not make a PHP login page a visit.
			text:  `186.236.254.56 - - [20/Aug/2026:03:28:45 +0000] "GET /wp-login.php HTTP/1.1" 404 196 "-" "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"`,
			level: "warn", at: time.Date(2026, 8, 20, 3, 28, 45, 0, time.UTC), event: "probe",
			attrs: map[string]string{
				"method": "GET", "path": "/wp-login.php", "proto": "HTTP/1.1", "status": "404", "class": "4xx",
				"bytes": "196", "client": "186.236.254.56", "agent": "Chrome",
			},
		},
		{
			text:  `64.62.156.132 - - [26/Aug/2026:03:08:30 +0000] "GET /webui/ HTTP/1.1" 308 16 "-" "Mozilla/5.0 (Windows NT 10.0; rv:125.0) Gecko/20100101 Firefox/125.0"`,
			level: "info", at: time.Date(2026, 8, 26, 3, 8, 30, 0, time.UTC), event: "request",
			attrs: map[string]string{
				"method": "GET", "path": "/webui/", "proto": "HTTP/1.1", "status": "308", "class": "3xx",
				"bytes": "16", "client": "64.62.156.132", "agent": "Firefox",
			},
		},
		{
			text:  `194.88.98.112 - - [17/Aug/2026:00:58:51 +0000] "GET / HTTP/1.1" 502 166 "-" "Mozilla/5.0 (compatible; Infrawatch/1.0; +https://infrawat.ch/)"`,
			level: "error", at: time.Date(2026, 8, 17, 0, 58, 51, 0, time.UTC), event: "server_error",
			attrs: map[string]string{
				"method": "GET", "path": "/", "proto": "HTTP/1.1", "status": "502", "class": "5xx",
				"bytes": "166", "client": "194.88.98.112", "agent": "Mozilla", "bot": "1",
			},
		},
		{
			text:  `34.34.225.117 - - [26/Aug/2026:03:43:43 +0000] "HEAD / HTTP/1.1" 404 0 "-" "Python-urllib/3.14"`,
			level: "warn", at: time.Date(2026, 8, 26, 3, 43, 43, 0, time.UTC), event: "client_error",
			attrs: map[string]string{
				"method": "HEAD", "path": "/", "proto": "HTTP/1.1", "status": "404", "class": "4xx",
				"bytes": "0", "client": "34.34.225.117", "agent": "Python-urllib", "bot": "1",
			},
		},
		{
			text:  `195.178.110.204 - - [26/Aug/2026:04:45:46 +0000] "POST /boaform/admin/formLogin HTTP/1.1" 404 134 "http://57.131.21.87:80/admin/login.asp" "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:77.0) Gecko/20100101 Firefox/77.0"`,
			level: "warn", at: time.Date(2026, 8, 26, 4, 45, 46, 0, time.UTC), event: "probe",
			attrs: map[string]string{
				"method": "POST", "path": "/boaform/admin/formLogin", "proto": "HTTP/1.1", "status": "404",
				"class": "4xx", "bytes": "134", "client": "195.178.110.204", "agent": "Firefox",
				"referer": "http://57.131.21.87:80/admin/login.asp",
			},
		},
		{
			// A TLS handshake sent to the plain port: nginx escapes it into
			// a "request line", and the method it would read is noise.
			text:  `198.235.24.89 - - [26/Aug/2026:00:55:39 +0000] "\x16\x03\x01\x00\xEE\x01\x00\x00\xEA\x03\x03\xA2\xB9\xBB!\xBAQ(\xE7\xC2\xB3e^l\x87S\x9A\xE2\xC8\xCF\xAE\x81\x91P3\x86\x8BJs\x16L\xDE\xDF {\x14M5\x8Df\xEA\x0F\xB6\x90\x97\xF2\x91\xEF\xD8F\x84\xDBm|\xB5\xA6HF\xB1\xA8]\xA7\xE1\x88\x97~\x00&\xC0+\xC0/\xC0,\xC00\xCC\xA9\xCC\xA8\xC0\x09\xC0\x13\xC0" 400 166 "-" "-"`,
			level: "warn", at: time.Date(2026, 8, 26, 0, 55, 39, 0, time.UTC), event: "probe",
			attrs: map[string]string{"status": "400", "class": "4xx", "bytes": "166", "client": "198.235.24.89"},
		},
		{
			// An RDP client's cookie, the same way.
			text:  `3.129.187.38 - - [26/Aug/2026:06:46:03 +0000] "\x03\x00\x00+&\xE0\x00\x00\x00\x00\x00Cookie: mstshash=zgrab" 400 166 "-" "-"`,
			level: "warn", at: time.Date(2026, 8, 26, 6, 46, 3, 0, time.UTC), event: "probe",
			attrs: map[string]string{"status": "400", "class": "4xx", "bytes": "166", "client": "3.129.187.38"},
		},
		{
			text:  `66.132.224.82 - - [26/Aug/2026:12:49:44 +0000] "PRI * HTTP/2.0" 400 166 "-" "-"`,
			level: "warn", at: time.Date(2026, 8, 26, 12, 49, 44, 0, time.UTC), event: "probe",
			attrs: map[string]string{
				"method": "PRI", "path": "*", "proto": "HTTP/2.0", "status": "400", "class": "4xx",
				"bytes": "166", "client": "66.132.224.82",
			},
		},
		{
			// No request at all, which the access parser rightly refuses and
			// the lens still counts as the probe it is.
			text:  `18.218.118.203 - - [26/Aug/2026:03:21:59 +0000] "" 400 0 "-" "-"`,
			level: "warn", at: time.Date(2026, 8, 26, 3, 21, 59, 0, time.UTC), event: "probe",
			attrs: map[string]string{"status": "400", "class": "4xx", "bytes": "0", "client": "18.218.118.203"},
		},
		{
			// An address the generic stamp scan does not start on.
			text:  `::1 - - [27/Nov/2024:06:21:42 +0000] "GET /combined HTTP/1.1" 200 2 "-" "curl/8.7.1"`,
			level: "info", at: time.Date(2024, 11, 27, 6, 21, 42, 0, time.UTC), event: "request",
			attrs: map[string]string{
				"method": "GET", "path": "/combined", "proto": "HTTP/1.1", "status": "200", "class": "2xx",
				"bytes": "2", "client": "::1", "agent": "curl", "bot": "1",
			},
		},
		{
			text:  `127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326`,
			level: "info", at: time.Date(2000, 10, 10, 20, 55, 36, 0, time.UTC), event: "request",
			attrs: map[string]string{
				"method": "GET", "path": "/apache_pb.gif", "proto": "HTTP/1.0", "status": "200", "class": "2xx",
				"bytes": "2326", "client": "127.0.0.1",
			},
		},
		{
			text:  `{"level":"info","ts":1790507073.608195,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"100.84.53.82","remote_port":"57058","client_ip":"100.84.53.82","proto":"HTTP/2.0","method":"GET","host":"just-dashboard.com","uri":"/_next/static/media/Satoshi_Variable-s.p.2ta4m073o532p.woff2","headers":{"Origin":["https://just-dashboard.com"],"Sec-Ch-Ua-Platform":["\"macOS\""],"User-Agent":["Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"],"Priority":["u=1"],"Sec-Ch-Ua-Mobile":["?0"],"Sec-Fetch-Site":["same-origin"],"Accept-Encoding":["gzip, deflate, br, zstd"],"Accept-Language":["en-GB,en;q=0.9"],"Sec-Ch-Ua":["\"Google Chrome\";v=\"153\", \"Not_A Brand\";v=\"8\", \"Chromium\";v=\"153\""],"Sec-Fetch-Mode":["cors"],"Accept":["*/*"],"Sec-Fetch-Dest":["font"],"Referer":["https://just-dashboard.com/"]},"tls":{"resumed":false,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"just-dashboard.com","ech":false}},"bytes_read":0,"user_id":"","duration":0.013092502,"size":42588,"status":200,"resp_headers":{"Alt-Svc":["h3=\":443\"; ma=2592000"],"Accept-Ranges":["bytes"],"Etag":["W/\"a65c-1a0d8e83df0\""],"Content-Length":["42588"],"Content-Type":["font/woff2"],"Via":["1.1 Caddy"],"Date":["Sun, 27 Sep 2026 11:04:33 GMT"],"Cache-Control":["public, max-age=31536000, immutable"],"Last-Modified":["Fri, 25 Sep 2026 14:11:34 GMT"]}}`,
			level: "info", at: time.Unix(1790507073, 608195000), event: "request",
			attrs: map[string]string{
				"method": "GET", "path": "/_next/static/media/Satoshi_Variable-s.p.2ta4m073o532p.woff2",
				"proto": "HTTP/2.0", "status": "200", "class": "2xx", "bytes": "42588", "client": "100.84.53.82",
				"host": "just-dashboard.com", "referer": "https://just-dashboard.com/", "agent": "Chrome",
				"duration_ms": "13.093",
			},
		},
		{
			// Caddy writes a 404 at info; the access lens holds every
			// request log to the one status rule.
			text:  `{"level":"info","ts":1790507096.371178,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"130.12.180.117","remote_port":"22700","client_ip":"130.12.180.117","proto":"HTTP/2.0","method":"GET","host":"just-dashboard.com:443","uri":"/.env","headers":{"User-Agent":["Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko, Yokohama Institute of Information Security  https://www.iisec.ac.jp) Chrome/124.0.0.0 Safari/537.36"],"Accept-Encoding":["gzip"]},"tls":{"resumed":false,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"just-dashboard.com","ech":false}},"bytes_read":0,"user_id":"","duration":0.011442643,"size":2873,"status":404,"resp_headers":{"Cache-Control":["private, no-cache, no-store, max-age=0, must-revalidate"],"X-Nextjs-Stale-Time":["300"],"Via":["1.1 Caddy"],"Etag":["\"kj1v9kwumh9x9\""],"Vary":["rsc, next-router-state-tree, next-router-prefetch, next-router-segment-prefetch, Accept-Encoding"],"Alt-Svc":["h3=\":443\"; ma=2592000"],"Content-Type":["text/html; charset=utf-8"],"X-Nextjs-Cache":["HIT"],"X-Nextjs-Prerender":["1","1"],"Content-Encoding":["gzip"],"Date":["Sun, 27 Sep 2026 11:04:56 GMT"]}}`,
			level: "warn", at: time.Unix(1790507096, 371178000), event: "probe",
			attrs: map[string]string{
				"method": "GET", "path": "/.env", "proto": "HTTP/2.0", "status": "404", "class": "4xx",
				"bytes": "2873", "client": "130.12.180.117", "host": "just-dashboard.com:443", "agent": "Chrome",
				"duration_ms": "11.443",
			},
		},
		{
			// The runtime log shares a container's stdout with the access
			// entries; its lines are left alone rather than decoded twice.
			text:  `{"level":"info","ts":1790380131.536519,"msg":"config is unchanged"}`,
			level: "info", at: time.Unix(1790380131, 0), event: "",
		},
	}
}

// The lens runs on every line of a search that may span gigabytes of access
// log, which makes its cost per line the cost of the page.
func BenchmarkLensHTTPAccess(b *testing.B) {
	lines := []string{
		`93.123.109.228 - - [26/Aug/2026:00:08:38 +0000] "GET / HTTP/1.1" 200 14313 "-" "l9tcpid/v1.1.0"`,
		`186.236.254.56 - - [20/Aug/2026:03:28:45 +0000] "GET /wp-login.php HTTP/1.1" 404 196 "-" "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"`,
		`195.178.110.204 - - [26/Aug/2026:04:45:46 +0000] "POST /boaform/admin/formLogin HTTP/1.1" 404 134 "http://57.131.21.87:80/admin/login.asp" "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:77.0) Gecko/20100101 Firefox/77.0"`,
		`18.218.118.203 - - [26/Aug/2026:03:21:59 +0000] "" 400 0 "-" "-"`,
	}
	parsed := make([]Line, len(lines))
	for i, text := range lines {
		parsed[i] = ParseLine(text, "access.log")
	}
	r := lenses["http-access"].New()
	b.ReportAllocs()
	for b.Loop() {
		for _, l := range parsed {
			r.Read(&l)
		}
	}
}
