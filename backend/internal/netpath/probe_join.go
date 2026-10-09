package netpath

import (
	"fmt"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// JoinProbe adds selected layers of a connection-path report to a quick
// tool's result: the route lookup gains its policy, NAT and firewall layers,
// the port check its listener ownership and firewall evidence. Each layer
// keeps its basis, so a modeled firewall prediction is never shown as a
// measurement.
func JoinProbe(res *netsec.ProbeResult, path *Result, layers ...string) {
	if res == nil || path == nil {
		return
	}
	want := map[string]bool{}
	for _, id := range layers {
		want[id] = true
	}
	table := netsec.ProbeTable{ID: "path", Title: "Path layers", Columns: []string{"Layer", "Basis", "State", "Summary", "Owner"},
		Note: fmt.Sprintf("Evidence for %s / %d to %s from %s; collected in sequence, not as one packet trace.", strings.ToUpper(path.Scope.Protocol), path.Scope.Port, nonEmpty(path.Scope.Address, path.Scope.Target), nonEmpty(path.Scope.SourceAddress, "the selected source"))}
	for _, e := range path.Evidence {
		if !want[e.ID] {
			continue
		}
		table.Rows = append(table.Rows, []string{e.Title, string(e.Basis), e.State, e.Summary, e.Owner})
		table.RowLinks = append(table.RowLinks, e.OwnerPath)
		for _, f := range e.Facts {
			if f.Value == "" {
				continue
			}
			switch {
			case e.ID == "firewall" && f.Label == "Adapter prediction":
				res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Firewall prediction (" + path.Scope.Protocol + "/" + fmt.Sprint(path.Scope.Port) + ")", Value: f.Value, Basis: netsec.BasisInferred})
			case e.ID == "owner" && (f.Label == "Process" || f.Label == "Container" || f.Label == "Possible dual-stack owner"):
				res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Local " + strings.ToLower(f.Label), Value: f.Value, Basis: netsec.BasisObserved})
			case e.ID == "nat" && strings.HasPrefix(f.Label, "Configured"):
				res.Facts = append(res.Facts, netsec.ProbeFact{Label: f.Label, Value: f.Value, Basis: netsec.BasisConfigured})
			case e.ID == "rules" && len(res.Facts) < 48 && strings.HasPrefix(f.Label, "Priority"):
				res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Policy rule " + strings.TrimPrefix(f.Label, "Priority "), Value: f.Value, Basis: netsec.BasisObserved})
			}
		}
		for _, limit := range e.Limitations {
			if len(res.Limitations) < 12 {
				res.Limitations = append(res.Limitations, e.Title+": "+limit)
			}
		}
	}
	if len(table.Rows) > 0 {
		res.Tables = append(res.Tables, table)
	}
	res.Links = append(res.Links, netsec.ProbeLink{Label: "Investigate this path with every layer", Href: "/network/investigate"})
}

// CorrelatePort reads a port check against the listener and firewall layers
// of the same tuple and names any disagreement. The correlation is a
// reading of two snapshots; it does not establish which layer decided.
func CorrelatePort(res *netsec.ProbeResult, path *Result) {
	if res == nil || path == nil {
		return
	}
	var owner, firewall *Evidence
	for i := range path.Evidence {
		switch path.Evidence[i].ID {
		case "owner":
			owner = &path.Evidence[i]
		case "firewall":
			firewall = &path.Evidence[i]
		}
	}
	prediction := ""
	if firewall != nil {
		for _, f := range firewall.Facts {
			if f.Label == "Adapter prediction" {
				prediction = f.Value
			}
		}
	}
	listener := owner != nil && owner.State == "observed"
	absent := owner != nil && owner.State == "absent"
	local := owner != nil && owner.Basis != Unknown && owner.State != "unknown" || absent
	switch {
	case res.OK && absent:
		res.Findings = append(res.Findings, netsec.ProbeFinding{ID: "no-local-owner", Level: "notice", Title: "Connected, but no matching local listener was seen",
			Detail: "The address is local, yet the socket snapshot shows no listener or Docker publication for this port. A NAT rule or a listener in another namespace may be answering.", Owner: "Ports", Href: "/proxy/ports"})
	case !res.OK && listener:
		res.Findings = append(res.Findings, netsec.ProbeFinding{ID: "listener-not-reached", Level: "warning", Title: "A local listener exists, but the connection failed",
			Detail: "Something on this host listens on the port. The failure points at the firewall, the bound address or the listener's own acceptance; this check does not say which.", Owner: "Host firewall", Href: "/network/firewall"})
	case !res.OK && absent:
		res.Findings = append(res.Findings, netsec.ProbeFinding{ID: "nothing-listening", Level: "notice", Title: "Nothing on this host listens on the port",
			Detail: "The socket snapshot shows no listener or publication, which explains a refusal.", Owner: "Ports", Href: "/proxy/ports"})
	}
	switch {
	case res.OK && (prediction == "deny" || prediction == "reject"):
		res.Findings = append(res.Findings, netsec.ProbeFinding{ID: "firewall-disagrees", Level: "notice", Title: "The firewall model predicted a block, yet TCP connected",
			Detail: "The modeled rule set is incomplete for this flow (foreign chains, state or ordering). Trust the measurement and review the firewall rules.", Owner: "Host firewall", Href: "/network/firewall"})
	case !res.OK && (prediction == "deny" || prediction == "reject"):
		res.Findings = append(res.Findings, netsec.ProbeFinding{ID: "firewall-blocks", Level: "warning", Title: "The host firewall model predicts this flow is blocked",
			Detail: "The outbound rules modeled for this tuple " + prediction + " it, which matches the failure. Other layers may also block it.", Owner: "Host firewall", Href: "/network/firewall"})
	}
	if !local {
		res.Facts = append(res.Facts, netsec.ProbeFact{Label: "Listener ownership", Value: "not local: the remote service's owner cannot be read from this host", Basis: netsec.BasisUnknown})
	}
	if len(res.Findings) > 0 && res.Verdict == netsec.ProbeOK {
		res.Verdict = netsec.ProbeFindings
	}
}

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
