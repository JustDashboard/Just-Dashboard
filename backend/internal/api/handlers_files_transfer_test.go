package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/gorilla/websocket"
)

func dialFileWatch(t *testing.T, srv *httptest.Server, cookie string, q url.Values) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+
		"/api/v1/files/watch?"+q.Encode(), http.Header{"Cookie": {cookie}})
	if conn != nil {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	return conn, resp, err
}

func TestFileWatchSocketSendsTheChangedRows(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	root := s.modules.files.Roots()[0]

	conn, _, err := dialFileWatch(t, srv, cookie, url.Values{"path": {root}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if kind, _ := readFrame(t, conn); kind != "watching" {
		t.Fatalf("first frame %q", kind)
	}
	if err := os.WriteFile(filepath.Join(root, "arrived.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	kind, payload := readFrame(t, conn)
	if kind != "changes" {
		t.Fatalf("frame %q", kind)
	}
	var change files.Change
	if err := json.Unmarshal(payload, &change); err != nil {
		t.Fatal(err)
	}
	if len(change.Entries) != 1 || change.Entries[0].Name != "arrived.txt" || change.Entries[0].Size != 5 {
		t.Fatalf("change %+v", change)
	}
	conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for s.modules.fileTransfer.watches.Active() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the watch outlived its socket")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFileWatchRefusesAPathOutsideTheRootsBeforeTheSocketOpens(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	_, resp, err := dialFileWatch(t, srv, cookie, url.Values{"path": {t.TempDir()}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("outside the roots opened (%d): %v", status, err)
	}
}

func TestFileWatchPastTheLimitSaysFallback(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	s.modules.fileTransfer.watches = files.NewWatchHub(s.modules.hostFiles, 1, 1)
	root := s.modules.files.Roots()[0]
	first, _, err := dialFileWatch(t, srv, cookie, url.Values{"path": {root}})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	readFrame(t, first)
	second, _, err := dialFileWatch(t, srv, cookie, url.Values{"path": {root}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if kind, _ := readFrame(t, second); kind != "fallback" {
		t.Fatalf("past the limit the socket said %q", kind)
	}

	c := &client{t: t, h: s.Routes(), cookie: cookie}
	w := c.do(http.MethodGet, query("/api/v1/files/changes", map[string]string{"path": root}), "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "fingerprint") {
		t.Fatalf("changes answered %d: %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, query("/api/v1/files/changes", map[string]string{"path": t.TempDir()}), "", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("changes outside the roots answered %d", w.Code)
	}
}
