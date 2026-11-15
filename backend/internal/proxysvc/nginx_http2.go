package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// nginx 1.25.1 moved HTTP/2 from a `listen` parameter to its own `http2 on;`
// directive, and every nginx before it refuses that directive as unknown:
// the whole configuration fails its test. Ubuntu 24.04 ships 1.24 and 22.04
// ships 1.18, so a site or deployment route written with the new spelling
// was refused on a fresh install of either, and a first deployment failed
// at its cutover with nothing the operator could change.
//
// So the host's nginx is asked once which spelling it reads, by testing a
// configuration of a few lines that uses the new one. Asking rather than
// comparing version numbers is what keeps builds that print another name,
// such as OpenResty or Angie, right as well.

// http2ProbeTTL is how long one answer is kept. An nginx upgraded or
// downgraded under a running dashboard is written for correctly again once
// it runs out; the old spelling still works on a newer nginx meanwhile.
const http2ProbeTTL = 10 * time.Minute

type http2Spelling struct {
	mu     sync.Mutex
	at     time.Time
	legacy bool
	// probe is what asks nginx; a test replaces it.
	probe func(ctx context.Context, dir string) bool
}

// nginxNeedsListenHTTP2 reports whether this host's nginx reads HTTP/2 only
// as the `listen … http2` parameter. Anything other than nginx naming the
// directive as unknown — no nginx at all, a probe that could not be written
// — is answered with the current spelling, which is what was written
// before the probe existed.
func (s *Service) nginxNeedsListenHTTP2() bool {
	h := &s.http2
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.at.IsZero() && time.Since(h.at) < http2ProbeTTL {
		return h.legacy
	}
	probe := h.probe
	if probe == nil {
		probe = probeListenHTTP2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.legacy, h.at = probe(ctx, s.nginxDir), time.Now()
	return h.legacy
}

// probeListenHTTP2 tests a configuration of its own, in a folder under the
// nginx directory so the host's nginx reads it at the same path when the
// dashboard runs in a container, and never touches the configuration nginx
// serves. Nothing listens: `nginx -t` only parses.
func probeListenHTTP2(ctx context.Context, nginxDir string) bool {
	if !hostexec.Available("nginx") {
		return false
	}
	dir, err := os.MkdirTemp(nginxDir, ".jd-http2-probe-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	conf := filepath.Join(dir, "nginx.conf")
	content := fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\nhttp {\n    access_log off;\n    server {\n        listen 127.0.0.1:8443;\n        http2 on;\n    }\n}\n", dir)
	if err := os.WriteFile(conf, []byte(content), 0o600); err != nil {
		return false
	}
	out, err := hostexec.Command(ctx, "nginx", "-t", "-p", dir, "-c", conf).CombinedOutput()
	return err != nil && strings.Contains(string(out), `unknown directive "http2"`)
}

// forHostNginx gives spec the spelling this host's nginx reads. Every path
// that writes a file nginx serves passes through here before rendering.
func (s *Service) forHostNginx(spec *SiteSpec) {
	spec.listenHTTP2 = spec.HTTP2 && s.nginxNeedsListenHTTP2()
}

// ForHostNginx is forHostNginx for the API's preview, which renders a site
// before it is saved and has to show the file the save writes.
func (s *Service) ForHostNginx(spec *SiteSpec) { s.forHostNginx(spec) }
