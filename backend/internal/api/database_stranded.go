package api

import (
	"context"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Repairing connections sealed under another master key.
//
// A reinstall that keeps /var/lib/just-dashboard but not the .env beside the
// checkout starts on a new JD_MASTER_KEY, and every connection string in the
// store was sealed under the old one. The rows are still listed, as ones that
// cannot be opened, and nothing matched them to the servers they pointed at —
// so the server was found again as if it had never been connected, and
// connecting it made a second row, `postgres-1-2`, beside the first, which
// stayed red with the backup jobs and deployments that name it.
//
// For a connection made from a found server the secret was never something
// the operator had to keep: it was read from the container, or the server
// asks for none, and it can be read again. Such a row is repaired in place,
// keeping its id, whenever the server it came from is signed in to — by
// connecting it, by the reconcile, and by a pass in the background that does
// not wait for anybody to open the page. A connection typed by hand has
// nothing to read its password from again, and is left to the operator.

const (
	// strandedFirstCheck leaves the engines time to come up after the
	// dashboard does: after a reboot they start together.
	strandedFirstCheck = time.Minute
	// strandedCheckEvery is how often a row that could not be repaired yet is
	// looked at again, for a server that has started since.
	strandedCheckEvery = 30 * time.Minute
)

// strandedConnection is a connection made from a found server whose DSN this
// install cannot unseal.
type strandedConnection struct {
	id     int64
	name   string
	driver dbx.Driver
}

// strandedConnections lists them by the key of the server each was made from.
// A server two of them were made from is left out: they were two accounts or
// two databases, and nothing says which one a new sign-in stands for.
func (s *Server) strandedConnections(ctx context.Context) (map[string]strandedConnection, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT id, name, driver, dsn_enc, origin FROM db_connections WHERE origin <> '' ORDER BY id`)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	out := map[string]strandedConnection{}
	several := map[string]bool{}
	for rows.Next() {
		var (
			row                    strandedConnection
			driver, sealed, origin string
		)
		if err := rows.Scan(&row.id, &row.name, &driver, &sealed, &origin); err != nil {
			return nil, httpx.Internal(err)
		}
		if _, err := s.Sealer.Open(sealed); err == nil {
			continue
		}
		row.driver = dbx.Driver(driver)
		if _, seen := out[origin]; seen {
			several[origin] = true
		}
		out[origin] = row
	}
	if err := rows.Err(); err != nil {
		return nil, httpx.Internal(err)
	}
	for origin := range several {
		delete(out, origin)
	}
	return out, nil
}

// strandedFrom is the one stranded connection made from a server, for the
// engine it runs.
func (s *Server) strandedFrom(ctx context.Context, origin string, driver dbx.Driver) (strandedConnection, bool, error) {
	if origin == "" {
		return strandedConnection{}, false, nil
	}
	all, err := s.strandedConnections(ctx)
	if err != nil {
		return strandedConnection{}, false, err
	}
	row, ok := all[origin]
	return row, ok && row.driver == driver, nil
}

func (s *Server) startStrandedRepair(ctx context.Context) {
	ctx, s.dbStrandedStop = context.WithCancel(ctx)
	s.dbStrandedDone = make(chan struct{})
	go func() {
		defer close(s.dbStrandedDone)
		wait := strandedFirstCheck
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			s.repairStranded(ctx)
			wait = strandedCheckEvery
		}
	}()
}

func (s *Server) stopStrandedRepair() {
	if s.dbStrandedStop != nil {
		s.dbStrandedStop()
		<-s.dbStrandedDone
	}
}

// repairStranded signs in to the server each stranded connection was made
// from, with what that server states, and seals what let it in into the row.
//
// It asks only what the reconcile would ask, and remembers a refusal the way
// the reconcile does: a server that turned the stated password down is not
// sent it again every half hour.
func (s *Server) repairStranded(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stranded, err := s.strandedConnections(ctx)
	if err != nil {
		s.Log.Warn("connections sealed under another master key could not be listed", "err", err)
		return
	}
	if len(stranded) == 0 {
		return
	}
	inv, _ := s.buildInventory(ctx, true, dbFileScanResult{})
	conns, _, err := s.savedConnections(ctx)
	if err != nil {
		return
	}
	dbx.AttachConnections(inv.Instances, conns)
	ignored, err := s.ignoredOrigins(ctx)
	if err != nil {
		return
	}
	attempts := []*syncAttempt{}
	for i := range inv.Instances {
		inst := &inv.Instances[i]
		row, ok := stranded[inst.Key]
		// A server another connection already opens was connected again
		// before this ran; repairing the old row as well would make two.
		if !ok || row.driver != inst.Driver || len(inst.Connections) > 0 || ignored[inst.Key] {
			continue
		}
		access, ok := inv.Access(inst.Key)
		if !ok || !signsInUnasked(inst, access) {
			continue
		}
		attempt := &syncAttempt{inst: inst, access: access, fingerprint: signInFingerprint(inst, access)}
		if _, refused := s.refusedBefore(inst.Key, attempt.fingerprint); refused {
			continue
		}
		attempts = append(attempts, attempt)
	}
	s.signInAll(ctx, attempts)
	repaired := false
	for _, attempt := range attempts {
		if attempt.dsn == "" {
			s.noteRefusal(attempt)
			continue
		}
		s.forgetRefusal(attempt.inst.Key)
		row := stranded[attempt.inst.Key]
		if err := s.resealDSN(ctx, row.id, row.driver, attempt.dsn); err != nil {
			s.Log.Warn("a connection sealed under another master key could not be repaired",
				"connection", row.name, "err", err)
			continue
		}
		repaired = true
		s.Audit.Record(ctx, audit.Entry{
			Actor: "system", Action: "database.connection.repair", Target: row.name, Success: true,
			Detail: audit.Detail(map[string]any{
				"origin": attempt.inst.Key, "driver": row.driver, "user": attempt.access.Candidate.User,
			}),
		})
	}
	if repaired {
		s.dropInventory()
	}
}

// signsInUnasked reports a running server the dashboard may sign in to with
// nothing but what the machine states: a container that states its
// credentials, or a server installed here that ships with none. It is the
// reconcile's rule, for a caller that only needs the answer.
func signsInUnasked(inst *dbx.Instance, access dbx.Access) bool {
	if inst.Kind != dbx.KindServer || inst.Driver == "" || inst.State != dbx.StateRunning || !inst.Connectable {
		return false
	}
	if inst.Source == dbx.SourceHost {
		return !access.Candidate.NeedsCredentials && dbx.BuildDSN(access.Candidate, "") != ""
	}
	if inst.Confidence == dbx.ConfidencePort {
		return false
	}
	switch inst.Credentials {
	case dbx.CredentialsEnv, dbx.CredentialsArgs, dbx.CredentialsOpen, dbx.CredentialsSecretFile:
		return true
	}
	return false
}
