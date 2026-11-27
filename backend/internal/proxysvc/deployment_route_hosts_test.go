package proxysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// serviceDomainRoute is a media stack's route: Jellyfin on the deployment's
// own name, Seerr on a service domain over HTTPS and Radarr on a plain one,
// with rules that must reach every name alike.
func serviceDomainRoute() DeploymentRoute {
	return DeploymentRoute{
		Name:     "just-dashboard-env-7.conf",
		Domains:  []string{"media-radarr.example.test", "media-requests.example.test", "media.example.test"},
		Upstream: "http://127.0.0.1:30080", TLS: true, ForceHTTPS: true,
		PlainHostnames: []string{"media-radarr.example.test"},
		AllowFrom:      []string{"10.0.0.0/8"}, AccessLog: true,
		ResponseHeaders: []HeaderValue{{Name: "X-Served-By", Value: "route"}},
		HostUpstreams: []HostUpstream{
			{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://127.0.0.1:30081"},
			{Hostnames: []string{"media-radarr.example.test"}, Upstream: "http://127.0.0.1:30082"},
		},
	}
}

// nginxServiceDomainRoute is the route as the host nginx writes it, with
// the certificate its HTTPS names are served with.
func nginxServiceDomainRoute() DeploymentRoute {
	route := serviceDomainRoute()
	route.CertPath, route.KeyPath = "/etc/ssl/media.crt", "/etc/ssl/media.key"
	return route
}

// On nginx a map on the request's name picks the catch-all's server, and
// every server block — the HTTPS one and the plain one — forwards through it,
// so nothing but the server differs between the names.
func TestNginxRouteSendsServiceDomainsToTheirOwnServers(t *testing.T) {
	route := nginxServiceDomainRoute()
	if err := validateDeploymentRules(route); err != nil {
		t.Fatal(err)
	}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	variable := "$" + NginxIdent(route.Name) + "_host_up"
	// Each name is matched whole, as a regular expression, which nginx does
	// not keep in the map's hash and so takes at any length.
	wantMap := "map $host " + variable + " {\n" +
		"    default                          127.0.0.1:30080;\n" +
		`    ~^media-radarr\.example\.test$   127.0.0.1:30082;` + "\n" +
		`    ~^media-requests\.example\.test$ 127.0.0.1:30081;` + "\n" +
		"}"
	if !strings.Contains(content, wantMap) {
		t.Fatalf("the route has no host map %q:\n%s", wantMap, content)
	}
	if got := strings.Count(content, "proxy_pass http://"+variable+";"); got != 2 {
		t.Fatalf("%d locations forward through the map, want the HTTPS server's and the plain one's:\n%s", got, content)
	}
	if strings.Contains(content, "proxy_pass http://127.0.0.1:") {
		t.Fatalf("a location still forwards to one server:\n%s", content)
	}
	// The rules are the site's, in both servers.
	for _, rule := range []string{"allow 10.0.0.0/8;", "add_header X-Served-By \"route\" always;", "/maintenance.on)"} {
		if got := strings.Count(content, rule); got != 2 {
			t.Fatalf("%q is in %d servers, want both:\n%s", rule, got, content)
		}
	}
	// The plain name is still served without a redirect, and the HTTPS ones
	// keep theirs.
	if !strings.Contains(content, "server_name media-radarr.example.test;") ||
		!strings.Contains(content, "server_name media-requests.example.test media.example.test;\n\n    # Let's Encrypt") {
		t.Fatalf("the names lost their schemes:\n%s", content)
	}
	// The listing reads every server the map names.
	got := hostMapUpstreams(content)
	want := []string{"http://127.0.0.1:30080", "http://127.0.0.1:30082", "http://127.0.0.1:30081"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("the map lists %q, want %q", got, want)
	}

	// Without host upstreams the route is the one it always was.
	plain := route
	plain.HostUpstreams = nil
	before := mustRender(t, deploymentSiteSpec(plain, ""))
	if strings.Contains(before, "map $host") || strings.Count(before, "proxy_pass http://127.0.0.1:30080;") != 2 {
		t.Fatalf("a route without service domains changed:\n%s", before)
	}
}

// A replicated release behind a route with service domains: the map's
// default is the pool, which proxy_pass names as an upstream block.
func TestNginxHostMapDefaultsToTheReleasesPool(t *testing.T) {
	route := nginxServiceDomainRoute()
	route.Upstream, route.Upstreams = "", []string{"http://127.0.0.1:30080", "http://127.0.0.1:30090"}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	pool := NginxIdent(route.Name) + "_pool"
	if !strings.Contains(content, "    default                          "+pool+";\n") || !strings.Contains(content, "upstream "+pool+" {") {
		t.Fatalf("the map does not default to the pool:\n%s", content)
	}
	if !strings.Contains(content, "proxy_connect_timeout 2s;") {
		t.Fatalf("the pool's location settings went:\n%s", content)
	}
}

// On Caddy each block that serves a service domain ends in a handle per
// service, matched by host, and the release's own last, all inside the
// route block whose fence, maintenance and headers come first.
func TestCaddyRouteSendsServiceDomainsToTheirOwnContainers(t *testing.T) {
	route := serviceDomainRoute()
	route.HostUpstreams = []HostUpstream{
		{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://172.18.0.9:5055"},
		{Hostnames: []string{"media-radarr.example.test"}, Upstream: "http://172.18.0.10:7878"},
	}
	content, err := renderDockerCaddyRoute(route, "http://172.18.0.5:8096")
	if err != nil {
		t.Fatal(err)
	}
	https := "    @jd_service_1 host media-requests.example.test\n" +
		"    handle @jd_service_1 {\n      reverse_proxy \"http://172.18.0.9:5055\"\n    }\n" +
		"    handle {\n      reverse_proxy \"http://172.18.0.5:8096\"\n    }\n  }\n"
	plain := "    @jd_service_2 host media-radarr.example.test\n" +
		"    handle @jd_service_2 {\n      reverse_proxy \"http://172.18.0.10:7878\"\n    }\n" +
		"    handle {\n      reverse_proxy \"http://172.18.0.5:8096\"\n    }\n  }\n"
	blocks := strings.SplitN(content, "\nhttp://media-radarr.example.test {\n", 2)
	if len(blocks) != 2 || !strings.Contains(blocks[0], https) || !strings.Contains(blocks[1], plain) {
		t.Fatalf("the blocks do not send their service domains on:\n%s", content)
	}
	for _, block := range blocks {
		fence := strings.Index(block, "respond @outside")
		handle := strings.Index(block, "@jd_service_")
		if fence < 0 || handle < fence || !strings.Contains(block, "X-Served-By \"route\"") {
			t.Fatalf("a block's rules do not come before its services:\n%s", block)
		}
	}
	if strings.Count(content, "@jd_service_1 host") != 1 || strings.Count(content, "@jd_service_2 host") != 1 {
		t.Fatalf("a service is matched in a block that does not serve its name:\n%s", content)
	}

	// A route with no rules of its own is still a route block, since a
	// handle is only exclusive inside one.
	bare := DeploymentRoute{Name: route.Name, Domains: []string{"media-requests.example.test", "media.example.test"},
		Upstream:      "http://127.0.0.1:30080",
		HostUpstreams: []HostUpstream{{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://172.18.0.9:5055"}}}
	content, err = renderDockerCaddyRoute(bare, "http://172.18.0.5:8096")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "  route {\n") || !strings.Contains(content, "    handle @jd_service_1 {\n") {
		t.Fatalf("a route with no other rules does not wrap its services:\n%s", content)
	}

	// Without host upstreams the route is the one it always was.
	bare.HostUpstreams = nil
	content, err = renderDockerCaddyRoute(bare, "http://172.18.0.5:8096")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "handle") || !strings.Contains(content, "\n    reverse_proxy \"http://172.18.0.5:8096\"\n  }\n}\n") {
		t.Fatalf("a route without service domains changed:\n%s", content)
	}
}

// The service's proxy carries what the release's does: the cookies the
// dashboard's name drops and the application's error pages intercepted.
func TestCaddyServiceProxyKeepsTheRoutesProxyRules(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"media-requests.example.test", "media.example.test"},
		Upstream:    "http://127.0.0.1:30080",
		DropCookies: []string{"jd_session"}, ErrorPages: []int{502}, InterceptErrors: true,
		HostUpstreams: []HostUpstream{{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://172.18.0.9:5055"}},
	}
	content, err := renderDockerCaddyRoute(route, "http://172.18.0.5:8096")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(content, "@jd_error status 502"); got != 2 {
		t.Fatalf("%d proxies intercept the application's errors, want both:\n%s", got, content)
	}
	if got := strings.Count(content, "header_up Cookie"); got != 2*strings.Count(caddyDropCookies("", route.DropCookies), "header_up Cookie") {
		t.Fatalf("the service's proxy does not drop the dashboard's cookies:\n%s", content)
	}
}

func TestDeploymentRouteRefusesHostUpstreamsItCannotWrite(t *testing.T) {
	base := func() DeploymentRoute {
		route := serviceDomainRoute()
		route.Redirects = []HostRedirect{{From: "www.media.example.test", To: "media.example.test"}}
		route.Domains = append(route.Domains, "*.preview.example.test")
		return route
	}
	cases := map[string]func(*DeploymentRoute){
		"a name the route does not serve": func(r *DeploymentRoute) {
			r.HostUpstreams[0].Hostnames = []string{"elsewhere.example.test"}
		},
		"a redirecting name": func(r *DeploymentRoute) {
			r.HostUpstreams[0].Hostnames = []string{"www.media.example.test"}
		},
		"a wildcard": func(r *DeploymentRoute) {
			r.HostUpstreams[0].Hostnames = []string{"*.preview.example.test"}
		},
		"a name sent twice": func(r *DeploymentRoute) {
			r.HostUpstreams[1].Hostnames = []string{"MEDIA-REQUESTS.example.test"}
		},
		"no name":           func(r *DeploymentRoute) { r.HostUpstreams[0].Hostnames = nil },
		"a path":            func(r *DeploymentRoute) { r.HostUpstreams[0].Upstream = "http://127.0.0.1:30081/app" },
		"https":             func(r *DeploymentRoute) { r.HostUpstreams[0].Upstream = "https://127.0.0.1:30081" },
		"a host name":       func(r *DeploymentRoute) { r.HostUpstreams[0].Upstream = "http://seerr:5055" },
		"no port":           func(r *DeploymentRoute) { r.HostUpstreams[0].Upstream = "http://127.0.0.1" },
		"a directive's end": func(r *DeploymentRoute) { r.HostUpstreams[0].Upstream = "http://127.0.0.1:1;" },
		"the release's own": func(r *DeploymentRoute) { r.HostUpstreams[0].Upstream = r.Upstream },
		"one server twice":  func(r *DeploymentRoute) { r.HostUpstreams[1].Upstream = r.HostUpstreams[0].Upstream },
		"no release":        func(r *DeploymentRoute) { r.Upstream = "" },
		"a canary": func(r *DeploymentRoute) {
			r.Canary = &DeploymentCanary{Percent: 10, Upstreams: []string{"http://127.0.0.1:1"}, Live: "1", Candidate: "2"}
		},
		"skew protection":    func(r *DeploymentRoute) { r.Skew = &DeploymentSkew{Release: "1", MaxAge: 600} },
		"a sleeping release": func(r *DeploymentRoute) { r.Sleep = &DeploymentSleep{HoldSeconds: 30} },
		"too many": func(r *DeploymentRoute) {
			r.HostUpstreams = nil
			for index := range MaxHostUpstreams + 1 {
				name := "s" + strconv.Itoa(index) + ".example.test"
				r.Domains = append(r.Domains, name)
				r.HostUpstreams = append(r.HostUpstreams, HostUpstream{Hostnames: []string{name}, Upstream: "http://127.0.0.1:" + strconv.Itoa(31000+index)})
			}
		},
	}
	if err := validateDeploymentRules(base()); err != nil {
		t.Fatalf("the base route is refused: %v", err)
	}
	for name, change := range cases {
		route := base()
		change(&route)
		if err := validateDeploymentRules(route); err == nil {
			t.Errorf("%s: accepted %+v", name, route.HostUpstreams)
		}
	}
	// What the dashboard writes is a publication on this host's loopback;
	// a container's own address is Caddy's to resolve, not the plan's.
	private := serviceDomainRoute()
	private.HostUpstreams[0].Upstream = "http://172.18.0.9:5055"
	if err := validateDeploymentRules(private); err != nil {
		t.Fatalf("a resolved container address is not a route's shape: %v", err)
	}
	if err := requirePrivateHostUpstreams(private); err == nil {
		t.Error("a service's server off this host's loopback was accepted")
	}
	if err := requirePrivateHostUpstreams(serviceDomainRoute()); err != nil {
		t.Errorf("a loopback publication was refused: %v", err)
	}
	// nginx is held to the same names and addresses where the map is
	// written.
	spec := deploymentSiteSpec(nginxServiceDomainRoute(), "")
	if _, err := RenderNginx(spec); err != nil {
		t.Fatalf("the route itself does not render: %v", err)
	}
	spec.hostUpstreams[0].Address = "127.0.0.1:1; include /etc/passwd"
	if _, err := RenderNginx(spec); err == nil {
		t.Error("nginx wrote a map line that ends a directive early")
	}
	spec = deploymentSiteSpec(nginxServiceDomainRoute(), "")
	spec.hostUpstreams[0].Host = "other.example.test"
	if _, err := RenderNginx(spec); err == nil {
		t.Error("nginx wrote a map line for a name the site does not serve")
	}
}

// serviceHost is an ingress Caddy, the release's Jellyfin and a Seerr
// beside it, each publishing its port on loopback, as Docker's inspect
// describes them.
func serviceHost(seerrAddress string) string {
	jellyfin, seerr := strings.Repeat("a", 64), strings.Repeat("b", 64)
	container := func(id, name, port, host, address string) string {
		return `{"Id":"` + id + `","Name":"/` + name + `","State":{"Running":true},"NetworkSettings":{"Ports":{"` + port +
			`/tcp":[{"HostIp":"127.0.0.1","HostPort":"` + host + `"}]},"Networks":{"jd-e7_default":{"IPAddress":"` + address + `"}}}}`
	}
	ingress := strings.TrimSuffix(strings.TrimSpace(ingressInspect), "]")
	return ingress + "," + container(jellyfin, "jellyfin", "8096", "30080", "172.18.0.5") + "," +
		container(seerr, "seerr", "5055", "30081", seerrAddress) + "]"
}

// Caddy finds each service domain's container by its loopback publication
// as it finds the release's, sends the name to the container's address, and
// records the container on a target line of its own, which the inventory
// lists and the maintenance pass repairs.
func TestCaddyRouteResolvesEachServiceToItsContainer(t *testing.T) {
	edge := &dockerCaddy{ID: "4f1c0ffee", docker: dockerSource{reader: &fakeContainers{running: serviceHost("172.18.0.9")}}}
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"media-requests.example.test", "media.example.test"},
		Upstream:      "http://127.0.0.1:30080",
		HostUpstreams: []HostUpstream{{Hostnames: []string{"media-requests.example.test"}, Upstream: "http://127.0.0.1:30081"}},
	}
	content, targets, err := edge.routeContent(context.Background(), route)
	if err != nil {
		t.Fatal(err)
	}
	jellyfin := dockerCaddyTarget{ContainerID: strings.Repeat("a", 64), Network: "jd-e7_default", Port: "8096", Address: "172.18.0.5"}
	seerr := dockerCaddyTarget{ContainerID: strings.Repeat("b", 64), Network: "jd-e7_default", Port: "5055", Address: "172.18.0.9"}
	if len(targets) != 2 || targets[0] != jellyfin || targets[1] != seerr {
		t.Fatalf("Caddy joins %+v, want the release's container and Seerr's", targets)
	}
	if !strings.Contains(content, "handle @jd_service_1 {\n      reverse_proxy \"http://172.18.0.9:5055\"\n") ||
		!strings.HasSuffix(content, "}\n"+jellyfin.metadata()+seerr.metadata()) {
		t.Fatalf("the route does not send Seerr's name to its container or record it:\n%s", content)
	}
	recorded, ok := parseDockerCaddyTargets(content)
	if !ok || len(recorded) != 2 || recorded[1] != seerr {
		t.Fatalf("the route records %+v", recorded)
	}

	// Seerr's container came back at another address: the pass finds it
	// by its identity and rewrites the service's proxy with the release's
	// left as it is.
	moved := seerr
	moved.Address = "172.18.0.12"
	var running []ingressContainer
	if err := json.Unmarshal([]byte(serviceHost("172.18.0.12")), &running); err != nil {
		t.Fatal(err)
	}
	next, found, _ := edge.locateTarget(seerr, running)
	if !found || next != moved {
		t.Fatalf("the moved service was located as %+v, %v", next, found)
	}
	repaired := replaceProxiedUpstreams(content, recorded, []dockerCaddyTarget{jellyfin, moved})
	if !strings.Contains(repaired, "reverse_proxy \"http://172.18.0.12:5055\"") || strings.Contains(repaired, "172.18.0.9:5055\"") ||
		!strings.Contains(repaired, "reverse_proxy \"http://172.18.0.5:8096\"") {
		t.Fatalf("the repair did not move the service alone:\n%s", repaired)
	}

	// A service nothing publishes is no route at all.
	route.HostUpstreams[0].Upstream = "http://127.0.0.1:30099"
	if _, _, err := edge.routeContent(context.Background(), route); err == nil || !strings.Contains(err.Error(), "media-requests.example.test") {
		t.Fatalf("a service with no container: %v", err)
	}
}

// The host nginx's listing reads a route's service servers from its map,
// beside the release's own.
func TestNginxListingNamesTheServiceServers(t *testing.T) {
	dir := t.TempDir()
	route := nginxServiceDomainRoute()
	content := mustRender(t, deploymentSiteSpec(route, ""))
	path := filepath.Join(dir, route.Name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(dir, "")
	site := service.fileVHost(route.Name, path, "conf.d", logDefaults{})
	want := []string{"http://127.0.0.1:30080", "http://127.0.0.1:30082", "http://127.0.0.1:30081"}
	if strings.Join(site.Upstreams, " ") != strings.Join(want, " ") {
		t.Fatalf("the listing names %q, want %q", site.Upstreams, want)
	}
}

// The host's real nginx takes a service domain of any length: a sibling
// name such as media-subtitles.<handle>.just-dashboard.com is past the 46
// characters a map's hash bucket holds by default, and the map matched by
// string refused the whole configuration with "could not build map_hash".
// The server-name hash is raised the way the dashboard's fit does
// (server_names_fit.go), and each name reaches its own server whatever its
// case.
func TestLiveNginxSendsALongServiceDomainToItsServer(t *testing.T) {
	root := liveNginx(t)
	server := func(name string) string {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, name)
		}))
		t.Cleanup(backend.Close)
		return backend.URL
	}
	long := "media-subtitles.a-handle-of-twelve.just-dashboard.example"
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{long, "media.example.test"},
		Upstream:      server("release"),
		HostUpstreams: []HostUpstream{{Hostnames: []string{long}, Upstream: server("service")}},
	}
	if err := validateDeploymentRules(route); err != nil {
		t.Fatal(err)
	}
	fit := filepath.Join(root, "conf.d", serverNamesFile)
	if err := os.WriteFile(fit, []byte(fmt.Sprintf("%s %d;\n", bucketDirective, bucketFor(len(long)))), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unprivileged nginx cannot open the route's request record under
	// /var/log; the route is otherwise installed as rendered.
	spec := deploymentSiteSpec(route, "")
	spec.AccessLog = false
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused a %d-character service domain: %s", len(long), result.Output)
	}
	startNginx(t, root)
	for host, want := range map[string]string{
		long: "service", strings.ToUpper(long): "service", "media.example.test": "release",
	} {
		if _, body := siteGet(t, port, host, "/"); body != want {
			t.Errorf("%s reached %q, want %q", host, body, want)
		}
	}
}
