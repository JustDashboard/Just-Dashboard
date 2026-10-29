package deploy

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// A domain served through Cloudflare Tunnel (cftunnel): a plan's domain with
// `tunnel` set is reached through the connector the dashboard runs, not at
// this server's public address. Cloudflare terminates TLS at its edge, so
// such a name needs no certificate here — the release's certificate step
// leaves it out — and the route serves it over plain HTTP, which only the
// connector on the private network reaches; the public-IP DNS check says
// nothing about it and is skipped; and the tunnel's ingress rules and the
// name's CNAME to <tunnel>.cfargotunnel.com are set as the release goes live.

// TunnelRouter puts the hostnames of a release about to go live on the
// tunnel, beside the ones already live, before its public-route checks ask
// for them.
type TunnelRouter func(ctx context.Context, extra []string) error

// TunnelObserver says whether a tunnel is connected, for preflight.
type TunnelObserver func(ctx context.Context) (name string, connected bool)

// WithTunnelRouter attaches the Cloudflare Tunnel manager's sync.
func (e *NormalizedStepExecutor) WithTunnelRouter(router TunnelRouter) *NormalizedStepExecutor {
	e.tunnel = router
	return e
}

// WithTunnel attaches the tunnel's connection to preflight.
func (o *HostPreflightObserver) WithTunnel(observer TunnelObserver) *HostPreflightObserver {
	o.tunnel = observer
	return o
}

// tunnelHostnames are a release's domains routed through the tunnel.
func tunnelHostnames(domains []PlannedDomain) []string {
	var out []string
	for _, domain := range domains {
		if domain.Tunnel {
			out = append(out, strings.ToLower(strings.TrimSpace(domain.Hostname)))
		}
	}
	return out
}

// routeThroughTunnel is the activation's half: the tunnel routes the
// release's names before anything asks the public route for them. A tunnel
// that cannot be set says so in the transcript and does not fail the
// release, which is serving on this host either way; the manager's pass
// tries again.
func (e *NormalizedStepExecutor) routeThroughTunnel(ctx context.Context, execution StepExecution, domains []PlannedDomain) {
	names := tunnelHostnames(domains)
	if len(names) == 0 {
		return
	}
	if e.tunnel == nil {
		_ = stepLog(execution, "stderr", "This release routes "+strings.Join(names, ", ")+" through Cloudflare Tunnel, and this dashboard runs no tunnel manager")
		return
	}
	if err := e.tunnel(ctx, names); err != nil {
		_ = stepLog(execution, "stderr", "Cloudflare Tunnel: "+err.Error())
		return
	}
	_ = stepLog(execution, "status", "Routed "+strings.Join(names, ", ")+" through Cloudflare Tunnel")
}

// TunnelDomain is one live release's domain routed through the tunnel.
type TunnelDomain struct {
	Hostname      string `json:"hostname"`
	ProjectID     int64  `json:"projectId"`
	EnvironmentID int64  `json:"environmentId"`
	Project       string `json:"project"`
}

// TunnelDomains are every live release's domains routed through the tunnel,
// read from the releases' own snapshots: what is served, not what a plan
// saved since says it will be.
func (s *OrchestrationStore) TunnelDomains(ctx context.Context) ([]TunnelDomain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.project_id, p.name, e.live_release_id
		FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		WHERE e.archived_at = 0 AND p.archived_at = 0 AND e.live_release_id > 0`)
	if err != nil {
		return nil, err
	}
	type live struct {
		environmentID, projectID, releaseID int64
		project                             string
	}
	var lives []live
	for rows.Next() {
		var l live
		if err := rows.Scan(&l.environmentID, &l.projectID, &l.project, &l.releaseID); err != nil {
			rows.Close()
			return nil, err
		}
		lives = append(lives, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []TunnelDomain{}
	for _, l := range lives {
		release, err := s.Release(ctx, l.releaseID)
		if err != nil {
			continue
		}
		snapshot, err := decodeReleaseRuntimeSnapshot(release)
		if err != nil {
			if errors.Is(err, ErrInvalidPlan) {
				continue
			}
			continue
		}
		for _, name := range tunnelHostnames(snapshot.Domains) {
			out = append(out, TunnelDomain{Hostname: name, ProjectID: l.projectID, EnvironmentID: l.environmentID, Project: l.project})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out, nil
}

// tunnelFindings are preflight's word on a domain routed through the
// tunnel, in place of the DNS and certificate findings, which describe this
// server's public address and say nothing about it.
func tunnelFindings(planned PlannedDomain, observation HostObservation, field string) []PreflightFinding {
	tunnel := observation.Tunnel
	if tunnel == nil || !tunnel.Connected {
		item := finding("tunnel_unavailable", PreflightBlocked,
			"No Cloudflare Tunnel is connected", planned.Hostname,
			"This domain is to be served through Cloudflare Tunnel, and the dashboard runs no connector for one, so nothing would reach it.",
			"Connect a tunnel under Proxy › Tunnel, or serve the domain at this server's own address.", "proxy", field)
		item.DeepLink = "/proxy/tunnel"
		return []PreflightFinding{item}
	}
	item := finding("tunnel_domain", PreflightPass,
		"Served through Cloudflare Tunnel", planned.Hostname+" through "+tunnel.Name,
		"Cloudflare terminates HTTPS at its edge, so no certificate is issued here; the route serves the name over plain HTTP on the private network the connector shares with the proxy, and the tunnel's route and the name's record are set as the release goes live.",
		"", "proxy", field)
	item.DeepLink = "/proxy/tunnel"
	return []PreflightFinding{item}
}

// TunnelObservation is the tunnel as preflight sees it.
type TunnelObservation struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}
