package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/gorilla/websocket"
)

// A site's requests are read from the file its own access_log names, through
// the same window, live tail and export a deployment's are, by anybody who
// may read the site — and a site whose log cannot be read says which of the
// several nothings it is.
func TestSiteRequestsReadTheFileTheSiteNames(t *testing.T) {
	s := testServer(t)
	nginx := t.TempDir()
	s.Cfg.NginxDir = nginx
	s.initModules()
	root := s.Cfg.LogRoots[0]
	if err := os.MkdirAll(filepath.Join(nginx, "sites-available"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := func(name, directives string) {
		t.Helper()
		content := fmt.Sprintf("server {\n    listen 80;\n    server_name %s.example.com;\n%s\n    location / { proxy_pass http://127.0.0.1:3000; }\n}\n", name, directives)
		if err := os.WriteFile(filepath.Join(nginx, "sites-available", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	accessLog := filepath.Join(root, "shop.access.log")
	errorLog := filepath.Join(root, "shop.error.log")
	site("shop", "    access_log "+accessLog+";\n    error_log "+errorLog+" warn;")
	site("quiet", "    access_log off;")
	site("elsewhere", "    access_log "+filepath.Join(t.TempDir(), "elsewhere.access.log")+";")

	now := time.Now().UTC()
	line := func(ago time.Duration, client, request string, status, size int, agent string) string {
		return fmt.Sprintf(`%s - - [%s] "%s HTTP/1.1" %d %d "-" "%s"`,
			client, now.Add(-ago).Format("02/Jan/2006:15:04:05 -0700"), request, status, size, agent)
	}
	writeLog(t, accessLog,
		line(3*time.Minute, "203.0.113.9", "GET /", 200, 612, "Mozilla/5.0"),
		line(2*time.Minute, "203.0.113.9", "POST /cart", 502, 157, "Mozilla/5.0"),
		// An agent is whatever the client sent, a formula included.
		line(time.Minute, "198.51.100.7", "GET /.env", 404, 0, "=HYPERLINK(A1)"),
	)

	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "site-reader", auth.RoleReadOnly)}

	// The site says where it logs, which is what its page reads.
	read := decode[struct {
		Spec proxysvc.SiteSpec `json:"spec"`
	}](t, reader.do("GET", "/api/v1/proxy/sites/shop", "", nil))
	if read.Spec.AccessLogPath != accessLog || read.Spec.ErrorLogPath != errorLog {
		t.Fatalf("spec logs to %q and %q", read.Spec.AccessLogPath, read.Spec.ErrorLogPath)
	}

	window := decode[deploy.RequestWindow](t, reader.do("GET", "/api/v1/proxy/sites/shop/requests", "", nil))
	if window.Status != "available" || window.Driver != "nginx" || window.Latency {
		t.Fatalf("status %q driver %q latency %v: %s", window.Status, window.Driver, window.Latency, window.Reason)
	}
	if len(window.Entries) != 3 || window.Summary.Classes["5xx"] != 1 || window.Summary.Classes["4xx"] != 1 {
		t.Fatalf("entries %d classes %v", len(window.Entries), window.Summary.Classes)
	}
	failed := decode[deploy.RequestWindow](t, reader.do("GET", "/api/v1/proxy/sites/shop/requests?classes=5xx", "", nil))
	if len(failed.Entries) != 1 || failed.Entries[0].Path != "/cart" || failed.Entries[0].Method != "POST" {
		t.Fatalf("5xx = %+v", failed.Entries)
	}

	export := reader.do("GET", "/api/v1/proxy/sites/shop/requests/export?classes=4xx", "", nil)
	rows := strings.Split(strings.TrimSpace(export.Body.String()), "\n")
	if export.Code != 200 || len(rows) != 2 || !strings.Contains(rows[1], "/.env") ||
		!strings.Contains(export.Header().Get("Content-Disposition"), "requests-shop-") {
		t.Fatalf("export (%d) %q:\n%s", export.Code, export.Header().Get("Content-Disposition"), export.Body.String())
	}
	if !strings.HasSuffix(rows[1], ",'=HYPERLINK(A1),") {
		t.Errorf("the agent reached the spreadsheet as a formula: %s", rows[1])
	}

	// The live tail continues from the window's cursor with what nginx appends.
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+
		"/api/v1/proxy/sites/shop/requests/stream?after="+strconv.FormatUint(window.Coverage.Cursor, 10),
		http.Header{"Cookie": {reader.cookie}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("could not open the stream (%d): %v", status, err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if kind, data := readFrame(t, conn); kind != "meta" || !strings.Contains(string(data), `"driver":"nginx"`) {
		t.Fatalf("first frame %s %s", kind, data)
	}
	f, err := os.OpenFile(accessLog, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, line(0, "203.0.113.9", "GET /checkout", 503, 0, "Mozilla/5.0"))
	f.Close()
	kind, data := readFrame(t, conn)
	var batch []struct {
		Path   string `json:"path"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(data, &batch); err != nil || kind != "requests" || len(batch) != 1 ||
		batch[0].Path != "/checkout" || batch[0].Status != 503 {
		t.Fatalf("appended frame %s %s", kind, data)
	}

	// Edited to log elsewhere — in the raw sheet, or over SSH — the site is
	// read from its new file on the next question, not the one it named
	// when the record was first held.
	moved := filepath.Join(root, "shop-moved.access.log")
	site("shop", "    access_log "+moved+";")
	writeLog(t, moved, line(30*time.Second, "192.0.2.4", "GET /moved", 200, 10, "Mozilla/5.0"))
	after := decode[deploy.RequestWindow](t, reader.do("GET", "/api/v1/proxy/sites/shop/requests", "", nil))
	if len(after.Entries) != 1 || after.Entries[0].Path != "/moved" {
		t.Fatalf("after the edit: %+v", after.Entries)
	}

	// Each nothing says which it is.
	for name, want := range map[string]string{
		"quiet":     "keeps no access log of its own",
		"elsewhere": "JD_LOG_ROOTS",
		"gone":      "no site by that name",
	} {
		got := decode[deploy.RequestWindow](t, reader.do("GET", "/api/v1/proxy/sites/"+name+"/requests", "", nil))
		if got.Status != "unavailable" || !strings.Contains(got.Reason, want) {
			t.Errorf("%s: %q %q", name, got.Status, got.Reason)
		}
	}
}
