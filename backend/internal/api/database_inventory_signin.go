package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// Remembering a server that would not be signed in to.
//
// The reconcile runs every time somebody opens the Databases section, and it
// signs in to whatever it finds that is not connected yet. A container whose
// volume was initialised under another password refuses what its environment
// states — and refused it again on the next page load, and the one after: a
// failed login in the server's log per visit, from a dashboard nobody asked to
// try. That is the hazard the host half was already written around, and a
// container's server keeps the same log.
//
// So a refusal is remembered against exactly what was tried. The same server
// offered the same credentials is not asked again until one of them changes —
// the container is recreated, its environment is edited — or the operator
// connects it by hand. The memory is the process's own: a restart asks once
// more, which is the least a fresh start can do.
//
// A server that did not answer at all is a different thing. One that is still
// starting refuses the connection, not the password, and has to be tried
// again or it would never be connected; it is left alone only briefly.

// dbUnansweredRetry is how long a server that did not answer is left alone.
const dbUnansweredRetry = 2 * time.Minute

// syncDialWorkers is how many servers the reconcile signs in to at once. An
// address that drops packets costs the full dial timeout, and a handful of
// those one after another used up the request.
const syncDialWorkers = 4

type refusedSignIn struct {
	fingerprint string
	// says is what the reconcile reported, with the engine's own words in it,
	// kept so it can go on saying why without asking again.
	says string
	// lasting marks the engine refusing who asked, which stands until what was
	// tried changes. Anything else — no answer, a password file that could
	// not be read — is worth another try after a while.
	lasting bool
	at      time.Time
}

// signInFingerprint names what a sign-in would send and to what, without
// keeping any of it: the server's identity as Docker or the kernel gives it,
// the address, the account and the secret. A password kept in a file is named
// by the file, so a server that refused it is not the reason to read the file
// out of its container again.
func signInFingerprint(inst *dbx.Instance, access dbx.Access) string {
	identity := ""
	switch {
	case inst.Container != nil:
		identity = inst.Container.ID
	case inst.Host != nil:
		identity = strconv.Itoa(int(inst.Host.PID))
	}
	c := access.Candidate
	sum := sha256.New()
	for _, part := range []string{
		identity, string(c.Driver), c.Host, strconv.Itoa(c.Port), c.User, c.Database, c.AuthSource, c.SSLMode,
		access.Password, access.SecretFile,
	} {
		sum.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// refusedBefore reports a sign-in that was tried as it stands and failed, and
// is not due another try.
func (s *Server) refusedBefore(key, fingerprint string) (refusedSignIn, bool) {
	s.dbInventory.mu.Lock()
	defer s.dbInventory.mu.Unlock()
	kept, ok := s.dbInventory.refused[key]
	if !ok || kept.fingerprint != fingerprint {
		return refusedSignIn{}, false
	}
	if !kept.lasting && time.Since(kept.at) > dbUnansweredRetry {
		return refusedSignIn{}, false
	}
	return kept, true
}

func (s *Server) rememberRefusal(key, fingerprint, says string, lasting bool) {
	s.dbInventory.mu.Lock()
	defer s.dbInventory.mu.Unlock()
	if s.dbInventory.refused == nil {
		s.dbInventory.refused = map[string]refusedSignIn{}
	}
	s.dbInventory.refused[key] = refusedSignIn{fingerprint: fingerprint, says: says, lasting: lasting, at: time.Now()}
}

// forgetRefusal drops what is remembered about one instance: it was signed in
// to, or the operator is about to try it by hand.
func (s *Server) forgetRefusal(key string) {
	s.dbInventory.mu.Lock()
	delete(s.dbInventory.refused, key)
	s.dbInventory.mu.Unlock()
}

// pruneRefusals forgets the instances that are no longer on the machine, so
// the memory is bounded by what is.
func (s *Server) pruneRefusals(present map[string]bool) {
	s.dbInventory.mu.Lock()
	defer s.dbInventory.mu.Unlock()
	for key := range s.dbInventory.refused {
		if !present[key] {
			delete(s.dbInventory.refused, key)
		}
	}
}

// refusalPhrases are how each engine words a refusal of who is asking. They
// are matched in the engine's answer only where its driver has no code to
// read instead.
var refusalPhrases = []string{
	// PostgreSQL and what speaks its protocol.
	"password authentication failed", "authentication failed", "no pg_hba.conf entry",
	// MySQL and MariaDB.
	"access denied",
	// Redis and its relatives.
	"wrongpass", "noauth", "invalid password", "invalid username-password",
	// MongoDB.
	"requires authentication", "authenticationfailed", "auth error", "not authorized", "unauthorized",
	// SQL Server.
	"login failed",
	// ClickHouse answers "Authentication failed", matched above.
	// Oracle: invalid username/password, and a locked account.
	"ora-01017", "ora-28000",
}

// startingPhrases are a server saying it is not ready yet in words that would
// otherwise read as a refusal: SQL Server answers "Login failed … Server is in
// script upgrade mode" to the right password while it starts.
var startingPhrases = []string{"script upgrade mode", "starting up", "not yet accepting connections"}

// credentialRefusal reports an error that is a server refusing the account or
// the password, as opposed to one that did not answer, is still starting, or
// was asked for a database it does not have. Only the first is worth
// remembering for good, and only the first is fairly described as "did not
// accept the credentials".
func credentialRefusal(err error) bool {
	if err == nil {
		return false
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		// Class 28 is invalid authorization specification.
		return strings.HasPrefix(pg.Code, "28")
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		// Access denied for the account, for its host, or with no password.
		return my.Number == 1045 || my.Number == 1130 || my.Number == 1698
	}
	text := strings.ToLower(err.Error())
	for _, phrase := range startingPhrases {
		if strings.Contains(text, phrase) {
			return false
		}
	}
	for _, phrase := range refusalPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// refusalReason words why a container's server was not connected by the
// reconcile.
func refusalReason(err error) string {
	if credentialRefusal(err) {
		return "it did not accept the credentials its container states (" + err.Error() +
			") — connect it with the password it actually uses"
	}
	return "it did not answer a sign-in (" + err.Error() + ") — it may still be starting, and is tried again shortly"
}
