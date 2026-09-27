package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// A watched host:port link put "mail.example.com:993" in ?domain=, which the
// scan joined with the default port into "[mail.example.com:993]:443", and a
// pasted URL was looked up in DNS whole. Both now reach the host they name.
func TestTLSScanReadsTheTargetTheWayThePageDoes(t *testing.T) {
	c, _ := newClient(t)
	for _, path := range []string{
		"/api/v1/certificates/scan?domain=127.0.0.1:1",
		"/api/v1/certificates/scan?domain=" + url.QueryEscape("https://127.0.0.1:1/login"),
		"/api/v1/certificates/scan?domain=127.0.0.1&port=1",
	} {
		w := c.do(http.MethodGet, path, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var scan proxysvc.TLSScan
		if err := json.Unmarshal(w.Body.Bytes(), &scan); err != nil {
			t.Fatal(err)
		}
		if scan.Domain != "127.0.0.1" || scan.Port != 1 {
			t.Errorf("%s scanned %s port %d", path, scan.Domain, scan.Port)
		}
	}
	for _, path := range []string{
		"/api/v1/certificates/scan?domain=" + url.QueryEscape("exa mple.com"),
		"/api/v1/certificates/scan?domain=127.0.0.1&port=abc",
		"/api/v1/certificates/scan?domain=127.0.0.1:993&port=443",
		"/api/v1/certificates/scan?domain=",
		"/api/v1/certificates/check?domain=" + url.QueryEscape("user@127.0.0.1"),
	} {
		if w := c.do(http.MethodGet, path, "", nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400: %s", path, w.Code, w.Body.String())
		}
	}
}

// One row per name meant watching mail on 993 replaced it on 443, and a pasted
// URL was split on its colon into a host called "https".
func TestTheWatchListHoldsEndpoints(t *testing.T) {
	c, s := newClient(t)
	add := func(body string, want int) watchedDomain {
		t.Helper()
		w := c.do(http.MethodPost, "/api/v1/certificates/watched", body, nil)
		if w.Code != want {
			t.Fatalf("%s: %d, want %d: %s", body, w.Code, want, w.Body.String())
		}
		var d watchedDomain
		json.Unmarshal(w.Body.Bytes(), &d)
		return d
	}
	web := add(`{"domain":"https://Mail.Example.test/webmail","port":0}`, http.StatusCreated)
	if web.Domain != "mail.example.test" || web.Port != 443 {
		t.Fatalf("a pasted URL watched %s port %d", web.Domain, web.Port)
	}
	imap := add(`{"domain":"mail.example.test:993","port":0}`, http.StatusCreated)
	if imap.Port != 993 || imap.ID == web.ID {
		t.Fatalf("the second port replaced the first: %+v and %+v", web, imap)
	}
	if again := add(`{"domain":"mail.example.test","port":993}`, http.StatusOK); again.ID != imap.ID {
		t.Fatalf("watching an endpoint twice made a second row: %+v", again)
	}
	for _, body := range []string{
		`{"domain":"exa mple.test","port":0}`,
		`{"domain":"mail.example.test","port":70000}`,
		`{"domain":"*.example.test","port":0}`,
	} {
		if w := c.do(http.MethodPost, "/api/v1/certificates/watched", body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}

	w := c.do(http.MethodGet, "/api/v1/certificates/watched?check=false", "", nil)
	var list []watchedDomain
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list = %s (%v)", w.Body.String(), err)
	}

	// A row carried over from watched_domains goes from both tables, or the
	// copy on the next boot would bring it back.
	if _, err := s.Store.DB.Exec(`INSERT INTO watched_domains(domain, port, created_at)
		VALUES('mail.example.test', 443, 1)`); err != nil {
		t.Fatal(err)
	}
	if w := c.do(http.MethodDelete, "/api/v1/certificates/watched/"+strconv.FormatInt(web.ID, 10), "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("unwatch: %d %s", w.Code, w.Body.String())
	}
	var legacy int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM watched_domains WHERE domain = 'mail.example.test'`).Scan(&legacy)
	if legacy != 0 {
		t.Fatal("the watched_domains row survived the unwatch")
	}
	if w := c.do(http.MethodDelete, "/api/v1/certificates/watched/"+strconv.FormatInt(web.ID, 10), "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("a second unwatch: %d", w.Code)
	}
}

// The watch list is readable by every account, and reading it used to
// handshake with every endpoint — outbound traffic a read-only account may
// not send. It now reads what the last administrator's check found.
func TestReadOnlyAccountsReadTheLastCheckWithoutAHandshake(t *testing.T) {
	var handshakes atomic.Int32
	target := httptest.NewUnstartedServer(http.NotFoundHandler())
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			handshakes.Add(1)
		}
	}
	target.StartTLS()
	defer target.Close()
	host, port, _ := net.SplitHostPort(target.Listener.Addr().String())

	admin, s := newClient(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "watch-viewer", auth.RoleReadOnly)}
	if w := admin.do(http.MethodPost, "/api/v1/certificates/watched",
		`{"domain":"`+host+`","port":`+port+`}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("watch: %d %s", w.Code, w.Body.String())
	}
	list := func(c *client) watchedDomain {
		t.Helper()
		w := c.do(http.MethodGet, "/api/v1/certificates/watched", "", nil)
		var rows []watchedDomain
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		return rows[0]
	}

	if row := list(reader); row.Cert != nil || row.CheckedAt != nil {
		t.Fatalf("nothing has been checked yet: %+v", row)
	}
	if handshakes.Load() != 0 {
		t.Fatal("a read-only account's visit sent a handshake")
	}

	checked := list(admin)
	if handshakes.Load() == 0 || checked.Cert == nil || checked.Cert.Fingerprint == "" || checked.CheckedAt == nil {
		t.Fatalf("an administrator's visit should check: %d handshakes, %+v", handshakes.Load(), checked)
	}
	before := handshakes.Load()

	stored := list(reader)
	if handshakes.Load() != before {
		t.Fatal("a read-only account's visit sent a handshake")
	}
	if stored.Cert == nil || stored.Cert.Fingerprint != checked.Cert.Fingerprint ||
		stored.CheckedAt == nil || !stored.CheckedAt.Equal(*checked.CheckedAt) {
		t.Fatalf("the reader should see the last check: %+v", stored)
	}
}

// The check was meant to stay within 30 seconds, but CheckDomain's dial
// ignored the context: 33 endpoints that accept and never speak held an
// administrator's request for 40 seconds, and every row was overwritten with
// "context deadline exceeded". The budget now ends the checks, and what they
// did not reach keeps the result it had.
func TestTheWatchCheckKeepsToItsBudget(t *testing.T) {
	admin, s := newClient(t)
	silent := []int{}
	for range 10 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			// Accepted and never answered, until the test ends.
			var held []net.Conn
			defer func() {
				for _, conn := range held {
					conn.Close()
				}
			}()
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				held = append(held, conn)
			}
		}()
		silent = append(silent, ln.Addr().(*net.TCPAddr).Port)
	}
	target := httptest.NewTLSServer(http.NotFoundHandler())
	defer target.Close()
	targetPort := target.Listener.Addr().(*net.TCPAddr).Port

	checkedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for _, port := range silent {
		if _, err := s.Store.DB.Exec(`INSERT INTO watched_endpoints(domain, port, created_at, checked_at, certificate)
			VALUES('127.0.0.1', ?, 1, ?, '{"name":"127.0.0.1","issuer":"the stored check","domains":[],"usedBy":[]}')`,
			port, checkedAt.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	// Never checked, so it goes before the ten checked an hour ago.
	if _, err := s.Store.DB.Exec(`INSERT INTO watched_endpoints(domain, port, created_at) VALUES('127.0.0.1', ?, 1)`,
		targetPort); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/certificates/watched", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Cookie", admin.cookie)
	w := httptest.NewRecorder()
	started := time.Now()
	s.Routes().ServeHTTP(w, req)
	if took := time.Since(started); took > 7*time.Second {
		t.Fatalf("the check took %s on a 2s budget", took)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}

	check := func(body []byte) {
		t.Helper()
		var rows []watchedDomain
		if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 11 {
			t.Fatalf("rows = %s (%v)", body, err)
		}
		for _, row := range rows {
			if row.Port == targetPort {
				if row.Cert == nil || row.Cert.Fingerprint == "" || row.CheckedAt == nil {
					t.Errorf("the endpoint that answers was not checked: %+v", row)
				}
				continue
			}
			if row.Cert == nil || row.Cert.Issuer != "the stored check" || row.CheckedAt == nil || !row.CheckedAt.Equal(checkedAt) {
				t.Errorf("port %d lost its stored check: %+v %+v", row.Port, row.Cert, row.CheckedAt)
			}
		}
	}
	check(w.Body.Bytes())
	stored := admin.do(http.MethodGet, "/api/v1/certificates/watched?check=false", "", nil)
	check(stored.Body.Bytes())
}
