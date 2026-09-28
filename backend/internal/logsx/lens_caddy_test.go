package logsx

import (
	"testing"
	"time"
)

// The ingress and dashboard-proxy lines are this project's public host's
// Caddy 2 containers. The timeouts, the lookup failure, the refused reload,
// the renewal and the failing issuer were provoked from the same image's
// binary (v2.11.4) on that host — a silent upstream, an unroutable one, a
// name that does not resolve, an admin POST of a config naming no such
// module, a ninety-second internal certificate, an ACME directory on a
// closed port — with the scratch prefix of their paths shortened to
// /srv/caddy.
func TestLensCaddy(t *testing.T) {
	webLensCheck(t, "caddy", []webLensLine{
		{
			text:  `{"level":"warn","ts":1790380130.488044,"msg":"Caddyfile input is not formatted; run 'caddy fmt --overwrite' to fix inconsistencies","adapter":"caddyfile","file":"/etc/caddy/Caddyfile","line":2}`,
			level: "warn", at: time.Unix(1790380130, 488044000), event: "config",
		},
		{
			text:  `{"level":"info","ts":1790380130.489889,"msg":"serving initial configuration"}`,
			level: "info", at: time.Unix(1790380130, 489889000), event: "startup",
		},
		{
			// The admin API's own request log is the loudest thing in the
			// file and names nothing — but its stamp keeps its fraction.
			text:  `{"level":"info","ts":1790380130.739639,"logger":"admin.api","msg":"received request","method":"GET","host":"127.0.0.1:2019","uri":"/config/","remote_ip":"127.0.0.1","remote_port":"36208","headers":{"User-Agent":["Wget"],"Accept":["*/*"],"Connection":["close"]}}`,
			level: "info", at: time.Unix(1790380130, 739639000),
		},
		{
			text:  `{"level":"info","ts":1790380131.5365279,"logger":"admin.api","msg":"load complete"}`,
			level: "info", at: time.Unix(1790380131, 536527900), event: "config",
			attrs: map[string]string{"logger": "admin.api"},
		},
		{
			text:  `{"level":"error","ts":1790515627.2783782,"logger":"admin.api","msg":"request error","error":"loading config: loading new config: loading http app module: provision http: server x: setting up route handlers: route 0: loading handler modules: position 0: loading module 'nope': unknown module: http.handlers.nope","status_code":400}`,
			level: "error", at: time.Unix(1790515627, 278378200), event: "config",
			attrs: map[string]string{
				"logger": "admin.api",
				"error":  "loading config: loading new config: loading http app module: provision http: server x: setting up route handlers: route 0: loading handler modules: position 0: loading module 'nope': unknown module: http.handlers.nope",
			},
		},
		{
			text:  `Error: adapting config using caddyfile: parsing caddyfile tokens for 'tls': unknown subdirective: lifetime, at Caddyfile:12`,
			level: "error", event: "config",
		},
		{
			// Caddy writes the access log at info below 500, and the lens
			// keeps Caddy's level.
			text:  `{"level":"info","ts":1790381608.0767596,"logger":"http.log.access","msg":"handled request","request":{"remote_ip":"45.148.10.171","remote_port":"43004","client_ip":"45.148.10.171","proto":"HTTP/1.1","method":"GET","host":"57.131.21.87","uri":"/.env","headers":{"User-Agent":["Mozilla/5.0 (Linux; U; Android 1.5; en-us; htc_bahamas Build/CRB17) AppleWebKit/528.5  (KHTML, like Gecko) Version/3.1.2 Mobile Safari/525.20.1"],"Accept-Charset":["utf-8"],"Accept-Encoding":["gzip"],"Connection":["close"]}},"bytes_read":0,"user_id":"","duration":0.000128486,"size":0,"status":308,"resp_headers":{"Server":["Caddy"],"Connection":["close"],"Location":["https://57.131.21.87/.env"],"Content-Type":[]}}`,
			level: "info", at: time.Unix(1790381608, 76759600), event: "request",
			attrs: map[string]string{
				"method": "GET", "path": "/.env", "proto": "HTTP/1.1", "status": "308", "class": "3xx", "bytes": "0",
				"client": "45.148.10.171", "host": "57.131.21.87", "agent": "Safari", "duration_ms": "0.128",
				"logger": "http.log.access",
			},
		},
		{
			text:  `{"level":"info","ts":1790507096.371178,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"130.12.180.117","remote_port":"22700","client_ip":"130.12.180.117","proto":"HTTP/2.0","method":"GET","host":"just-dashboard.com:443","uri":"/.env","headers":{"User-Agent":["Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko, Yokohama Institute of Information Security  https://www.iisec.ac.jp) Chrome/124.0.0.0 Safari/537.36"],"Accept-Encoding":["gzip"]},"tls":{"resumed":false,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"just-dashboard.com","ech":false}},"bytes_read":0,"user_id":"","duration":0.011442643,"size":2873,"status":404,"resp_headers":{"Cache-Control":["private, no-cache, no-store, max-age=0, must-revalidate"],"X-Nextjs-Stale-Time":["300"],"Via":["1.1 Caddy"],"Etag":["\"kj1v9kwumh9x9\""],"Vary":["rsc, next-router-state-tree, next-router-prefetch, next-router-segment-prefetch, Accept-Encoding"],"Alt-Svc":["h3=\":443\"; ma=2592000"],"Content-Type":["text/html; charset=utf-8"],"X-Nextjs-Cache":["HIT"],"X-Nextjs-Prerender":["1","1"],"Content-Encoding":["gzip"],"Date":["Sun, 27 Sep 2026 11:04:56 GMT"]}}`,
			level: "info", at: time.Unix(1790507096, 371178000), event: "probe",
			attrs: map[string]string{
				"method": "GET", "path": "/.env", "proto": "HTTP/2.0", "status": "404", "class": "4xx",
				"bytes": "2873", "client": "130.12.180.117", "host": "just-dashboard.com:443", "agent": "Chrome",
				"duration_ms": "11.443", "logger": "http.log.access.log0",
			},
		},
		{
			text:  `{"level":"info","ts":1790507165.0249968,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"64.227.70.2","remote_port":"49530","client_ip":"64.227.70.2","proto":"HTTP/1.1","method":"OPTIONS","host":"just-dashboard.com","uri":"/","headers":{"User-Agent":["Mozilla/5.0 (l9scan/2.0.7383e21323e2133313e27353; +https://leakix.net)"],"Accept-Encoding":["gzip"],"Connection":["close"]},"tls":{"resumed":true,"version":772,"cipher_suite":4865,"proto":"","server_name":"just-dashboard.com","ech":false}},"bytes_read":0,"user_id":"","duration":0.015516274,"size":18,"status":405,"resp_headers":{"Vary":["rsc, next-router-state-tree, next-router-prefetch, next-router-segment-prefetch"],"Allow":["GET","HEAD"],"Date":["Sun, 27 Sep 2026 11:06:05 GMT"],"Via":["1.1 Caddy"],"Alt-Svc":["h3=\":443\"; ma=2592000"]}}`,
			level: "info", at: time.Unix(1790507165, 24996800), event: "client_error",
			attrs: map[string]string{
				"method": "OPTIONS", "path": "/", "proto": "HTTP/1.1", "status": "405", "class": "4xx", "bytes": "18",
				"client": "64.227.70.2", "host": "just-dashboard.com", "agent": "Mozilla", "bot": "1",
				"duration_ms": "15.516", "logger": "http.log.access.log0",
			},
		},
		{
			// A request abandoned before any response has status 0, which
			// the access parser refuses: a plain line, with its stamp.
			text:  `{"level":"info","ts":1790510230.109475,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"100.84.53.82","remote_port":"57338","client_ip":"100.84.53.82","proto":"HTTP/2.0","method":"GET","host":"just-dashboard.com","uri":"/media/docker.webm","headers":{"Accept":["*/*"],"Sec-Ch-Ua-Platform":["\"macOS\""],"Sec-Ch-Ua-Mobile":["?0"],"Referer":["https://just-dashboard.com/"],"Priority":["i"],"User-Agent":["Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"],"Sec-Ch-Ua":["\"Google Chrome\";v=\"153\", \"Not_A Brand\";v=\"8\", \"Chromium\";v=\"153\""],"Sec-Fetch-Dest":["video"],"Range":["bytes=134829-1572282"],"If-Range":["W/\"17fdbb-1a0d8e75f48\""],"Sec-Fetch-Mode":["no-cors"],"Sec-Fetch-Storage-Access":["none"],"Accept-Language":["en-GB,en;q=0.9"],"Accept-Encoding":["identity;q=1, *;q=0"],"Sec-Fetch-Site":["same-origin"]},"tls":{"resumed":true,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"just-dashboard.com","ech":false}},"bytes_read":0,"user_id":"","duration":0.002334948,"size":0,"status":0,"resp_headers":{"Server":["Caddy"],"Alt-Svc":["h3=\":443\"; ma=2592000"]}}`,
			level: "info", at: time.Unix(1790510230, 109475000),
		},
		{
			text:  `{"level":"error","ts":1790515619.0385482,"logger":"http.log.access","msg":"handled request","request":{"remote_ip":"127.0.0.1","remote_port":"43862","client_ip":"127.0.0.1","proto":"HTTP/1.1","method":"GET","host":"127.0.0.1:18281","uri":"/refused","headers":{"User-Agent":["curl/8.12.1"],"Accept":["*/*"]}},"bytes_read":0,"user_id":"","duration":0.000656591,"size":0,"status":502,"resp_headers":{"Server":["Caddy"]}}`,
			level: "error", at: time.Unix(1790515619, 38548200), event: "server_error",
			attrs: map[string]string{
				"method": "GET", "path": "/refused", "proto": "HTTP/1.1", "status": "502", "class": "5xx",
				"bytes": "0", "client": "127.0.0.1", "host": "127.0.0.1:18281", "agent": "curl", "bot": "1",
				"duration_ms": "0.657", "logger": "http.log.access",
			},
		},
		{
			// The same request's error line: this is where the 502 says why.
			text:  `{"level":"error","ts":1790506849.660717,"logger":"http.log.error","msg":"dial tcp 127.0.0.1:42996: connect: connection refused","request":{"remote_ip":"100.84.53.82","remote_port":"57025","client_ip":"100.84.53.82","proto":"HTTP/2.0","method":"GET","host":"vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443","uri":"/api/v1/dashboard/config","headers":{"Sec-Ch-Ua":["\"Google Chrome\";v=\"153\", \"Not_A Brand\";v=\"8\", \"Chromium\";v=\"153\""],"Sec-Ch-Ua-Mobile":["?0"],"Sec-Fetch-Site":["same-origin"],"Sec-Fetch-Mode":["cors"],"Accept":["*/*"],"Sec-Fetch-Dest":["empty"],"Cookie":["REDACTED"],"Priority":["u=1, i"],"Sec-Ch-Ua-Platform":["\"macOS\""],"Accept-Language":["en-GB,en;q=0.9"],"User-Agent":["Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"],"Accept-Encoding":["gzip, deflate, br, zstd"]},"tls":{"resumed":false,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"vps-07749119-vps-ovh-net.tailed39ba.ts.net","ech":false}},"duration":0.001818104,"status":502,"err_id":"6pue15j76","err_trace":"reverseproxy.statusError (reverseproxy.go:1626)"}`,
			level: "error", at: time.Unix(1790506849, 660717000), event: "upstream_refused",
			attrs: map[string]string{
				"logger": "http.log.error", "error": "dial tcp 127.0.0.1:42996: connect: connection refused",
				"upstream": "127.0.0.1:42996", "method": "GET", "path": "/api/v1/dashboard/config",
				"proto": "HTTP/2.0", "host": "vps-07749119-vps-ovh-net.tailed39ba.ts.net:8443",
				"client": "100.84.53.82", "status": "502", "class": "5xx", "duration_ms": "1.818",
			},
		},
		{
			text:  `{"level":"error","ts":1790515620.0531096,"logger":"http.log.error","msg":"net/http: timeout awaiting response headers","request":{"remote_ip":"127.0.0.1","remote_port":"43876","client_ip":"127.0.0.1","proto":"HTTP/1.1","method":"GET","host":"127.0.0.1:18281","uri":"/slow","headers":{"Accept":["*/*"],"User-Agent":["curl/8.12.1"]}},"duration":1.003497016,"status":504,"err_id":"kir427yqg","err_trace":"reverseproxy.statusError (reverseproxy.go:1626)"}`,
			level: "error", at: time.Unix(1790515620, 53109600), event: "upstream_timeout",
			attrs: map[string]string{
				"logger": "http.log.error", "error": "net/http: timeout awaiting response headers", "method": "GET",
				"path": "/slow", "proto": "HTTP/1.1", "host": "127.0.0.1:18281", "client": "127.0.0.1",
				"status": "504", "class": "5xx", "duration_ms": "1003.497",
			},
		},
		{
			// A dial that timed out is answered 502, not 504: the event is
			// read from the error, not the status.
			text:  `{"level":"error","ts":1790515621.0829978,"logger":"http.log.error","msg":"dial tcp 10.255.255.1:80: i/o timeout","request":{"remote_ip":"127.0.0.1","remote_port":"43900","client_ip":"127.0.0.1","proto":"HTTP/1.1","method":"GET","host":"127.0.0.1:18281","uri":"/blackhole","headers":{"Accept":["*/*"],"User-Agent":["curl/8.12.1"]}},"duration":1.001229616,"status":502,"err_id":"qz877kpud","err_trace":"reverseproxy.statusError (reverseproxy.go:1626)"}`,
			level: "error", at: time.Unix(1790515621, 82997800), event: "upstream_timeout",
			attrs: map[string]string{
				"logger": "http.log.error", "error": "dial tcp 10.255.255.1:80: i/o timeout",
				"upstream": "10.255.255.1:80", "method": "GET", "path": "/blackhole", "proto": "HTTP/1.1",
				"host": "127.0.0.1:18281", "client": "127.0.0.1", "status": "502", "class": "5xx",
				"duration_ms": "1001.23",
			},
		},
		{
			text:  `{"level":"error","ts":1790515620.0670104,"logger":"http.log.error","msg":"EOF","request":{"remote_ip":"127.0.0.1","remote_port":"43890","client_ip":"127.0.0.1","proto":"HTTP/1.1","method":"GET","host":"127.0.0.1:18281","uri":"/closer","headers":{"User-Agent":["curl/8.12.1"],"Accept":["*/*"]}},"duration":0.000970282,"status":502,"err_id":"gn8s1gez7","err_trace":"reverseproxy.statusError (reverseproxy.go:1626)"}`,
			level: "error", at: time.Unix(1790515620, 67010400), event: "error",
			attrs: map[string]string{
				"logger": "http.log.error", "error": "EOF", "method": "GET", "path": "/closer", "proto": "HTTP/1.1",
				"host": "127.0.0.1:18281", "client": "127.0.0.1", "status": "502", "class": "5xx",
				"duration_ms": "0.97",
			},
		},
		{
			// A backend that does not resolve is still named.
			text:  `{"level":"error","ts":1790515621.11282,"logger":"http.log.error","msg":"dial tcp: lookup nosuchhost.invalid on 127.0.0.53:53: no such host","request":{"remote_ip":"127.0.0.1","remote_port":"43916","client_ip":"127.0.0.1","proto":"HTTP/1.1","method":"GET","host":"127.0.0.1:18281","uri":"/nohost","headers":{"User-Agent":["curl/8.12.1"],"Accept":["*/*"]}},"duration":0.018425547,"status":502,"err_id":"295dhqp06","err_trace":"reverseproxy.statusError (reverseproxy.go:1626)"}`,
			level: "error", at: time.Unix(1790515621, 112820000), event: "error",
			attrs: map[string]string{
				"logger": "http.log.error", "error": "dial tcp: lookup nosuchhost.invalid on 127.0.0.53:53: no such host",
				"upstream": "nosuchhost.invalid", "method": "GET", "path": "/nohost", "proto": "HTTP/1.1",
				"host": "127.0.0.1:18281", "client": "127.0.0.1", "status": "502", "class": "5xx",
				"duration_ms": "18.426",
			},
		},
		{
			// A client that left mid-stream: the upstream is named, nothing
			// failed that the operator could fix.
			text:  `{"level":"warn","ts":1790380156.406836,"logger":"http.handlers.reverse_proxy","msg":"aborting with incomplete response","upstream":"10.0.4.3:3000","duration":0.049515938,"request":{"remote_ip":"57.131.21.87","remote_port":"45466","client_ip":"57.131.21.87","proto":"HTTP/2.0","method":"GET","host":"lampino-081b35.57-131-21-87.sslip.io","uri":"/ro","headers":{"Referer":["https://lampino-081b35.57-131-21-87.sslip.io/"],"Accept-Encoding":["gzip"],"User-Agent":["Go-http-client/2.0"],"X-Forwarded-For":["57.131.21.87"],"X-Forwarded-Proto":["https"],"X-Forwarded-Host":["lampino-081b35.57-131-21-87.sslip.io"],"Via":["2.0 Caddy"],"Accept":["text/html"]},"tls":{"resumed":false,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"lampino-081b35.57-131-21-87.sslip.io","ech":false}},"error":"reading: context canceled"}`,
			level: "warn", at: time.Unix(1790380156, 406836000),
			attrs: map[string]string{
				"logger": "http.handlers.reverse_proxy", "error": "reading: context canceled",
				"upstream": "10.0.4.3:3000", "method": "GET", "path": "/ro", "proto": "HTTP/2.0",
				"host": "lampino-081b35.57-131-21-87.sslip.io", "client": "57.131.21.87", "duration_ms": "49.516",
			},
		},
		{
			text:  `{"level":"info","ts":1790507072.4859831,"logger":"tls.obtain","msg":"certificate obtained successfully","identifier":"just-dashboard.com","issuer":"acme-v02.api.letsencrypt.org-directory"}`,
			level: "info", at: time.Unix(1790507072, 485983100), event: "cert_obtained",
			attrs: map[string]string{"logger": "tls.obtain", "domain": "just-dashboard.com"},
		},
		{
			text:  `{"level":"info","ts":1790515948.2619846,"logger":"tls.renew","msg":"certificate renewed successfully","identifier":"renew.localhost","issuer":"local"}`,
			level: "info", at: time.Unix(1790515948, 261984600), event: "cert_renewed",
			attrs: map[string]string{"logger": "tls.renew", "domain": "renew.localhost"},
		},
		{
			// One failed attempt writes this per issuer and then one "will
			// retry"; only the retry is the failure, or each would count twice.
			text:  `{"level":"error","ts":1790515616.585479,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"fail.example.test","issuer":"127.0.0.1:18901-directory","error":"registering account [] with server: provisioning client: performing request: Get \"http://127.0.0.1:18901/directory\": dial tcp 127.0.0.1:18901: connect: connection refused"}`,
			level: "error", at: time.Unix(1790515616, 585479000),
		},
		{
			text:  `{"level":"error","ts":1790515616.5855193,"logger":"tls.obtain","msg":"will retry","error":"[fail.example.test] Obtain: registering account [] with server: provisioning client: performing request: Get \"http://127.0.0.1:18901/directory\": dial tcp 127.0.0.1:18901: connect: connection refused","attempt":1,"retrying_in":60,"elapsed":0.504136289,"max_duration":2592000}`,
			level: "error", at: time.Unix(1790515616, 585519300), event: "cert_failed",
			attrs: map[string]string{
				"logger": "tls.obtain", "domain": "fail.example.test",
				"error": `[fail.example.test] Obtain: registering account [] with server: provisioning client: performing request: Get "http://127.0.0.1:18901/directory": dial tcp 127.0.0.1:18901: connect: connection refused`,
			},
		},
		{
			// A renewal's retry names no domain at all.
			text:  `{"level":"error","ts":1790515636.1017923,"logger":"tls.renew","msg":"will retry","error":"open /srv/caddy/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/renew.localhost/renew.localhost.key: no such file or directory","attempt":1,"retrying_in":60,"elapsed":0.000052452,"max_duration":2592000}`,
			level: "error", at: time.Unix(1790515636, 101792300), event: "cert_failed",
			attrs: map[string]string{
				"logger": "tls.renew",
				"error":  "open /srv/caddy/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/renew.localhost/renew.localhost.key: no such file or directory",
			},
		},
		{
			// A warning with no logger that is not the adapter's is not about
			// the configuration.
			text:  `{"level":"warn","ts":1790515890.5029423,"msg":"exiting; byeee!! 👋","signal":"SIGINT"}`,
			level: "warn", at: time.Unix(1790515890, 502942300),
		},
	})
}

func BenchmarkLensCaddy(b *testing.B) {
	lines := []string{
		`{"level":"info","ts":1790381608.0767596,"logger":"http.log.access","msg":"handled request","request":{"remote_ip":"45.148.10.171","remote_port":"43004","client_ip":"45.148.10.171","proto":"HTTP/1.1","method":"GET","host":"57.131.21.87","uri":"/.env","headers":{"User-Agent":["Mozilla/5.0 (Linux; U; Android 1.5; en-us; htc_bahamas Build/CRB17) AppleWebKit/528.5  (KHTML, like Gecko) Version/3.1.2 Mobile Safari/525.20.1"],"Accept-Charset":["utf-8"],"Accept-Encoding":["gzip"],"Connection":["close"]}},"bytes_read":0,"user_id":"","duration":0.000128486,"size":0,"status":308,"resp_headers":{"Server":["Caddy"],"Connection":["close"],"Location":["https://57.131.21.87/.env"],"Content-Type":[]}}`,
		`{"level":"info","ts":1790380130.739639,"logger":"admin.api","msg":"received request","method":"GET","host":"127.0.0.1:2019","uri":"/config/","remote_ip":"127.0.0.1","remote_port":"36208","headers":{"User-Agent":["Wget"],"Accept":["*/*"],"Connection":["close"]}}`,
		`{"level":"error","ts":1790515621.0829978,"logger":"http.log.error","msg":"dial tcp 10.255.255.1:80: i/o timeout","request":{"remote_ip":"127.0.0.1","remote_port":"43900","client_ip":"127.0.0.1","proto":"HTTP/1.1","method":"GET","host":"127.0.0.1:18281","uri":"/blackhole","headers":{"Accept":["*/*"],"User-Agent":["curl/8.12.1"]}},"duration":1.001229616,"status":502,"err_id":"qz877kpud","err_trace":"reverseproxy.statusError (reverseproxy.go:1626)"}`,
		`{"level":"info","ts":1790507072.4859831,"logger":"tls.obtain","msg":"certificate obtained successfully","identifier":"just-dashboard.com","issuer":"acme-v02.api.letsencrypt.org-directory"}`,
	}
	parsed := make([]Line, len(lines))
	for i, text := range lines {
		parsed[i] = ParseLine(text, "")
	}
	r := lenses["caddy"].New()
	b.ReportAllocs()
	for b.Loop() {
		for _, l := range parsed {
			r.Read(&l)
		}
	}
}
