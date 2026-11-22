// Package appdomain gives this server's deployed apps addresses under
// just-dashboard.com: <app>.<handle>.<zone>.
//
// One handle per server, held on the site through the account link. The site
// publishes <handle>.<zone> and *.<handle>.<zone> at this server's public IPv4
// address, and this server keeps one wildcard certificate for both, proved
// over DNS-01 through the site (proxysvc/app_domain.go). After that a
// deployment needs nothing from the site: the wildcard record resolves every
// app name and the wildcard certificate covers it.
//
// The contract with the site is app-domains-protocol.md in its repository,
// https://github.com/JustDashboard/frontend/blob/main/docs/app-domains-protocol.md,
// and the design is docs/internal/backend/app-domains.md.
package appdomain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// settingsKey holds the State.
const settingsKey = "app.domain.state"

// The statuses a domain has here: the site's two, and detached for a handle
// this server held and no longer does (released on the site, the link
// cleared, the server removed there).
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusDetached  = "detached"
)

// State is everything this server keeps about its app domain. A state with
// no Handle holds no domain, and keeps AutoAssign for the next claim.
type State struct {
	// Revision is raised by every write, so a write that read an older copy
	// is never the one stored (update).
	Revision int64  `json:"revision"`
	Handle   string `json:"handle,omitempty"`
	Zone     string `json:"zone,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Address  string `json:"address,omitempty"`
	Status   string `json:"status,omitempty"`
	// Reason is why the domain is suspended or detached, for the page.
	Reason string `json:"reason,omitempty"`
	// AutoAssign gives new deployments the address by default.
	AutoAssign bool  `json:"autoAssign"`
	UpdatedAt  int64 `json:"updatedAt,omitempty"`
	// LastCheckedAt is the last answer from GET /domain, and LastError the
	// last failed exchange with the site (cleared by a good one).
	LastCheckedAt int64  `json:"lastCheckedAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	// Available and AvailableReason are the site's last word on whether it
	// offers app domains at all; nil when it has not been asked.
	Available       *bool  `json:"available,omitempty"`
	AvailableReason string `json:"availableReason,omitempty"`
	// CertificateError is why the last issuance failed, until one succeeds.
	CertificateError string `json:"certificateError,omitempty"`
	// Offer is the handle the account chose for this server on the site
	// (at link approval or later) that it has not claimed; GET /domain's
	// offer. OfferTried and OfferTriedAt are the last automatic claim of
	// one, and OfferError why it failed: the same handle is not tried
	// again within the hour (autoClaim).
	Offer        string `json:"offer,omitempty"`
	OfferTried   string `json:"offerTried,omitempty"`
	OfferTriedAt int64  `json:"offerTriedAt,omitempty"`
	OfferError   string `json:"offerError,omitempty"`
}

// holdsDomain reports a handle the site holds for this server, usable or
// suspended; a detached one is not held.
func (s State) holdsDomain() bool { return s.Handle != "" && s.Status != StatusDetached }

// held reports a handle this server holds and may use now.
func (s State) held() bool { return s.Handle != "" && s.Status == StatusActive }

// base is <handle>.<zone>, the name every app name ends in.
func (s State) base() string {
	if s.Handle == "" || s.Zone == "" {
		return ""
	}
	return s.Handle + "." + s.Zone
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readState(ctx context.Context, q queryer) (State, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing claimed yet: new deployments get the address by default
		// once one is.
		return State{AutoAssign: true}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return State{}, err
	}
	return st, nil
}

func writeState(ctx context.Context, tx *sql.Tx, st State) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKey, string(raw))
	return err
}

// update changes the state inside one transaction: the copy change sees is
// the one stored, and what it leaves is stored with the next revision. change
// reports whether it changed anything.
func update(ctx context.Context, db *sql.DB, change func(*State) (bool, error)) (State, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	st, err := readState(ctx, tx)
	if err != nil {
		return State{}, err
	}
	changed, err := change(&st)
	if err != nil {
		return st, err
	}
	if !changed {
		return st, nil
	}
	st.Revision++
	if err := writeState(ctx, tx, st); err != nil {
		return State{}, err
	}
	return st, tx.Commit()
}

// DetachInTx marks a held handle detached, inside a transaction that ends
// the account link. The site deletes a server's records when it is unlinked
// or removed, so the handle is no longer this server's to use. It is the
// account link's OnClear hook (and the terminal unlink's).
func DetachInTx(ctx context.Context, tx *sql.Tx, now int64) error {
	st, err := readState(ctx, tx)
	if err != nil || st.Handle == "" || st.Status == StatusDetached {
		return err
	}
	st.Status, st.Reason, st.UpdatedAt = StatusDetached, "The account link was cleared, so the site released this server's handle and deleted its records.", now
	st.Revision++
	return writeState(ctx, tx, st)
}
