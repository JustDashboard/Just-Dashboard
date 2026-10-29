package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A route whose application may sleep. The dashboard stops the live
// release's container while nobody visits (deploy/sleep.go) and the route
// keeps answering: a request that finds the application stopped is held
// while the dashboard starts it, and then sent on as it came, so the visitor
// gets the page after a pause rather than an error.
//
// Two things send a request to the wake, on either proxy:
//
//   - The sleeping switch, a file beside the route in its pages folder
//     (sleeping.on), written before the container is stopped and removed once
//     it answers again. The proxy looks for it on every request, as it does
//     the maintenance switch, so nothing is reloaded. While it is there the
//     route never dials the stopped container's old address: Docker hands a
//     stopped container's address on the network, and its loopback port, to
//     whatever starts next, and a visitor must not be answered by that.
//   - The application not answering at all (a 502 or 504 from the proxy
//     itself), which covers a switch that went missing.
//
// The wake is a call to the dashboard over a Unix socket the proxy alone
// can reach (WakeSockets): nginx's auth_request, Caddy's reverse_proxy with a
// handle_response. It answers 200 once the application is ready (with the
// address it now has, for Caddy), 401 while it is still starting after what
// the visitor is held for — the proxy then serves the waking page, which
// asks the browser to try again — and 403 when there is nothing asleep to
// wake, when the proxy answers the 502 it would have.
//
// Every check the route makes runs before the wake: the fence, maintenance,
// the password and the sign-in. Only a request the route would have handed
// to the application can wake it.

// sleepingSwitchFile is the file whose presence says the route's application
// is asleep.
const sleepingSwitchFile = "sleeping.on"

// asleepSocket is the address the nginx route sends a sleeping request to:
// a socket that is never created, so the connection fails at once and the
// request is handed to the wake.
const asleepSocket = "asleep.sock"

// wakeURI is the internal location nginx asks the dashboard through.
const wakeURI = "/__jd/wake"

// dockerCaddyWakeSocket is where the dashboard listens for the Docker Caddy
// ingress, inside its persistent /config: Caddy's loopback is its own
// container's, and a socket in a volume only the ingress mounts is reached by
// the ingress and nothing else.
const dockerCaddyWakeSocket = dockerCaddyRoot + "/wake/wake.sock"

// The wake's answers, in the words both proxies' wake read: nginx's
// auth_request knows a 2xx, a 401 and a 403 and nothing else.
const (
	// WakeAwake is the application answering: the request is sent on.
	WakeAwake = http.StatusOK
	// WakeStarting is the application still starting after the visitor's
	// hold: the waking page.
	WakeStarting = http.StatusUnauthorized
	// WakeNothing is nothing asleep to wake, or a wake that failed: the 502
	// the request would have had.
	WakeNothing = http.StatusForbidden
)

// WakeUpstreamHeader carries the address the woken application answers at,
// which Caddy sends the held request to: a container started again may have
// been given another address on its network.
const WakeUpstreamHeader = "X-Jd-Upstream"

// maxSocketPath is the longest path a Unix socket address carries, less its
// terminating NUL; nginx refuses a configuration naming a longer one.
const maxSocketPath = 107

// MaxWakeHold is the longest a proxy holds a visitor's request while the
// application starts. Past it the visitor gets the waking page, which tries
// again on its own, rather than a connection that looks hung.
const MaxWakeHold = 300

// DeploymentSleep is a route whose application may sleep.
type DeploymentSleep struct {
	// HoldSeconds is how long the proxy holds a request while the
	// application starts, before it answers with the waking page.
	HoldSeconds int `json:"holdSeconds"`
}

func validateDeploymentSleep(route DeploymentRoute) error {
	sleep := route.Sleep
	if sleep == nil {
		return nil
	}
	if sleep.HoldSeconds < 1 || sleep.HoldSeconds > MaxWakeHold {
		return fmt.Errorf("a sleeping route holds a request for 1 to %d seconds", MaxWakeHold)
	}
	if !strings.HasPrefix(route.Name, "just-dashboard-") {
		return errors.New("a route that sleeps is kept under its deployment's name")
	}
	if _, _, err := sleepUpstream(route.Upstream); err != nil {
		return err
	}
	return nil
}

// sleepUpstream is the route's upstream as host and port, which is what both
// proxies' wake send a request on to.
func sleepUpstream(upstream string) (string, string, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		net.ParseIP(parsed.Hostname()) == nil || parsed.Port() == "" {
		return "", "", errors.New("a route that sleeps forwards to an address and port")
	}
	return parsed.Hostname(), parsed.Port(), nil
}

// siteSleep is a deployment route's wake as nginx renders it.
type siteSleep struct {
	// Route names the route to the dashboard, in the wake's path.
	Route string
	// Socket is the dashboard's wake socket on this host.
	Socket string
	// Upstream is the application's address, host and port.
	Upstream string
	// Hold is how long nginx waits on the dashboard's answer.
	Hold int
}

// The kinds of location a sleeping route adds to the one it forwards from.
const (
	wakeMain  = "main"
	wakeRetry = "retry"
)

func (spec *SiteSpec) sleepingSwitchPath() string {
	return spec.pagesDir() + "/" + sleepingSwitchFile
}

func (spec *SiteSpec) sleepUpstreamVar() string { return spec.varIdent() + "_up" }

// renderSleepSwitch picks where the server sends the application's requests:
// the application's address, or — while the switch is there — a socket that
// does not exist, so the request is refused at once and woken. A variable
// set at server level rather than an if in the location, where proxy_pass
// would be another location's.
func renderSleepSwitch(l *lines, spec *SiteSpec) {
	if spec.sleep == nil {
		return
	}
	l.add("    # The application may be asleep by design: its container is stopped")
	l.add("    # while nobody visits, and this file exists while it is. Its address")
	l.add("    # may then be another's, so nothing is sent there; the request is")
	l.add("    # refused at once and handed to the wake below, after every check.")
	l.add("    set $%s %s;", spec.sleepUpstreamVar(), spec.sleep.Upstream)
	l.add("    if (-f %s) {", spec.sleepingSwitchPath())
	l.add("        set $%s unix:%s/%s;", spec.sleepUpstreamVar(), spec.pagesDir(), asleepSocket)
	l.add("    }")
	l.blank()
}

// renderWakeOpening is the start of the location a woken request is sent on
// from: the dashboard is asked first, and the request waits for its answer.
func renderWakeOpening(l *lines, spec *SiteSpec) {
	l.add("    location @jd_wake {")
	l.add("        # The dashboard starts the stopped container and answers once it")
	l.add("        # is ready; the request is then sent on as it came, body and all.")
	l.add("        auth_request %s;", wakeURI)
	// Its own set, so the sign-in's are not inherited and read again from
	// the wake's answer: the visitor's were read in the location it came
	// from, and are sent on as they were.
	l.add("        auth_request_set $%s_woke $upstream_http_x_jd_upstream;", spec.varIdent())
}

// renderWakeErrors is where a location of a sleeping route sends what goes
// wrong. A location that names any error_page replaces the server's, so the
// server's own are said again here: the sign-in's 401 and the route's pages.
func renderWakeErrors(l *lines, loc SiteLocation, spec *SiteSpec) {
	switch loc.wake {
	case wakeMain:
		l.add("        # The application not answering is woken rather than refused:")
		l.add("        # it may be asleep by design.")
		l.add("        recursive_error_pages on;")
		l.add("        error_page 502 504 = @jd_wake;")
		for _, line := range serverErrorPages(spec, 502, 504) {
			l.add("        %s", line)
		}
	case wakeRetry:
		l.add("        recursive_error_pages on;")
		l.add("        # Still starting after what a visitor is held for: the waking")
		l.add("        # page, which tries again by itself.")
		l.add("        error_page 401 502 504 =503 %s;", pageURI("waking"))
		l.add("        # Nothing asleep to wake, or it could not be started: the 502")
		l.add("        # the request would have had.")
		l.add("        error_page 403 500 = @jd_down;")
		for _, line := range serverErrorPages(spec, 401, 403, 500, 502, 504) {
			l.add("        %s", line)
		}
	}
}

// serverErrorPages are the error_page lines the server itself writes, less
// the codes given: the sign-in's and the route's own pages.
func serverErrorPages(spec *SiteSpec, skip ...int) []string {
	skipped := func(code int) bool {
		for _, s := range skip {
			if s == code {
				return true
			}
		}
		return false
	}
	var out []string
	if f := spec.ForwardAuth; f != nil && !skipped(401) {
		sep := "?"
		if strings.Contains(f.SignIn, "?") {
			sep = "&"
		}
		out = append(out, fmt.Sprintf("error_page 401 =302 %s%s%s;", f.SignIn, sep, forwardAuthReturn))
	}
	on := spec.Maintenance != nil && spec.Maintenance.On && !spec.Maintenance.Switch
	if on && !skipped(503) {
		out = append(out, "error_page 503 "+pageURI("maintenance")+";")
	}
	for _, code := range errorPageCodes {
		if spec.hasErrorPage(code) && !(on && code == 503) && !skipped(code) {
			out = append(out, fmt.Sprintf("error_page %d %s;", code, pageURI(strconv.Itoa(code))))
		}
	}
	return out
}

// renderWakeLocations are the locations a sleeping route's wake uses beside
// @jd_wake: the call to the dashboard, and the 502 a request that had
// nothing to wake is answered with. The waking page is with the other pages.
func renderWakeLocations(l *lines, spec *SiteSpec) {
	if spec.sleep == nil {
		return
	}
	l.add("    location @jd_down {")
	l.add("        return 502;")
	l.add("    }")
	l.blank()
	l.add("    # The dashboard's wake, over a socket only this host's proxy reaches.")
	l.add("    # A subrequest skips the access phase, so it is not itself checked,")
	l.add("    # and it carries none of the visitor's headers or body.")
	l.add("    location = %s {", wakeURI)
	l.add("        internal;")
	l.add("        proxy_pass http://unix:%s:/wake/%s;", spec.sleep.Socket, spec.sleep.Route)
	l.add("        proxy_method GET;")
	l.add("        proxy_pass_request_body off;")
	l.add("        proxy_pass_request_headers off;")
	l.add("        proxy_set_header Host jd-wake;")
	l.add("        proxy_set_header X-Forwarded-Host $host;")
	l.add("        proxy_connect_timeout 2s;")
	l.add("        proxy_read_timeout %ds;", spec.sleep.Hold+10)
	l.add("    }")
}

// renderWakingPageExtra is what the waking page's location adds: a short
// Retry-After and no caching, since the next answer is the application.
func renderWakingPageExtra(l *lines, spec *SiteSpec) {
	l.add("        add_header Retry-After 5 always;")
	l.add("        add_header Cache-Control \"no-store\" always;")
	inner := &lines{}
	renderHeaders(inner, spec)
	for _, line := range inner.out {
		l.add("    %s", line)
	}
}

// nginxWakeSocket is where the dashboard listens for this host's nginx.
func (s *Service) nginxWakeSocket() string {
	return filepath.Join(s.nginxDir, "jd-wake", "wake.sock")
}

// --- Caddy ------------------------------------------------------------------

// renderCaddySleep is the sleeping switch inside a route's handling, after
// every check and just before the application, and the mark that says a
// request got that far: only such a request's error is woken.
func renderCaddySleep(route DeploymentRoute, pages string) string {
	return "  @jd_asleep file {\n" +
		"    root " + pages + "\n" +
		"    try_files /" + sleepingSwitchFile + "\n" +
		"  }\n" +
		"  route @jd_asleep {\n" +
		indentCaddy(renderCaddyWake(route, pages)) +
		"  }\n" +
		"  vars jd_app 1\n"
}

// renderCaddyWake asks the dashboard to wake the application and sends the
// request on to the address it answers with. A 401 is the application still
// starting after the visitor's hold, answered with the waking page; anything
// else is nothing to wake, answered as the proxy would have answered.
func renderCaddyWake(route DeploymentRoute, pages string) string {
	hold := route.Sleep.HoldSeconds
	down := "      respond \"Bad Gateway\" 502\n"
	for _, code := range route.ErrorPages {
		if code == 502 {
			down = "      root * " + pages + "\n" +
				"      rewrite * /502.html\n" +
				"      file_server {\n        status 502\n      }\n"
		}
	}
	return "  reverse_proxy unix/" + dockerCaddyWakeSocket + " {\n" +
		"    method GET\n" +
		"    rewrite " + strconv.Quote("/wake/"+route.Name) + "\n" +
		"    header_up Host jd-wake\n" +
		"    header_up -Cookie\n" +
		"    header_up -Authorization\n" +
		"    header_up -Proxy-Authorization\n" +
		"    transport http {\n" +
		"      response_header_timeout " + strconv.Itoa(hold+10) + "s\n" +
		"    }\n" +
		"    @jd_awake status 2xx\n" +
		"    handle_response @jd_awake {\n" +
		"      vars jd_wake_upstream {rp.header." + WakeUpstreamHeader + "}\n" +
		"    }\n" +
		"    @jd_waking status 401\n" +
		"    handle_response @jd_waking {\n" +
		"      header Retry-After 5\n" +
		"      header Cache-Control no-store\n" +
		"      root * " + pages + "\n" +
		"      rewrite * /waking.html\n" +
		"      file_server {\n        status 503\n      }\n" +
		"    }\n" +
		"    handle_response {\n" +
		down +
		"    }\n" +
		"  }\n" +
		"  reverse_proxy {vars.jd_wake_upstream} {\n" +
		// The woken application is sent the request as the route's own
		// reverse_proxy sends it: without the cookies the route drops.
		caddyDropCookies("    ", route.DropCookies) +
		"    lb_try_duration 5s\n" +
		"    lb_try_interval 250ms\n" +
		"  }\n"
}

// withCaddyDialTimeout gives a reverse_proxy directive a short dial
// timeout, inside the block it may already have.
func withCaddyDialTimeout(directive string) string {
	transport := "    transport http {\n      dial_timeout 1s\n    }\n"
	if strings.HasSuffix(directive, "\n  }") {
		return strings.TrimSuffix(directive, "  }") + transport + "  }"
	}
	return directive + " {\n" + transport + "  }"
}

// renderCaddyWakeErrors is the wake for a request the application did not
// answer, ahead of the route's own error pages: a 502 or 504 that came from
// the application's reverse_proxy, which only a request every check let
// through reaches (jd_app). An auth server that does not answer is a 502
// too, and must never wake anything past the sign-in.
func renderCaddyWakeErrors(route DeploymentRoute, pages string) string {
	return "    @jd_wake {\n" +
		"      expression `{err.status_code} in [502, 504]`\n" +
		"      vars jd_app 1\n" +
		"    }\n" +
		"    handle @jd_wake {\n" +
		"      route {\n" +
		indentCaddy(indentCaddy(indentCaddy(renderCaddyWake(route, pages)))) +
		"      }\n" +
		"    }\n"
}

// --- the switch and the socket ----------------------------------------------

// SleepSwitch is where a route's sleeping switch stands.
type SleepSwitch struct {
	Driver string `json:"driver"`
	// Ready says the live route carries the wake, so a stopped application
	// is started by the next request.
	Ready bool `json:"ready"`
	// On says the switch is there: requests are sent to the wake.
	On    bool       `json:"on"`
	Since *time.Time `json:"since,omitempty"`
}

// ErrSleepUnready refuses to put to sleep an application whose live route
// would not wake it.
var ErrSleepUnready = errors.New("the deployment's live route does not wake a sleeping application")

// DeploymentSleeping reads a route's sleeping switch.
func (s *Service) DeploymentSleeping(ctx context.Context, name string) (SleepSwitch, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return SleepSwitch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge != nil {
		return edge.sleeping(ctx, name)
	}
	return s.nginxSleepingLocked(name)
}

// SetDeploymentSleeping writes or removes a route's sleeping switch. On is
// refused for a route that would not wake the application; off is always
// allowed, so a switch left behind can be cleared whatever the route is.
func (s *Service) SetDeploymentSleeping(ctx context.Context, name string, on bool) (SleepSwitch, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return SleepSwitch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge != nil {
		return edge.setSleeping(ctx, name, on)
	}
	return s.setNginxSleepingLocked(name, on)
}

func (s *Service) nginxSleepingLocked(name string) (SleepSwitch, error) {
	out := SleepSwitch{Driver: "nginx"}
	if !siteNameRe.MatchString(name) {
		return out, errors.New("invalid deployment route name")
	}
	path, _, err := s.deploymentRoutePaths(name)
	if err != nil {
		return out, err
	}
	if content, exists := readIfPresent(path); exists {
		out.Ready = strings.Contains(content, "auth_request "+wakeURI+";")
	}
	switchPath := filepath.Join(s.SitePagesDir(name), sleepingSwitchFile)
	if info, err := os.Stat(switchPath); err == nil {
		out.On = true
		since := switchSince(readSwitch(switchPath), info.ModTime())
		out.Since = &since
	}
	return out, nil
}

func (s *Service) setNginxSleepingLocked(name string, on bool) (SleepSwitch, error) {
	current, err := s.nginxSleepingLocked(name)
	if err != nil {
		return current, err
	}
	switchPath := filepath.Join(s.SitePagesDir(name), sleepingSwitchFile)
	if !on {
		// The folder is checked as a page's is, so a link put in its place
		// never has a file removed wherever it points.
		if _, err := s.pageFile(name, "waking", false); errors.Is(err, os.ErrNotExist) {
			return s.nginxSleepingLocked(name)
		} else if err != nil {
			return current, err
		}
		if err := os.Remove(switchPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return current, err
		}
		return s.nginxSleepingLocked(name)
	}
	if !current.Ready {
		return current, ErrSleepUnready
	}
	if current.On {
		return current, nil
	}
	// The page first, so the wake never sends anyone to a page that is not
	// there; the release that wrote the route wrote it too, unless it went.
	full, err := s.pageFile(name, "waking", true)
	if err != nil {
		return current, err
	}
	if _, err := os.Lstat(full); errors.Is(err, os.ErrNotExist) {
		page, _ := ShippedSitePage("waking")
		if err := writeAtomic(full, page); err != nil {
			return current, err
		}
		if err := os.Chmod(full, 0o644); err != nil {
			return current, err
		}
	}
	if err := writeAtomic(switchPath, time.Now().UTC().Format(time.RFC3339)+"\n"); err != nil {
		return current, err
	}
	if err := os.Chmod(switchPath, 0o644); err != nil {
		return current, err
	}
	return s.nginxSleepingLocked(name)
}

func (c *dockerCaddy) sleeping(ctx context.Context, name string) (SleepSwitch, error) {
	out := SleepSwitch{Driver: "caddy"}
	path, err := dockerCaddyRoutePath(name)
	if err != nil {
		return out, err
	}
	dir, err := dockerCaddyPagesDir(name)
	if err != nil {
		return out, err
	}
	content, exists, err := c.read(ctx, path)
	if err != nil {
		return out, err
	}
	if exists && strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		out.Ready = strings.Contains(content, "reverse_proxy unix/"+dockerCaddyWakeSocket+" {\n")
	}
	flag, on, err := c.read(ctx, dir+"/"+sleepingSwitchFile)
	if err != nil {
		return out, err
	}
	if on {
		out.On = true
		since := switchSince(flag, time.Now())
		out.Since = &since
	}
	return out, nil
}

func (c *dockerCaddy) setSleeping(ctx context.Context, name string, on bool) (SleepSwitch, error) {
	dir, err := dockerCaddyPagesDir(name)
	if err != nil {
		return SleepSwitch{Driver: "caddy"}, err
	}
	if !on {
		// One command: a wake clears the switch while visitors wait on it.
		if _, err := c.command(ctx, "", "rm", "-f", dir+"/"+sleepingSwitchFile); err != nil {
			return SleepSwitch{Driver: "caddy"}, err
		}
		return SleepSwitch{Driver: "caddy"}, nil
	}
	current, err := c.sleeping(ctx, name)
	if err != nil {
		return current, err
	}
	if !current.Ready {
		return current, ErrSleepUnready
	}
	if current.On {
		return current, nil
	}
	if err := c.ensurePages(ctx, DeploymentRoute{Name: name, Sleep: &DeploymentSleep{HoldSeconds: 1}}); err != nil {
		return current, err
	}
	if err := c.write(ctx, dir+"/"+sleepingSwitchFile, time.Now().UTC().Format(time.RFC3339)+"\n"); err != nil {
		return current, err
	}
	return c.sleeping(ctx, name)
}

// WakeSocket is one socket the dashboard answers the wake on.
type WakeSocket struct {
	// Path is the socket on this host's filesystem, as the dashboard sees it.
	Path string `json:"path"`
	// Proxy is the program that dials it: "nginx" or "caddy".
	Proxy string `json:"proxy"`
}

// WakeSockets are where the dashboard has to listen for this host's proxy to
// reach its wake: the Docker Caddy ingress's, inside the volume it keeps its
// configuration in, or host nginx's beside its sites.
func (s *Service) WakeSockets(ctx context.Context) ([]WakeSocket, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, err
	}
	if edge != nil {
		if edge.ConfigSource == "" {
			return nil, fmt.Errorf("the ingress %s keeps no /config this host can reach", edge.Name)
		}
		root, ok := hostVisiblePath(edge.ConfigSource)
		if !ok {
			return nil, fmt.Errorf("the ingress %s keeps its /config at %s, which the dashboard cannot reach", edge.Name, edge.ConfigSource)
		}
		return []WakeSocket{{Path: filepath.Join(root, strings.TrimPrefix(dockerCaddyWakeSocket, "/config/")), Proxy: "caddy"}}, nil
	}
	return []WakeSocket{{Path: s.nginxWakeSocket(), Proxy: "nginx"}}, nil
}

// WakeSocketReached says whether the proxy sees the socket the dashboard
// opened: a socket made in a folder that is not the ingress's volume after
// all would be one nobody dials, and an application put to sleep behind it
// would never wake.
func (s *Service) WakeSocketReached(ctx context.Context, socket WakeSocket) bool {
	if socket.Proxy != "caddy" {
		return true
	}
	edge, err := s.dockerCaddy(ctx)
	if err != nil || edge == nil {
		return false
	}
	_, err = edge.command(ctx, "", "test", "-S", dockerCaddyWakeSocket)
	return err == nil
}

// hostVisiblePath is a host directory where this process can reach it: at
// its own name, as the Docker volumes are mounted into the dashboard's
// container, or under /host, where the rest of the host is.
func hostVisiblePath(path string) (string, bool) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path, true
	}
	if info, err := os.Stat(filepath.Join("/host", path)); err == nil && info.IsDir() {
		return filepath.Join("/host", path), true
	}
	return path, false
}

// RefreshDeploymentRoute brings a route up to its application's address
// after the application was started again, and answers it as host:port. A
// stopped container's address on its network is given to whatever starts
// next, so a container started again may have another; on Caddy the route
// is moved to it, as the lifecycle pass would, rather than at the next pass.
// nginx forwards to the loopback port the release publishes, which a start
// keeps, and has nothing to refresh.
func (s *Service) RefreshDeploymentRoute(ctx context.Context, name string) (string, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil || edge == nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return edge.refreshTarget(ctx, name)
}

func (c *dockerCaddy) refreshTarget(ctx context.Context, name string) (string, error) {
	snapshot, err := c.snapshot(ctx, name)
	if err != nil {
		return "", err
	}
	target, ok := parseDockerCaddyTarget(snapshot.Content)
	if !ok {
		return "", errors.New("the route names no application container")
	}
	// The two containers that matter, rather than every one on the host.
	raw, err := hostexec.Command(ctx, "docker", "inspect", "--type", "container", target.ContainerID, c.ID).Output()
	if err != nil {
		return "", errors.New("the route's application container is gone")
	}
	var containers []ingressContainer
	if err := json.Unmarshal(raw, &containers); err != nil {
		return "", err
	}
	for index := range containers {
		if !containers[index].State.Running {
			containers[index].NetworkSettings.Networks = nil
		}
	}
	next, found, connected := c.locateTarget(target, containers)
	if !found {
		return "", errors.New("the route's application container is not running")
	}
	if !connected {
		if _, err := c.connectTarget(ctx, next); err != nil {
			return "", err
		}
	}
	if next != target {
		if err := c.configurationSynced(ctx); err != nil {
			return "", err
		}
		content := strings.ReplaceAll(snapshot.Content, "reverse_proxy "+strconv.Quote(target.upstream()), "reverse_proxy "+strconv.Quote(next.upstream()))
		content = strings.Replace(content, target.metadata(), next.metadata(), 1)
		writeErr := c.write(ctx, snapshot.Path, content)
		if writeErr == nil {
			writeErr = c.reload(ctx)
		}
		if writeErr != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			return "", errors.Join(writeErr, c.restore(recovery, snapshot))
		}
	}
	return net.JoinHostPort(next.Address, next.Port), nil
}
