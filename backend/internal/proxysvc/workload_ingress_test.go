package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExistingCaddyIngressKeepsHostPathUpstreamCorrelation(t *testing.T) {
	raw := []byte(`{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"routes":[{"match":[{"host":["web.example.test"],"path":["/api/*"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"web:3000"}]}]}]}]},{"match":[{"host":["worker.example.test"],"path":["/jobs/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"worker:8080"}]}]}]}}}}}`)
	targets := []ExistingIngressTarget{{Service: "web", Alias: "web", Network: "app-net", ContainerPort: 3000}, {Service: "worker", Alias: "worker", Network: "app-net", ContainerPort: 8080}}
	bindings, err := existingCaddyBindings(raw, targets, "docker-caddy", "edge", "/etc/caddy/Caddyfile", "web.example.test {\n reverse_proxy web:3000\n}\n", true)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings: %+v, %v", bindings, err)
	}
	for _, binding := range bindings {
		if binding.Service == "web" && (binding.Hostname != "web.example.test" || binding.Path != "/api/*") {
			t.Fatalf("unrelated route joined: %+v", binding)
		}
		if binding.Service == "worker" && (binding.Hostname != "worker.example.test" || binding.Path != "/jobs/*") {
			t.Fatalf("unrelated route joined: %+v", binding)
		}
	}
}

func TestExistingCaddyIngressORMatchersDoNotCrossJoin(t *testing.T) {
	raw := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["a.example.test"],"path":["/a/*"]},{"host":["b.example.test"],"path":["/b/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"app:3000"}]}]}]}}}}}`)
	bindings, err := existingCaddyBindings(raw, []ExistingIngressTarget{{Service: "app", Alias: "app", Network: "network", ContainerPort: 3000}}, "docker-caddy", "edge", "/config", "", true)
	if err != nil || len(bindings) != 2 {
		t.Fatal(bindings, err)
	}
	for _, b := range bindings {
		if (b.Hostname == "a.example.test" && b.Path != "/a/*") || (b.Hostname == "b.example.test" && b.Path != "/b/*") {
			t.Fatalf("cross match: %+v", b)
		}
	}
}

func TestExistingCaddyIngressRejectsContradictoryAndConditionalMatchers(t *testing.T) {
	raw := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["a.example.test"]}],"handle":[{"handler":"subroute","routes":[{"match":[{"host":["b.example.test"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"app:3000"}]}]}]}]},{"match":[{"host":["conditional.example.test"],"not":[{"path":["/admin/*"]}]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"app:3000"}]}]}]}}}}}`)
	bindings, err := existingCaddyBindings(raw, []ExistingIngressTarget{{Service: "app", Network: "owned", Alias: "app", ContainerPort: 3000}}, "docker-caddy", "edge", "/source", "", true)
	if err != nil || len(bindings) != 1 || bindings[0].Hostname != "conditional.example.test" || bindings[0].Status != "blocked" {
		t.Fatal("conditional/contradictory route was flattened into verified evidence", bindings, err)
	}
}

func TestExistingNginxIngressPreservesServiceLocationsAndUnsavedBlocker(t *testing.T) {
	text := "http { server { listen 443 ssl; server_name app.example.test; location /api/ { proxy_pass http://127.0.0.1:3000/preserved/; } location /jobs/ { proxy_pass http://127.0.0.1:8080; } } }"
	files := []ConfigFile{{Path: "/etc/nginx/nginx.conf", Content: text}}
	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	bindings := existingNginxBindings(tree, files, []ExistingIngressTarget{{Service: "api", Host: "127.0.0.1", Port: 3000}, {Service: "worker", Host: "127.0.0.1", Port: 8080}}, false)
	if len(bindings) != 2 {
		t.Fatal(bindings)
	}
	for _, b := range bindings {
		if b.Status != "blocked" || !b.HTTPS {
			t.Fatal(b)
		}
		if b.Service == "api" && (b.Path != "/api/" || !strings.HasSuffix(b.Upstream, "/preserved/")) {
			t.Fatal(b)
		}
	}
}

func TestExistingIngressRetargetPreservesEverySurroundingByte(t *testing.T) {
	for _, driver := range []string{"nginx", "docker-caddy"} {
		t.Run(driver, func(t *testing.T) {
			text := "server {\n ssl_certificate /operator/cert.pem;\n location /api/ {\n  proxy_pass http://10.0.0.2:3000/original/; # keep URI\n  proxy_set_header Upgrade $http_upgrade;\n  auth_basic existing;\n }\n}\n"
			binding := ExistingIngressBinding{ProxyKind: driver, SourcePath: "/source", Selector: "nginx:4:http://10.0.0.2:3000/original/", Upstream: "http://10.0.0.2:3000/original/"}
			if driver == "docker-caddy" {
				text = "app.example.test {\n tls /operator/cert.pem /operator/key.pem\n basic_auth {\n  operator hashed\n }\n reverse_proxy 10.0.0.2:3000 {\n  header_up Host original\n }\n}\n"
				binding.Upstream = "10.0.0.2:3000"
			}
			next, err := ingressReplacement(binding.Upstream, "10.0.0.3", 3000)
			if err != nil {
				t.Fatal(err)
			}
			after, err := replaceIngressLiteral(text, binding, binding.Upstream, next)
			if err != nil {
				t.Fatal(err)
			}
			if after != strings.Replace(text, binding.Upstream, next, 1) {
				t.Fatal("surrounding TLS/auth/URI behavior changed")
			}
		})
	}
}

func ingressJournalFixture(t *testing.T) (*Service, ExistingIngressBinding, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "conf.d", "owned.conf")
	text := "server {\n server_name app.example.test;\n location / { proxy_pass http://10.0.0.2:3000/preserved/; }\n}\n"
	if err := os.WriteFile(path, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
	s := New(root, filepath.Join(root, "Caddyfile")).WithIngressJournalDir(filepath.Join(t.TempDir(), "journals"))
	s.ingressReload = func(context.Context, ExistingIngressBinding) error { return nil }
	b := newIngressBinding("app.example.test", "/", "http://10.0.0.2:3000/preserved/", "nginx", "owned", path, "nginx:3:http://10.0.0.2:3000/preserved/", text, ExistingIngressTarget{Service: "web", Network: "owned-network", ContainerPort: 3000}, "retarget", true)
	return s, b, text
}

func TestExistingIngressJournalCASAndConditionalRecovery(t *testing.T) {
	s, b, before := ingressJournalFixture(t)
	target := []ExistingIngressTarget{{Service: "web", Network: "owned-network", ContainerPort: 3000, Address: "10.0.0.3"}}
	if err := s.ApplyExistingIngress(t.Context(), 11, 22, []ExistingIngressBinding{b}, target); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(b.SourcePath)
	if !strings.Contains(string(after), "10.0.0.3:3000/preserved/") {
		t.Fatal(string(after))
	}
	if err := s.VerifyExistingIngress(t.Context(), []ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	journal, err := s.readIngressJournal(b)
	if err != nil || journal.Phase != "applied" {
		t.Fatal(journal, err)
	}
	path, _ := s.ingressJournalPath(b)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("journal is not private")
	}
	if err := os.WriteFile(b.SourcePath, append(after, []byte("# operator edit\n")...), 0o640); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.RestoreExistingIngress(t.Context(), 11, 22, []ExistingIngressBinding{b}), ErrExistingIngressChanged) {
		t.Fatal("rollback overwrote a later operator edit")
	}
	if err := os.WriteFile(b.SourcePath, after, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreExistingIngress(t.Context(), 11, 22, []ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(b.SourcePath)
	if string(restored) != before {
		t.Fatal("original proxy bytes did not restore")
	}
}

func TestExistingIngressReloadFailureAndCrashRestoreExactBytes(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload", true: "interrupted"}[crash], func(t *testing.T) {
			s, b, before := ingressJournalFixture(t)
			if crash {
				after := strings.Replace(before, "10.0.0.2", "10.0.0.3", 1)
				journal := &ingressHandoffJournal{Version: 1, Owner: 11, Release: 22, Phase: "prepared", Binding: b, Before: before, After: after, BeforeDigest: routeDigest(before), AfterDigest: routeDigest(after), Mode: 0o640}
				if err := s.writeIngressJournal(journal); err != nil {
					t.Fatal(err)
				}
				if err := writeIngressCAS(b.SourcePath, before, after, 0o640, b.SourceIdentity); err != nil {
					t.Fatal(err)
				}
				if err := s.RecoverExistingIngress(t.Context(), 11, 23, []ExistingIngressBinding{b}); err != nil {
					t.Fatal(err)
				}
			} else {
				calls := 0
				s.ingressReload = func(context.Context, ExistingIngressBinding) error {
					calls++
					if calls == 1 {
						return errors.New("owned fixture rejects reload")
					}
					return nil
				}
				if err := s.ApplyExistingIngress(t.Context(), 11, 22, []ExistingIngressBinding{b}, []ExistingIngressTarget{{Service: "web", Network: "owned-network", ContainerPort: 3000, Address: "10.0.0.3"}}); err == nil {
					t.Fatal("reload failure disappeared")
				}
			}
			restored, _ := os.ReadFile(b.SourcePath)
			if string(restored) != before {
				t.Fatal("failed or interrupted handoff did not restore exact bytes")
			}
		})
	}
}

func TestExistingIngressRefusesSharedDynamicAndAliasCollision(t *testing.T) {
	targets := []ExistingIngressTarget{{Service: "a", Network: "net", Alias: "app", ContainerPort: 3000}, {Service: "b", Network: "net", Alias: "app", ContainerPort: 3000}}
	if _, _, ok := ingressMatches("app:3000", targets, true); ok {
		t.Fatal("ambiguous alias was selected")
	}
	if closedCaddyLiteral("import routes/*\nreverse_proxy 10.0.0.2:3000\n", "10.0.0.2:3000") {
		t.Fatal("shared imported config authorized a literal write")
	}
	if _, _, ok := ingressEndpoint("http://$dynamic:3000"); ok {
		t.Fatal("dynamic upstream was claimed")
	}
}

func TestExistingNginxUpstreamPoolRetargetsOnlyServerEndpoint(t *testing.T) {
	content := "http {\n upstream original {\n  server 10.0.0.2:3000 max_fails=2;\n }\n server {\n  server_name app.example.test;\n  location /api/ { proxy_pass http://original/preserved/; }\n }\n}\n"
	files := []ConfigFile{{Path: "/etc/nginx/nginx.conf", Content: content}}
	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	bindings := existingNginxBindings(tree, files, []ExistingIngressTarget{{Service: "web", Network: "owned", Address: "10.0.0.2", ContainerPort: 3000}}, true)
	if len(bindings) != 1 || bindings[0].Status != "linked" {
		t.Fatal(bindings)
	}
	after, err := replaceIngressLiteral(content, bindings[0], bindings[0].Upstream, "10.0.0.3:3000")
	if err != nil || after != strings.Replace(content, "10.0.0.2:3000", "10.0.0.3:3000", 1) {
		t.Fatal(after, err)
	}
	shared := strings.Replace(content, "  server 10.0.0.2:3000 max_fails=2;", "  server 10.0.0.2:3000 max_fails=2;\n  server 10.0.0.9:3000;", 1)
	files[0].Content = shared
	tree, _ = NginxTree(files)
	bindings = existingNginxBindings(tree, files, []ExistingIngressTarget{{Service: "web", Network: "owned", Address: "10.0.0.2", ContainerPort: 3000}}, true)
	if len(bindings) != 1 || bindings[0].Status != "blocked" {
		t.Fatal("shared upstream was not blocked", bindings)
	}
}

func TestExistingIngressRefusesReplacedInodeAndRecoversInterruptedWrite(t *testing.T) {
	s, b, before := ingressJournalFixture(t)
	oldPath := b.SourcePath + ".old"
	if err := os.Rename(b.SourcePath, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.SourcePath, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.VerifyExistingIngress(t.Context(), []ExistingIngressBinding{b}), ErrExistingIngressChanged) {
		t.Fatal("replacement inode authorized stale mounted config")
	}
	if !errors.Is(writeIngressCAS(b.SourcePath, before, before+"# changed\n", 0o640, b.SourceIdentity), ErrExistingIngressChanged) {
		t.Fatal("opened replacement inode was not fenced before CAS write")
	}
	if err := os.Remove(b.SourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, b.SourcePath); err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(before, "10.0.0.2:3000", "10.0.0.30:3000", 1)
	journal := &ingressHandoffJournal{Version: 1, Owner: 11, Release: 22, Phase: "prepared", Binding: b, Before: before, After: after, BeforeDigest: routeDigest(before), AfterDigest: routeDigest(after), Mode: 0o640}
	if err := s.writeIngressJournal(journal); err != nil {
		t.Fatal(err)
	}
	prefix := strings.Index(after, "10.0.0.30") + len("10.0.0.30")
	partial := after[:prefix] + before[prefix:]
	if err := os.WriteFile(b.SourcePath, []byte(partial), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverExistingIngress(t.Context(), 11, 23, []ExistingIngressBinding{b}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(b.SourcePath); string(raw) != before {
		t.Fatal("interrupted exact write was not restored")
	}
}

func TestExistingIngressStoppedAliasRequiresExactCapturedContainer(t *testing.T) {
	id := strings.Repeat("a", 64)
	binding := ExistingIngressBinding{Upstream: "api:3000", Network: "owned", TargetContainerID: id, CapturedStopped: true}
	var container ingressContainer
	if err := json.Unmarshal([]byte(`{"Id":"`+id+`","State":{"Running":false},"NetworkSettings":{"Networks":{"owned":{"Aliases":["api"]}}}}`), &container); err != nil {
		t.Fatal(err)
	}
	if !stoppedIngressAlias(binding, container) {
		t.Fatal("exact stopped alias was rejected")
	}
	foreign := container
	foreign.ID = strings.Repeat("b", 64)
	if stoppedIngressAlias(binding, foreign) {
		t.Fatal("foreign stopped container accepted")
	}
	container.State.Running = true
	if stoppedIngressAlias(binding, container) || !runningIngressAliasOwned(binding, id, []ingressContainer{container}) {
		t.Fatal("running/stopped ownership was conflated")
	}
	if runningIngressAliasOwned(binding, foreign.ID, []ingressContainer{container}) {
		t.Fatal("a different running alias owner was accepted")
	}
	foreign.State.Running = true
	if runningIngressAliasOwned(binding, id, []ingressContainer{container, foreign}) {
		t.Fatal("shared running alias was accepted")
	}
	delete(container.NetworkSettings.Networks, "owned")
	container.State.Running = false
	if stoppedIngressAlias(binding, container) {
		t.Fatal("missing captured network was accepted")
	}
}

func TestExistingIngressIgnoresInstalledInactiveOrUnassociatedNginx(t *testing.T) {
	for _, scenario := range []string{"installed", "stopped", "unassociated"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			s := New(root, filepath.Join(root, "Caddyfile"))
			s.pending.processes = func() ([]nginxProcess, error) {
				if scenario == "unassociated" {
					return []nginxProcess{{PID: 5, Title: "nginx: master process nginx -c /operator/other/nginx.conf", Ticks: 2}}, nil
				}
				return nil, nil
			}
			bindings, err := s.CaptureExistingIngress(t.Context(), []ExistingIngressTarget{{Service: "app", Host: "127.0.0.1", Port: 3000}})
			if err != nil || len(bindings) != 0 {
				t.Fatal("binary presence became a proxy dependency", bindings, err)
			}
		})
	}
}

func TestExistingIngressBlocksActiveUninspectableNginx(t *testing.T) {
	s, root, running := pendingTree(t)
	running.load(time.Now().Add(-time.Hour), 100)
	writeFile(t, filepath.Join(root, "broken"), "invalid configuration")
	if _, err := s.CaptureExistingIngress(t.Context(), []ExistingIngressTarget{{Service: "app", Host: "127.0.0.1", Port: 3000}}); err == nil {
		t.Fatal("observed active nginx inspection failure became absence")
	}
}
