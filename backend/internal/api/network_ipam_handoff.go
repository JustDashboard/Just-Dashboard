package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
)

func (s *Server) beginIPAMOwnerHandoff(r *http.Request, ids []string, owner, name string, prefixes []string) (*netipam.Handoff, error) {
	if len(ids) == 0 && len(prefixes) == 0 {
		return nil, nil
	}
	if e := s.modules.ipam.Ready(); e != nil {
		return nil, httpx.Wrap(http.StatusServiceUnavailable, "ipam_unavailable", e)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if len(ids) == 0 {
		if e := s.modules.ipam.CheckUnselectedReservations(ctx, prefixes); e != nil {
			return nil, httpx.Wrap(http.StatusConflict, "ipam_reservation_required", e)
		}
		return nil, nil
	}
	if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		return nil, httpx.Err(http.StatusForbidden, "requires_admin", "selected shared planning reservations require system.admin")
	}
	handoff, e := s.modules.ipam.BeginHandoff(ctx, ids, owner, name, prefixes)
	if e != nil {
		return nil, httpx.Wrap(http.StatusConflict, "ipam_handoff_refused", e)
	}
	return &handoff, nil
}

func (s *Server) finishIPAMOwnerHandoff(handoff *netipam.Handoff, nativeID string, ownerErr error) string {
	if handoff == nil {
		return ""
	}
	// Client cancellation or a lost response must not release or replay native
	// work. Record the available outcome independently; startup holds an unfinished claim.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := s.modules.ipam.FinishHandoff(ctx, *handoff, nativeID, ownerErr); e != nil {
		return "Native creation outcome requires IPAM review; the planning allocation remains held. Inspect the native owner before retrying or releasing the plan."
	}
	return ""
}
