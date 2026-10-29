package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// The route a tailnet-only deployment's gate asks about every request
// (tailnet-apps.md): GET /api/v1/tailnet/whois?environment=<id>, the tailnet
// address in X-Tailnet-Peer. It sits behind the network allowlist like every
// other API route, which judges the proxy's own loopback address, and has no
// session of its own to ask for: what admits the caller is that it is the
// proxy — on loopback, forwarded by nobody — and what admits the visitor is
// tailscaled's answer about the address.
func (s *Server) initTailnetGate() {
	if s.Cfg.AgentMode {
		return
	}
	s.modules.tailnetGate = &tailnetgate.Handler{
		// The host's tailscaled, through PID 1's root: the backend's own
		// /run is the container's.
		WhoIs:   tailnetgate.NewLocalAPI(hostexec.HostPath),
		Access:  s.modules.deployRuns.LiveTailnetAllow,
		Refused: s.recordTailnetRefusal,
	}
}

func (s *Server) mountTailnetGate(r interface {
	Method(string, string, http.Handler)
}) {
	if s.modules.tailnetGate == nil {
		return
	}
	r.Method(http.MethodGet, "/tailnet/whois", s.modules.tailnetGate)
	r.Method(http.MethodHead, "/tailnet/whois", s.modules.tailnetGate)
}

// recordTailnetRefusal writes a refusal to the audit trail: who was turned
// away from which environment, and why. The gate folds repeats of one into
// the next record, at most one a minute.
func (s *Server) recordTailnetRefusal(ctx context.Context, refusal tailnetgate.Refusal) {
	detail := map[string]any{"reason": refusal.Reason}
	if refusal.Login != "" {
		detail["login"] = refusal.Login
	}
	if refusal.Name != "" {
		detail["name"] = refusal.Name
	}
	if refusal.Node != "" {
		detail["node"] = refusal.Node
	}
	if len(refusal.Tags) > 0 {
		detail["tags"] = refusal.Tags
	}
	if refusal.Repeated > 0 {
		detail["repeated"] = refusal.Repeated
	}
	encoded, _ := json.Marshal(detail)
	target := ""
	if refusal.EnvironmentID > 0 {
		target = strconv.FormatInt(refusal.EnvironmentID, 10)
	}
	s.Audit.Record(ctx, audit.Entry{
		Actor: "tailnet", Action: "deploy.tailnet.refused", Target: target, IP: refusal.Address,
		Method: http.MethodGet, Path: tailnetgate.Path, Status: refusal.Status, Success: false, Detail: string(encoded),
	})
}

// tailnetGateAPI is this API on loopback as the gate reaches it, or nothing
// when the backend does not answer on 127.0.0.1 — an environment that names
// its visitors then fails to activate rather than going live unchecked.
func (s *Server) tailnetGateAPI() string {
	if s.Cfg.AgentMode {
		return ""
	}
	host, port, err := net.SplitHostPort(s.Cfg.Addr)
	if err != nil {
		return ""
	}
	switch host {
	case "127.0.0.1", "localhost", "", "0.0.0.0", "::":
		return "http://127.0.0.1:" + port
	}
	return ""
}

// preflightTailnet is the tailnet as preflight reads it for a tailnet-only
// plan: this node's name and whether its tailnet issues certificates, the
// environment's address or the port a first publish would take, and which
// proxy would gate it. Every read is one tailscaled, the address table or
// Docker can answer without changing anything.
type preflightTailnet struct{ server *Server }

func (p preflightTailnet) ObserveTailnet(ctx context.Context, environmentID int64) deploy.TailnetObservation {
	s := p.server
	// The pull request panels' reading, kept a minute, when there is one.
	var identity selfcfg.Identity
	if s.modules.pullRequests != nil {
		identity = s.modules.pullRequests.Tailnet(ctx)
	} else {
		identity = selfcfg.DetectTailscale(ctx)
	}
	out := deploy.TailnetObservation{Usable: identity.Usable(), Hostname: identity.Hostname, HTTPS: identity.HTTPSEnabled}
	if !out.Usable {
		out.Detail = identity.Detail
		return out
	}
	if environmentID > 0 {
		if address, err := s.modules.deployRuns.PreviewAddressFor(ctx, environmentID); err == nil && address != nil {
			out.Port = address.Port
			out.URL = address.URL
			if out.URL == "" {
				scheme := "https"
				if !out.HTTPS {
					scheme = "http"
				}
				out.URL = scheme + "://" + out.Hostname + ":" + strconv.Itoa(address.Port)
			}
		}
	}
	if out.URL == "" && s.modules.tailnet != nil {
		if served, err := s.modules.tailnet.ServedTailnetPorts(ctx); err == nil {
			if addresses, err := s.modules.deployRuns.ListPreviewAddresses(ctx); err == nil {
				held := map[int]bool{}
				for _, address := range addresses {
					held[address.Port] = true
				}
				for port := selfcfg.TailnetPortMin; port <= selfcfg.TailnetPortMax; port++ {
					if _, taken := served[port]; !taken && !held[port] {
						out.NextPort = port
						break
					}
				}
			}
		}
	}
	if s.modules.proxy != nil {
		if gate, running, err := s.modules.proxy.TailnetGateHost(ctx); err == nil {
			out.Gate, out.GateRunning = gate, running
		}
	}
	return out
}
