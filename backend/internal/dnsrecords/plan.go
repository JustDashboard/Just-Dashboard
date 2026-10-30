package dnsrecords

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Action is what a write would do to one record set.
type Action string

const (
	// ActionCreate writes a set the name does not have.
	ActionCreate Action = "create"
	// ActionUpdate changes the values of a set the name has.
	ActionUpdate Action = "update"
	// ActionKeep is a set already as wanted.
	ActionKeep Action = "keep"
	// ActionConflict is a wanted set this code cannot write without
	// deleting a record: a CNAME where an address is wanted, addresses
	// where a CNAME is, a set it would have to shrink, a set it cannot
	// replace. Nothing is written while one stands.
	ActionConflict Action = "conflict"
	// ActionStale is a set nobody wants that sends visitors elsewhere — an
	// AAAA on a server with no public IPv6 — which is said and left alone.
	ActionStale Action = "stale"
)

// Change is one set's line of a plan: what the name holds of a type, what
// it should, and what a write does about it.
type Change struct {
	Type    Type     `json:"type"`
	Name    string   `json:"name"`
	Action  Action   `json:"action"`
	Current []string `json:"current"`
	Want    []string `json:"want"`
	// CurrentTTL is the set's TTL now, TTL what a write sends.
	CurrentTTL int  `json:"currentTtl,omitempty"`
	TTL        int  `json:"ttl,omitempty"`
	Proxied    bool `json:"proxied,omitempty"`
	// Detail is the line in words.
	Detail string `json:"detail"`

	current *Set
	want    Set
}

// Diff compares what a name should hold with what it holds. want is one
// set per type, current what Read answered. perRecord is the provider's
// PerRecord: shrinking a set there means deleting a record.
func Diff(want []Set, current []Set, perRecord bool) []Change {
	held := map[Type]*Set{}
	for index := range current {
		set := current[index]
		if existing, ok := held[set.Type]; ok {
			// Two answers of one type are one set.
			existing.Values = append(existing.Values, set.Values...)
			existing.ids = append(existing.ids, set.ids...)
			continue
		}
		held[set.Type] = &set
	}
	wanted := map[Type]bool{}
	changes := []Change{}
	for _, w := range want {
		wanted[w.Type] = true
		change := Change{Type: w.Type, Name: Canonical(w.Name), Want: append([]string(nil), w.Values...), TTL: w.TTL, Proxied: w.Proxied, want: w}
		cur := held[w.Type]
		if cur != nil {
			change.Current, change.CurrentTTL, change.current = append([]string(nil), cur.Values...), cur.TTL, cur
		} else {
			change.Current = []string{}
		}
		cname := held[TypeCNAME]
		switch {
		case w.Type != TypeCNAME && cname != nil:
			change.Action = ActionConflict
			change.Detail = fmt.Sprintf("%s has a CNAME to %s, and a name with a CNAME holds no other record. Nothing here deletes a record: remove the CNAME where the zone is kept, then create the %s record.",
				change.Name, strings.Join(cname.Values, ", "), w.Type)
		case w.Type == TypeCNAME && (held[TypeA] != nil || held[TypeAAAA] != nil):
			change.Action = ActionConflict
			change.Detail = fmt.Sprintf("%s has %s, and a CNAME cannot sit beside another record. Nothing here deletes a record: remove %s where the zone is kept, then create the CNAME.",
				change.Name, describeAddresses(held), pronounFor(held))
		case cur == nil:
			change.Action = ActionCreate
			change.Detail = fmt.Sprintf("Create %s.", Describe(w))
		case cur.Unwritable != "":
			change.Action = ActionConflict
			change.Detail = fmt.Sprintf("%s %s cannot be replaced from here: %s.", w.Type, change.Name, cur.Unwritable)
		case SameValues(w.Type, cur.Values, w.Values) && (w.Type != TypeCNAME || cur.Proxied == w.Proxied):
			change.Action = ActionKeep
			change.Detail = fmt.Sprintf("%s is already right.", Describe(Set{Name: w.Name, Type: w.Type, Values: cur.Values, TTL: cur.TTL, Proxied: cur.Proxied}))
		case perRecord && len(cur.Values) > len(w.Values):
			change.Action = ActionConflict
			change.Detail = fmt.Sprintf("%s has %d %s records (%s), and this provider keeps one record per value: writing %d would mean deleting the rest, which nothing here does. Remove the others where the zone is kept.",
				change.Name, len(cur.Values), w.Type, strings.Join(cur.Values, ", "), len(w.Values))
		case SameValues(w.Type, cur.Values, w.Values):
			change.Action = ActionUpdate
			change.Detail = fmt.Sprintf("Put %s behind Cloudflare's edge.", Describe(w))
		default:
			change.Action = ActionUpdate
			change.Detail = fmt.Sprintf("Change %s %s from %s to %s.", w.Type, change.Name, strings.Join(cur.Values, ", "), strings.Join(w.Values, ", "))
		}
		changes = append(changes, change)
	}
	// An address of the other family that is not this server's: the name's
	// visitors over it go somewhere else, and only the operator may remove it.
	for _, kind := range []Type{TypeA, TypeAAAA} {
		cur := held[kind]
		if cur == nil || wanted[kind] || wanted[TypeCNAME] || len(want) == 0 {
			continue
		}
		family := "IPv4"
		if kind == TypeAAAA {
			family = "IPv6"
		}
		changes = append(changes, Change{
			Type: kind, Name: Canonical(cur.Name), Action: ActionStale, Current: append([]string(nil), cur.Values...), Want: []string{},
			CurrentTTL: cur.TTL, current: cur,
			Detail: fmt.Sprintf("%s also has %s %s, and this server has no public %s address: visitors over %s are sent there. Nothing here deletes a record; remove it where the zone is kept if it is not meant to be.",
				Canonical(cur.Name), kind, strings.Join(cur.Values, ", "), family, family),
		})
	}
	sort.SliceStable(changes, func(i, j int) bool { return typeOrder(changes[i].Type) < typeOrder(changes[j].Type) })
	return changes
}

func typeOrder(kind Type) int {
	for index, known := range Types {
		if known == kind {
			return index
		}
	}
	return len(Types)
}

func describeAddresses(held map[Type]*Set) string {
	parts := []string{}
	for _, kind := range []Type{TypeA, TypeAAAA} {
		if set := held[kind]; set != nil {
			parts = append(parts, string(kind)+" "+strings.Join(set.Values, ", "))
		}
	}
	return strings.Join(parts, " and ")
}

func pronounFor(held map[Type]*Set) string {
	count := 0
	for _, kind := range []Type{TypeA, TypeAAAA} {
		if set := held[kind]; set != nil {
			count += len(set.Values)
		}
	}
	if count == 1 {
		return "it"
	}
	return "them"
}

// Describe is a set as one line: "A app.example.com → 203.0.113.7".
func Describe(set Set) string {
	line := fmt.Sprintf("%s %s → %s", set.Type, Canonical(set.Name), strings.Join(set.Values, ", "))
	if set.Proxied {
		line += " (proxied)"
	}
	return line
}

// Blocked is the first change that keeps a plan from being written, if any.
func Blocked(changes []Change) (Change, bool) {
	for _, change := range changes {
		if change.Action == ActionConflict {
			return change, true
		}
	}
	return Change{}, false
}

// Pending are the changes a write makes.
func Pending(changes []Change) []Change {
	out := []Change{}
	for _, change := range changes {
		if change.Action == ActionCreate || change.Action == ActionUpdate {
			out = append(out, change)
		}
	}
	return out
}

// Apply writes a plan's pending changes, in order, and answers the ones it
// wrote. It writes nothing while a change is blocked, and stops at the
// provider's first refusal: what was written before it is in the answer.
func Apply(ctx context.Context, provider Provider, zone Zone, changes []Change) ([]Change, error) {
	if blocked, ok := Blocked(changes); ok {
		return nil, fmt.Errorf("%s", blocked.Detail)
	}
	written := []Change{}
	for _, change := range Pending(changes) {
		var current *Set
		if change.Action == ActionUpdate {
			current = change.current
		}
		if err := provider.Write(ctx, zone, current, change.want); err != nil {
			return written, err
		}
		written = append(written, change)
	}
	return written, nil
}
