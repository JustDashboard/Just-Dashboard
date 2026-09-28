package dockerx

import (
	"context"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// HostPortBinding is a host port a container publishes or will publish when
// it starts again.
type HostPortBinding struct {
	Container string `json:"container"`
	HostIP    string `json:"hostIp,omitempty"`
	HostPort  int    `json:"hostPort"`
	Protocol  string `json:"protocol"`
	Running   bool   `json:"running"`
}

// HostPortBindings is every host port the containers on this host hold or
// keep for their next start. A stopped container holds no socket, so only
// its configuration says a port is spoken for; a binding left for Docker to
// choose names no port and is left out.
func (c *Client) HostPortBindings(ctx context.Context) ([]HostPortBinding, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	items, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := []HostPortBinding{}
	for _, it := range items {
		name := it.ID
		if names := trimNames(it.Names); len(names) > 0 {
			name = names[0]
		}
		if it.State == "running" {
			for _, p := range it.Ports {
				if p.PublicPort > 0 {
					out = append(out, HostPortBinding{Container: name, HostIP: p.IP, HostPort: int(p.PublicPort), Protocol: p.Type, Running: true})
				}
			}
			continue
		}
		insp, err := cli.ContainerInspect(ctx, it.ID)
		if err != nil || insp.HostConfig == nil {
			continue
		}
		for portSpec, bindings := range insp.HostConfig.PortBindings {
			for _, b := range bindings {
				// A range ("8000-8010") keeps every port in it.
				low, high, _ := strings.Cut(b.HostPort, "-")
				first, err := strconv.Atoi(low)
				if err != nil || first == 0 {
					continue
				}
				last := first
				if high != "" {
					if last, err = strconv.Atoi(high); err != nil || last < first || last > 65535 {
						continue
					}
				}
				for port := first; port <= last; port++ {
					out = append(out, HostPortBinding{Container: name, HostIP: b.HostIP, HostPort: port, Protocol: portSpec.Proto()})
				}
			}
		}
	}
	return out, nil
}
