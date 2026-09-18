package dockerx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLiveComposeDatabaseNetworkMerge(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	root := t.TempDir()
	source := "services:\n  web:\n    image: caddy:2-alpine\n    networks:\n      private:\n        aliases: [internal-api]\n  worker:\n    image: caddy:2-alpine\nnetworks:\n  private:\n    internal: true\n"
	if err := os.WriteFile(filepath.Join(root, "compose.yml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	owner := New("unix:///var/run/docker.sock")
	spec := ComposeReleaseSpec{ProjectName: "jd-network-merge", ProjectDirectory: root, Files: []string{"compose.yml"}}
	content, err := owner.ComposeRuntimeNetworks(context.Background(), spec, "services:\n  web:\n    image: caddy:2-alpine\n  worker:\n    image: caddy:2-alpine\n", []string{"jd-fixture-database"})
	if err != nil {
		t.Fatal(err)
	}
	spec.OverrideFile = filepath.Join(root, "runtime.yml")
	if err := os.WriteFile(spec.OverrideFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := owner.composeReleaseConfiguration(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Services map[string]struct {
			Networks map[string]any `json:"networks"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "worker"} {
		if _, ok := config.Services[name].Networks["jd_managed_jd-fixture-database"]; !ok {
			t.Fatal("managed network missing from final Compose model")
		}
	}
	if _, ok := config.Services["web"].Networks["private"]; !ok {
		t.Fatal("private network was lost")
	}
	if _, ok := config.Services["worker"].Networks["default"]; !ok {
		t.Fatal("default network was lost")
	}
}

func TestComposeDatabaseNetworkPreservesExistingAliasesAndAddresses(t *testing.T) {
	raw := []byte(`{"services":{"web":{"networks":{"private":{"aliases":["internal-api"],"ipv4_address":"172.30.0.9","priority":10}}},"worker":{"networks":{"default":null}}},"networks":{"private":{},"default":{}}}`)
	override := "services:\n  web:\n    image: fixture@sha256:abc\n    labels:\n      io.just-dashboard.managed: \"true\"\n  worker:\n    image: fixture@sha256:def\n"
	text, err := composeRuntimeNetworks(raw, override, []string{"jd-e7-db-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Services map[string]struct {
			Image    string         `yaml:"image"`
			Networks map[string]any `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]struct {
			Name     string `yaml:"name"`
			External bool   `yaml:"external"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatal(err)
	}
	private := parsed.Services["web"].Networks["private"].(map[string]any)
	if private["ipv4_address"] != "172.30.0.9" || private["aliases"].([]any)[0] != "internal-api" || parsed.Services["web"].Image != "fixture@sha256:abc" {
		t.Fatalf("existing network configuration changed: %s", text)
	}
	for _, service := range parsed.Services {
		if _, ok := service.Networks["jd_managed_jd-e7-db-fixture"]; !ok {
			t.Fatal("service was not attached to database network")
		}
	}
	net := parsed.Networks["jd_managed_jd-e7-db-fixture"]
	if !net.External || net.Name != "jd-e7-db-fixture" {
		t.Fatal("database network was not declared external")
	}
}

func TestComposeDatabaseNetworkRejectsNamespaceSharingAndReservedKeys(t *testing.T) {
	for _, raw := range []string{
		`{"services":{"web":{"network_mode":"host"}}}`,
		`{"services":{"web":{"network_mode":"service:database"}}}`,
		`{"services":{"web":{}},"networks":{"jd_managed_jd-e7-db-fixture":{}}}`,
	} {
		if _, err := composeRuntimeNetworks([]byte(raw), "services:\n  web:\n    image: fixture\n", []string{"jd-e7-db-fixture"}); err == nil {
			t.Fatal("unsafe network composition accepted")
		}
	}
	if _, err := composeRuntimeNetworks([]byte(`{"services":{"web":{}}}`), "services:\n  web:\n    image: fixture\n", []string{strings.Repeat("../", 3)}); err == nil {
		t.Fatal("invalid network name accepted")
	}
}
