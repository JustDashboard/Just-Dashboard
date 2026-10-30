package cftunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// The real connector, cloudflared, as the dashboard runs it: a container of
// its own on a network of its own, its token in its environment, publishing
// nothing. A real tunnel needs a Cloudflare account, which a test does not
// have, so it is given a token of the right shape for a tunnel that does not
// exist: cloudflared starts, dials Cloudflare's edge, is refused, and keeps
// trying — and the dashboard reads that from cloudflared itself: running,
// its metrics server saying it holds no connection, and the reason in its
// own log. A token that is not one at all is refused by cloudflared before
// it starts, and that is read in its words too.
func TestLiveConnectorReportsCloudflaredsOwnFailure(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host, as root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	stamp := time.Now().UnixNano()
	connector := &DockerConnector{
		Image: ConnectorImage, Name: fmt.Sprintf("jdtest-cloudflared-%d", stamp),
		Network: fmt.Sprintf("jdtest-tunnel-%d", stamp), Labels: map[string]string{"io.justdashboard.test": "cftunnel"},
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", connector.Name).Run()
		_ = exec.Command("docker", "network", "rm", connector.Network).Run()
	})
	secret := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	raw, _ := json.Marshal(map[string]string{"a": "0123456789abcdef0123456789abcdef", "t": "6a1b9c4e-2f3d-4a5b-8c7d-9e0f1a2b3c4d", "s": secret})
	token := base64.StdEncoding.EncodeToString(raw)

	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sealer, _ := auth.NewSealer(strings.Repeat("cd", 32))
	manager := NewManager(Options{
		DB: st.DB, Sealer: sealer, Connector: connector, Network: connector.Network,
		API: func() (API, error) { return API{}, errors.New("no Cloudflare credentials are saved") },
		Origin: func(context.Context, string) (Origin, error) {
			return Origin{Service: "http://host.docker.internal:80", Network: connector.Network, HostGateway: true, Proxy: "this host's nginx"}, nil
		},
		Hostnames: func(context.Context) ([]Hostname, error) {
			return []Hostname{{Hostname: "shop.example.com"}}, nil
		},
	})
	status, err := manager.Connect(ctx, token, "live-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("connector %s at %s: %d connections, ready %t", status.Connector.State, status.Connector.Address, status.ReadyConnections, status.Ready)
	if status.Connector.State != "running" || status.Connector.Address == "" || status.Ready {
		t.Fatalf("status = %+v", status)
	}
	// cloudflared needs a few seconds to be refused by the edge and say so.
	var complaint string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		status = manager.Sync(ctx)
		if strings.Contains(status.Error, "error=") {
			complaint = status.Error
			break
		}
	}
	t.Logf("cloudflared said: %s", complaint)
	if complaint == "" {
		raw, _ := exec.Command("docker", "logs", "--tail", "20", connector.Name).CombinedOutput()
		t.Fatalf("no complaint read; status = %+v; log:\n%s", status, raw)
	}
	ready, err := connector.Ready(ctx, status.Connector)
	if err != nil || ready.ReadyConnections != 0 || ready.Status != 503 {
		t.Fatalf("metrics = %+v, %v", ready, err)
	}
	// Its token is in the container's environment, not its command line.
	inspect, _ := exec.Command("docker", "inspect", "--format", "{{json .Config.Cmd}} {{json .HostConfig.PortBindings}}", connector.Name).Output()
	if strings.Contains(string(inspect), token) || !strings.Contains(string(inspect), "{}") {
		t.Fatalf("command and ports = %s", inspect)
	}
	if len(status.Routes) != 2 || status.Hostnames[0].DNS != "manual" {
		t.Fatalf("routes = %+v hostnames = %+v", status.Routes, status.Hostnames)
	}

	// A token that is not one: cloudflared refuses it and exits.
	if err := connector.Run(ctx, "bm90LWEtdG9rZW4=", Origin{Service: "http://host.docker.internal:80", HostGateway: true}); err != nil {
		t.Fatal(err)
	}
	var exited ConnectorState
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if exited, err = connector.Inspect(ctx); err == nil && exited.State != "running" {
			break
		}
	}
	words := connector.LastError(ctx)
	t.Logf("with a token that is not one: %s, %q", exited.State, words)
	if exited.State == "running" || words != "Provided Tunnel token is not valid." {
		t.Fatalf("a malformed token = %+v, %q", exited, words)
	}
	if err := manager.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _ := connector.Inspect(ctx); state.State != "missing" {
		t.Fatalf("after disconnect = %+v", state)
	}
}
