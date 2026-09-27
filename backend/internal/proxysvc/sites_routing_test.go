package proxysvc

import (
	"reflect"
	"strings"
	"testing"
)

// roundTrip renders a spec, reads the file back and renders that again. The
// two files have to be identical, or opening a site and saving it changes it.
func roundTrip(t *testing.T, spec *SiteSpec) (*SiteSpec, string) {
	t.Helper()
	first, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := ParseSiteSpec(spec.Name, first)
	second, err := RenderNginx(parsed)
	if err != nil {
		t.Fatalf("the parsed spec no longer renders: %v", err)
	}
	if first != second {
		t.Fatalf("a round trip changed the file:\n--- first\n%s\n--- second\n%s", first, second)
	}
	return parsed, first
}

// The upstream's path is how nginx is told to swap the location's prefix,
// so it is written as typed: trimming its slash sent /page to /app/ as
// /apppage, and made /api/ to http://x/ unable to strip /api.
func TestUpstreamPathsAreWrittenAsTyped(t *testing.T) {
	spec := proxySpec()
	spec.Upstream = "http://127.0.0.1:3000/app/"
	spec.Locations = []SiteLocation{{Path: "/api/", Upstream: "http://127.0.0.1:4000/"}}
	parsed, out := roundTrip(t, spec)
	for _, want := range []string{
		"proxy_pass http://127.0.0.1:3000/app/;",
		"location /api/ {\n        proxy_pass http://127.0.0.1:4000/;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	if parsed.Upstream != spec.Upstream || parsed.Locations[0].Upstream != "http://127.0.0.1:4000/" {
		t.Fatalf("read back %q and %q", parsed.Upstream, parsed.Locations[0].Upstream)
	}
}

// An upstream pasted with a slash, on /, asks nginx to swap / for / — which
// changes nothing but costs the request its encoding: nginx forwards the
// path as the client sent it only to an upstream without a path. So a path
// that is the location's own is left off, and every other one is kept.
func TestAnUpstreamPathThatIsTheLocationsOwnIsLeftOff(t *testing.T) {
	spec := proxySpec()
	spec.Upstream = "http://127.0.0.1:3000/"
	spec.Locations = []SiteLocation{
		{Path: "/pkg/", Upstream: "http://127.0.0.1:4000/pkg/"},
		{Path: "/api", Upstream: "http://127.0.0.1:5000/api"},
		{Path: "/sock/", Upstream: "unix:/run/app.sock:/sock/"},
		{Path: "/v2/", Upstream: "http://127.0.0.1:6000/v1/"},
	}
	parsed, out := roundTrip(t, spec)
	for _, want := range []string{
		"location / {\n        proxy_pass http://127.0.0.1:3000;",
		"location /pkg/ {\n        proxy_pass http://127.0.0.1:4000;",
		"location /api {\n        proxy_pass http://127.0.0.1:5000;",
		"location /sock/ {\n        proxy_pass http://unix:/run/app.sock;",
		"location /v2/ {\n        proxy_pass http://127.0.0.1:6000/v1/;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	if parsed.Upstream != "http://127.0.0.1:3000" {
		t.Fatalf("read back as %q", parsed.Upstream)
	}
}

func TestSplitUpstreamIsWhereNginxReadsThePath(t *testing.T) {
	for _, tc := range []struct{ upstream, address, uri string }{
		{"http://127.0.0.1:3000", "http://127.0.0.1:3000", ""},
		{"http://127.0.0.1:3000/", "http://127.0.0.1:3000", "/"},
		{"https://app.internal/v1/", "https://app.internal", "/v1/"},
		{"unix:/run/app.sock", "unix:/run/app.sock", ""},
		{"unix:/run/app.sock:/", "unix:/run/app.sock", "/"},
		{"unix:/run/app.sock:/app/", "unix:/run/app.sock", "/app/"},
	} {
		address, uri := splitUpstream(tc.upstream)
		if address != tc.address || uri != tc.uri {
			t.Errorf("%s split as %q %q, want %q %q", tc.upstream, address, uri, tc.address, tc.uri)
		}
	}
}

func TestUpstreamDecodeWarning(t *testing.T) {
	for _, tc := range []struct{ path, upstream, want string }{
		{"/", "http://127.0.0.1:3000", ""},
		{"/", "http://127.0.0.1:3000/", ""},
		{"/api/", "http://127.0.0.1:4000/api/", ""},
		{"/", "unix:/run/app.sock", ""},
		{"/", "http://127.0.0.1:3000/app/", "/a%2Fb reaches the application as /app/a/b:"},
		{"/api/", "http://127.0.0.1:4000/", "/api/a%2Fb reaches the application as /a/b:"},
		{"/api", "http://127.0.0.1:4000/v1", "/api/a%2Fb reaches the application as /v1/a/b:"},
		{"/sock/", "unix:/run/app.sock:/", "/sock/a%2Fb reaches the application as /a/b:"},
	} {
		got := upstreamDecodeWarning(tc.path, tc.upstream)
		if (tc.want == "") != (got == "") || !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s → %s: got %q, want %q", tc.path, tc.upstream, got, tc.want)
		}
	}
	spec := proxySpec()
	spec.Upstream = "http://127.0.0.1:3000/"
	spec.Locations = []SiteLocation{{Path: "/api/", Upstream: "http://127.0.0.1:4000/"}}
	warnings := SpecWarnings(spec)
	if !containsSubstring(warnings, "/api/a%2Fb reaches the application as /a/b") {
		t.Fatalf("the decoding is not among the warnings: %v", warnings)
	}
	// Only /api/: the slash on / is left off, so there is nothing to say.
	if n := strings.Count(strings.Join(warnings, "\n"), "%2F kept"); n != 1 {
		t.Fatalf("%d decoding warnings, want 1: %v", n, warnings)
	}
}

// nginx refuses `proxy_pass unix:/run/app.sock` with "invalid URL prefix";
// the form offered unix: and every such save failed its test.
func TestAUnixSocketUpstreamIsSpelledAsNginxWantsIt(t *testing.T) {
	spec := proxySpec()
	spec.Upstream = "unix:/run/app.sock"
	parsed, out := roundTrip(t, spec)
	if !strings.Contains(out, "proxy_pass http://unix:/run/app.sock;") {
		t.Fatalf("socket not written as http://unix:\n%s", out)
	}
	if parsed.Upstream != "unix:/run/app.sock" {
		t.Fatalf("read back as %q", parsed.Upstream)
	}
}

// A folder is served at its path. `root` appended the path to it, so
// /assets with /var/www/assets looked for /var/www/assets/assets/app.css.
func TestAFolderIsServedAtItsPath(t *testing.T) {
	spec := proxySpec()
	spec.Locations = []SiteLocation{{Path: "/assets", Root: "/var/www/assets"}}
	parsed, out := roundTrip(t, spec)
	// Both end in a slash, so /assets-private is never read from
	// /var/www/assets-private through this block.
	want := "    location /assets/ {\n        alias /var/www/assets/;\n        try_files $uri $uri/ =404;\n    }"
	if !strings.Contains(out, want) {
		t.Fatalf("missing\n%s\nfrom:\n%s", want, out)
	}
	if strings.Contains(out, "root /var/www/assets") {
		t.Fatal("a folder location still uses root")
	}
	if got := parsed.Locations; !reflect.DeepEqual(got, []SiteLocation{{Path: "/assets/", Root: "/var/www/assets"}}) {
		t.Fatalf("read back as %+v", got)
	}
}

// A location written with root keeps it: nginx appends the path to that
// folder, and rewriting it as the folder itself would move every file.
func TestALocationWrittenWithRootKeepsIt(t *testing.T) {
	content := `server {
    listen 80;
    server_name app.example.com;
    location /static {
        root /srv/site;
    }
    location /media/ {
        alias /srv/uploads/;
    }
    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}
`
	parsed, _ := ParseSiteSpec("app", content)
	want := []SiteLocation{
		{Path: "/static", Root: "/srv/site", RootMode: "root"},
		{Path: "/media/", Root: "/srv/uploads"},
	}
	if !reflect.DeepEqual(parsed.Locations, want) {
		t.Fatalf("locations = %+v, want %+v", parsed.Locations, want)
	}
	out, err := RenderNginx(parsed)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"location /static {\n        root /srv/site;",
		"location /media/ {\n        alias /srv/uploads/;",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q from:\n%s", line, out)
		}
	}
	roundTrip(t, parsed)
}

func TestTwoFoldersForOnePathAreRefused(t *testing.T) {
	spec := proxySpec()
	spec.Locations = []SiteLocation{
		{Path: "/assets", Root: "/var/www/a"},
		{Path: "/assets/", Root: "/var/www/b"},
	}
	if err := ValidateSpec(spec); err == nil || !strings.Contains(err.Error(), "/assets/") {
		t.Fatalf("two blocks for /assets/ were accepted: %v", err)
	}
	spec.Locations[1].RootMode = "alias"
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("an unknown root mode was accepted")
	}
}

// Debian's nginx.conf has `gzip on;` in http, so a site that says nothing
// compresses: switching the form's compression off has to say off.
func TestCompressionOffIsWrittenAndReadBack(t *testing.T) {
	spec := proxySpec()
	spec.Gzip = false
	parsed, out := roundTrip(t, spec)
	if !strings.Contains(out, "    gzip off;\n") {
		t.Fatalf("compression off not written:\n%s", out)
	}
	if parsed.Gzip {
		t.Fatal("gzip off read back as on")
	}
	spec.Gzip = true
	if parsed, _ := roundTrip(t, spec); !parsed.Gzip {
		t.Fatal("gzip on read back as off")
	}
}

// A file with no gzip line inherits the http block's. A hand-written one
// reads back as on, the Debian default; a managed one predates `gzip off;`
// and was written with the switch off.
func TestAFileWithoutAGzipLine(t *testing.T) {
	hand := "server {\n    listen 80;\n    server_name app.example.com;\n    location / {\n        proxy_pass http://127.0.0.1:3000;\n    }\n}\n"
	if parsed, _ := ParseSiteSpec("app", hand); !parsed.Gzip {
		t.Error("a hand-written file without gzip read back as off")
	}
	managed := managedMarker + "\n" + hand
	if parsed, _ := ParseSiteSpec("app", managed); parsed.Gzip {
		t.Error("a managed file without gzip read back as on")
	}
}

// nginx warns that a name listed twice is "conflicting" and ignored, which
// reads as another site owning it; the form says so instead.
func TestADomainListedTwiceIsRefused(t *testing.T) {
	spec := proxySpec()
	spec.Domains = []string{"app.example.com", "App.Example.com"}
	if err := ValidateSpec(spec); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("a domain listed twice was accepted: %v", err)
	}
}

func TestUpstreamSlashWarning(t *testing.T) {
	for _, tc := range []struct{ path, upstream, want string }{
		{"/", "http://127.0.0.1:3000", ""},
		{"/", "http://127.0.0.1:3000/", ""},
		{"/", "http://127.0.0.1:3000/app/", ""},
		{"/api/", "http://127.0.0.1:4000/", ""},
		{"/api", "http://127.0.0.1:4000/v1", ""},
		{"/api", "http://127.0.0.1:4000", ""},
		{"/", "unix:/run/app.sock:/app", ""},
		{"/", "http://127.0.0.1:3000/app", "/page reaches the application as /apppage."},
		{"/api", "http://127.0.0.1:4000/", "/api/page reaches the application as //page."},
	} {
		got := upstreamSlashWarning(tc.path, tc.upstream)
		if (tc.want == "") != (got == "") || !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s → %s: got %q, want %q", tc.path, tc.upstream, got, tc.want)
		}
	}
	spec := proxySpec()
	spec.Locations = []SiteLocation{{Path: "/api", Upstream: "http://127.0.0.1:4000/"}}
	if !containsSubstring(SpecWarnings(spec), "/api/page reaches the application as //page") {
		t.Fatalf("the mismatch is not among the warnings: %v", SpecWarnings(spec))
	}
}

// The comment the renderer writes about WebSockets gave a reason that was
// false: a site file's top level is http context, and a map there passes.
func TestTheWebSocketCommentGivesTheRealReason(t *testing.T) {
	out, _ := RenderNginx(proxySpec())
	if strings.Contains(out, "cannot reach there") {
		t.Fatalf("the generated file still says a site file cannot reach the http block:\n%s", out)
	}
	if !strings.Contains(out, "the last file to define it silently decides") {
		t.Fatalf("the reason is missing:\n%s", out)
	}
}
