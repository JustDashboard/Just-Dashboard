package dockerx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const composePreviewReleaseOverride = "services:\n  web:\n    image: \"sha256:aaa\"\n    pull_policy: never\n    labels:\n      io.just-dashboard.managed: \"true\"\n  db:\n    image: \"sha256:bbb\"\n    pull_policy: never\n    labels:\n      io.just-dashboard.managed: \"true\"\n"

func composePreviewIsolationFixture() ComposePreviewIsolation {
	return ComposePreviewIsolation{
		Network: "jd-preview-e7", Networks: []string{"jd-e7-db-3"}, Public: "web",
		Publish: PortMapping{HostIP: "127.0.0.1", HostPort: 41001, ContainerPort: 8080},
		Volume: func(key string) string {
			return "jd-preview-e7-" + strings.NewReplacer(":", "-", "/", "-").Replace(key)
		},
		Labels: map[string]string{"io.just-dashboard.preview": "true"},
	}
}

// The resolved document as `docker compose config --format json` writes it:
// absolute bind sources, a named volume, an external volume with production's
// name, ports, a fixed container name, a secret and a network alias.
func composePreviewConfigFixture(t *testing.T, root string) []byte {
	t.Helper()
	config := map[string]any{
		"services": map[string]any{
			"web": map[string]any{
				"container_name": "production-web",
				"ports":          []any{map[string]any{"target": 8080, "published": "80", "protocol": "tcp"}},
				"networks":       map[string]any{"front": nil, "back": map[string]any{"aliases": []any{"api.internal"}}},
				"secrets":        []any{map[string]any{"source": "token"}},
				"external_links": []any{"production-cache:cache"},
				"volumes": []any{
					map[string]any{"type": "volume", "source": "uploads", "target": "/uploads"},
					map[string]any{"type": "bind", "source": filepath.Join(root, "nginx.conf"), "target": "/etc/nginx.conf", "read_only": true},
					map[string]any{"type": "bind", "source": "/etc/hostname", "target": "/host-name", "read_only": true},
					map[string]any{"type": "bind", "source": "/srv/production/cache", "target": "/cache"},
					map[string]any{"type": "volume", "target": "/scratch"},
					map[string]any{"type": "tmpfs", "target": "/tmp"},
				},
				"labels": map[string]any{"com.example.team": "web"},
			},
			"db": map[string]any{
				"ports":    []any{map[string]any{"target": 5432, "published": "5432", "protocol": "tcp"}},
				"networks": map[string]any{"back": nil},
				"volumes":  []any{map[string]any{"type": "volume", "source": "dbdata", "target": "/var/lib/postgresql/data"}},
			},
		},
		"networks": map[string]any{"front": map[string]any{"name": "jd-e7_front"}, "back": map[string]any{"name": "production-back"}},
		"volumes":  map[string]any{"uploads": map[string]any{"name": "jd-e7_uploads"}, "dbdata": map[string]any{"name": "production-db", "external": true}},
		"secrets":  map[string]any{"token": map[string]any{"file": filepath.Join(root, "token.txt")}},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestComposePreviewOverrideIsolatesEveryService(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("events {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := composePreviewConfigFixture(t, root)
	isolation := composePreviewIsolationFixture()
	result, err := composePreviewOverride(raw, composePreviewReleaseOverride, root, isolation)
	if err != nil {
		t.Fatal(err)
	}
	again, err := composePreviewOverride(raw, composePreviewReleaseOverride, root, isolation)
	if err != nil || again.Override != result.Override {
		t.Fatalf("the override is not deterministic, so a retried start would refuse its own file: %v", err)
	}
	for _, tag := range []string{"!override", "!reset"} {
		if !strings.Contains(result.Override, tag) {
			t.Fatalf("override lacks %s:\n%s", tag, result.Override)
		}
	}
	// The tags are Compose's merge instructions; read the values beneath them.
	var document map[string]any
	if err := yaml.Unmarshal([]byte(result.Override), &document); err != nil {
		t.Fatal(err)
	}
	services := document["services"].(map[string]any)
	web, db := services["web"].(map[string]any), services["db"].(map[string]any)

	if got := web["ports"].([]any); len(got) != 1 || got[0].(map[string]any)["host_ip"] != "127.0.0.1" ||
		got[0].(map[string]any)["published"] != "41001" || got[0].(map[string]any)["target"] != 8080 {
		t.Fatalf("public service ports = %v", web["ports"])
	}
	if got, ok := db["ports"].([]any); !ok || len(got) != 0 {
		t.Fatalf("a private service kept a published port: %v", db["ports"])
	}
	wantWebVolumes := []any{
		map[string]any{"type": "volume", "source": "jd-preview-e7-volume-uploads", "target": "/uploads"},
		map[string]any{"type": "bind", "source": mustEvalSymlinks(t, filepath.Join(root, "nginx.conf")), "target": "/etc/nginx.conf", "read_only": true},
		map[string]any{"type": "volume", "source": "jd-preview-e7-bind--srv-production-cache", "target": "/cache"},
		map[string]any{"type": "volume", "source": "jd-preview-e7-anonymous-web--scratch", "target": "/scratch"},
		map[string]any{"type": "tmpfs", "target": "/tmp"},
	}
	if !reflect.DeepEqual(web["volumes"], wantWebVolumes) {
		t.Fatalf("web volumes = %#v", web["volumes"])
	}
	if !reflect.DeepEqual(db["volumes"], []any{map[string]any{"type": "volume", "source": "jd-preview-e7-volume-dbdata", "target": "/var/lib/postgresql/data"}}) {
		t.Fatalf("db volumes = %#v (production's external volume must not be reached)", db["volumes"])
	}
	if strings.Contains(result.Override, "production-db") || strings.Contains(result.Override, "/srv/production") && !strings.Contains(result.Override, "bind--srv-production-cache") {
		t.Fatalf("override names production storage:\n%s", result.Override)
	}
	if !reflect.DeepEqual(result.LeftOut, []string{"web:/host-name"}) {
		t.Fatalf("left out = %v", result.LeftOut)
	}
	wantNetworks := map[string]any{"jd_preview": map[string]any{"aliases": []any{"api.internal"}}, "jd_managed_jd-e7-db-3": nil}
	if !reflect.DeepEqual(web["networks"], wantNetworks) {
		t.Fatalf("web networks = %#v", web["networks"])
	}
	if !reflect.DeepEqual(db["networks"], map[string]any{"jd_preview": map[string]any{}, "jd_managed_jd-e7-db-3": nil}) {
		t.Fatalf("db networks = %#v", db["networks"])
	}
	if !strings.Contains(result.Override, "container_name: !reset null") {
		t.Fatalf("container_name was not reset:\n%s", result.Override)
	}
	if got, ok := web["secrets"].([]any); !ok || len(got) != 0 {
		t.Fatalf("secrets were not stripped: %v", web["secrets"])
	}
	if got, ok := web["external_links"].([]any); !ok || len(got) != 0 {
		t.Fatalf("external links were not stripped: %v", web["external_links"])
	}
	if got := web["labels"].(map[string]any); got["io.just-dashboard.preview"] != "true" || got["io.just-dashboard.managed"] != "true" {
		t.Fatalf("labels = %v", got)
	}
	networks := document["networks"].(map[string]any)
	if !reflect.DeepEqual(networks, map[string]any{
		"jd_preview":            map[string]any{"external": true, "name": "jd-preview-e7"},
		"jd_managed_jd-e7-db-3": map[string]any{"external": true, "name": "jd-e7-db-3"},
	}) {
		t.Fatalf("top-level networks = %#v", networks)
	}
	if got := document["volumes"].(map[string]any); len(got) != 4 || got["jd-preview-e7-volume-dbdata"] == nil {
		t.Fatalf("top-level volumes = %#v", got)
	}
	if got, ok := document["secrets"].(map[string]any); !ok || len(got) != 0 {
		t.Fatalf("top-level secrets = %#v", document["secrets"])
	}
	if want := []string{"jd-preview-e7-anonymous-web--scratch", "jd-preview-e7-bind--srv-production-cache", "jd-preview-e7-volume-dbdata", "jd-preview-e7-volume-uploads"}; !reflect.DeepEqual(result.Volumes, want) {
		t.Fatalf("volumes to create = %v", result.Volumes)
	}
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// A link in the pull request's tree is not the checkout's own file: read
// through, it could be any file on this server.
func TestComposePreviewOverrideLeavesOutACheckoutLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/etc/hostname", filepath.Join(root, "nginx.conf")); err != nil {
		t.Fatal(err)
	}
	result, err := composePreviewOverride(composePreviewConfigFixture(t, root), composePreviewReleaseOverride, root, composePreviewIsolationFixture())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Override, "nginx.conf") || !reflect.DeepEqual(result.LeftOut, []string{"web:/etc/nginx.conf", "web:/host-name"}) {
		t.Fatalf("a linked checkout file was mounted: left out %v\n%s", result.LeftOut, result.Override)
	}
}

func TestComposePreviewOverrideRefusesWhatAPreviewNeverGets(t *testing.T) {
	for name, service := range map[string]map[string]any{
		"network mode":         {"network_mode": "container:production-web"},
		"host network":         {"network_mode": "host"},
		"privileged":           {"privileged": true},
		"capabilities":         {"cap_add": []any{"NET_ADMIN"}},
		"devices":              {"devices": []any{map[string]any{"source": "/dev/kvm", "target": "/dev/kvm"}}},
		"gpus":                 {"gpus": []any{map[string]any{"count": -1}}},
		"device reservations":  {"deploy": map[string]any{"resources": map[string]any{"reservations": map[string]any{"devices": []any{map[string]any{"capabilities": []any{"gpu"}}}}}}},
		"host pid":             {"pid": "host"},
		"another container":    {"ipc": "container:production-web"},
		"host user namespace":  {"userns_mode": "host"},
		"unconfined seccomp":   {"security_opt": []any{"no-new-privileges:true", "seccomp=unconfined"}},
		"disabled labelling":   {"security_opt": []any{"label:disable"}},
		"volumes from":         {"volumes_from": []any{"container:production-web"}},
		"docker socket":        {"volumes": []any{map[string]any{"type": "bind", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock", "read_only": true}}},
		"image mount":          {"volumes": []any{map[string]any{"type": "image", "source": "production:latest", "target": "/image"}}},
		"dashboard label":      {"labels": map[string]any{"io.just-dashboard.rehearsal": "1"}},
		"privileged hook":      {"post_start": []any{map[string]any{"command": []any{"id"}, "privileged": true}}},
		"other runtime":        {"runtime": "nvidia"},
		"cgroup parent":        {"cgroup_parent": "system.slice"},
		"provider on the host": {"provider": map[string]any{"type": "model"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"services": map[string]any{"web": service}})
			_, err := composePreviewOverride(raw, "services:\n  web:\n    image: x\n", t.TempDir(), composePreviewIsolationFixture())
			if !errors.Is(err, ErrComposePreviewRefused) || !strings.Contains(err.Error(), "service web") {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	// Confinement the stack adds is not a reason to refuse it.
	raw, _ := json.Marshal(map[string]any{"services": map[string]any{"web": map[string]any{"security_opt": []any{"no-new-privileges:true"}, "pid": "service:db", "runtime": "runsc"}}})
	if _, err := composePreviewOverride(raw, "services:\n  web:\n    image: x\n", t.TempDir(), composePreviewIsolationFixture()); err != nil {
		t.Fatal(err)
	}
	// The public service has to be one of the stack's.
	missing := composePreviewIsolationFixture()
	missing.Public = "api"
	if _, err := composePreviewOverride(raw, "services:\n  web:\n    image: x\n", t.TempDir(), missing); err == nil {
		t.Fatal("a public service the stack does not have was accepted")
	}
}
