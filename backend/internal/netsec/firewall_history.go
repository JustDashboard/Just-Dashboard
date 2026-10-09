package netsec

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// RuleEvent is one firewall change the dashboard made or refused, filed
// under the identity of the rule it touched. A replacement links the rule's
// new identity to its previous one, so a rule's history follows its edits.
// Changes made with ufw or firewall-cmd directly leave no event.
type RuleEvent struct {
	ID             int64           `json:"id"`
	At             time.Time       `json:"at"`
	Actor          string          `json:"actor"`
	Backend        Backend         `json:"backend"`
	Operation      string          `json:"operation"`
	RuleID         string          `json:"ruleId,omitempty"`
	PreviousRuleID string          `json:"previousRuleId,omitempty"`
	Rule           json.RawMessage `json:"rule,omitempty"`
	Previous       json.RawMessage `json:"previous,omitempty"`
	Outcome        string          `json:"outcome"`
	Detail         string          `json:"detail,omitempty"`
	ChangeID       string          `json:"changeId,omitempty"`
}

// RuleHistory is the events that match, with what the history cannot say.
type RuleHistory struct {
	Events []RuleEvent `json:"events"`
	Limits []string    `json:"limits"`
}

const ruleHistoryKeep = 2000

var ruleHistoryLimits = []string{
	"Only changes made from this dashboard are recorded; edits made with ufw or firewall-cmd directly leave no entry.",
	"A rule is followed through its edits by identity; a rule recreated by hand with different text starts a new history.",
}

// UseHistory keeps rule events in the dashboard's database.
func (s *Service) UseHistory(db *sql.DB) { s.history = db }

// RecordRuleEvent files one event. A store failure is returned for the
// caller to log; it never undoes the firewall change it describes.
func (s *Service) RecordRuleEvent(ctx context.Context, e RuleEvent) error {
	if s.history == nil {
		return nil
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	tx, err := s.history.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO firewall_rule_events(at,actor,backend,operation,rule_id,previous_rule_id,rule_json,previous_json,outcome,detail,change_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.At.UnixMilli(), e.Actor, string(e.Backend), e.Operation, e.RuleID, e.PreviousRuleID, string(e.Rule), string(e.Previous), e.Outcome, e.Detail, e.ChangeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_rule_events WHERE id IN (SELECT id FROM firewall_rule_events ORDER BY at DESC,id DESC LIMIT -1 OFFSET ?)`, ruleHistoryKeep); err != nil {
		return err
	}
	return tx.Commit()
}

// History lists events, newest first. With a rule identity it follows the
// rule back through every replacement that produced it.
func (s *Service) History(ctx context.Context, ruleID string, limit int) (*RuleHistory, error) {
	if s.history == nil {
		return nil, errors.New("firewall history needs the dashboard's database")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := &RuleHistory{Events: []RuleEvent{}, Limits: ruleHistoryLimits}
	ids := map[string]bool{}
	if ruleID != "" {
		ids[ruleID] = true
		// Follow replacements backwards: each one names the identity it
		// replaced. Bounded, since a cycle would need a hand-made one.
		frontier := []string{ruleID}
		for depth := 0; depth < 32 && len(frontier) > 0; depth++ {
			id := frontier[0]
			frontier = frontier[1:]
			var previous string
			err := s.history.QueryRowContext(ctx, `SELECT previous_rule_id FROM firewall_rule_events WHERE rule_id=? AND previous_rule_id<>'' ORDER BY at DESC,id DESC LIMIT 1`, id).Scan(&previous)
			if err == nil && previous != "" && !ids[previous] {
				ids[previous] = true
				frontier = append(frontier, previous)
			} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
	}
	rows, err := s.history.QueryContext(ctx, `SELECT id,at,actor,backend,operation,rule_id,previous_rule_id,rule_json,previous_json,outcome,detail,change_id FROM firewall_rule_events ORDER BY at DESC,id DESC LIMIT ?`, ruleHistoryKeep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() && len(out.Events) < limit {
		var e RuleEvent
		var at int64
		var backend, rule, previous string
		if err := rows.Scan(&e.ID, &at, &e.Actor, &backend, &e.Operation, &e.RuleID, &e.PreviousRuleID, &rule, &previous, &e.Outcome, &e.Detail, &e.ChangeID); err != nil {
			return nil, err
		}
		if len(ids) > 0 && !ids[e.RuleID] && !ids[e.PreviousRuleID] {
			continue
		}
		e.At, e.Backend = time.UnixMilli(at).UTC(), Backend(backend)
		if rule != "" {
			e.Rule = json.RawMessage(rule)
		}
		if previous != "" {
			e.Previous = json.RawMessage(previous)
		}
		out.Events = append(out.Events, e)
	}
	return out, rows.Err()
}
