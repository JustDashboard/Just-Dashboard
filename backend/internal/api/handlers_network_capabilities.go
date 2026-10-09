package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func (s *Server) handleNetworkCapabilities(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.modules.network.HostSupport(ctx))
	return nil
}

func networkSupportProbe(view netx.HostSupport) *netsec.ProbeResult {
	res := &netsec.ProbeResult{Tool: "capabilities", Target: "this host", OK: true}
	var b strings.Builder
	fmt.Fprintf(&b, "Boot persistence: %s\nIPv6: %s\n\nHost tools:\n", view.Persistence, view.IPv6State)
	tools := netsec.ProbeTable{ID: "tools", Title: "Host tools", Columns: []string{"Tool", "Present", "Package", "Used for"}}
	present := 0
	for _, tool := range view.Tools {
		status := "available"
		if !tool.Available {
			status = "missing (package: " + tool.Package + ")"
		} else {
			present++
		}
		fmt.Fprintf(&b, "%s: %s — %s\n", tool.Tool, status, tool.Purpose)
		tools.Rows = append(tools.Rows, []string{tool.Tool, map[bool]string{true: "yes", false: "no"}[tool.Available], tool.Package, tool.Purpose})
	}
	probes := netsec.ProbeTable{ID: "probes", Title: "Capability probes", Columns: []string{"Capability", "Status", "Evidence", "Enables"},
		Note: "Each probe is a read or an open-and-close; none changes host configuration."}
	counts := map[string]int{}
	b.WriteString("\nCapability probes:\n")
	for _, p := range view.Probes {
		counts[p.Status]++
		probes.Rows = append(probes.Rows, []string{p.Label, p.Status, p.Evidence, p.Enables})
		fmt.Fprintf(&b, "%s: %s — %s\n", p.Label, p.Status, p.Evidence)
		if p.Status == netx.ProbeRestricted || p.Status == netx.ProbeUnsupported {
			res.Findings = append(res.Findings, netsec.ProbeFinding{ID: "probe-" + p.ID, Level: "notice", Title: p.Label + " is " + p.Status, Detail: p.Evidence + ". Affects " + p.Enables + ".", Owner: "Host kernel and container privileges"})
		}
	}
	for _, note := range view.Notes {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	res.Tables = append(res.Tables, probes, tools)
	res.Facts = append(res.Facts,
		netsec.ProbeFact{Label: "Boot persistence", Value: view.Persistence, Basis: netsec.BasisObserved},
		netsec.ProbeFact{Label: "IPv6", Value: view.IPv6State, Basis: netsec.BasisObserved},
	)
	res.Limitations = append(res.Limitations, view.Notes...)
	res.Output = strings.TrimSpace(b.String())
	res.Verdict = netsec.ProbeOK
	if len(res.Findings) > 0 {
		res.Verdict = netsec.ProbeFindings
	}
	res.Summary = fmt.Sprintf("%d of %d tools present; capability probes: %d supported, %d restricted, %d unsupported, %d unknown.",
		present, len(view.Tools), counts[netx.ProbeSupported], counts[netx.ProbeRestricted], counts[netx.ProbeUnsupported], counts[netx.ProbeUnknown])
	return res
}
