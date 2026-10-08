package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netflows"
	"github.com/go-chi/chi/v5"
)

func (s *Server) initFlowAccounting() {
	native := &netflows.Native{}
	if s.modules.docker != nil {
		native.Docker = s.modules.docker
	}
	s.modules.flowAccounting = netflows.New(netflows.NewStore(s.Store.DB), native)
	s.modules.flowAccounting.SetObserver(netflows.NewKernelObserver(native.Docker))
}

// Peer and descriptor-owner history has the same privilege as live process
// traffic. Its recorder is independent of page reads and stays off by default.
func (s *Server) mountNetworkFlowRoutes(r chi.Router) {
	r.Route("/flows", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleFlowReport))
		r.Method(http.MethodGet, "/export", s.handle(s.handleFlowExport))
		r.Method(http.MethodPost, "/recording", s.handle(s.handleFlowRecording))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/observer", s.handle(s.handleFlowObserver))
			r.Method(http.MethodPut, "/policy", s.handle(s.handleFlowPolicy))
			r.Method(http.MethodDelete, "/history", s.handle(s.handleFlowClear))
		})
	})
}

func (s *Server) handleFlowObserver(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.flow.observer", "", nil)
	flowPrivate(w)
	if err := s.flowReady(); err != nil {
		return err
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Enabled == nil {
		return httpx.BadRequest("enabled is required")
	}
	httpx.SetAudit(r, "network.flow.observer", "", map[string]any{"enabled": *req.Enabled})
	evidence, err := s.modules.flowAccounting.KernelRecording(r.Context(), *req.Enabled)
	if err != nil {
		return mapFlowError(err)
	}
	httpx.JSON(w, http.StatusOK, evidence)
	return nil
}
func mapFlowError(err error) error {
	switch {
	case errors.Is(err, netflows.ErrInvalid):
		return httpx.BadRequest("%v", err)
	case errors.Is(err, netflows.ErrUnavailable):
		return httpx.Err(http.StatusServiceUnavailable, "flow_accounting_unavailable", err.Error()).Retry()
	default:
		return httpx.Internal(err)
	}
}
func flowQuery(r *http.Request) (netflows.Query, error) {
	q := netflows.Query{}
	v := r.URL.Query()
	for key, values := range v {
		if len(values) != 1 {
			return q, httpx.BadRequest("Each flow filter is supplied once")
		}
		switch key {
		case "from", "to", "address", "containerId", "limit":
		default:
			return q, httpx.BadRequest("Unknown flow filter %s", key)
		}
	}
	for key, dst := range map[string]*time.Time{"from": &q.From, "to": &q.To} {
		if raw := v.Get(key); raw != "" {
			at, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return q, httpx.BadRequest("%s is a UTC hour timestamp", key)
			}
			*dst = at
		}
	}
	q.Address, q.ContainerID = v.Get("address"), v.Get("containerId")
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return q, httpx.BadRequest("limit is an integer")
		}
		q.Limit = n
	}
	return q, nil
}
func (s *Server) flowReady() error {
	if s.modules.flowAccounting == nil {
		return mapFlowError(netflows.ErrUnavailable)
	}
	return nil
}
func flowPrivate(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }
func (s *Server) handleFlowReport(w http.ResponseWriter, r *http.Request) error {
	flowPrivate(w)
	if err := s.flowReady(); err != nil {
		return err
	}
	q, err := flowQuery(r)
	if err != nil {
		return err
	}
	report, err := s.modules.flowAccounting.Report(r.Context(), q)
	if err != nil {
		return mapFlowError(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}
func (s *Server) handleFlowExport(w http.ResponseWriter, r *http.Request) error {
	flowPrivate(w)
	if err := s.flowReady(); err != nil {
		return err
	}
	q, err := flowQuery(r)
	if err != nil {
		return err
	}
	data, err := s.modules.flowAccounting.Export(r.Context(), q)
	if err != nil {
		return mapFlowError(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="socket-observations.json"`)
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(data)
	return err
}
func (s *Server) handleFlowRecording(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.flow.recording", "", nil)
	flowPrivate(w)
	if err := s.flowReady(); err != nil {
		return err
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Enabled == nil {
		return httpx.BadRequest("enabled is required")
	}
	httpx.SetAudit(r, "network.flow.recording", "", map[string]any{"enabled": *req.Enabled})
	settings, err := s.modules.flowAccounting.Recording(r.Context(), *req.Enabled)
	if err != nil {
		return mapFlowError(err)
	}
	httpx.JSON(w, http.StatusOK, settings)
	return nil
}
func (s *Server) handleFlowPolicy(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.flow.policy", "", nil)
	flowPrivate(w)
	if err := s.flowReady(); err != nil {
		return err
	}
	var req struct {
		IntervalSeconds int `json:"intervalSeconds"`
		RetentionDays   int `json:"retentionDays"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.flow.policy", "", map[string]any{"intervalSeconds": req.IntervalSeconds, "retentionDays": req.RetentionDays})
	settings, err := s.modules.flowAccounting.Policy(r.Context(), req.IntervalSeconds, req.RetentionDays)
	if err != nil {
		return mapFlowError(err)
	}
	httpx.JSON(w, http.StatusOK, settings)
	return nil
}
func (s *Server) handleFlowClear(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.flow.clear", "", nil)
	flowPrivate(w)
	if err := s.flowReady(); err != nil {
		return err
	}
	if err := s.modules.flowAccounting.Clear(r.Context()); err != nil {
		return mapFlowError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
