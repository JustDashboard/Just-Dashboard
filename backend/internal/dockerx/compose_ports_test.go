package dockerx

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestComposePortOverridePreservesScopeProtocolAndPrivateServices(t *testing.T) {
	raw, err := composePortOverride([]byte(`{"services":{"web":{"ports":[{"target":80,"published":"8080","host_ip":"127.0.0.1","protocol":"tcp"},{"target":53,"published":"5353","host_ip":"::1","protocol":"udp"}],"environment":{"SECRET":"do-not-persist"}},"db":{"expose":[5432]},"host":{"network_mode":"host","ports":[{"target":80,"published":"80"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"!override", "127.0.0.1", "::1", "udp", "target: 80"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	for _, unwanted := range []string{"do-not-persist", "db:", "host:"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("unexpected %s: %s", unwanted, text)
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
}

func TestLiveComposeRecoversPortConflictAndInvalidatesEditedPreferences(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for Docker mutation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	occupied := listener.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	project := fmt.Sprintf("jd-compose-ports-%d", time.Now().UnixNano())
	source := filepath.Join(dir, "compose.yaml")
	writeSource := func(port int) {
		t.Helper()
		raw := fmt.Sprintf("name: %s\nservices:\n  web:\n    image: nginx:alpine\n    environment:\n      SECRET: compose-port-private-value\n    ports:\n      - '127.0.0.1:%d:80'\n", project, port)
		if err := os.WriteFile(source, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSource(occupied)
	c := New("unix:///var/run/docker.sock")
	defer func() {
		result, err := c.RunCompose(context.Background(), dir, ComposeDown, "")
		if err != nil || result.ExitCode != 0 {
			t.Errorf("cleanup: %+v %v", result, err)
		}
	}()
	result, err := c.RunCompose(ctx, dir, ComposeUp, "")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("automatic Compose start: %+v, %v", result, err)
	}
	containers, err := c.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": project})
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || len(containers[0].Ports) != 1 || int(containers[0].Ports[0].PublicPort) == occupied || containers[0].Ports[0].IP != "127.0.0.1" {
		t.Fatalf("ports: %+v", containers)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".just-dashboard-ports.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "compose-port-private-value") {
		t.Fatal("secret persisted in automatic ports file")
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("original owner no longer reachable", err)
	}
	conn.Close()
	// The original requested port becomes available after its owner exits.
	// An explicit source edit must replace the automatic mapping on the next up.
	spare, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	preferred := spare.Addr().(*net.TCPAddr).Port
	spare.Close()
	writeSource(preferred)
	result, err = c.RunCompose(ctx, dir, ComposeUp, "")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("edited preference: %+v %v", result, err)
	}
	containers, err = c.ListContainersWithLabels(ctx, map[string]string{"com.docker.compose.project": project})
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || len(containers[0].Ports) != 1 || int(containers[0].Ports[0].PublicPort) != preferred {
		t.Fatalf("stale automatic mapping: %+v", containers)
	}
}

func TestComposePortFingerprintOnlyDependsOnSourceBindings(t *testing.T) {
	first, err := composePortFingerprint([]byte(`{"services":{"app":{"ports":[{"target":80,"published":"8080","host_ip":"192.0.2.1","protocol":"sctp"}],"environment":{"SECRET":"old"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := composePortFingerprint([]byte(`{"services":{"app":{"ports":[{"target":80,"published":"8080","host_ip":"192.0.2.1","protocol":"sctp"}],"environment":{"SECRET":"new"},"image":"changed"}}}`))
	if err != nil || first != second {
		t.Fatalf("unrelated edits changed port fingerprint: %v", err)
	}
	third, err := composePortFingerprint([]byte(`{"services":{"app":{"ports":[{"target":80,"published":"9090","host_ip":"192.0.2.1","protocol":"sctp"}]}}}`))
	if err != nil || first == third {
		t.Fatalf("edited port did not invalidate fingerprint: %v", err)
	}
}
