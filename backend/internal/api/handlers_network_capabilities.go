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
	var b strings.Builder
	fmt.Fprintf(&b, "Boot persistence: %s\nIPv6 globally enabled: %t\n\nHost tools:\n", view.Persistence, view.IPv6)
	for _, tool := range view.Tools {
		status := "available"
		if !tool.Available {
			status = "missing (package: " + tool.Package + ")"
		}
		fmt.Fprintf(&b, "%s: %s — %s\n", tool.Tool, status, tool.Purpose)
	}
	for _, note := range view.Notes {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	return &netsec.ProbeResult{Tool: "capabilities", Target: "this host", OK: true, Output: strings.TrimSpace(b.String())}
}
