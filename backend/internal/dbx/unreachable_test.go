package dbx

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	"go.mongodb.org/mongo-driver/mongo"
)

// A server that was not there to answer is worth asking again; one that was
// there and said no is not. Each engine's own way of saying either is read.
func TestUnreachableTellsAServerThatIsGoneFromOneThatSaidNo(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"nothing", nil, false},
		{"a request that was abandoned", fmt.Errorf("ping: %w", context.Canceled), false},
		{"a deadline", fmt.Errorf("ping: %w", context.DeadlineExceeded), true},
		{"a refused dial", fmt.Errorf("failed to connect: %w", refused), true},
		{"a reset mid-reply", fmt.Errorf("read: %w", syscall.ECONNRESET), true},
		{"a connection that ended", io.ErrUnexpectedEOF, true},
		{"a pool's dead connection", driver.ErrBadConn, true},
		{"a socket that is not there", &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.ENOENT)}, true},
		{"a resolver that did not answer", &net.DNSError{Err: "server misbehaving", Name: "db.internal", IsTemporary: true}, true},
		{"a name that does not exist", &net.DNSError{Err: "no such host", Name: "db.nonesuch", IsNotFound: true}, false},
		{"a TLS alert", &net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}, false},
		{"a certificate nobody trusts", errors.New("x509: certificate signed by unknown authority"), false},
		{"a connection string nobody could read", errors.New("the connection string is not a Redis URL"), false},

		{"postgres starting up", &pgconn.PgError{Code: "57P03", Message: "the database system is starting up"}, true},
		{"postgres out of connections", &pgconn.PgError{Code: "53300"}, true},
		{"postgres wrong password", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}, false},
		{"postgres no such database", fmt.Errorf("connect: %w", &pgconn.PgError{Code: "3D000"}), false},
		{"postgres permission denied", &pgconn.PgError{Code: "42501"}, false},

		{"mysql too many connections", &mysql.MySQLError{Number: 1040}, true},
		{"mysql shutting down", &mysql.MySQLError{Number: 1053}, true},
		{"mysql access denied", &mysql.MySQLError{Number: 1045, Message: "Access denied for user"}, false},
		{"mysql unknown database", &mysql.MySQLError{Number: 1049}, false},

		{"sql server still upgrading after a start", mssql.Error{Number: 18401}, true},
		{"sql server login failed", mssql.Error{Number: 18456, Message: "Login failed for user 'sa'."}, false},
		{"sql server dial, as its driver words it",
			errors.New("unable to open tcp connection with host '127.0.0.1:51433': dial tcp 127.0.0.1:51433: connect: connection refused"), true},

		{"clickhouse network error", &clickhouse.Exception{Code: 210}, true},
		{"clickhouse wrong password", &clickhouse.Exception{Code: 516, Message: "Authentication failed"}, false},

		{"redis loading its data", redisServerError("LOADING Redis is loading the dataset in memory"), true},
		{"redis busy with a script", redisServerError("BUSY Redis is busy running a script"), true},
		{"redis wrong password", redisServerError("WRONGPASS invalid username-password pair or user is disabled."), false},
		{"redis a command it refuses", redisServerError("ERR unknown command 'NOPE'"), false},

		{"mongodb no server to select",
			errors.New("server selection error: context deadline exceeded, current topology: { Type: Unknown, Servers: [{ Addr: 127.0.0.1:1, Type: Unknown, Last error: dial tcp 127.0.0.1:1: connect: connection refused }, ] }"), true},
		{"mongodb wrong password, which its driver reports the same way",
			errors.New("server selection error: context deadline exceeded, current topology: { Type: Unknown, Servers: [{ Addr: 127.0.0.1:27017, Type: Unknown, Last error: connection() error occurred during connection handshake: auth error: sasl conversation error: unable to authenticate using mechanism \"SCRAM-SHA-256\": (AuthenticationFailed) Authentication failed. }, ] }"), false},
		{"mongodb shutting down", mongo.CommandError{Code: 91, Name: "ShutdownInProgress"}, true},
		{"mongodb a command it refuses", mongo.CommandError{Code: 13, Name: "Unauthorized"}, false},
		{"mongodb a write it refuses", mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 11000}}}, false},

		{"oracle starting up", errors.New("ORA-01033: ORACLE initialization or shutdown in progress"), true},
		{"oracle no listener", errors.New("ORA-12541: TNS:no listener"), true},
		{"oracle wrong password", errors.New("ORA-01017: invalid username/password; logon denied"), false},
		{"oracle a statement it refuses", errors.New("ORA-00942: table or view does not exist"), false},
	} {
		if got := Unreachable(c.err); got != c.want {
			t.Errorf("%s: Unreachable(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}
