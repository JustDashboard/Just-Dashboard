package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

func caddyHostnames(value any) []string {
	names := []string{}
	switch node := value.(type) {
	case map[string]any:
		if hosts, ok := node["host"].([]any); ok {
			for _, host := range hosts {
				if name, ok := host.(string); ok {
					names = append(names, name)
				}
			}
		}
		for _, child := range node {
			names = append(names, caddyHostnames(child)...)
		}
	case []any:
		for _, child := range node {
			names = append(names, caddyHostnames(child)...)
		}
	}
	return names
}

func (c *dockerCaddy) vhosts(ctx context.Context) ([]VHost, error) {
	active, err := c.command(ctx, "", "wget", "-qO-", "http://127.0.0.1:2019/config/")
	if err != nil {
		return nil, err
	}
	var config any
	if json.Unmarshal(active, &config) != nil {
		return nil, errors.New("could not read Caddy route inventory")
	}
	hosts := map[string]bool{}
	for _, name := range caddyHostnames(config) {
		hosts[name] = true
	}
	// Missing managed storage means this is an existing, unintegrated server.
	raw, err := c.command(ctx, "", "sh", "-c", `if [ -d "$1" ]; then find "$1" -maxdepth 1 -type f -name 'just-dashboard-*.caddy'; fi`, "sh", dockerCaddyRoot+"/routes")
	if err != nil {
		return nil, err
	}
	sites := []VHost{}
	for _, path := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if path == "" {
			continue
		}
		content, exists, err := c.read(ctx, path)
		if err != nil {
			return nil, err
		}
		if !exists || !strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
			continue
		}
		// Adapt the closed managed fragment so inventory follows Caddy's parser.
		adapted, err := c.command(ctx, content, "caddy", "adapt", "--config", "-", "--adapter", "caddyfile")
		if err != nil {
			return nil, err
		}
		var own any
		if json.Unmarshal(adapted, &own) != nil {
			return nil, ErrInvalidConf
		}
		names := caddyHostnames(own)
		enabled := len(names) > 0
		for _, name := range names {
			enabled = enabled && hosts[name]
			delete(hosts, name)
		}
		site := VHost{Name: strings.TrimSuffix(filepath.Base(path), ".caddy"), Kind: KindCaddy, Enabled: enabled, ServerNames: names, TLS: !strings.Contains(content, "http://"+firstName(names)), Listen: []string{"80", "443"}, Upstreams: []string{}}
		if target, ok := parseDockerCaddyTarget(content); ok {
			site.Upstreams = []string{target.upstream()}
		}
		sites = append(sites, site)
	}
	for name := range hosts {
		sites = append(sites, VHost{Name: "docker-caddy:" + c.Name + ":" + name, Kind: KindCaddy, Enabled: true, ServerNames: []string{name}, Listen: []string{"80", "443"}, Upstreams: []string{}})
	}
	return sites, nil
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
