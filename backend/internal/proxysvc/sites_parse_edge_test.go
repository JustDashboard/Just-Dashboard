package proxysvc

import (
	"strings"
	"testing"
	"time"
)

func timeIn(days int) time.Time { return time.Now().Add(time.Duration(days) * 24 * time.Hour) }

// A listen on an odd port used to be read as TLS whenever the port began
// with 443 or the line mentioned "ssl" anywhere, so a plain-HTTP site on 4430
// came back as one that needed a certificate.
func TestListenIsTLSReadsTheAddressAndTheParameters(t *testing.T) {
	cases := map[string]bool{
		"80":                  false,
		"[::]:80":             false,
		"4430":                false,
		"127.0.0.1:8443":      false,
		"443":                 true,
		"[::]:443":            true,
		"0.0.0.0:443 ssl":     true,
		"8443 ssl http2":      true,
		"443 ssl":             true,
		"unix:/run/x.sock":    false,
		"*:443":               true,
		"8080 default_server": false,
	}
	for value, want := range cases {
		if got := listenIsTLS(value); got != want {
			t.Errorf("listenIsTLS(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestParseSiteSpecReadsOldStyleHTTP2(t *testing.T) {
	spec, _ := ParseSiteSpec("app", `
server {
    listen 443 ssl http2;
    server_name app.example.com;
    ssl_certificate /etc/ssl/app.pem;
    ssl_certificate_key /etc/ssl/app.key;
    location / { proxy_pass http://127.0.0.1:3000; }
}
`)
	if !spec.TLS || !spec.HTTP2 {
		t.Fatalf("tls=%v http2=%v, want both", spec.TLS, spec.HTTP2)
	}
}

// A hand-written file with no access_log directive is logging to nginx's
// default. Reading it back as "off" meant the first save from the form wrote
// `access_log off;` into a site that had been logging all along.
func TestParseSiteSpecKeepsLoggingOnForHandWrittenFiles(t *testing.T) {
	handWritten := `
server {
    listen 80;
    server_name legacy.example.com;
    location / { proxy_pass http://127.0.0.1:3000; }
}
`
	spec, managed := ParseSiteSpec("legacy", handWritten)
	if managed {
		t.Fatal("no marker, so not managed")
	}
	if !spec.AccessLog {
		t.Fatal("a hand-written file without access_log must round-trip as logging on")
	}
	rendered, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "access_log off") {
		t.Fatal("the first save turned logging off")
	}

	// The form's own choice is still honoured on the files it wrote.
	off := proxySpec()
	off.AccessLog = false
	content, err := RenderNginx(off)
	if err != nil {
		t.Fatal(err)
	}
	back, managed := ParseSiteSpec("app", content)
	if !managed || back.AccessLog {
		t.Fatalf("managed=%v accessLog=%v, want managed and off", managed, back.AccessLog)
	}
}

// Hand-written files often put a whole location on one line. The line reader
// used to take the opener and drop everything after the brace, so the site
// read back with no upstream at all.
func TestSplitInlineStatements(t *testing.T) {
	got := splitInline(`location / { proxy_pass http://127.0.0.1:3000; proxy_set_header Host $host; }`)
	want := []string{"location / {", "proxy_pass http://127.0.0.1:3000;", "proxy_set_header Host $host;", "}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	// Semicolons inside quotes are the value, not a separator.
	got = splitInline(`location / { add_header Content-Security-Policy "default-src 'self'; img-src *"; }`)
	if len(got) != 3 || got[1] != `add_header Content-Security-Policy "default-src 'self'; img-src *";` {
		t.Fatalf("quoted semicolon was split: %q", got)
	}
	// A line with nothing after its brace, and a line without one, come back as they were.
	if got := splitInline("location / {"); len(got) != 1 || got[0] != "location / {" {
		t.Fatalf("plain opener changed: %q", got)
	}
	if got := splitInline(`return 301 "{not a block}";`); len(got) != 1 {
		t.Fatalf("a brace inside quotes is not a block: %q", got)
	}
}
