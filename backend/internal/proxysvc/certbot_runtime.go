package proxysvc

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
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
	// snap is the host's certbot being the snap: its plugins are snaps too,
	// and a distribution package installs a second certbot it never loads.
	snap bool
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
	// /usr/bin precedes /snap/bin on every distribution that ships snapd, so
	// the snap is the certbot that runs only when there is no packaged one.
	rt.snap = rt.onHost && rt.on(ctx, "test", "-x", "/snap/bin/certbot").Run() == nil &&
		rt.on(ctx, "test", "-x", "/usr/bin/certbot").Run() != nil
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

// CertbotRuntimeView is which certbot the page reads and the jobs run, as the
// page shows it: "certbot 2.11.0 on the host · nginx, standalone, webroot".
type CertbotRuntimeView struct {
	OnHost bool `json:"onHost"`
	// Plugins are the authenticators it lists: the methods it can prove
	// control with.
	Plugins []string `json:"plugins"`
	// PluginsError is why it could not list them, when it could not.
	PluginsError string `json:"pluginsError,omitempty"`
	// Snap is the host's certbot being the snap, whose plugins no package
	// manager can install.
	Snap bool `json:"snap,omitempty"`
}

func certbotRuntimeView(rt *certbotRuntime, err error) CertbotRuntimeView {
	view := CertbotRuntimeView{OnHost: rt.onHost, Snap: rt.snap, Plugins: []string{}}
	if err != nil {
		view.PluginsError = err.Error()
	}
	for name := range rt.authenticators {
		view.Plugins = append(view.Plugins, name)
	}
	sort.Strings(view.Plugins)
	return view
}

// CertbotPackage names the package that brings plugin ("" for certbot
// itself) to the certbot the jobs run, under the host's package manager, or
// says why none does. Only names each manager is known to carry are
// answered: a guessed one fails in the package manager, and a package that
// installs beside a certbot that never loads it changes nothing.
func CertbotPackage(ctx context.Context, manager, plugin string) (string, error) {
	if plugin != "" && plugin != "nginx" {
		if _, ok := dnsProviderForPlugin(plugin); !ok {
			return "", fmt.Errorf("%q is not a certbot plugin this dashboard installs", plugin)
		}
	}
	if plugin != "" {
		rt, err := loadCertbotRuntime(ctx)
		if rt != nil && rt.snap {
			return "", fmt.Errorf("the host's certbot is the snap, which loads only plugins installed as snaps: run snap install certbot-%s on the host", plugin)
		}
		if rt != nil && err == nil && rt.authenticators[plugin] {
			return "", fmt.Errorf("the certbot that runs here (%s) already has the %s plugin", rt.where(), plugin)
		}
	}
	name := ""
	switch manager {
	case "apt", "dnf", "dnf5", "yum":
		name = "python3-certbot-" + plugin
		if plugin == "" {
			name = "certbot"
		}
	case "pacman":
		name = "certbot-" + plugin
		if plugin == "" {
			name = "certbot"
		}
	case "apk":
		if plugin == "" {
			name = "certbot"
		}
	}
	// Gandi's plugin is a third party's, packaged under no one name.
	if name == "" || plugin == "dns-gandi" {
		what := "certbot"
		if plugin != "" {
			what = "certbot's " + plugin + " plugin"
		}
		if manager == "" {
			return "", fmt.Errorf("this host has no package manager the dashboard drives; install %s by hand", what)
		}
		return "", fmt.Errorf("%s has no package this dashboard knows for %s; install it by hand", manager, what)
	}
	return name, nil
}

// CertbotInstalled asks certbot afresh, after a package install, whether it
// now has plugin ("" for certbot itself), and says which certbot answered.
func CertbotInstalled(ctx context.Context, plugin string) (string, error) {
	forgetCertbotRuntime()
	rt, err := loadCertbotRuntime(ctx)
	if rt == nil {
		return "", fmt.Errorf("the package installed, but no certbot can be found on the host or in the dashboard")
	}
	if plugin == "" {
		return fmt.Sprintf("certbot is ready: %s.", rt.where()), nil
	}
	if err != nil {
		return "", err
	}
	if !rt.authenticators[plugin] {
		return "", fmt.Errorf("the package installed, but the certbot that runs here (%s) still lists no %s plugin — it is not the certbot that package belongs to", rt.where(), plugin)
	}
	return fmt.Sprintf("The certbot that runs here (%s) now lists the %s plugin.", rt.where(), plugin), nil
}
