package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// blockFirewall is a rule list standing in for the host's firewall: the
// tests must never reach ufw or firewalld on the machine running them.
type blockFirewall struct {
	rules   []netsec.Rule
	callers []string
	refuse  bool
}

func (f *blockFirewall) Status(context.Context) (*netsec.FirewallStatus, error) {
	return &netsec.FirewallStatus{Rules: append([]netsec.Rule(nil), f.rules...)}, nil
}

func (f *blockFirewall) AddRule(_ context.Context, req netsec.RuleRequest, callerIP string) (string, error) {
	f.callers = append(f.callers, callerIP)
	if f.refuse {
		return "", fmt.Errorf("%w: %s is the address you are connected from", netsec.ErrLockout, req.From)
	}
	f.rules = append(f.rules, netsec.Rule{Number: len(f.rules) + 1, Action: req.Action, Direction: req.Direction, From: req.From, Comment: req.Comment})
	return "Rule added", nil
}

func (f *blockFirewall) DeleteRule(_ context.Context, number int) (string, error) {
	for i, r := range f.rules {
		if r.Number == number {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			for j := range f.rules {
				f.rules[j].Number = j + 1
			}
			return "Rule deleted", nil
		}
	}
	return "", errors.New("no such rule")
}

func blockClients(t *testing.T) (*Server, *client, *client, *blockFirewall) {
	t.Helper()
	s := testServer(t)
	fw := &blockFirewall{}
	s.modules.blocks = netsec.NewBlocks(s.Store.DB, fw)
	h := s.Routes()
	return s, &client{t: t, h: h, cookie: signInAs(t, s, "admin", auth.RoleAdmin)},
		&client{t: t, h: h, cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}, fw
}

func blockAuditCount(t *testing.T, s *Server, action, detail string) int {
	t.Helper()
	var n int
	if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ? AND detail LIKE ?`, action, "%"+detail+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A block is a guarded deny with a reason and an end, every step audited;
// reading the blocks is the rules' standing, lifting one is destructive.
func TestFirewallBlocksAreGuardedRecordedAndAudited(t *testing.T) {
	s, admin, viewer, fw := blockClients(t)
	body := `{"address":"198.51.100.23","reason":"credential stuffing","durationSeconds":3600}`
	if w := viewer.do(http.MethodPost, "/api/v1/firewall/blocks", body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("readonly block = %d", w.Code)
	}
	if w := admin.do(http.MethodPost, "/api/v1/firewall/blocks", `{"address":"198.51.100.23","reason":""}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no reason = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodPost, "/api/v1/firewall/blocks", `{"address":"198.51.100.23","reason":"x","incidentRunId":"0123456789abcdef0123456789abcdef"}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "block_incident_absent") {
		t.Fatalf("an absent incident = %d %s", w.Code, w.Body)
	}
	if len(fw.callers) != 0 {
		t.Fatal("the firewall was asked before the request validated")
	}

	fw.refuse = true
	w := admin.do(http.MethodPost, "/api/v1/firewall/blocks", `{"address":"198.51.100.9","reason":"mine"}`, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "would_lock_you_out") {
		t.Fatalf("lockout = %d %s", w.Code, w.Body)
	}
	if blockAuditCount(t, s, "firewall.block.add", "refused_lockout") != 1 || len(fw.callers) != 1 || fw.callers[0] == "" {
		t.Fatalf("lockout audit or caller missing: %v", fw.callers)
	}
	fw.refuse = false

	w = admin.do(http.MethodPost, "/api/v1/firewall/blocks", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("block = %d %s", w.Code, w.Body)
	}
	var blk netsec.Block
	decodeNetworkBody(t, w.Body.Bytes(), &blk)
	if blk.Address != "198.51.100.23" || blk.ExpiresAt == nil || blk.State != "active" || len(fw.rules) != 1 || fw.rules[0].Comment != blk.Comment {
		t.Fatalf("block = %+v rules = %+v", blk, fw.rules)
	}
	if blockAuditCount(t, s, "firewall.block.add", blk.ID) != 1 {
		t.Fatal("the block was not audited with its id")
	}
	var list []netsec.Block
	decodeNetworkBody(t, viewer.do(http.MethodGet, "/api/v1/firewall/blocks", "", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0].RulePresent == nil || !*list[0].RulePresent {
		t.Fatalf("list = %+v", list)
	}
	if w := viewer.do(http.MethodDelete, "/api/v1/firewall/blocks/"+blk.ID, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("readonly lift = %d", w.Code)
	}
	w = admin.do(http.MethodDelete, "/api/v1/firewall/blocks/"+blk.ID, "", nil)
	if w.Code != http.StatusOK || len(fw.rules) != 0 {
		t.Fatalf("lift = %d %s rules %+v", w.Code, w.Body, fw.rules)
	}
	if blockAuditCount(t, s, "firewall.block.lift", blk.ID) != 1 {
		t.Fatal("the lift was not audited")
	}
	if w := admin.do(http.MethodDelete, "/api/v1/firewall/blocks/"+blk.ID, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("lift twice = %d", w.Code)
	}
}

func TestConnectionDetailRefusesWhatIsNotAnAddress(t *testing.T) {
	_, admin, _, _ := blockClients(t)
	if w := admin.do(http.MethodGet, "/api/v1/connections/not-an-address", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("detail = %d %s", w.Code, w.Body)
	}
}
