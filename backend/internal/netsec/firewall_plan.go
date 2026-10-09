package netsec

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// FirewallPlan is several rule changes reviewed and applied as one: adds
// first, then replacements, then removals, so the firewall is never less
// strict mid-plan than at either end. Rules are named by stable identity,
// never by number.
type FirewallPlan struct {
	Operations []PlanOperation `json:"operations"`
}

// PlanOperation is add (Rule), replace (RuleID with Rule) or delete (RuleID).
type PlanOperation struct {
	Op     string       `json:"op"`
	RuleID string       `json:"ruleId,omitempty"`
	Rule   *RuleRequest `json:"rule,omitempty"`
}

// PlanStep is one operation in the order it runs, described against the
// list as it stands.
type PlanStep struct {
	Op          string `json:"op"`
	RuleID      string `json:"ruleId,omitempty"`
	Number      int    `json:"number,omitempty"`
	Description string `json:"description"`
	// Outcome is pending in a preview; applied, failed, skipped or
	// compensated after an apply.
	Outcome string `json:"outcome"`
	Detail  string `json:"detail,omitempty"`
}

// PlanReview is a plan judged before it runs, or reported after.
type PlanReview struct {
	Preflight
	Steps []PlanStep `json:"steps"`
}

const maxPlanOperations = 20

type preparedStep struct {
	op    PlanOperation
	clean RuleRequest
	old   Rule
}

// preparePlan validates every operation against the current list and the
// lockout guard, orders them, and simulates the result.
func (s *Service) preparePlan(ctx context.Context, b fwBackend, plan FirewallPlan, callerIP string) (*FirewallStatus, *FirewallStatus, []preparedStep, error) {
	if len(plan.Operations) == 0 {
		return nil, nil, nil, fmt.Errorf("a plan holds at least one change")
	}
	if len(plan.Operations) > maxPlanOperations {
		return nil, nil, nil, fmt.Errorf("a plan holds at most %d changes", maxPlanOperations)
	}
	before, err := s.statusOf(ctx, b)
	if err != nil {
		return nil, nil, nil, err
	}
	if before.Error != "" {
		return nil, nil, nil, errors.New(before.Error)
	}
	var adds, replaces, deletes []preparedStep
	named := map[string]bool{}
	for i, op := range plan.Operations {
		p := preparedStep{op: op}
		switch op.Op {
		case "add", "replace":
			if op.Rule == nil {
				return nil, nil, nil, fmt.Errorf("change %d: an %s names a rule", i+1, op.Op)
			}
			if p.clean, err = normaliseRule(*op.Rule); err != nil {
				return nil, nil, nil, fmt.Errorf("change %d: %w", i+1, err)
			}
			if err := guardLockout(p.clean.Action, p.clean.Direction, p.clean.From, callerIP); err != nil {
				return nil, nil, nil, fmt.Errorf("change %d: %w", i+1, err)
			}
		case "delete":
		default:
			return nil, nil, nil, fmt.Errorf("change %d: %q is not add, replace or delete", i+1, op.Op)
		}
		if op.Op != "add" {
			old, ok := ruleWithID(before.Rules, op.RuleID)
			if !ok {
				return nil, nil, nil, fmt.Errorf("change %d: %w", i+1, ErrRuleChanged)
			}
			if named[op.RuleID] {
				return nil, nil, nil, fmt.Errorf("change %d names a rule another change already does", i+1)
			}
			named[op.RuleID] = true
			// The form cannot write a rule on one device or a forwarding rule,
			// so neither can be the replacement, nor be put back as it was if
			// a later step of the plan fails.
			if old.Interface != "" || strings.EqualFold(old.Direction, "FWD") {
				if op.Op == "replace" {
					return nil, nil, nil, fmt.Errorf("change %d: rule %d cannot be expressed by this form; edit it with %s directly", i+1, old.Number, b.Kind())
				}
				return nil, nil, nil, fmt.Errorf("change %d: rule %d could not be written back as it was if the plan failed; remove it on its own", i+1, old.Number)
			}
			p.old = old
		}
		switch op.Op {
		case "add":
			p.clean.Position = 0
			adds = append(adds, p)
		case "replace":
			replaces = append(replaces, p)
		default:
			deletes = append(deletes, p)
		}
	}
	steps := append(append(adds, replaces...), deletes...)
	after := cloneStatus(before)
	for _, p := range steps {
		switch p.op.Op {
		case "add":
			err = simulateAdd(after, p.clean)
		case "replace":
			current, ok := ruleWithID(after.Rules, p.old.ID)
			if !ok {
				return nil, nil, nil, ErrRuleChanged
			}
			err = simulateReplace(after, current.Number, p.clean)
		case "delete":
			current, ok := ruleWithID(after.Rules, p.old.ID)
			if !ok {
				return nil, nil, nil, ErrRuleChanged
			}
			err = simulateDelete(after, current.Number)
		}
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return before, after, steps, nil
}

func describeStep(p preparedStep) PlanStep {
	step := PlanStep{Op: p.op.Op, RuleID: p.op.RuleID, Number: p.old.Number, Outcome: "pending"}
	describe := func(r RuleRequest) string {
		target := firstNonBlank(r.App, r.Port, "any port")
		if r.Port != "" && r.Protocol != "" {
			target += "/" + r.Protocol
		}
		from := firstNonBlank(r.From, "anywhere")
		return fmt.Sprintf("%s %s %s from %s", r.Action, r.Direction, target, from)
	}
	switch p.op.Op {
	case "add":
		step.Description = "Add: " + describe(p.clean)
	case "replace":
		step.Description = fmt.Sprintf("Replace rule %d (%s) with: %s", p.old.Number, p.old.Raw, describe(p.clean))
	case "delete":
		step.Description = fmt.Sprintf("Remove rule %d: %s", p.old.Number, p.old.Raw)
	}
	return step
}

// PreviewPlan reviews a plan without changing anything.
func (s *Service) PreviewPlan(ctx context.Context, plan FirewallPlan, callerIP string) (*PlanReview, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Editable })
	if err != nil {
		return nil, err
	}
	before, after, steps, err := s.preparePlan(ctx, b, plan, callerIP)
	if err != nil {
		return nil, err
	}
	review := &PlanReview{Preflight: *s.preflightOf(ctx, b.Kind(), before, after), Steps: []PlanStep{}}
	for _, p := range steps {
		review.Steps = append(review.Steps, describeStep(p))
	}
	return review, nil
}

// ApplyPlan runs a reviewed plan. Each operation re-resolves its rule by
// identity just before it runs. If one fails, the operations already made are
// taken back in reverse — added rules removed, removed rules written again
// where they were — and the review says which steps did what.
func (s *Service) ApplyPlan(ctx context.Context, plan FirewallPlan, callerIP string) (*PlanReview, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Editable })
	if err != nil {
		return nil, err
	}
	before, after, steps, err := s.preparePlan(ctx, b, plan, callerIP)
	if err != nil {
		return nil, err
	}
	review := &PlanReview{Preflight: *s.preflightOf(ctx, b.Kind(), before, after), Steps: []PlanStep{}}
	if a, ok := accessFrom(ctx); ok {
		if err := compareAccess(before, after, a); err != nil {
			return review, err
		}
	}
	for _, p := range steps {
		review.Steps = append(review.Steps, describeStep(p))
	}
	if checking(ctx) {
		return review, ErrChecked
	}
	var undo []func() error
	for i, p := range steps {
		done, err := s.runPlanStep(ctx, b, p, callerIP)
		if err != nil {
			review.Steps[i].Outcome, review.Steps[i].Detail = "failed", err.Error()
			for j := i + 1; j < len(steps); j++ {
				review.Steps[j].Outcome = "skipped"
			}
			var failures []string
			for k := len(undo) - 1; k >= 0; k-- {
				if uerr := undo[k](); uerr != nil {
					failures = append(failures, uerr.Error())
					review.Steps[k].Detail = "taking it back failed: " + uerr.Error()
				} else {
					review.Steps[k].Outcome = "compensated"
				}
			}
			if len(failures) > 0 {
				return review, fmt.Errorf("plan step %d failed (%w) and %d earlier step(s) could not be taken back: %s", i+1, err, len(failures), strings.Join(failures, "; "))
			}
			return review, fmt.Errorf("plan step %d failed and every earlier step was taken back: %w", i+1, err)
		}
		review.Steps[i].Outcome = "applied"
		undo = append(undo, done)
	}
	return review, nil
}

// runPlanStep makes one change and returns how to take it back.
func (s *Service) runPlanStep(ctx context.Context, b fwBackend, p preparedStep, callerIP string) (func() error, error) {
	current := func(id string) (Rule, error) {
		st, err := s.statusOf(ctx, b)
		if err != nil {
			return Rule{}, err
		}
		r, ok := ruleWithID(st.Rules, id)
		if !ok {
			return Rule{}, ErrRuleChanged
		}
		return r, nil
	}
	removeAdded := func(req RuleRequest) func() error {
		return func() error {
			st, err := s.statusOf(ctx, b)
			if err != nil {
				return err
			}
			want := requestRules(b.Kind(), req)[0]
			for _, r := range st.Rules {
				if r.IPv6 == want.IPv6 && sameRule(r, want) {
					_, err := b.DeleteRule(ctx, r.Number)
					return err
				}
			}
			return nil
		}
	}
	restore := func(old Rule) func() error {
		return func() error {
			req := ruleRequestFor(old)
			if b.Kind() != BackendFirewalld {
				req.Position = old.Number
			}
			_, err := b.AddRule(ctx, req)
			if errors.Is(err, errRuleExists) {
				return nil
			}
			return err
		}
	}
	switch p.op.Op {
	case "add":
		if _, err := b.AddRule(ctx, p.clean); err != nil {
			return nil, err
		}
		return removeAdded(p.clean), nil
	case "replace":
		old, err := current(p.old.ID)
		if err != nil {
			return nil, err
		}
		if _, err := replaceRule(ctx, b, old.Number, p.clean, callerIP); err != nil {
			return nil, err
		}
		remove := removeAdded(p.clean)
		back := restore(old)
		return func() error {
			if err := remove(); err != nil {
				return err
			}
			return back()
		}, nil
	default:
		old, err := current(p.old.ID)
		if err != nil {
			return nil, err
		}
		if _, err := b.DeleteRule(ctx, old.Number); err != nil {
			return nil, err
		}
		return restore(old), nil
	}
}
