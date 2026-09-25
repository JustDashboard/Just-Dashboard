package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Making an account on a database installed on the machine itself, from the
// machine itself.
//
// A native Postgres or MySQL keeps its passwords where no amount of reading
// the process reveals them, so `POST /databases/host` asks for one. But an
// operator who installed the server with apt an hour ago often has no
// password to give: both engines ship authenticating local connections by
// the operating-system account instead, so `postgres` and `root` are
// reachable from a shell on the host and from nowhere else.
//
// This dashboard has that shell. hostexec already crosses into the host's
// namespaces for nginx and PM2, and the same crossing runs `psql` as the
// host's `postgres` account over its Unix socket, where peer authentication
// lets it in without a password. From there one statement makes (or resets)
// an account with a password this process generated and sealed — and the
// saved connection dials TCP with it like every other connection here.
//
// The privilege this spends is the one the dashboard already holds: root on
// the host, through the Docker socket and /host. What it adds is an audited
// route that spends it on purpose, with the account it made named in the
// entry. The password is generated here unless the operator supplies one,
// never shown, and reaches the engine as one argv element of a command that
// runs without a shell.

type hostGrantRequest struct {
	Driver dbx.Driver `json:"driver"`
	Host   string     `json:"host"`
	Port   int        `json:"port"`
	// User is the account to create or reset; empty picks the dashboard's own.
	User string `json:"user"`
	// Password is the one to set; empty generates one.
	Password string `json:"password"`
	Database string `json:"database"`
	Name     string `json:"name"`
	// Superuser makes the account able to manage the server from here —
	// roles, databases, extensions — which is what the Server page needs.
	// Off makes an ordinary login that owns nothing yet.
	Superuser *bool `json:"superuser"`
}

const dashboardDBAccount = "just_dashboard"

func (s *Server) handleDBHostGrant(w http.ResponseWriter, r *http.Request) error {
	var req hostGrantRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Driver.Valid() {
		return httpx.BadRequest("driver must be one of %s", driverNames())
	}
	host := strings.TrimSpace(req.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	if !databaseLoopback(host) {
		return httpx.BadRequest("only a server on this machine can be set up this way")
	}
	if req.Port <= 0 || req.Port > 65535 {
		return httpx.BadRequest("a port is required")
	}
	account := strings.TrimSpace(req.User)
	if account == "" {
		account = dashboardDBAccount
	}
	if err := validateRoleName(account); err != nil {
		return httpx.BadRequest("%v", err)
	}
	password := req.Password
	if password == "" {
		generated, err := generatePassword()
		if err != nil {
			return httpx.Internal(err)
		}
		password = generated
	} else {
		for _, c := range password {
			if c < 0x20 || c == 0x7f {
				return httpx.BadRequest("the password contains a control character")
			}
		}
	}
	superuser := req.Superuser == nil || *req.Superuser

	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	var (
		out   string
		err   error
		cand  = dbx.Candidate{Driver: req.Driver, Source: dbx.SourceHost, Host: host, Port: req.Port, User: account}
		audit = map[string]any{"driver": string(req.Driver), "port": req.Port, "user": account, "superuser": superuser}
	)
	switch req.Driver {
	case dbx.DriverPostgres:
		cand.Database = firstNonEmpty(strings.TrimSpace(req.Database), "postgres")
		out, err = hostPostgresAccount(ctx, req.Port, account, password, superuser)
	case dbx.DriverMySQL:
		cand.Database = strings.TrimSpace(req.Database)
		out, err = hostMySQLAccount(ctx, req.Port, account, password, superuser)
	case dbx.DriverMongo:
		cand.Database = firstNonEmpty(strings.TrimSpace(req.Database), "admin")
		out, err = hostMongoAccount(ctx, req.Port, account, password, superuser)
	case dbx.DriverClickHouse:
		cand.Database = firstNonEmpty(strings.TrimSpace(req.Database), "default")
		out, err = hostClickHouseAccount(ctx, req.Port, account, password, superuser)
	case dbx.DriverRedis:
		// Redis has one password, not accounts: it is in the server's own
		// configuration file, which root on this machine may read.
		cand.Database = firstNonEmpty(strings.TrimSpace(req.Database), "0")
		cand.User = ""
		if req.Password == "" {
			found, ok := hostRedisPassword()
			if !ok {
				err = fmt.Errorf("no requirepass in the server's configuration; paste the password instead")
			}
			password = found
		}
	default:
		return httpx.BadRequest("%s on the host has to be connected with a password it already has", req.Driver)
	}
	if err != nil {
		audit["ok"], audit["error"] = false, err.Error()
		httpx.SetAudit(r, "database.connection.host.grant", host, audit)
		return httpx.Err(http.StatusBadGateway, "host_command_failed", err.Error())
	}
	_ = out
	if req.Driver == dbx.DriverRedis && password == "" {
		// An open Redis: connect as it is.
		password = ""
	}

	dsn := dbx.BuildDSN(cand, password)
	if dsn == "" {
		return httpx.BadRequest("no connection string could be built for %s", req.Driver)
	}
	if err := s.probeConnection(ctx, req.Driver, dsn); err != nil {
		audit["ok"], audit["error"] = false, err.Error()
		httpx.SetAudit(r, "database.connection.host.grant", host, audit)
		return httpx.Err(http.StatusBadGateway, "connect_failed",
			fmt.Sprintf("the account was set up but a connection over TCP was refused: %v", err))
	}

	existing, err := s.existingDSNs(ctx)
	if err != nil {
		return err
	}
	if have, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
		// A connection to this server already exists — with a password that
		// may no longer work. Re-seal it with the one that does.
		conn, _, err := s.connectionByName(ctx, have)
		if err != nil {
			return err
		}
		contained, err := s.containDSN(req.Driver, dsn)
		if err != nil {
			return err
		}
		sealed, err := s.Sealer.Seal(contained)
		if err != nil {
			return httpx.Internal(err)
		}
		if _, err := s.Store.DB.ExecContext(ctx, `UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, conn.ID); err != nil {
			return httpx.Internal(err)
		}
		s.modules.dbs.Close(conn.ID)
		conn, _, err = s.dbConnRow(ctx, conn.ID)
		if err != nil {
			return err
		}
		audit["ok"], audit["connection"] = true, conn.Name
		httpx.SetAudit(r, "database.connection.host.grant", host, audit)
		httpx.JSON(w, http.StatusOK, conn)
		return nil
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = dbx.HostConnectionName(cand)
	}
	if !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	name = uniqueConnectionName(name, existing)
	audit["ok"] = true
	return s.saveConnection(w, r, name, req.Driver, dsn, "database.connection.host.grant", audit)
}

// validateRoleName is the identifier rule every engine here accepts for an
// account: letters, digits and underscores, starting with a letter.
func validateRoleName(name string) error {
	if name == "" || len(name) > 63 {
		return fmt.Errorf("an account name of up to 63 characters is required")
	}
	for i, r := range name {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		digit := r >= '0' && r <= '9'
		if !letter && !(digit && i > 0) {
			return fmt.Errorf("account name %q may contain letters, digits and underscores", name)
		}
	}
	return nil
}

// sqlLiteral quotes a value the SQL way — doubled single quotes — for the
// statements below, which run through a client program that takes the whole
// statement as one argument and cannot bind.
func sqlLiteral(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

// hostAccount finds an operating-system account in the host's passwd, which
// is mounted into this container at /etc. Looked up by hand rather than
// through os/user, which would answer from this image's own passwd where the
// account does not exist.
func hostAccount(name string) (*user.User, error) {
	for _, path := range []string{"/host/etc/passwd", "/etc/passwd"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) != 7 || fields[0] != name {
				continue
			}
			if _, err := strconv.ParseUint(fields[2], 10, 32); err != nil {
				continue
			}
			return &user.User{Username: fields[0], Uid: fields[2], Gid: fields[3], HomeDir: fields[5]}, nil
		}
	}
	return nil, fmt.Errorf("no %s account on this server", name)
}

// runHostCmd runs a prepared host command and hands back its output, or the
// last line the program wrote to stderr — which for psql and mysql is the
// engine's own refusal, the sentence the operator needs.
func runHostCmd(ctx context.Context, cmd *exec.Cmd) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", lastLine(msg))
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("the command did not finish in time")
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// hostPostgresAccount runs one statement as the host's postgres account over
// the Unix socket, which peer authentication admits without a password.
func hostPostgresAccount(ctx context.Context, port int, account, password string, superuser bool) (string, error) {
	if !hostexec.Available("psql") {
		return "", fmt.Errorf("psql is not installed on this server, so the account cannot be made from here; set a password from a shell instead")
	}
	pg, err := hostAccount("postgres")
	if err != nil {
		return "", err
	}
	privileges := "LOGIN"
	if superuser {
		privileges += " SUPERUSER CREATEDB CREATEROLE"
	}
	// A DO block so one round trip creates or resets. The role name is an
	// identifier (format %I) and the password a literal (%L), quoted by the
	// server itself rather than by string concatenation here.
	stmt := fmt.Sprintf(`DO $jd$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %[1]s) THEN
    EXECUTE format('ALTER ROLE %%I WITH %[3]s PASSWORD %%L', %[1]s, %[2]s);
  ELSE
    EXECUTE format('CREATE ROLE %%I WITH %[3]s PASSWORD %%L', %[1]s, %[2]s);
  END IF;
END $jd$;`, sqlLiteral(account), sqlLiteral(password), privileges)
	args := []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "-p", strconv.Itoa(port), "-d", "postgres", "-c", stmt}
	cmd, err := hostexec.CommandOnHostAsUser(ctx, pg, []string{"PGCONNECT_TIMEOUT=10"}, "psql", args...)
	if err != nil {
		return "", err
	}
	return runHostCmd(ctx, cmd)
}

// hostMySQLAccount runs as root over the socket, which auth_socket admits.
// The account is made for both spellings of the local host, because a TCP
// client from 127.0.0.1 matches 'localhost' only when name resolution is on.
func hostMySQLAccount(ctx context.Context, port int, account, password string, superuser bool) (string, error) {
	client := "mysql"
	if !hostexec.Available(client) {
		client = "mariadb"
		if !hostexec.Available(client) {
			return "", fmt.Errorf("neither mysql nor mariadb client is installed on this server; set a password from a shell instead")
		}
	}
	var stmts []string
	for _, host := range []string{"localhost", "127.0.0.1"} {
		acct := "'" + account + "'@'" + host + "'"
		stmts = append(stmts,
			"CREATE USER IF NOT EXISTS "+acct+" IDENTIFIED BY "+mysqlLiteral(password)+";",
			"ALTER USER "+acct+" IDENTIFIED BY "+mysqlLiteral(password)+";")
		if superuser {
			stmts = append(stmts, "GRANT ALL PRIVILEGES ON *.* TO "+acct+" WITH GRANT OPTION;")
		}
	}
	stmts = append(stmts, "FLUSH PRIVILEGES;")
	args := []string{"--protocol=socket", "--user=root", "--batch", "--execute", strings.Join(stmts, " ")}
	_ = port
	cmd := hostexec.CommandOnHost(ctx, client, args...)
	return runHostCmd(ctx, cmd)
}

func mysqlLiteral(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "'", "''")
	return "'" + v + "'"
}

// hostMongoAccount uses the shell's localhost exception: with no users in
// admin yet, a local shell may create the first one; with users, root's
// shell still connects where authentication is off, which is the apt default.
func hostMongoAccount(ctx context.Context, port int, account, password string, superuser bool) (string, error) {
	shell := "mongosh"
	if !hostexec.Available(shell) {
		shell = "mongo"
		if !hostexec.Available(shell) {
			return "", fmt.Errorf("mongosh is not installed on this server; create a user from a shell instead")
		}
	}
	roles := "[]"
	if superuser {
		roles = `[{role: "root", db: "admin"}]`
	}
	script := fmt.Sprintf(`const a = db.getSiblingDB("admin");
const u = %s; const p = %s;
if (a.getUser(u)) { a.updateUser(u, {pwd: p, roles: %s}); } else { a.createUser({user: u, pwd: p, roles: %s}); }`,
		jsLiteral(account), jsLiteral(password), roles, roles)
	args := []string{"--quiet", "--port", strconv.Itoa(port), "--eval", script}
	cmd := hostexec.CommandOnHost(ctx, shell, args...)
	return runHostCmd(ctx, cmd)
}

func jsLiteral(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// hostClickHouseAccount uses the local client as default, which a stock
// install admits from the machine without a password.
func hostClickHouseAccount(ctx context.Context, port int, account, password string, superuser bool) (string, error) {
	if !hostexec.Available("clickhouse-client") {
		return "", fmt.Errorf("clickhouse-client is not installed on this server")
	}
	stmts := []string{
		"CREATE USER IF NOT EXISTS " + quoteBacktickPlain(account) + " IDENTIFIED WITH sha256_password BY " + sqlLiteral(password),
		"ALTER USER " + quoteBacktickPlain(account) + " IDENTIFIED WITH sha256_password BY " + sqlLiteral(password),
	}
	if superuser {
		stmts = append(stmts, "GRANT ALL ON *.* TO "+quoteBacktickPlain(account)+" WITH GRANT OPTION")
	}
	_ = port
	for _, stmt := range stmts {
		cmd := hostexec.CommandOnHost(ctx, "clickhouse-client", "--query", stmt)
		if _, err := runHostCmd(ctx, cmd); err != nil {
			return "", err
		}
	}
	return "", nil
}

func quoteBacktickPlain(name string) string { return "`" + name + "`" }

// hostRedisPassword reads requirepass out of the server's configuration on
// the host, which root may read and the operator otherwise has to go and
// find. It is the one engine whose "account" is a single password.
func hostRedisPassword() (string, bool) {
	for _, path := range []string{"/host/etc/redis/redis.conf", "/etc/redis/redis.conf", "/host/etc/valkey/valkey.conf", "/etc/valkey/valkey.conf"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			fields := strings.Fields(string(line))
			if len(fields) >= 2 && fields[0] == "requirepass" {
				return strings.Trim(fields[1], `"`), true
			}
		}
	}
	return "", false
}

// hostPostgresDrop removes a role the same way it was made. Used by the live
// test to leave the server as it found it.
func hostPostgresDrop(ctx context.Context, port int, account string) (string, error) {
	pg, err := hostAccount("postgres")
	if err != nil {
		return "", err
	}
	stmt := "DROP ROLE IF EXISTS " + `"` + strings.ReplaceAll(account, `"`, `""`) + `"`
	args := []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "-p", strconv.Itoa(port), "-d", "postgres", "-c", stmt}
	cmd, err := hostexec.CommandOnHostAsUser(ctx, pg, []string{"PGCONNECT_TIMEOUT=10"}, "psql", args...)
	if err != nil {
		return "", err
	}
	return runHostCmd(ctx, cmd)
}
