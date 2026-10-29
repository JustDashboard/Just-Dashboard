package cftunnel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// fakeConnector is the connector's driver without Docker: what it was run
// with, and what its metrics say.
type fakeConnector struct {
	mu        sync.Mutex
	state     ConnectorState
	runs      []string
	ready     int
	complaint string
}

func (c *fakeConnector) Inspect(context.Context) (ConnectorState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.State == "" {
		return ConnectorState{State: "missing", Version: ConnectorVersion}, nil
	}
	return c.state, nil
}

func (c *fakeConnector) Run(_ context.Context, token string, origin Origin) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs = append(c.runs, TokenDigest(token)+" "+origin.Service)
	c.state = ConnectorState{State: "running", ID: "c1", Address: "10.0.0.9", TokenDigest: TokenDigest(token), Origin: origin.Service, Version: ConnectorVersion}
	return nil
}

func (c *fakeConnector) Remove(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = ConnectorState{}
	return nil
}

func (c *fakeConnector) Ready(context.Context, ConnectorState) (Readiness, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Readiness{Status: 200, ReadyConnections: c.ready}, nil
}

func (c *fakeConnector) LastError(context.Context) string { return c.complaint }

type managerFixture struct {
	manager   *Manager
	connector *fakeConnector
	cloud     *fakeCloudflare
	hostnames []Hostname
	audits    []string
	apiErr    error
}

func newManagerFixture(t *testing.T) *managerFixture {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	f := &managerFixture{connector: &fakeConnector{ready: 4}, cloud: newFakeCloudflare(t)}
	f.manager = NewManager(Options{
		DB: st.DB, Sealer: sealer, Connector: f.connector,
		API: func() (API, error) {
			if f.apiErr != nil {
				return API{}, f.apiErr
			}
			return f.cloud.api(), nil
		},
		Origin: func(context.Context, string) (Origin, error) {
			return Origin{Service: "http://just-dashboard-ingress:80", Network: TunnelNetwork, Proxy: "the Caddy ingress"}, nil
		},
		Hostnames: func(context.Context) ([]Hostname, error) { return f.hostnames, nil },
		Audit: func(_ context.Context, action string, _ map[string]any, success bool) {
			f.audits = append(f.audits, action+map[bool]string{true: " ok", false: " failed"}[success])
		},
	})
	f.manager.readyAttempts = 1
	return f
}

// A tunnel created through the API: the token sealed, never stored in the
// clear; the connector started with it against the proxy; the live
// deployments' tunnel names routed and their records made — and a second
// pass, with nothing changed, sends nothing.
func TestCreatedTunnelRoutesTheDeploymentsMarkedForIt(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	f.hostnames = []Hostname{{Hostname: "shop.example.com", ProjectID: 3, EnvironmentID: 7, Project: "shop"}}
	status, err := f.manager.Create(ctx, "production", "", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Connected || status.Connection.Mode != "api" || status.Connection.TunnelName != "production" || status.Connection.AccountID != "acc123" {
		t.Fatalf("connection = %+v", status.Connection)
	}
	if !status.Ready || status.ReadyConnections != 4 || status.Connector.State != "running" || len(f.connector.runs) != 1 ||
		!strings.HasSuffix(f.connector.runs[0], " http://just-dashboard-ingress:80") {
		t.Fatalf("status = %+v, runs %v", status, f.connector.runs)
	}
	var sealed string
	if err := f.manager.opts.DB.QueryRow(`SELECT token_enc FROM proxy_cloudflare_tunnel`).Scan(&sealed); err != nil || strings.Contains(sealed, "eyJhIjoiYWNjMTIzIn0=") {
		t.Fatalf("the token is stored as %q", sealed)
	}
	if len(status.Hostnames) != 1 || !status.Hostnames[0].Routed || status.Hostnames[0].DNS != string(DNSCreated) || status.Hostnames[0].Project != "shop" {
		t.Fatalf("hostnames = %+v", status.Hostnames)
	}
	var configuration Configuration
	_ = json.Unmarshal(f.cloud.configuration[status.Connection.TunnelID], &configuration)
	if strings.Join(configuration.Hostnames(), ",") != "shop.example.com" {
		t.Fatalf("routes at Cloudflare = %+v", configuration)
	}
	puts := func() int {
		count := 0
		for _, call := range f.cloud.calls {
			if strings.HasSuffix(call, "/configurations") {
				count++
			}
		}
		return count
	}
	before := puts()
	again := f.manager.Sync(ctx)
	if puts() != before || len(f.connector.runs) != 1 || again.Hostnames[0].DNS != string(DNSCreated) {
		t.Fatalf("an unchanged pass sent %d routes and ran %d connectors: %+v", puts()-before, len(f.connector.runs), again.Hostnames)
	}
	// A release going live with a new name: routed before it is live.
	if err := f.manager.Route(ctx, []string{"api.example.com"}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(f.cloud.configuration[status.Connection.TunnelID], &configuration)
	if strings.Join(configuration.Hostnames(), ",") != "api.example.com,shop.example.com" {
		t.Fatalf("routes after a release = %+v", configuration)
	}
	// A name whose record is somebody else's is refused in words, and the
	// release is told.
	f.cloud.records["zone-example"] = append(f.cloud.records["zone-example"], DNSRecord{ID: "theirs", Type: "A", Name: "www.example.com", Content: "203.0.113.7"})
	err = f.manager.Route(ctx, []string{"www.example.com"})
	if err == nil || !strings.Contains(err.Error(), "already has a A record") {
		t.Fatalf("a foreign record = %v", err)
	}
	if !strings.Contains(strings.Join(f.audits, ","), "proxy.tunnel.dns failed") {
		t.Fatalf("audits = %v", f.audits)
	}
	if err := f.manager.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if after := f.manager.Status(ctx); after.Connected || f.connector.state.State != "" {
		t.Fatalf("after disconnect = %+v", after)
	}
}

// A pasted token without saved credentials: the connector runs, the routes
// are shown for the operator to add in Cloudflare's dashboard, and the
// record is theirs to create. A connector with no connection says why in
// cloudflared's words.
func TestPastedTokenWithoutCredentialsSaysWhatIsLeftToDo(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	f.apiErr = errors.New("no Cloudflare credentials are saved")
	f.connector.ready = 0
	f.connector.complaint = `Register tunnel error from server side error="Unauthorized: Invalid tunnel secret"`
	f.hostnames = []Hostname{{Hostname: "shop.example.com"}}
	if _, err := f.manager.Connect(ctx, "garbage", "ada"); err == nil {
		t.Fatal("a token that is not one was kept")
	}
	token := fakeToken(t, map[string]string{"a": "acc123", "t": "11111111-2222-4333-8444-555555555555", "s": "c2VjcmV0"})
	status, err := f.manager.Connect(ctx, token, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.Error != f.connector.complaint || status.API.Available || status.Connection.TunnelID != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Routes) != 2 || status.Routes[0].Hostname != "shop.example.com" || status.Hostnames[0].DNS != "manual" ||
		!strings.Contains(status.Hostnames[0].Error, "11111111-2222-4333-8444-555555555555.cfargotunnel.com") {
		t.Fatalf("what is left to do = %+v %+v", status.Routes, status.Hostnames)
	}
	// A connector stopped by hand is started again with the same token.
	f.connector.state.State = "exited"
	f.manager.Sync(ctx)
	if len(f.connector.runs) != 2 || f.connector.state.State != "running" {
		t.Fatalf("runs = %v", f.connector.runs)
	}
}
