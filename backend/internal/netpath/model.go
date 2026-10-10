// Package netpath joins existing owners' evidence without interpreting a new
// packet engine. A kernel decision or configured edge is never a measurement.
package netpath

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type Basis string

const (
	Observed Basis = "observed"
	Modeled  Basis = "modeled"
	Measured Basis = "measured"
	Unknown  Basis = "unknown"
)

type Request struct {
	SourceKind    string `json:"sourceKind"`
	ContainerID   string `json:"containerId,omitempty"`
	SourceAddress string `json:"sourceAddress,omitempty"`
	Target        string `json:"target"`
	Address       string `json:"address,omitempty"`
	Family        string `json:"family"`
	Protocol      string `json:"protocol"`
	Port          int    `json:"port"`
	Mark          string `json:"mark,omitempty"`
	Measure       bool   `json:"measure"`
}

type Scope struct {
	Vantage       string   `json:"vantage"`
	Source        string   `json:"source"`
	SourceAddress string   `json:"sourceAddress,omitempty"`
	Target        string   `json:"target"`
	Address       string   `json:"address,omitempty"`
	Family        string   `json:"family"`
	Protocol      string   `json:"protocol"`
	Port          int      `json:"port"`
	Mark          string   `json:"mark,omitempty"`
	Limitations   []string `json:"limitations"`
}

type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Evidence struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Basis       Basis     `json:"basis"`
	State       string    `json:"state"`
	Scope       string    `json:"scope"`
	Owner       string    `json:"owner"`
	OwnerPath   string    `json:"ownerPath,omitempty"`
	CheckedAt   time.Time `json:"checkedAt"`
	Summary     string    `json:"summary"`
	Facts       []Fact    `json:"facts"`
	Limitations []string  `json:"limitations"`
}

type Result struct {
	Request     Request             `json:"request"`
	Scope       Scope               `json:"scope"`
	StartedAt   time.Time           `json:"startedAt"`
	EndedAt     time.Time           `json:"endedAt"`
	Addresses   []string            `json:"addresses"`
	Evidence    []Evidence          `json:"evidence"`
	Measurement *netsec.ProbeResult `json:"measurement,omitempty"`
	Comparison  string              `json:"comparison"`
}

type DNSAnswer struct {
	Addresses []string
	Owner     string
	Summary   string
	Limits    []string
}

type OwnerSnapshot struct {
	Listeners []proxysvc.Listener
	Limits    []string
}

type Providers struct {
	SourceName string
	DNS        func(context.Context, Request) (DNSAnswer, error)
	Rules      func(context.Context, string) ([]netx.TrafficRule, error)
	Route      func(context.Context, Request) (netx.RouteLookup, error)
	Firewall   func(context.Context) (*netsec.FirewallStatus, error)
	Gateway    func(context.Context) (*netx.GatewayView, error)
	Links      func(context.Context) ([]netx.Link, error)
	Owners     func(context.Context) (OwnerSnapshot, error)
	Probe      func(context.Context, Request) (*netsec.ProbeResult, error)
	// Stream reads a native nginx stream's configuration, sockets and
	// recent log; StreamSession waits for the session a client's connection
	// left in that log.
	Stream func(ctx context.Context, name string) (*proxysvc.StreamPath, error)
	// Policy reads a proxy site's request limits, caching and HTTP versions:
	// the application-layer service policy requests through it meet.
	Policy        func(ctx context.Context, site string) (*proxysvc.ServicePolicy, error)
	StreamSession func(ctx context.Context, name, client string, since time.Time) (*proxysvc.StreamLoggedSession, error)
}

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Validate(req Request) (Request, error) {
	req.Target = strings.TrimSpace(req.Target)
	req.SourceAddress = strings.TrimSpace(req.SourceAddress)
	req.Address = strings.TrimSpace(req.Address)
	if req.SourceKind != "host" && req.SourceKind != "container" {
		return req, fmt.Errorf("select the host or a running container as the source")
	}
	if req.SourceKind == "container" && !containerID.MatchString(req.ContainerID) || req.SourceKind == "host" && req.ContainerID != "" {
		return req, fmt.Errorf("a container source needs its full inventory ID")
	}
	if !netsec.ValidTarget(req.Target) || (req.Family != "inet" && req.Family != "inet6") || (req.Protocol != "tcp" && req.Protocol != "udp") || req.Port < 1 || req.Port > 65535 {
		return req, fmt.Errorf("provide a hostname or literal address, IPv4/IPv6, TCP/UDP and port from 1 to 65535")
	}
	for _, value := range []*string{&req.SourceAddress, &req.Address} {
		if *value != "" {
			addr, err := netx.ParseAddr(*value)
			if err != nil || (req.Family == "inet") != addr.Is4() {
				return req, fmt.Errorf("source and chosen destination addresses must match the selected family")
			}
			*value = addr.String()
		}
	}
	if addr, err := netip.ParseAddr(req.Target); err == nil {
		if (req.Family == "inet") != addr.Unmap().Is4() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() {
			return req, fmt.Errorf("the literal target must be unicast and match the selected family")
		}
		req.Target = addr.Unmap().String()
		if req.Address != "" && req.Address != req.Target {
			return req, fmt.Errorf("the chosen address does not match the literal target")
		}
	}
	if req.Mark != "" {
		base, value := 10, req.Mark
		if strings.HasPrefix(value, "0x") {
			base, value = 16, value[2:]
		}
		n, err := strconv.ParseUint(value, base, 32)
		if err != nil {
			return req, fmt.Errorf("the mark must be one 32-bit value without a mask")
		}
		req.Mark = fmt.Sprintf("0x%x", n)
	}
	return req, nil
}

func evidence(id, title, scope, owner, ownerPath string) Evidence {
	return Evidence{ID: id, Title: title, Scope: scope, Owner: owner, OwnerPath: ownerPath, Basis: Unknown, State: "unknown", CheckedAt: time.Now().UTC(), Facts: []Fact{}, Limitations: []string{}}
}

func (e *Evidence) failure(err error) {
	e.Basis, e.State, e.Summary = Unknown, "unavailable", err.Error()
}
