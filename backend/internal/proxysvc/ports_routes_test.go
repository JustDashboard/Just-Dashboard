package proxysvc

import "testing"

func TestSitesForUpstreamPortReadsEveryLoopbackSpelling(t *testing.T) {
	sites := []VHost{
		{Name: "app", Enabled: true, Upstreams: []string{"http://127.0.0.1:3000"}},
		{Name: "api", Enabled: true, Upstreams: []string{"localhost:3000/api"}},
		{Name: "off", Enabled: false, Upstreams: []string{"http://127.0.0.1:3000"}},
		{Name: "other", Enabled: true, Upstreams: []string{"http://127.0.0.1:3001"}},
		// A name other than localhost is a container or another machine.
		{Name: "container", Enabled: true, Upstreams: []string{"http://web:3000"}},
	}
	got := siteNames(SitesForUpstreamPort(sites, 3000, "0.0.0.0"))
	if len(got) != 2 || got[0] != "app" || got[1] != "api" {
		t.Fatalf("sites = %v, want app and api", got)
	}
	// A socket on one address of the machine is not reached by loopback.
	if got := SitesForUpstreamPort(sites, 3000, "203.0.113.7"); len(got) != 0 {
		t.Fatalf("a site reached a socket on another address: %v", siteNames(got))
	}
}

func TestStreamsForUpstreamPortReadsPoolsAndRanges(t *testing.T) {
	streams := []StreamEntry{
		{StreamSpec: StreamSpec{Name: "pg", Listen: 6432, Upstream: "127.0.0.1:5432"}},
		{StreamSpec: StreamSpec{Name: "pool", Listen: 7000, Upstream: "10.9.9.9:5432",
			Servers: []StreamServer{{Address: "10.9.9.9:5432"}, {Address: "localhost:5432"}}}},
		{StreamSpec: StreamSpec{Name: "game", Listen: 27015, ListenEnd: 27030, SamePort: true, Upstream: "127.0.0.1"}},
		{StreamSpec: StreamSpec{Name: "elsewhere", Listen: 8000, Upstream: "10.9.9.9:5432"}},
	}
	got := []string{}
	for _, s := range StreamsForUpstreamPort(streams, 5432, "") {
		got = append(got, s.Name)
	}
	if len(got) != 2 || got[0] != "pg" || got[1] != "pool" {
		t.Fatalf("streams on 5432 = %v, want pg and pool", got)
	}
	if got := StreamsForUpstreamPort(streams, 27020, "127.0.0.1"); len(got) != 1 || got[0].Name != "game" {
		t.Fatalf("a range forwarded port for port did not reach 27020: %v", got)
	}
	if got := StreamsForUpstreamPort(streams, 27031, "127.0.0.1"); len(got) != 0 {
		t.Fatalf("a port past the range was reached: %v", got)
	}
}

func siteNames(sites []VHost) []string {
	out := []string{}
	for _, s := range sites {
		out = append(out, s.Name)
	}
	return out
}
