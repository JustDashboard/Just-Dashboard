package netpath

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func streamProviders(session *proxysvc.StreamLoggedSession, probeOK bool) Providers {
	return Providers{
		Route: func(context.Context, Request) (netx.RouteLookup, error) {
			return netx.RouteLookup{Path: netx.Path{Local: true, Source: "127.0.0.1", Device: "lo"}}, nil
		},
		Owners: func(context.Context) (OwnerSnapshot, error) {
			return OwnerSnapshot{Listeners: []proxysvc.Listener{{Protocol: "tcp", Port: 6432, Address: "0.0.0.0", Process: "nginx", Stream: "pg"}}}, nil
		},
		Stream: func(_ context.Context, name string) (*proxysvc.StreamPath, error) {
			return &proxysvc.StreamPath{Name: name, State: "live", Protocol: "tcp", Listens: []string{"port 6432/tcp"},
				Access: "allow 10.0.0.0/8, deny everyone else", SocketsRead: true, Clients: 3, Logging: true, LogComplete: true,
				LogSessions: 12, LogFailed: 1, Servers: []proxysvc.StreamPathServer{
					{Address: "10.0.0.5:5432", Connections: 2, Sessions: 11, Failed: 1},
					{Address: "10.0.0.6:5432", Backup: true},
				}}, nil
		},
		StreamSession: func(_ context.Context, name, client string, _ time.Time) (*proxysvc.StreamLoggedSession, error) {
			if name != "pg" || client != "127.0.0.1" {
				return nil, nil
			}
			return session, nil
		},
		Probe: func(context.Context, Request) (*netsec.ProbeResult, error) {
			return &netsec.ProbeResult{OK: probeOK}, nil
		},
	}
}

// A listener nginx holds for a stream joins the stream's forward, access,
// sessions and backend legs; a measured connection through it is followed
// into the stream's log to the backend it reached.
func TestStreamListenerJoinsTheStreamAndItsBackendLeg(t *testing.T) {
	req := Request{SourceKind: "host", Target: "127.0.0.1", Family: "inet", Protocol: "tcp", Port: 6432, Measure: true}
	session := &proxysvc.StreamLoggedSession{Client: "127.0.0.1", Status: 200, Upstream: "10.0.0.5:5432", Seconds: 0.002}
	r, err := Investigate(context.Background(), req, streamProviders(session, true))
	if err != nil {
		t.Fatal(err)
	}
	stream := findEvidence(t, r, "stream")
	if stream.Basis != Observed || stream.State != "live" || stream.OwnerPath != "/proxy/streams?stream=pg" ||
		!strings.Contains(stream.Summary, "nginx forwards TCP on port 6432/tcp to 10.0.0.5:5432, 10.0.0.6:5432.") {
		t.Fatalf("stream = %+v", stream)
	}
	facts := map[string]string{}
	for _, f := range stream.Facts {
		facts[f.Label] = f.Value
	}
	if facts["backend 10.0.0.5:5432"] != "2 connections from nginx now; 11 sessions in the last hour, 1 failed" ||
		facts["backup backend 10.0.0.6:5432"] != "0 connections from nginx now; 0 sessions in the last hour, 0 failed" ||
		facts["Client sessions now"] != "3" || facts["Access"] != "allow 10.0.0.0/8, deny everyone else" {
		t.Fatalf("facts = %v", facts)
	}
	leg := findEvidence(t, r, "stream_traversal")
	if leg.Basis != Measured || leg.State != "forwarded" || !strings.Contains(leg.Summary, "forwarded the measured connection to 10.0.0.5:5432") {
		t.Fatalf("leg = %+v", leg)
	}
	if r.Evidence[len(r.Evidence)-1].ID != "stream_traversal" {
		t.Fatalf("the backend leg is not read after the measurement: %+v", r.Evidence)
	}
}

// Without a measurement, or with a refused one, no backend leg is claimed;
// a session nginx refused or failed says so.
func TestStreamBackendLegIsOnlyClaimedFromTheLog(t *testing.T) {
	req := Request{SourceKind: "host", Target: "127.0.0.1", Family: "inet", Protocol: "tcp", Port: 6432}
	r, err := Investigate(context.Background(), req, streamProviders(nil, true))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Evidence {
		if e.ID == "stream_traversal" {
			t.Fatalf("a leg without a measurement: %+v", e)
		}
	}
	findEvidence(t, r, "stream")

	req.Measure = true
	r, _ = Investigate(context.Background(), req, streamProviders(nil, false))
	for _, e := range r.Evidence {
		if e.ID == "stream_traversal" {
			t.Fatalf("a leg after a connection that failed: %+v", e)
		}
	}
	r, _ = Investigate(context.Background(), req, streamProviders(nil, true))
	if leg := findEvidence(t, r, "stream_traversal"); leg.Basis != Unknown || !strings.Contains(leg.Summary, "logged no session") {
		t.Fatalf("leg without a logged session = %+v", leg)
	}
	for status, want := range map[int]string{403: "denied", 502: "failed"} {
		r, _ = Investigate(context.Background(), req, streamProviders(&proxysvc.StreamLoggedSession{Client: "127.0.0.1", Status: status, Upstream: "10.0.0.5:5432"}, true))
		if leg := findEvidence(t, r, "stream_traversal"); leg.State != want || leg.Basis != Measured {
			t.Fatalf("status %d: %+v", status, leg)
		}
	}
}

// A site forwarding to the destination port carries its service policy into
// the proxy layer: what requests through it meet, and only those.
func TestProxyLayerCarriesTheSitesServicePolicy(t *testing.T) {
	req := Request{SourceKind: "host", Target: "127.0.0.1", Family: "inet", Protocol: "tcp", Port: 3000}
	p := Providers{
		Route: func(context.Context, Request) (netx.RouteLookup, error) {
			return netx.RouteLookup{Path: netx.Path{Local: true, Source: "127.0.0.1", Device: "lo"}}, nil
		},
		Owners: func(context.Context) (OwnerSnapshot, error) {
			return OwnerSnapshot{Listeners: []proxysvc.Listener{{Protocol: "tcp", Port: 3000, Address: "127.0.0.1", Process: "node",
				Routes: []proxysvc.ListenerRoute{{Site: "app", ServerName: "app.example.com"}}}}}, nil
		},
		Policy: func(_ context.Context, site string) (*proxysvc.ServicePolicy, error) {
			return &proxysvc.ServicePolicy{Site: site, Controls: []proxysvc.PolicyControl{
				{ID: "rate-limit", Title: "Request limit", Configured: true, Setting: "10r/s, burst 20", Support: "built-in"},
				{ID: "proxy-cache", Title: "Response cache", Configured: false, Support: "built-in"},
				{ID: "http3", Title: "HTTP/3", Configured: true, Setting: "listen 443 quic", Support: "missing"},
			}}, nil
		},
	}
	r, err := Investigate(context.Background(), req, p)
	if err != nil {
		t.Fatal(err)
	}
	proxy := findEvidence(t, r, "proxy")
	var fact string
	for _, f := range proxy.Facts {
		if f.Label == "Service policy of app" {
			fact = f.Value
		}
	}
	if fact != "request limit 10r/s, burst 20 · http/3 listen 443 quic (this nginx lacks its module)" {
		t.Fatalf("policy fact = %q in %+v", fact, proxy.Facts)
	}
	if !strings.Contains(strings.Join(proxy.Limitations, " "), "a direct connection to the port meets none of it") {
		t.Fatalf("limitations = %v", proxy.Limitations)
	}
}
