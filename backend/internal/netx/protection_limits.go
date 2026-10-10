package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// LimitProfile is a starting point for a limit on a common service. Every
// figure is only a suggestion the form fills in: the operator still reads
// and saves it, and the server validates it like any other limit.
type LimitProfile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Why               string `json:"why"`
	Protocol          string `json:"protocol"`
	Ports             string `json:"ports"`
	Rate              int    `json:"rate"`
	Per               string `json:"per"`
	Burst             int    `json:"burst"`
	PerSource         bool   `json:"perSource"`
	MaxConnections    int    `json:"maxConnections"`
	GlobalConnections int    `json:"globalConnections"`
	Action            string `json:"action"`
}

var limitProfiles = []LimitProfile{
	{
		ID: "ssh", Name: "SSH", Protocol: "tcp", Ports: "22", Rate: 6, Per: "minute", Burst: 4, PerSource: true, MaxConnections: 10, Action: "drop",
		Why: "A password guesser opens a new connection per attempt. Six a minute per address leaves an operator room to reconnect and slows guessing to a crawl.",
	},
	{
		ID: "https", Name: "Website (HTTPS)", Protocol: "tcp", Ports: "443", Rate: 50, Per: "second", Burst: 100, PerSource: true, MaxConnections: 200, GlobalConnections: 20000, Action: "drop",
		Why: "Browsers open a handful of connections per page; an office behind one address opens many. The per-address figures absorb both and stop one source holding the server's sockets.",
	},
	{
		ID: "http", Name: "Website (HTTP)", Protocol: "tcp", Ports: "80", Rate: 30, Per: "second", Burst: 60, PerSource: true, MaxConnections: 100, Action: "drop",
		Why: "Plain HTTP mostly redirects to HTTPS or answers certificate challenges; it needs less room than HTTPS and is a common target for scanners.",
	},
	{
		ID: "dns", Name: "DNS server", Protocol: "both", Ports: "53", Rate: 50, Per: "second", Burst: 100, PerSource: true, Action: "drop",
		Why: "An answering DNS server can be used to amplify floods at a spoofed victim. A per-address rate caps what one source can make it send.",
	},
	{
		ID: "smtp", Name: "Mail (SMTP)", Protocol: "tcp", Ports: "25", Rate: 10, Per: "minute", Burst: 10, PerSource: true, MaxConnections: 10, Action: "reject",
		Why: "A real mail server delivers over a few connections and retries a refusal later; a spam run opens many. Reject tells a legitimate sender to retry.",
	},
	{
		ID: "wireguard", Name: "WireGuard handshakes", Protocol: "udp", Ports: "51820", Rate: 20, Per: "second", Burst: 40, PerSource: true, Action: "drop",
		Why: "Only a handshake's first packet is a new flow; the tunnel's traffic is established. A per-address rate stops a handshake flood without touching a working tunnel.",
	},
	{
		ID: "database", Name: "Exposed database", Protocol: "tcp", Ports: "5432", Rate: 30, Per: "minute", Burst: 10, PerSource: true, MaxConnections: 20, GlobalConnections: 200, Action: "reject",
		Why: "An application holds a small pool of long connections. A tight per-address ceiling and a ceiling for everyone stop a scanner or a runaway client exhausting the server's connection slots.",
	},
	{
		ID: "game", Name: "Game server (UDP)", Protocol: "udp", Ports: "27015", Rate: 30, Per: "second", Burst: 60, PerSource: true, Action: "drop",
		Why: "Query floods aim at game servers' status port. Players open one flow each; thirty new flows a second per address is far above that and far below a flood.",
	},
}

func limitProfileFor(id string) (LimitProfile, bool) {
	for _, p := range limitProfiles {
		if p.ID == id {
			return p, true
		}
	}
	return LimitProfile{}, false
}

// LimitMeters is how many sources a limit's per-source sets hold right now:
// the rate meters (which forget an idle source after their timeout) and the
// connection counters (one entry per source with open connections). A set
// holds at most limitMeterSize; near that, new sources are not metered.
type LimitMeters struct {
	RateSources *int      `json:"rateSources"`
	ConnSources *int      `json:"connSources"`
	Capacity    int       `json:"capacity"`
	CheckedAt   time.Time `json:"checkedAt"`
	Error       string    `json:"error,omitempty"`
}

const limitMeterSize = 65535

// readLimitMeters counts the elements of a limit's dynamic sets in both
// families. Unknown stays null rather than a fabricated zero.
func readLimitMeters(ctx context.Context, l LimitSpec) LimitMeters {
	m := LimitMeters{Capacity: limitMeterSize, CheckedAt: gatewayNow().UTC()}
	count := func(prefix string) *int {
		total := 0
		for _, fam := range []string{"4", "6"} {
			name := fmt.Sprintf("%s_%d_%s", prefix, l.ID, fam)
			out, err := run(ctx, "nft", "-j", "list", "set", "inet", gatewayTable, name)
			if err != nil {
				m.Error = err.Error()
				return nil
			}
			n, err := countSetElements(out, name)
			if err != nil {
				m.Error = err.Error()
				return nil
			}
			total += n
		}
		return &total
	}
	if l.Rate > 0 && l.PerSource {
		m.RateSources = count("lim")
	}
	if l.MaxConnections > 0 {
		m.ConnSources = count("conn")
	}
	return m
}

func countSetElements(out, name string) (int, error) {
	var listing struct {
		Nftables []struct {
			Set *struct {
				Name string            `json:"name"`
				Elem []json.RawMessage `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &listing) != nil {
		return 0, fmt.Errorf("nft returned an unreadable set %s", name)
	}
	for _, o := range listing.Nftables {
		if o.Set != nil && o.Set.Name == name {
			return len(o.Set.Elem), nil
		}
	}
	return 0, fmt.Errorf("nft did not return the set %s", name)
}

// limitKey is the counter comment a limit's refusals are summed under.
func limitKey(id int) string { return "limit:" + strconv.Itoa(id) }
