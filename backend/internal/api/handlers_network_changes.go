package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

const networkApplyHeader = "X-JD-Network-Apply"

// Only operations with serialized netx undo coverage may opt into pending apply.
// Firewall, DNS, namespaces and VPN have separate owners and are not enrolled.
func supportsPendingNetworkApply(path string) bool {
	path = strings.TrimSuffix(path, "/")
	if path == "/network/drift/repairs" {
		return true
	}
	for _, prefix := range []string{"/network/links", "/network/routing/routes", "/network/routing/rules", "/network/forwarding", "/network/shaping", "/network/gateway/forwards", "/network/gateway/nat", "/network/protection/limits", "/network/protection/blocklists", "/network/protection/settings", "/network/protection/trusted"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// pendingResponse keeps existing response bodies intact while attaching the
// journal identity after the handler's authenticated, guarded apply finishes.
type pendingResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *pendingResponse) Header() http.Header { return w.header }
func (w *pendingResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *pendingResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func (s *Server) pendingNetworkApply(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := r.Header.Get(networkApplyHeader)
		if mode == "" {
			next.ServeHTTP(w, r)
			return
		}
		if mode != "pending" || (r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete) || !supportsPendingNetworkApply(strings.TrimPrefix(r.URL.Path, "/api/v1")) {
			httpx.WriteError(w, r, httpx.BadRequest("Pending apply is supported only for managed link, routing, shaping, gateway and kernel-setting mutations."))
			return
		}
		p := httpx.MustPrincipal(r)
		if p.Kind != "session" || p.SessionID == "" || p.UserID() <= 0 || !p.Can(auth.CapSystemAdmin) {
			httpx.WriteError(w, r, httpx.Err(http.StatusForbidden, "session_required", "Pending network apply requires an administrator's interactive session."))
			return
		}
		r = r.WithContext(netx.WithPendingConfirmation(r.Context(), p.UserID()))
		response := &pendingResponse{header: make(http.Header)}
		next.ServeHTTP(response, r)
		if s.modules.network != nil {
			view, err := s.modules.network.ConfirmationStatus(r.Context(), p.UserID())
			if err == nil && view.Owned && view.Change != nil && view.Change.Phase == "awaiting_confirmation" {
				response.header.Set("X-JD-Network-Change", view.Change.ID)
				response.header.Set("X-JD-Network-Expires", view.Change.ExpiresAt.Format(time.RFC3339Nano))
			}
		}
		for name, values := range response.header {
			w.Header()[name] = values
		}
		if response.status == 0 {
			response.status = http.StatusOK
		}
		w.WriteHeader(response.status)
		_, _ = io.Copy(w, &response.body)
	})
}

func (s *Server) mountNetworkChangeRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireSession)
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/changes/current", s.handle(s.handleNetworkChangeStatus))
		r.Method(http.MethodPost, "/changes/{id}/verify", s.handle(s.handleNetworkChangeVerify))
		r.Method(http.MethodPost, "/changes/{id}/confirm", s.handle(s.handleNetworkChangeConfirm))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/changes/{id}/recover", s.handle(s.handleNetworkChangeRecover))
		})
	})
}

func (s *Server) handleNetworkChangeStatus(w http.ResponseWriter, r *http.Request) error {
	view, err := s.modules.network.ConfirmationStatus(r.Context(), httpx.MustPrincipal(r).UserID())
	if err != nil {
		return mapNetworkConfirmationError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleNetworkChangeVerify(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	id := chi.URLParam(r, "id")
	result, err := s.modules.network.VerifyReconnection(r.Context(), id, p.UserID(), p.SessionID, p.IP)
	httpx.SetAudit(r, "network.change.verify", id, nil)
	if err != nil {
		return mapNetworkConfirmationError(err)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleNetworkChangeConfirm(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Challenge string `json:"challenge"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	id := chi.URLParam(r, "id")
	result, err := s.modules.network.ConfirmChange(r.Context(), id, p.UserID(), p.SessionID, req.Challenge, p.IP)
	httpx.SetAudit(r, "network.change.confirm", id, nil)
	if err != nil {
		return mapNetworkConfirmationError(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleNetworkChangeRecover(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	ctx, cancel := applyContext(r, 60*time.Second)
	defer cancel()
	result, err := s.modules.network.RecoverOwnedChange(ctx, id, httpx.MustPrincipal(r).UserID())
	httpx.SetAudit(r, "network.change.recover", id, nil)
	if err != nil {
		return mapNetworkConfirmationError(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func mapNetworkConfirmationError(err error) error {
	var refusal *netx.ConfirmationError
	if errors.As(err, &refusal) {
		return httpx.Err(http.StatusConflict, "network_confirmation", refusal.Error())
	}
	return mapNetworkError(err)
}
