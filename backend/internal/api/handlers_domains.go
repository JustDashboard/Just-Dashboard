package api

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// The watch list is endpoints, a name and a port, in watched_endpoints: the
// older watched_domains held one row per name, so watching mail.example.com
// on 993 quietly replaced it on 443.

type watchedDomain struct {
	ID        int64     `json:"id"`
	Domain    string    `json:"domain"`
	Port      int       `json:"port"`
	CreatedAt time.Time `json:"createdAt"`
	// CheckedAt is when Cert was read; absent until the first check.
	CheckedAt *time.Time            `json:"checkedAt,omitempty"`
	Cert      *proxysvc.Certificate `json:"certificate,omitempty"`
}

// handleWatchedDomains lists the watched endpoints with what their last live
// check found.
//
// Only an administrator's request checks them. A check is a handshake with a
// host off this machine, and a read-only account is not allowed to send one —
// yet opening the Certificates page used to send one to every endpoint, and
// again every five minutes while it stayed open. Everyone else reads the
// stored result and the time it was taken.
//
// The checks run eight at a time under one 30-second budget, because each is
// a handshake with a remote host and twenty in sequence would make the page
// feel broken. The budget is real: a handshake still going when it ends is
// abandoned, an endpoint not started is not started, and both keep the
// result they had, so every row's time says how old its answer is. The
// stalest go first, so a list longer than one budget is covered over the
// next visits rather than the same tail missing every time.
func (s *Server) handleWatchedDomains(w http.ResponseWriter, r *http.Request) error {
	domains, err := s.watchedEndpoints(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	if r.URL.Query().Get("check") != "false" && httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		ctx, cancel := timeoutCtx(r, 30*time.Second)
		defer cancel()
		checked := checkWatched(ctx, domains)
		// What was found is kept even when the viewer has gone: those
		// handshakes were made, and the next reader should see them.
		if err := s.storeWatchedChecks(context.WithoutCancel(r.Context()), checked); err != nil {
			return httpx.Internal(err)
		}
	}
	httpx.JSON(w, http.StatusOK, domains)
	return nil
}

// checkWatched checks the endpoints, stalest first, until ctx ends, and
// returns the ones that got an answer.
func checkWatched(ctx context.Context, domains []*watchedDomain) []*watchedDomain {
	lastChecked := func(d *watchedDomain) int64 {
		if d.CheckedAt == nil {
			return 0
		}
		return d.CheckedAt.Unix()
	}
	queue := slices.Clone(domains)
	slices.SortStableFunc(queue, func(a, b *watchedDomain) int {
		return cmp.Compare(lastChecked(a), lastChecked(b))
	})
	var (
		mu      sync.Mutex
		checked []*watchedDomain
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, 8)
	for _, d := range queue {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cert, err := proxysvc.CheckEndpoint(ctx, d.Domain, d.Port)
			if err != nil {
				return
			}
			now := time.Now().UTC().Truncate(time.Second)
			mu.Lock()
			d.Cert, d.CheckedAt = cert, &now
			checked = append(checked, d)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return checked
}

func (s *Server) watchedEndpoints(ctx context.Context) ([]*watchedDomain, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT id, domain, port, created_at, checked_at, certificate
		   FROM watched_endpoints ORDER BY domain, port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := []*watchedDomain{}
	for rows.Next() {
		var d watchedDomain
		var created, checked int64
		var cert string
		if err := rows.Scan(&d.ID, &d.Domain, &d.Port, &created, &checked, &cert); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(created, 0).UTC()
		if checked > 0 && cert != "" {
			at := time.Unix(checked, 0).UTC()
			d.CheckedAt = &at
			if err := json.Unmarshal([]byte(cert), &d.Cert); err != nil {
				return nil, err
			}
		}
		domains = append(domains, &d)
	}
	return domains, rows.Err()
}

func (s *Server) storeWatchedChecks(ctx context.Context, domains []*watchedDomain) error {
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range domains {
		cert, err := json.Marshal(d.Cert)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE watched_endpoints SET checked_at = ?, certificate = ? WHERE id = ?`,
			d.CheckedAt.Unix(), string(cert), d.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type watchDomainRequest struct {
	Domain string `json:"domain"`
	Port   int    `json:"port"`
}

// handleWatchDomain adds an endpoint, read the way the scan reads its field:
// a pasted https://mail.example.com/ watches mail.example.com, where it used
// to watch a host called "https".
func (s *Server) handleWatchDomain(w http.ResponseWriter, r *http.Request) error {
	var req watchDomainRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	target, err := proxysvc.ParseScanTarget(req.Domain, req.Port)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO watched_endpoints(domain, port, created_at) VALUES(?,?,?)
		 ON CONFLICT(domain, port, ip) DO NOTHING`,
		target.Host, target.Port, time.Now().Unix())
	if err != nil {
		return httpx.Internal(err)
	}
	added, _ := res.RowsAffected()
	d := watchedDomain{Domain: target.Host, Port: target.Port}
	var created int64
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, created_at FROM watched_endpoints WHERE domain = ? AND port = ? AND ip = ''`,
		target.Host, target.Port).Scan(&d.ID, &created); err != nil {
		return httpx.Internal(err)
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	httpx.SetAudit(r, "certificates.watch.add", target.Host, map[string]any{"port": target.Port})
	status := http.StatusOK
	if added > 0 {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, d)
	return nil
}

func (s *Server) handleUnwatchDomain(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid id")
	}
	var domain string
	var port int
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT domain, port FROM watched_endpoints WHERE id = ?`, id).Scan(&domain, &port); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.ErrNotFound
		}
		return httpx.Internal(err)
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM watched_endpoints WHERE id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	// The copy from watched_domains runs on every boot, so the row it came
	// from goes too or the endpoint would be back after a restart.
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM watched_domains WHERE domain = ? AND port = ?`, domain, port); err != nil {
		return httpx.Internal(err)
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.watch.remove", domain, map[string]any{"port": port})
	httpx.NoContent(w)
	return nil
}
