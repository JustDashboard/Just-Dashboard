package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ExistingIngressBinding describes an externally owned route. It is never a
// managed DeploymentRoute: its certificates, authentication and URI behavior
// remain the original proxy's responsibility.
type ExistingIngressBinding struct {
	ID                string `json:"id"`
	Hostname          string `json:"hostname"`
	Path              string `json:"path"`
	Service           string `json:"service"`
	Owner             string `json:"owner"`
	ProxyKind         string `json:"proxyKind"`
	Status            string `json:"status"`
	Continuity        string `json:"continuity"`
	PlannedChange     string `json:"plannedChange,omitempty"`
	HTTPS             bool   `json:"https,omitempty"`
	Upstream          string `json:"upstream,omitempty"`
	SourcePath        string `json:"sourcePath,omitempty"`
	SourceDigest      string `json:"sourceDigest,omitempty"`
	SourceIdentity    string `json:"sourceIdentity,omitempty"`
	Selector          string `json:"selector,omitempty"`
	IngressIdentity   string `json:"ingressIdentity,omitempty"`
	ContainerID       string `json:"containerId,omitempty"`
	TargetContainerID string `json:"targetContainerId,omitempty"`
	CapturedStopped   bool   `json:"capturedStopped,omitempty"`
	Network           string `json:"network,omitempty"`
	Port              int    `json:"port,omitempty"`
}

type ExistingIngressTarget struct {
	Service       string
	Host          string
	Port          int
	ContainerPort int
	Network       string
	Alias         string
	Address       string
	ContainerID   string
	Stopped       bool
}

func ingressEndpoint(raw string) (string, int, bool) {
	if strings.ContainsAny(raw, "${}\r\n\x00") {
		return "", 0, false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "h2c") {
		return "", 0, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return u.Hostname(), port, true
}

func ingressMatches(raw string, targets []ExistingIngressTarget, docker bool) (ExistingIngressTarget, string, bool) {
	host, port, ok := ingressEndpoint(raw)
	if !ok {
		return ExistingIngressTarget{}, "", false
	}
	var selected ExistingIngressTarget
	continuity := ""
	for _, target := range targets {
		matched, mode := false, ""
		if target.Network == "" && port == target.Port && !docker {
			matched = host == target.Host || ((host == "localhost" || host == "127.0.0.1" || host == "::1") && (target.Host == "" || target.Host == "0.0.0.0" || target.Host == "::" || target.Host == "127.0.0.1" || target.Host == "::1"))
			mode = "host_port"
		}
		if target.Network != "" && (port == target.ContainerPort || target.ContainerPort == 0) {
			if target.Alias != "" && host == target.Alias && docker {
				matched, mode = true, "network_alias"
			}
			if target.Address != "" && host == target.Address {
				matched, mode = true, "retarget"
			}
		}
		if !matched {
			continue
		}
		if target.ContainerPort == 0 {
			target.ContainerPort = port
		}
		if continuity != "" && (selected.Service != target.Service || selected.Network != target.Network) {
			return ExistingIngressTarget{}, "unverified", false
		}
		selected, continuity = target, mode
	}
	return selected, continuity, continuity != ""
}

// CaptureExistingIngress reads manager-authored route trees rather than joining
// a virtual host's independently flattened hostname and upstream lists.
func (s *Service) CaptureExistingIngress(ctx context.Context, targets []ExistingIngressTarget) ([]ExistingIngressBinding, error) {
	out := []ExistingIngressBinding{}
	knownDockerEdge := ""
	if hostexec.Available("nginx") {
		pending, err := s.Pending(ctx, "")
		if err != nil {
			return nil, errors.New("existing nginx configuration cannot be verified")
		}
		// An installed binary, stopped daemon or master reading another
		// configuration does not establish a route owned by this manager.
		if pending.Running {
			files, err := s.EffectiveConfig(ctx)
			if err != nil {
				return nil, errors.New("existing nginx configuration cannot be inspected")
			}
			tree, err := NginxTree(files)
			if err != nil {
				return nil, err
			}
			ready := pending.Running && pending.Reason == "" && pending.Problem == "" && len(pending.Files) == 0
			bindings := existingNginxBindings(tree, files, targets, ready)
			for i := range bindings {
				resolved, resolveErr := s.allowedPath(bindings[i].SourcePath)
				if resolveErr != nil {
					bindings[i].Status, bindings[i].Continuity, bindings[i].PlannedChange = "blocked", "unverified", "The original proxy source is outside its permitted configuration directory."
					continue
				}
				bindings[i].SourcePath = resolved
				bindings[i].SourceIdentity = ingressFileIdentity(resolved)
				bindings[i].ID = routeDigest("nginx\x00" + resolved + "\x00" + bindings[i].Selector + "\x00" + bindings[i].Hostname + "\x00" + bindings[i].Path + "\x00" + bindings[i].Service)
			}
			out = append(out, bindings...)
		}
	}
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return nil, err
	} else if edge != nil {
		knownDockerEdge = edge.ID
		containers, inspectErr := ingressContainers(ctx)
		if inspectErr != nil {
			return nil, errors.New("existing Caddy network ownership cannot be inspected")
		}
		networks := map[string]bool{}
		for _, container := range containers {
			if container.ID == edge.ID {
				for network := range container.NetworkSettings.Networks {
					networks[network] = true
				}
			}
		}
		sharedTargets := []ExistingIngressTarget{}
		for _, target := range targets {
			if target.Network != "" && networks[target.Network] {
				sharedTargets = append(sharedTargets, target)
			}
		}
		raw, err := edge.command(ctx, "", "wget", "-qO-", "http://127.0.0.1:2019/config/")
		if err != nil {
			return nil, errors.New("existing Caddy configuration cannot be inspected")
		}
		ready := edge.configurationSynced(ctx) == nil
		content, err := boundedIngressFile(edge.Source)
		if err != nil {
			return nil, err
		}
		bindings, err := existingCaddyBindings(raw, sharedTargets, "docker-caddy", edge.Name, edge.Source, content, ready)
		if err != nil {
			return nil, err
		}
		for i := range bindings {
			bindings[i].IngressIdentity, bindings[i].ContainerID = edge.Identity, edge.ID
			if bindings[i].Continuity == "network_alias" && ((bindings[i].CapturedStopped && s.verifyStoppedIngressAlias(ctx, bindings[i], containers) != nil) || (!bindings[i].CapturedStopped && !runningIngressAliasOwned(bindings[i], bindings[i].TargetContainerID, containers))) {
				bindings[i].Status, bindings[i].Continuity, bindings[i].PlannedChange = "blocked", "unverified", "This network alias is shared by another running container; isolate the exact application upstream before Deploy changes."
			}
		}
		out = append(out, bindings...)
	} else if hostexec.Available("caddy") {
		path, pathErr := s.allowedPath(s.caddyFile)
		if pathErr != nil {
			return nil, pathErr
		}
		content, err := boundedIngressFile(path)
		if err == nil {
			raw, err := hostexec.Command(ctx, "caddy", "adapt", "--config", s.caddyFile, "--adapter", "caddyfile").Output()
			if err != nil {
				return nil, errors.New("existing Caddy configuration cannot be inspected")
			}
			active, activeErr := nativeCaddyActive(ctx)
			ready := activeErr == nil && equalIngressJSON(raw, active)
			bindings, err := existingCaddyBindings(raw, targets, "caddy", "Caddy", path, content, ready)
			if err != nil {
				return nil, err
			}
			out = append(out, bindings...)
		}
	}
	if (s.dockerIngress || s.ingressResolve != nil) && hostexec.Available("docker") {
		if containers, err := ingressContainers(ctx); err == nil {
			out = append(out, unknownIngressHints(containers, targets, knownDockerEdge)...)
		}
	}
	for _, pidPath := range []string{"/run/apache2/apache2.pid", "/run/httpd/httpd.pid"} {
		text, err := boundedIngressFile(pidPath)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || pid <= 0 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
		if err == nil && (strings.TrimSpace(string(comm)) == "apache2" || strings.TrimSpace(string(comm)) == "httpd") {
			out = append(out, ExistingIngressBinding{ID: routeDigest("apache-hint\x00" + pidPath), Path: "/", Owner: "Apache", ProxyKind: "apache", Status: "hint", Continuity: "unverified", PlannedChange: "A host Apache server is running. Verify any domain or path associated with this workload in its active virtual-host configuration; the dashboard does not take over Apache routes."})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func unknownIngressHints(containers []ingressContainer, targets []ExistingIngressTarget, knownEdge string) []ExistingIngressBinding {
	bindings := []ExistingIngressBinding{}
	for _, container := range containers {
		if !container.State.Running || container.ID == knownEdge {
			continue
		}
		image := strings.ToLower(container.Config.Image)
		kind, owner := "", ""
		switch {
		case strings.Contains(image, "nginx-proxy-manager"):
			kind, owner = "npm", "Nginx Proxy Manager"
		case strings.Contains(image, "traefik"):
			kind, owner = "traefik", "Traefik"
		case strings.Contains(image, "httpd") || strings.Contains(image, "apache"):
			kind, owner = "apache", "Apache"
		case strings.Contains(image, "caddy"):
			kind, owner = "caddy", "Caddy"
		case strings.Contains(image, "nginx"):
			kind, owner = "nginx", "nginx"
		}
		if kind == "" {
			continue
		}
		relevant := false
		if kind != "nginx" && kind != "caddy" {
			for _, target := range targets {
				if _, present := container.NetworkSettings.Networks[target.Network]; target.Network != "" && present {
					relevant = true
				}
			}
		}
		for _, port := range []string{"80/tcp", "443/tcp"} {
			for _, mapping := range container.NetworkSettings.Ports[port] {
				relevant = relevant || mapping.HostPort == "80" || mapping.HostPort == "443"
			}
		}
		if !relevant {
			continue
		}
		bindings = append(bindings, ExistingIngressBinding{ID: routeDigest("hint\x00" + container.ID + "\x00" + kind), Hostname: "", Path: "/", Owner: owner, ProxyKind: kind, Status: "hint", Continuity: "unverified", ContainerID: container.ID, PlannedChange: "This existing proxy manager may route this workload. Verify its domain, middleware and upstream in the original manager before Deploy changes; its configuration remains externally owned."})
	}
	return bindings
}

// Only the conventional local admin endpoint is supported. Custom or disabled
// admin endpoints require review and never authorize file-backed replacement.
func nativeCaddyActive(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:2019/config/", nil)
	if err != nil {
		return nil, err
	}
	request.Close = true
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrExistingIngressChanged
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, ErrExistingIngressChanged
	}
	return raw, nil
}

func equalIngressJSON(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	aa, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(aa) == string(bb)
}

func nativeCaddySynced(ctx context.Context, path string) error {
	adapted, err := hostexec.Command(ctx, "caddy", "adapt", "--config", path, "--adapter", "caddyfile").Output()
	if err != nil {
		return err
	}
	active, err := nativeCaddyActive(ctx)
	if err != nil || !equalIngressJSON(adapted, active) {
		return ErrExistingIngressChanged
	}
	return nil
}

func exclusiveIngressAlias(b ExistingIngressBinding, containers []ingressContainer) bool {
	host, _, ok := ingressEndpoint(b.Upstream)
	if !ok {
		return false
	}
	count := 0
	for _, container := range containers {
		if !container.State.Running {
			continue
		}
		network, present := container.NetworkSettings.Networks[b.Network]
		if present && containsIngressString(network.Aliases, host) {
			count++
		}
	}
	return count == 1
}

func runningIngressAliasOwned(b ExistingIngressBinding, targetID string, containers []ingressContainer) bool {
	if targetID == "" || !exclusiveIngressAlias(b, containers) {
		return false
	}
	host, _, _ := ingressEndpoint(b.Upstream)
	for _, container := range containers {
		network, present := container.NetworkSettings.Networks[b.Network]
		if container.ID == targetID && container.State.Running && present && containsIngressString(network.Aliases, host) {
			return true
		}
	}
	return false
}

func (s *Service) verifyIngressAlias(ctx context.Context, b ExistingIngressBinding, containers []ingressContainer, allowStopped bool) error {
	if exclusiveIngressAlias(b, containers) {
		return nil
	}
	if !allowStopped {
		return ErrExistingIngressChanged
	}
	return s.verifyStoppedIngressAlias(ctx, b, containers)
}

func (s *Service) verifyStoppedIngressAlias(ctx context.Context, b ExistingIngressBinding, containers []ingressContainer) error {
	host, _, ok := ingressEndpoint(b.Upstream)
	if !ok || !b.CapturedStopped || len(b.TargetContainerID) != 64 {
		return ErrExistingIngressChanged
	}
	for _, r := range b.TargetContainerID {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return ErrExistingIngressChanged
		}
	}
	for _, container := range containers {
		if container.State.Running {
			if network, present := container.NetworkSettings.Networks[b.Network]; present && containsIngressString(network.Aliases, host) {
				return ErrExistingIngressChanged
			}
		}
	}
	raw, err := hostexec.Command(ctx, "docker", "inspect", "--", b.TargetContainerID).Output()
	if err != nil || len(raw) > 4<<20 {
		return ErrExistingIngressChanged
	}
	var captured []ingressContainer
	if json.Unmarshal(raw, &captured) != nil || len(captured) != 1 || !stoppedIngressAlias(b, captured[0]) {
		return ErrExistingIngressChanged
	}
	return nil
}

func stoppedIngressAlias(b ExistingIngressBinding, container ingressContainer) bool {
	host, _, ok := ingressEndpoint(b.Upstream)
	network, present := container.NetworkSettings.Networks[b.Network]
	return ok && b.CapturedStopped && container.ID == b.TargetContainerID && !container.State.Running && present && containsIngressString(network.Aliases, host)
}

func boundedIngressFile(path string) (string, error) {
	return boundedIngressFileLimit(path, 4<<20)
}

func boundedIngressFileLimit(path string, limit int64) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "", errors.New("existing proxy source is not a bounded regular file")
	}
	b := make([]byte, info.Size())
	if _, err := f.ReadAt(b, 0); err != nil && len(b) > 0 {
		return "", err
	}
	return string(b), nil
}

func newIngressBinding(host, path, upstream, driver, owner, source, selector, content string, target ExistingIngressTarget, mode string, ready bool) ExistingIngressBinding {
	b := ExistingIngressBinding{Hostname: host, Path: path, Service: target.Service, Owner: owner, ProxyKind: driver, Status: "linked", Continuity: mode, Upstream: upstream, SourcePath: source, SourceDigest: routeDigest(content), Selector: selector, Network: target.Network, Port: target.ContainerPort}
	b.SourceIdentity = ingressFileIdentity(source)
	if mode == "network_alias" {
		b.TargetContainerID, b.CapturedStopped = target.ContainerID, target.Stopped
	}
	b.ID = routeDigest(driver + "\x00" + source + "\x00" + selector + "\x00" + host + "\x00" + path + "\x00" + target.Service)
	if mode == "retarget" {
		b.PlannedChange = "Update only this route's literal upstream address at Deploy changes; preserve the existing proxy configuration."
	}
	if !ready {
		b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "Persist and verify the proxy's active configuration before Deploy changes."
	}
	return b
}

func ingressFileIdentity(path string) string {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return ingressInfoIdentity(info)
}

func ingressInfoIdentity(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return routeDigest(fmt.Sprintf("%d:%d", stat.Dev, stat.Ino))
}

func existingNginxBindings(tree []Directive, files []ConfigFile, targets []ExistingIngressTarget, ready bool) []ExistingIngressBinding {
	contents := map[string]string{}
	for _, file := range files {
		contents[filepath.Clean(file.Path)] = file.Content
	}
	out := []ExistingIngressBinding{}
	upstreams := map[string][]Directive{}
	var collect func([]Directive)
	collect = func(nodes []Directive) {
		for _, node := range nodes {
			if node.Name == "upstream" && len(node.Args) == 1 {
				upstreams[node.Args[0]] = node.Block
			}
			collect(node.Block)
		}
	}
	collect(tree)
	var walk func([]Directive)
	walk = func(nodes []Directive) {
		for _, node := range nodes {
			if node.Name != "server" {
				walk(node.Block)
				continue
			}
			hosts := []string{}
			tls := false
			for _, d := range node.Block {
				if d.Name == "server_name" {
					for _, host := range d.Args {
						if plannedIngressHost(host) {
							hosts = append(hosts, host)
						}
					}
				}
				if d.Name == "listen" {
					for _, word := range d.Args {
						tls = tls || word == "ssl" || word == "quic"
					}
				}
			}
			var routes func([]Directive, string)
			routes = func(children []Directive, path string) {
				for _, d := range children {
					if d.Name == "location" {
						if len(d.Args) > 0 {
							location := d.Args[len(d.Args)-1]
							if len(d.Args) > 1 && d.Args[0] != "^~" {
								location = strings.Join(d.Args, " ")
							}
							routes(d.Block, location)
						}
						continue
					}
					if d.Name != "proxy_pass" || len(d.Args) != 1 {
						continue
					}
					endpoint := d
					shared := false
					uri, _ := url.Parse(d.Args[0])
					if uri != nil && uri.Port() == "" && upstreams[uri.Hostname()] != nil {
						servers := []Directive{}
						for _, entry := range upstreams[uri.Hostname()] {
							if entry.Name == "server" && len(entry.Args) > 0 {
								servers = append(servers, entry)
							}
						}
						shared = len(servers) != 1
						for _, entry := range servers {
							if _, _, ok := ingressMatches(entry.Args[0], targets, false); ok {
								endpoint = entry
								break
							}
						}
					}
					target, mode, ok := ingressMatches(endpoint.Args[0], targets, false)
					if !ok {
						continue
					}
					content := contents[filepath.Clean(endpoint.File)]
					selectorKind := "nginx"
					if endpoint.Name == "server" {
						selectorKind = "nginx-upstream"
					}
					for _, host := range hosts {
						b := newIngressBinding(host, path, endpoint.Args[0], "nginx", filepath.Base(node.File), endpoint.File, fmt.Sprintf("%s:%d:%s", selectorKind, endpoint.Line, endpoint.Args[0]), content, target, mode, ready)
						b.HTTPS = tls
						if mode == "retarget" {
							if _, err := replaceIngressLiteral(content, b, b.Upstream, "http://127.0.0.1:1"); err != nil {
								b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "The exact persisted upstream cannot be safely isolated before Deploy changes."
							}
						}
						if shared {
							b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "This shared nginx upstream pool requires an explicit external proxy handoff before Deploy changes."
						}
						out = append(out, b)
					}
				}
			}
			routes(node.Block, "/")
		}
	}
	walk(tree)
	return out
}

func plannedIngressHost(host string) bool {
	return host != "" && host != "_" && !strings.ContainsAny(host, "$~* /\\\r\n\x00") && strings.Contains(host, ".")
}

func existingCaddyBindings(raw []byte, targets []ExistingIngressTarget, driver, owner, source, content string, ready bool) ([]ExistingIngressBinding, error) {
	var model map[string]any
	if len(raw) > 4<<20 || json.Unmarshal(raw, &model) != nil {
		return nil, errors.New("existing Caddy route tree is invalid")
	}
	out := []ExistingIngressBinding{}
	var routes func([]any, []string, []string, string, bool, bool)
	routes = func(nodes []any, inheritedHosts, inheritedPaths []string, pointer string, tls bool, inheritedUnverified bool) {
		for i, rawRoute := range nodes {
			route, ok := rawRoute.(map[string]any)
			if !ok {
				continue
			}
			hosts, paths := inheritedHosts, inheritedPaths
			unverified := inheritedUnverified
			if matches, ok := route["match"].([]any); ok {
				// Match objects are OR alternatives; their host/path pairing must
				// stay intact instead of forming a Cartesian product.
				for m, rawMatch := range matches {
					match, ok := rawMatch.(map[string]any)
					if !ok {
						continue
					}
					mh, mp := hosts, paths
					uncertain := unverified
					for key := range match {
						if key != "host" && key != "path" {
							uncertain = true
						}
					}
					if h, ok := match["host"].([]any); ok {
						mh = nil
						for _, v := range h {
							if text, ok := v.(string); ok && plannedIngressHost(text) && (len(hosts) == 0 || containsIngressString(hosts, text)) {
								mh = append(mh, text)
							}
						}
					}
					if p, ok := match["path"].([]any); ok {
						mp = nil
						for _, v := range p {
							if text, ok := v.(string); ok && strings.HasPrefix(text, "/") && compatibleIngressPath(paths, text) {
								mp = append(mp, text)
							}
						}
					}
					if _, present := match["host"]; present && len(mh) == 0 {
						continue
					}
					if _, present := match["path"]; present && len(mp) == 0 {
						continue
					}
					copy := map[string]any{"handle": route["handle"]}
					routes([]any{copy}, mh, mp, fmt.Sprintf("%s/%d/match/%d", pointer, i, m), tls, uncertain)
				}
				continue
			}
			handlers, _ := route["handle"].([]any)
			for n, rawHandler := range handlers {
				handler, ok := rawHandler.(map[string]any)
				if !ok {
					continue
				}
				slot := fmt.Sprintf("%s/%d/handle/%d", pointer, i, n)
				if children, ok := handler["routes"].([]any); ok {
					routes(children, hosts, paths, slot+"/routes", tls, unverified)
				}
				if handler["handler"] != "reverse_proxy" {
					continue
				}
				upstreams, _ := handler["upstreams"].([]any)
				for u, rawUpstream := range upstreams {
					upstream, ok := rawUpstream.(map[string]any)
					if !ok {
						continue
					}
					dial, _ := upstream["dial"].(string)
					target, mode, ok := ingressMatches(dial, targets, driver == "docker-caddy")
					if !ok {
						continue
					}
					if len(paths) == 0 {
						paths = []string{"/"}
					}
					for _, host := range hosts {
						for _, path := range paths {
							b := newIngressBinding(host, path, dial, driver, owner, source, fmt.Sprintf("%s/upstreams/%d", slot, u), content, target, mode, ready)
							b.HTTPS = tls
							if unverified {
								b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "This conditional Caddy matcher cannot be represented as an exact hostname and path; verify its original route before Deploy changes."
							}
							if len(upstreams) != 1 || handler["dynamic_upstreams"] != nil {
								b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "This shared or dynamic upstream needs an explicit external proxy handoff before Deploy changes."
							}
							if mode == "retarget" && !closedCaddyLiteral(content, dial) {
								b.Status, b.Continuity, b.PlannedChange = "blocked", "unverified", "The exact persisted upstream cannot be safely isolated; review the original proxy before Deploy changes."
							}
							out = append(out, b)
						}
					}
				}
			}
		}
	}
	apps, _ := model["apps"].(map[string]any)
	http, _ := apps["http"].(map[string]any)
	servers, _ := http["servers"].(map[string]any)
	for name, value := range servers {
		server, _ := value.(map[string]any)
		list, _ := server["routes"].([]any)
		tls := false
		if policies, ok := server["tls_connection_policies"].([]any); ok && len(policies) > 0 {
			tls = true
		}
		for _, listen := range anySlice(server["listen"]) {
			text, _ := listen.(string)
			tls = tls || strings.HasSuffix(text, ":443")
		}
		routes(list, nil, nil, "/apps/http/servers/"+name+"/routes", tls, false)
	}
	return out, nil
}

func anySlice(value any) []any { result, _ := value.([]any); return result }

func containsIngressString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func compatibleIngressPath(parents []string, child string) bool {
	if len(parents) == 0 {
		return true
	}
	for _, parent := range parents {
		if parent == child || parent == "/*" || (strings.HasSuffix(parent, "*") && strings.HasPrefix(child, strings.TrimSuffix(parent, "*"))) {
			return true
		}
	}
	return false
}

func closedCaddyLiteral(content, dial string) bool {
	count := 0
	total := 0
	for _, line := range strings.Split(content, "\n") {
		words := strings.Fields(strings.TrimSpace(line))
		if len(words) > 0 && (words[0] == "import" || strings.HasPrefix(words[0], "(")) {
			return false
		}
		if len(words) > 0 && words[0] == "reverse_proxy" {
			total++
		}
		if (len(words) == 2 || (len(words) == 3 && words[2] == "{")) && words[0] == "reverse_proxy" && words[1] == dial {
			count++
		}
	}
	return count == 1 && total == 1
}

func ingressReplacement(raw, host string, port int) (string, error) {
	if net.ParseIP(host) == nil || port < 1 || port > 65535 {
		return "", errors.New("candidate ingress address is invalid")
	}
	if !strings.Contains(raw, "://") {
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return "", errors.New("original ingress address is invalid")
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	return u.String(), nil
}
