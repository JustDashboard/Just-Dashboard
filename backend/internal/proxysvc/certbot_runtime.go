package proxysvc

import (
	"context"
	"fmt"
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
	// challenge, and installers what it lists as able to deploy one: a
	// lineage names one of each, and renews only while both are there.
	authenticators map[string]bool
	installers     map[string]bool
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
	dirs, cleanup, err := rt.probeDirs(ctx)
	if err != nil {
		return rt, err
	}
	out, err := rt.command(ctx, append([]string{"plugins"}, dirs...)...).CombinedOutput()
	cleanup()
	if err != nil {
		return rt, fmt.Errorf("certbot could not list its plugins: %s", lastMeaningfulLine(strings.TrimSpace(string(out))))
	}
	rt.authenticators = certbotAuthenticators(string(out))
	rt.installers = certbotInstallers(string(out))
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
	return rt.on(ctx, "certbot", args...)
}

// on runs name on the side this certbot runs on, so what it does to the
// filesystem is what certbot will see.
func (rt *certbotRuntime) on(ctx context.Context, name string, args ...string) *exec.Cmd {
	if rt.onHost {
		return hostexec.CommandOnHost(ctx, name, args...)
	}
	return hostexec.Command(ctx, name, args...)
}

const certbotProbePrefix = "just-dashboard-certbot-probe."

// probeDirs points a certbot that only answers a question at directories of
// its own. Run as root, certbot takes the lock on its configuration directory
// even to list plugins, and a renewal timer that fires during that second
// fails with "Another instance of Certbot is already running" — a failure the
// page would have caused and then reported.
//
// The directory is new for every probe, made by mktemp on certbot's side:
// mode 0700 under a name nobody could have created first. It used to be one
// fixed name in the shared temporary directory, which on the host is /tmp —
// any local user could make it their own before the first probe, and certbot,
// as root, follows the symlinks it finds inside and writes where they point.
func (rt *certbotRuntime) probeDirs(ctx context.Context) ([]string, func(), error) {
	out, err := rt.on(ctx, "mktemp", "-d", "-t", certbotProbePrefix+"XXXXXXXXXX").Output()
	dir := strings.TrimSpace(string(out))
	if err != nil || !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Base(dir), certbotProbePrefix) {
		reason := fmt.Sprintf("it printed %q", dir)
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			reason = lastMeaningfulLine(strings.TrimSpace(string(exit.Stderr)))
		} else if err != nil {
			reason = err.Error()
		}
		return nil, nil, fmt.Errorf("certbot could not list its plugins: mktemp made no directory to run it in: %s", reason)
	}
	cleanup := func() {
		// The probe's own context may be the one that ended it.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = rt.on(ctx, "rm", "-rf", "--", dir).Run()
	}
	return []string{"--config-dir", dir, "--work-dir", filepath.Join(dir, "work"), "--logs-dir", filepath.Join(dir, "logs")}, cleanup, nil
}

// certbotInstallers reads the plugins `certbot plugins` lists with the
// Installer interface, the way certbotAuthenticators reads the others.
func certbotInstallers(output string) map[string]bool {
	found := map[string]bool{}
	name := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "* "):
			name = strings.TrimSpace(strings.TrimPrefix(line, "* "))
		case name != "" && strings.HasPrefix(line, "Interfaces:"):
			for _, role := range strings.Split(strings.TrimPrefix(line, "Interfaces:"), ",") {
				if strings.EqualFold(strings.TrimSpace(role), "Installer") {
					found[name] = true
				}
			}
			name = ""
		}
	}
	return found
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
