package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetworkVantageRoutes(r chi.Router) {
	r.Route("/external", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method("GET", "/vantages", s.handle(s.handleNetworkVantages))
		r.Method("GET", "/checks", s.handle(s.handleNetworkExternalChecks))
		r.Method("POST", "/checks", s.handle(s.handleNetworkExternalCheck))
		r.Method("GET", "/compare", s.handle(s.handleNetworkExternalCompare))
		r.Method("POST", "/checks/{id}/cancel", s.handle(s.handleNetworkExternalCancel))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method("POST", "/enrollments", s.handle(s.handleNetworkVantageEnrollment))
			s.destructive(r, func(r chi.Router) { r.Method("DELETE", "/vantages/{id}", s.handle(s.handleNetworkVantageRevoke)) })
		})
	})
}

// Call this inside /api/v1 after the allowlist and before human authentication.
// This namespace recognises only its dedicated proof/signatures, never a user
// token or ambient cookie, and it grants no dashboard feature capability.
func (s *Server) mountNetworkVantageMachineRoutes(r chi.Router) {
	limit := httpx.NewLimiter(30, 6)
	r.Route("/probe-agent", func(r chi.Router) {
		r.Use(limit.Middleware)
		r.Use(httpx.AuditMutations(s.Audit))
		r.Method("POST", "/enroll", s.handle(s.handleProbeAgentEnroll))
		r.Method("POST", "/poll", s.handle(s.handleProbeAgentPoll))
		r.Method("POST", "/result", s.handle(s.handleProbeAgentResult))
	})
}
func (s *Server) vantageReady() error {
	if e := s.modules.networkVantages.Ready(); e != nil {
		return httpx.Err(503, "vantage_unavailable", fmt.Sprintf("Controlled-vantage state is unavailable: %s", e))
	}
	return nil
}
func (s *Server) handleNetworkVantages(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	values, e := s.modules.networkVantages.Vantages(r.Context())
	if e != nil {
		return httpx.Internal(e)
	}
	httpx.JSON(w, 200, values)
	return nil
}
func (s *Server) handleNetworkVantageEnrollment(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	var input netvantage.EnrollmentRequest
	if e := httpx.DecodeJSON(r, &input); e != nil {
		return e
	}
	httpx.SetAudit(r, "network.vantage.enroll", input.Name, map[string]any{"placement": input.Placement, "scopes": len(input.Scopes)})
	enrollment, e := s.modules.networkVantages.CreateEnrollment(r.Context(), input)
	if e != nil {
		return httpx.BadRequest("%s", e)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, 201, enrollment)
	return nil
}
func (s *Server) handleNetworkVantageRevoke(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.vantage.revoke", id, nil)
	if e := s.modules.networkVantages.Revoke(r.Context(), id); e != nil {
		return httpx.BadRequest("%s", e)
	}
	httpx.JSON(w, 200, map[string]bool{"revoked": true})
	return nil
}
func (s *Server) handleNetworkExternalChecks(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	checks, e := s.modules.networkVantages.Checks(r.Context())
	if e != nil {
		return httpx.Internal(e)
	}
	httpx.JSON(w, 200, checks)
	return nil
}
func (s *Server) handleNetworkExternalCheck(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	var input netvantage.Request
	if e := httpx.DecodeJSON(r, &input); e != nil {
		return e
	}
	httpx.SetAudit(r, "network.external.check", input.VantageID, map[string]any{"scopeId": input.ScopeID, "family": input.Family, "port": input.Port, "tls": input.TLS})
	check, e := s.modules.networkVantages.CreateCheck(r.Context(), input, httpx.MustPrincipal(r).Username())
	if e != nil {
		return httpx.BadRequest("%s", e)
	}
	httpx.JSON(w, 202, check)
	return nil
}
func (s *Server) handleNetworkExternalCancel(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	id := chi.URLParam(r, "id")
	httpx.SetAudit(r, "network.external.cancel", id, nil)
	if e := s.modules.networkVantages.Cancel(r.Context(), id); e != nil {
		return httpx.BadRequest("%s", e)
	}
	httpx.JSON(w, 200, map[string]bool{"cancelled": true, "lateResultsRejected": true})
	return nil
}
func (s *Server) handleNetworkExternalCompare(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	checks, e := s.modules.networkVantages.Checks(r.Context())
	if e != nil {
		return httpx.Internal(e)
	}
	var left, right *netvantage.Check
	for i := range checks {
		if checks[i].ID == r.URL.Query().Get("before") {
			left = &checks[i]
		}
		if checks[i].ID == r.URL.Query().Get("after") {
			right = &checks[i]
		}
	}
	if left == nil || right == nil {
		return httpx.Err(404, "probe_check_unavailable", "One or both retained checks are unavailable")
	}
	httpx.JSON(w, 200, map[string]any{"before": left, "after": right, "comparison": netvantage.Compare(*left, *right)})
	return nil
}
func decodeMachine(body []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return httpx.BadRequest("invalid bounded machine payload")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return httpx.BadRequest("one machine payload is required")
	}
	return nil
}
func machineBody(r *http.Request, limit int64) ([]byte, error) {
	body, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil || int64(len(body)) > limit {
		return nil, httpx.Err(413, "probe_payload_limit", "Machine payload exceeds its fixed bound")
	}
	return body, nil
}
func (s *Server) handleProbeAgentEnroll(w http.ResponseWriter, r *http.Request) error {
	if e := s.vantageReady(); e != nil {
		return e
	}
	httpx.SetAuditActor(r, "probe-agent-enrollment")
	httpx.SetAudit(r, "network.vantage.claim", "", nil)
	body, e := machineBody(r, 4096)
	if e != nil {
		return e
	}
	var claim netvantage.Claim
	if e = decodeMachine(body, &claim); e != nil {
		return e
	}
	manifest, e := s.modules.networkVantages.Claim(r.Context(), claim)
	if e != nil {
		return httpx.Err(401, "vantage_enrollment_invalid", "Enrollment is invalid, expired or already used")
	}
	httpx.SetAudit(r, "network.vantage.claim", manifest.Vantage.ID, nil)
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, 200, manifest)
	return nil
}
func (s *Server) authenticateProbeAgent(r *http.Request) (netvantage.Signature, []byte, error) {
	var signature netvantage.Signature
	if e := s.vantageReady(); e != nil {
		return signature, nil, e
	}
	signature.ID = r.Header.Get("X-JD-Vantage")
	signature.ServerKey = r.Header.Get("X-JD-Probe-Server")
	signature.Value = r.Header.Get("X-JD-Probe-Signature")
	signature.Sequence, _ = strconv.ParseInt(r.Header.Get("X-JD-Probe-Sequence"), 10, 64)
	signature.Timestamp, _ = strconv.ParseInt(r.Header.Get("X-JD-Probe-Time"), 10, 64)
	httpx.SetAuditActor(r, "probe-agent")
	httpx.SetAudit(r, "network.vantage.request", "", nil)
	body, e := machineBody(r, netvantage.MaxBody)
	if e != nil {
		return signature, nil, e
	}
	if e = s.modules.networkVantages.Authenticate(r.Context(), r.Method, r.URL.RequestURI(), body, signature, httpx.ClientIP(r)); e != nil {
		return signature, nil, httpx.Err(401, "vantage_authentication_failed", "The dedicated machine signature is invalid or stale")
	}
	httpx.SetAuditActor(r, "probe-agent:"+signature.ID)
	return signature, body, nil
}
func (s *Server) handleProbeAgentPoll(w http.ResponseWriter, r *http.Request) error {
	signature, body, e := s.authenticateProbeAgent(r)
	if e != nil {
		return e
	}
	if len(body) != 0 {
		return httpx.BadRequest("Polling has no request-built probe content")
	}
	httpx.SetAudit(r, "network.vantage.poll", signature.ID, nil)
	job, e := s.modules.networkVantages.Poll(r.Context(), signature.ID)
	if e != nil {
		return httpx.Internal(e)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, 200, map[string]any{"job": job, "serverTime": time.Now().UTC()})
	return nil
}
func (s *Server) handleProbeAgentResult(w http.ResponseWriter, r *http.Request) error {
	signature, body, e := s.authenticateProbeAgent(r)
	if e != nil {
		return e
	}
	var result netvantage.Result
	if e = decodeMachine(body, &result); e != nil {
		return e
	}
	httpx.SetAudit(r, "network.vantage.result", signature.ID, map[string]any{"checkId": result.CheckID, "family": result.Request.Family, "port": result.Request.Port})
	if e = s.modules.networkVantages.Complete(r.Context(), signature.ID, result); e != nil {
		return httpx.Err(409, "vantage_result_rejected", fmt.Sprintf("Result is outside the enrolled job, bounds or lease: %s", e))
	}
	httpx.JSON(w, 200, map[string]bool{"recorded": true})
	return nil
}
