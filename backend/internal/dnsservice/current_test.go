package dnsservice

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDNSServiceCurrentReviewReadsExactFreshSelectionWithoutClaim(t *testing.T) {
	for _, item := range []struct {
		engine Engine
		action string
	}{{AdGuard, "override_add"}, {PiHole, "override_add"}, {Technitium, "record_add"}, {PiHole, "client_groups"}} {
		t.Run(string(item.engine)+"/"+item.action, func(t *testing.T) {
			s, c, f := newPolicyFixture(t, item.engine)
			req := ChangeRequest{Action: item.action, Record: &RecordChange{Name: "test.owned.example", Type: "A", Value: "198.51.100.99"}}
			if item.engine == Technitium {
				req.Zone, req.Record.TTL = "owned.example", 60
			}
			if item.action == "client_groups" {
				req.Record, req.Client = nil, &ClientGroupChange{Address: "198.51.100.77", Groups: []int{}}
			}
			plan, err := s.Preview(t.Context(), c.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.CurrentChange(t.Context(), plan.ID)
			if err != nil || current.State != "available" || current.Snapshot == nil || current.Connection.ID != c.ID || current.Connection.Generation != plan.Generation || current.Snapshot.SelectionFingerprint != plan.Before.SelectionFingerprint || current.Snapshot.PolicyFingerprint != plan.Before.PolicyFingerprint {
				t.Fatal("current selection did not match retained scope", current, err)
			}
			ordinary, err := s.Inspect(t.Context(), c.ID)
			if err != nil || ordinary.Snapshot.PolicyFingerprint == current.Snapshot.PolicyFingerprint || ordinary.Snapshot.SelectionFingerprint != "" {
				t.Fatal("ordinary inventory was substituted for the selected review", err)
			}
			f.mu.Lock()
			switch item.action {
			case "client_groups":
				f.clients[0]["comment"] = "changed private comment"
			case "record_add":
				f.records[1]["comments"] = "changed zone record metadata"
			default:
				if item.engine == AdGuard {
					f.rewrites = append(f.rewrites, map[string]any{"domain": "other.example", "answer": "198.51.100.6", "enabled": true})
				} else {
					f.hosts = append(f.hosts, "198.51.100.6 other.example")
				}
			}
			f.mu.Unlock()
			current, err = s.CurrentChange(t.Context(), plan.ID)
			if err != nil || current.State != "available" || current.Snapshot.SelectionFingerprint == plan.Before.SelectionFingerprint || current.Snapshot.PolicyFingerprint == plan.Before.PolicyFingerprint {
				t.Fatal("current selected metadata did not expose drift", current, err)
			}
			retained, err := s.Change(t.Context(), plan.ID)
			if err != nil || retained.State != "planned" || retained.After != nil || retained.EndedAt != nil || retained.Before.PolicyFingerprint != plan.Before.PolicyFingerprint || f.writes != 0 {
				t.Fatal("current read claimed, overwrote or mutated the review", err)
			}
			if _, err = s.db.ExecContext(t.Context(), `UPDATE network_dns_service_changes SET state='verified',expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UnixMilli(), plan.ID); err != nil {
				t.Fatal(err)
			}
			current, err = s.CurrentChange(t.Context(), plan.ID)
			if err != nil || current.State != "available" || f.writes != 0 {
				t.Fatal("consumed expired selection could not be inspected", err)
			}
			if _, err = s.Apply(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || f.writes != 0 {
				t.Fatal("current read authorized another apply", err)
			}
		})
	}
}

func TestDNSServiceCurrentReviewRefusesGenerationChangesAndMalformedSelection(t *testing.T) {
	s, c, f := newPolicyFixture(t, AdGuard)
	plan, err := s.Preview(t.Context(), c.ID, ChangeRequest{Action: "override_add", Record: &RecordChange{Name: "test.example", Type: "A", Value: "198.51.100.99"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"action":"unknown_native_action"}`,
		`{"action":"override_add","record":{"name":"test.example","type":"TXT","value":"arbitrary"}}`,
		`{"action":"override_add","record":{"name":"test.example","type":"A","value":"198.51.100.99"},"proxy":{"url":"http://127.0.0.1:1"}}`,
		`{"action":"override_add","record":{"name":"test.example","type":"A","value":"198.51.100.99","unknown":true}}`,
	} {
		f.mu.Lock()
		reads := f.reads
		f.mu.Unlock()
		if _, err = s.db.ExecContext(t.Context(), `UPDATE network_dns_service_changes SET request_json=? WHERE id=?`, raw, plan.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CurrentChange(t.Context(), plan.ID); err == nil {
			t.Fatal("malformed retained selection was read", raw)
		}
		f.mu.Lock()
		actualReads, writes := f.reads, f.writes
		f.mu.Unlock()
		if actualReads != reads || writes != 0 {
			t.Fatal("invalid retained selection contacted or mutated the native engine")
		}
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE network_dns_service_changes SET request_json=? WHERE id=?`, `{"action":"override_add","record":{"name":"test.example","type":"A","value":"198.51.100.99"}}`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE network_dns_services SET generation=generation+1 WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CurrentChange(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || f.writes != 0 {
		t.Fatal("replacement generation admitted for retained review", err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE network_dns_services SET generation=? WHERE id=?`, plan.Generation, c.ID); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.readHook = func(path string) {
		if path == "/control/status" {
			if _, err := s.db.ExecContext(t.Context(), `UPDATE network_dns_services SET generation=generation+1 WHERE id=?`, c.ID); err != nil {
				t.Error(err)
			}
		}
	}
	f.mu.Unlock()
	if _, err = s.CurrentChange(t.Context(), plan.ID); !errors.Is(err, ErrConflict) || f.writes != 0 {
		t.Fatal("connection replacement during native reads returned stale current evidence", err)
	}
}

func TestDNSServiceCurrentReviewFailureIsBoundedAndNeverReplays(t *testing.T) {
	s, c, f := newPolicyFixture(t, AdGuard)
	plan, err := s.Preview(t.Context(), c.ID, ChangeRequest{Action: "override_add", Record: &RecordChange{Name: "test.example", Type: "A", Value: "198.51.100.99"}})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.unavailable = true
	f.mu.Unlock()
	current, err := s.CurrentChange(t.Context(), plan.ID)
	if err != nil || current.State != "unavailable" || current.Snapshot != nil || strings.Contains(current.Error, "native-policy-password") || f.writes != 0 {
		t.Fatal("failed selected read became available or exposed native error credentials", current, err)
	}
	f.mu.Lock()
	f.unavailable = false
	f.pause = make(chan struct{})
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	current, err = s.CurrentChange(ctx, plan.ID)
	if time.Since(started) > time.Second || err == nil && current.State != "unavailable" {
		t.Fatal("selected read did not honor its cancelled deadline", current, err)
	}
	retained, err := s.Change(t.Context(), plan.ID)
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if err != nil || retained.State != "planned" || retained.After != nil || writes != 0 {
		t.Fatal("failed current read claimed or replayed its retained selection", err)
	}
}
