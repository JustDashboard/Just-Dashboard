package netx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Route history is observed, not reconstructed. Every routeHistoryEvery the
// observer reads the same inventory the Routing page does and records what
// appeared, disappeared or changed since the previous reading, bounded by the
// two reading times. A route that came and went between two readings leaves
// no trace, and the page says so. The local table is left out: it follows the
// host's addresses, which the Interfaces page reads.
const (
	routeHistoryEvery     = 30 * time.Second
	routeHistoryKeep      = 30 * 24 * time.Hour
	routeHistoryMaxEvents = 2000
	// A reading with more changes than this records them as one event, so
	// a renumbering or a flap cannot push every earlier change out.
	routeHistoryMaxPerReading = 100
	// Past this many routes the host carries a routing table rather than a
	// configuration, and rereading it every interval is a cost the observer
	// does not impose: it stops and says why.
	routeHistoryMaxRoutes = 20000
)

// daemonProtocols are the routes a routing daemon installs (iproute2's names
// and FRR's numbers): they follow its sessions, a full BGP table is a
// million of them, and the BGP page reads them from the daemon instead.
var daemonProtocols = map[string]bool{
	"bgp": true, "isis": true, "ospf": true, "rip": true, "ripng": true, "eigrp": true, "babel": true,
	"bird": true, "zebra": true, "openr": true, "11": true, "12": true, "42": true, "99": true,
	"186": true, "187": true, "188": true, "189": true, "190": true, "191": true, "192": true, "193": true, "197": true,
}

// RouteEvent is one change between two readings.
type RouteEvent struct {
	ID         int64     `json:"id"`
	ObservedAt time.Time `json:"observedAt"`
	// PreviousAt is the reading before, so the change happened in
	// (PreviousAt, ObservedAt].
	PreviousAt time.Time `json:"previousAt"`
	// AcrossRestart is a change found by the first reading after this
	// process started, against the last reading its predecessor kept.
	AcrossRestart bool   `json:"acrossRestart,omitempty"`
	Object        string `json:"object"`
	Change        string `json:"change"`
	Family        string `json:"family"`
	Table         int    `json:"table,omitempty"`
	TableName     string `json:"tableName,omitempty"`
	Destination   string `json:"destination,omitempty"`
	Owner         string `json:"owner"`
	Managed       bool   `json:"managed"`
	Before        string `json:"before,omitempty"`
	After         string `json:"after,omitempty"`
}

// RouteHistory is the observer's state and the matching events.
type RouteHistory struct {
	IntervalSeconds int          `json:"intervalSeconds"`
	Running         bool         `json:"running"`
	Since           *time.Time   `json:"since,omitempty"`
	LastReading     *time.Time   `json:"lastReading,omitempty"`
	LastError       string       `json:"lastError,omitempty"`
	Events          []RouteEvent `json:"events"`
	Limits          []string     `json:"limits"`
}

// RouteHistoryQuery narrows the events. Target keeps events whose
// destination covers that address or network.
type RouteHistoryQuery struct {
	Limit  int
	Family string
	Object string
	Target string
}

// historyItem is one route or rule in a reading, keyed by what the kernel
// identifies it by.
type historyItem struct {
	Object      string `json:"object"`
	Family      string `json:"family"`
	Table       int    `json:"table,omitempty"`
	TableName   string `json:"tableName,omitempty"`
	Destination string `json:"destination,omitempty"`
	Owner       string `json:"owner"`
	Managed     bool   `json:"managed"`
	Summary     string `json:"summary"`
}

type routeObserver struct {
	mu      sync.Mutex
	running bool
	since   time.Time
	last    time.Time
	lastErr string
	items   map[string]historyItem
}

// snapshotRouting turns an inventory into keyed items. A route's key is its
// kernel identity (family, table, destination, metric) and its summary is
// everything else worth noticing; an RA route's countdown is not part of it.
func snapshotRouting(view *RoutingView) map[string]historyItem {
	items := map[string]historyItem{}
	for _, t := range view.Tables {
		for _, r := range t.Routes {
			if daemonProtocols[r.Protocol] {
				continue
			}
			key := fmt.Sprintf("route|%s|%d|%s|%d", r.Family, t.ID, r.Destination, r.Metric)
			items[key] = historyItem{
				Object: "route", Family: r.Family, Table: t.ID, TableName: t.Name, Destination: r.Destination,
				Owner: r.Owner, Managed: r.Managed, Summary: routeSummary(r),
			}
		}
	}
	for _, r := range view.Rules {
		key := fmt.Sprintf("rule|%s|%d|%s", r.Family, r.Priority, ruleSelectorText(r))
		items[key] = historyItem{
			Object: "rule", Family: r.Family, Table: r.Table, TableName: r.TableName, Destination: r.To,
			Owner: r.Owner, Managed: r.Managed, Summary: fmt.Sprintf("%d %s %s", r.Priority, ruleSelectorText(r), ruleActionText(r)),
		}
	}
	return items
}

func routeSummary(r RouteEntry) string {
	parts := []string{r.Destination}
	if r.Type != "unicast" {
		parts = append([]string{r.Type}, parts...)
	}
	if r.Gateway != "" {
		parts = append(parts, "via", r.Gateway)
	}
	if r.Device != "" {
		parts = append(parts, "dev", r.Device)
	}
	for _, n := range r.Nexthops {
		leg := "nexthop"
		if n.Gateway != "" {
			leg += " via " + n.Gateway
		}
		if n.Device != "" {
			leg += " dev " + n.Device
		}
		if n.Weight > 1 {
			leg += " weight " + strconv.Itoa(n.Weight)
		}
		parts = append(parts, leg)
	}
	if r.Source != "" {
		parts = append(parts, "src", r.Source)
	}
	if r.Protocol != "" {
		parts = append(parts, "proto", r.Protocol)
	}
	if r.Metric != 0 {
		parts = append(parts, "metric", strconv.Itoa(r.Metric))
	}
	for _, f := range r.Flags {
		if f == "linkdown" || f == "dead" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, " ")
}

// ruleSelectorText is a rule's selectors as ip rule writes them.
func ruleSelectorText(r RuleEntry) string {
	var parts []string
	if r.Not {
		parts = append(parts, "not")
	}
	for _, f := range [][2]string{{"from", r.From}, {"to", r.To}, {"iif", r.IIF}, {"oif", r.OIF}, {"fwmark", r.FWMark},
		{"uidrange", r.UIDRange}, {"tos", r.TOS}, {"ipproto", r.IPProto}, {"sport", r.SPort}, {"dport", r.DPort}} {
		if f[1] != "" {
			parts = append(parts, f[0], f[1])
		}
	}
	if r.L3MDev {
		parts = append(parts, "l3mdev")
	}
	if len(parts) == 0 {
		return "all"
	}
	return strings.Join(parts, " ")
}

func ruleActionText(r RuleEntry) string {
	switch r.Action {
	case "lookup":
		if r.L3MDev {
			return "lookup vrf"
		}
		out := "lookup " + tableLabel(r.Table, r.TableName)
		if r.SuppressPrefixLength != nil {
			out += fmt.Sprintf(" suppress_prefixlength %d", *r.SuppressPrefixLength)
		}
		return out
	case "goto":
		return "goto " + strconv.Itoa(r.Goto)
	}
	return r.Action
}

// diffRouting lists what changed between two readings, in a stable order.
func diffRouting(before, after map[string]historyItem, previous, now time.Time, acrossRestart bool) []RouteEvent {
	var out []RouteEvent
	event := func(item historyItem, change, was, is string) {
		out = append(out, RouteEvent{
			ObservedAt: now, PreviousAt: previous, AcrossRestart: acrossRestart,
			Object: item.Object, Change: change, Family: item.Family, Table: item.Table, TableName: item.TableName,
			Destination: item.Destination, Owner: item.Owner, Managed: item.Managed, Before: was, After: is,
		})
	}
	keys := make([]string, 0, len(before)+len(after))
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		was, had := before[k]
		is, has := after[k]
		switch {
		case had && !has:
			event(was, "removed", was.Summary, "")
		case !had && has:
			event(is, "added", "", is.Summary)
		case was.Summary != is.Summary || was.Owner != is.Owner || was.Managed != is.Managed:
			event(is, "changed", was.Summary, is.Summary)
		}
	}
	return out
}

// StartRouteHistory begins the observer. Without a database there is nowhere
// to keep the history, and it does not start.
func (s *Service) StartRouteHistory(ctx context.Context) {
	if s.db == nil {
		return
	}
	go func() {
		if !s.observeRoutes(ctx) {
			return
		}
		ticker := time.NewTicker(routeHistoryEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.history.mu.Lock()
				s.history.running = false
				s.history.mu.Unlock()
				return
			case <-ticker.C:
				if !s.observeRoutes(ctx) {
					return
				}
			}
		}
	}()
}

// observeRoutes takes one reading and records its changes, and says whether
// to keep observing. The first reading of a process compares with the
// snapshot its predecessor kept.
func (s *Service) observeRoutes(ctx context.Context) bool {
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	view, err := s.readRouting(readCtx)
	now := time.Now().UTC()
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	if err != nil {
		s.history.lastErr = err.Error()
		return true
	}
	routes := 0
	for _, t := range view.Tables {
		routes += len(t.Routes)
	}
	if routes > routeHistoryMaxRoutes {
		s.history.running, s.history.items = false, nil
		s.history.lastErr = fmt.Sprintf("This host carries %d routes, a routing table rather than a configuration; the observer stopped instead of rereading it every %d seconds.", routes, int(routeHistoryEvery/time.Second))
		return false
	}
	items := snapshotRouting(view)
	if err := s.recordRouteReading(ctx, items, now); err != nil {
		s.history.lastErr = err.Error()
		return true
	}
	if s.history.since.IsZero() {
		s.history.since = now
	}
	s.history.running, s.history.last, s.history.lastErr, s.history.items = true, now, "", items
	return true
}

// recordRouteReading diffs a reading against the previous one (in memory, or
// the stored snapshot after a restart), inserts the events and replaces the
// snapshot, in one transaction. The caller holds history.mu.
func (s *Service) recordRouteReading(ctx context.Context, items map[string]historyItem, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, previous, acrossRestart := s.history.items, s.history.last, false
	if before == nil {
		var at int64
		var raw string
		switch err := tx.QueryRowContext(ctx, `SELECT observed_at,snapshot_json FROM network_route_snapshot WHERE id=1`).Scan(&at, &raw); err {
		case nil:
			if json.Unmarshal([]byte(raw), &before) != nil {
				before = nil
			}
			previous, acrossRestart = time.UnixMilli(at).UTC(), true
		case sql.ErrNoRows:
		default:
			return err
		}
	}
	if before != nil {
		events := diffRouting(before, items, previous, now, acrossRestart)
		if len(events) > routeHistoryMaxPerReading {
			events = []RouteEvent{{ObservedAt: now, PreviousAt: previous, AcrossRestart: acrossRestart, Object: "reading", Change: "changed",
				Owner: "system", After: fmt.Sprintf("%d routes and rules changed at once; too many to record one by one.", len(events))}}
		}
		for _, e := range events {
			if _, err := tx.ExecContext(ctx, `INSERT INTO network_route_events(observed_at,previous_at,across_restart,object,change,family,table_id,table_name,destination,owner,managed,before_summary,after_summary) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				e.ObservedAt.UnixMilli(), e.PreviousAt.UnixMilli(), e.AcrossRestart, e.Object, e.Change, e.Family, e.Table, e.TableName, e.Destination, e.Owner, e.Managed, e.Before, e.After); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM network_route_events WHERE observed_at<? OR id IN (SELECT id FROM network_route_events ORDER BY observed_at DESC,id DESC LIMIT -1 OFFSET ?)`,
			now.Add(-routeHistoryKeep).UnixMilli(), routeHistoryMaxEvents); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO network_route_snapshot(id,observed_at,snapshot_json) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET observed_at=excluded.observed_at,snapshot_json=excluded.snapshot_json`, now.UnixMilli(), string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

var routeHistoryLimits = []string{
	"Readings are taken every 30 seconds; a route that appeared and disappeared between two readings is not recorded.",
	"A change is dated by the reading that found it: it happened after the previous reading and no later than this one.",
	"The local table, which follows this host's own addresses, is not recorded here.",
	"Routes a routing daemon installs (BGP, OSPF, IS-IS, RIP, Babel) follow its sessions and are not recorded; the BGP page reads them.",
	"A reading that finds more than 100 changes records them as one event.",
}

// RouteHistory returns the newest events that match the query.
func (s *Service) RouteHistory(ctx context.Context, q RouteHistoryQuery) (*RouteHistory, error) {
	if s.db == nil {
		return nil, ErrUnavailable
	}
	var target netip.Prefix
	if q.Target != "" {
		p, err := ParsePrefix(q.Target)
		if err != nil {
			return nil, err
		}
		target = p.Masked()
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	query := `SELECT id,observed_at,previous_at,across_restart,object,change,family,table_id,table_name,destination,owner,managed,before_summary,after_summary FROM network_route_events WHERE 1=1`
	var args []any
	if q.Family != "" {
		query += ` AND family=?`
		args = append(args, q.Family)
	}
	if q.Object != "" {
		query += ` AND object=?`
		args = append(args, q.Object)
	}
	query += ` ORDER BY observed_at DESC,id DESC`
	if !target.IsValid() {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &RouteHistory{IntervalSeconds: int(routeHistoryEvery / time.Second), Events: []RouteEvent{}, Limits: routeHistoryLimits}
	for rows.Next() && len(out.Events) < limit {
		var e RouteEvent
		var observed, previous int64
		if err := rows.Scan(&e.ID, &observed, &previous, &e.AcrossRestart, &e.Object, &e.Change, &e.Family, &e.Table, &e.TableName, &e.Destination, &e.Owner, &e.Managed, &e.Before, &e.After); err != nil {
			return nil, err
		}
		e.ObservedAt, e.PreviousAt = time.UnixMilli(observed).UTC(), time.UnixMilli(previous).UTC()
		if target.IsValid() && !destinationCovers(e.Family, e.Destination, target) {
			continue
		}
		out.Events = append(out.Events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.history.mu.Lock()
	out.Running, out.LastError = s.history.running, s.history.lastErr
	if !s.history.since.IsZero() {
		since, last := s.history.since, s.history.last
		out.Since, out.LastReading = &since, &last
	}
	s.history.mu.Unlock()
	return out, nil
}

// destinationCovers reports whether a route or rule destination holds the
// target address or network; a rule with no destination selects every one.
func destinationCovers(family, destination string, target netip.Prefix) bool {
	if familyOf(target.Addr()) != family {
		return false
	}
	if destination == "" || destination == "default" {
		return true
	}
	p, err := ParsePrefix(destination)
	if err != nil {
		return false
	}
	p = p.Masked()
	return p.Bits() <= target.Bits() && p.Contains(target.Addr())
}
