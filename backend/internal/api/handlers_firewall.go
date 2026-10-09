package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// firewallAccess is the operator's own way in for this request: their
// address and the device it arrives on, the port the dashboard was reached
// on, sshd's ports, and Caddy's public ingress.
func (s *Server) firewallAccess(r *http.Request) netsec.AccessContext {
	a := netsec.AccessContext{Ingress: []int{80, 443}}
	if s.modules.network != nil {
		a.Client = s.networkClient(r)
		if path, err := s.modules.network.ClientPath(r.Context(), a.Client); err == nil && !path.Local {
			a.Interface = path.Device
		}
		// Public ingress arrives where the default route leaves.
		if uplink, err := s.modules.network.LookupRoute(r.Context(), "1.1.1.1", "", ""); err == nil && !uplink.Local {
			a.Uplink = uplink.Device
		}
	} else {
		a.Client = httpx.ClientIP(r)
	}
	port := 443
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") {
		port = 80
	}
	for _, host := range []string{r.Header.Get("X-Forwarded-Host"), r.Host} {
		if _, p, err := net.SplitHostPort(host); err == nil {
			if n, err := strconv.Atoi(p); err == nil {
				port = n
				break
			}
		}
	}
	a.Dashboard = []int{port}
	if s.modules.netsec != nil {
		for _, raw := range s.modules.netsec.SSHDStatus(r.Context()).Ports {
			if n, err := strconv.Atoi(raw); err == nil {
				a.SSH = append(a.SSH, n)
			}
		}
		a.Profiles = s.modules.netsec.ProfileMap(r.Context())
	}
	if len(a.SSH) == 0 {
		a.SSH = []int{22}
	}
	return a
}

func (s *Server) firewallContext(r *http.Request) context.Context {
	return netsec.WithAccess(r.Context(), s.firewallAccess(r))
}

// protectFirewall runs a ufw or firewalld change inside the network journal,
// with the post-change access verification as its verify step, so a failure
// restores the tool's files at once and an unconfirmed temporary apply is
// restored by the host. The change runs first only as far as its validation
// and access guard, so a refusal opens no journal and sets off no recovery,
// and both passes are bound to the firewall the journal was prepared for.
// The owned nftables table journals itself.
func (s *Server) protectFirewall(ctx context.Context, apply func(context.Context) (string, error)) (string, error) {
	if s.modules.network == nil {
		return apply(ctx)
	}
	state, before, err := s.modules.netsec.RecoveryState(ctx)
	if state.Backend != netsec.BackendUFW && state.Backend != netsec.BackendFirewalld {
		return apply(ctx)
	}
	if err != nil {
		// Neither the access guard nor the recovery can be prepared from a
		// firewall that cannot be read, and a pending apply would otherwise
		// go ahead with no watchdog behind it.
		return "", fmt.Errorf("%w: %v", netsec.ErrUnreadable, err)
	}
	var out string
	err = s.modules.network.ProtectFirewallChange(ctx, netx.FirewallState{
		Backend: string(state.Backend), Enabled: state.Enabled, Unit: state.Unit, Zone: state.Zone,
	}, func(ctx context.Context) error {
		if _, err := apply(netsec.Scoped(ctx, state.Backend, true)); !errors.Is(err, netsec.ErrChecked) {
			return err
		}
		return nil
	}, func(ctx context.Context) error {
		o, err := apply(netsec.Scoped(ctx, state.Backend, false))
		out = o
		return err
	}, func(ctx context.Context) error { return s.modules.netsec.VerifyAccessAfter(ctx, before) })
	return out, err
}

// mapFirewallError distinguishes "this host's firewall cannot do that" from
// "you asked for something invalid", and keeps the network journal's own
// answers (a pending change, an unarmed watchdog) as the network pages give
// them.
func mapFirewallError(err error) error {
	var confirmation *netx.ConfirmationError
	var readOnly *netx.ReadOnlyError
	var guard *netx.GuardError
	switch {
	case errors.As(err, &confirmation), errors.As(err, &readOnly), errors.As(err, &guard), errors.Is(err, netx.ErrUnavailable):
		return mapNetworkError(err)
	case errors.Is(err, netsec.ErrLockout):
		return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
	case errors.Is(err, netsec.ErrRuleChanged):
		return httpx.Err(http.StatusConflict, "rule_changed", "The rule has changed since the list was read; reload the rules and try again.")
	case errors.Is(err, netsec.ErrBackendChanged):
		return httpx.Err(http.StatusConflict, "firewall_changed", err.Error())
	case errors.Is(err, netsec.ErrUnreadable):
		return httpx.Err(http.StatusServiceUnavailable, "firewall_unreadable", err.Error())
	case errors.Is(err, netsec.ErrReadOnly):
		return httpx.Err(http.StatusNotImplemented, "firewall_read_only", err.Error())
	case errors.Is(err, netsec.ErrNoFirewall):
		return httpx.Err(http.StatusServiceUnavailable, "no_firewall", err.Error())
	}
	return httpx.BadRequest("%v", err)
}

// recordFirewall files a rule event; a history failure is logged, never
// returned, since the firewall change itself has happened or been refused.
func (s *Server) recordFirewall(r *http.Request, e netsec.RuleEvent) {
	if s.modules.netsec == nil {
		return
	}
	e.Actor = actor(r)
	if e.Backend == "" {
		e.Backend = s.modules.netsec.Backend()
	}
	if r.Header.Get(networkApplyHeader) == "pending" && s.modules.network != nil {
		if p, ok := httpx.PrincipalFrom(r.Context()); ok {
			if view, err := s.modules.network.ConfirmationStatus(r.Context(), p.UserID()); err == nil && view.Owned && view.Change != nil && view.Change.Phase == "awaiting_confirmation" {
				e.ChangeID = view.Change.ID
			}
		}
	}
	if err := s.modules.netsec.RecordRuleEvent(context.WithoutCancel(r.Context()), e); err != nil && s.Log != nil {
		s.Log.Warn("firewall history could not be recorded", "err", err)
	}
}

func eventJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func outcomeOf(err error) string {
	switch {
	case err == nil:
		return "applied"
	case errors.Is(err, netsec.ErrLockout):
		return "refused"
	}
	return "failed"
}

func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.modules.netsec.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

// handleFirewallAccess is how the requester's own ways in and Caddy's public
// ingress fare under the current rules. Separate from the status, which
// several pages poll, because it asks the kernel and sshd for this request.
func (s *Server) handleFirewallAccess(w http.ResponseWriter, r *http.Request) error {
	ctx := s.firewallContext(r)
	st, err := s.modules.netsec.Status(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	checks := s.modules.netsec.EvaluateAccess(ctx, st)
	if checks == nil {
		checks = []netsec.AccessCheck{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backend": st.Backend, "checks": checks})
	return nil
}

func (s *Server) handleFirewallHistory(w http.ResponseWriter, r *http.Request) error {
	rule := r.URL.Query().Get("rule")
	if rule != "" && !strings.HasPrefix(rule, "fw-") {
		return httpx.BadRequest("rule is a rule identity such as fw-0123456789ab")
	}
	history, err := s.modules.netsec.History(r.Context(), rule, atoiDefault(r.URL.Query().Get("limit"), 100))
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "history_unavailable", err.Error())
	}
	httpx.JSON(w, http.StatusOK, history)
	return nil
}

// handleFirewallPreflight reviews one change named by the query: enable,
// disable, reset, policy (direction, policy) or delete (ruleId). A rule to add
// or several changes are reviewed as a plan.
func (s *Server) handleFirewallPreflight(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	change := netsec.ProposedChange{Op: q.Get("op"), Direction: q.Get("direction"), Policy: q.Get("policy"), RuleID: q.Get("ruleId")}
	switch change.Op {
	case "enable", "disable", "reset", "policy", "delete":
	default:
		return httpx.BadRequest("op is enable, disable, reset, policy or delete; review a rule to add as a plan")
	}
	preflight, err := s.modules.netsec.Preflight(s.firewallContext(r), change)
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.JSON(w, http.StatusOK, preflight)
	return nil
}

func (s *Server) handleFirewallPlanPreview(w http.ResponseWriter, r *http.Request) error {
	var plan netsec.FirewallPlan
	if err := httpx.DecodeJSON(r, &plan); err != nil {
		return err
	}
	review, err := s.modules.netsec.PreviewPlan(s.firewallContext(r), plan, s.networkClient(r))
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.JSON(w, http.StatusOK, review)
	return nil
}

func (s *Server) handleFirewallPlanApply(w http.ResponseWriter, r *http.Request) error {
	var plan netsec.FirewallPlan
	if err := httpx.DecodeJSON(r, &plan); err != nil {
		return err
	}
	ctx := s.firewallContext(r)
	var review *netsec.PlanReview
	_, err := s.protectFirewall(ctx, func(ctx context.Context) (string, error) {
		var err error
		review, err = s.modules.netsec.ApplyPlan(ctx, plan, s.networkClient(r))
		return "", err
	})
	httpx.SetAudit(r, "firewall.plan.apply", strconv.Itoa(len(plan.Operations)), map[string]any{"operations": plan.Operations, "result": outcomeOf(err)})
	if review != nil {
		for _, step := range review.Steps {
			s.recordFirewall(r, netsec.RuleEvent{Operation: "plan." + step.Op, RuleID: step.RuleID, Outcome: firstNonEmptyString(step.Outcome, outcomeOf(err)), Detail: step.Description})
		}
	}
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.JSON(w, http.StatusOK, review)
	return nil
}

func (s *Server) handleFirewallAddRule(w http.ResponseWriter, r *http.Request) error {
	var req netsec.RuleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := s.firewallContext(r)
	// The caller's own address is handed to the firewall layer so it can
	// refuse a rule that would sever this very connection.
	out, err := s.protectFirewall(ctx, func(ctx context.Context) (string, error) {
		return s.modules.netsec.AddRule(ctx, req, s.networkClient(r))
	})
	event := netsec.RuleEvent{Operation: "add", Rule: eventJSON(req), Outcome: outcomeOf(err)}
	if err != nil {
		event.Detail = err.Error()
		s.recordFirewall(r, event)
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "firewall.rule.add", req.Port, map[string]any{"result": "refused_lockout"})
		}
		return mapFirewallError(err)
	}
	if added, ok := s.modules.netsec.FindRuleFor(r.Context(), req); ok {
		event.RuleID = added.ID
	}
	s.recordFirewall(r, event)
	httpx.SetAudit(r, "firewall.rule.add", req.Port, req)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out, "ruleId": event.RuleID})
	return nil
}

// ruleTarget reads the rule an edit or removal names: its listed number,
// and its identity when the client sends one, which survives renumbering.
func ruleTarget(r *http.Request) (int, string, error) {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number <= 0 {
		return 0, "", httpx.BadRequest("invalid rule number")
	}
	return number, r.URL.Query().Get("id"), nil
}

// resolveRule finds the named rule as the firewall lists it now: by its
// identity, or by number for callers that predate identities. It runs inside
// the protected change, under the journal's lock, so a list renumbered since
// the client read it answers rule_changed rather than changing a neighbour.
func (s *Server) resolveRule(ctx context.Context, number int, id string) (netsec.Rule, error) {
	if id == "" {
		return netsec.Rule{Number: number}, nil
	}
	return s.modules.netsec.RuleByID(ctx, id)
}

func (s *Server) handleFirewallReplaceRule(w http.ResponseWriter, r *http.Request) error {
	number, id, err := ruleTarget(r)
	if err != nil {
		return err
	}
	var req netsec.RuleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx := s.firewallContext(r)
	old := netsec.Rule{Number: number, ID: id}
	out, err := s.protectFirewall(ctx, func(ctx context.Context) (string, error) {
		rule, err := s.resolveRule(ctx, number, id)
		if err != nil {
			return "", err
		}
		old, number = rule, rule.Number
		return s.modules.netsec.ReplaceRule(ctx, number, req, s.networkClient(r))
	})
	if errors.Is(err, netsec.ErrRuleChanged) {
		return mapFirewallError(err)
	}
	event := netsec.RuleEvent{Operation: "replace", PreviousRuleID: old.ID, Rule: eventJSON(req), Previous: eventJSON(old), Outcome: outcomeOf(err)}
	if err != nil {
		event.RuleID, event.Detail = old.ID, err.Error()
		s.recordFirewall(r, event)
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "firewall.rule.replace", strconv.Itoa(number), map[string]any{"result": "refused_lockout"})
		}
		return mapFirewallError(err)
	}
	if replaced, ok := s.modules.netsec.FindRuleFor(r.Context(), req); ok {
		event.RuleID = replaced.ID
	}
	s.recordFirewall(r, event)
	httpx.SetAudit(r, "firewall.rule.replace", strconv.Itoa(number), req)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out, "ruleId": event.RuleID})
	return nil
}

func (s *Server) handleFirewallDeleteRule(w http.ResponseWriter, r *http.Request) error {
	number, id, err := ruleTarget(r)
	if err != nil {
		return err
	}
	// No typed phrase: a rule is one line of configuration, visible on the row
	// being deleted and re-addable from the form beside it. Turning the
	// firewall off entirely is the route below, and that also uses ordinary confirmation.
	old := netsec.Rule{Number: number, ID: id}
	out, err := s.protectFirewall(s.firewallContext(r), func(ctx context.Context) (string, error) {
		rule, err := s.resolveRule(ctx, number, id)
		if err != nil {
			return "", err
		}
		old, number = rule, rule.Number
		return s.modules.netsec.DeleteRule(ctx, number)
	})
	if errors.Is(err, netsec.ErrRuleChanged) {
		return mapFirewallError(err)
	}
	event := netsec.RuleEvent{Operation: "delete", RuleID: old.ID, Previous: eventJSON(old), Outcome: outcomeOf(err)}
	if err != nil {
		event.Detail = err.Error()
	}
	s.recordFirewall(r, event)
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.rule.delete", strconv.Itoa(number), map[string]any{"ruleId": old.ID})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

type firewallToggleRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleFirewallToggle(w http.ResponseWriter, r *http.Request) error {
	var req firewallToggleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	out, err := s.protectFirewall(s.firewallContext(r), func(ctx context.Context) (string, error) {
		return s.modules.netsec.SetEnabled(ctx, req.Enabled)
	})
	operation := "disable"
	if req.Enabled {
		operation = "enable"
	}
	s.recordFirewall(r, netsec.RuleEvent{Operation: operation, Outcome: outcomeOf(err), Detail: errorText(err)})
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "firewall.toggle", "", map[string]any{"enabled": req.Enabled, "result": "refused_lockout"})
		}
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.toggle", "", map[string]any{"enabled": req.Enabled})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type firewallPolicyRequest struct {
	Direction string `json:"direction"`
	Policy    string `json:"policy"`
}

func (s *Server) handleFirewallPolicy(w http.ResponseWriter, r *http.Request) error {
	var req firewallPolicyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	out, err := s.protectFirewall(s.firewallContext(r), func(ctx context.Context) (string, error) {
		return s.modules.netsec.SetDefaultPolicy(ctx, req.Direction, req.Policy)
	})
	s.recordFirewall(r, netsec.RuleEvent{Operation: "policy", Rule: eventJSON(req), Outcome: outcomeOf(err), Detail: errorText(err)})
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "firewall.policy", req.Direction, map[string]any{"result": "refused_lockout"})
		}
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.policy", req.Direction, map[string]any{"policy": req.Policy})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

type firewallLoggingRequest struct {
	Level string `json:"level"`
}

func (s *Server) handleFirewallLogging(w http.ResponseWriter, r *http.Request) error {
	var req firewallLoggingRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	out, err := s.modules.netsec.SetLogging(r.Context(), req.Level)
	s.recordFirewall(r, netsec.RuleEvent{Operation: "logging", Rule: eventJSON(req), Outcome: outcomeOf(err), Detail: errorText(err)})
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.logging", req.Level, nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleFirewallReset(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(s.firewallContext(r), 60*time.Second)
	defer cancel()
	out, err := s.protectFirewall(ctx, func(ctx context.Context) (string, error) {
		return s.modules.netsec.Reset(ctx)
	})
	s.recordFirewall(r, netsec.RuleEvent{Operation: "reset", Outcome: outcomeOf(err), Detail: errorText(err)})
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.reset", "", nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

// ownedFirewall adapts the network module's owned nftables table to the
// firewall layer, which offers it as a backend.
type ownedFirewall struct{ net *netx.Service }

func (o ownedFirewall) Available() bool { return o.net.OwnedFirewallAvailable() }

func (o ownedFirewall) Read(ctx context.Context) (netsec.OwnedTable, error) {
	v, err := o.net.OwnedFirewall(ctx)
	if err != nil {
		return netsec.OwnedTable{}, err
	}
	t := netsec.OwnedTable{Enabled: v.Enabled, Incoming: v.Incoming, Runtime: v.Runtime, Foreign: v.Foreign, Raw: v.Raw}
	for _, r := range v.Rules {
		t.Rules = append(t.Rules, netsec.OwnedRule{ID: r.ID, Action: r.Action, Protocol: r.Protocol, Ports: r.Ports, Source: r.Source, Interface: r.Interface, Comment: r.Comment})
	}
	return t, nil
}

func (o ownedFirewall) Change(ctx context.Context, c netsec.OwnedChange) error {
	change := netx.OwnedFirewallChange{Op: c.Op, ID: c.ID, Policy: c.Policy}
	if c.Rule != nil {
		change.Rule = &netx.FirewallRuleRequest{Action: c.Rule.Action, Protocol: c.Rule.Protocol, Ports: c.Rule.Ports,
			Source: c.Rule.Source, Interface: c.Rule.Interface, Comment: c.Rule.Comment, Position: c.Position}
	}
	who := ""
	if p, ok := httpx.PrincipalFrom(ctx); ok {
		who = p.Username()
	}
	return o.net.ChangeOwnedFirewall(ctx, change, who)
}
