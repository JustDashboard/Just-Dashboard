package netsec

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// fakeFirewall is a numbered rule list behind the same validation and
// lockout guard Service.AddRule runs, so a block is refused exactly where a
// rule written on the Firewall page would be.
type fakeFirewall struct {
	rules      []Rule
	noComments bool
	deleteErr  error
	callers    []string
	deletes    []int
}

func (f *fakeFirewall) Status(context.Context) (*FirewallStatus, error) {
	out := make([]Rule, len(f.rules))
	copy(out, f.rules)
	return &FirewallStatus{Rules: out}, nil
}

func (f *fakeFirewall) AddRule(_ context.Context, req RuleRequest, callerIP string) (string, error) {
	f.callers = append(f.callers, callerIP)
	clean, err := normaliseRule(req)
	if err != nil {
		return "", err
	}
	if err := guardLockout(clean.Action, clean.Direction, clean.From, callerIP); err != nil {
		return "", err
	}
	r := Rule{Action: clean.Action, Direction: clean.Direction, From: clean.From, Comment: clean.Comment}
	if f.noComments {
		r.Comment = ""
	}
	// A source deny goes in front, as ufw's backend inserts it.
	f.rules = append([]Rule{r}, f.rules...)
	f.renumber()
	return "Rule inserted", nil
}

func (f *fakeFirewall) DeleteRule(_ context.Context, number int) (string, error) {
	f.deletes = append(f.deletes, number)
	if f.deleteErr != nil {
		return "", f.deleteErr
	}
	for i, r := range f.rules {
		if r.Number == number {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			f.renumber()
			return "Rule deleted", nil
		}
	}
	return "", errors.New("no such rule")
}

func (f *fakeFirewall) renumber() {
	for i := range f.rules {
		f.rules[i].Number = i + 1
	}
}

func blocksDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st.DB
}

const operator = "100.110.34.9"

func newTestBlocks(t *testing.T) (*Blocks, *fakeFirewall, *time.Time) {
	t.Helper()
	fw := &fakeFirewall{rules: []Rule{{Action: "allow", Direction: "in", Port: "22", From: "Anywhere"}}}
	fw.renumber()
	b := NewBlocks(blocksDB(t), fw)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	return b, fw, &now
}

func TestBlockRequestsAreValidatedBeforeTheFirewallIsTouched(t *testing.T) {
	b, fw, _ := newTestBlocks(t)
	ctx := context.Background()
	for _, c := range []struct {
		req  BlockRequest
		want string
	}{
		{BlockRequest{Address: "example.com", Reason: "x"}, "one IPv4 or IPv6 address"},
		{BlockRequest{Address: "198.51.100.0/24", Reason: "x"}, "one IPv4 or IPv6 address"},
		{BlockRequest{Address: "127.0.0.1", Reason: "x"}, "not a remote host"},
		{BlockRequest{Address: "::", Reason: "x"}, "not a remote host"},
		{BlockRequest{Address: "224.0.0.1", Reason: "x"}, "not a remote host"},
		{BlockRequest{Address: "198.51.100.23", Reason: "  "}, "say why"},
		{BlockRequest{Address: "198.51.100.23", Reason: strings.Repeat("x", 201)}, "say why"},
		{BlockRequest{Address: "198.51.100.23", Reason: "two\nlines"}, "one line"},
		{BlockRequest{Address: "198.51.100.23", Reason: "x", DurationSeconds: 60}, "5 minutes to 90 days"},
		{BlockRequest{Address: "198.51.100.23", Reason: "x", DurationSeconds: 100 * 86400}, "5 minutes to 90 days"},
		{BlockRequest{Address: "198.51.100.23", Reason: "x", IncidentRunID: "not-an-id"}, "saved diagnostic"},
	} {
		if _, err := b.Create(ctx, c.req, operator, "ops"); !errors.Is(err, ErrBlockInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v = %v, want %q", c.req, err, c.want)
		}
	}
	if len(fw.callers) != 0 {
		t.Fatalf("the firewall was asked %d times", len(fw.callers))
	}
}

// The block goes through the guarded rule path: the operator's own address
// is refused there and nothing is recorded.
func TestBlockingTheOperatorsOwnAddressIsRefused(t *testing.T) {
	b, fw, _ := newTestBlocks(t)
	_, err := b.Create(context.Background(), BlockRequest{Address: operator, Reason: "testing", DurationSeconds: 3600}, operator, "ops")
	if !errors.Is(err, ErrLockout) {
		t.Fatalf("err = %v", err)
	}
	if len(fw.rules) != 1 || len(fw.callers) != 1 || fw.callers[0] != operator {
		t.Fatalf("rules = %+v callers = %v", fw.rules, fw.callers)
	}
	list, _ := b.List(context.Background())
	if len(list) != 0 {
		t.Fatalf("a refused block was recorded: %+v", list)
	}
}

// A temporary block is a deny with its own comment, recorded with why and
// until when; the loop lifts exactly its rule when it ends.
func TestATemporaryBlockIsLiftedWhenItEnds(t *testing.T) {
	b, fw, now := newTestBlocks(t)
	ctx := context.Background()
	blk, err := b.Create(ctx, BlockRequest{Address: "198.51.100.23", Reason: "credential stuffing on /login", DurationSeconds: 3600,
		IncidentRunID: "0123456789abcdef0123456789abcdef"}, operator, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if blk.State != "active" || blk.ExpiresAt == nil || !blk.ExpiresAt.Equal(now.Add(time.Hour)) || blk.Reason != "credential stuffing on /login" ||
		blk.IncidentRunID != "0123456789abcdef0123456789abcdef" || !strings.HasPrefix(blk.Comment, "jd-block ") || blk.CreatedBy != "ops" {
		t.Fatalf("block = %+v", blk)
	}
	if fw.rules[0].Action != "deny" || fw.rules[0].From != "198.51.100.23" || fw.rules[0].Comment != blk.Comment {
		t.Fatalf("rules = %+v", fw.rules)
	}
	// The same address again is refused, here and when the firewall already
	// holds a plain deny somebody wrote.
	if _, err := b.Create(ctx, BlockRequest{Address: "198.51.100.23", Reason: "again"}, operator, "ops"); !errors.Is(err, ErrBlockExists) {
		t.Fatalf("a second block = %v", err)
	}
	fw.rules = append(fw.rules, Rule{Action: "deny", Direction: "in", From: "203.0.113.77"})
	fw.renumber()
	if _, err := b.Create(ctx, BlockRequest{Address: "203.0.113.77", Reason: "x", DurationSeconds: 600}, operator, "ops"); !errors.Is(err, ErrBlockExists) || !strings.Contains(err.Error(), "already denies") {
		t.Fatalf("over an existing deny = %v", err)
	}
	list, _ := b.List(ctx)
	if len(list) != 1 || list[0].RulePresent == nil || !*list[0].RulePresent {
		t.Fatalf("list = %+v", list)
	}

	// Not yet: nothing happens.
	if out := b.Expire(ctx); len(out) != 0 {
		t.Fatalf("expired early: %+v", out)
	}
	*now = now.Add(time.Hour + time.Second)
	out := b.Expire(ctx)
	if len(out) != 1 || out[0].Err != nil || out[0].Block.State != "expired" || out[0].Block.EndedBy != "expiry" || out[0].Block.EndedAt == nil {
		t.Fatalf("expiry = %+v", out)
	}
	for _, r := range fw.rules {
		if r.From == "198.51.100.23" {
			t.Fatalf("the block's rule is still there: %+v", fw.rules)
		}
	}
	// The other deny and the allow are untouched.
	if len(fw.rules) != 2 {
		t.Fatalf("rules after expiry = %+v", fw.rules)
	}
	if out := b.Expire(ctx); len(out) != 0 {
		t.Fatalf("expired twice: %+v", out)
	}
}

// On a firewall that keeps no comments the rule is found by its exact shape,
// which creation refused to duplicate. A failed removal leaves the block
// active with the reason, and the next pass tries again.
func TestExpiryWithoutCommentsAndAfterAFailedRemoval(t *testing.T) {
	b, fw, now := newTestBlocks(t)
	fw.noComments = true
	ctx := context.Background()
	if _, err := b.Create(ctx, BlockRequest{Address: "2001:db8::77", Reason: "scanner", DurationSeconds: 600}, operator, "ops"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(11 * time.Minute)
	fw.deleteErr = errors.New("ufw: could not delete")
	out := b.Expire(ctx)
	if len(out) != 1 || out[0].Err == nil {
		t.Fatalf("failed expiry = %+v", out)
	}
	list, _ := b.List(ctx)
	if list[0].State != "active" || !strings.Contains(list[0].EndError, "could not delete") {
		t.Fatalf("after a failure = %+v", list[0])
	}
	fw.deleteErr = nil
	out = b.Expire(ctx)
	if len(out) != 1 || out[0].Err != nil || out[0].Block.State != "expired" || out[0].Block.EndError != "" {
		t.Fatalf("retry = %+v", out)
	}
	if len(fw.rules) != 1 || fw.rules[0].Port != "22" {
		t.Fatalf("rules = %+v", fw.rules)
	}
}

func TestLiftingAPermanentBlockByHand(t *testing.T) {
	b, fw, _ := newTestBlocks(t)
	ctx := context.Background()
	blk, err := b.Create(ctx, BlockRequest{Address: "192.0.2.145", Reason: "ssh brute force"}, operator, "ops")
	if err != nil || blk.ExpiresAt != nil {
		t.Fatalf("permanent = %+v %v", blk, err)
	}
	// A permanent block never expires.
	if out := b.Expire(ctx); len(out) != 0 {
		t.Fatalf("expired: %+v", out)
	}
	lifted, err := b.Lift(ctx, blk.ID, "ana")
	if err != nil || lifted.State != "lifted" || lifted.EndedBy != "ana" {
		t.Fatalf("lifted = %+v %v", lifted, err)
	}
	if len(fw.rules) != 1 {
		t.Fatalf("rules = %+v", fw.rules)
	}
	if _, err := b.Lift(ctx, blk.ID, "ana"); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("lifting twice = %v", err)
	}
	if _, err := b.Lift(ctx, "missing", "ana"); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("lifting nothing = %v", err)
	}
	// A rule removed outside the dashboard reads as absent.
	blk, _ = b.Create(ctx, BlockRequest{Address: "192.0.2.146", Reason: "x"}, operator, "ops")
	fw.rules = fw.rules[1:]
	fw.renumber()
	list, _ := b.List(ctx)
	if list[0].ID != blk.ID || list[0].RulePresent == nil || *list[0].RulePresent {
		t.Fatalf("list = %+v", list[0])
	}
}
