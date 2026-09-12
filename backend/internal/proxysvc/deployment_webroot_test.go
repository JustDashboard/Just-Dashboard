package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentChallengeUsesListenerOwnership(t *testing.T) {
	plugins := map[string]bool{"standalone": true, "webroot": true, "nginx": true}
	for _, tc := range []struct {
		name      string
		listeners []Listener
		method    string
		wantError string
	}{
		{name: "free", method: "standalone"},
		{name: "host nginx", listeners: []Listener{{Protocol: "tcp", Port: 80, Process: "nginx"}}, method: "webroot"},
		{name: "caddy", listeners: []Listener{{Protocol: "tcp", Port: 80, Process: "caddy", PID: 42}}, wantError: "caddy (PID 42)"},
		{name: "unknown", listeners: []Listener{{Protocol: "tcp", Port: 80}}, wantError: "unidentified process"},
		{name: "mixed owners", listeners: []Listener{{Protocol: "tcp", Port: 80, Process: "nginx"}, {Protocol: "tcp", Port: 80, Process: "apache2"}}, wantError: "apache2"},
		{name: "different port", listeners: []Listener{{Protocol: "tcp", Port: 8080, Process: "caddy"}}, method: "standalone"},
		{name: "udp", listeners: []Listener{{Protocol: "udp", Port: 80}}, method: "standalone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chooseDeploymentHTTPChallenge(plugins, tc.listeners, true)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			if err != nil || got.method != tc.method {
				t.Fatalf("got %+v, %v; want %s", got, err, tc.method)
			}
		})
	}
	_, err := chooseDeploymentHTTPChallenge(map[string]bool{"standalone": true}, []Listener{{Protocol: "tcp", Port: 80, Process: "nginx"}}, true)
	if !errors.Is(err, ErrCertbotUnavailable) {
		t.Fatalf("fell back to occupied standalone: %v", err)
	}
}

func TestDeploymentChallengePreservesBindScope(t *testing.T) {
	out := renderDeploymentChallenge([]string{"app.example.test"}, deploymentACMEWebroot, []Listener{
		{Address: "127.0.0.1", Port: 80}, {Address: "127.0.0.1", Port: 80},
	})
	if strings.Count(out, "listen ") != 1 || !strings.Contains(out, "listen 127.0.0.1:80;") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "location / { return 404; }") || strings.Contains(out, "proxy_pass") {
		t.Fatal(out)
	}
}

func TestDeploymentACMEWebrootSurvivesSiteEditing(t *testing.T) {
	spec := deploymentSiteSpec(DeploymentRoute{Name: "app.conf", Domains: []string{"app.example.test"}, Upstream: "http://127.0.0.1:3000", TLS: true, ForceHTTPS: true, CertPath: "/etc/ssl/cert.pem", KeyPath: "/etc/ssl/key.pem"})
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := ParseSiteSpec(spec.Name, out)
	if !parsed.ManagedACME {
		t.Fatal("editing dropped the deployment challenge root")
	}
	roundtrip, err := RenderNginx(parsed)
	if err != nil || !strings.Contains(roundtrip, "root "+deploymentACMEWebroot+";") {
		t.Fatalf("%v\n%s", err, roundtrip)
	}
	parsed.ManagedACME = false
	regular, err := RenderNginx(parsed)
	if err != nil || !strings.Contains(regular, "root /var/www/html;") {
		t.Fatalf("ordinary site webroot changed: %v\n%s", err, regular)
	}
}

func TestDeploymentWebrootRestoresOnCancellation(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"conf.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	probe := func(context.Context, []string) error {
		calls++
		if calls > 1 {
			cancel()
		}
		return errors.New("challenge unavailable")
	}
	_, err := New(root, "").prepareDeploymentWebroot(ctx, []string{"app.example.test"}, filepath.Join(root, "web"), []Listener{{Address: "127.0.0.1", Port: 80}}, probe)
	if err == nil {
		t.Fatal("accepted a challenge that could not be served")
	}
	entries, err := os.ReadDir(filepath.Join(root, "conf.d"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("challenge config survived cancellation: %v, %v", entries, err)
	}
}
