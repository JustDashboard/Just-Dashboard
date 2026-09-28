package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/term"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

func terminalServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	// The terminal account is the host's lowest regular account unless one is
	// named, and a session's clipboard directory is chowned to it. As root the
	// backend may give a file to anyone; as the account running the tests it
	// may only keep it, so the tests name that account rather than depend on
	// it happening to be the lowest one on the machine.
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Addr:           "127.0.0.1:8080",
		DataDir:        t.TempDir(),
		AllowedCIDRs:   []*net.IPNet{loopback},
		SessionTTL:     time.Hour,
		IdleTTL:        time.Minute,
		FileRoots:      []string{t.TempDir()},
		LogRoots:       []string{t.TempDir()},
		TerminalEnable: true,
		TerminalUser:   me.Username,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
	s := New(cfg, log, st, svc, sealer, audit.New(st, log), nil)
	s.modules.term.SetClipboardRootForTest(t.TempDir())
	if _, err := s.modules.term.Account(); err != nil {
		t.Skipf("no account to open a session as: %v", err)
	}

	// context.Background, not t.Context: the test context is already cancelled
	// by the time cleanups run, but the direct PTYs still need to be reaped.
	t.Cleanup(func() {
		for _, sess := range s.modules.term.List() {
			s.modules.term.Kill(context.Background(), sess.ID)
		}
		s.Shutdown()
	})

	r := chi.NewRouter()
	// The capability middleware on the route group needs somebody to check.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			username := req.Header.Get("X-Test-User")
			if username == "" {
				username = "tester"
			}
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: username},
				Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountTerminalRoutes(r)
	return s, r
}

type apiCall struct {
	t       *testing.T
	handler http.Handler
}

func (c apiCall) do(method, path string, body any, confirm string) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if confirm != "" {
		req.Header.Set("X-Confirm", confirm)
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

func (c apiCall) ok(method, path string, body any, confirm string) *httptest.ResponseRecorder {
	c.t.Helper()
	rec := c.do(method, path, body, confirm)
	if rec.Code >= 300 {
		c.t.Fatalf("%s %s = %d: %s", method, path, rec.Code, rec.Body.String())
	}
	return rec
}

type listResponse struct {
	Enabled  bool             `json:"enabled"`
	Folders  []terminalFolder `json:"folders"`
	Sessions []workspace      `json:"sessions"`
}

func (c apiCall) list() listResponse {
	c.t.Helper()
	rec := c.ok(http.MethodGet, "/terminal/", nil, "")
	var out listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		c.t.Fatalf("decoding the listing: %v", err)
	}
	return out
}

func (l listResponse) session(t *testing.T, title string) workspace {
	t.Helper()
	for _, s := range l.Sessions {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("no session called %q in %+v", title, l.Sessions)
	return workspace{}
}

func (c apiCall) create(title, folder string) workspace {
	c.t.Helper()
	rec := c.ok(http.MethodPost, "/terminal/", map[string]any{
		"title": title, "folder": folder, "rows": 24, "cols": 80,
	}, "")
	var created workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		c.t.Fatal(err)
	}
	return created
}

func TestTerminalSizeQuery(t *testing.T) {
	for _, tt := range []struct {
		query      string
		rows, cols uint16
		ok         bool
	}{
		{query: "?rows=43&cols=156", rows: 43, cols: 156, ok: true},
		{query: "?rows=0&cols=156"},
		{query: "?rows=43"},
		{query: "?rows=-1&cols=80"},
		{query: "?rows=43&cols=999999"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/terminal/id/attach"+tt.query, nil)
		rows, cols, ok := terminalSizeQuery(r)
		if rows != tt.rows || cols != tt.cols || ok != tt.ok {
			t.Errorf("terminalSizeQuery(%q) = %d, %d, %v; want %d, %d, %v", tt.query, rows, cols, ok, tt.rows, tt.cols, tt.ok)
		}
	}
}

func TestTerminalAttachSynchronizesPTYBeforeRawIO(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("geometry", "")

	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") +
		"/terminal/" + created.ID + "/attach?rows=43&cols=156"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("stty size\r")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var output []byte
	for !bytes.Contains(output, []byte("43 156")) {
		kind, chunk, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("reading PTY output: %v; output %q", err, output)
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		output = append(output, chunk...)
	}
}

func TestDirectPTYAttachRetainsBestEffortShellHistory(t *testing.T) {
	s, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("direct-history", "")
	sess, err := s.modules.term.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A real login shell as the host account, with whatever its rc files
	// load; on a runner already busy with the rest of the suite, five seconds
	// to the first prompt was not a fair bound.
	deadline := time.Now().Add(20 * time.Second)
	for {
		snapshot, id, _, err := sess.Subscribe()
		if err != nil {
			t.Fatal(err)
		}
		sess.Unsubscribe(id)
		if len(snapshot) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("direct PTY produced no startup output")
		}
		time.Sleep(20 * time.Millisecond)
	}

	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") +
		"/terminal/" + created.ID + "/attach?rows=31&cols=106"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.TextMessage || !bytes.Contains(payload, []byte(`"type":"scrollback"`)) {
		t.Fatalf("first direct PTY frame = kind %d %q, want scrollback marker", kind, payload)
	}
}

// The reported bug, end to end.
//
// "I create a folder, then New session inside this folder makes a session that
// is not in the folder but in Other. Then the normal New session makes another
// one in Other, and the first one moves into the folder I made."
//
// Every step of that is this test: create the folder, create a session in it,
// read the listing the page reads immediately afterwards, then create a second
// session and read it again. The first listing used to put the new session
// under no folder, and the second used to show the two sessions swapping
// places. Workspace metadata must be visible on the first listing and remain
// stable when another direct PTY workspace is opened.
func TestSessionCreatedInAFolderIsInThatFolderImmediately(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}

	api.ok(http.MethodPost, "/terminal/folders", map[string]any{"name": "deploy"}, "")

	inFolder := api.create("migration", "deploy")
	if inFolder.Folder != "deploy" {
		t.Fatalf("the create response says folder=%q, want deploy", inFolder.Folder)
	}
	first := api.list()
	got := first.session(t, "migration")
	if got.Folder != "deploy" {
		t.Fatalf("the listing taken straight after the create puts the session in %q, want deploy", got.Folder)
	}

	// The second half of the report: opening an unrelated session must not
	// move the first one anywhere.
	api.create("scratch", "")
	second := api.list()
	if moved := second.session(t, "migration"); moved.Folder != "deploy" {
		t.Errorf("after a second session was opened, the first is in %q, want deploy", moved.Folder)
	}
	if scratch := second.session(t, "scratch"); scratch.Folder != "" {
		t.Errorf("the unfiled session landed in %q, want no folder", scratch.Folder)
	}
	if len(second.Sessions) != 2 {
		t.Errorf("the listing has %d sessions, want 2", len(second.Sessions))
	}
}

// Dragging a session into a folder sends the folder and nothing else. The
// server merges it onto what the session already has — the earlier shape,
// where the client echoed every field, erased whatever it had not looked at
// recently.
func TestPartialMetaUpdateKeepsTheRest(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("api server", "")

	path := "/terminal/" + created.ID
	api.ok(http.MethodPatch, path, map[string]any{"folder": "infra"}, "")
	after := api.list().session(t, "api server")
	if after.Folder != "infra" {
		t.Fatalf("folder = %q after the drag, want infra", after.Folder)
	}

	api.ok(http.MethodPatch, path, map[string]any{"favourite": true}, "")
	final := api.list().session(t, "api server")
	if final.Title != "api server" || final.Folder != "infra" {
		t.Errorf("a partial metadata change disturbed the rest: %+v", final)
	}
	if !final.Favourite {
		t.Errorf("the changes did not stick: %+v", final)
	}
}

// Renaming a folder moves everything filed under it, in one request. Doing it
// as a loop in the browser left half the sessions in a folder that no longer
// existed whenever the tab was closed midway.
func TestRenamingAFolderMovesItsSessions(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	api.ok(http.MethodPost, "/terminal/folders", map[string]any{"name": "staging"}, "")
	api.create("web", "staging")
	api.create("worker", "staging")

	api.ok(http.MethodPatch, "/terminal/folders/staging",
		map[string]any{"name": "production"}, "")

	after := api.list()
	names := []string{}
	for _, f := range after.Folders {
		names = append(names, f.Name)
	}
	if len(names) != 1 || names[0] != "production" {
		t.Fatalf("folders = %v, want [production]", names)
	}
	for _, title := range []string{"web", "worker"} {
		s := after.session(t, title)
		if s.Folder != "production" {
			t.Errorf("%s stayed in %q", title, s.Folder)
		}
	}
}

// Deleting a folder unfiles what was in it and closes nothing: it is filing,
// not a destructive action, which is why it carries no typed confirmation.
func TestDeletingAFolderUnfilesItsSessions(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	api.ok(http.MethodPost, "/terminal/folders", map[string]any{"name": "temp"}, "")
	api.create("shell", "temp")

	api.ok(http.MethodDelete, "/terminal/folders/temp", nil, "")

	after := api.list()
	if len(after.Folders) != 0 {
		t.Errorf("folders = %+v, want none", after.Folders)
	}
	s := after.session(t, "shell")
	if s.Folder != "" {
		t.Errorf("the session is still filed under %q", s.Folder)
	}
	if !s.Live {
		t.Error("deleting a folder must not touch what is running in it")
	}
}

// Two folders cannot share a name, however it is capitalised: the name is the
// key used by both the record and every workspace's metadata.
func TestFolderNamesAreUnique(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	api.ok(http.MethodPost, "/terminal/folders", map[string]any{"name": "deploy"}, "")
	rec := api.do(http.MethodPost, "/terminal/folders", map[string]any{"name": "Deploy"}, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("a duplicate folder = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

// Reordering is a whole-list replace, because that is what a drag produces:
// the client already holds the arrangement it wants.
func TestFolderOrderIsKept(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	for _, name := range []string{"a", "b", "c"} {
		api.ok(http.MethodPost, "/terminal/folders", map[string]any{"name": name}, "")
	}
	api.ok(http.MethodPut, "/terminal/folders", map[string]any{
		"folders": []map[string]any{{"name": "c"}, {"name": "a"}, {"name": "b"}},
	}, "")

	after := api.list()
	got := []string{}
	for _, f := range after.Folders {
		got = append(got, f.Name)
	}
	if strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("order = %v, want [c a b]", got)
	}
}

// Direct sessions contain independent PTY windows: no tmux persistence and no
// pane routes. The strip can create, rename, reorder and close them.
func TestDirectPTYWindowsCanBeNamedReorderedAndClosed(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("build", "")
	base := "/terminal/" + created.ID
	api.ok(http.MethodPatch, base, map[string]any{"favourite": true}, "")

	api.ok(http.MethodPost, base+"/windows", map[string]any{"name": "logs"}, "")
	api.ok(http.MethodPost, base+"/windows", map[string]any{"name": "commands"}, "")
	windows := fetchWindows(t, api, base)
	if len(windows) != 3 {
		t.Fatalf("windows = %d, want 3", len(windows))
	}

	api.ok(http.MethodPatch, base+"/windows/"+windows[0].ID,
		map[string]any{"name": "compile"}, "")
	windows = fetchWindows(t, api, base)
	if windows[0].Name != "compile" {
		t.Fatalf("rename did not stick: %+v", windows[0])
	}

	last := windows[2]
	api.ok(http.MethodPatch, base+"/windows/"+last.ID, map[string]any{"position": 0}, "")
	windows = fetchWindows(t, api, base)
	if windows[0].ID != last.ID {
		t.Errorf("after reorder first = %q, want %q", windows[0].ID, last.ID)
	}

	api.ok(http.MethodDelete, base+"/windows/"+windows[1].ID, nil, "")
	if got := fetchWindows(t, api, base); len(got) != 2 {
		t.Errorf("after closing one there are %d windows, want 2", len(got))
	}
	if workspace := api.list().session(t, "build"); !workspace.Favourite {
		t.Error("workspace metadata was lost when its original PTY window closed")
	}
	if rec := api.do(http.MethodGet, base+"/windows/0/panes", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("pane route = %d, want 404", rec.Code)
	}
}

func clipboardUploadRequest(t *testing.T, path, filename, mimeType string, body []byte) *http.Request {
	t.Helper()
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name": "file", "filename": filename,
	}))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &encoded)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestTerminalClipboardUploadUsesSessionDirectoryAndIgnoresMultipartPath(t *testing.T) {
	s, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("clipboard", "")
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	req := clipboardUploadRequest(t, "/terminal/"+created.ID+"/clipboard", "../../screenshot.png", "image/png", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	var uploaded struct {
		Path string `json:"path"`
		Name string `json:"name"`
		MIME string `json:"mime"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Dir(uploaded.Path)
	if filepath.Base(wantDir) != created.ID {
		t.Fatalf("path = %q, want a randomized file in a directory named %q", uploaded.Path, created.ID)
	}
	if uploaded.Name != "screenshot.png" || uploaded.MIME != "image/png" || uploaded.Size != int64(len(body)) {
		t.Errorf("response = %+v", uploaded)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(wantDir), "screenshot.png")); !os.IsNotExist(err) {
		t.Errorf("multipart traversal wrote outside the session directory: %v", err)
	}
	if err := s.modules.term.Kill(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wantDir); !os.IsNotExist(err) {
		t.Errorf("ending the terminal left its clipboard directory behind: %v", err)
	}
}

func TestTerminalClipboardUploadRejectsUnsupportedTypeAndMissingSession(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("clipboard-errors", "")

	gif := clipboardUploadRequest(t, "/terminal/"+created.ID+"/clipboard", "image.gif", "image/gif", []byte("GIF89a"))
	gifRec := httptest.NewRecorder()
	handler.ServeHTTP(gifRec, gif)
	if gifRec.Code != http.StatusUnsupportedMediaType || !strings.Contains(gifRec.Body.String(), "unsupported_image_type") {
		t.Fatalf("GIF upload = %d: %s", gifRec.Code, gifRec.Body.String())
	}

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	missing := clipboardUploadRequest(t, "/terminal/0000000000000000/clipboard", "image.png", "image/png", png)
	missingRec := httptest.NewRecorder()
	handler.ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusNotFound || !strings.Contains(missingRec.Body.String(), "terminal_session_not_found") {
		t.Fatalf("missing session upload = %d, want 404: %s", missingRec.Code, missingRec.Body.String())
	}
}

func TestTerminalClipboardUploadRejectsOversizedRequest(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("clipboard-too-large", "")
	body := make([]byte, term.MaxClipboardImageBytes+(2<<20))
	copy(body, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	req := clipboardUploadRequest(t, "/terminal/"+created.ID+"/clipboard", "large.png", "image/png", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "image_too_large") {
		t.Fatalf("oversized upload = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestTerminalClipboardUploadEnforcesDashboardSessionOwnership(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	created := api.create("owned", "")
	body := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	req := clipboardUploadRequest(t, "/terminal/"+created.ID+"/clipboard", "image.png", "image/png", body)
	req.Header.Set("X-Test-User", "somebody-else")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "terminal_session_owner") {
		t.Fatalf("foreign session upload = %d: %s", rec.Code, rec.Body.String())
	}
}

type testWindow struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Name  string `json:"name"`
}

func fetchWindows(t *testing.T, api apiCall, base string) []testWindow {
	t.Helper()
	rec := api.ok(http.MethodGet, base+"/windows", nil, "")
	var out []testWindow
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Nothing is called "shell": an unnamed session and its windows are numbered
// "Terminal", and the listing says which names the operator chose so the page
// knows which ones may follow what the shell is doing.
func TestUnnamedSessionsAndWindowsAreCalledTerminal(t *testing.T) {
	_, handler := terminalServer(t)
	api := apiCall{t, handler}
	rec := api.ok(http.MethodPost, "/terminal/", map[string]any{"rows": 24, "cols": 80}, "")
	var first struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Title != "Terminal" {
		t.Fatalf("first session is called %q, want Terminal", first.Title)
	}
	rec = api.ok(http.MethodPost, "/terminal/", map[string]any{"rows": 24, "cols": 80}, "")
	var second struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Title != "Terminal 2" {
		t.Fatalf("second session is called %q, want Terminal 2", second.Title)
	}

	base := "/terminal/" + first.ID
	api.ok(http.MethodPost, base+"/windows", nil, "")
	api.ok(http.MethodPost, base+"/windows", map[string]any{"name": "logs"}, "")
	rec = api.ok(http.MethodGet, base+"/windows", nil, "")
	var windows []struct {
		Name  string `json:"name"`
		Named bool   `json:"named"`
		Busy  bool   `json:"busy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &windows); err != nil {
		t.Fatal(err)
	}
	if len(windows) != 3 {
		t.Fatalf("windows = %+v, want 3", windows)
	}
	if windows[0].Name != "Terminal" || windows[0].Named {
		t.Errorf("first window = %+v, want an unnamed Terminal", windows[0])
	}
	if windows[1].Name != "Terminal 2" || windows[1].Named {
		t.Errorf("second window = %+v, want an unnamed Terminal 2", windows[1])
	}
	if windows[2].Name != "logs" || !windows[2].Named {
		t.Errorf("third window = %+v, want a named logs", windows[2])
	}

	listing := api.list()
	ws := listing.session(t, "Terminal")
	if ws.Named {
		t.Error("a default title counts as chosen")
	}
	if ws.Current == nil {
		t.Fatal("the listing names no current window")
	}
	api.ok(http.MethodPatch, base, map[string]any{"title": "build box"}, "")
	if ws := api.list().session(t, "build box"); !ws.Named {
		t.Error("a typed title does not count as chosen")
	}
}
