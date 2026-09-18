package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
)

// Compose's normalized network maps preserve existing aliases, addresses and
// priorities while an external managed network is added before services start.
func (c *Client) ComposeRuntimeNetworks(ctx context.Context, spec ComposeReleaseSpec, override string, networks []string) (string, error) {
	raw, err := c.composeReleaseConfiguration(ctx, spec)
	if err != nil {
		return "", err
	}
	return composeRuntimeNetworks(raw, override, networks)
}

func composeRuntimeNetworks(raw []byte, override string, networks []string) (string, error) {
	var config struct {
		Services map[string]struct {
			NetworkMode string         `json:"network_mode"`
			Networks    map[string]any `json:"networks"`
		} `json:"services"`
		Networks map[string]any `json:"networks"`
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Services) == 0 {
		return "", errors.New("Compose returned no service networks")
	}
	var result map[string]any
	if yaml.Unmarshal([]byte(override), &result) != nil {
		return "", errors.New("invalid deployment override")
	}
	services, ok := result["services"].(map[string]any)
	if !ok {
		return "", errors.New("deployment override has no services")
	}
	added := map[string]any{}
	for _, network := range networks {
		if !validResourceName(network) {
			return "", errors.New("invalid managed network name")
		}
		key := "jd_managed_" + network
		if _, exists := config.Networks[key]; exists {
			return "", errors.New("Compose uses a reserved managed network key")
		}
		added[key] = map[string]any{"external": true, "name": network}
	}
	for name, service := range config.Services {
		entry, ok := services[name].(map[string]any)
		if !ok {
			return "", errors.New("Compose service identity changed after resolution")
		}
		if service.NetworkMode != "" {
			return "", fmt.Errorf("Compose service %s uses network_mode and cannot join a managed database network", name)
		}
		if service.Networks == nil {
			service.Networks = map[string]any{"default": nil}
		}
		for name := range added {
			service.Networks[name] = nil
		}
		entry["networks"] = service.Networks
	}
	result["networks"] = added
	out, err := yaml.Marshal(result)
	return string(out), err
}
