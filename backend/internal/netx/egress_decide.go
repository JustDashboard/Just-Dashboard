package netx

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The monitor's judgement, kept pure so the live monitor, a manual switch's
// advice and the namespace simulation all decide with exactly this code.

// egressHistoryLimit bounds the samples kept in memory per member.
const egressHistoryLimit = 120

// egressMemberState is the monitor's view of one member.
type egressMemberState struct {
	// State is unknown (not enough evidence either way), up or down.
	State string
	// Since is when State began; zero while unknown.
	Since   time.Time
	Good    int
	Bad     int
	History []EgressSample
	// key is the member's configuration; a changed member starts again.
	key string
}

func newEgressMemberState(key string) *egressMemberState {
	return &egressMemberState{State: "unknown", key: key}
}

// observe judges a sample against the window before it and applies the
// hysteresis rules. It returns the new state when the sample changed it.
func (st *egressMemberState) observe(s EgressSample, t EgressThresholds) (string, EgressSample) {
	judgeSample(&s, st.History, t)
	st.History = append(st.History, s)
	if len(st.History) > egressHistoryLimit {
		st.History = st.History[len(st.History)-egressHistoryLimit:]
	}
	if s.Good {
		st.Good, st.Bad = st.Good+1, 0
		if st.State != "up" && st.Good >= t.RecoverAfter {
			st.State, st.Since = "up", s.At
			return "up", s
		}
		return "", s
	}
	st.Bad, st.Good = st.Bad+1, 0
	if st.State != "down" && st.Bad >= t.FailAfter {
		st.State, st.Since = "down", s.At
		return "down", s
	}
	return "", s
}

func (st *egressMemberState) last() *EgressSample {
	if len(st.History) == 0 {
		return nil
	}
	return &st.History[len(st.History)-1]
}

// egressMemberView is what the decision reads of a member.
type egressMemberView struct {
	State string
	Since time.Time
	// Fresh is evidence recent enough to act on: the last sample is no older
	// than three intervals.
	Fresh bool
	Why   string
}

func (v egressMemberView) proven() bool { return v.State == "up" && v.Fresh }

// EgressDecision is what the rules say should happen now.
type EgressDecision struct {
	// Action is none, failover, failback, rebalance or hold.
	Action string `json:"action"`
	Target []int  `json:"target,omitempty"`
	Reason string `json:"reason"`
	// key identifies a held situation so it is recorded once.
	key string
}

func (d EgressDecision) switches() bool {
	return d.Action == "failover" || d.Action == "failback" || d.Action == "rebalance"
}

// decideEgress applies the group's rules to its members' states.
//
// The best tier with a proven member carries traffic, shared by every proven
// member of that tier. Moving away from a member that is down is a failover
// and happens as soon as the member is declared down: the hold time never
// keeps traffic on a dead path. Moving to a better tier (or adding a
// recovered member back into a tier) is a failback: it waits for the
// configured stable recovery and the hold time since the last switch, and a
// manual group only says it is available. With no proven member the group
// keeps the route it has: withdrawing it would strand the traffic anyway.
func decideEgress(g EgressGroupSpec, views map[int]egressMemberView, now time.Time) EgressDecision {
	active := append([]int{}, g.Active...)
	sort.Ints(active)
	byTier := map[int][]int{}
	var tiers []int
	for _, m := range g.Members {
		if views[m.ID].proven() {
			if _, ok := byTier[m.Priority]; !ok {
				tiers = append(tiers, m.Priority)
			}
			byTier[m.Priority] = append(byTier[m.Priority], m.ID)
		}
	}
	sort.Ints(tiers)
	var down []string
	for _, id := range active {
		switch {
		case g.member(id) == nil:
			down = append(down, "member "+strconv.Itoa(id)+" no longer exists")
		case views[id].State == "down":
			down = append(down, egressMemberName(g, id)+" is down"+egressWhy(views[id].Why))
		}
	}
	if len(tiers) == 0 {
		if len(down) > 0 || len(active) == 0 {
			return EgressDecision{Action: "hold", key: "none-proven", Reason: "No member is proven healthy; the group keeps the route it has, since withdrawing it would strand the traffic anyway."}
		}
		return EgressDecision{Action: "none", Reason: "No other member is proven healthy."}
	}
	desired := byTier[tiers[0]]
	sort.Ints(desired)
	if equalIDs(desired, active) {
		return EgressDecision{Action: "none", Reason: "The decided members are the best proven members."}
	}
	activeTier := g.tierOf(active)
	desiredTier := tiers[0]
	names := egressNames(g, desired)
	if len(down) > 0 || len(active) == 0 {
		action := "failover"
		if desiredTier == activeTier {
			action = "rebalance"
		}
		reason := strings.Join(down, "; ")
		if reason == "" {
			reason = "The group had no decided member"
		}
		return EgressDecision{Action: action, Target: desired, Reason: reason + "; " + names + " " + isAre(desired) + " the best proven " + memberWord(desired) + "."}
	}
	better := desiredTier < activeTier || desiredTier == activeTier && containsAll(desired, active)
	if !better {
		// The decided members are not down, only unproven for now: the
		// rules do not leave a member on a lack of evidence.
		return EgressDecision{Action: "none", Reason: "The decided members are not down; the group stays on them until they are."}
	}
	action := "failback"
	if desiredTier == activeTier {
		action = "rebalance"
	}
	if g.Failback == "manual" {
		return EgressDecision{Action: "hold", Target: desired, key: "manual:" + idKey(desired), Reason: "Failback to " + names + " is available; this group fails back by hand."}
	}
	var stable time.Duration = -1
	for _, id := range desired {
		if slices.Contains(active, id) {
			continue
		}
		up := now.Sub(views[id].Since)
		if stable < 0 || up < stable {
			stable = up
		}
	}
	need := time.Duration(g.Thresholds.StableSeconds) * time.Second
	if stable >= 0 && stable < need {
		return EgressDecision{Action: "hold", Target: desired, key: "stable:" + idKey(desired), Reason: fmt.Sprintf("%s has been up for %s of the %s stable recovery before failback.", names, roundDuration(stable), roundDuration(need))}
	}
	hold := time.Duration(g.Thresholds.HoldSeconds) * time.Second
	if !g.DecidedAt.IsZero() && now.Sub(g.DecidedAt) < hold {
		return EgressDecision{Action: "hold", Target: desired, key: "hold:" + idKey(desired), Reason: fmt.Sprintf("The last switch was %s ago; failback to %s waits for the %s hold time.", roundDuration(now.Sub(g.DecidedAt)), names, roundDuration(hold))}
	}
	return EgressDecision{Action: action, Target: desired, Reason: names + " " + isAre(desired) + " back and stable for " + roundDuration(stable) + "; failing back."}
}

func egressWhy(why string) string {
	if why == "" {
		return ""
	}
	return " (" + why + ")"
}

func roundDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return (d.Round(time.Second)).String()
}

func egressMemberName(g EgressGroupSpec, id int) string {
	if m := g.member(id); m != nil {
		return m.Name
	}
	return "member " + strconv.Itoa(id)
}

func egressNames(g EgressGroupSpec, ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = egressMemberName(g, id)
	}
	return strings.Join(parts, " and ")
}

func isAre(ids []int) string {
	if len(ids) == 1 {
		return "is"
	}
	return "are"
}

func memberWord(ids []int) string {
	if len(ids) == 1 {
		return "member"
	}
	return "members"
}

func equalIDs(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]int{}, a...), append([]int{}, b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func containsAll(set, sub []int) bool {
	for _, id := range sub {
		if !slices.Contains(set, id) {
			return false
		}
	}
	return true
}

func idKey(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}
