package api

import (
	"encoding/pem"
	"errors"
	"net/http"
	"runtime/debug"

	"github.com/Wayy01/Just-Dashboard/backend/internal/agent"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountAgentRoutes exposes the two endpoints a hub needs before it can talk to
// this agent at all. They are mounted only in agent mode, and they sit outside
// the authenticated group because neither can ask for the hub's certificate:
// /info is how a hub learns which certificate it is enrolling against, and
// /enrol is where it hands over its own, once.
//
// Both answer anybody on the allowlisted network, so both take the login rate
// budget per address rather than nothing. A hub's enrolment is two or three
// calls; somebody walking the network for agents, or guessing at a token,
// gets ten a minute.
func (s *Server) mountAgentRoutes(r chi.Router) {
	r.Route("/agent", func(r chi.Router) {
		r.Use(s.loginLim.Middleware)
		r.Method(http.MethodGet, "/info", s.handle(s.handleAgentInfo))
		// Enrolment is the one unauthenticated write in the whole API. It is
		// audited: handing a machine root-equivalent access to this host is
		// the most consequential thing that can happen to an agent, and the
		// trail should show it even though no user was involved.
		r.Group(func(r chi.Router) {
			r.Use(httpx.AuditMutations(s.Audit))
			r.Method(http.MethodPost, "/enrol", s.handle(s.handleAgentEnrol))
		})
	})
}

type agentInfoResponse struct {
	AgentID     string `json:"agentId"`
	Hostname    string `json:"hostname"`
	Version     string `json:"version"`
	Enrolled    bool   `json:"enrolled"`
	EnrolledAt  string `json:"enrolledAt,omitempty"`
	Fingerprint string `json:"fingerprint"`
	CertPEM     string `json:"certPem"`
}

// handleAgentInfo is how a hub discovers what it is pointed at, before and
// after enrolment. Anybody on the allowlisted network may ask, and what they
// learn is the host's name, the build's version, the agent's identifier and
// its public certificate: no address, no configuration, nothing about what
// runs on the host.
func (s *Server) handleAgentInfo(w http.ResponseWriter, r *http.Request) error {
	id := s.Agent
	if id == nil {
		return httpx.Err(http.StatusNotFound, "not_an_agent", "this server is not running in agent mode")
	}
	host, _ := hostexec.Hostname()
	resp := agentInfoResponse{
		AgentID:     id.ID(),
		Hostname:    host,
		Version:     buildVersion(),
		Enrolled:    id.Enrolled(),
		Fingerprint: id.Fingerprint(),
		CertPEM:     string(id.CertPEM()),
	}
	if at := id.EnrolledAt(); !at.IsZero() {
		resp.EnrolledAt = at.Format("2006-01-02T15:04:05Z07:00")
	}
	httpx.JSON(w, http.StatusOK, resp)
	return nil
}

type agentEnrolRequest struct {
	Token      string `json:"token"`
	HubCertPEM string `json:"hubCertPem"`
}

// handleAgentEnrol pins the hub that presents a valid one-time token.
//
// Every failure mode here is a refusal rather than a retry: a wrong token, an
// expired one, or an agent that already has a hub all end the exchange. That
// is what makes a leaked token close to worthless — it is good once, briefly,
// and only against an agent nobody has claimed yet.
func (s *Server) handleAgentEnrol(w http.ResponseWriter, r *http.Request) error {
	id := s.Agent
	if id == nil {
		return httpx.Err(http.StatusNotFound, "not_an_agent", "this server is not running in agent mode")
	}

	var req agentEnrolRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Token == "" || req.HubCertPEM == "" {
		return httpx.Err(http.StatusBadRequest, "invalid_request", "token and hubCertPem are both required")
	}
	block, _ := pem.Decode([]byte(req.HubCertPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return httpx.Err(http.StatusBadRequest, "invalid_request", "hubCertPem is not a PEM certificate")
	}

	switch err := id.Enrol(req.Token, block.Bytes); {
	case err == nil:
	case errors.Is(err, agent.ErrAlreadyEnrolled):
		return httpx.Err(http.StatusConflict, "already_enrolled",
			"this agent is already enrolled; run it with --agent-reset on the host to release it")
	case errors.Is(err, agent.ErrTokenExpired):
		return httpx.Err(http.StatusForbidden, "token_expired",
			"the enrolment token has expired; restart the agent to mint a new one")
	case errors.Is(err, agent.ErrTokenMismatch):
		return httpx.Err(http.StatusForbidden, "token_invalid", "the enrolment token is not valid")
	default:
		return httpx.Internal(err)
	}

	s.Log.Warn("agent enrolled with a hub",
		"agent_id", id.ID(), "hub_fingerprint", id.HubFingerprint())

	host, _ := hostexec.Hostname()
	httpx.JSON(w, http.StatusOK, agentInfoResponse{
		AgentID:     id.ID(),
		Hostname:    host,
		Version:     buildVersion(),
		Enrolled:    true,
		EnrolledAt:  id.EnrolledAt().Format("2006-01-02T15:04:05Z07:00"),
		Fingerprint: id.Fingerprint(),
		CertPEM:     string(id.CertPEM()),
	})
	return nil
}

// buildVersion reports the module version stamped in by the Go toolchain, so a
// hub can tell which agents are behind without a hand-maintained constant.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}
	return info.Main.Version
}
