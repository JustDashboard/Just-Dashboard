package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

// networkClients is an admin and a read-only session on a server whose network
// module writes into a temporary directory, so the routes that change the
// resolver and the hosts file can be driven end to end without touching the
// machine the tests run on.
func networkClients(t *testing.T) (admin, viewer *client, paths netx.Paths) {
	t.Helper()
	s := testServer(t)
	dir := t.TempDir()
	paths = netx.Paths{
		Dir:       filepath.Join(dir, "network"),
		Sysctl:    filepath.Join(dir, "sysctl.d", "90-just-dashboard.conf"),
		Unit:      filepath.Join(dir, "systemd", netx.UnitName),
		Resolved:  filepath.Join(dir, "resolved.conf.d", "90-just-dashboard.conf"),
		Hosts:     filepath.Join(dir, "hosts"),
		WireGuard: filepath.Join(dir, "wireguard"),
	}
	s.modules.network = netx.New(netx.Options{Paths: paths, DB: s.Store.DB, Log: s.Log})
	h := s.Routes()
	admin = &client{t: t, h: h, cookie: signInAs(t, s, "admin", auth.RoleAdmin)}
	viewer = &client{t: t, h: h, cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}
	return admin, viewer, paths
}

func decodeNetworkBody(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("body is not the expected JSON: %v\n%s", err, body)
	}
}

// The readings answer on whatever host runs them, with the lists the pages
// iterate as arrays and never null.
func TestNetworkDNSAndTrafficReadsAnswer(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	for _, path := range []string{
		"/api/v1/network/dns/",
		"/api/v1/network/dns/hosts",
		"/api/v1/network/traffic/containers",
		"/api/v1/network/traffic/containers?window=24h",
		"/api/v1/network/ebpf",
	} {
		for name, c := range map[string]*client{"admin": admin, "readonly": viewer} {
			t.Run(name+" "+path, func(t *testing.T) {
				w := c.do(http.MethodGet, path, "", nil)
				if w.Code != http.StatusOK {
					t.Fatalf("got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
				}
			})
		}
	}

	var view netx.DNSView
	decodeNetworkBody(t, admin.do(http.MethodGet, "/api/v1/network/dns/", "", nil).Body.Bytes(), &view)
	if len(view.Presets) != 6 || view.Presets[0].ID != "cloudflare" || view.Listeners == nil || view.Adblock == nil ||
		view.ResolvConf.Nameservers == nil || view.Resolved.Links == nil || view.Resolved.Global.Servers == nil {
		t.Fatalf("view = %+v", view)
	}

	var ebpf netx.EBPFView
	decodeNetworkBody(t, admin.do(http.MethodGet, "/api/v1/network/ebpf", "", nil).Body.Bytes(), &ebpf)
	if ebpf.Programs == nil || ebpf.Attachments == nil || ebpf.ByType == nil {
		t.Fatalf("ebpf = %+v", ebpf)
	}

	var containers netx.ContainerTraffic
	decodeNetworkBody(t, admin.do(http.MethodGet, "/api/v1/network/traffic/containers?window=6h", "", nil).Body.Bytes(), &containers)
	if containers.WindowSeconds != 6*3600 || containers.Containers == nil {
		t.Fatalf("containers = %+v", containers)
	}
	if w := admin.do(http.MethodGet, "/api/v1/network/traffic/containers?window=forever", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a bad window = %d", w.Code)
	}
	if w := admin.do(http.MethodGet, "/api/v1/network/traffic/containers?window=90d", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a window past a month = %d", w.Code)
	}
}

// Which program talks to which address is an administrator's to read.
func TestNetworkProcessesAreAdminOnly(t *testing.T) {
	admin, viewer, _ := networkClients(t)
	if w := viewer.do(http.MethodGet, "/api/v1/network/traffic/processes", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("readonly read the process traffic: %d", w.Code)
	}
	w := admin.do(http.MethodGet, "/api/v1/network/traffic/processes", "", nil)
	switch w.Code {
	case http.StatusOK:
		var res netx.ProcessTraffic
		decodeNetworkBody(t, w.Body.Bytes(), &res)
		if res.Programs == nil || !res.Warming || res.Note == "" {
			t.Fatalf("first read = %+v", res)
		}
	case http.StatusServiceUnavailable:
		if !strings.Contains(w.Body.String(), "tool_unavailable") {
			t.Fatalf("wrong code for a host without ss: %s", w.Body.String())
		}
	default:
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}

// Every route that changes the resolver or the hosts file is an administrator's.
func TestNetworkDNSWritesNeedAnAdministrator(t *testing.T) {
	_, viewer, paths := networkClients(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/network/dns/", `{"servers":["9.9.9.9"]}`},
		{http.MethodDelete, "/api/v1/network/dns/", ``},
		{http.MethodPut, "/api/v1/network/dns/hosts", `{"records":[{"address":"10.0.0.1","names":["a.internal"]}]}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if w := viewer.do(tc.method, tc.path, tc.body, nil); w.Code != http.StatusForbidden {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	for _, p := range []string{paths.Resolved, paths.Hosts} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("a refused request wrote %s", p)
		}
	}
}

// A request that cannot be valid is refused before anything on the host is
// asked or written.
func TestNetworkDNSSetValidatesFirst(t *testing.T) {
	for name, body := range map[string]string{
		"dot without names": `{"servers":["1.1.1.1"],"dnsOverTLS":"yes"}`,
		"not an address":    `{"servers":["one.one.one.one"]}`,
		"the stub itself":   `{"servers":["127.0.0.53"]}`,
		"unknown setting":   `{"servers":["1.1.1.1"],"dnssec":"maybe"}`,
		"nothing at all":    `{}`,
		"an unknown field":  `{"servers":["1.1.1.1"],"resolver":"x"}`,
		"newline in a name": `{"servers":["1.1.1.1#a.com\nDNS=6.6.6.6"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			admin, _, paths := networkClients(t)
			w := admin.do(http.MethodPost, "/api/v1/network/dns/", body, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if _, err := os.Stat(paths.Resolved); err == nil {
				t.Fatal("a refused request wrote the drop-in")
			}
		})
	}
}

func TestNetworkDNSHostsRoundTrip(t *testing.T) {
	admin, viewer, paths := networkClients(t)
	if err := os.WriteFile(paths.Hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := admin.do(http.MethodPut, "/api/v1/network/dns/hosts",
		`{"records":[{"address":"192.0.2.10","names":["app.internal","git.internal"]}]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var put netx.HostRecords
	decodeNetworkBody(t, w.Body.Bytes(), &put)
	if len(put.Managed) != 1 || put.Managed[0].Address != "192.0.2.10" || len(put.Other) != 1 {
		t.Fatalf("put = %+v", put)
	}
	// Any role reads them back.
	var got netx.HostRecords
	decodeNetworkBody(t, viewer.do(http.MethodGet, "/api/v1/network/dns/hosts", "", nil).Body.Bytes(), &got)
	if len(got.Managed) != 1 || got.Managed[0].Names[1] != "git.internal" || got.Other[0].Names[0] != "localhost" {
		t.Fatalf("got = %+v", got)
	}
	b, _ := os.ReadFile(paths.Hosts)
	if string(b) != "127.0.0.1 localhost\n# BEGIN Just Dashboard\n192.0.2.10 app.internal git.internal\n# END Just Dashboard\n" {
		t.Fatalf("hosts file = %q", b)
	}

	for name, body := range map[string]string{
		"a bad address":     `{"records":[{"address":"nope","names":["a"]}]}`,
		"a name with space": `{"records":[{"address":"10.0.0.1","names":["a b"]}]}`,
		"no names":          `{"records":[{"address":"10.0.0.1","names":[]}]}`,
	} {
		if w := admin.do(http.MethodPut, "/api/v1/network/dns/hosts", body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", name, w.Code)
		}
	}
	// Refused edits left the file as it was.
	if after, _ := os.ReadFile(paths.Hosts); string(after) != string(b) {
		t.Fatalf("a refused edit changed the file: %q", after)
	}

	// A corrupt file is a conflict the page explains, not a server fault.
	if err := os.WriteFile(paths.Hosts, []byte("# BEGIN Just Dashboard\n# BEGIN Just Dashboard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = admin.do(http.MethodPut, "/api/v1/network/dns/hosts", `{"records":[{"address":"10.0.0.1","names":["a"]}]}`, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Just Dashboard") {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}

// A lookup is a read: any role may ask, and a bad question is refused before a
// packet is sent.
func TestNetworkDNSLookupValidation(t *testing.T) {
	_, viewer, _ := networkClients(t)
	for name, body := range map[string]string{
		"an address for an A lookup": `{"name":"192.0.2.1","type":"A"}`,
		"a name for a PTR":           `{"name":"example.com","type":"PTR"}`,
		"a record type outside":      `{"name":"example.com","type":"ANY"}`,
		"a space in the name":        `{"name":"exa mple.com","type":"A"}`,
		"an empty name":              `{"name":"","type":"A"}`,
		"a server of the caller's":   `{"name":"example.com","type":"A","server":"192.0.2.9"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if w := viewer.do(http.MethodPost, "/api/v1/network/dns/lookup", body, nil); w.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
