package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountProxyAlertRoutes is the Alerts sheet. Every route is the operator's:
// the rules say which channels are told about what, the history quotes the
// readings, and a pass on demand makes the dashboard dial every upstream and
// watched endpoint. Pausing a rule, muting a subject and removing a rule each
// stop somebody being told, so they sit behind destructive.
func (s *Server) mountProxyAlertRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
	r.Method(http.MethodGet, "/", s.handle(s.handleProxyAlerts))
	r.Method(http.MethodPost, "/rules", s.handle(s.handleProxyAlertCreate))
	r.Method(http.MethodPost, "/test", s.handle(s.handleProxyAlertTest))
	r.Method(http.MethodPost, "/evaluate", s.handle(s.handleProxyAlertEvaluate))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodPut, "/rules/{id}", s.handle(s.handleProxyAlertUpdate))
		r.Method(http.MethodDelete, "/rules/{id}", s.handle(s.handleProxyAlertDelete))
		r.Method(http.MethodPut, "/mutes", s.handle(s.handleProxyAlertMute))
	})
}

type proxyAlertsView struct {
	Rules    []proxyAlertRule    `json:"rules"`
	Subjects []proxyAlertSubject `json:"subjects"`
	History  []proxyAlertEvent   `json:"history"`
	// LastPass is when the last pass finished, absent before the first
	// since the dashboard started.
	LastPass        *time.Time `json:"lastPass,omitempty"`
	IntervalSeconds int        `json:"intervalSeconds"`
}

func (s *Server) proxyAlertsView(ctx context.Context) (*proxyAlertsView, error) {
	alerts := s.modules.proxyExtras.alerts
	rules, err := alerts.Rules(ctx, false)
	if err != nil {
		return nil, err
	}
	subjects, err := alerts.Subjects(ctx)
	if err != nil {
		return nil, err
	}
	history, err := alerts.History(ctx, 100)
	if err != nil {
		return nil, err
	}
	return &proxyAlertsView{
		Rules: rules, Subjects: subjects, History: history,
		LastPass: alerts.LastPass(), IntervalSeconds: int(alerts.interval / time.Second),
	}, nil
}

func (s *Server) handleProxyAlerts(w http.ResponseWriter, r *http.Request) error {
	view, err := s.proxyAlertsView(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func mapProxyAlertError(err error) error {
	switch {
	case errors.Is(err, errProxyAlertNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, errProxyAlertInvalid):
		return httpx.BadRequest("%v", err)
	}
	return httpx.Internal(err)
}

func proxyAlertID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("invalid rule id")
	}
	return id, nil
}

func (s *Server) handleProxyAlertCreate(w http.ResponseWriter, r *http.Request) error {
	var in proxyAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := s.modules.proxyExtras.alerts.CreateRule(r.Context(), in)
	if err != nil {
		return mapProxyAlertError(err)
	}
	httpx.SetAudit(r, "proxy.alerts.rule.create", rule.Kind, map[string]any{"id": rule.ID, "params": rule.Params, "channels": rule.Channels})
	httpx.JSON(w, http.StatusCreated, rule)
	return nil
}

func (s *Server) handleProxyAlertUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := proxyAlertID(r)
	if err != nil {
		return err
	}
	var in proxyAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := s.modules.proxyExtras.alerts.UpdateRule(r.Context(), id, in)
	if err != nil {
		return mapProxyAlertError(err)
	}
	httpx.SetAudit(r, "proxy.alerts.rule.update", rule.Kind, map[string]any{"id": rule.ID, "params": rule.Params, "channels": rule.Channels, "enabled": rule.Enabled})
	httpx.JSON(w, http.StatusOK, rule)
	return nil
}

func (s *Server) handleProxyAlertDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := proxyAlertID(r)
	if err != nil {
		return err
	}
	rule, err := s.modules.proxyExtras.alerts.DeleteRule(r.Context(), id)
	if err != nil {
		return mapProxyAlertError(err)
	}
	httpx.SetAudit(r, "proxy.alerts.rule.delete", rule.Kind, map[string]any{"id": rule.ID})
	httpx.NoContent(w)
	return nil
}

type proxyAlertMuteRequest struct {
	RuleID  int64  `json:"ruleId"`
	Subject string `json:"subject"`
	Muted   bool   `json:"muted"`
}

func (s *Server) handleProxyAlertMute(w http.ResponseWriter, r *http.Request) error {
	var in proxyAlertMuteRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.RuleID <= 0 || in.Subject == "" || len(in.Subject) > 1024 {
		return httpx.BadRequest("a mute names a rule and one of its subjects")
	}
	if err := s.modules.proxyExtras.alerts.SetMuted(r.Context(), in.RuleID, in.Subject, in.Muted); err != nil {
		return mapProxyAlertError(err)
	}
	action := "proxy.alerts.unmute"
	if in.Muted {
		action = "proxy.alerts.mute"
	}
	httpx.SetAudit(r, action, in.Subject, map[string]any{"ruleId": in.RuleID})
	httpx.NoContent(w)
	return nil
}

type proxyAlertTestRequest struct {
	ChannelID int64 `json:"channelId"`
}

// handleProxyAlertTest sends one channel a proxy alert as it would arrive, so
// the message is seen on the operator's phone before it is trusted to wake
// them.
func (s *Server) handleProxyAlertTest(w http.ResponseWriter, r *http.Request) error {
	var in proxyAlertTestRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.ChannelID <= 0 {
		return httpx.BadRequest("choose a notification channel")
	}
	alerts := s.modules.proxyExtras.alerts
	if err := alerts.checkChannels(r.Context(), []int64{in.ChannelID}); err != nil {
		return mapProxyAlertError(err)
	}
	httpx.SetAudit(r, "proxy.alerts.test", strconv.FormatInt(in.ChannelID, 10), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	switch err := alerts.deliver(ctx, in.ChannelID, alerts.TestEnvelope()); {
	case errors.Is(err, deploy.ErrHookDisabled):
		return httpx.Err(http.StatusConflict, "channel_paused", "That channel is paused, so it is told nothing. Resume it on Notification channels first.")
	case err != nil:
		return httpx.Err(http.StatusBadGateway, "delivery_failed", "The channel did not take the message: "+err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": true})
	return nil
}

// handleProxyAlertEvaluate runs a pass now rather than at the next tick, and
// answers the sheet as it stands after it. A kind that must hold still has to
// hold: a pass on demand counts as one pass, not as the minutes it skipped.
func (s *Server) handleProxyAlertEvaluate(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "proxy.alerts.evaluate", "", nil)
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()
	s.modules.proxyExtras.alerts.Evaluate(ctx)
	view, err := s.proxyAlertsView(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
