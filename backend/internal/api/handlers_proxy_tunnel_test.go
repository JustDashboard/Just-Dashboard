package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/cftunnel"
)

// The tunnel's routes: read by anyone signed in, a token refused in words
// and audited, and the host alert unjudged while nothing is connected.
func TestTunnelRoutesRefuseATokenThatIsNotOne(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "tunnel-admin", auth.RoleAdmin)}
	res := admin.do(http.MethodGet, "/api/v1/proxy/tunnel", "", nil)
	var status cftunnel.Status
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil || res.Code != http.StatusOK || status.Connected {
		t.Fatalf("status = %d %s", res.Code, res.Body.String())
	}
	res = admin.do(http.MethodPut, "/api/v1/proxy/tunnel", `{"token":"not a token"}`, nil)
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "tunnel token is base64") {
		t.Fatalf("a token that is not one = %d %s", res.Code, res.Body.String())
	}
	if entry := lastAudit(t, s, "proxy.tunnel.connect"); !strings.Contains(entry.Detail, `"result":"refused"`) || strings.Contains(entry.Detail, "not a token") {
		t.Fatalf("audit = %+v", entry)
	}
	viewer := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "tunnel-viewer", auth.RoleLimited)}
	if res := viewer.do(http.MethodPut, "/api/v1/proxy/tunnel", `{"token":"x"}`, nil); res.Code != http.StatusForbidden {
		t.Fatalf("a viewer connecting = %d", res.Code)
	}
	if reading := s.tunnelAlertReading(t.Context()); reading.Judged || !strings.Contains(reading.Why, "No Cloudflare Tunnel is connected") {
		t.Fatalf("alert reading = %+v", reading)
	}
}

// tunnelConnectorStub is the connector without Docker, told what to say.
type tunnelConnectorStub struct {
	state, digest string
	ready         int
	complaint     string
}

func (c *tunnelConnectorStub) Inspect(context.Context) (cftunnel.ConnectorState, error) {
	if c.state == "" {
		return cftunnel.ConnectorState{State: "missing"}, nil
	}
	return cftunnel.ConnectorState{State: c.state, Address: "10.0.0.9", TokenDigest: c.digest, Origin: "http://ingress:80"}, nil
}

func (c *tunnelConnectorStub) Run(_ context.Context, token string, _ cftunnel.Origin) error {
	c.state, c.digest = "running", cftunnel.TokenDigest(token)
	return nil
}

func (c *tunnelConnectorStub) Remove(context.Context) error { c.state = ""; return nil }

func (c *tunnelConnectorStub) Ready(context.Context, cftunnel.ConnectorState) (cftunnel.Readiness, error) {
	return cftunnel.Readiness{Status: 200, ReadyConnections: c.ready}, nil
}

func (c *tunnelConnectorStub) LastError(context.Context) string { return c.complaint }

// The host's Tunnel down reading: quiet with connections to the edge,
// firing with none in cloudflared's own words, and firing when the connector
// is not running at all — one subject, the tunnel, either way.
func TestTunnelDownIsTheConnectorOrTheEdge(t *testing.T) {
	s := testServer(t)
	stub := &tunnelConnectorStub{ready: 4}
	s.modules.tunnel = cftunnel.NewManager(cftunnel.Options{
		DB: s.Store.DB, Sealer: s.Sealer, Connector: stub,
		API: func() (cftunnel.API, error) { return cftunnel.API{}, errors.New("no Cloudflare credentials are saved") },
		Origin: func(context.Context, string) (cftunnel.Origin, error) {
			return cftunnel.Origin{Service: "http://ingress:80", Proxy: "the Caddy ingress"}, nil
		},
		Hostnames: func(context.Context) ([]cftunnel.Hostname, error) { return nil, nil },
	})
	raw, _ := json.Marshal(map[string]string{"a": "acc", "t": "11111111-2222-4333-8444-555555555555", "s": "c2VjcmV0"})
	if _, err := s.modules.tunnel.Connect(t.Context(), base64.StdEncoding.EncodeToString(raw), "ada"); err != nil {
		t.Fatal(err)
	}
	subject := "tunnel:11111111-2222-4333-8444-555555555555"
	if reading := s.tunnelAlertReading(t.Context()); !reading.Judged || len(reading.Firing) != 0 || !strings.Contains(reading.Now[subject], "4 connections") {
		t.Fatalf("connected = %+v", reading)
	}
	stub.ready, stub.complaint = 0, `Register tunnel error from server side error="Unauthorized: Invalid tunnel secret"`
	s.modules.tunnel.Sync(t.Context())
	reading := s.tunnelAlertReading(t.Context())
	if len(reading.Firing) != 1 || reading.Firing[0].Subject != subject || !strings.Contains(reading.Firing[0].Detail, "cloudflared said: Register tunnel error") {
		t.Fatalf("no edge connection = %+v", reading)
	}
	stub.state = "restarting"
	reading = s.tunnelAlertReading(t.Context())
	if len(reading.Firing) != 1 || !strings.Contains(reading.Firing[0].Detail, "connector container is restarting") {
		t.Fatalf("connector restarting = %+v", reading)
	}
}
