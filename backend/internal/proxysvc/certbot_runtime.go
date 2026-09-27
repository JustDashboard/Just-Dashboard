package proxysvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// One certbot, read and run in the same place.
//
// The image ships Debian's certbot so a deployment can issue on a server that
// has none, and a server running nginx usually has its own. The page used to
// read the image's and run the host's: it reported certbot 2.1.0 with two
// plugins while its jobs ran 2.11.0 with the nginx plugin, a DNS plugin
// installed on the host read as missing and was refused, and the refusal
// pointed at an install that changed nothing. Everything the Certificates
// page asks of certbot now goes through the runtime below — the host's when
// the host has one, this process's own otherwise — and the jobs run it there.

type certbotRuntime struct {
	// onHost is the host's certbot, reached through its namespaces; false is
	// this process's own.
	onHost  bool
	version string
	// authenticators is what `certbot plugins` lists as able to answer a
	// challenge.
	authenticators map[string]bool
}

// certbotRuntimeTTL bounds how stale the plugin list may be. Long enough that
// a page load costs no subprocess, short enough that a plugin installed a
// minute ago is seen.
const certbotRuntimeTTL = time.Minute

var certbotRuntimeCache struct {
	mu sync.Mutex
	at time.Time
	rt *certbotRuntime
}

// loadCertbotRuntime answers which certbot this dashboard uses and what it
// can do, or nil when there is none on either side. The error is the plugin
// list failing, which leaves nothing to say about which methods will work.
func loadCertbotRuntime(ctx context.Context) (*certbotRuntime, error) {
	certbotRuntimeCache.mu.Lock()
	defer certbotRuntimeCache.mu.Unlock()
	if certbotRuntimeCache.rt != nil && time.Since(certbotRuntimeCache.at) < certbotRuntimeTTL {
		return certbotRuntimeCache.rt, nil
	}
	if !hostexec.Available("certbot") {
		return nil, nil
	}
	rt := &certbotRuntime{onHost: hostexec.AvailableOnHost("certbot")}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if out, err := rt.command(ctx, "--version").CombinedOutput(); err == nil {
		rt.version = strings.TrimSpace(string(out))
	}
	out, err := rt.command(ctx, append([]string{"plugins"}, certbotProbeDirs()...)...).CombinedOutput()
	if err != nil {
		return rt, fmt.Errorf("certbot could not list its plugins: %s", lastMeaningfulLine(strings.TrimSpace(string(out))))
	}
	rt.authenticators = certbotAuthenticators(string(out))
	certbotRuntimeCache.rt, certbotRuntimeCache.at = rt, time.Now()
	return rt, nil
}

// forgetCertbotRuntime drops the cached answer, for a caller that has just
// changed it — and for tests that swap certbots between cases.
func forgetCertbotRuntime() {
	certbotRuntimeCache.mu.Lock()
	defer certbotRuntimeCache.mu.Unlock()
	certbotRuntimeCache.rt = nil
}

// where names the certbot for a message: which side, and which release.
func (rt *certbotRuntime) where() string {
	side := "this dashboard's own"
	if rt.onHost {
		side = "the host's"
	}
	if rt.version == "" {
		return side
	}
	return side + ", " + rt.version
}

func (rt *certbotRuntime) command(ctx context.Context, args ...string) *exec.Cmd {
	if rt.onHost {
		return hostexec.CommandOnHost(ctx, "certbot", args...)
	}
	return hostexec.Command(ctx, "certbot", args...)
}

// certbotProbeDirs points a certbot that only answers a question at
// directories of its own. Run as root, certbot takes the lock on its
// configuration directory even to list plugins, and a renewal timer that
// fires during that second fails with "Another instance of Certbot is
// already running" — a failure the page would have caused and then reported.
func certbotProbeDirs() []string {
	dir := filepath.Join(os.TempDir(), "just-dashboard-certbot-probe")
	return []string{"--config-dir", dir, "--work-dir", filepath.Join(dir, "work"), "--logs-dir", filepath.Join(dir, "logs")}
}

// CertbotCommand is certbot with args on the side the page reads it from, for
// a job to stream. env is added to the process environment.
func CertbotCommand(ctx context.Context, env []string, args ...string) (*exec.Cmd, error) {
	rt, err := loadCertbotRuntime(ctx)
	if rt == nil {
		if err == nil {
			err = fmt.Errorf("certbot is not installed on this host")
		}
		return nil, err
	}
	cmd := rt.command(ctx, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	return cmd, nil
}
