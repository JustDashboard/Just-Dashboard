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
				err = edge.reconcile(pass, record)
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
	if !strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		return dockerCaddyTarget{}, false
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, dockerCaddyTargetPrefix) {
			continue
		}
		var target dockerCaddyTarget
		if json.Unmarshal([]byte(strings.TrimPrefix(line, dockerCaddyTargetPrefix)), &target) != nil {
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

func (c *dockerCaddy) reconcile(ctx context.Context, record func(string, bool)) error {
	// An unintegrated proxy has nothing to repair and must not be modified.
	evidence, err := c.command(ctx, "", "sh", "-c", `if [ -d "$1" ]; then printf present; else printf absent; fi`, "sh", dockerCaddyRoot+"/routes")
	if err != nil {
		return err
	}
	if string(evidence) == "absent" {
		return nil
	}
	if string(evidence) != "present" {
		return ErrRouteRecovery
	}
	return c.reconcileRoutes(ctx, record)
}

func (c *dockerCaddy) reconcileRoutes(ctx context.Context, record func(string, bool)) error {
	raw, err := c.command(ctx, "", "find", dockerCaddyRoot+"/routes", "-maxdepth", "1", "-type", "f", "-name", "just-dashboard-*.caddy")
	if err != nil {
		return err
	}
	containers, err := ingressContainers(ctx)
	if err != nil {
		return err
	}
	for _, path := range strings.Fields(string(raw)) {
		name := strings.TrimSuffix(filepath.Base(path), ".caddy")
		wantPath, err := dockerCaddyRoutePath(name)
		if err != nil || wantPath != path {
			return ErrRouteRecovery
		}
		snapshot, err := c.snapshot(ctx, name)
		if err != nil {
			return err
		}
		target, ok := parseDockerCaddyTarget(snapshot.Content)
		if !ok {
			continue
		}
		next := target
		found := false
		connected := false
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
		if !found || (connected && next == target) {
			continue
		}
		if err := c.configurationSynced(ctx); err != nil {
			return err
		}
		_, repairErr := c.connectTarget(ctx, next)
		if repairErr == nil && next != target {
			content := strings.Replace(snapshot.Content, "reverse_proxy "+strconv.Quote(target.upstream()), "reverse_proxy "+strconv.Quote(next.upstream()), 1)
			content = strings.Replace(content, target.metadata(), next.metadata(), 1)
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
			return repairErr
		}
	}
	return nil
}
