package dbx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

// A failure is one of two things to the person looking at it. Either the
// server was not there to answer — refused, timed out, gone in the middle of
// a reply, still starting — or it was there and said no. The first is worth
// asking again: the server is coming up, the network blinked, the page was
// opened in the minute the container was restarting. The second fails the
// same way however often it is asked: a wrong password, a database that does
// not exist, a connection string nobody could parse, a privilege the account
// lacks.
//
// Unreachable tells the two apart, for every engine, so that the page offers
// "Try again" for the first kind and does not offer it for the second.
//
// It reads the error's own type wherever a driver kept one: a refusal the
// engine sent is recognised as the engine's, and is unreachable only for the
// few codes that mean "not now" rather than "no". A transport error is
// recognised as the transport's. Only what is left — a driver that flattened
// its dial error into a sentence — is read by its words.

// Unreachable reports whether a failure is the server not answering rather
// than the server answering no.
func Unreachable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		// A request that was abandoned says nothing about the server.
		return false
	}
	if refused, notNow := engineSaid(err); refused {
		return notNow
	}
	text := strings.ToLower(err.Error())
	for _, said := range refusedWords {
		if strings.Contains(text, said) {
			return false
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return true
	}
	for _, gone := range []syscall.Errno{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE, syscall.ETIMEDOUT,
		syscall.EHOSTUNREACH, syscall.ENETUNREACH,
	} {
		if errors.Is(err, gone) {
			return true
		}
	}
	var lookup *net.DNSError
	if errors.As(err, &lookup) {
		// A name that does not exist will not exist on the next try either; a
		// resolver that did not answer may.
		return !lookup.IsNotFound
	}
	var op *net.OpError
	if errors.As(err, &op) && (op.Op == "remote error" || op.Op == "local error") {
		// A TLS alert: the two ends disagree about how to talk, and will
		// again.
		return false
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	if mongo.IsNetworkError(err) || errors.Is(err, mongo.ErrClientDisconnected) {
		return true
	}
	for _, said := range unreachableWords {
		if strings.Contains(text, said) {
			return true
		}
	}
	return false
}

// refusedWords mark a failure as the server's answer where no type says so:
// MongoDB's driver reports a wrong password as a server it could not select,
// and a certificate nobody trusts arrives as a failed dial.
var refusedWords = []string{
	"auth error", "authentication failed", "authenticationfailed", "password authentication",
	"access denied", "login failed", "wrongpass", "noauth", "certificate", "x509:",
}

// unreachableWords are how a driver that kept no type says the server was not
// there.
var unreachableWords = []string{
	"connection refused", "connection reset", "broken pipe", "i/o timeout", "no route to host",
	"network is unreachable", "connection timed out", "unexpected eof", "bad connection",
	"server selection error", "use of closed network connection",
}

// oracleCode finds the error number in what go-ora prints.
var oracleCode = regexp.MustCompile(`ORA-([0-9]{5})`)

// engineSaid recognises an error the engine itself sent. notNow reports
// that what it said is that it cannot answer at the moment: it is starting or
// stopping, loading its data, or out of connections.
func engineSaid(err error) (refused, notNow bool) {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		// PostgreSQL: shutting down, crashed, starting up or in recovery, out
		// of connections, and the connection exceptions.
		case "57P01", "57P02", "57P03", "53300", "08000", "08001", "08003", "08006":
			return true, true
		}
		return true, false
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		switch my.Number {
		// Too many connections, server shutdown in progress, aborted
		// connection.
		case 1040, 1053, 1152:
			return true, true
		}
		return true, false
	}
	var ms mssql.Error
	if errors.As(err, &ms) {
		switch ms.Number {
		// Shutdown in progress, the server is paused, it is still upgrading
		// its databases after a start, and the transport-level numbers the
		// server reports for a connection that went away.
		case 6005, 17142, 18401, 233, 10053, 10054, 10060, 10061:
			return true, true
		}
		return true, false
	}
	var ch *clickhouse.Exception
	if errors.As(err, &ch) {
		switch ch.Code {
		// TOO_MANY_SIMULTANEOUS_QUERIES, NO_FREE_CONNECTION, SOCKET_TIMEOUT,
		// NETWORK_ERROR.
		case 202, 203, 209, 210:
			return true, true
		}
		return true, false
	}
	var command mongo.CommandError
	if errors.As(err, &command) {
		switch command.Code {
		// HostUnreachable, HostNotFound, NetworkTimeout, ShutdownInProgress,
		// PrimarySteppedDown, SocketException, NotWritablePrimary,
		// InterruptedAtShutdown, InterruptedDueToReplStateChange,
		// NotPrimaryNoSecondaryOk, NotPrimaryOrSecondary: the driver's own
		// list of what is worth another attempt.
		case 6, 7, 89, 91, 189, 9001, 10107, 11600, 11602, 13435, 13436:
			return true, true
		}
		return true, command.HasErrorLabel("NetworkError")
	}
	var document mongo.ServerError
	if errors.As(err, &document) {
		return true, document.HasErrorLabel("NetworkError")
	}
	var replied redis.Error
	if errors.As(err, &replied) {
		word, _, _ := strings.Cut(replied.Error(), " ")
		switch word {
		// Loading its data after a start, busy running a script, and a
		// cluster or a replica that is between states.
		case "LOADING", "BUSY", "TRYAGAIN", "CLUSTERDOWN", "MASTERDOWN":
			return true, true
		}
		return true, false
	}
	if found := oracleCode.FindStringSubmatch(err.Error()); found != nil {
		switch code, _ := strconv.Atoi(found[1]); code {
		// Starting up or shutting down, not available, shutdown in progress,
		// the channel closed, no listener, a listener that does not know the
		// service yet, the instance refusing new connections, and the
		// connection closing or timing out.
		case 1033, 1034, 1089, 1090, 3113, 3114, 3135, 12170, 12514, 12528, 12537, 12541, 12543:
			return true, true
		}
		return true, false
	}
	return false, false
}
