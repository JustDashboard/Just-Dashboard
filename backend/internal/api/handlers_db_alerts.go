package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountDatabaseAlertRoutes is a database's Alerts page. Reading the rules,
// what they watch and what they told is on the read surface, as every other
// reading of a database is; a rule and its test are the operator's, as every
// other setting that makes the dashboard send something is. Pausing and
// removing a rule stop somebody being told, so an edit and a removal sit
// behind destructive, as the proxy's alert rules do.
//
// The routes are not under /{id}: a rule may watch every database, and one
// for a single database is the dashboard's own state about it, which a
// protected connection does not refuse.
func (s *Server) mountDatabaseAlertRoutes(r chi.Router) {
	r.Route("/alerts", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleDBAlerts))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/rules", s.handle(s.handleDBAlertCreate))
			r.Method(http.MethodPost, "/rules/{rule}/test", s.handle(s.handleDBAlertTest))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPut, "/rules/{rule}", s.handle(s.handleDBAlertUpdate))
				r.Method(http.MethodDelete, "/rules/{rule}", s.handle(s.handleDBAlertDelete))
			})
		})
	})
}

// dbAlertsView is the Alerts page's reading, for one database or every one.
type dbAlertsView struct {
	Rules    []dbAlertRule    `json:"rules"`
	Subjects []dbAlertSubject `json:"subjects"`
	// Events is the newest hundred, newest first.
	Events []dbAlertEvent `json:"events"`
	// LastPass is when the recorder's last pass was judged, absent before
	// the first since the dashboard started; EverySeconds is how often.
	LastPass     *time.Time `json:"lastPass,omitempty"`
	EverySeconds int        `json:"everySeconds"`
}

func (s *Server) handleDBAlerts(w http.ResponseWriter, r *http.Request) error {
	var connection *int64
	if raw := r.URL.Query().Get("connection"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return httpx.BadRequest("connection must be a saved database's id")
		}
		connection = &id
	}
	alerts := s.modules.dbAlerts
	ctx := r.Context()
	rules, err := alerts.Rules(ctx, connection, false)
	if err != nil {
		return httpx.Internal(err)
	}
	subjects, err := alerts.Subjects(ctx, connection)
	if err != nil {
		return httpx.Internal(err)
	}
	// A backup job's subject is drawn on every database it dumps, and only
	// beside a rule that page lists.
	listed := map[int64]bool{}
	for _, rule := range rules {
		listed[rule.ID] = true
	}
	shown := subjects[:0]
	for _, subject := range subjects {
		if listed[subject.RuleID] {
			shown = append(shown, subject)
		}
	}
	events, err := alerts.Events(ctx, connection, 100)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, dbAlertsView{
		Rules: rules, Subjects: shown, Events: events,
		LastPass: alerts.LastPass(), EverySeconds: int(databaseMetricEvery / time.Second),
	})
	return nil
}

func mapDBAlertError(err error) error {
	var api *httpx.APIError
	switch {
	case errors.As(err, &api):
		return err
	case errors.Is(err, errDBAlertNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, errDBAlertInvalid):
		return httpx.BadRequest("%v", err)
	}
	return httpx.Internal(err)
}

func dbAlertRuleID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "rule"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("invalid rule id")
	}
	return id, nil
}

// dbAlertKinds is what a saved database can be watched for: its engine's,
// read as the product that last answered where that is known.
func (s *Server) dbAlertKinds(ctx context.Context) func(int64) ([]string, error) {
	return func(id int64) ([]string, error) {
		conn, dsn, err := s.storedConnectionDSN(ctx, id)
		if errors.Is(err, httpx.ErrNotFound) {
			return nil, invalidDBAlert("database %d is not a saved connection", id)
		}
		if err != nil {
			return nil, err
		}
		flavor := ""
		if identity, _, ok := s.lastIdentity(id, dsn); ok && !conn.Broken {
			flavor = identity.Flavor
		}
		return dbx.AlertKinds(conn.Driver, flavor), nil
	}
}

// dbAlertAudit is what the audit log keeps of a rule: its terms, never a
// reading.
func dbAlertAudit(rule *dbAlertRule) map[string]any {
	return map[string]any{
		"id": rule.ID, "connectionId": rule.ConnectionID, "threshold": rule.Threshold, "minutes": rule.Minutes,
		"cooldownMinutes": rule.CooldownMinutes, "notifyRecovery": rule.NotifyRecovery,
		"channels": rule.Channels, "enabled": rule.Enabled,
	}
}

func (s *Server) handleDBAlertCreate(w http.ResponseWriter, r *http.Request) error {
	var in dbAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := s.modules.dbAlerts.CreateRule(r.Context(), in, s.dbAlertKinds(r.Context()))
	if err != nil {
		return mapDBAlertError(err)
	}
	httpx.SetAudit(r, "database.alert.create", rule.Kind, dbAlertAudit(rule))
	httpx.JSON(w, http.StatusCreated, rule)
	return nil
}

func (s *Server) handleDBAlertUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := dbAlertRuleID(r)
	if err != nil {
		return err
	}
	var in dbAlertWrite
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := s.modules.dbAlerts.UpdateRule(r.Context(), id, in, s.dbAlertKinds(r.Context()))
	if err != nil {
		return mapDBAlertError(err)
	}
	httpx.SetAudit(r, "database.alert.update", rule.Kind, dbAlertAudit(rule))
	httpx.JSON(w, http.StatusOK, rule)
	return nil
}

func (s *Server) handleDBAlertDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := dbAlertRuleID(r)
	if err != nil {
		return err
	}
	rule, err := s.modules.dbAlerts.DeleteRule(r.Context(), id)
	if err != nil {
		return mapDBAlertError(err)
	}
	httpx.SetAudit(r, "database.alert.delete", rule.Kind, dbAlertAudit(rule))
	httpx.NoContent(w)
	return nil
}

// handleDBAlertTest delivers the rule's message as it would arrive, marked as
// a test, to the channels it tells, so it is seen on the operator's phone
// before it is trusted to wake them. A paused channel is told nothing and
// counted apart from one that failed.
func (s *Server) handleDBAlertTest(w http.ResponseWriter, r *http.Request) error {
	id, err := dbAlertRuleID(r)
	if err != nil {
		return err
	}
	alerts := s.modules.dbAlerts
	rule, err := alerts.rule(r.Context(), id)
	if err != nil {
		return mapDBAlertError(err)
	}
	name := ""
	if rule.ConnectionID != nil {
		if conn, _, err := s.storedConnectionDSN(r.Context(), *rule.ConnectionID); err == nil {
			name = conn.Name
		}
	}
	httpx.SetAudit(r, "database.alert.test", rule.Kind, map[string]any{"id": rule.ID, "connectionId": rule.ConnectionID})
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	envelope := alerts.TestEnvelope(*rule, name)
	channels := alerts.recipients(ctx, *rule)
	delivered, failed, paused := 0, 0, 0
	for _, channel := range channels {
		switch err := alerts.deliver(ctx, channel, envelope); {
		case err == nil:
			delivered++
		case errors.Is(err, deploy.ErrHookDisabled):
			paused++
		default:
			failed++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]int{
		"delivered": delivered, "failed": failed, "paused": paused, "channels": len(channels),
	})
	return nil
}
