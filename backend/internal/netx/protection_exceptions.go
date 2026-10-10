package netx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Exceptions and the notes behind kept trusted addresses.
//
// Both let a source past drops the operator otherwise wants, so each carries
// who made it and why, and may carry an expiry. The expiry is written into the
// rule as an absolute `meta time` match: the kernel stops honouring the rule at
// that instant whether or not the dashboard runs, and the boot file stays a
// pure function of the spec. The dashboard's sweeper only tidies an expired
// entry out of the spec afterwards.

const (
	maxExceptions = 256
	// A kept address nobody has used or confirmed for this long is reported
	// stale, so accumulated operator addresses are reviewed rather than kept
	// forever by default.
	trustedStaleAfter = 30 * 24 * time.Hour
	// operatorActivityWindow bounds how far back sign-in evidence is read.
	operatorActivityWindow = 90 * 24 * time.Hour
)

// gatewayNow is the clock the views and the sweeper read; the renderer never
// does, because expiry lives in the rule.
var gatewayNow = time.Now

// trustedNote returns the note kept for an address, if any.
func trustedNote(sp *Spec, address string) (TrustedNote, int) {
	for i, n := range sp.TrustedNotes {
		if n.Address == address {
			return n, i
		}
	}
	return TrustedNote{}, -1
}

// expiringTrusted are kept addresses whose note ends them. They are rendered
// as rules of their own, not as members of the permanent trusted sets.
func expiringTrusted(sp *Spec) map[string]time.Time {
	out := map[string]time.Time{}
	for _, raw := range sp.Trusted {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		key := p.Masked().String()
		if n, i := trustedNote(sp, key); i >= 0 && !n.ExpiresAt.IsZero() {
			out[key] = n.ExpiresAt
		}
	}
	return out
}

// gwUntil is the kernel-enforced end of a rule, empty for none.
func gwUntil(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf(" meta time < %d", t.Unix())
}

// allowRules let expiring trusted addresses and "all"-scope exceptions past
// every drop and limit, one rule each so each can expire on its own.
func allowRules(sp *Spec) []string {
	var out []string
	expiring := expiringTrusted(sp)
	keys := make([]string, 0, len(expiring))
	for k := range expiring {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, _ := ParsePrefix(k)
		out = append(out, fmt.Sprintf("%s %s%s return comment \"trusted:%s\"", gwSaddr(p.Addr()), gwPrefixWord(p), gwUntil(expiring[k]), p))
	}
	return append(out, exceptionRules(sp, "all")...)
}

// exceptionRules are the returns of one scope's exceptions, counted so the
// page can say how much each one let through.
func exceptionRules(sp *Spec, scope string) []string {
	var out []string
	for _, e := range sp.Exceptions {
		if e.Scope != scope {
			continue
		}
		p, err := ParsePrefix(e.Address)
		if err != nil {
			continue
		}
		p = p.Masked()
		out = append(out, fmt.Sprintf("%s %s%s counter return comment \"exception:%d\"", gwSaddr(p.Addr()), gwPrefixWord(p), gwUntil(e.ExpiresAt), e.ID))
	}
	return out
}

// checkExceptions validates what is rendered of exceptions and notes.
func checkExceptions(sp *Spec) error {
	if len(sp.Exceptions) > maxExceptions {
		return fmt.Errorf("at most %d exceptions are kept", maxExceptions)
	}
	lists := map[string]bool{}
	for _, bl := range sp.Blocklists {
		lists["blocklist:"+strconv.Itoa(bl.ID)] = true
	}
	for _, e := range sp.Exceptions {
		p, err := ParsePrefix(e.Address)
		if err != nil {
			return fmt.Errorf("exception %d: %w", e.ID, err)
		}
		if err := exceptionWidth(p); err != nil {
			return fmt.Errorf("exception %d: %w", e.ID, err)
		}
		if e.Scope != "all" && !lists[e.Scope] {
			return fmt.Errorf("exception %d: scope %q names no blocklist", e.ID, e.Scope)
		}
		if _, err := CleanLabel(e.Reason, 160); err != nil {
			return fmt.Errorf("exception %d: the reason: %w", e.ID, err)
		}
	}
	for _, n := range sp.TrustedNotes {
		if _, err := ParsePrefix(n.Address); err != nil {
			return fmt.Errorf("trusted note: %w", err)
		}
	}
	return nil
}

// exceptionWidth refuses an exception wider than the allowlist ranges the
// dashboard trusts: a /4 "exception" is the list switched off.
func exceptionWidth(p netip.Prefix) error {
	if (p.Addr().Is4() && p.Bits() < 8) || (p.Addr().Is6() && p.Bits() < 16) {
		return fmt.Errorf("%s is wider than a /8 (a /16 in IPv6); switch the list off instead", p.Masked())
	}
	return nil
}

// ExceptionRequest is the body of an exception's create.
type ExceptionRequest struct {
	Address string `json:"address"`
	// Scope is "all" or "blocklist:<id>".
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
	// ExpiresAt is an RFC 3339 instant; empty keeps the exception until it
	// is removed.
	ExpiresAt string `json:"expiresAt"`
}

// ExceptionView is an exception with what it let through and whether it still applies.
type ExceptionView struct {
	ID        int        `json:"id"`
	Address   string     `json:"address"`
	Scope     string     `json:"scope"`
	ScopeName string     `json:"scopeName"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Expired   bool       `json:"expired"`
	Made
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

func exceptionViews(sp *Spec, counters map[string]RuleCounter) []ExceptionView {
	names := map[string]string{"all": "every blocklist and limit"}
	for _, bl := range sp.Blocklists {
		names["blocklist:"+strconv.Itoa(bl.ID)] = bl.Name
	}
	now := gatewayNow()
	out := make([]ExceptionView, 0, len(sp.Exceptions))
	for _, e := range sp.Exceptions {
		c := counters["exception:"+strconv.Itoa(e.ID)]
		v := ExceptionView{ID: e.ID, Address: e.Address, Scope: e.Scope, ScopeName: names[e.Scope], Reason: e.Reason, Made: e.Made, Packets: c.Packets, Bytes: c.Bytes}
		if !e.ExpiresAt.IsZero() {
			t := e.ExpiresAt
			v.ExpiresAt, v.Expired = &t, !now.Before(t)
		}
		out = append(out, v)
	}
	return out
}

// parseExpiry reads an optional future instant.
func parseExpiry(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("the expiry %q is not an RFC 3339 time", raw)
	}
	t = t.UTC().Truncate(time.Second)
	if !t.After(gatewayNow()) {
		return time.Time{}, errors.New("the expiry is already past")
	}
	if t.After(gatewayNow().Add(366 * 24 * time.Hour)) {
		return time.Time{}, errors.New("an expiry is at most a year away; leave it empty to keep the entry until it is removed")
	}
	return t, nil
}

// AddException lets a network past one list, or every list and limit.
func (s *Service) AddException(ctx context.Context, req ExceptionRequest, actor string) (ExceptionView, error) {
	p, err := ParsePrefix(req.Address)
	if err != nil {
		return ExceptionView{}, err
	}
	p = p.Masked()
	if err := exceptionWidth(p); err != nil {
		return ExceptionView{}, err
	}
	reason, err := CleanLabel(req.Reason, 160)
	if err != nil {
		return ExceptionView{}, fmt.Errorf("say why the exception is needed: %w", err)
	}
	expires, err := parseExpiry(req.ExpiresAt)
	if err != nil {
		return ExceptionView{}, err
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "all"
	}
	var saved ExceptionSpec
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		if scope != "all" {
			id, ok := strings.CutPrefix(scope, "blocklist:")
			n, convErr := strconv.Atoi(id)
			found := false
			for _, bl := range next.Blocklists {
				found = found || (ok && convErr == nil && bl.ID == n)
			}
			if !found {
				return false, fmt.Errorf("the scope is \"all\" or \"blocklist:<id>\" of an existing list")
			}
		}
		if len(next.Exceptions) >= maxExceptions {
			return false, fmt.Errorf("at most %d exceptions are kept; remove one first", maxExceptions)
		}
		for _, e := range next.Exceptions {
			if e.Scope == scope && e.Address == p.String() {
				return false, fmt.Errorf("%w: %s is already excepted there", ErrExists, p)
			}
		}
		saved = ExceptionSpec{ID: next.takeID(), Address: p.String(), Scope: scope, Reason: reason, ExpiresAt: expires, Made: gwStamp(actor)}
		next.Exceptions = append(next.Exceptions, saved)
		return false, nil
	})
	if err != nil {
		return ExceptionView{}, err
	}
	sp, err := s.loadSpec()
	if err != nil {
		return ExceptionView{}, err
	}
	for _, v := range exceptionViews(sp, nil) {
		if v.ID == saved.ID {
			return v, nil
		}
	}
	return ExceptionView{}, fmt.Errorf("exception %d: %w", saved.ID, ErrNotFound)
}

// DeleteException removes an exception: the drops it held back apply again
// to new connections. The requester cannot remove the last thing letting
// their own address through a list that holds it.
func (s *Service) DeleteException(ctx context.Context, id int, client string) error {
	return s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i, e := range next.Exceptions {
			if e.ID != id {
				continue
			}
			next.Exceptions = append(next.Exceptions[:i], next.Exceptions[i+1:]...)
			if addr, err := ParseAddr(client); err == nil && !s.isTrusted(next, addr) && s.exceptionHoldsBack(next, e, addr) {
				return false, guarded("exception %d is what lets your own address (%s) through a list that holds it, and nothing permanent trusts it. Keep the exception, or trust your address first.", id, addr)
			}
			return false, nil
		}
		return false, fmt.Errorf("exception %d: %w", id, ErrNotFound)
	})
}

// exceptionHoldsBack reports whether an exception covers an address that one
// of the lists in its scope would otherwise drop.
func (s *Service) exceptionHoldsBack(sp *Spec, e ExceptionSpec, addr netip.Addr) bool {
	p, err := ParsePrefix(e.Address)
	if err != nil || !p.Contains(addr) {
		return false
	}
	for _, bl := range sp.Blocklists {
		if !bl.Enabled || (e.Scope != "all" && e.Scope != "blocklist:"+strconv.Itoa(bl.ID)) {
			continue
		}
		nets, _ := blocklistData(filepath.Join(s.paths.Dir, "lists"), bl)
		for _, n := range nets {
			if n.Contains(addr) {
				return true
			}
		}
	}
	return false
}

// TrustedRequest edits the note behind a kept trusted address.
type TrustedRequest struct {
	Address string `json:"address"`
	Reason  string `json:"reason"`
	// ExpiresAt is RFC 3339, or empty to keep the address until removed.
	ExpiresAt string `json:"expiresAt"`
	// Confirm records that the operator reviewed the address and still
	// needs it.
	Confirm bool `json:"confirm"`
}

// UpdateTrusted sets the reason and expiry of a kept trusted address, and
// records a review. An expiry on the requester's only coverage is refused:
// at that instant a list or limit could refuse them.
func (s *Service) UpdateTrusted(ctx context.Context, req TrustedRequest, client, actor string) error {
	want, err := ParsePrefix(req.Address)
	if err != nil {
		return err
	}
	want = want.Masked()
	reason := strings.TrimSpace(req.Reason)
	if reason != "" {
		if reason, err = CleanLabel(reason, 160); err != nil {
			return fmt.Errorf("the reason: %w", err)
		}
	}
	expires, err := parseExpiry(req.ExpiresAt)
	if err != nil {
		return err
	}
	return s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		kept := false
		for _, raw := range next.Trusted {
			if p, perr := ParsePrefix(raw); perr == nil && p.Masked() == want {
				kept = true
			}
		}
		if !kept {
			return false, fmt.Errorf("%s is not an address the dashboard keeps: %w", want, ErrNotFound)
		}
		note, i := trustedNote(next, want.String())
		if i < 0 {
			note = TrustedNote{Address: want.String()}
		}
		note.Reason, note.ExpiresAt = reason, expires
		if req.Confirm {
			note.ConfirmedAt, note.ConfirmedBy = gatewayNow().UTC(), actor
		}
		if i < 0 {
			next.TrustedNotes = append(next.TrustedNotes, note)
		} else {
			next.TrustedNotes[i] = note
		}
		if addr, aerr := ParseAddr(client); aerr == nil && !expires.IsZero() && s.isTrusted(old, addr) && !s.isTrusted(next, addr) {
			return false, guarded("%s is how your own address is trusted, and nothing else covers it. An expiry would let a list or a limit refuse you after it passes; trust your address another way first.", want)
		}
		return false, nil
	})
}

// recordTrustedNote records why an address was kept when the dashboard adds
// it for the requester.
func recordTrustedNote(sp *Spec, address, actor, reason string) {
	if _, i := trustedNote(sp, address); i >= 0 {
		sp.TrustedNotes[i].ExpiresAt = time.Time{}
		return
	}
	sp.TrustedNotes = append(sp.TrustedNotes, TrustedNote{Address: address, Reason: reason, Made: gwStamp(actor)})
}

// sweepExpired takes expired exceptions and trusted addresses out of the
// spec. The kernel already stopped honouring them; this keeps the lists
// honest and the boot file small. Returns whether anything was removed.
func (s *Service) sweepExpired(ctx context.Context) (bool, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return false, err
	}
	now := gatewayNow()
	if !hasExpired(sp, now) {
		return false, nil
	}
	// A change awaiting confirmation holds the journal; the kernel already
	// ignores the expired rules, so tidying waits for the next pass.
	if prior, err := readChange(s.paths.Dir); err == nil && !changeTerminal(prior.Phase) {
		return false, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	removed := false
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		kept := next.Exceptions[:0]
		for _, e := range next.Exceptions {
			if !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt) {
				removed = true
				continue
			}
			kept = append(kept, e)
		}
		next.Exceptions = kept
		expired := map[string]bool{}
		notes := next.TrustedNotes[:0]
		for _, n := range next.TrustedNotes {
			if !n.ExpiresAt.IsZero() && !now.Before(n.ExpiresAt) {
				expired[n.Address] = true
				removed = true
				continue
			}
			notes = append(notes, n)
		}
		next.TrustedNotes = notes
		trusted := next.Trusted[:0]
		for _, raw := range next.Trusted {
			if p, perr := ParsePrefix(raw); perr == nil && expired[p.Masked().String()] {
				continue
			}
			trusted = append(trusted, raw)
		}
		next.Trusted = trusted
		return false, nil
	})
	return removed, err
}

func hasExpired(sp *Spec, now time.Time) bool {
	for _, e := range sp.Exceptions {
		if !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt) {
			return true
		}
	}
	for _, n := range sp.TrustedNotes {
		if !n.ExpiresAt.IsZero() && !now.Before(n.ExpiresAt) {
			return true
		}
	}
	return false
}

// expiryLoop sweeps expired entries every interval while ctx lives.
func (s *Service) expiryLoop(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep, cancel := context.WithTimeout(ctx, time.Minute)
			if _, err := s.sweepExpired(sweep); err != nil {
				s.log.Warn("removing expired protection exceptions", "err", err)
			}
			cancel()
		}
	}
}

// operatorActivity is the last successful dashboard use per address: live
// sessions and recorded sign-ins in the bounded window. It is evidence for
// review, not authority — a NAT or a VPN can share or change addresses.
func (s *Service) operatorActivity(ctx context.Context) map[netip.Addr]time.Time {
	out := map[netip.Addr]time.Time{}
	if s.db == nil {
		return out
	}
	since := gatewayNow().Add(-operatorActivityWindow).Unix()
	read := func(query string) {
		rows, err := s.db.QueryContext(ctx, query, since)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var ip string
			var at sql.NullInt64
			if rows.Scan(&ip, &at) != nil || !at.Valid {
				continue
			}
			addr, err := ParseAddr(ip)
			if err != nil {
				continue
			}
			if t := time.Unix(at.Int64, 0).UTC(); t.After(out[addr]) {
				out[addr] = t
			}
		}
	}
	read(`SELECT ip, MAX(last_seen_at) FROM sessions WHERE ip <> '' AND last_seen_at >= ? GROUP BY ip`)
	read(`SELECT ip, MAX(ts) FROM audit_log WHERE action = 'auth.login' AND success = 1 AND ip <> '' AND ts >= ? GROUP BY ip`)
	return out
}
