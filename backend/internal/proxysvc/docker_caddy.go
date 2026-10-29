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
	"slices"
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

type dockerCaddy struct {
	ID, Name, Source, Identity string
	// ConfigSource is where the ingress's /config is on the host, which is
	// where the dashboard's wake socket goes (deployment_sleep.go).
	ConfigSource string
	// StartedAt is when the container last started, as Docker reports it,
	// so the overview can say how long the ingress has been up.
	StartedAt string
	// Driver names the Caddy in a route's snapshot: the public ingress, or
	// the loopback-only Caddy that gates tailnet-only deployments
	// (tailnet_gate.go). A snapshot is only ever restored into the Caddy
	// that took it.
	Driver string
}

// driver is the snapshot's name for this Caddy.
func (c *dockerCaddy) driver() string {
	if c.Driver != "" {
		return c.Driver
	}
	return accessDriverCaddy
}

type ingressContainer struct {
	ID    string
	Name  string
	State struct {
		Running   bool
		StartedAt string
	}
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
		if !runsCaddyfile(c) {
			continue
		}
		if !public("443/tcp", "443") {
			return nil, fmt.Errorf("Caddy container %s owns HTTP but does not publish HTTPS on port 443", strings.TrimPrefix(c.Name, "/"))
		}
		found, err := dockerCaddyFrom(c)
		if err != nil {
			return nil, err
		}
		if edge != nil {
			return nil, errors.New("multiple public Caddy containers require an explicit ingress selection")
		}
		edge = found
	}
	return edge, nil
}

func runsCaddyfile(c ingressContainer) bool {
	command := c.Config.Cmd
	return len(command) >= 4 && filepath.Base(command[0]) == "caddy" && command[1] == "run" &&
		strings.Contains(strings.Join(command[2:], " "), "--config /etc/caddy/Caddyfile")
}

// dockerCaddyFrom is everything discovery asks of an ingress apart from the
// ports it publishes: a persisted Caddyfile and writable /config and /data.
func dockerCaddyFrom(c ingressContainer) (*dockerCaddy, error) {
	found := dockerCaddy{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), StartedAt: c.State.StartedAt}
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
	found.Identity = routeDigest(found.Source + "\x00" + storage["/config"] + "\x00" + storage["/data"])
	found.ConfigSource = storage["/config"]
	return &found, nil
}

// namedDockerCaddy is the ingress an operator named rather than the one
// discovery would choose. It must still be a running Caddy over its own
// persisted Caddyfile; only the public ports are not asked of it, which is
// what makes it nameable at all.
//
// It inspects that one container by name rather than every running one: a
// listing inspected a moment after it was taken fails whole when any other
// container on the host went away in between.
func namedDockerCaddy(ctx context.Context, name string) (*dockerCaddy, error) {
	raw, err := hostexec.Command(ctx, "docker", "inspect", "--type", "container", name).Output()
	if err != nil {
		return nil, fmt.Errorf("the ingress container %s is not running", name)
	}
	var containers []ingressContainer
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return nil, errors.New("could not read Docker ingress ownership")
	}
	c := containers[0]
	if !c.State.Running || !runsCaddyfile(c) {
		return nil, fmt.Errorf("the ingress container %s is not a running Caddy serving /etc/caddy/Caddyfile", name)
	}
	return dockerCaddyFrom(c)
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
	for _, name := range route.Domains {
		if !certDomainRe.MatchString(name) {
			return "", errors.New("invalid managed Caddy domain")
		}
		// A wildcard site is served from a certificate the run proved over
		// a DNS challenge through certbot and copied here; without one,
		// Caddy's automatic HTTPS would try to order for the wildcard
		// itself, which it can only answer with a DNS provider module the
		// caddy:2-alpine image does not carry, and fail at every renewal.
		if strings.HasPrefix(name, "*.") && !route.plain(name) && !servesExplicitCertificate(route) {
			return "", errors.New("a wildcard route on the Caddy ingress serves a certificate issued over a DNS challenge, and none was resolved for " + name)
		}
	}
	// A deployment's route has pages and a maintenance switch, named after
	// it; the nameless route a certificate is issued through has neither.
	pages := ""
	if route.Name != "" {
		var err error
		if pages, err = dockerCaddyPagesDir(route.Name); err != nil {
			return "", err
		}
	} else if len(route.ErrorPages) > 0 || route.InterceptErrors || route.Sleep != nil {
		return "", errors.New("a route's pages are kept under its name")
	}
	directive := `respond "Deployment is preparing" 503`
	if upstream != "" {
		parsed, err := url.Parse(upstream)
		if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("invalid Caddy deployment upstream")
		}
		directive = "reverse_proxy " + strconv.Quote(upstream)
		if route.InterceptErrors {
			directive += renderCaddyInterceptErrors(route.ErrorPages)
		}
		if route.Sleep != nil {
			// A stopped container's address answers nothing at all, and
			// Caddy waits three seconds for it by default before the wake
			// is asked; a running one answers in well under a millisecond.
			directive = withCaddyDialTimeout(directive)
		}
	}
	tlsDirective := ""
	if servesExplicitCertificate(route) {
		hash, err := dockerCaddyCertificateCopy(route.CertPath)
		if err != nil {
			return "", err
		}
		tlsDirective = tlsCopyLine(hash) + "\n"
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
	if err := validateDeploymentRules(route); err != nil {
		return "", err
	}
	// Deferred, so each header is set as the answer is written and replaces
	// the application's own rather than being sent beside it. HSTS is one of
	// them on the HTTPS block alone, and the plain block drops the
	// application's own: the control owns the header on the route, a
	// browser ignores it over plain HTTP anyway, and a plain name promising
	// HTTPS it does not serve would be a lie.
	headerDirective := func(https bool) string {
		lines := []string{}
		if route.HSTS != nil {
			if https {
				lines = append(lines, "    Strict-Transport-Security "+strconv.Quote(route.HSTS.Value()))
			} else {
				lines = append(lines, "    -Strict-Transport-Security")
			}
		}
		for _, header := range route.ResponseHeaders {
			lines = append(lines, "    "+header.Name+" "+strconv.Quote(header.Value))
		}
		if len(lines) == 0 {
			return ""
		}
		lines = append(lines, "    defer")
		return "  header {\n" + strings.Join(lines, "\n") + "\n  }\n"
	}
	gateDirective := ""
	if route.ForwardAuth != nil {
		var err error
		if gateDirective, err = renderCaddyForwardAuth(*route.ForwardAuth); err != nil {
			return "", err
		}
	}
	// Caddy orders a site's directives itself and puts basic_auth before
	// respond, which would ask a refused address for the password first,
	// and a header it removes after forward_auth has set it. A route block
	// runs in the order written: the fence first, then maintenance, then the
	// password or the sign-in, then the application. nginx decides in the
	// same order, so a refused address is refused whether or not the route
	// is in maintenance, and nobody is asked to sign in to read it.
	fence := ""
	if len(route.AllowFrom) > 0 {
		fence = "  @outside not remote_ip " + strings.Join(route.AllowFrom, " ") + "\n" +
			"  respond @outside \"Forbidden\" 403\n"
	}
	maintenance, errorPages, sleep := "", "", ""
	if pages != "" {
		wake := ""
		if route.Sleep != nil && upstream != "" {
			// After every check, so only a request the route would hand to
			// the application can wake it.
			sleep, wake = renderCaddySleep(route, pages), renderCaddyWakeErrors(route, pages)
		}
		maintenance, errorPages = renderCaddyMaintenance(route.Maintenance, pages), renderCaddyErrorPages(route.ErrorPages, pages, wake)
	}
	handling := fence + maintenance + authDirective + gateDirective + bodyDirective + sleep + "  " + directive + "\n"
	if fence != "" || maintenance != "" || route.ForwardAuth != nil {
		handling = "  route {\n" + indentCaddy(handling) + "  }\n"
	}
	// The names served over HTTPS are one site block and the names served
	// over plain HTTP another, the same handling in each: Caddy refuses a
	// `tls` directive in a block with an http:// address, and the plain
	// names have no certificate to serve. A route without TLS is the plain
	// block alone, as it always was.
	block := func(names []string, https bool) string {
		addresses := append([]string(nil), names...)
		sort.Strings(addresses)
		content := ""
		if https {
			content = tlsDirective
		} else {
			for index := range addresses {
				addresses[index] = "http://" + addresses[index]
			}
		}
		return strings.Join(addresses, ", ") + " {\n" + content + logDirective + headerDirective(https) + handling + errorPages + "}\n"
	}
	tlsServed, plainServed := route.servedByScheme()
	out := "# Managed by Just Dashboard\n"
	if len(tlsServed) > 0 {
		out += block(tlsServed, true)
	}
	if len(plainServed) > 0 {
		if len(tlsServed) > 0 {
			out += "\n"
		}
		out += block(plainServed, false)
	}
	redirects := append([]HostRedirect(nil), route.Redirects...)
	sort.Slice(redirects, func(i, j int) bool { return redirects[i].From < redirects[j].From })
	for _, redirect := range redirects {
		from, to := strings.ToLower(redirect.From), strings.ToLower(redirect.To)
		if !certDomainRe.MatchString(from) {
			return "", errors.New("invalid managed Caddy domain")
		}
		// The redirecting name gets the same certificate handling as the
		// route and nothing else: it serves no application, so it has no
		// password, fence or request record of its own. A plain name
		// answers on port 80 alone, and the visitor is sent on over the
		// scheme the domain is served on.
		tls := ""
		if route.plain(from) {
			from = "http://" + from
		} else {
			tls = tlsDirective
		}
		scheme := "https://"
		if route.plain(to) {
			scheme = "http://"
		}
		out += "\n" + from + " {\n" + tls +
			"  redir " + scheme + to + "{uri} " + strconv.Itoa(redirect.HostRedirectCode()) + "\n}\n"
	}
	return out, nil
}

// renderCaddyForwardAuth asks the auth server what nginx's auth_request asks
// it on a Proxy site, so one check address answers on either proxy: the
// address the visitor asked for as X-Original-URL, the server's 2xx letting
// the request through with the user copied into Remote-User and
// Remote-Email, its 401 sending the visitor to sign in with rd= set, and
// anything else — Caddy's own 502 when the server does not answer included —
// refusing it. Host is the auth server's own, as nginx's proxy_pass sends.
//
// The two headers are removed from what the visitor sent before the check.
// Caddy 2.11's forward_auth drops them itself when the server sends none;
// removing them first holds whichever Caddy the operator's ingress runs.
func renderCaddyForwardAuth(gate SiteForwardAuth) (string, error) {
	verify, err := url.Parse(gate.Verify)
	if err != nil {
		return "", err
	}
	if verify.Hostname() == "localhost" || (net.ParseIP(verify.Hostname()) != nil && net.ParseIP(verify.Hostname()).IsLoopback()) {
		// Caddy's loopback is its own container's. Activation swaps in the
		// address of the container publishing the port.
		return "", errors.New("Caddy reaches an auth server on this host through the container that publishes it")
	}
	path := verify.EscapedPath()
	if path == "" {
		path = "/"
	}
	from := forwardAuthResponseHeaders[gate.Provider]
	copies := make([]string, 0, len(forwardAuthHeaders))
	for index, to := range forwardAuthHeaders {
		if strings.EqualFold(from[index], to) {
			copies = append(copies, to)
		} else {
			copies = append(copies, from[index]+">"+to)
		}
	}
	separator := "?"
	if strings.Contains(gate.SignIn, "?") {
		separator = "&"
	}
	var out strings.Builder
	for _, header := range forwardAuthHeaders {
		out.WriteString("  request_header -" + header + "\n")
	}
	out.WriteString("  forward_auth " + strconv.Quote(verify.Scheme+"://"+verify.Host) + " {\n")
	out.WriteString("    uri " + strconv.Quote(path) + "\n")
	out.WriteString("    header_up Host {upstream_hostport}\n")
	out.WriteString("    header_up X-Original-URL {scheme}://{hostport}{uri}\n")
	out.WriteString("    header_up X-Original-Method {method}\n")
	out.WriteString("    copy_headers " + strings.Join(copies, " ") + "\n")
	out.WriteString("    @signin status 401\n")
	out.WriteString("    handle_response @signin {\n")
	out.WriteString("      redir " + strconv.Quote(gate.SignIn+separator+"rd={scheme}://{hostport}{uri}") + " 302\n")
	out.WriteString("    }\n")
	out.WriteString("  }\n")
	return out.String(), nil
}

// dockerCaddyPagesDir is where a route's maintenance and error pages live
// inside the ingress, and its maintenance switch beside them: the ingress's
// persistent /config, named after the route the way its request record is.
func dockerCaddyPagesDir(name string) (string, error) {
	if _, err := dockerCaddyRoutePath(name); err != nil {
		return "", err
	}
	return dockerCaddyRoot + "/pages/" + strings.TrimSuffix(name, ".conf"), nil
}

// renderCaddyMaintenance is the switch every route carries. Caddy looks for
// the file on every request, as nginx does, so turning maintenance on or off
// is the file and nothing else: no reload and no rewrite of the route. The
// ACME challenge is let through, as on nginx, though Caddy answers its own
// before any route sees it.
func renderCaddyMaintenance(m DeploymentMaintenance, pages string) string {
	var out strings.Builder
	out.WriteString("  @jd_maintenance {\n")
	out.WriteString("    file {\n      root " + pages + "\n      try_files /" + maintenanceSwitchFile + "\n    }\n")
	if len(m.BypassFrom) > 0 {
		out.WriteString("    not remote_ip " + strings.Join(m.BypassFrom, " ") + "\n")
	}
	out.WriteString("    not path " + acmeChallengePath + "*\n")
	out.WriteString("  }\n")
	out.WriteString("  route @jd_maintenance {\n")
	if m.RetryAfter > 0 {
		out.WriteString("    header Retry-After " + strconv.Itoa(m.RetryAfter) + "\n")
	}
	out.WriteString("    root * " + pages + "\n")
	out.WriteString("    rewrite * /maintenance.html\n")
	out.WriteString("    file_server {\n      status 503\n    }\n")
	out.WriteString("  }\n")
	return out.String()
}

// renderCaddyErrorPages answers the route's chosen codes with its own pages.
// A file served while Caddy handles an error keeps the error's status, so a
// 502 is still a 502. Every other error keeps Caddy's own answer.
//
// wake, when the route's application may sleep, comes first: the
// application not answering is woken before any page is chosen for it.
func renderCaddyErrorPages(codes []int, pages, wake string) string {
	if len(codes) == 0 {
		if wake == "" {
			return ""
		}
		return "  handle_errors {\n" + wake + "  }\n"
	}
	sorted := slices.Sorted(slices.Values(codes))
	list := make([]string, 0, len(sorted))
	for _, code := range sorted {
		list = append(list, strconv.Itoa(code))
	}
	return "  handle_errors {\n" + wake +
		"    @jd_page expression `{err.status_code} in [" + strings.Join(list, ", ") + "]`\n" +
		"    handle @jd_page {\n" +
		"      root * " + pages + "\n" +
		"      rewrite * /{err.status_code}.html\n" +
		"      file_server\n" +
		"    }\n" +
		"  }\n"
}

// renderCaddyInterceptErrors turns the application's own answers with the
// route's chosen codes into Caddy's errors, which its pages then answer, as
// nginx's proxy_intercept_errors does. The status is the application's.
func renderCaddyInterceptErrors(codes []int) string {
	sorted := slices.Sorted(slices.Values(codes))
	list := make([]string, 0, len(sorted))
	for _, code := range sorted {
		list = append(list, strconv.Itoa(code))
	}
	return " {\n" +
		"    @jd_error status " + strings.Join(list, " ") + "\n" +
		"    handle_response @jd_error {\n" +
		"      error \"the application's error page was replaced\" {rp.status_code}\n" +
		"    }\n" +
		"  }"
}

// indentCaddy moves every line of a block one level in.
func indentCaddy(block string) string {
	lines := strings.SplitAfter(block, "\n")
	for index, line := range lines {
		if line != "" {
			lines[index] = "  " + line
		}
	}
	return strings.Join(lines, "")
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
	return DeploymentRouteSnapshot{Version: 1, Driver: c.driver(), ContainerID: c.ID, IngressIdentity: c.Identity, Name: name, Path: path, Existed: exists, Content: content, ContentDigest: routeDigest(content), Mode: 0600}, nil
}

func (c *dockerCaddy) restore(ctx context.Context, snapshot DeploymentRouteSnapshot) error {
	path, err := dockerCaddyRoutePath(snapshot.Name)
	if err != nil || snapshot.Version != 1 || snapshot.Driver != c.driver() || path != snapshot.Path || snapshot.ContentDigest != routeDigest(snapshot.Content) || (snapshot.ContainerID != c.ID && (snapshot.IngressIdentity == "" || snapshot.IngressIdentity != c.Identity)) {
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
	return c.findTarget(ctx, upstream, false)
}

// findTarget is target, and with everywhere it also takes a port published
// on every interface, which answers on loopback as well. The application's
// publication is the dashboard's own and always on loopback; an auth server
// is the operator's, and is as often published as 9091:9091.
func (c *dockerCaddy) findTarget(ctx context.Context, upstream string, everywhere bool) (dockerCaddyTarget, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || net.ParseIP(parsed.Hostname()) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() || parsed.Port() == "" {
		return dockerCaddyTarget{}, errors.New("Caddy requires a private deployment upstream")
	}
	containers, err := ingressContainers(ctx)
	if err != nil {
		return dockerCaddyTarget{}, err
	}
	if target, ok := publishedTarget(containers, parsed.Hostname(), parsed.Port(), everywhere); ok {
		return target, nil
	}
	return dockerCaddyTarget{}, errors.New("Caddy ingress requires a running Docker candidate with a matching loopback publication")
}

// publishedTarget is the container publishing host:port, and its address on
// the first of its networks by name.
func publishedTarget(containers []ingressContainer, host, port string, everywhere bool) (dockerCaddyTarget, bool) {
	for _, candidate := range containers {
		for target, bindings := range candidate.NetworkSettings.Ports {
			if !strings.HasSuffix(target, "/tcp") {
				continue
			}
			for _, binding := range bindings {
				wildcard := everywhere && (binding.HostIP == "" || binding.HostIP == "0.0.0.0" || binding.HostIP == "::")
				if binding.HostPort != port || (binding.HostIP != host && !wildcard) {
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
						return dockerCaddyTarget{candidate.ID, network, strings.TrimSuffix(target, "/tcp"), address}, true
					}
				}
			}
		}
	}
	return dockerCaddyTarget{}, false
}

// dockerCaddyAuthTargetPrefix carries the auth server a route signs in
// through, when Caddy reaches it the way it reaches the application, so the
// maintenance pass repairs that network too.
const dockerCaddyAuthTargetPrefix = "# Just Dashboard auth target: "

func (target dockerCaddyTarget) authMetadata() string {
	raw, _ := json.Marshal(target)
	return dockerCaddyAuthTargetPrefix + string(raw) + "\n"
}

// routeContent is the route as the ingress holds it, metadata included, and
// the targets Caddy has to share a network with: the application, and the
// auth server when the route signs in through one published on loopback.
// Activation writes it and verification compares against it, so the two
// cannot disagree about what a route with a sign-in looks like.
func (c *dockerCaddy) routeContent(ctx context.Context, route DeploymentRoute) (string, []dockerCaddyTarget, error) {
	target, err := c.target(ctx, route.Upstream)
	if err != nil {
		return "", nil, err
	}
	rendered, auth, err := c.resolveForwardAuth(ctx, route)
	if err != nil {
		return "", nil, err
	}
	content, err := renderDockerCaddyRoute(rendered, target.upstream())
	if err != nil {
		return "", nil, err
	}
	content += target.metadata()
	targets := []dockerCaddyTarget{target}
	if auth != nil {
		content += auth.authMetadata()
		targets = append(targets, *auth)
	}
	// Where the copy the route serves came from, so a renewal of that
	// lineage refreshes it (docker_caddy_copies.go).
	if source, ok := certificateSourceFor(route); ok {
		content += source.metadata()
	}
	return content, targets, nil
}

// resolveForwardAuth points a route's sign-in check at the container that
// publishes it on this host's loopback, the way the application's upstream
// is found: Caddy's own loopback is its container's, where nothing answers.
// A check address anywhere else — a name or address Caddy can reach — is
// asked as it is.
func (c *dockerCaddy) resolveForwardAuth(ctx context.Context, route DeploymentRoute) (DeploymentRoute, *dockerCaddyTarget, error) {
	gate := route.ForwardAuth
	if gate == nil {
		return route, nil, nil
	}
	verify, err := url.Parse(gate.Verify)
	if err != nil {
		return route, nil, err
	}
	host := verify.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return route, nil, nil
	}
	if verify.Scheme != "http" || verify.Port() == "" {
		return route, nil, fmt.Errorf("Caddy reaches an auth server on this host through the container that publishes its port; %s has to be http:// with a port", gate.Verify)
	}
	origin := "http://" + net.JoinHostPort(host, verify.Port())
	target, err := c.findTarget(ctx, origin, true)
	if err != nil {
		return route, nil, fmt.Errorf("no running container publishes the auth server at %s for Caddy to reach: %w", origin, err)
	}
	resolved := *gate
	resolved.Verify = target.upstream() + verify.EscapedPath()
	route.ForwardAuth = &resolved
	return route, &target, nil
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
	// A site claims the name it is written for, not the names under its
	// wildcard: Caddy answers a request from its most specific site, as
	// nginx does from an exact server_name before a wildcard's, so a
	// preview's own route lives beside the wildcard route one label above it.
	claimed := false
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if hosts, ok := node["host"].([]any); ok {
				for _, host := range hosts {
					if name, ok := host.(string); ok && strings.EqualFold(name, domain) {
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
	content, targets, err := c.routeContent(ctx, route)
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
	if err := c.ensurePages(ctx, route); err != nil {
		return result, fmt.Errorf("the route's pages could not be written: %w", err)
	}
	if servesExplicitCertificate(route) {
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
		certPath, keyPath := certificateCopyPaths(certificateCopyHash(cert))
		if err := c.write(ctx, certPath, string(cert)); err != nil {
			return result, err
		}
		if err := c.write(ctx, keyPath, string(key)); err != nil {
			return result, err
		}
	}
	for _, target := range targets {
		if _, err := c.connectTarget(ctx, target); err != nil {
			return result, err
		}
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

// ensureDockerCaddyCertificate has Caddy obtain a certificate for each name.
// Caddy orders and renews one certificate per name rather than one covering
// them all, so a route with a second name — the www. that redirects to the
// bare domain — is each name issued in turn. The first name's copy is the
// release's evidence; the route itself never reads it, because Caddy serves
// the certificates it holds.
func (s *Service) ensureDockerCaddyCertificate(ctx context.Context, c *dockerCaddy, names []string, log func(string, string)) (DeploymentCertificate, error) {
	if len(names) == 0 {
		return DeploymentCertificate{}, errors.New("a TLS deployment route needs at least one domain")
	}
	var evidence DeploymentCertificate
	outcome := CertificateReused
	for index, name := range names {
		got, err := s.ensureDockerCaddyName(ctx, c, name, log)
		if err != nil {
			return DeploymentCertificate{}, err
		}
		if got.Outcome == CertificateIssued {
			outcome = CertificateIssued
		}
		if index == 0 {
			evidence = got
		}
	}
	evidence.Outcome, evidence.Domains = outcome, names
	return evidence, nil
}

func (s *Service) ensureDockerCaddyName(ctx context.Context, c *dockerCaddy, hostname string, log func(string, string)) (DeploymentCertificate, error) {
	names := []string{hostname}
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
			imported, err := keepCaddyEvidence("caddy-"+strings.TrimPrefix(routeDigest(names[0]), "sha256:")[:24], certificate, key)
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

// dockerCaddyCertificateCopy names the copy of the certificate at path, by
// the hash of its bytes. Immutable, content-addressed copies keep route
// rollback independent of a newer certificate at the same host path — a
// rotated import, a renewed lineage — overwriting the pair an environment's
// previous route snapshot names; a renewal gets a copy of its own and the
// routes are moved to it (docker_caddy_copies.go).
func dockerCaddyCertificateCopy(path string) (string, error) {
	cert, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return certificateCopyHash(cert), nil
}

func dockerCaddyCertificatePaths(path string) (string, string, error) {
	hash, err := dockerCaddyCertificateCopy(path)
	if err != nil {
		return "", "", err
	}
	certPath, keyPath := certificateCopyPaths(hash)
	return certPath, keyPath, nil
}
