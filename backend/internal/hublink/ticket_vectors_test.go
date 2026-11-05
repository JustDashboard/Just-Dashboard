package hublink

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// The sign-in ticket vectors (hub-link-vectors.json, protocol section 12) run
// through steps 3 to 11 of section 9.7 with the code a redeem uses: the shape,
// the pinned key, the signature, the payload's JSON rules, the server, the
// time, the hub's second factor, the device, and the nonce in a real store.

type ticketClient struct {
	Address string          `json:"address"`
	WhoIs   json.RawMessage `json:"whois"`
}

type ticketVectors struct {
	Context struct {
		Client          ticketClient `json:"client"`
		Hub             string       `json:"hub"`
		PinnedKid       string       `json:"pinnedKid"`
		PinnedPublicKey string       `json:"pinnedPublicKey"`
		ServerID        string       `json:"serverId"`
	} `json:"signInTicketContext"`
	Cases []struct {
		Name         string        `json:"name"`
		Ticket       string        `json:"ticket"`
		Now          int64         `json:"now"`
		Expect       string        `json:"expect"`
		DeviceCheck  string        `json:"deviceCheck"`
		SecondRedeem string        `json:"secondRedeem"`
		Client       *ticketClient `json:"client"`
	} `json:"signInTickets"`
}

// vectorWhoIs answers as a case's client says tailscaled does.
type vectorWhoIs struct {
	t      *testing.T
	client ticketClient
	asked  int
}

func (v *vectorWhoIs) WhoIs(_ context.Context, a netip.Addr) (tailnetgate.Peer, error) {
	v.asked++
	if a.String() != v.client.Address {
		v.t.Fatalf("WhoIs asked about %s, the client is %s", a, v.client.Address)
	}
	var word string
	if json.Unmarshal(v.client.WhoIs, &word) == nil {
		switch word {
		case "not_a_peer":
			return tailnetgate.Peer{}, tailnetgate.ErrNotAPeer
		case "unavailable":
			return tailnetgate.Peer{}, tailnetgate.ErrUnavailable
		}
		v.t.Fatalf("unknown whois answer %q", word)
	}
	var who struct{ Login, Node string }
	if err := json.Unmarshal(v.client.WhoIs, &who); err != nil {
		v.t.Fatal(err)
	}
	return tailnetgate.Peer{Address: a, Login: who.Login, StableID: who.Node, Node: "laptop.tailed39ba.ts.net"}, nil
}

func TestSignInTicketVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/internal/backend/hub-link-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v ticketVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) != 37 {
		t.Fatalf("%d ticket cases; the amended vectors have 37", len(v.Cases))
	}
	keys := []accountlink.SiteKey{{Kid: v.Context.PinnedKid, PublicKey: v.Context.PinnedPublicKey}}
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Service{st: st, now: time.Now}

	redeem := func(ticket string, now int64, client ticketClient) (string, DeviceCheck) {
		t.Helper()
		s.now = func() time.Time { return time.Unix(now, 0) }
		_, p, _, refused := checkTicket(ticket, ticketCheck{
			Keys: keys, Hub: v.Context.Hub, ServerID: v.Context.ServerID, Now: s.now(),
		})
		if refused != nil {
			return refused.Code, DeviceCheck{}
		}
		whois := &vectorWhoIs{t: t, client: client}
		device, refused := checkDevice(t.Context(), whois, client.Address, p)
		if refused != nil {
			return refused.Code, device
		}
		if addr := netip.MustParseAddr(client.Address); addr.IsLoopback() && whois.asked != 0 {
			t.Fatalf("a loopback client was asked about")
		}
		used, err := s.spendNonce(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		if used {
			return codeTicketUsed, device
		}
		return "ok", device
	}

	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			client := v.Context.Client
			if c.Client != nil {
				client = *c.Client
			}
			got, device := redeem(c.Ticket, c.Now, client)
			if got != c.Expect {
				t.Fatalf("redeem = %s, want %s", got, c.Expect)
			}
			if c.Expect == "ok" && device.Result != c.DeviceCheck {
				t.Fatalf("deviceCheck = %q, want %q", device.Result, c.DeviceCheck)
			}
			if c.SecondRedeem != "" {
				if again, _ := redeem(c.Ticket, c.Now, v.Context.Client); again != c.SecondRedeem {
					t.Fatalf("second redeem = %s, want %s", again, c.SecondRedeem)
				}
			}
		})
	}
}

// Step 10 is skipped only when tailscaled cannot be asked. A tailscaled that
// answered, but named no login or no node for the browser's address, was
// asked: the device is refused, through the real local API client, as one
// that named someone else is (protocol 9.7 step 10).
func TestADeviceTailscaledCannotNameIsRefused(t *testing.T) {
	answers := map[string]func(http.ResponseWriter){
		// A person's node whose profile has no login.
		"100.101.102.110": func(w http.ResponseWriter) {
			w.Write([]byte(`{"Node": {"StableID": "nLaptop", "Name": "laptop.tailed39ba.ts.net."},
				"UserProfile": {"ID": 4, "LoginName": "", "DisplayName": ""}}`))
		},
		// No node and no profile at all.
		"100.101.102.111": func(w http.ResponseWriter) { w.Write([]byte(`{}`)) },
		// Not JSON.
		"100.101.102.112": func(w http.ResponseWriter) { w.Write([]byte(`<html>`)) },
		// tailscaled is there and says it cannot answer: an outage.
		"100.101.102.113": func(w http.ResponseWriter) { http.Error(w, "backend not running", http.StatusServiceUnavailable) },
	}
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answers[r.URL.Query().Get("addr")](w)
	}))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	whois := tailnetgate.NewLocalAPI(nil, socket)
	p := &TicketPayload{TSLogin: "wayy01@github", TSNode: "nLaptop"}

	for _, addr := range []string{"100.101.102.110", "100.101.102.111", "100.101.102.112"} {
		device, refused := checkDevice(t.Context(), whois, addr, p)
		if refused == nil || refused.Code != "wrong_device" || device.Result != "refused" {
			t.Fatalf("%s: %+v, %v; want wrong_device", addr, device, refused)
		}
	}
	device, refused := checkDevice(t.Context(), whois, "100.101.102.113", p)
	if refused != nil || device.Result != "skipped" {
		t.Fatalf("tailscaled out of service: %+v, %v; want skipped", device, refused)
	}
}
