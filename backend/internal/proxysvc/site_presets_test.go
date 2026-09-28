package proxysvc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The new-site form's presets live in frontend/src/components/proxy/site-presets.ts
// and are checked in here, one JSON file each, by that module's own test, so
// the two cannot drift. Each is laid over the spec the form starts from and
// has to validate, read back as itself and pass a real `nginx -t`, with TLS
// off and on.

// presetBase is the spec the form starts from (BLANK in site-presets.ts).
func presetBase(name string) *SiteSpec {
	return &SiteSpec{
		Name: name, Domains: []string{name + ".preset.test"}, Kind: "proxy",
		Upstream: "http://127.0.0.1:3000", ForceHTTPS: true, HSTS: true, HTTP2: true,
		WebSockets: true, Gzip: true, BlockExploits: true, SecurityHeaders: true,
		ClientMaxBody: "50m", ProxyTimeout: 60, AccessLog: true,
		AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
}

type sitePreset struct {
	id   string
	spec *SiteSpec
}

// loadPresets lays every preset over the form's starting spec, naming each
// site after the preset and suffix. A field the TypeScript preset sets and
// SiteSpec does not have fails the decode: the server would drop it.
func loadPresets(t *testing.T, suffix string) []sitePreset {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "presets", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no presets in testdata/presets (%v)", err)
	}
	var out []sitePreset
	for _, file := range files {
		id := strings.TrimSuffix(filepath.Base(file), ".json")
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		spec := presetBase("preset-" + id + suffix)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(spec); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		// What a kind needs that no preset can know: the folder a static
		// site serves and the address a redirect sends to.
		switch spec.Kind {
		case "static":
			spec.Root = "/var/www/preset"
		case "redirect":
			spec.RedirectTo = "https://example.com"
		}
		out = append(out, sitePreset{id, spec})
	}
	return out
}

// withTLS is a preset served over HTTPS with the form's defaults: HTTP sent
// to HTTPS, HSTS and HTTP/2.
func withTLS(spec *SiteSpec, cert, key string) *SiteSpec {
	next := *spec
	next.TLS, next.CertPath, next.KeyPath = true, cert, key
	return &next
}

func TestPresetsRenderAndReadBackAsThemselves(t *testing.T) {
	for _, p := range loadPresets(t, "") {
		for _, spec := range []*SiteSpec{p.spec, withTLS(p.spec, "/etc/ssl/preset.crt", "/etc/ssl/preset.key")} {
			rendered, err := RenderNginx(spec)
			if err != nil {
				t.Fatalf("%s (tls %v): %v", p.id, spec.TLS, err)
			}
			back, managed := ParseSiteSpec(spec.Name, rendered)
			if !managed {
				t.Fatalf("%s: the marker was not recognised", p.id)
			}
			again, err := RenderNginx(back)
			if err != nil {
				t.Fatalf("%s read back as a spec the server refuses: %v", p.id, err)
			}
			if again != rendered {
				t.Fatalf("%s (tls %v) changes when it is opened and saved:\n%s\nsecond render:\n%s",
					p.id, spec.TLS, rendered, again)
			}
			// Over HTTPS a preset is a site this dashboard would have set up
			// itself, so the form's notes have nothing to say about it.
			if spec.TLS {
				if warnings := SpecWarnings(spec); len(warnings) > 0 {
					t.Fatalf("%s warns about its own settings: %q", p.id, warnings)
				}
			}
		}
	}
}

// installPreset writes a rendered site into the private prefix on loopback
// ports instead of 80 and 443, with its logs in the prefix, and links it.
func installPreset(t *testing.T, root string, spec *SiteSpec, port, tlsPort int) {
	t.Helper()
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatalf("%s: %v", spec.Name, err)
	}
	content = strings.NewReplacer(
		"    listen 80;\n", fmt.Sprintf("    listen 127.0.0.1:%d;\n", port),
		"    listen [::]:80;\n", "",
		"    listen 443 ssl;\n", fmt.Sprintf("    listen 127.0.0.1:%d ssl;\n", tlsPort),
		"    listen [::]:443 ssl;\n", "",
		"/var/log/nginx/", filepath.Join(root, "logs")+"/",
	).Replace(content)
	available := filepath.Join(root, "sites-available", spec.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", spec.Name)); err != nil {
		t.Fatal(err)
	}
}

// presetCertificate writes a self-signed pair for nginx to load.
func presetCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "preset.test"},
		DNSNames:     []string{"*.preset.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, private := filepath.Join(dir, "preset.crt"), filepath.Join(dir, "preset.key")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, private
}

// Every preset, plain and over HTTPS, side by side in one configuration: the
// host's nginx passes it without a warning.
func TestLivePresetsPassNginxTest(t *testing.T) {
	requireLiveNginxHTTP2(t)
	root := liveNginx(t)
	cert, key := presetCertificate(t, root)
	port, tlsPort := freePort(t), freePort(t)
	for _, p := range loadPresets(t, "") {
		installPreset(t, root, p.spec, port, tlsPort)
	}
	for _, p := range loadPresets(t, "-tls") {
		installPreset(t, root, withTLS(p.spec, cert, key), port, tlsPort)
	}
	v := runValidator(context.Background(), "nginx", "-t")
	if !v.Valid || v.Warnings > 0 {
		t.Fatalf("nginx -t on the presets: valid %v, %d warnings\n%s", v.Valid, v.Warnings, v.Output)
	}
}

// The single-page app preset answers a path with no file of its own with
// index.html, where a plain folder answers it 404, and still refuses the
// probes the exploit blocks exist for.
func TestLiveSPAPresetAnswersADeepLinkWithTheApp(t *testing.T) {
	root := liveNginx(t)
	www := filepath.Join(root, "www")
	if err := os.MkdirAll(filepath.Join(www, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "<div id=app>", "assets/app.js": "boot()", ".env": "SECRET=1"} {
		if err := os.WriteFile(filepath.Join(www, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var spa, files *SiteSpec
	for _, p := range loadPresets(t, "") {
		if p.id == "spa" {
			spa = p.spec
		}
	}
	if spa == nil {
		t.Fatal("no spa preset")
	}
	spa.Root = www
	files = &SiteSpec{}
	*files = *spa
	files.Name, files.Domains, files.SPA = "files", []string{"files.preset.test"}, false
	port := freePort(t)
	installPreset(t, root, spa, port, freePort(t))
	installPreset(t, root, files, port, freePort(t))
	startNginx(t, root)

	host := spa.Domains[0]
	if response, body := siteGet(t, port, host, "/settings/profile"); response.StatusCode != http.StatusOK || body != "<div id=app>" {
		t.Fatalf("a deep link answered %d %q, want the app's index.html", response.StatusCode, body)
	}
	if _, body := siteGet(t, port, host, "/assets/app.js"); body != "boot()" {
		t.Fatalf("a file that exists answered %q", body)
	}
	if response, _ := siteGet(t, port, host, "/.env"); response.StatusCode != http.StatusForbidden {
		t.Fatalf("a dotfile answered %d, want the probe block's 403", response.StatusCode)
	}
	if response, _ := siteGet(t, port, "files.preset.test", "/settings/profile"); response.StatusCode != http.StatusNotFound {
		t.Fatalf("a plain folder answered a deep link %d, want 404", response.StatusCode)
	}
}

// zeros is an endless run of zero bytes, so a large upload is streamed rather
// than held in memory.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// The large-upload presets take a body past the 50 MB every other site is
// limited to, and pass it through to the application; the Node.js preset
// refuses the same upload with nginx's 413.
func TestLiveLargeUploadPresetsPassABodyThrough(t *testing.T) {
	root := liveNginx(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, n)
	}))
	defer upstream.Close()

	port := freePort(t)
	sites := map[string]*SiteSpec{}
	for _, p := range loadPresets(t, "") {
		if p.id == "registry" || p.id == "minio" || p.id == "node" {
			p.spec.Upstream = upstream.URL
			p.spec.AccessLog = false
			installPreset(t, root, p.spec, port, freePort(t))
			sites[p.id] = p.spec
		}
	}
	startNginx(t, root)

	const size = 64 << 20
	upload := func(spec *SiteSpec) (int, string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPut, "http://127.0.0.1:"+strconv.Itoa(port)+"/v2/blob", io.LimitReader(zeros{}, size))
		request.Host = spec.Domains[0]
		request.ContentLength = size
		var response *http.Response
		var err error
		for deadline := time.Now().Add(5 * time.Second); ; {
			response, err = (&http.Client{Timeout: 60 * time.Second}).Do(request)
			if err == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
			request.Body = io.NopCloser(io.LimitReader(zeros{}, size))
		}
		if err != nil {
			t.Fatalf("%s: %v", spec.Name, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	for _, id := range []string{"registry", "minio"} {
		if status, body := upload(sites[id]); status != http.StatusOK || body != strconv.Itoa(size) {
			t.Fatalf("%s: a 64 MB upload answered %d %q, want 200 with every byte at the application", id, status, body)
		}
	}
	if status, _ := upload(sites["node"]); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("node: a 64 MB upload answered %d, want 413 at the 50 MB limit", status)
	}
}
