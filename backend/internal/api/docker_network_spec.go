package api

import (
	"net/http"
	"net/netip"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func (s *Server) authoriseNetworkSpec(r *http.Request, spec dockerx.NetworkSpec) error {
	client, err := netip.ParseAddr(httpx.ClientIP(r))
	if err == nil {
		client = client.Unmap()
		for _, pool := range spec.IPAM {
			prefix, err := netip.ParsePrefix(pool.Subnet)
			if err == nil && prefix.Contains(client) {
				return httpx.Err(http.StatusConflict, "would_lock_you_out", "the address pool includes this dashboard connection's observed client address; choose a different range")
			}
		}
	}
	for key := range spec.Labels {
		// These labels grant ownership to other dashboard and Compose paths;
		// manually creating a network must never impersonate those owners.
		if strings.HasPrefix(key, "io.just-dashboard.") || strings.HasPrefix(key, "com.docker.compose.") {
			return httpx.Err(http.StatusBadRequest, "reserved_label", "Compose and dashboard ownership labels are reserved")
		}
	}
	if spec.Driver != "bridge" || len(spec.Options) != 0 {
		if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
			return httpx.Err(http.StatusForbidden, "requires_admin", "custom network drivers and driver options require system.admin")
		}
	}
	return nil
}
