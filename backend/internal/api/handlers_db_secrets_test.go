package api

import (
	"bytes"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// A connection string holds a password, and a driver that cannot use the
// string says so by quoting it. What a driver says is what a failed request
// answers with, what a refused mutation's audit entry records and what the
// process logs — three places a password must never be written.
//
// So every route under a connection is asked, for every engine family, with a
// connection string its driver refuses for the way it is written, and the
// password has to be in none of the three. A handler that opens a connection
// of its own and passes on what the driver said is caught here whichever file
// it is in, and so is one added tomorrow.
func TestNoRouteQuotesAConnectionStringsPassword(t *testing.T) {
	const secret = "s3cr3t-never-on-the-wire"
	// How many answers had a password taken out of them. None would mean no
	// driver here quotes a string any more, and that this test has stopped
	// proving anything.
	blanked := 0
	for _, c := range []struct {
		driver dbx.Driver
		dsn    string
	}{
		// A port that is not a number: net/url, which most drivers parse
		// with, answers by quoting the whole string.
		{dbx.DriverRedis, "redis://:" + secret + "@127.0.0.1:port/0"},
		{dbx.DriverMongo, "mongodb://app:" + secret + "@127.0.0.1:port/shop"},
		{dbx.DriverPostgres, "postgres://app:" + secret + "@127.0.0.1:port/shop"},
		{dbx.DriverClickHouse, "clickhouse://app:" + secret + "@127.0.0.1:port/shop"},
		{dbx.DriverMSSQL, "sqlserver://sa:" + secret + "@127.0.0.1:port?database=shop"},
		{dbx.DriverOracle, "oracle://app:" + secret + "@127.0.0.1:port/shop"},
		// An address nothing listens on: the string is read, and the dial
		// is what fails. (Not MongoDB, which waits eight seconds for a server
		// to appear before each of its seventy routes gives up.)
		{dbx.DriverRedis, "redis://:" + secret + "@127.0.0.1:1/0"},
		{dbx.DriverMySQL, "app:" + secret + "@tcp(127.0.0.1:1)/shop"},
	} {
		t.Run(string(c.driver)+" "+strings.SplitN(strings.SplitN(c.dsn, "@", 2)[1], "/", 2)[0], func(t *testing.T) {
			// The harness stands in for Docker, the machine's sockets and
			// units, and systemctl: every route is asked, and none of them is
			// to reach this machine.
			s := newConnHarness(t).s
			t.Cleanup(s.Shutdown)
			sealed, err := s.Sealer.Seal(c.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Store.DB.Exec(
				`INSERT INTO db_connections(id, name, driver, dsn_enc, created_at) VALUES(1,'leaky',?,?,0)`,
				string(c.driver), sealed); err != nil {
				t.Fatal(err)
			}
			// Everything the process would log, by either logger.
			var logged bytes.Buffer
			previousLog, previousSlog := log.Writer(), slog.Default()
			log.SetOutput(&logged)
			slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
			t.Cleanup(func() {
				log.SetOutput(previousLog)
				slog.SetDefault(previousSlog)
			})

			handler := s.Routes()
			client := &client{t: t, h: handler, cookie: signIn(t, s)}
			asked, failed, scrubbed := 0, 0, 0
			for _, rt := range apiRoutes(t, handler) {
				if !strings.HasPrefix(rt.pattern, connectionRoutePrefix+"/") {
					continue
				}
				rest := strings.TrimPrefix(rt.pattern, connectionRoutePrefix)
				switch {
				case rest == "/redis/monitor" || rest == "/redis/subscribe":
					// A WebSocket upgrade cannot be asked through a recorder.
					continue
				case rt.method != http.MethodGet && (rest == "/access" || rest == "/power"):
					// These act on the machine and not on the connection.
					continue
				}
				body := ""
				if rt.method != http.MethodGet {
					body = `{}`
				}
				rec := client.do(rt.method, rt.path, body, map[string]string{httpx.ConfirmHeader: "shop"})
				asked++
				if rec.Code >= http.StatusInternalServerError {
					failed++
				}
				if strings.Contains(rec.Body.String(), "***") {
					scrubbed++
				}
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("%s %s answered with the password: %d %s", rt.method, rt.pattern, rec.Code, strings.TrimSpace(rec.Body.String()))
				}
			}
			if asked < 150 {
				t.Fatalf("only %d routes were asked; the walk is not seeing the real router", asked)
			}
			blanked += scrubbed
			if failed == 0 {
				t.Fatalf("no route failed to connect, so nothing was put to the test")
			}
			var leaked int
			if err := s.Store.DB.QueryRow(
				`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%' || ? || '%' OR target LIKE '%' || ? || '%'`,
				secret, secret).Scan(&leaked); err != nil {
				t.Fatal(err)
			}
			if leaked != 0 {
				t.Errorf("%d audit entries carry the password", leaked)
			}
			if text := logged.String(); strings.Contains(text, secret) {
				at := strings.Index(text, secret)
				t.Errorf("the process log carries the password: …%s…", text[max(0, at-200):min(len(text), at+60)])
			}
		})
	}
	if blanked == 0 {
		t.Error("no driver quoted its connection string, so no answer needed its password taken out: find one that does")
	}
}
