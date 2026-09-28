package dockerx

import (
	"context"

	"github.com/docker/docker/api/types/container"
)

// ListRunning is the running containers as the daemon's listing names them:
// id, name, image, labels, published ports and mounts, and nothing that
// needs an inspect. ListContainers inspects every running container for its
// uptime and limits, which the ports page, asking which container a socket
// belongs to every fifteen seconds, has no use for.
func (c *Client) ListRunning(ctx context.Context) ([]Container, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	items, err := cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(items))
	for _, it := range items {
		cn := Container{
			ID:           it.ID,
			Names:        trimNames(it.Names),
			Image:        it.Image,
			State:        it.State,
			Labels:       it.Labels,
			Ports:        make([]Port, 0, len(it.Ports)),
			ComposeStack: it.Labels["com.docker.compose.project"],
			ComposeSvc:   it.Labels["com.docker.compose.service"],
		}
		if len(cn.Names) > 0 {
			cn.Name = cn.Names[0]
		}
		for _, p := range it.Ports {
			cn.Ports = append(cn.Ports, Port{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
		}
		for _, m := range it.Mounts {
			cn.Mounts = append(cn.Mounts, MountPoint{Type: string(m.Type), Source: m.Source, Destination: m.Destination})
		}
		out = append(out, cn)
	}
	return out, nil
}
