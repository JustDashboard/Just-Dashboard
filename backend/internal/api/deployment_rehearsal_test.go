package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// A copy's restore against a Docker daemon that records what it is asked to
// run. Every exec is answered at once, with nothing on its streams and exit
// code 0; every file put into a container is recorded by directory; and the
// copy is a container on its network at an address the test listens on,
// which counts the connections made to it and closes each.

const copyTestNetwork = "jd-e4-run12-rehearsal"

type copyExec struct {
	Cmd []string
	Env []string
}

type copyEngine struct {
	mu      sync.Mutex
	execs   []copyExec
	uploads []string
	dialled int
}

func (e *copyEngine) recorded() ([]copyExec, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.execs), slices.Clone(e.uploads)
}

func (e *copyEngine) dials() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dialled
}

// startCopyEngine is the daemon and the copy's address: a port on loopback
// that takes a connection and closes it, so that whatever signs in there
// fails having reached it.
func startCopyEngine(t *testing.T) (*migrationRehearser, *copyEngine, int) {
	t.Helper()
	engine := &copyEngine{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			engine.mu.Lock()
			engine.dialled++
			engine.mu.Unlock()
			_ = conn.Close()
		}
	}()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1.") {
			path = path[strings.Index(path[1:], "/")+1:]
		}
		switch {
		case path == "/_ping":
			w.Header().Set("API-Version", "1.47")
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/exec"):
			var body copyExec
			_ = json.NewDecoder(r.Body).Decode(&body)
			engine.mu.Lock()
			engine.execs = append(engine.execs, body)
			id := len(engine.execs)
			engine.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": fmt.Sprintf("exec-%d", id)})
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/exec/") && strings.HasSuffix(path, "/start"):
			// The start's body is read before the connection is taken over, and
			// the stream is ended with a half-close the client reads as EOF:
			// closing a socket that still holds unread bytes resets it instead,
			// which the client sees as a failed exec and retries.
			_, _ = io.Copy(io.Discard, r.Body)
			conn, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = buffered.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buffered.Flush()
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(io.Discard, buffered)
			}
			_ = conn.Close()
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/exec/") && strings.HasSuffix(path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ID": strings.Split(path, "/")[2], "Running": false, "ExitCode": 0})
		case r.Method == http.MethodPut && strings.HasSuffix(path, "/archive"):
			_, _ = io.Copy(io.Discard, r.Body)
			engine.mu.Lock()
			engine.uploads = append(engine.uploads, r.URL.Query().Get("path"))
			engine.mu.Unlock()
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": "c0py", "Name": "/" + strings.Split(path, "/")[2],
				"State":           map[string]any{"Status": "running", "Running": true},
				"Config":          map[string]any{"Image": "postgres:17-alpine"},
				"NetworkSettings": map[string]any{"Networks": map[string]any{copyTestNetwork: map[string]any{"IPAddress": "127.0.0.1"}}},
			})
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(daemon.Close)
	s := &Server{}
	// tcp://, not http://: the client hijacks an exec's connection by
	// dialling the daemon itself, and dials only the networks it knows.
	s.modules.docker = dockerx.New("tcp://" + strings.TrimPrefix(daemon.URL, "http://"))
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	return &migrationRehearser{server: s}, engine, listener.Addr().(*net.TCPAddr).Port
}

// copyTestSecret is in every password the tests hand a copy, so that a
// command line carrying one is found by looking for it.
const copyTestSecret = `pw-'quoted' "and" spaced\`

func copyTestMade(driver dbx.Driver, engine string, port int) *madeCopy {
	user := "postgres"
	if driver == dbx.DriverMySQL {
		user = "root"
	}
	return &madeCopy{
		conn:   &dbConnection{ID: 4, Name: "orders", Driver: driver},
		info:   &dbx.ConnInfo{User: "app", Password: "app-" + copyTestSecret, Database: "orders"},
		engine: rehearsalEngine{name: engine, user: user, password: "root-" + copyTestSecret},
		port:   port,
		copy:   deploy.RehearsalCopy{Container: "jd-e4-run12-rehearsal-db", Network: copyTestNetwork},
	}
}

func writeDump(t *testing.T, name string, body []byte, compress bool) string {
	t.Helper()
	if compress {
		var zipped bytes.Buffer
		gz := gzip.NewWriter(&zipped)
		_, _ = gz.Write(body)
		_ = gz.Close()
		body = zipped.Bytes()
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A SQL dump is replayed by dbx over a connection to the copy's address, for
// PostgreSQL, MySQL and MariaDB alike, plain or compressed, whoever wrote it:
// nothing is run in the copy and nothing is put into it, so no psql, mysql or
// mariadb is ever fed a script (invariant 6).
func TestACopyReplaysASQLDumpOverAConnectionAndRunsNoClient(t *testing.T) {
	postgresScript := []byte("SET standard_conforming_strings = on;\nCREATE TABLE items (id integer);\n")
	mysqlScript := []byte("-- MySQL dump 10.13\nCREATE TABLE items (id int);\n")
	own := []byte("-- Just Dashboard dump\nCREATE TABLE items (id integer);\n")
	for _, c := range []struct {
		name   string
		driver dbx.Driver
		engine string
		file   string
		body   []byte
		gzip   bool
	}{
		{"postgres plain pg_dump", dbx.DriverPostgres, "postgres", "orders.sql", postgresScript, false},
		{"postgres compressed", dbx.DriverPostgres, "postgres", "orders.sql.gz", postgresScript, true},
		{"postgres the dashboard's own", dbx.DriverPostgres, "postgres", "orders.sql", own, false},
		{"mysql mysqldump", dbx.DriverMySQL, "mysql", "orders.sql", mysqlScript, false},
		{"mysql compressed", dbx.DriverMySQL, "mysql", "orders.sql.gz", mysqlScript, true},
		{"mariadb mysqldump", dbx.DriverMySQL, "mariadb", "orders.sql", mysqlScript, false},
		{"mariadb the dashboard's own, compressed", dbx.DriverMySQL, "mariadb", "orders.sql.gz", own, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, engine, port := startCopyEngine(t)
			path := writeDump(t, c.file, c.body, c.gzip)
			if kind := dbx.DumpKind(path); kind != "SQL" && kind != "compressed SQL" {
				t.Fatalf("the fixture reads as %q", kind)
			}
			var transcript []string
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := m.restoreDump(ctx, copyTestMade(c.driver, c.engine, port), path, func(line deploy.BuildLog) error {
				transcript = append(transcript, line.Text)
				return nil
			})
			if err == nil {
				t.Fatal("a restore into an address that closes every connection succeeded")
			}
			if engine.dials() == 0 {
				t.Fatalf("the replay never reached the copy's address: %v", err)
			}
			execs, uploads := engine.recorded()
			if len(execs) != 0 || len(uploads) != 0 {
				t.Fatalf("a SQL dump ran %v and put files into %v in the copy", execs, uploads)
			}
			if strings.Contains(err.Error(), copyTestSecret) || strings.Contains(strings.Join(transcript, "\n"), copyTestSecret) {
				t.Fatalf("the failure carries the copy's password: %v", err)
			}
			if !slices.Contains(transcript, strings.Split(err.Error(), "\n")[0]) {
				t.Fatalf("the transcript %q does not name the failure %q", transcript, err)
			}
		})
	}
}

// A pg_dump archive is the one dump the copy's own client reads: pg_restore,
// inside the copy, over its socket, as the application's role.
func TestACopyRestoresAnArchiveWithPgRestoreAlone(t *testing.T) {
	m, engine, port := startCopyEngine(t)
	path := writeDump(t, "orders.dump", append([]byte("PGDMP"), make([]byte, 64)...), false)
	if err := m.restoreDump(context.Background(), copyTestMade(dbx.DriverPostgres, "postgres", port), path, func(deploy.BuildLog) error { return nil }); err != nil {
		t.Fatal(err)
	}
	execs, uploads := engine.recorded()
	if len(execs) != 1 || execs[0].Cmd[0] != "pg_restore" || !slices.Contains(execs[0].Cmd, "--role=app") || execs[0].Cmd[len(execs[0].Cmd)-1] != "/tmp/rehearsal-dump" {
		t.Fatalf("an archive was restored with %v", execs)
	}
	if !slices.Equal(uploads, []string{"/tmp"}) {
		t.Fatalf("the archive was put at %v", uploads)
	}
	if engine.dials() != 0 {
		t.Fatal("an archive's restore signed in over the network")
	}
}

// No command a copy runs carries a password: the readiness ping, the
// application's account, the copy's own passwords and the archive's restore
// each take theirs from the environment the client reads, and the password
// statements MySQL needs go over a connection instead. A command line is
// readable by every account on the host; an environment only by its own.
func TestNoCommandACopyRunsCarriesAPassword(t *testing.T) {
	m, engine, port := startCopyEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	postgres := copyTestMade(dbx.DriverPostgres, "postgres", port)
	maria := copyTestMade(dbx.DriverMySQL, "mariadb", port)
	archive := writeDump(t, "orders.dump", append([]byte("PGDMP"), make([]byte, 64)...), false)
	script := writeDump(t, "orders.sql", []byte("CREATE TABLE items (id int);\n"), false)
	ignore := func(deploy.BuildLog) error { return nil }

	for _, step := range []struct {
		name string
		run  func() error
		// fails: the step signs in over the network, which the test's
		// address refuses.
		fails bool
	}{
		{"postgres ping", func() error { return m.waitForCopy(ctx, postgres.copy.Container, postgres.engine) }, false},
		{"mariadb ping", func() error { return m.waitForCopy(ctx, maria.copy.Container, maria.engine) }, false},
		{"application role", func() error {
			return m.copyApplicationRole(ctx, postgres.copy.Container, postgres.engine, postgres.info)
		}, false},
		{"postgres copy password", func() error { return m.setCopyPassword(ctx, postgres, "copy-"+copyTestSecret) }, false},
		{"archive restore", func() error { return m.restoreDump(ctx, postgres, archive, ignore) }, false},
		{"postgres script", func() error { return m.restoreDump(ctx, postgres, script, ignore) }, true},
		{"mariadb script", func() error { return m.restoreDump(ctx, maria, script, ignore) }, true},
		{"mariadb copy password", func() error { return m.setCopyPassword(ctx, maria, "copy-"+copyTestSecret) }, true},
	} {
		if err := step.run(); (err != nil) != step.fails {
			t.Fatalf("%s: %v", step.name, err)
		}
	}

	execs, _ := engine.recorded()
	if len(execs) != 5 {
		t.Fatalf("the steps ran %d commands, not the five that go through the copy's own clients: %v", len(execs), execs)
	}
	// A password is looked for in every form a client could be handed it in:
	// as it is, escaped for PGOPTIONS, and inside a SQL or a MySQL literal,
	// each of which doubles its quotes.
	forms := []string{copyTestSecret, pgOptionsValue(copyTestSecret)}
	for _, literal := range []string{sqlLiteral(copyTestSecret), mysqlLiteral(copyTestSecret)} {
		forms = append(forms, literal[1:len(literal)-1])
	}
	for _, exec := range execs {
		for _, arg := range exec.Cmd {
			for _, form := range forms {
				if strings.Contains(arg, form) {
					t.Fatalf("%v carries a password on its command line", exec.Cmd)
				}
			}
		}
	}
	// And the passwords that were needed did arrive, in the environment.
	environment := func(i int) string { return strings.Join(execs[i].Env, "\n") }
	if !strings.Contains(environment(1), "MYSQL_PWD=root-"+copyTestSecret) {
		t.Errorf("the MariaDB ping was not given root's password: %q", execs[1].Env)
	}
	if !strings.Contains(environment(2), "jd.secret="+pgOptionsValue("app-"+copyTestSecret)) || !strings.Contains(execs[2].Cmd[len(execs[2].Cmd)-1], copySecretSQL) {
		t.Errorf("the application's role was not made from the session's password: %v %q", execs[2].Cmd, execs[2].Env)
	}
	if !strings.Contains(environment(3), "jd.secret="+pgOptionsValue("copy-"+copyTestSecret)) {
		t.Errorf("the copy's password was not handed over in the environment: %q", execs[3].Env)
	}
}

// pgOptionsValue against the server's own reading of PGOPTIONS (postinit.c,
// pg_split_opts, then the -c switch's name=value): whatever a password holds,
// the options come back as the two that were written, and the variable is
// still text the Engine API carries intact.
func TestPGOptionsCarryAnyPasswordIntact(t *testing.T) {
	split := func(options string) []string {
		var out []string
		for i := 0; i < len(options); {
			for i < len(options) && strings.IndexByte(" \t\n\v\f\r", options[i]) >= 0 {
				i++
			}
			if i == len(options) {
				break
			}
			var word []byte
			escaped := false
			for ; i < len(options); i++ {
				c := options[i]
				if strings.IndexByte(" \t\n\v\f\r", c) >= 0 && !escaped {
					break
				}
				if !escaped && c == '\\' {
					escaped = true
					continue
				}
				escaped = false
				word = append(word, c)
			}
			out = append(out, string(word))
		}
		return out
	}
	for _, value := range []string{"", "plain", "a b", "tab\tend", "line\nbreak", `back\slash`, `trailing\`, "it's", `"dq"`, "$$", "-c jd.role=x", "=", "wörd ünïcode", copyTestSecret} {
		role := "role " + value
		options := "-c jd.role=" + pgOptionsValue(role) + " -c jd.secret=" + pgOptionsValue(value)
		if !utf8.ValidString(options) {
			t.Fatalf("%q escapes to invalid UTF-8", value)
		}
		got := split(options)
		want := []string{"-c", "jd.role=" + role, "-c", "jd.secret=" + value}
		if !slices.Equal(got, want) {
			t.Fatalf("%q reads back as %q", value, got)
		}
	}
}

// What a copy runs is built in these files, and none of their text pipes a
// script into a database client or names a password on a command line: the
// bound invariant 6 sets, held where the next change to them would break it.
func TestCopyCodeFeedsNoClientAScriptAndNamesNoPasswordFlag(t *testing.T) {
	piped := regexp.MustCompile(`(^|[^|])\|\s*("\$client"|psql\b|mysql\b|mariadb\b)`)
	forbidden := []string{"--password", `-p"`, "-p$", "REHEARSAL_PASSWORD"}
	for _, file := range []string{"deployment_rehearsal.go", "deployment_data_rollback.go", "deployment_preview_branches.go", "deployment_preview_branch_mask.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			for _, word := range forbidden {
				if strings.Contains(text, word) {
					t.Errorf("%s: %q holds %q", file, text, word)
				}
			}
			if piped.MatchString(text) {
				t.Errorf("%s: %q pipes a script into a database client", file, text)
			}
			if text == "-f" {
				t.Errorf("%s: a client is handed a file to read (-f)", file)
			}
			return true
		})
	}
}
