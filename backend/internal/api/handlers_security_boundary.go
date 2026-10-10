package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// The access boundary: how the dashboard and its previews are reached, read
// as it is, so a proposed change can be judged against it before it is made
// and a pending change compared against it before it is confirmed.
//
// Readable by any signed-in principal, as the exposure grade is: it names
// the allowlist, the ports and whether each boundary holds, which every
// role already sees on the Security pages.
func (s *Server) mountSecurityBoundaryRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/security/boundary", s.handle(s.handleAccessBoundary))
	// A GET because judging a proposal changes nothing; a POST would be
	// audited as a mutation every time a form asked.
	r.Method(http.MethodGet, "/security/boundary/check", s.handle(s.handleBoundaryCheck))
}

func (s *Server) handleAccessBoundary(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.accessBoundary(ctx, r))
	return nil
}

type boundaryCheckResponse struct {
	Proposal netsec.BoundaryProposal `json:"proposal"`
	Impacts  []netsec.BoundaryImpact `json:"impacts"`
}

func (s *Server) handleBoundaryCheck(w http.ResponseWriter, r *http.Request) error {
	proposal, err := boundaryProposalFrom(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, boundaryCheckResponse{
		Proposal: proposal,
		Impacts:  netsec.BoundaryImpacts(s.accessBoundary(ctx, r), proposal),
	})
	return nil
}

// boundaryProposalFrom reads a proposal from the query: kind, target, action,
// port, protocol, policy, and setting=key=value for each SSH directive.
func boundaryProposalFrom(r *http.Request) (netsec.BoundaryProposal, error) {
	q := r.URL.Query()
	p := netsec.BoundaryProposal{
		Kind: q.Get("kind"), Target: q.Get("target"), Action: q.Get("action"),
		Port: q.Get("port"), Protocol: q.Get("protocol"), Policy: q.Get("policy"),
	}
	switch p.Kind {
	case "ban", "firewall.rule", "firewall.policy", "ssh":
	default:
		return p, httpx.BadRequest("kind must be ban, firewall.rule, firewall.policy or ssh")
	}
	for _, pair := range q["setting"] {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return p, httpx.BadRequest("a setting is key=value")
		}
		if p.Settings == nil {
			p.Settings = map[string]string{}
		}
		p.Settings[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return p, nil
}

// accessBoundary gathers the boundary's evidence concurrently: the host's
// sockets, the configured ingress, sshd's ports and tailscaled's serve
// configuration. Anything that cannot be read is left unread, which the
// boundary reports as unknown rather than as held.
func (s *Server) accessBoundary(ctx context.Context, r *http.Request) netsec.AccessBoundary {
	in := netsec.BoundaryInput{
		Allowlist: s.Cfg.AllowedCIDRs,
		Client:    httpx.ClientIP(r),
		Now:       time.Now(),
	}
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	run(func() {
		if s.modules.network != nil {
			in.Operator = s.networkClient(r)
		}
	})
	run(func() {
		exposure := netsec.DescribeExposure(s.Cfg.AllowedCIDRs)
		in.TailnetIP, in.TailnetUp = exposure.TailscaleIP, exposure.TailscaleIP != ""
	})
	run(func() {
		in.CaddyPort, in.Binds = 8443, []string{"localhost"}
		if s.modules.selfConfig != nil {
			settings := s.modules.selfConfig.Report(ctx).Settings
			in.CaddyPort = settings.Port
			bind := settings.Bind
			if bind == "" {
				bind = settings.Site
			}
			in.Binds = []string{bind}
			if bind != "localhost" {
				in.Binds = append(in.Binds, "localhost")
			}
		}
	})
	run(func() {
		owners := make(chan proxysvc.OwnerInput, 1)
		go func() { owners <- s.ownerInput(ctx, false) }()
		listeners, err := proxysvc.ListListeners(ctx)
		if err != nil {
			<-owners
			return
		}
		for _, l := range proxysvc.AttributeOwners(listeners, <-owners) {
			in.Listeners = append(in.Listeners, netsec.BoundaryListener{
				Protocol: l.Protocol, Address: l.Address, Port: l.Port, Process: l.Process,
				Caddy: dashboardCaddy(l), Dashboard: l.Self && !dashboardCaddy(l),
			})
		}
		in.ListenersRead = true
	})
	run(func() { in.SSHPorts = s.modules.netsec.SSHDStatus(ctx).Ports })
	run(func() {
		if s.modules.tailnet == nil {
			return
		}
		// A host without the client serves nothing and reads as an empty map.
		served, err := s.modules.tailnet.ServedTailnetPorts(ctx)
		if err != nil {
			return
		}
		for port, upstream := range served {
			in.Previews = append(in.Previews, netsec.PreviewServe{Port: port, Upstream: upstream})
		}
		in.PreviewsRead = true
	})
	wg.Wait()
	return netsec.DescribeBoundary(in)
}

// boundaryGate judges a security mutation against the boundary before it is
// applied. A cut of this session's own way in is refused outright; an effect
// on the boundary for anybody else is refused until the request acknowledges
// it, which the forms do once the operator has seen the same sentences.
func (s *Server) boundaryGate(r *http.Request, action string, proposal netsec.BoundaryProposal, acknowledged bool) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	impacts := netsec.BoundaryImpacts(s.accessBoundary(ctx, r), proposal)
	var cuts, affects []string
	for _, impact := range impacts {
		if impact.Level == netsec.ImpactCuts {
			cuts = append(cuts, impact.Text)
		} else {
			affects = append(affects, impact.Text)
		}
	}
	switch {
	case len(cuts) > 0:
		httpx.SetAudit(r, action, proposal.Target, map[string]any{"result": "refused_boundary", "impacts": impacts})
		return httpx.Err(http.StatusConflict, "would_lock_you_out", strings.Join(cuts, " "))
	case len(affects) > 0 && !acknowledged:
		httpx.SetAudit(r, action, proposal.Target, map[string]any{"result": "boundary_unacknowledged", "impacts": impacts})
		return httpx.Err(http.StatusConflict, "boundary_acknowledgement_required", strings.Join(affects, " "))
	}
	return nil
}

// Pending network changes compare the boundary before the apply with the
// boundary the confirming session sees. The before-picture lives in memory:
// a backend that restarted mid-change says it has none rather than guessing.
var pendingBoundaries sync.Map

type boundaryBaseline struct {
	boundary netsec.AccessBoundary
	at       time.Time
}

func rememberBoundary(id string, b netsec.AccessBoundary) {
	if id == "" {
		return
	}
	pendingBoundaries.Range(func(key, value any) bool {
		if time.Since(value.(boundaryBaseline).at) > 10*time.Minute {
			pendingBoundaries.Delete(key)
		}
		return true
	})
	pendingBoundaries.Store(id, boundaryBaseline{boundary: b, at: time.Now()})
}

// boundaryAfterVerify is attached to a reconnection verification.
type boundaryAfterVerify struct {
	Before  bool                    `json:"before"`
	Checks  []netsec.BoundaryCheck  `json:"checks"`
	Changes []netsec.BoundaryChange `json:"changes"`
	Lost    int                     `json:"lost"`
}

func (s *Server) boundaryAfter(ctx context.Context, r *http.Request, id string) *boundaryAfterVerify {
	after := s.accessBoundary(ctx, r)
	out := &boundaryAfterVerify{Checks: after.Checks, Changes: []netsec.BoundaryChange{}}
	if v, ok := pendingBoundaries.Load(id); ok {
		out.Before = true
		out.Changes = netsec.CompareBoundaries(v.(boundaryBaseline).boundary, after)
		for _, c := range out.Changes {
			if c.Lost {
				out.Lost++
			}
		}
	}
	return out
}
