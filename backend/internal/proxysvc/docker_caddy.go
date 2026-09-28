package proxysvc

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const dockerCaddyRoot = "/config/just-dashboard"
const dockerCaddyImport = "import /config/just-dashboard/routes/*.caddy"

// dockerCaddyAccessRoot is where a route keeps the record of what it
// served. It sits inside the ingress's own persistent /config volume
// rather than on the host: the container may be one the operator already
// owned, and adding a bind mount to it would mean recreating their
// running web server to turn on a log.
const dockerCaddyAccessRoot = dockerCaddyRoot + "/access"

type dockerCaddy struct{ ID, Name, Source, Identity string }
type ingressContainer struct {
	ID     string
	Name   string
	State  struct{ Running bool }
	Config struct{ Cmd []string }
	Mounts []struct {
		Type, Source, Destination string
		RW                        bool
	}
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIP, HostPort string }
		Networks map[string]struct{ IPAddress string }
	}
}

func ingressContainers(ctx context.Context) ([]ingressContainer, error) {
	ids, err := hostexec.Command(ctx, "docker", "ps", "-q").Output()
	if err != nil {
		return nil, err
	}
	list := strings.Fields(string(ids))
	if len(list) == 0 {
		return nil, nil
	}
	raw, err := hostexec.Command(ctx, "docker", append([]string{"inspect"}, list...)...).Output()
	if err != nil {
		return nil, err
	}
	var result []ingressContainer
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("could not read Docker ingress ownership")
	}
	return result, nil
}

// Docker's socket proxy is not the HTTP server. Resolve the published mapping,
// and require a persistent Caddyfile/data layout before offering integration.
func discoverDockerCaddy(ctx context.Context) (*dockerCaddy, error) {
	if !hostexec.Available("docker") {
		return nil, nil
	}
	containers, err := ingressContainers(ctx)
	if err != nil {
		return nil, errors.New("could not determine Docker ingress ownership")
	}
	return selectDockerCaddy(containers)
}

func selectDockerCaddy(containers []ingressContainer) (*dockerCaddy, error) {
	var edge *dockerCaddy
	for _, c := range containers {
		if !c.State.Running {
			continue
		}
		public := func(key, port string) bool {
			for _, binding := range c.NetworkSettings.Ports[key] {
				if binding.HostPort == port && (binding.HostIP == "0.0.0.0" || binding.HostIP == "::" || binding.HostIP == "") {
					return true
				}
			}
			return false
		}
		if !public("80/tcp", "80") {
			continue
		}
		command := c.Config.Cmd
		if len(command) < 4 || filepath.Base(command[0]) != "caddy" || command[1] != "run" || !strings.Contains(strings.Join(command[2:], " "), "--config /etc/caddy/Caddyfile") {
			continue
		}
		if !public("443/tcp", "443") {
			return nil, fmt.Errorf("Caddy container %s owns HTTP but does not publish HTTPS on port 443", strings.TrimPrefix(c.Name, "/"))
		}
		found := dockerCaddy{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/")}
		config, data := false, false
		storage := map[string]string{}
		for _, mount := range c.Mounts {
			if mount.Destination == "/config" || mount.Destination == "/data" {
				storage[mount.Destination] = mount.Source
			}
			if mount.Destination == "/etc/caddy/Caddyfile" && mount.Type == "bind" {
				found.Source = mount.Source
			}
			if mount.Destination == "/config" && mount.RW {
				config = true
			}
			if mount.Destination == "/data" && mount.RW {
				data = true
			}
		}
		if found.Source == "" || !config || !data {
			return nil, fmt.Errorf("Caddy container %s needs a persisted Caddyfile, /config and /data before deployments can share it", found.Name)
		}
		if edge != nil {
			return nil, errors.New("multiple public Caddy containers require an explicit ingress selection")
		}
		found.Identity = routeDigest(found.Source + "\x00" + storage["/config"] + "\x00" + storage["/data"])
		edge = &found
	}
	return edge, nil
}

func (c *dockerCaddy) command(ctx context.Context, input string, args ...string) ([]byte, error) {
	command := hostexec.Command(ctx, "docker", append([]string{"exec", "-i", c.ID}, args...)...)
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	return command.Output()
}

func (c *dockerCaddy) read(ctx context.Context, path string) (string, bool, error) {
	evidence, err := c.command(ctx, "", "sh", "-c", `if [ -e "$1" ]; then printf present; else printf absent; fi`, "sh", path)
	if err != nil {
		return "", false, err
	}
	if string(evidence) == "absent" {
		return "", false, nil
	}
	if string(evidence) != "present" {
		return "", false, errors.New("could not establish Caddy file ownership")
	}
	raw, err := c.command(ctx, "", "cat", path)
	return string(raw), true, err
}

func (c *dockerCaddy) write(ctx context.Context, path, content string) error {
	if !strings.HasPrefix(path, dockerCaddyRoot+"/") || strings.Contains(path, "..") {
		return ErrUnsafePath
	}
	// Only constant shell source is used; paths and bytes are separate argv/stdin.
	_, err := c.command(ctx, content, "sh", "-c", `umask 077; mkdir -p "$1" && tmp=$(mktemp "$1/.jd-write-XXXXXX") && trap 'rm -f "$tmp"' EXIT && cat > "$tmp" && mv "$tmp" "$2"`, "sh", filepath.Dir(path), path)
	return err
}

func (c *dockerCaddy) reload(ctx context.Context) error {
	if _, err := c.command(ctx, "", "caddy", "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("Caddy configuration validation failed: %w", err)
	}
	if _, err := c.command(ctx, "", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("Caddy reload failed: %w", err)
	}
	return nil
}

func (c *dockerCaddy) configurationSynced(ctx context.Context) error {
	// Never replace a file-backed configuration over unsaved admin-API edits.
	adapted, err := c.command(ctx, "", "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	if err != nil {
		return err
	}
	active, err := c.command(ctx, "", "wget", "-qO-", "http://127.0.0.1:2019/config/")
	if err != nil {
		return errors.New("Caddy's local configuration API is unavailable")
	}
	canonical := func(raw []byte) []byte {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return nil
		}
		out, _ := json.Marshal(value)
		return out
	}
	if len(canonical(adapted)) == 0 || len(canonical(active)) == 0 || !bytes.Equal(canonical(adapted), canonical(active)) {
		return errors.New("Caddy has unsaved API configuration changes; persist them before adding deployment routes")
	}
	return nil
}

func (c *dockerCaddy) attach(ctx context.Context) error {
	raw, err := os.ReadFile(c.Source)
	if err != nil {
		return fmt.Errorf("read Caddy's persisted configuration: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == dockerCaddyImport {
			return c.configurationSynced(ctx)
		}
	}
	if err := c.configurationSynced(ctx); err != nil {
		return err
	}
	if _, err := c.command(ctx, "", "mkdir", "-p", dockerCaddyRoot+"/routes"); err != nil {
		return err
	}
	// A bind mount of one file keeps its inode; replacing it with rename would
	// leave Caddy reading the old inode until its container was recreated.
	next := string(raw) + "\n# Just Dashboard managed deployment routes\n" + dockerCaddyImport + "\n"
	if current, err := os.ReadFile(c.Source); err != nil || !bytes.Equal(current, raw) {
		return errors.New("Caddyfile changed while preparing ingress")
	}
	file, err := os.OpenFile(c.Source, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(next[len(raw):])
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := c.reload(ctx); err != nil {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		current, readErr := os.ReadFile(c.Source)
		if readErr != nil || string(current) != next {
			return fmt.Errorf("%w; Caddyfile changed concurrently and was not overwritten", err)
		}
		if restoreErr := os.WriteFile(c.Source, raw, 0600); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return errors.Join(err, c.reload(restoreCtx))
	}
	return nil
}

// installACMERoots copies a private authority's trust bundle into the
// container, where the route's issuer block names it. Nothing to copy for
// the default authority.
func (c *dockerCaddy) installACMERoots(ctx context.Context, directory ACMEDirectory) error {
	if !directory.private() {
		return nil
	}
	raw, err := os.ReadFile(directory.CARoot)
	if err != nil {
		return fmt.Errorf("JD_ACME_CA_ROOT: %w", err)
	}
	return c.write(ctx, dockerCaddyACMERoot, string(raw))
}

func dockerCaddyRoutePath(name string) (string, error) {
	if !strings.HasPrefix(name, "just-dashboard-") || filepath.Base(name) != name || strings.ContainsAny(name, "\x00\r\n") {
		return "", ErrUnsafePath
	}
	return dockerCaddyRoot + "/routes/" + name + ".caddy", nil
}

// dockerCaddyAccessLogPath is where one route's request record lives. It takes
// the same name through the same check as the route file, because a name that
// cannot be trusted to build a config path cannot be trusted to build a log
// path either — and this one is handed to Caddy, which will create whatever it
// is pointed at.
func dockerCaddyAccessLogPath(name string) (string, error) {
	if _, err := dockerCaddyRoutePath(name); err != nil {
		return "", err
	}
	return dockerCaddyAccessRoot + "/" + strings.TrimSuffix(name, ".conf") + ".log", nil
}

func renderDockerCaddyRoute(route DeploymentRoute, upstream string) (string, error) {
	if len(route.Domains) == 0 {
		return "", errors.New("a public route needs a domain")
	}
	names := []string{}
	for _, name := range route.Domains {
		if !certDomainRe.MatchString(name) || strings.HasPrefix(name, "*.") {
			return "", errors.New("invalid managed Caddy domain")
		}
		if !route.TLS {
			name = "http://" + name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	directive := `respond "Deployment is preparing" 503`
	if upstream != "" {
		parsed, err := url.Parse(upstream)
		if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("invalid Caddy deployment upstream")
		}
		directive = "reverse_proxy " + strconv.Quote(upstream)
	}
	tlsDirective := ""
	if route.TLS && route.CertPath != "" && !strings.HasPrefix(filepath.Base(filepath.Dir(route.CertPath)), "caddy-") {
		certPath, keyPath, err := dockerCaddyCertificatePaths(route.CertPath)
		if err != nil {
			return "", err
		}
		tlsDirective = "  tls " + strconv.Quote(certPath) + " " + strconv.Quote(keyPath) + "\n"
	}
	if route.TLS && tlsDirective == "" {
		// Caddy issues and renews this one itself; the configured authority,
		// if any, is where it asks.
		tlsDirective = acmeDirectory().caddyIssuer()
	}
	logDirective := ""
	if route.AccessLog {
		var err error
		if logDirective, err = renderAccessLogDirective(route.Name); err != nil {
			return "", err
		}
	}
	// Caddy sets no request-body limit of its own, so a route carries one
	// only when the plan names it.
	bodyDirective := ""
	if route.MaxBodyMB > 0 {
		bodyDirective = "  request_body {\n    max_size " + strconv.Itoa(route.MaxBodyMB) + "MB\n  }\n"
	}
	authDirective := ""
	if len(route.BasicAuth) > 0 {
		lines := make([]string, 0, len(route.BasicAuth))
		for _, user := range route.BasicAuth {
			if !authUserRe.MatchString(user.Username) || !bcryptHashRe.MatchString(user.Hash) {
				return "", errors.New("invalid deployment route credentials")
			}
			lines = append(lines, "    "+user.Username+" "+user.Hash)
		}
		authDirective = "  basic_auth {\n" + strings.Join(lines, "\n") + "\n  }\n"
	}
	return "# Managed by Just Dashboard\n" + strings.Join(names, ", ") + " {\n" + tlsDirective + logDirective + authDirective + bodyDirective + "  " + directive + "\n}\n", nil
}

// renderAccessLogDirective is the `log` block a managed route carries. One
// spelling, because two routes write it: activation renders it into a new
// route, and the lifecycle worker adds it to a route written before request
// recording existed — and the two must come out byte-identical, or an upgraded
// route reads as hand-edited to the next pass.
//
// JSON rather than the console format: the console spelling drops the
// request duration into a human sentence, and the page's whole point is a
// latency reading. Rotation is Caddy's own — the ingress volume is not on a
// logrotate schedule this dashboard controls, and an access log is the
// fastest-growing file a deployment produces. Rolled generations stay
// uncompressed so the reader can pick one up from an offset: the tail of the
// file that rolled between two reads is recovered from wherever the roller
// put it, and a gzip has no offsets.
func renderAccessLogDirective(name string) (string, error) {
	path, err := dockerCaddyAccessLogPath(name)
	if err != nil {
		return "", err
	}
	return "  log {\n" +
		"    output file " + strconv.Quote(path) + " {\n" +
		"      roll_size 16MiB\n" +
		"      roll_keep 4\n" +
		"      roll_keep_for 336h\n" +
		"      roll_uncompressed\n" +
		"    }\n" +
		"    format json\n" +
		"  }\n", nil
}

func (c *dockerCaddy) snapshot(ctx context.Context, name string) (DeploymentRouteSnapshot, error) {
	path, err := dockerCaddyRoutePath(name)
	if err != nil {
		return DeploymentRouteSnapshot{}, err
	}
	content, exists, err := c.read(ctx, path)
	if err != nil {
		return DeploymentRouteSnapshot{}, err
	}
	return DeploymentRouteSnapshot{Version: 1, Driver: "docker-caddy", ContainerID: c.ID, IngressIdentity: c.Identity, Name: name, Path: path, Existed: exists, Content: content, ContentDigest: routeDigest(content), Mode: 0600}, nil
}

func (c *dockerCaddy) restore(ctx context.Context, snapshot DeploymentRouteSnapshot) error {
	path, err := dockerCaddyRoutePath(snapshot.Name)
	if err != nil || snapshot.Version != 1 || snapshot.Driver != "docker-caddy" || path != snapshot.Path || snapshot.ContentDigest != routeDigest(snapshot.Content) || (snapshot.ContainerID != c.ID && (snapshot.IngressIdentity == "" || snapshot.IngressIdentity != c.Identity)) {
		return ErrRouteRecovery
	}
	if snapshot.Existed {
		err = c.write(ctx, path, snapshot.Content)
	} else {
		_, err = c.command(ctx, "", "rm", "-f", path)
	}
	if err != nil {
		return err
	}
	if err := c.reload(ctx); err != nil {
		return err
	}
	actual, exists, err := c.read(ctx, path)
	if err != nil || exists != snapshot.Existed || (exists && actual != snapshot.Content) {
		return ErrRouteRecovery
	}
	return nil
}

// Reach the candidate on an existing Docker network, without widening its host
// publication. Caddy is the only public listener and keeps its other networks.
type dockerCaddyTarget struct {
	ContainerID string `json:"containerId"`
	Network     string `json:"network"`
	Port        string `json:"port"`
	Address     string `json:"address"`
}

func (target dockerCaddyTarget) upstream() string {
	return "http://" + net.JoinHostPort(target.Address, target.Port)
}

const dockerCaddyTargetPrefix = "# Just Dashboard target: "

func (target dockerCaddyTarget) metadata() string {
	raw, _ := json.Marshal(target)
	return dockerCaddyTargetPrefix + string(raw) + "\n"
}

func (c *dockerCaddy) target(ctx context.Context, upstream string) (dockerCaddyTarget, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || net.ParseIP(parsed.Hostname()) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() || parsed.Port() == "" {
		return dockerCaddyTarget{}, errors.New("Caddy requires a private deployment upstream")
	}
	containers, err := ingressContainers(ctx)
	if err != nil {
		return dockerCaddyTarget{}, err
	}
	for _, candidate := range containers {
		for target, bindings := range candidate.NetworkSettings.Ports {
			if !strings.HasSuffix(target, "/tcp") {
				continue
			}
			for _, binding := range bindings {
				if binding.HostPort != parsed.Port() || binding.HostIP != parsed.Hostname() {
					continue
				}
				networks := make([]string, 0, len(candidate.NetworkSettings.Networks))
				for name := range candidate.NetworkSettings.Networks {
					networks = append(networks, name)
				}
				sort.Strings(networks)
				for _, network := range networks {
					address := candidate.NetworkSettings.Networks[network].IPAddress
					if net.ParseIP(address) != nil {
						return dockerCaddyTarget{candidate.ID, network, strings.TrimSuffix(target, "/tcp"), address}, nil
					}
				}
			}
		}
	}
	return dockerCaddyTarget{}, errors.New("Caddy ingress requires a running Docker candidate with a matching loopback publication")
}

func (c *dockerCaddy) connectTarget(ctx context.Context, target dockerCaddyTarget) (bool, error) {
	raw, err := hostexec.Command(ctx, "docker", "inspect", c.ID).Output()
	if err != nil {
		return false, err
	}
	var containers []ingressContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return false, ErrRouteRecovery
	}
	if _, exists := containers[0].NetworkSettings.Networks[target.Network]; exists {
		return false, nil
	}
	_, err = hostexec.Command(ctx, "docker", "network", "connect", target.Network, c.ID).Output()
	return true, err
}

func (c *dockerCaddy) domainClaimed(ctx context.Context, domain, ownContent string) (bool, error) {
	raw, err := c.command(ctx, "", "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	if err != nil {
		return false, err
	}
	var config any
	if json.Unmarshal(raw, &config) != nil {
		return false, errors.New("invalid adapted Caddy configuration")
	}
	claimed := false
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if hosts, ok := node["host"].([]any); ok {
				for _, host := range hosts {
					if name, ok := host.(string); ok && certificateCoversAll([]string{name}, []string{domain}) {
						claimed = true
					}
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(config)
	if claimed && ownContent != "" {
		own, err := c.command(ctx, ownContent, "caddy", "adapt", "--config", "-", "--adapter", "caddyfile")
		if err != nil {
			return false, err
		}
		claimed = false
		var ownConfig any
		if json.Unmarshal(own, &ownConfig) != nil {
			return false, ErrInvalidConf
		}
		walk(ownConfig)
		return !claimed, nil
	}
	return claimed, nil
}

func (s *Service) applyDockerCaddyRoute(ctx context.Context, c *dockerCaddy, route DeploymentRoute) (DeploymentRouteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := c.snapshot(ctx, route.Name)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	result := DeploymentRouteResult{Snapshot: snapshot}
	if snapshot.Existed && !strings.HasPrefix(snapshot.Content, "# Managed by Just Dashboard\n") {
		return result, errors.New("Caddy route filename is owned by another configuration")
	}
	for _, domain := range route.Domains {
		claimed, err := c.domainClaimed(ctx, domain, snapshot.Content)
		if err != nil {
			return result, err
		}
		if claimed {
			return result, fmt.Errorf("%s is already served by an existing Caddy site", domain)
		}
	}
	target, err := c.target(ctx, route.Upstream)
	if err != nil {
		return result, err
	}
	content, err := renderDockerCaddyRoute(route, target.upstream())
	if err != nil {
		return result, err
	}
	if err = c.attach(ctx); err != nil {
		return result, err
	}
	if route.AccessLog {
		if err := c.ensureAccessLogDir(ctx); err != nil {
			return result, err
		}
	}
	if route.TLS && route.CertPath != "" && !strings.HasPrefix(filepath.Base(filepath.Dir(route.CertPath)), "caddy-") {
		cert, err := os.ReadFile(route.CertPath)
		if err != nil {
			return result, err
		}
		key, err := os.ReadFile(route.KeyPath)
		if err != nil {
			return result, err
		}
		leaf, _, err := parseCertChain(string(cert))
		if err != nil {
			return result, err
		}
		if err := keyMatchesCertificate(string(key), leaf); err != nil {
			return result, err
		}
		certPath, keyPath, err := dockerCaddyCertificatePaths(route.CertPath)
		if err != nil {
			return result, err
		}
		if err := c.write(ctx, certPath, string(cert)); err != nil {
			return result, err
		}
		if err := c.write(ctx, keyPath, string(key)); err != nil {
			return result, err
		}
	}
	content += target.metadata()
	if _, err := c.connectTarget(ctx, target); err != nil {
		return result, err
	}
	if route.TLS {
		if err := c.installACMERoots(ctx, acmeDirectory()); err != nil {
			return result, err
		}
	}
	if err = c.write(ctx, snapshot.Path, content); err == nil {
		err = c.reload(ctx)
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		restoreErr := c.restore(recovery, snapshot)
		result.Recovered = restoreErr == nil
		return result, errors.Join(err, restoreErr)
	}
	actual, exists, err := c.read(ctx, snapshot.Path)
	result.Applied = true
	result.Verified = err == nil && exists && actual == content
	if !result.Verified {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		restoreErr := c.restore(recovery, snapshot)
		result.Recovered = restoreErr == nil
		return result, errors.Join(ErrRouteRecovery, restoreErr)
	}
	return result, nil
}

func (s *Service) ensureDockerCaddyCertificate(ctx context.Context, c *dockerCaddy, names []string, log func(string, string)) (DeploymentCertificate, error) {
	if len(names) != 1 {
		return DeploymentCertificate{}, errors.New("automatic Caddy ingress currently requires one hostname per deployment")
	}
	directory := acmeDirectory()
	if err := directory.validate(); err != nil {
		return DeploymentCertificate{}, err
	}
	roots, err := directory.roots()
	if err != nil {
		return DeploymentCertificate{}, err
	}
	// A private authority validates however it was set up to; public DNS
	// says nothing about it. Let's Encrypt and its staging twin still need
	// the name to resolve here before an order is worth placing.
	if !directory.private() {
		if err := validateDeploymentHTTPDomains(ctx, names, net.DefaultResolver.LookupIPAddr); err != nil {
			return DeploymentCertificate{}, err
		}
	}
	// Caddy owns issuance and renewal on the public listener. Imported bytes are
	// release evidence; traffic continues using Caddy's actively renewed storage.
	//
	// "Not issued yet" and "issued but the import failed" are different
	// answers: the first is waited on, the second is reported at once, so a
	// directory this process cannot write never reads as an authority that
	// never answered.
	errNotIssued := errors.New("Caddy has not issued the requested certificate yet")
	read := func(ctx context.Context) (*ImportResult, error) {
		// The certificates directory itself appears with the first issuance.
		raw, err := c.command(ctx, "", "find", "/data/caddy/certificates", "-type", "f", "-name", "*.crt")
		if err != nil {
			return nil, errNotIssued
		}
		for _, path := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if !strings.HasPrefix(path, "/data/caddy/certificates/") {
				continue
			}
			certificate, _, err := c.read(ctx, path)
			if err != nil {
				continue
			}
			leaf, _, err := parseCertChain(certificate)
			if err != nil || time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) || leaf.VerifyHostname(names[0]) != nil {
				continue
			}
			intermediates := x509.NewCertPool()
			remaining := []byte(certificate)
			for {
				block, rest := pem.Decode(remaining)
				if block == nil {
					break
				}
				remaining = rest
				parsed, err := x509.ParseCertificate(block.Bytes)
				if err == nil && !bytes.Equal(parsed.Raw, leaf.Raw) {
					intermediates.AddCert(parsed)
				}
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: names[0], Intermediates: intermediates, Roots: roots}); err != nil {
				continue
			}
			key, _, err := c.read(ctx, strings.TrimSuffix(path, ".crt")+".key")
			if err != nil {
				continue
			}
			imported, err := ImportCertificate("caddy-"+strings.TrimPrefix(routeDigest(names[0]), "sha256:")[:24], certificate, key)
			if err != nil {
				return nil, fmt.Errorf("Caddy issued the certificate for %s but it could not be kept as release evidence: %w", names[0], err)
			}
			return imported, nil
		}
		return nil, errNotIssued
	}
	result := func(imported *ImportResult, outcome CertificateOutcome) DeploymentCertificate {
		return DeploymentCertificate{Outcome: outcome, CertPath: imported.CertPath, KeyPath: imported.KeyPath, Name: imported.Name, Method: "caddy", Domains: names}
	}
	name := "just-dashboard-certificate-" + strings.TrimPrefix(routeDigest(names[0]), "sha256:")[:24]
	content, err := renderDockerCaddyRoute(DeploymentRoute{Domains: names, TLS: true}, "")
	if err != nil {
		return DeploymentCertificate{}, err
	}
	snapshot, err := c.snapshot(ctx, name)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	if snapshot.Existed && snapshot.Content != content {
		return DeploymentCertificate{}, errors.New("certificate preparation route was modified outside Just Dashboard")
	}
	// A deterministic, unchanged preparation route is safe to resume after a
	// process restart. Cleanup always removes it, including reused certificates.
	empty := snapshot
	empty.Existed, empty.Content, empty.ContentDigest = false, "", routeDigest("")
	cleanup := func() error {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return c.restore(recovery, empty)
	}
	if imported, err := read(ctx); err == nil {
		if snapshot.Existed {
			if err := c.configurationSynced(ctx); err != nil {
				return DeploymentCertificate{}, err
			}
			if err := cleanup(); err != nil {
				return DeploymentCertificate{}, err
			}
		}
		return result(imported, CertificateReused), nil
	}
	claimed, err := c.domainClaimed(ctx, names[0], snapshot.Content)
	if err != nil {
		return DeploymentCertificate{}, err
	}
	if claimed {
		return DeploymentCertificate{}, fmt.Errorf("Caddy already serves %s but its certificate is not ready; existing traffic was left unchanged", names[0])
	}
	if err = c.attach(ctx); err != nil {
		return DeploymentCertificate{}, err
	}
	if err = c.installACMERoots(ctx, directory); err != nil {
		return DeploymentCertificate{}, err
	}
	if err = c.write(ctx, snapshot.Path, content); err == nil {
		err = c.reload(ctx)
	}
	if err != nil {
		return DeploymentCertificate{}, errors.Join(err, cleanup())
	}
	if log != nil {
		log("stdout", "Requesting HTTPS through the existing public Caddy container "+c.Name)
	}
	issueCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		imported, err := read(issueCtx)
		if err == nil {
			if err := cleanup(); err != nil {
				return DeploymentCertificate{}, err
			}
			return result(imported, CertificateIssued), nil
		}
		if !errors.Is(err, errNotIssued) && issueCtx.Err() == nil {
			return DeploymentCertificate{}, errors.Join(err, cleanup())
		}
		select {
		case <-issueCtx.Done():
			return DeploymentCertificate{}, errors.Join(fmt.Errorf("Caddy did not issue a certificate for %s before the deadline; verify public DNS and inbound HTTPS reachability", names[0]), cleanup())
		case <-ticker.C:
		}
	}
}

func dockerCaddyCertificatePaths(path string) (string, string, error) {
	cert, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	// Immutable certificate copies keep route rollback independent of a newer
	// imported certificate overwriting an environment's previous keypair.
	prefix := dockerCaddyRoot + "/certs/" + strings.TrimPrefix(routeDigest(string(cert)), "sha256:")
	return prefix + ".crt", prefix + ".key", nil
}
