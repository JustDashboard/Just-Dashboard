package api

import (
	"context"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbtls"
)

const (
	// dbTLSCheckEvery is how often certificates are looked at. A certificate
	// is renewed a month before it ends, so a day's resolution is generous; six
	// hours is what lets a server whose public address changed be named in its
	// certificate the same day.
	dbTLSCheckEvery = 6 * time.Hour
	// dbTLSFirstCheck leaves the engines time to come up after the dashboard
	// does, since a server that is down cannot be asked to read a certificate.
	dbTLSFirstCheck = 2 * time.Minute
)

// startDatabaseTLS runs the renewal. Certificates are renewed here, in the
// background, rather than when somebody opens the page, because the operator
// who most needs a certificate renewed is the one who has not looked at the
// dashboard in a year.
func (s *Server) startDatabaseTLS(ctx context.Context) {
	ctx, s.dbTLS.stop = context.WithCancel(ctx)
	s.dbTLS.done = make(chan struct{})
	go func() {
		defer close(s.dbTLS.done)
		s.mendDBTLSAuthorityFile(ctx)
		wait := dbTLSFirstCheck
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			s.renewDueDBTLS(ctx)
			wait = dbTLSCheckEvery
		}
	}()
}

func (s *Server) stopDatabaseTLS() {
	if s.dbTLS.stop != nil {
		s.dbTLS.stop()
		<-s.dbTLS.done
	}
}

// mendDBTLSAuthorityFile writes the authority's certificate to the file the
// saved connections verify against, so a data directory restored without it
// still opens its connections.
func (s *Server) mendDBTLSAuthorityFile(ctx context.Context) {
	authority, err := s.readDBTLSAuthority(ctx)
	if err != nil || authority == nil {
		return
	}
	if _, err := s.dbTLSCAFile(authority.CertPEM); err != nil {
		s.Log.Warn("the database certificate authority's file could not be written", "err", err)
	}
}

// renewDueDBTLS renews every certificate that is inside its renewal window, or
// that no longer names the addresses its server is reached at.
func (s *Server) renewDueDBTLS(ctx context.Context) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT connection_id, not_after FROM db_server_tls ORDER BY connection_id`)
	if err != nil {
		s.Log.Warn("database certificates could not be listed", "err", err)
		return
	}
	type due struct {
		id       int64
		notAfter time.Time
	}
	var all []due
	for rows.Next() {
		var (
			d        due
			notAfter int64
		)
		if err := rows.Scan(&d.id, &notAfter); err != nil {
			rows.Close()
			return
		}
		d.notAfter = time.Unix(notAfter, 0)
		all = append(all, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, d := range all {
		if ctx.Err() != nil {
			return
		}
		reason := s.dbTLSRenewalReason(ctx, d.id, d.notAfter)
		if reason == "" {
			continue
		}
		step, cancel := context.WithTimeout(ctx, 5*time.Minute)
		name, detail, err := s.renewDBTLS(step, d.id, reason)
		cancel()
		if name == "" {
			// Nothing to name: the connection or its server is not there, and the
			// next pass looks again.
			continue
		}
		entry := audit.Entry{Actor: "system", Action: "database.tls.renew", Target: name, Success: err == nil}
		if err != nil {
			detail["error"] = err.Error()
			s.Log.Warn("a database certificate could not be renewed", "connection", name, "err", err)
		}
		entry.Detail = audit.Detail(detail)
		s.Audit.Record(ctx, entry)
	}
}

// dbTLSRenewalReason says why a certificate is due: "expiry", "names", or empty
// when it is not.
func (s *Server) dbTLSRenewalReason(ctx context.Context, id int64, notAfter time.Time) string {
	if dbtls.NeedsRenewal(notAfter, time.Now()) {
		return "expiry"
	}
	row, err := s.dbTLSRow(ctx, id)
	if err != nil || row == nil {
		return ""
	}
	t, reason, err := s.resolveDBTLS(ctx, id)
	if err != nil || reason != "" {
		return ""
	}
	if !dbtls.SameNames(row.Names, dbTLSNames(t)) {
		return "names"
	}
	return ""
}
