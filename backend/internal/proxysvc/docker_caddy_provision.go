package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const managedIngressName = "just-dashboard-ingress"
const managedIngressSource = "/etc/just-dashboard/ingress/Caddyfile"
const managedIngressConfig = "{\n  admin 127.0.0.1:2019\n}\n"

// Planning observes sockets and Docker mappings without binding or provisioning.
// A competing bind between this check and run is still rejected by Docker.
func (s *Service) canProvisionIngress(ctx context.Context) bool {
	if !s.dockerIngress || !hostexec.Available("docker") {
		return false
	}
	listeners, err := ListListeners(ctx)
	if err != nil {
		return false
	}
	for _, listener := range listeners {
		if listener.Protocol == "tcp" && (listener.Port == 80 || listener.Port == 443) {
			return false
		}
	}
	containers, err := ingressContainers(ctx)
	if err != nil {
		return false
	}
	for _, container := range containers {
		for target, bindings := range container.NetworkSettings.Ports {
			if !strings.HasSuffix(target, "/tcp") {
				continue
			}
			for _, binding := range bindings {
				if binding.HostPort == "80" || binding.HostPort == "443" {
					return false
				}
			}
		}
	}
	return true
}

// Called under the route mutex, only from an authorized deployment mutation.
func (s *Service) provisionIngress(ctx context.Context) (*dockerCaddy, error) {
	if edge, err := s.dockerCaddy(ctx); err != nil || edge != nil {
		return edge, err
	}
	if !s.canProvisionIngress(ctx) {
		return nil, nil
	}
	if _, err := startDockerIngress(ctx, managedIngressName, managedIngressSource, "80:80/tcp", "443:443/tcp", "just-dashboard-ingress-config", "just-dashboard-ingress-data"); err != nil {
		return nil, err
	}
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		edge, err := s.dockerCaddy(ready)
		if err == nil && edge != nil && edge.Name == managedIngressName && edge.configurationSynced(ready) == nil {
			return edge, nil
		}
		select {
		case <-ready.Done():
			return nil, errors.New("deployment ingress did not become ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func startDockerIngress(ctx context.Context, name, source, httpBinding, httpsBinding, configVolume, dataVolume string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		return "", err
	}
	file, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err == nil {
		_, writeErr := file.WriteString(managedIngressConfig)
		err = errors.Join(writeErr, file.Sync(), file.Close())
	} else if errors.Is(err, os.ErrExist) {
		// Reuse only our persisted layout. Arbitrary configuration at this path
		// is never overwritten or adopted by a fresh installation.
		raw, readErr := os.ReadFile(source)
		err = readErr
		if err == nil && !strings.HasPrefix(string(raw), managedIngressConfig) {
			return "", errors.New("managed ingress configuration path is already owned")
		}
	}
	if err != nil {
		return "", err
	}
	// Reuse an explicitly labelled stopped ingress left by a prior installation.
	owned, inspectErr := hostexec.Command(ctx, "docker", "inspect", name).Output()
	if inspectErr == nil {
		var existing []struct {
			ID     string
			Config struct{ Labels map[string]string }
			Mounts []struct{ Source, Destination, Name string }
		}
		if json.Unmarshal(owned, &existing) != nil || len(existing) != 1 || existing[0].Config.Labels["com.just-dashboard.ingress"] != "true" {
			return "", errors.New("managed ingress container name is already owned")
		}
		mounts := map[string]string{}
		for _, mount := range existing[0].Mounts {
			if mount.Name != "" {
				mounts[mount.Destination] = mount.Name
			} else {
				mounts[mount.Destination] = mount.Source
			}
		}
		if mounts["/etc/caddy/Caddyfile"] != source || mounts["/config"] != configVolume || mounts["/data"] != dataVolume {
			return "", errors.New("managed ingress persistent storage was changed")
		}
		_, err = hostexec.Command(ctx, "docker", "start", name).Output()
		if err == nil {
			return existing[0].ID, nil
		}
	} else {
		var output []byte
		output, err = hostexec.Command(ctx, "docker", "run", "-d", "--name", name,
			"--label", "com.just-dashboard.ingress=true", "--restart", "unless-stopped",
			"-p", httpBinding, "-p", httpsBinding,
			"-v", source+":/etc/caddy/Caddyfile:ro",
			"-v", configVolume+":/config", "-v", dataVolume+":/data",
			"caddy:2-alpine").Output()
		if err == nil {
			return strings.TrimSpace(string(output)), nil
		}
	}
	return "", errors.New("could not start deployment ingress; public port ownership may have changed")
}
