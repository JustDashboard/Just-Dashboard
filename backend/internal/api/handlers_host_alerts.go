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

// mountHostAlertRoutes is the host's Alerts page. Every route is the
// operator's, as the proxy's alerts are: the rules say which channels are
// told about what, the history quotes the readings — container names, mount
// points, backup failures — and a test sends a message out of the dashboard.
// Pausing a rule, muting a subject and removing a rule each stop somebody
// being told, so they sit behind destructive.
func (s *Server) mountHostAlertRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
	r.Method(http.MethodGet, "/", s.handle(s.handleHostAlerts))
	r.Method(http.MethodPost, "/rules", s.handle(s.handleHostAlertCreate))
	r.Method(http.MethodPost, "/rules/{id}/test", s.handle(s.handleHostAlertRuleTest))
	r.Method(http.MethodPost, "/test", s.handle(s.handleHostAlertTest))
	r.Method(http.MethodPost, "/evaluate", s.handle(s.handleHostAlertEvaluate))
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodPut, "/rules/{id}", s.handle(s.handleHostAlertUpdate))
		r.Method(http.MethodDelete, "/rules/{id}", s.handle(s.handleHostAlertDelete))
		r.Method(http.MethodPut, "/mutes", s.handle(s.handleHostAlertMute))
	})
}

type hostAlertsView struct {
	Rules    []hostAlertRule    `json:"rules"`
	Subjects []hostAlertSubject `json:"subjects"`
	History  []hostAlertEvent   `json:"history"`
	// LastPass is when the last pass finished, absent before the first
	// since the dashboard started.
	LastPass        *time.Time `json:"lastPass,omitempty"`
	IntervalSeconds int        `json:"intervalSeconds"`
	// Metrics says whether the recorder keeps the history CPU, memory and
	// disk rules read, so the form can say so before a rule is made blind.
	Metrics bool `json:"metrics"`
}

func (s *Server) hostAlertsView(ctx context.Context) (*hostAlertsView, error) {
	alerts := s.modules.hostAlerts
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
	return &hostAlertsView{
		Rules: rules, Subjects: subjects, History: history,
		LastPass: alerts.LastPass(), IntervalSeconds: int(alerts.interval / time.Second),
		Metrics: s.modules.metrics != nil && s.modules.metrics.Enabled(),
	}, nil
}

func (s *Server) handleHostAlerts(w http.ResponseWriter, r *http.Request) error {
	view, err := s.hostAlertsView(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func mapHostAlertError(err error) error {
	switch {
	case errors.Is(err, errHostAlertNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, errHostAlertInvalid):
		return httpx.BadRequest("%v", err)
	}
	return httpx.Internal(err)
}

func hostAlertRuleID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("invalid rule id")
	}
	return id, nil
}

func (s *Server) handleHostAlertCreate(w http.ResponseWriter, r *http.Request) error {
	var in hostAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := s.modules.hostAlerts.CreateRule(r.Context(), in)
	if err != nil {
		return mapHostAlertError(err)
	}
	httpx.SetAudit(r, "host.alerts.rule.create", rule.Kind, map[string]any{"id": rule.ID, "params": rule.Params, "channels": rule.Channels, "enabled": rule.Enabled})
	httpx.JSON(w, http.StatusCreated, rule)
	return nil
}

func (s *Server) handleHostAlertUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := hostAlertRuleID(r)
	if err != nil {
		return err
	}
	var in hostAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := s.modules.hostAlerts.UpdateRule(r.Context(), id, in)
	if err != nil {
		return mapHostAlertError(err)
	}
	httpx.SetAudit(r, "host.alerts.rule.update", rule.Kind, map[string]any{"id": rule.ID, "params": rule.Params, "channels": rule.Channels, "enabled": rule.Enabled})
	httpx.JSON(w, http.StatusOK, rule)
	return nil
}

func (s *Server) handleHostAlertDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := hostAlertRuleID(r)
	if err != nil {
		return err
	}
	rule, err := s.modules.hostAlerts.DeleteRule(r.Context(), id)
	if err != nil {
		return mapHostAlertError(err)
	}
	httpx.SetAudit(r, "host.alerts.rule.delete", rule.Kind, map[string]any{"id": rule.ID})
	httpx.NoContent(w)
	return nil
}

type hostAlertMuteRequest struct {
	RuleID  int64  `json:"ruleId"`
	Subject string `json:"subject"`
	Muted   bool   `json:"muted"`
}

func (s *Server) handleHostAlertMute(w http.ResponseWriter, r *http.Request) error {
	var in hostAlertMuteRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.RuleID <= 0 || in.Subject == "" || len(in.Subject) > 1024 {
		return httpx.BadRequest("a mute names a rule and one of its subjects")
	}
	if err := s.modules.hostAlerts.SetMuted(r.Context(), in.RuleID, in.Subject, in.Muted); err != nil {
		return mapHostAlertError(err)
	}
	action := "host.alerts.unmute"
	if in.Muted {
		action = "host.alerts.mute"
	}
	httpx.SetAudit(r, action, in.Subject, map[string]any{"ruleId": in.RuleID})
	httpx.NoContent(w)
	return nil
}

type hostAlertTestRequest struct {
	ChannelID int64 `json:"channelId"`
}

// handleHostAlertTest sends one channel a host alert as it would arrive, so
// the message is seen on the operator's phone before it is trusted to wake
// them.
func (s *Server) handleHostAlertTest(w http.ResponseWriter, r *http.Request) error {
	var in hostAlertTestRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.ChannelID <= 0 {
		return httpx.BadRequest("choose a notification channel")
	}
	alerts := s.modules.hostAlerts
	if err := alerts.checkChannels(r.Context(), []int64{in.ChannelID}); err != nil {
		return mapHostAlertError(err)
	}
	httpx.SetAudit(r, "host.alerts.test", strconv.FormatInt(in.ChannelID, 10), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	switch err := alerts.deliver(ctx, in.ChannelID, alerts.TestEnvelope(ctx)); {
	case errors.Is(err, deploy.ErrHookDisabled):
		return httpx.Err(http.StatusConflict, "channel_paused", "That channel is paused, so it is told nothing. Resume it on Notification channels first.")
	case err != nil:
		return httpx.Err(http.StatusBadGateway, "delivery_failed", "The channel did not take the message: "+err.Error())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": true})
	return nil
}

// handleHostAlertRuleTest sends a rule's own message, marked as a test, to
// every channel the rule tells, and says how many took it.
func (s *Server) handleHostAlertRuleTest(w http.ResponseWriter, r *http.Request) error {
	id, err := hostAlertRuleID(r)
	if err != nil {
		return err
	}
	alerts := s.modules.hostAlerts
	rule, err := alerts.rule(r.Context(), id)
	if err != nil {
		return mapHostAlertError(err)
	}
	httpx.SetAudit(r, "host.alerts.rule.test", rule.Kind, map[string]any{"id": rule.ID})
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	delivered, failed := alerts.SendTest(ctx, *rule)
	if delivered == 0 && failed == 0 {
		return httpx.Err(http.StatusConflict, "no_channel", "No enabled channel is told by this rule. Add one on Notification channels, or resume a paused one.")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"delivered": delivered, "failed": failed})
	return nil
}

// handleHostAlertEvaluate runs a pass now rather than at the next tick, and
// answers the page as it stands after it. A kind that must hold still has to
// hold: a pass on demand counts as one pass, not as the minutes it skipped.
func (s *Server) handleHostAlertEvaluate(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "host.alerts.evaluate", "", nil)
	ctx, cancel := timeoutCtx(r, time.Minute)
	defer cancel()
	s.modules.hostAlerts.Evaluate(ctx)
	view, err := s.hostAlertsView(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
