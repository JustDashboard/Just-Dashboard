package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The front of every tailnet address, and the gate of a tailnet-only
// deployment that names who may open it.
//
// tailscaled publishes a pull request preview or a tailnet-only deployment
// on the host's tailnet address and forwards one port to loopback, to this:
// a route that answers on 127.0.0.1 alone, on the same port number, and
// forwards to the container. It is there for every tailnet address, because
// that address is a port of the node's MagicDNS name, which is the name the
// dashboard itself is reached by: a browser signed in to the dashboard sends
// its session cookie to every port of it, and the front takes the
// dashboard's cookies out of the request before the application sees it
// (dashboard_cookie.go). Without a list that is all it does, and the tailnet
// is the fence. With one it is the gate as well, and asks the dashboard
// about every request before the application sees it — nginx's
// auth_request, or Caddy's forward_auth, the same mechanism a public route's
// sign-in uses. The dashboard asks tailscaled who holds the tailnet address
// the request came from (internal/tailnetgate) and answers 204 with the
// login, 403, or 503 when tailscaled cannot be asked; the application is told
// X-Tailnet-Login and X-Tailnet-User, and never what the visitor claimed.
//
// Which proxy holds the route is decided per host. Where this host's
// deployment routes are host nginx's, nginx does: it already listens on the
// host's loopback and reaches the dashboard there. The Docker Caddy ingress
// cannot: it runs in a network of its own, where neither tailscaled's
// forward nor the dashboard's loopback API reaches, and it publishes only 80
// and 443. So everywhere else the gate is a Caddy of the dashboard's own,
// just-dashboard-tailnet, on the host's network with every site bound to
// 127.0.0.1, its admin API on a socket inside its own volume, and nothing
// published: it binds no routable address, so invariant 7 holds, and it is
// started the first time a deployment needs it rather than asked of the
// operator.

// TailnetGateRoute is one environment's front: its gate when Check is set.
type TailnetGateRoute struct {
	// Name is the environment's route name, just-dashboard-env-<id>.conf.
	Name string `json:"name"`
	// Listen is the loopback port tailscale serve forwards to, the number
	// of the tailnet port itself.
	Listen int `json:"listen"`
	// Upstream is the application's loopback publication.
	Upstream string `json:"upstream"`
	// Check is the dashboard's whois route for this environment,
	// http://127.0.0.1:<port>/api/v1/tailnet/whois?environment=<id>, for
	// an environment that names who may open it. Empty is a front that
	// checks nobody: the tailnet is the fence.
	Check string `json:"check,omitempty"`
	// MaxBodyMB is the request-body limit; zero is nginx's deployment
	// default and no limit on Caddy, as on a public route.
	MaxBodyMB int `json:"maxBodyMb,omitempty"`
}

const (
	// TailnetGateDriver is the snapshot's driver for a route the gate Caddy
	// holds.
	TailnetGateDriver = "tailnet-caddy"
	// TailnetGateDir holds the gate Caddy's Caddyfile on the host; an
	// uninstall that removes the gate removes it too.
	TailnetGateDir = "/etc/just-dashboard/tailnet"
	// TailnetGateContainer is the gate Caddy's name, and TailnetGateLabel
	// the label that says it is the dashboard's.
	TailnetGateContainer = "just-dashboard-tailnet"
	TailnetGateLabel     = "com.just-dashboard.tailnet-gate"
	tailnetGateMarker    = "# Tailnet gate: "
	tailnetCheckURI      = "/__jd/tailnet"
)

// TailnetGateVolumes are the gate Caddy's own storage.
var TailnetGateVolumes = []string{"just-dashboard-tailnet-config", "just-dashboard-tailnet-data"}

// tailnetGateConfig is the gate Caddy's whole Caddyfile. Its admin API is a
// socket in its own volume, where no process on the host's loopback can
// reconfigure it; automatic HTTPS is off because it serves plain HTTP to
// tailscaled alone, which carries the tailnet's own certificate; and
// tailscaled's X-Forwarded-For is trusted from loopback, which is where only
// tailscaled forwards from, so {client_ip} is the tailnet address.
const tailnetGateConfig = "# Managed by Just Dashboard: the loopback-only Caddy in front of\n" +
	"# tailnet-only deployments that name who may open them.\n" +
	"{\n" +
	"\tadmin unix//config/caddy-admin.sock\n" +
	"\tauto_https off\n" +
	"\tservers {\n" +
	"\t\ttrusted_proxies static 127.0.0.1/32 ::1/128\n" +
	"\t}\n" +
	"}\n\n" +
	dockerCaddyImport + "\n"

var (
	tailnetGateNameRe  = regexp.MustCompile(`^just-dashboard-env-([1-9][0-9]{0,17})\.conf$`)
	tailnetGateCheckRe = regexp.MustCompile(`^http://127\.0\.0\.1:[1-9][0-9]{0,4}/api/v1/tailnet/whois\?environment=([1-9][0-9]{0,17})$`)
)

// ValidateTailnetGate checks a front before either proxy writes it: every
// address on loopback, and a gate's check the dashboard's own route for the
// same environment as the route's name.
func ValidateTailnetGate(route TailnetGateRoute) error {
	name := tailnetGateNameRe.FindStringSubmatch(route.Name)
	if name == nil {
		return errors.New("a tailnet gate is named after its environment's route")
	}
	if route.Listen < 1024 || route.Listen > 65535 {
		return fmt.Errorf("a tailnet gate listens on a loopback port above 1023, not %d", route.Listen)
	}
	if _, err := loopbackUpstreamPort(route.Upstream); err != nil {
		return err
	}
	if route.Check != "" {
		check := tailnetGateCheckRe.FindStringSubmatch(route.Check)
		if check == nil {
			return errors.New("a tailnet gate asks the dashboard's own whois route on loopback")
		}
		if check[1] != name[1] {
			return errors.New("a tailnet gate asks about its own environment")
		}
	}
	if route.MaxBodyMB < 0 || route.MaxBodyMB > 1<<20 {
		return errors.New("the request body limit is out of range")
	}
	return nil
}

func loopbackUpstreamPort(upstream string) (int, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.Hostname() != "127.0.0.1" {
		return 0, errors.New("a tailnet gate forwards to the application's publication on 127.0.0.1")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("a tailnet gate forwards to a port")
	}
	return port, nil
}

func tailnetGateIdent(name string) string {
	return "jd_tailnet_e" + tailnetGateNameRe.FindStringSubmatch(name)[1]
}

func tailnetCheckSplit(check string) (origin, uri string) {
	parsed, _ := url.Parse(check)
	return parsed.Host, parsed.RequestURI()
}

// renderNginxTailnetGate writes the front as a host nginx server. accessLog
// and errorLog are the files a public route of the same name would use, so
// the request record reads the same way whichever the environment is.
func renderNginxTailnetGate(route TailnetGateRoute, accessLog, errorLog string) (string, error) {
	if err := ValidateTailnetGate(route); err != nil {
		return "", err
	}
	ident := tailnetGateIdent(route.Name)
	gated := route.Check != ""
	body := route.MaxBodyMB
	if body <= 0 {
		body = DefaultDeploymentMaxBodyMB
	}
	l := &lines{}
	l.add(managedMarker)
	l.add("# Site: %s", route.Name)
	if gated {
		l.add("%sthis deployment exists only on the tailnet. tailscaled", tailnetGateMarker)
		l.add("# forwards its tailnet port here, to loopback alone, and every request is")
		l.add("# checked first: the dashboard asks tailscaled who holds the address it")
		l.add("# came from, and answers whether this deployment admits them.")
	} else {
		l.add("%sthe front of this deployment's tailnet address. tailscaled", tailnetGateMarker)
		l.add("# forwards its tailnet port here, to loopback alone, and everyone on the")
		l.add("# tailnet may open it.")
	}
	l.blank()
	l.add("map $http_upgrade $%s_connection {", ident)
	l.add("    default upgrade;")
	l.add("    ''      close;")
	l.add("}")
	l.blank()
	l.add("# The address is a port of the name the dashboard is reached by, and a")
	l.add("# browser sends a cookie to every port of its name: the dashboard's session")
	l.add("# is taken out of what the application is sent, every other cookie kept.")
	cookie := nginxDropCookieMaps(l, ident, DashboardCookies())
	l.blank()
	if gated {
		l.add("log_format %s '$remote_addr - $remote_user [$time_local] \"$request\" $status $body_bytes_sent \"$http_referer\" \"$http_user_agent\" rt=$request_time urt=\"$upstream_response_time\" host=$host tl=$%s_login';", ident, ident)
	} else {
		l.add("log_format %s '$remote_addr - $remote_user [$time_local] \"$request\" $status $body_bytes_sent \"$http_referer\" \"$http_user_agent\" rt=$request_time urt=\"$upstream_response_time\" host=$host';", ident)
	}
	l.blank()
	l.add("server {")
	l.add("    listen 127.0.0.1:%d;", route.Listen)
	l.blank()
	l.add("    access_log %s %s;", accessLog, ident)
	l.add("    error_log %s;", errorLog)
	l.blank()
	l.add("    # Only tailscaled forwards here, and it says in X-Forwarded-For which")
	l.add("    # tailnet address it forwarded, having dropped whatever the visitor sent.")
	l.add("    set_real_ip_from 127.0.0.1;")
	l.add("    set_real_ip_from ::1;")
	l.add("    real_ip_header X-Forwarded-For;")
	l.blank()
	l.add("    client_max_body_size %dm;", body)
	l.blank()
	if gated {
		l.add("    # 204 lets the request through; 403 refuses it; anything else, the")
		l.add("    # dashboard or tailscaled not answering included, is a 503.")
		l.add("    auth_request %s;", tailnetCheckURI)
		l.add("    auth_request_set $%s_login $upstream_http_x_tailnet_login;", ident)
		l.add("    auth_request_set $%s_user $upstream_http_x_tailnet_user;", ident)
		l.add("    error_page 403 = @jd_tailnet_refused;")
		l.add("    error_page 500 = @jd_tailnet_unavailable;")
		l.blank()
	}
	l.add("    location / {")
	l.add("        proxy_pass %s;", route.Upstream)
	l.add("        proxy_http_version 1.1;")
	l.add("        proxy_set_header Host              $http_host;")
	l.add("        proxy_set_header Upgrade           $http_upgrade;")
	l.add("        proxy_set_header Connection        $%s_connection;", ident)
	l.add("        proxy_set_header X-Real-IP         $remote_addr;")
	l.add("        proxy_set_header X-Forwarded-For   $remote_addr;")
	l.add("        proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;")
	l.add("        proxy_set_header X-Forwarded-Host  $http_x_forwarded_host;")
	l.add("        proxy_set_header Cookie            %s;", cookie)
	if gated {
		l.add("        # Who the dashboard says this is, and never what the visitor sent.")
		l.add("        proxy_set_header X-Tailnet-Login   $%s_login;", ident)
		l.add("        proxy_set_header X-Tailnet-User    $%s_user;", ident)
	} else {
		l.add("        # Nobody was asked who this is, so nobody is named, whatever the")
		l.add("        # visitor claimed.")
		l.add("        proxy_set_header X-Tailnet-Login   \"\";")
		l.add("        proxy_set_header X-Tailnet-User    \"\";")
	}
	l.add("        proxy_read_timeout 3600s;")
	l.add("        proxy_send_timeout 3600s;")
	l.add("    }")
	if gated {
		l.blank()
		origin, uri := tailnetCheckSplit(route.Check)
		l.add("    # Internal, so a visitor cannot ask it; the visitor's credentials and")
		l.add("    # the forwarding headers stay behind, and the address goes as its own")
		l.add("    # header, so the dashboard's allowlist judges this loopback caller.")
		l.add("    location = %s {", tailnetCheckURI)
		l.add("        internal;")
		l.add("        proxy_pass http://%s%s;", origin, uri)
		l.add("        proxy_pass_request_body off;")
		l.add("        proxy_set_header Content-Length  \"\";")
		l.add("        proxy_set_header X-Tailnet-Peer  $remote_addr;")
		l.add("        proxy_set_header X-Forwarded-For \"\";")
		l.add("        proxy_set_header X-Real-IP       \"\";")
		l.add("        proxy_set_header Forwarded       \"\";")
		l.add("        proxy_set_header Cookie          \"\";")
		l.add("        proxy_set_header Authorization   \"\";")
		l.add("    }")
		l.blank()
		l.add("    location @jd_tailnet_refused {")
		l.add("        default_type text/plain;")
		l.add("        return 403 \"Your tailnet login is not one this deployment admits.\\n\";")
		l.add("    }")
		l.blank()
		l.add("    location @jd_tailnet_unavailable {")
		l.add("        default_type text/plain;")
		l.add("        return 503 \"Who you are on the tailnet could not be checked just now. Try again in a moment.\\n\";")
		l.add("    }")
	}
	l.add("}")
	return l.String(), nil
}

// renderCaddyTailnetGate writes the front as a site of the gate Caddy, which
// shares the host's network: bound to 127.0.0.1 on the tailnet port's number,
// asking the dashboard's loopback API with forward_auth when it is a gate.
func renderCaddyTailnetGate(route TailnetGateRoute) (string, error) {
	if err := ValidateTailnetGate(route); err != nil {
		return "", err
	}
	logDirective, err := renderAccessLogDirective(route.Name)
	if err != nil {
		return "", err
	}
	gated := route.Check != ""
	var out strings.Builder
	out.WriteString("# Managed by Just Dashboard\n")
	if gated {
		out.WriteString(tailnetGateMarker + route.Name + " answers on loopback alone, where\n")
		out.WriteString("# tailscaled forwards the deployment's tailnet port, and asks the dashboard\n")
		out.WriteString("# who holds the address of every request before the application sees it.\n")
	} else {
		out.WriteString(tailnetGateMarker + route.Name + " answers on loopback alone, where\n")
		out.WriteString("# tailscaled forwards the deployment's tailnet port, and lets everyone on\n")
		out.WriteString("# the tailnet in.\n")
	}
	out.WriteString("# The address is a port of the name the dashboard is reached by, so the\n")
	out.WriteString("# dashboard's session cookie is taken out of what the application is sent.\n")
	out.WriteString("http://:" + strconv.Itoa(route.Listen) + " {\n")
	out.WriteString("  bind 127.0.0.1\n")
	out.WriteString(logDirective)
	if route.MaxBodyMB > 0 {
		out.WriteString("  request_body {\n    max_size " + strconv.Itoa(route.MaxBodyMB) + "MB\n  }\n")
	}
	out.WriteString("  route {\n")
	out.WriteString("    request_header -X-Tailnet-Login\n")
	out.WriteString("    request_header -X-Tailnet-User\n")
	if gated {
		origin, uri := tailnetCheckSplit(route.Check)
		out.WriteString("    forward_auth " + strconv.Quote(origin) + " {\n")
		out.WriteString("      uri " + strconv.Quote(uri) + "\n")
		out.WriteString("      header_up X-Tailnet-Peer {client_ip}\n")
		for _, header := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP", "Forwarded", "Cookie", "Authorization"} {
			out.WriteString("      header_up -" + header + "\n")
		}
		out.WriteString("      copy_headers X-Tailnet-Login X-Tailnet-User\n")
		out.WriteString("    }\n")
		// Set only once the check let the request through, so an error after
		// it — the application down — keeps its own status, and one before it
		// — the dashboard not answering — is the gate's 503.
		out.WriteString("    vars jd_tailnet_checked yes\n")
		out.WriteString("    log_append tailnet_login {http.request.header.X-Tailnet-Login}\n")
	}
	out.WriteString("    reverse_proxy " + strconv.Quote(strings.TrimPrefix(route.Upstream, "http://")) + " {\n")
	out.WriteString("      header_up X-Forwarded-For {client_ip}\n")
	out.WriteString("      header_up X-Real-IP {client_ip}\n")
	out.WriteString(caddyDropCookies("      ", DashboardCookies()))
	out.WriteString("    }\n")
	out.WriteString("  }\n")
	if gated {
		out.WriteString("  handle_errors {\n")
		out.WriteString("    @jd_tailnet_unchecked expression `{http.vars.jd_tailnet_checked} != \"yes\"`\n")
		out.WriteString("    respond @jd_tailnet_unchecked \"Who you are on the tailnet could not be checked just now. Try again in a moment.\" 503\n")
		out.WriteString("  }\n")
	}
	out.WriteString("}\n")
	return out.String(), nil
}

// tailnetGateOnNginx says whether this host's gate is its nginx: nginx is
// what serves this host's deployment routes — no Docker Caddy ingress found,
// named or about to be started — and it is installed. An isolated service
// (New, without Docker ingress) is nginx's, as its public routes are.
func (s *Service) tailnetGateOnNginx(ctx context.Context) (bool, error) {
	if !s.dockerIngress {
		return true, nil
	}
	if s.ingressContainer != "" {
		return false, nil
	}
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return false, err
	}
	if edge != nil || !hostexec.Available("nginx") || s.canProvisionIngress(ctx) {
		return false, nil
	}
	return true, nil
}

func (s *Service) gateContainerName() string {
	if s.tailnetGateName != "" {
		return s.tailnetGateName
	}
	return TailnetGateContainer
}

func (s *Service) gateSource() string {
	if s.tailnetGateSource != "" {
		return s.tailnetGateSource
	}
	return filepath.Join(TailnetGateDir, "Caddyfile")
}

// gateContainer is the gate Caddy, running, or nil when there is none.
func (s *Service) gateContainer(ctx context.Context) (*dockerCaddy, error) {
	if !hostexec.Available("docker") {
		return nil, nil
	}
	raw, err := hostexec.Command(ctx, "docker", "inspect", "--type", "container", s.gateContainerName()).Output()
	if err != nil {
		return nil, nil
	}
	var containers []struct {
		ingressContainer
		Config struct {
			Cmd    []string
			Labels map[string]string
		}
		HostConfig struct{ NetworkMode string }
	}
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return nil, errors.New("could not read the tailnet gate's container")
	}
	c := containers[0]
	if c.Config.Labels[TailnetGateLabel] != "true" {
		return nil, fmt.Errorf("a container named %s exists and is not the dashboard's tailnet gate", s.gateContainerName())
	}
	if c.HostConfig.NetworkMode != "host" {
		return nil, fmt.Errorf("the tailnet gate %s is not on the host's network", s.gateContainerName())
	}
	if !c.State.Running {
		return nil, nil
	}
	c.ingressContainer.Config.Cmd = c.Config.Cmd
	if !runsCaddyfile(c.ingressContainer) {
		return nil, fmt.Errorf("the tailnet gate %s is not running Caddy over its Caddyfile", s.gateContainerName())
	}
	edge, err := dockerCaddyFrom(c.ingressContainer)
	if err != nil {
		return nil, err
	}
	edge.Driver = TailnetGateDriver
	return edge, nil
}

// ensureGateContainer starts the gate Caddy when it is not running: from its
// stopped container when there is one, else a new one over the persisted
// Caddyfile and its two volumes. Called under s.mu.
func (s *Service) ensureGateContainer(ctx context.Context) (*dockerCaddy, error) {
	if edge, err := s.gateContainer(ctx); err != nil || edge != nil {
		return edge, err
	}
	if !hostexec.Available("docker") {
		return nil, errors.New("the tailnet gate runs in Docker, which is not available")
	}
	source := s.gateSource()
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		_, writeErr := file.WriteString(tailnetGateConfig)
		err = errors.Join(writeErr, file.Sync(), file.Close())
	} else if errors.Is(err, os.ErrExist) {
		raw, readErr := os.ReadFile(source)
		err = readErr
		if err == nil && string(raw) != tailnetGateConfig {
			return nil, fmt.Errorf("%s is not the tailnet gate's configuration, so it was left alone", source)
		}
	}
	if err != nil {
		return nil, err
	}
	name := s.gateContainerName()
	configVolume, dataVolume := name+"-config", name+"-data"
	if _, err := hostexec.Command(ctx, "docker", "inspect", "--type", "container", name).Output(); err == nil {
		if _, err := hostexec.Command(ctx, "docker", "start", name).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("the tailnet gate %s could not be started: %w", name, err)
		}
	} else {
		for _, volume := range []string{configVolume, dataVolume} {
			if out, err := hostexec.Command(ctx, "docker", "volume", "create", "--label", TailnetGateLabel+"=true", volume).CombinedOutput(); err != nil {
				return nil, fmt.Errorf("the tailnet gate's volume %s could not be created: %s", volume, strings.TrimSpace(string(out)))
			}
		}
		// The host's network, so 127.0.0.1 is the host's loopback, where
		// tailscaled forwards and the dashboard answers; nothing published,
		// and every site binds loopback alone.
		out, err := hostexec.Command(ctx, "docker", "run", "-d", "--name", name,
			"--label", TailnetGateLabel+"=true", "--restart", "unless-stopped", "--network", "host",
			"-v", source+":/etc/caddy/Caddyfile:ro",
			"-v", configVolume+":/config", "-v", dataVolume+":/data",
			"caddy:2-alpine").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("the tailnet gate could not be started: %s", strings.TrimSpace(string(out)))
		}
	}
	ready, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		edge, err := s.gateContainer(ready)
		if err == nil && edge != nil {
			if _, err := edge.command(ready, "", "test", "-S", "/config/caddy-admin.sock"); err == nil {
				return edge, nil
			}
		}
		select {
		case <-ready.Done():
			return nil, errors.New("the tailnet gate did not become ready")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ApplyTailnetGate writes the environment's gate on this host's gate proxy,
// reloads it and checks it holds what was written; a failure puts the
// previous bytes back, as a public route's cutover does.
func (s *Service) ApplyTailnetGate(ctx context.Context, route TailnetGateRoute) (DeploymentRouteResult, error) {
	if err := ValidateTailnetGate(route); err != nil {
		return DeploymentRouteResult{}, err
	}
	onNginx, err := s.tailnetGateOnNginx(ctx)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	if onNginx {
		return s.applyNginxTailnetGate(ctx, route)
	}
	s.mu.Lock()
	edge, err := s.ensureGateContainer(ctx)
	s.mu.Unlock()
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	return s.applyCaddyTailnetGate(ctx, edge, route)
}

func (s *Service) applyNginxTailnetGate(ctx context.Context, route TailnetGateRoute) (DeploymentRouteResult, error) {
	content, err := renderNginxTailnetGate(route, nginxAccessLogPath(route.Name), nginxErrorLogPath(route.Name))
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotDeploymentRouteLocked(route.Name)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	result := DeploymentRouteResult{Snapshot: snapshot}
	if !strings.Contains(snapshot.Content, fmt.Sprintf("    listen 127.0.0.1:%d;\n", route.Listen)) {
		if err := loopbackPortFree(route.Listen); err != nil {
			return result, err
		}
	}
	spec := &SiteSpec{Name: route.Name, Kind: "proxy", Upstream: route.Upstream, Domains: []string{}, Locations: []SiteLocation{}}
	if _, err := s.applySiteLocked(ctx, spec, content, true, true, true); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		restoreErr := s.restoreDeploymentRouteLocked(recoveryCtx, snapshot)
		cancel()
		result.Recovered = restoreErr == nil
		if restoreErr != nil {
			return result, fmt.Errorf("%w: apply: %v; restore: %v", ErrRouteRecovery, err, restoreErr)
		}
		return result, err
	}
	result.Applied = true
	result.Verified = s.routeMatchesLocked(route.Name, content, true)
	if !result.Verified {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		restoreErr := s.restoreDeploymentRouteLocked(recoveryCtx, snapshot)
		cancel()
		result.Recovered = restoreErr == nil
		return result, errors.Join(errors.New("the tailnet gate did not match what was written"), restoreErr)
	}
	return result, nil
}

func (s *Service) applyCaddyTailnetGate(ctx context.Context, c *dockerCaddy, route TailnetGateRoute) (DeploymentRouteResult, error) {
	content, err := renderCaddyTailnetGate(route)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := c.snapshot(ctx, route.Name)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	result := DeploymentRouteResult{Snapshot: snapshot}
	if snapshot.Existed && !strings.HasPrefix(snapshot.Content, "# Managed by Just Dashboard\n") {
		return result, errors.New("the tailnet gate's route file is owned by another configuration")
	}
	if !strings.Contains(snapshot.Content, "http://:"+strconv.Itoa(route.Listen)+" {\n") {
		if err := loopbackPortFree(route.Listen); err != nil {
			return result, err
		}
	}
	if err := c.ensureAccessLogDir(ctx); err != nil {
		return result, err
	}
	if _, err := c.command(ctx, "", "mkdir", "-p", dockerCaddyRoot+"/routes"); err != nil {
		return result, err
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

// loopbackPortFree checks nothing else answers on the gate's port before a
// proxy is told to bind it: nginx reloads without saying so when a new
// listen cannot bind, and the release would go live behind a port that
// answers somebody else.
func loopbackPortFree(port int) error {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("127.0.0.1:%d is already in use on this host, so the tailnet gate cannot answer there: %w", port, err)
	}
	return listener.Close()
}

// VerifyTailnetGate checks the gate still holds what activation wrote.
func (s *Service) VerifyTailnetGate(ctx context.Context, route TailnetGateRoute) error {
	onNginx, err := s.tailnetGateOnNginx(ctx)
	if err != nil {
		return err
	}
	if onNginx {
		content, err := renderNginxTailnetGate(route, nginxAccessLogPath(route.Name), nginxErrorLogPath(route.Name))
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.routeMatchesLocked(route.Name, content, true) {
			return errors.New("the tailnet gate does not match what activation wrote")
		}
		return nil
	}
	edge, err := s.gateContainer(ctx)
	if err != nil {
		return err
	}
	if edge == nil {
		return errors.New("the tailnet gate is not running")
	}
	want, err := renderCaddyTailnetGate(route)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := edge.snapshot(ctx, route.Name)
	if err != nil {
		return err
	}
	if !snapshot.Existed || snapshot.Content != want {
		return ErrRouteRecovery
	}
	return nil
}

// RemoveTailnetGate takes an environment's gate away, wherever it is, and
// nothing else: on nginx the file of that name only when it is a gate, since
// once the environment has a public route again that file is the route.
func (s *Service) RemoveTailnetGate(ctx context.Context, name string) error {
	if tailnetGateNameRe.FindStringSubmatch(name) == nil {
		return errors.New("a tailnet gate is named after its environment's route")
	}
	if err := s.removeGateContainerRoute(ctx, name); err != nil {
		return err
	}
	onNginx, err := s.tailnetGateOnNginx(ctx)
	if err != nil || !onNginx {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotDeploymentRouteLocked(name)
	if err != nil || !snapshot.Existed || !strings.Contains(snapshot.Content, tailnetGateMarker) {
		return err
	}
	empty := snapshot
	empty.Existed, empty.LinkExisted, empty.Content, empty.ContentDigest = false, false, "", routeDigest("")
	if err := s.restoreDeploymentRouteLocked(ctx, empty); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return errors.Join(err, s.restoreDeploymentRouteLocked(recoveryCtx, snapshot))
	}
	return nil
}

// removeGateContainerRoute takes the route out of the gate Caddy, when the
// gate is running and holds one. The access record goes with it.
func (s *Service) removeGateContainerRoute(ctx context.Context, name string) error {
	edge, err := s.gateContainer(ctx)
	if err != nil || edge == nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := edge.snapshot(ctx, name)
	if err != nil || !snapshot.Existed {
		return err
	}
	if !strings.HasPrefix(snapshot.Content, "# Managed by Just Dashboard\n") {
		return ErrRouteRecovery
	}
	empty := snapshot
	empty.Existed, empty.Content, empty.ContentDigest = false, "", routeDigest("")
	if err := edge.restore(ctx, empty); err != nil {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return errors.Join(err, edge.restore(recovery, snapshot))
	}
	edge.removeAccessLog(ctx, name)
	return nil
}

// IsTailnetGateSnapshot says whether a snapshot is of a tailnet front: the
// gate Caddy's route, or a host nginx file marked as one. A public route of
// the same name is neither.
func IsTailnetGateSnapshot(snapshot DeploymentRouteSnapshot) bool {
	return snapshot.Driver == TailnetGateDriver || strings.Contains(snapshot.Content, tailnetGateMarker)
}

// restoreGateSnapshot puts a gate Caddy route back as a snapshot recorded it.
func (s *Service) restoreGateSnapshot(ctx context.Context, snapshot DeploymentRouteSnapshot) error {
	edge, err := s.gateContainer(ctx)
	if err != nil {
		return err
	}
	if edge == nil {
		return ErrRouteRecovery
	}
	return edge.restore(ctx, snapshot)
}

// TailnetGateHost says which proxy gates this host's tailnet-only
// deployments, for preflight and the overview: "nginx", or "caddy" for the
// gate Caddy, which Running says is up.
func (s *Service) TailnetGateHost(ctx context.Context) (driver string, running bool, err error) {
	onNginx, err := s.tailnetGateOnNginx(ctx)
	if err != nil {
		return "", false, err
	}
	if onNginx {
		return accessDriverNginx, true, nil
	}
	edge, err := s.gateContainer(ctx)
	return "caddy", edge != nil, err
}
