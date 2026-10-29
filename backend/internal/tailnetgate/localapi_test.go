package tailnetgate

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeTailscaled is an httptest server on a unix socket, speaking the shape
// of tailscaled's local API whois: the node and user profile that hold the
// address, 404 for an address nobody holds. No real tailnet is involved, and
// the host's own tailscaled is never asked.
func fakeTailscaled(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return socket
}

// whoisAnswers is what tailscaled says for three addresses: a person's
// laptop, a tagged CI machine (its user profile is the shared placeholder),
// and nothing for anything else.
func whoisAnswers(asked *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.Host != "local-tailscaled.sock" || r.URL.Path != "/localapi/v0/whois" || r.Method != http.MethodGet {
			http.Error(w, "invalid localapi request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("addr") {
		case "100.101.102.103":
			w.Write([]byte(`{
	"Node": {"ID": 7, "StableID": "nLaptop", "Name": "alice-laptop.tail1234.ts.net.",
		"User": 31, "Addresses": ["100.101.102.103/32", "fd7a:115c:a1e0::1/128"],
		"ComputedName": "alice-laptop", "Hostinfo": {"Hostname": "alice-laptop"}},
	"UserProfile": {"ID": 31, "LoginName": "Alice@Example.com", "DisplayName": "Alice Example", "ProfilePicURL": ""},
	"CapMap": {}
}`))
		case "100.64.0.9":
			w.Write([]byte(`{
	"Node": {"ID": 9, "Name": "ci-runner.tail1234.ts.net.", "User": 2, "Tags": ["tag:ci", "tag:build"],
		"Addresses": ["100.64.0.9/32"], "ComputedName": "ci-runner"},
	"UserProfile": {"ID": 2, "LoginName": "tagged-devices", "DisplayName": "Tagged Devices"}
}`))
		case "100.64.0.66":
			w.Write([]byte(`{"Node": {"Name": "broken."}}`))
		default:
			http.Error(w, "no match for IP:port", http.StatusNotFound)
		}
	}
}

// The real client, dialling the socket and asking what tailscaled's own CLI
// asks, reads a person, a tagged machine and an address nobody holds.
func TestLocalAPIWhoIsReadsTailscaledsAnswer(t *testing.T) {
	var asked atomic.Int32
	socket := fakeTailscaled(t, whoisAnswers(&asked))
	client := NewLocalAPI(nil, filepath.Join(t.TempDir(), "absent.sock"), socket)
	ctx := context.Background()

	alice, err := client.WhoIs(ctx, netip.MustParseAddr("100.101.102.103"))
	if err != nil || alice.Login != "Alice@Example.com" || alice.Name != "Alice Example" ||
		alice.Node != "alice-laptop.tail1234.ts.net" || alice.Tagged() {
		t.Fatalf("a person's laptop = %+v, %v", alice, err)
	}
	ci, err := client.WhoIs(ctx, netip.MustParseAddr("::ffff:100.64.0.9"))
	if err != nil || ci.Login != "" || ci.Name != "ci-runner" || !slices.Equal(ci.Tags, []string{"tag:ci", "tag:build"}) {
		t.Fatalf("a tagged machine = %+v, %v", ci, err)
	}
	if _, err := client.WhoIs(ctx, netip.MustParseAddr("100.70.0.1")); !errors.Is(err, ErrNotAPeer) {
		t.Fatalf("an address nobody holds = %v", err)
	}
	if _, err := client.WhoIs(ctx, netip.MustParseAddr("100.64.0.66")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an answer naming nobody = %v", err)
	}
	if asked.Load() != 4 {
		t.Fatalf("the local API was asked %d times", asked.Load())
	}
}

// No socket, a socket nobody answers on, or a daemon answering an error are
// all "could not ask", which the gate answers with 503 rather than a refusal.
func TestLocalAPIWithoutTailscaledIsUnavailable(t *testing.T) {
	ctx := context.Background()
	address := netip.MustParseAddr("100.101.102.103")
	if _, err := NewLocalAPI(nil, filepath.Join(t.TempDir(), "missing.sock")).WhoIs(ctx, address); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no socket = %v", err)
	}
	stale := filepath.Join(t.TempDir(), "stale.sock")
	listener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
	if _, err := NewLocalAPI(nil, stale).WhoIs(ctx, address); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a socket nobody listens on = %v", err)
	}
	failing := fakeTailscaled(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend not running", http.StatusServiceUnavailable)
	})
	if _, err := NewLocalAPI(nil, failing).WhoIs(ctx, address); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("a daemon that is not running = %v", err)
	}
}

// The host's socket is reached through the path mapping the backend uses for
// the host's /run, not the container's own.
func TestLocalAPIDialsTheMappedHostPath(t *testing.T) {
	var asked atomic.Int32
	socket := fakeTailscaled(t, whoisAnswers(&asked))
	mapped := ""
	client := NewLocalAPI(func(path string) string {
		mapped = path
		return socket
	}, "/run/tailscale/tailscaled.sock")
	if _, err := client.WhoIs(context.Background(), netip.MustParseAddr("100.101.102.103")); err != nil {
		t.Fatal(err)
	}
	if mapped != "/run/tailscale/tailscaled.sock" {
		t.Fatalf("mapped %q", mapped)
	}
}
