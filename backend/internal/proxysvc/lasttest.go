package proxysvc

import (
	"sync"
	"time"
)

// TestRecord is the engine's most recent test of the configuration on disk:
// Test config's, a reload's, the one a start or restart runs first, and the
// one a config editor save runs with the file in place.
//
// nginx passes a configuration it is quietly ignoring part of — a second site
// claiming a server name another already holds is "ignored" with a [warn],
// and the test exits 0 — and that warning was on screen for as long as a
// toast. Kept here it outlives the toast and the page, so the overview can go
// on saying so until a test comes back clean. It is kept in memory: a
// restarted dashboard has no record rather than one it cannot vouch for.
type TestRecord struct {
	Kind Kind `json:"kind"`
	// CheckedAt is when the test began, which is when the engine read the
	// files it judged.
	CheckedAt  time.Time         `json:"checkedAt"`
	Validation *ValidationResult `json:"validation"`
}

type testMemory struct {
	mu   sync.Mutex
	last map[Kind]TestRecord
}

// remember keeps res as kind's last test, begun at started. A test begun
// before the one already kept read an older configuration, however late it
// finished, so it does not replace it.
func (s *Service) remember(kind Kind, started time.Time, res *ValidationResult) {
	if res == nil {
		return
	}
	s.tested.mu.Lock()
	defer s.tested.mu.Unlock()
	if kept, ok := s.tested.last[kind]; ok && kept.CheckedAt.After(started) {
		return
	}
	if s.tested.last == nil {
		s.tested.last = map[Kind]TestRecord{}
	}
	s.tested.last[kind] = TestRecord{Kind: kind, CheckedAt: started, Validation: res.clone()}
}

// LastTest is kind's most recent test, if one has run since the dashboard
// started. The result is the caller's own copy.
func (s *Service) LastTest(kind Kind) (TestRecord, bool) {
	s.tested.mu.Lock()
	defer s.tested.mu.Unlock()
	rec, ok := s.tested.last[kind]
	if ok {
		rec.Validation = rec.Validation.clone()
	}
	return rec, ok
}

// clone copies a result deeply enough that neither copy's diagnostics can
// change under the other: a kept record is read by requests while the one
// that made it goes on to place and serve its own.
func (r *ValidationResult) clone() *ValidationResult {
	c := *r
	c.Diagnostics = make([]Diagnostic, len(r.Diagnostics))
	for i, d := range r.Diagnostics {
		if d.Claims != nil {
			d.Claims = append([]NameClaim(nil), d.Claims...)
		}
		c.Diagnostics[i] = d
	}
	return &c
}
