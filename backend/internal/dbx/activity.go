package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Activity is one thing the server is currently doing.
//
// This exists for one moment: the application has gone unresponsive and nobody
// knows whether the database is the cause. Every engine can answer that, and
// none of them answer it the same way — so the shape below is the question
// ("what is running, for how long, and is it stuck behind something else?")
// rather than any one engine's table.
type Activity struct {
	// PID is whatever handle this engine's kill command takes. It is a string
	// because they are not all integers.
	PID      string `json:"pid"`
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
	// State is the engine's own word for what the session is doing; Status is
	// the same thing in the four words every engine shares.
	State  string `json:"state,omitempty"`
	Status string `json:"status,omitempty"`
	// Seconds is how long the statement has been running, and is zero for a
	// session that is running nothing. It used to be the time since the last
	// statement began whatever the state, which made a connection a pool had
	// held idle since morning the longest-running query on the server.
	Seconds float64 `json:"seconds"`
	// IdleSeconds is how long a session that is running nothing has been
	// that way, and TransactionSeconds how long its transaction has been
	// open — the figure that matters for an idle session holding locks.
	IdleSeconds        float64 `json:"idleSeconds,omitempty"`
	TransactionSeconds float64 `json:"transactionSeconds,omitempty"`
	Query              string  `json:"query,omitempty"`
	Client             string  `json:"client,omitempty"`
	// Application is what the client called itself when it connected.
	Application string `json:"application,omitempty"`
	// Wait is what the session is blocked on, where the engine reports it. This
	// is the field that turns "a query is slow" into "a query is waiting for a
	// lock", which are different problems with different fixes.
	Wait string `json:"wait,omitempty"`
	// WaitType and WaitEvent are the two halves of Wait, where the engine
	// reports a class and an event rather than one word.
	WaitType  string `json:"waitType,omitempty"`
	WaitEvent string `json:"waitEvent,omitempty"`
	// BlockedBy names the session holding what this one wants, where the engine
	// can say. It is what makes a pile-up readable: fifty blocked sessions and
	// one culprit.
	BlockedBy string `json:"blockedBy,omitempty"`
	// BlockedByPIDs is BlockedBy as a list of the same handles PID carries.
	// The comma-joined string cannot be split back: Oracle's handle is
	// "sid,serial#", with a comma of its own.
	BlockedByPIDs    []string   `json:"blockedByPids,omitempty"`
	TransactionStart *time.Time `json:"transactionStart,omitempty"`
	QueryStart       *time.Time `json:"queryStart,omitempty"`
	// StateSince is when the session entered its current state.
	StateSince  *time.Time `json:"stateSince,omitempty"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	// Self marks the connection that answered this request.
	//
	// The list deliberately includes the dashboard's own sessions rather than
	// filtering them out. Hiding them showed an operator a partially true
	// picture — a long browse of a big table is the dashboard holding the lock,
	// and a session list that cannot say so sends them looking somewhere else —
	// and it also meant a server with nothing else connected reported an empty
	// table, which reads as "the query is broken" rather than "nothing is
	// running". The flag is per-connection and means exactly what it says: this
	// row is the connection that answered. The pool holds others, and they show
	// up unmarked, which is fine — killing one costs a reconnect that
	// database/sql does silently.
	Self bool `json:"self,omitempty"`
}

// ErrNoActivityView is returned by engines that have no server-side session
// concept at all. It is not a failure — SQLite genuinely has nothing to show —
// so the handler renders it as information rather than an error.
var ErrNoActivityView = fmt.Errorf("this engine has no server-side session list")

func scanActivity(rows *sql.Rows) ([]Activity, error) {
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		// Self arrives as 1/0 rather than a boolean: only Postgres has a real
		// boolean type here, and scanning the other five engines' integer
		// through database/sql's bool conversion depends on each driver's
		// choice of Go type for a one-bit column.
		var self int
		if err := rows.Scan(&a.PID, nullText{&a.User}, nullText{&a.Database}, nullText{&a.State},
			&a.Seconds, nullText{&a.Query}, nullText{&a.Client}, nullText{&a.Wait},
			nullText{&a.BlockedBy}, &self); err != nil {
			return nil, err
		}
		a.Self = self != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// The four things a session can be doing, in every engine's terms at once.
// Blocked is an active session waiting on another session's lock.
const (
	SessionActive            = "active"
	SessionIdle              = "idle"
	SessionIdleInTransaction = "idle_in_transaction"
	SessionBlocked           = "blocked"
	// SessionBackground is a thread the engine runs for itself — a
	// replication thread, an event scheduler — listed among the sessions and
	// not something an application is waiting on.
	SessionBackground = "background"
)

// SessionLister is the optional, richer half of Dialect.Activity: the same
// rows with their state, timings and application. A dialect that implements
// it is asked instead; one that does not still answers through Activity.
type SessionLister interface {
	Sessions(ctx context.Context, db *sql.DB) ([]Activity, error)
}

// ListActivity reports what the server is currently running.
func ListActivity(ctx context.Context, db *sql.DB, driver Driver) ([]Activity, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	var list []Activity
	if s, ok := d.(SessionLister); ok {
		list, err = s.Sessions(ctx, db)
	} else {
		list, err = d.Activity(ctx, db)
	}
	if err != nil {
		return nil, err
	}
	for i := range list {
		finishActivity(&list[i])
	}
	return list, nil
}

// finishActivity fills what follows from the rest of a row, so each dialect
// states only what it read: the blocker list from the joined string, and a
// status for the dialects that report every listed session as running.
func finishActivity(a *Activity) {
	if a.BlockedByPIDs == nil && a.BlockedBy != "" {
		for _, pid := range strings.Split(a.BlockedBy, ",") {
			if pid = strings.TrimSpace(pid); pid != "" {
				a.BlockedByPIDs = append(a.BlockedByPIDs, pid)
			}
		}
	}
	if a.Status == "" {
		a.Status = SessionActive
	}
	if a.Status == SessionActive && len(a.BlockedByPIDs) > 0 {
		a.Status = SessionBlocked
	}
}

// Canceller is the optional dialect half of CancelQuery, for the engines that
// can stop a statement without ending the session it runs in.
type Canceller interface {
	Cancel(ctx context.Context, db *sql.DB, pid string) error
}

// ErrNoCancel is returned by engines whose only way to stop a statement is to
// end its session, which is what KillQuery does.
var ErrNoCancel = fmt.Errorf("this engine cannot stop a statement without ending its session")

// CancelQuery stops the statement a session is running and leaves the session
// connected.
//
// It is the gentler of the two: the statement's own work rolls back and the
// application's connection survives, so its pool does not have to notice.
// What it cannot do is clear a session that is idle inside a transaction —
// there is no statement to cancel, and the locks stay held. That is what
// KillQuery is for.
func CancelQuery(ctx context.Context, db *sql.DB, driver Driver, pid string) error {
	d, err := DialectFor(driver)
	if err != nil {
		return err
	}
	c, ok := d.(Canceller)
	if !ok {
		return ErrNoCancel
	}
	return c.Cancel(ctx, db, pid)
}

// KillQuery terminates a session or statement.
//
// It is destructive in the sense the route map means: something running is
// stopped, and whatever it had done so far rolls back. The handler in front
// demands the capability and pauses on a confirmation naming the session, but
// asks for no phrase to be typed — this is pressed repeatedly under exactly the
// time pressure that makes a typing exercise counterproductive, and the rollback
// means nothing is lost that was not already going.
func KillQuery(ctx context.Context, db *sql.DB, driver Driver, pid string) error {
	d, err := DialectFor(driver)
	if err != nil {
		return err
	}
	return d.Kill(ctx, db, pid)
}

// pidRe bounds what can reach a kill statement. None of these engines can bind
// a session id as a parameter in their kill syntax, so the value is checked
// rather than escaped — and a session id is always digits or, on ClickHouse, a
// query UUID.
func validatePID(pid string) error {
	if pid == "" {
		return fmt.Errorf("a session id is required")
	}
	if len(pid) > 64 {
		return fmt.Errorf("session id is too long")
	}
	for _, r := range pid {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') && r != '-' {
			return fmt.Errorf("session id %q is not a number or uuid", pid)
		}
	}
	return nil
}
