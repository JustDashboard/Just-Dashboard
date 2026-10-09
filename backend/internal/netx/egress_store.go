package netx

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Egress decisions and measurements are the dashboard's record of what it
// saw and did, kept in SQLite beside the other network observations. Without
// a store (tests, a broken database) they are kept in memory, bounded the
// same way, and the view says so.

const (
	egressEventRetention  = 90 * 24 * time.Hour
	egressEventsPerGroup  = 2000
	egressSampleRetention = 24 * time.Hour
	egressSimulationsKept = 10
)

// EgressEvent is one decision or observation, as recorded.
type EgressEvent struct {
	ID      int64     `json:"id"`
	GroupID int       `json:"groupId"`
	At      time.Time `json:"at"`
	// Kind is state (a member went up or down), switch (the decided members
	// changed), hold (a switch the rules allow is waiting, or the rules say
	// why nothing moves), refused (a guard declined a switch), failed (the
	// change could not be made), verify (the decided route was checked
	// against the kernel), connections (tracked connections were flushed),
	// simulation, automation or config.
	Kind string `json:"kind"`
	// Action is failover, failback, rebalance or manual for a switch.
	Action   string `json:"action,omitempty"`
	MemberID int    `json:"memberId,omitempty"`
	// Outcome is applying, pending (awaiting reconnection confirmation),
	// applied, recovered, refused, failed, interrupted, unknown or observed.
	Outcome  string          `json:"outcome,omitempty"`
	Reason   string          `json:"reason"`
	Actor    string          `json:"actor,omitempty"`
	Before   []int           `json:"before,omitempty"`
	After    []int           `json:"after,omitempty"`
	Evidence *EgressEvidence `json:"evidence,omitempty"`
}

// EgressEvidence is what a decision was taken on and what it did.
type EgressEvidence struct {
	Members     []EgressMemberEvidence `json:"members,omitempty"`
	RouteBefore string                 `json:"routeBefore,omitempty"`
	RouteAfter  string                 `json:"routeAfter,omitempty"`
	Connections *EgressConnections     `json:"connections,omitempty"`
	Change      string                 `json:"change,omitempty"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
}

// EgressMemberEvidence is one member's measurement at a decision.
type EgressMemberEvidence struct {
	ID            int       `json:"id"`
	State         string    `json:"state"`
	Since         time.Time `json:"since,omitzero"`
	LatencyMillis float64   `json:"latencyMillis,omitempty"`
	Loss          float64   `json:"loss"`
	OK            int       `json:"ok"`
	Total         int       `json:"total"`
	Why           string    `json:"why,omitempty"`
	At            time.Time `json:"at,omitzero"`
}

type egressStore struct {
	db *sql.DB
	mu sync.Mutex
	// Without a database, the events, samples and simulations live here.
	events      []EgressEvent
	nextID      int64
	simulations map[string]EgressSimulation
}

func newEgressStore(db *sql.DB) *egressStore {
	return &egressStore{db: db, simulations: map[string]EgressSimulation{}}
}

func (st *egressStore) persistent() bool { return st.db != nil }

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func splitIDs(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		if id, err := strconv.Atoi(part); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// record writes an event and returns its id.
func (st *egressStore) record(ctx context.Context, e EgressEvent) int64 {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if st.db == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.nextID++
		e.ID = st.nextID
		st.events = append(st.events, e)
		if len(st.events) > egressEventsPerGroup*egressSlots {
			st.events = st.events[len(st.events)-egressEventsPerGroup*egressSlots:]
		}
		return e.ID
	}
	var evidence string
	if e.Evidence != nil {
		b, _ := json.Marshal(e.Evidence)
		evidence = string(b)
	}
	res, err := st.db.ExecContext(ctx, `INSERT INTO network_egress_events(group_id,at,kind,action,member_id,outcome,reason,actor,before_members,after_members,evidence) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.GroupID, e.At.UnixMilli(), e.Kind, e.Action, e.MemberID, e.Outcome, e.Reason, e.Actor, joinIDs(e.Before), joinIDs(e.After), evidence)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// finish updates an applying switch with its outcome and what it did.
func (st *egressStore) finish(ctx context.Context, id int64, outcome, reason string, evidence *EgressEvidence) {
	if id == 0 {
		return
	}
	if st.db == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		for i := range st.events {
			if st.events[i].ID == id {
				st.events[i].Outcome = outcome
				if reason != "" {
					st.events[i].Reason = reason
				}
				if evidence != nil {
					st.events[i].Evidence = evidence
				}
			}
		}
		return
	}
	var raw string
	if evidence != nil {
		b, _ := json.Marshal(evidence)
		raw = string(b)
	}
	if reason == "" {
		_, _ = st.db.ExecContext(ctx, `UPDATE network_egress_events SET outcome=?, evidence=CASE WHEN ?='' THEN evidence ELSE ? END WHERE id=?`, outcome, raw, raw, id)
		return
	}
	_, _ = st.db.ExecContext(ctx, `UPDATE network_egress_events SET outcome=?, reason=?, evidence=CASE WHEN ?='' THEN evidence ELSE ? END WHERE id=?`, outcome, reason, raw, raw, id)
}

// list is a group's events, newest first; a zero group lists every group.
func (st *egressStore) list(ctx context.Context, group, limit int) ([]EgressEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := []EgressEvent{}
	if st.db == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		for i := len(st.events) - 1; i >= 0 && len(out) < limit; i-- {
			if group == 0 || st.events[i].GroupID == group {
				out = append(out, st.events[i])
			}
		}
		return out, nil
	}
	query := `SELECT id,group_id,at,kind,action,member_id,outcome,reason,actor,before_members,after_members,evidence FROM network_egress_events`
	args := []any{}
	if group != 0 {
		query += ` WHERE group_id=?`
		args = append(args, group)
	}
	query += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := st.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EgressEvent
		var at int64
		var before, after, evidence string
		if err := rows.Scan(&e.ID, &e.GroupID, &at, &e.Kind, &e.Action, &e.MemberID, &e.Outcome, &e.Reason, &e.Actor, &before, &after, &evidence); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at).UTC()
		e.Before, e.After = splitIDs(before), splitIDs(after)
		if evidence != "" {
			var ev EgressEvidence
			if json.Unmarshal([]byte(evidence), &ev) == nil {
				e.Evidence = &ev
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// applying are switches whose outcome was never recorded: the process that
// made them stopped first.
func (st *egressStore) applying(ctx context.Context) []EgressEvent {
	return st.withOutcome(ctx, "applying")
}

// withOutcome are the events still in one outcome, oldest first.
func (st *egressStore) withOutcome(ctx context.Context, outcome string) []EgressEvent {
	var out []EgressEvent
	if st.db == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		for _, e := range st.events {
			if e.Outcome == outcome {
				out = append(out, e)
			}
		}
		return out
	}
	rows, err := st.db.QueryContext(ctx, `SELECT id,group_id,kind,before_members,after_members,evidence FROM network_egress_events WHERE outcome=? ORDER BY id`, outcome)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var e EgressEvent
		var before, after, evidence string
		if rows.Scan(&e.ID, &e.GroupID, &e.Kind, &before, &after, &evidence) == nil {
			e.Before, e.After = splitIDs(before), splitIDs(after)
			if evidence != "" {
				var ev EgressEvidence
				if json.Unmarshal([]byte(evidence), &ev) == nil {
					e.Evidence = &ev
				}
			}
			out = append(out, e)
		}
	}
	return out
}

// saveSamples persists one round of a group's samples.
func (st *egressStore) saveSamples(ctx context.Context, group int, samples map[int]EgressSample, states map[int]string) {
	if st.db == nil || len(samples) == 0 {
		return
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for member, s := range samples {
		detail, _ := json.Marshal(struct {
			Probes []EgressProbeResult `json:"probes"`
			Why    string              `json:"why,omitempty"`
		}{s.Probes, s.Why})
		good := 0
		if s.Good {
			good = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO network_egress_samples(group_id,member_id,at,ok,total,loss,latency_ms,good,state,detail) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			group, member, s.At.UnixMilli(), s.OK, s.Total, s.Loss, s.LatencyMillis, good, states[member], string(detail)); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

// recentSamples reads a member's persisted history, oldest first, so a
// restarted monitor keeps its window.
func (st *egressStore) recentSamples(ctx context.Context, group, member, limit int) []EgressSample {
	if st.db == nil {
		return nil
	}
	rows, err := st.db.QueryContext(ctx, `SELECT at,ok,total,loss,latency_ms,good,detail FROM network_egress_samples WHERE group_id=? AND member_id=? ORDER BY at DESC LIMIT ?`, group, member, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []EgressSample
	for rows.Next() {
		var s EgressSample
		var at int64
		var good int
		var detail string
		if rows.Scan(&at, &s.OK, &s.Total, &s.Loss, &s.LatencyMillis, &good, &detail) != nil {
			continue
		}
		s.At, s.Good = time.UnixMilli(at).UTC(), good == 1
		var d struct {
			Probes []EgressProbeResult `json:"probes"`
			Why    string              `json:"why"`
		}
		if json.Unmarshal([]byte(detail), &d) == nil {
			s.Probes, s.Why = d.Probes, d.Why
		}
		out = append(out, s)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// prune keeps the record bounded.
func (st *egressStore) prune(ctx context.Context, now time.Time) {
	if st.db == nil {
		return
	}
	_, _ = st.db.ExecContext(ctx, `DELETE FROM network_egress_samples WHERE at < ?`, now.Add(-egressSampleRetention).UnixMilli())
	_, _ = st.db.ExecContext(ctx, `DELETE FROM network_egress_events WHERE at < ? AND outcome != 'applying'`, now.Add(-egressEventRetention).UnixMilli())
	_, _ = st.db.ExecContext(ctx, `DELETE FROM network_egress_events WHERE id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY at DESC, id DESC) AS n FROM network_egress_events) WHERE n > ?)`, egressEventsPerGroup)
	_, _ = st.db.ExecContext(ctx, `DELETE FROM network_egress_simulations WHERE status != 'running' AND id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY started_at DESC) AS n FROM network_egress_simulations) WHERE n > ?)`, egressSimulationsKept)
}

// saveSimulation writes a simulation's current state.
func (st *egressStore) saveSimulation(ctx context.Context, sim EgressSimulation) {
	if st.db == nil {
		st.mu.Lock()
		st.simulations[sim.ID] = sim
		st.mu.Unlock()
		return
	}
	result, _ := json.Marshal(sim.Result)
	var finished int64
	if !sim.FinishedAt.IsZero() {
		finished = sim.FinishedAt.UnixMilli()
	}
	_, _ = st.db.ExecContext(ctx, `INSERT OR REPLACE INTO network_egress_simulations(id,group_id,fingerprint,status,actor,started_at,finished_at,result,error) VALUES(?,?,?,?,?,?,?,?,?)`,
		sim.ID, sim.GroupID, sim.Fingerprint, sim.Status, sim.Actor, sim.StartedAt.UnixMilli(), finished, string(result), sim.Error)
}

func (st *egressStore) simulation(ctx context.Context, id string) (EgressSimulation, bool) {
	if st.db == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		sim, ok := st.simulations[id]
		return sim, ok
	}
	sims := st.querySimulations(ctx, `WHERE id=?`, id)
	if len(sims) == 0 {
		return EgressSimulation{}, false
	}
	return sims[0], true
}

// simulations are a group's runs, newest first.
func (st *egressStore) simulationsFor(ctx context.Context, group int) []EgressSimulation {
	if st.db == nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		var out []EgressSimulation
		for _, sim := range st.simulations {
			if sim.GroupID == group {
				out = append(out, sim)
			}
		}
		sortSimulations(out)
		return out
	}
	return st.querySimulations(ctx, `WHERE group_id=? ORDER BY started_at DESC LIMIT ?`, group, egressSimulationsKept)
}

func (st *egressStore) querySimulations(ctx context.Context, where string, args ...any) []EgressSimulation {
	rows, err := st.db.QueryContext(ctx, `SELECT id,group_id,fingerprint,status,actor,started_at,finished_at,result,error FROM network_egress_simulations `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []EgressSimulation
	for rows.Next() {
		var sim EgressSimulation
		var started, finished int64
		var result string
		if rows.Scan(&sim.ID, &sim.GroupID, &sim.Fingerprint, &sim.Status, &sim.Actor, &started, &finished, &result, &sim.Error) != nil {
			continue
		}
		sim.StartedAt = time.UnixMilli(started).UTC()
		if finished > 0 {
			sim.FinishedAt = time.UnixMilli(finished).UTC()
		}
		if result != "" && result != "null" {
			var r EgressSimulationResult
			if json.Unmarshal([]byte(result), &r) == nil {
				sim.Result = &r
			}
		}
		out = append(out, sim)
	}
	return out
}

// interruptRunning marks simulations a stopped process left running.
func (st *egressStore) interruptRunning(ctx context.Context) {
	if st.db == nil {
		return
	}
	_, _ = st.db.ExecContext(ctx, `UPDATE network_egress_simulations SET status='interrupted', error='The backend stopped during this simulation; its namespaces were removed at the next start.', finished_at=? WHERE status='running'`, time.Now().UnixMilli())
}
