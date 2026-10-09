package netsec

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Blocking a remote address from the connection it made, with a reason and,
// where the operator wants one, an end.
//
// The block is an ordinary deny in the host's firewall, added through the same
// guarded path as a rule written on the Firewall page — the lockout guard
// refuses the operator's own address, and the rule is persistent in the
// firewall's own configuration — so nothing here is a second firewall. What
// this adds is a record beside it: why, by whom, which saved incident it
// belongs to, and when it ends. An expiring block is lifted by a loop that
// removes exactly the rule it added, found by the comment it was written
// with (or, on a firewall that keeps no comments, by its exact shape, which
// creation refuses to duplicate). Lifting a block only ever loosens the
// firewall, so the loop can never cut anybody off.

// Block is one recorded block and its rule's standing.
type Block struct {
	ID            string     `json:"id"`
	Address       string     `json:"address"`
	Reason        string     `json:"reason"`
	Comment       string     `json:"comment"`
	IncidentRunID string     `json:"incidentRunId,omitempty"`
	CreatedBy     string     `json:"createdBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	// State is active, expired (lifted by its end), or lifted (by hand).
	State   string     `json:"state"`
	EndedAt *time.Time `json:"endedAt,omitempty"`
	EndedBy string     `json:"endedBy,omitempty"`
	// EndError is why the last attempt to lift an ended block failed; the
	// block stays active and the loop tries again.
	EndError string `json:"endError,omitempty"`
	// RulePresent is whether the firewall lists the block's rule now; nil
	// where the firewall could not be read.
	RulePresent *bool `json:"rulePresent,omitempty"`
}

// BlockRequest is the body of a new block.
type BlockRequest struct {
	Address string `json:"address"`
	Reason  string `json:"reason"`
	// DurationSeconds is how long it lasts; zero is until lifted.
	DurationSeconds int64  `json:"durationSeconds"`
	IncidentRunID   string `json:"incidentRunId,omitempty"`
}

var (
	// ErrBlockExists is an address already blocked, here or in the firewall.
	ErrBlockExists = errors.New("already blocked")
	// ErrBlockNotFound is a block id nothing records.
	ErrBlockNotFound = errors.New("no such block")
	// ErrBlockInvalid is a request that does not validate.
	ErrBlockInvalid = errors.New("invalid block")
)

const (
	minBlock    = 5 * time.Minute
	maxBlock    = 90 * 24 * time.Hour
	blocksShown = 100
	// blockPrefix starts every block rule's comment, so the Firewall page and
	// this record both say where the rule came from.
	blockPrefix = "jd-block "
)

// blockFirewall is the part of the firewall a block uses: the guarded add,
// the listing, and delete by listed number. *Service is it.
type blockFirewall interface {
	Status(ctx context.Context) (*FirewallStatus, error)
	AddRule(ctx context.Context, req RuleRequest, callerIP string) (string, error)
	DeleteRule(ctx context.Context, number int) (string, error)
}

// Blocks keeps the block records and lifts the ones that end.
type Blocks struct {
	db  *sql.DB
	fw  blockFirewall
	mu  sync.Mutex
	now func() time.Time
}

func NewBlocks(db *sql.DB, fw blockFirewall) *Blocks {
	return &Blocks{db: db, fw: fw, now: time.Now}
}

func validBlockRequest(req BlockRequest) (netip.Addr, string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(req.Address))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, "", fmt.Errorf("%w: the address is one IPv4 or IPv6 address", ErrBlockInvalid)
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalMulticast() {
		return netip.Addr{}, "", fmt.Errorf("%w: %s is not a remote host's address", ErrBlockInvalid, a)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || len(reason) > 200 {
		return netip.Addr{}, "", fmt.Errorf("%w: say why in 1 to 200 characters; the reason is what the record shows a month later", ErrBlockInvalid)
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return netip.Addr{}, "", fmt.Errorf("%w: the reason is one line of text", ErrBlockInvalid)
		}
	}
	if req.DurationSeconds != 0 {
		d := time.Duration(req.DurationSeconds) * time.Second
		if req.DurationSeconds < 0 || d < minBlock || d > maxBlock {
			return netip.Addr{}, "", fmt.Errorf("%w: a block lasts from 5 minutes to 90 days, or until lifted", ErrBlockInvalid)
		}
	}
	if id := req.IncidentRunID; id != "" && !isHexID(id) {
		return netip.Addr{}, "", fmt.Errorf("%w: the incident is a saved diagnostic run's id", ErrBlockInvalid)
	}
	return a, reason, nil
}

func isHexID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// sameAddress is whether a listed rule's source is exactly this address.
func sameAddress(from string, a netip.Addr) bool {
	from = strings.TrimSpace(from)
	if p, err := netip.ParsePrefix(from); err == nil {
		return p.IsSingleIP() && p.Addr().Unmap() == a
	}
	b, err := netip.ParseAddr(from)
	return err == nil && b.Unmap() == a
}

// blockRule is a listed rule that is a plain source deny of an address.
func blockRule(r Rule, a netip.Addr) bool {
	return (r.Action == "deny" || strings.EqualFold(r.Action, "DENY")) && sameAddress(r.From, a) &&
		r.Port == "" && !strings.EqualFold(r.Direction, "out")
}

// Create adds the deny through the guarded firewall path and records it.
// The rule goes in first: a record of a block the firewall refused would
// claim protection that does not exist.
func (b *Blocks) Create(ctx context.Context, req BlockRequest, callerIP, actor string) (*Block, error) {
	a, reason, err := validBlockRequest(req)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var existing string
	err = b.db.QueryRowContext(ctx, `SELECT id FROM network_address_blocks WHERE address = ? AND state = 'active'`, a.String()).Scan(&existing)
	if err == nil {
		return nil, fmt.Errorf("%w: %s has an active block", ErrBlockExists, a)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// A deny of the same shape already in the firewall is somebody's
	// permanent rule; an expiring block on top would remove it when it ends.
	if st, err := b.fw.Status(ctx); err == nil {
		for _, r := range st.Rules {
			if blockRule(r, a) {
				return nil, fmt.Errorf("%w: the firewall already denies %s (rule %d); edit that rule on the Firewall page", ErrBlockExists, a, r.Number)
			}
		}
	}
	id, err := blockID()
	if err != nil {
		return nil, err
	}
	comment := blockPrefix + id[:12]
	if _, err := b.fw.AddRule(ctx, RuleRequest{Action: "deny", Direction: "in", From: a.String(), Comment: comment}, callerIP); err != nil {
		return nil, err
	}
	now := b.now().UTC()
	var expires int64
	if req.DurationSeconds > 0 {
		expires = now.Add(time.Duration(req.DurationSeconds) * time.Second).Unix()
	}
	if _, err := b.db.ExecContext(ctx, `INSERT INTO network_address_blocks
		(id, address, reason, comment, incident_run_id, created_by, created_at, expires_at, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active')`,
		id, a.String(), reason, comment, req.IncidentRunID, actor, now.Unix(), expires); err != nil {
		// The rule exists and the record does not: take the rule back
		// rather than leave a deny nothing explains or ends.
		if _, removeErr := b.removeRules(context.WithoutCancel(ctx), comment, a); removeErr != nil {
			return nil, fmt.Errorf("recording the block failed (%v), and removing its rule failed too: %w", err, removeErr)
		}
		return nil, fmt.Errorf("recording the block: %w", err)
	}
	return b.get(ctx, id)
}

func blockID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

const blockColumns = `id, address, reason, comment, incident_run_id, created_by, created_at, expires_at, state, ended_at, ended_by, end_error`

func scanBlock(scan func(...any) error) (Block, error) {
	var blk Block
	var created, expires, ended int64
	if err := scan(&blk.ID, &blk.Address, &blk.Reason, &blk.Comment, &blk.IncidentRunID, &blk.CreatedBy,
		&created, &expires, &blk.State, &ended, &blk.EndedBy, &blk.EndError); err != nil {
		return blk, err
	}
	blk.CreatedAt = time.Unix(created, 0).UTC()
	if expires > 0 {
		t := time.Unix(expires, 0).UTC()
		blk.ExpiresAt = &t
	}
	if ended > 0 {
		t := time.Unix(ended, 0).UTC()
		blk.EndedAt = &t
	}
	return blk, nil
}

func (b *Blocks) get(ctx context.Context, id string) (*Block, error) {
	blk, err := scanBlock(b.db.QueryRowContext(ctx, `SELECT `+blockColumns+` FROM network_address_blocks WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBlockNotFound
	}
	if err != nil {
		return nil, err
	}
	return &blk, nil
}

// List is the active blocks and the most recent ended ones, each with
// whether the firewall still lists its rule.
func (b *Blocks) List(ctx context.Context) ([]Block, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT `+blockColumns+` FROM network_address_blocks
		ORDER BY state = 'active' DESC, created_at DESC LIMIT ?`, blocksShown)
	if err != nil {
		return nil, fmt.Errorf("reading the blocks: %w", err)
	}
	out := []Block{}
	for rows.Next() {
		blk, err := scanBlock(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, blk)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	st, err := b.fw.Status(ctx)
	if err != nil || st.Error != "" {
		return out, nil
	}
	for i := range out {
		if out[i].State != "active" {
			continue
		}
		a, err := netip.ParseAddr(out[i].Address)
		if err != nil {
			continue
		}
		present := len(matchingRules(st.Rules, out[i].Comment, a)) > 0
		out[i].RulePresent = &present
	}
	return out, nil
}

// matchingRules is the block's rules in a listing: by comment where the
// firewall keeps comments, by exact shape where it keeps none.
func matchingRules(rules []Rule, comment string, a netip.Addr) []Rule {
	var byComment, byShape []Rule
	for _, r := range rules {
		if r.Comment == comment {
			byComment = append(byComment, r)
		} else if r.Comment == "" && blockRule(r, a) {
			byShape = append(byShape, r)
		}
	}
	if len(byComment) > 0 {
		return byComment
	}
	return byShape
}

// removeRules deletes the block's rules one at a time, reading the list
// again before each so a number is never stale. It answers how many went.
func (b *Blocks) removeRules(ctx context.Context, comment string, a netip.Addr) (int, error) {
	removed := 0
	for attempt := 0; attempt < 4; attempt++ {
		st, err := b.fw.Status(ctx)
		if err != nil {
			return removed, err
		}
		if st.Error != "" {
			return removed, errors.New(st.Error)
		}
		matches := matchingRules(st.Rules, comment, a)
		if len(matches) == 0 {
			return removed, nil
		}
		number := matches[0].Number
		for i, r := range st.Rules {
			if r.Number == 0 && sameRule(r, matches[0]) {
				number = i + 1
			}
		}
		if _, err := b.fw.DeleteRule(ctx, number); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, errors.New("the block's rule is still listed after removing it")
}

// Lift ends an active block now and removes its rule.
func (b *Blocks) Lift(ctx context.Context, id, actor string) (*Block, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	blk, err := b.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if blk.State != "active" {
		return nil, fmt.Errorf("%w: the block of %s already ended", ErrBlockNotFound, blk.Address)
	}
	return b.end(ctx, blk, "lifted", actor)
}

func (b *Blocks) end(ctx context.Context, blk *Block, state, actor string) (*Block, error) {
	a, err := netip.ParseAddr(blk.Address)
	if err != nil {
		return nil, err
	}
	if _, err := b.removeRules(ctx, blk.Comment, a); err != nil {
		_, _ = b.db.ExecContext(ctx, `UPDATE network_address_blocks SET end_error = ? WHERE id = ?`, err.Error(), blk.ID)
		return nil, err
	}
	now := b.now().UTC().Unix()
	if _, err := b.db.ExecContext(ctx, `UPDATE network_address_blocks SET state = ?, ended_at = ?, ended_by = ?, end_error = ''
		WHERE id = ?`, state, now, actor, blk.ID); err != nil {
		return nil, fmt.Errorf("the rule was removed but the record could not say so: %w", err)
	}
	return b.get(ctx, blk.ID)
}

// ExpiryOutcome is one ended block the loop acted on.
type ExpiryOutcome struct {
	Block Block
	Err   error
}

// Expire lifts every active block whose end has passed. A block whose rule
// cannot be removed stays active with the error, and is tried again.
func (b *Blocks) Expire(ctx context.Context) []ExpiryOutcome {
	b.mu.Lock()
	defer b.mu.Unlock()
	rows, err := b.db.QueryContext(ctx, `SELECT `+blockColumns+` FROM network_address_blocks
		WHERE state = 'active' AND expires_at > 0 AND expires_at <= ? ORDER BY expires_at`, b.now().Unix())
	if err != nil {
		return []ExpiryOutcome{{Err: err}}
	}
	var due []Block
	for rows.Next() {
		blk, err := scanBlock(rows.Scan)
		if err == nil {
			due = append(due, blk)
		}
	}
	rows.Close()
	out := make([]ExpiryOutcome, 0, len(due))
	for i := range due {
		ended, err := b.end(ctx, &due[i], "expired", "expiry")
		if err != nil {
			out = append(out, ExpiryOutcome{Block: due[i], Err: err})
			continue
		}
		out = append(out, ExpiryOutcome{Block: *ended})
	}
	return out
}

// Run expires blocks every interval until ctx ends, reporting each outcome.
func (b *Blocks) Run(ctx context.Context, every time.Duration, report func(ExpiryOutcome)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for _, o := range b.Expire(ctx) {
			report(o)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
