package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountProxyMetricRoutes is the engine's live request metrics, read from
// nginx's stub_status, and the switch that puts its status server in place.
func (s *Server) mountProxyMetricRoutes(r chi.Router) {
	// Counters of the engine as a whole, read from the address in the
	// dashboard's own status file: nothing secret, and nothing a caller can
	// point anywhere.
	r.Method(http.MethodGet, "/metrics", s.handle(s.handleProxyMetrics))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Switching writes or removes a file in nginx's configuration and
		// reloads nginx, the trust a config save takes. Switching off is not
		// destructive: it stops no site, removes nothing but the dashboard's
		// own file, and the same switch puts it back.
		r.Method(http.MethodPut, "/metrics", s.handle(s.handleProxyMetricsSwitch))
	})
}

// handleProxyMetrics answers with the last hour of readings, or with those
// after ?after=<seq> when ?epoch= names the series the caller already holds.
func (s *Server) handleProxyMetrics(w http.ResponseWriter, r *http.Request) error {
	var cursor [2]int64
	for i, name := range []string{"epoch", "after"} {
		value := r.URL.Query().Get(name)
		if value == "" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			return httpx.BadRequest("%s must be a whole number", name)
		}
		cursor[i] = n
	}
	httpx.JSON(w, http.StatusOK, s.modules.proxyExtras.statusMetrics.Report(cursor[0], cursor[1]))
	return nil
}

type proxyMetricsSwitch struct {
	Enabled *bool `json:"enabled"`
}

// proxyMetricsBudget bounds one switch: two config tests, the dump that proves
// nginx reads conf.d, a reload and the wait for the first reading.
const proxyMetricsBudget = 90 * time.Second

// handleProxyMetricsSwitch puts the status server in place or takes it out.
//
// The change runs to its end whether or not the browser waits for it: each
// step either leaves the host as it found it or finishes, and a request
// cancelled halfway would stop between a reload and the undo that follows it.
func (s *Server) handleProxyMetricsSwitch(w http.ResponseWriter, r *http.Request) error {
	var req proxyMetricsSwitch
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Enabled == nil {
		return httpx.BadRequest("enabled is required")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), proxyMetricsBudget)
	defer cancel()
	sampler := s.modules.proxyExtras.statusMetrics
	event, verb := "proxy.metrics.disable", "taken out"
	var change *proxysvc.StatusChange
	var err error
	if *req.Enabled {
		event, verb = "proxy.metrics.enable", "added"
		change, err = sampler.Enable(ctx)
	} else {
		change, err = sampler.Disable(ctx)
	}
	target := s.modules.proxy.StatusServer().Path
	if err != nil {
		detail := map[string]any{"result": "failed", "error": err.Error()}
		if change != nil {
			detail["port"], detail["reloaded"] = change.Port, change.Reloaded
		}
		httpx.SetAudit(r, event, target, detail)
		if errors.Is(err, proxysvc.ErrInvalidConf) && change != nil && change.Validation != nil {
			return refuseInvalidConfig(w, r, httpx.Err(http.StatusUnprocessableEntity, "invalid_config",
				fmt.Sprintf("nginx's configuration test failed, so the status server was not %s: %s", verb, firstFailure(change.Validation))),
				change.Validation)
		}
		return mapProxyMetricsError(err)
	}
	httpx.SetAudit(r, event, target, map[string]any{"port": change.Port, "changed": change.Changed, "reloaded": change.Reloaded})
	httpx.JSON(w, http.StatusOK, sampler.Report(0, 0))
	return nil
}

// firstFailure is the line of a failed test that says why: its first error,
// placed at its file, or the last thing the engine wrote.
func firstFailure(res *proxysvc.ValidationResult) string {
	for _, d := range res.Diagnostics {
		switch d.Level {
		case "emerg", "alert", "crit", "error":
			if d.File != "" {
				return fmt.Sprintf("%s in %s:%d", d.Message, d.File, d.Line)
			}
			return d.Message
		}
	}
	return lastLine(res.Output)
}

func mapProxyMetricsError(err error) error {
	switch {
	case errors.Is(err, proxysvc.ErrStatusForeign),
		errors.Is(err, proxysvc.ErrStatusUnsupported),
		errors.Is(err, proxysvc.ErrStatusNotIncluded):
		return httpx.Err(http.StatusConflict, "metrics_unavailable", err.Error())
	case errors.Is(err, proxysvc.ErrStatusReload):
		return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
	case errors.Is(err, proxysvc.ErrStatusNoAnswer):
		return httpx.Err(http.StatusBadGateway, "no_answer", err.Error())
	}
	return mapProxyError(err)
}
