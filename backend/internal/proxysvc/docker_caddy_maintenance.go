package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaintainDockerIngress repairs only targets persisted by an authorized route
// activation. Container identity prevents a reused host port selecting a different
// application. Reads never reconnect networks; this lifecycle worker owns repair.
func (s *Service) MaintainDockerIngress(ctx context.Context, record func(string, bool), report func(error)) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		pass, cancel := context.WithTimeout(ctx, 25*time.Second)
		var err error
		if s.mu.TryLock() {
			edge, discoverErr := s.dockerCaddy(pass)
			err = discoverErr
			if err == nil && edge != nil {
				_, err = edge.reconcile(pass, record)
			}
			s.mu.Unlock()
		}
		cancel()
		if err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func parseDockerCaddyTarget(content string) (dockerCaddyTarget, bool) {
	return parseDockerCaddyTargetLine(content, dockerCaddyTargetPrefix)
}

// parseDockerCaddyAuthTarget reads the auth server a route signs in through,
// for a route that has one Caddy reaches over a container network.
func parseDockerCaddyAuthTarget(content string) (dockerCaddyTarget, bool) {
	return parseDockerCaddyTargetLine(content, dockerCaddyAuthTargetPrefix)
}

func parseDockerCaddyTargetLine(content, prefix string) (dockerCaddyTarget, bool) {
	if !strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		return dockerCaddyTarget{}, false
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var target dockerCaddyTarget
		if json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &target) != nil {
			return target, false
		}
		port, err := strconv.Atoi(target.Port)
		if err != nil || port < 1 || port > 65535 || len(target.ContainerID) != 64 || target.Network == "" {
			return target, false
		}
		return target, true
	}
	return dockerCaddyTarget{}, false
}

// reconcile is one pass over the managed routes. It answers with the routes
// whose certificate copy it refreshed, for a renewal job to report.
func (c *dockerCaddy) reconcile(ctx context.Context, record func(string, bool)) ([]string, error) {
	// An unintegrated proxy has nothing to repair and must not be modified.
	evidence, err := c.command(ctx, "", "sh", "-c", `if [ -d "$1" ]; then printf present; else printf absent; fi`, "sh", dockerCaddyRoot+"/routes")
	if err != nil {
		return nil, err
	}
	if string(evidence) == "absent" {
		return nil, nil
	}
	if string(evidence) != "present" {
		return nil, ErrRouteRecovery
	}
	return c.reconcileRoutes(ctx, record)
}

func (c *dockerCaddy) reconcileRoutes(ctx context.Context, record func(string, bool)) ([]string, error) {
	raw, err := c.command(ctx, "", "find", dockerCaddyRoot+"/routes", "-maxdepth", "1", "-type", "f", "-name", "just-dashboard-*.caddy")
	if err != nil {
		return nil, err
	}
	containers, err := ingressContainers(ctx)
	if err != nil {
		return nil, err
	}
	var refreshed []string
	// The copy each route serves as the pass leaves it — the one it was
	// moved to, or the one it had when the move was skipped or failed — so
	// the prune at the end keeps what is in use, two routes sharing a copy
	// included; a pass that stops early prunes nothing.
	serves := map[string]string{}
	for _, path := range strings.Fields(string(raw)) {
		name := strings.TrimSuffix(filepath.Base(path), ".caddy")
		wantPath, err := dockerCaddyRoutePath(name)
		if err != nil || wantPath != path {
			return refreshed, ErrRouteRecovery
		}
		snapshot, err := c.snapshot(ctx, name)
		if err != nil {
			return refreshed, err
		}
		// The certificate the route serves against the one the host holds:
		// a lineage certbot renewed since the copy was taken is copied again
		// and the route moved to it, and a route written before its source
		// was recorded has it recorded (docker_caddy_copies.go).
		certificate, servesCopy := planCertificateRefresh(snapshot.Content)
		if servesCopy {
			serves[name] = certificate.served
		}
		target, ok := parseDockerCaddyTarget(snapshot.Content)
		if !ok {
			continue
		}
		next, found, connected := c.locateTarget(target, containers)
		// The auth server a route signs in through is repaired the same way:
		// a Caddy recreated without its network would refuse every visitor.
		// One that is gone is left for the next activation to report.
		auth, gated := parseDockerCaddyAuthTarget(snapshot.Content)
		nextAuth, authFound, authConnected := auth, false, false
		if gated {
			nextAuth, authFound, authConnected = c.locateTarget(auth, containers)
		}
		authRepair := authFound && (!authConnected || nextAuth != auth)
		// A route written before request recording existed serves its site
		// unrecorded until something rewrites it, and nothing would until the
		// next deploy. This pass is the something: the block is added here,
		// through the same write, reload and restore a repair takes, so an
		// upgrade turns recording on for every managed route within a tick.
		unrecorded := !hasAccessLog(snapshot.Content)
		if !found || (connected && next == target && !unrecorded && !authRepair && !certificate.changed()) {
			continue
		}
		if err := c.configurationSynced(ctx); err != nil {
			return refreshed, err
		}
		var repairErr error
		if !connected || next != target {
			_, repairErr = c.connectTarget(ctx, next)
		}
		if repairErr == nil && authRepair {
			_, repairErr = c.connectTarget(ctx, nextAuth)
		}
		// Every occurrence: a route whose names are served over both
		// schemes proxies to the application from two site blocks.
		content, rewrite := snapshot.Content, false
		if repairErr == nil && next != target {
			content = strings.ReplaceAll(content, "reverse_proxy "+strconv.Quote(target.upstream()), "reverse_proxy "+strconv.Quote(next.upstream()))
			content = strings.Replace(content, target.metadata(), next.metadata(), 1)
			rewrite = true
		}
		if repairErr == nil && authRepair && nextAuth != auth {
			content = strings.ReplaceAll(content, "forward_auth "+strconv.Quote(auth.upstream()), "forward_auth "+strconv.Quote(nextAuth.upstream()))
			content = strings.Replace(content, auth.authMetadata(), nextAuth.authMetadata(), 1)
			rewrite = true
		}
		if repairErr == nil && unrecorded {
			upgraded, changed, err := withAccessLog(name, content)
			if err == nil && changed {
				content, rewrite = upgraded, true
				repairErr = c.ensureAccessLogDir(ctx)
			}
		}
		if repairErr == nil && certificate.changed() {
			// The copy goes in before the route that names it, so Caddy
			// never reloads onto a path that is not there yet.
			if repairErr = c.writeCertificateCopy(ctx, certificate); repairErr == nil {
				content, rewrite = certificate.apply(content), true
			}
		}
		if repairErr == nil && rewrite {
			repairErr = c.write(ctx, path, content)
			if repairErr == nil {
				repairErr = c.reload(ctx)
			}
			if repairErr != nil {
				recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				repairErr = errors.Join(repairErr, c.restore(recovery, snapshot))
				cancel()
			}
		}
		if record != nil {
			record(name, repairErr == nil)
		}
		if repairErr != nil {
			return refreshed, repairErr
		}
		if certificate.cert != nil {
			serves[name] = certificate.hash
			refreshed = append(refreshed, name)
		}
	}
	referenced := map[string]bool{}
	for _, hash := range serves {
		referenced[hash] = true
	}
	return refreshed, c.pruneCertificateCopies(ctx, referenced)
}

// locateTarget finds a persisted target's container where it is now: its
// address on the target's network, and whether Caddy is still on that
// network. Container identity, not the published port, decides, so a port
// reused by another container never redirects the route.
func (c *dockerCaddy) locateTarget(target dockerCaddyTarget, containers []ingressContainer) (next dockerCaddyTarget, found, connected bool) {
	next = target
	for _, container := range containers {
		if container.ID == c.ID {
			_, connected = container.NetworkSettings.Networks[target.Network]
		}
		if container.ID != target.ContainerID {
			continue
		}
		if network, ok := container.NetworkSettings.Networks[target.Network]; ok && network.IPAddress != "" {
			next.Address = network.IPAddress
			found = true
		}
	}
	return next, found, connected
}

// hasAccessLog reports whether a managed route already records its requests.
func hasAccessLog(content string) bool {
	return strings.Contains(content, "\n  log {\n")
}

// withAccessLog adds the `log` block to a managed route that has none, placed
// where activation renders it — directly inside the site's opening line, ahead
// of everything else the block holds. The rest of the file is untouched: the
// target metadata the next pass parses, the certificate and credentials the
// route was activated with. It answers false for a route that already records,
// and refuses anything not written by this dashboard.
func withAccessLog(name, content string) (string, bool, error) {
	if !strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		return content, false, errors.New("not a managed Caddy route")
	}
	if hasAccessLog(content) {
		return content, false, nil
	}
	directive, err := renderAccessLogDirective(name)
	if err != nil {
		return content, false, err
	}
	open := strings.Index(content, " {\n")
	if open < 0 {
		return content, false, errors.New("managed Caddy route has no site block")
	}
	at := open + len(" {\n")
	return content[:at] + directive + content[at:], true, nil
}
