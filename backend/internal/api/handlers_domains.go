package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// The watch list is endpoints, a name and a port, in watched_endpoints: the
// older watched_domains held one row per name, so watching mail.example.com
// on 993 quietly replaced it on 443.

type watchedDomain struct {
	ID     int64  `json:"id"`
	Domain string `json:"domain"`
	Port   int    `json:"port"`
	// IP is the address the name is reached at instead of where DNS points,
	// "" for DNS.
	IP string `json:"ip,omitempty"`
	// Kind is "tls", a handshake and its certificate, or "tcp", a network
	// probe that only connects.
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
	// CheckedAt is when Cert, or a probe's Probe, was read; absent until the
	// first check.
	CheckedAt *time.Time            `json:"checkedAt,omitempty"`
	Cert      *proxysvc.Certificate `json:"certificate,omitempty"`
	Probe     *proxysvc.ProbeCheck  `json:"probe,omitempty"`
}

// handleWatchedDomains lists the watched endpoints with what their last check
// found, and when. It sends nothing: the server checks them on its own
// schedule (proxysvc.TLSMonitor) and an administrator can ask for a check now
// with POST /watched/check. A visit used to handshake with every endpoint,
// which made opening the page slow and let the page's viewer, not the
// server, decide when a certificate was looked at.
func (s *Server) handleWatchedDomains(w http.ResponseWriter, r *http.Request) error {
	domains, err := s.watchedEndpoints(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, domains)
	return nil
}

func (s *Server) watchedEndpoints(ctx context.Context) ([]*watchedDomain, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT id, domain, port, ip, kind, created_at, checked_at, certificate, probe
		   FROM watched_endpoints ORDER BY domain, port, ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := []*watchedDomain{}
	for rows.Next() {
		var d watchedDomain
		var created, checked int64
		var cert, probe string
		if err := rows.Scan(&d.ID, &d.Domain, &d.Port, &d.IP, &d.Kind, &created, &checked, &cert, &probe); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(created, 0).UTC()
		at := time.Unix(checked, 0).UTC()
		switch {
		case checked > 0 && d.Kind == proxysvc.WatchTCP && probe != "":
			d.CheckedAt = &at
			if err := json.Unmarshal([]byte(probe), &d.Probe); err != nil {
				return nil, err
			}
		case checked > 0 && d.Kind != proxysvc.WatchTCP && cert != "":
			d.CheckedAt = &at
			if err := json.Unmarshal([]byte(cert), &d.Cert); err != nil {
				return nil, err
			}
		}
		domains = append(domains, &d)
	}
	return domains, rows.Err()
}

type watchDomainRequest struct {
	Domain string `json:"domain"`
	Port   int    `json:"port"`
	// IP is optional: the address to reach Domain at, for an origin behind a
	// CDN or one server of several behind one name.
	IP string `json:"ip,omitempty"`
	// Kind is "tls" (the default) or "tcp", a network probe that connects
	// and checks nothing more.
	Kind string `json:"kind,omitempty"`
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
	ip, err := proxysvc.ParseWatchIP(req.IP)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	kind := req.Kind
	if kind == "" {
		kind = proxysvc.WatchTLS
	}
	if kind != proxysvc.WatchTLS && kind != proxysvc.WatchTCP {
		return httpx.BadRequest("a watch is tls or tcp")
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO watched_endpoints(domain, port, ip, kind, created_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(domain, port, ip) DO NOTHING`,
		target.Host, target.Port, ip, kind, time.Now().Unix())
	if err != nil {
		return httpx.Internal(err)
	}
	added, _ := res.RowsAffected()
	d := watchedDomain{Domain: target.Host, Port: target.Port, IP: ip}
	var created int64
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, created_at, kind FROM watched_endpoints WHERE domain = ? AND port = ? AND ip = ?`,
		target.Host, target.Port, ip).Scan(&d.ID, &created, &d.Kind); err != nil {
		return httpx.Internal(err)
	}
	switch {
	case d.Kind == proxysvc.WatchTLS && kind == proxysvc.WatchTCP:
		// A handshake opens the connection first, so the TLS watch already
		// answers what the probe would ask.
		return httpx.Err(http.StatusConflict, "already_watched",
			"This endpoint is already watched for TLS, whose handshake checks the connection too.")
	case d.Kind == proxysvc.WatchTCP && kind == proxysvc.WatchTLS:
		// A probe is the lesser question; watching its certificate takes it
		// over, checked on the next pass.
		if _, err := s.Store.DB.ExecContext(r.Context(),
			`UPDATE watched_endpoints SET kind = 'tls', probe = '', checked_at = 0 WHERE id = ?`, d.ID); err != nil {
			return httpx.Internal(err)
		}
		d.Kind, added = proxysvc.WatchTLS, 1
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	httpx.SetAudit(r, "certificates.watch.add", target.Host, map[string]any{"port": target.Port, "ip": ip, "kind": kind})
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
	var domain, ip string
	var port int
	if err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT domain, port, ip FROM watched_endpoints WHERE id = ?`, id).Scan(&domain, &port, &ip); err != nil {
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
	// from goes too or the endpoint would be back after a restart. Only an
	// endpoint reached by DNS came from there.
	if ip == "" {
		if _, err := tx.ExecContext(r.Context(),
			`DELETE FROM watched_domains WHERE domain = ? AND port = ?`, domain, port); err != nil {
			return httpx.Internal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.watch.remove", domain, map[string]any{"port": port, "ip": ip})
	httpx.NoContent(w)
	return nil
}
