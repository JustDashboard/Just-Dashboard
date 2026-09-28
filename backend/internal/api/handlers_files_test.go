package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The file manager's read surface, driven through the whole chain with a real
// signed-in admin: the allowlist, the limiter, authentication, the capability
// group and the handler.
//
// Unit tests in internal/files pin what each of these decides. What they
// cannot say is whether the route was mounted inside the group it was meant to
// be in, which is the failure that ships — a preview route accidentally inside
// the file.write group is invisible to an admin and a 403 for everybody else.

// fileFixture writes a small tree into the server's configured root and
// returns it.
func fileFixture(t *testing.T, s *Server) string {
	t.Helper()
	root := s.modules.files.Roots()[0]
	if err := os.MkdirAll(filepath.Join(root, "etc", "nginx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "nginx", "nginx.conf"),
		[]byte("server {\n  listen 80;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"),
		[]byte("<script>alert(1)</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func query(path string, q map[string]string) string {
	values := url.Values{}
	for k, v := range q {
		values.Set(k, v)
	}
	return path + "?" + values.Encode()
}

func TestFileBrowsingRoutesAnswerAReader(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)

	for _, tc := range []struct{ name, path string }{
		{"places", "/api/v1/files/places"},
		{"complete", query("/api/v1/files/complete", map[string]string{"prefix": root + "/"})},
		{"find", query("/api/v1/files/find", map[string]string{"path": root, "q": "ngnx"})},
		{"preview", query("/api/v1/files/preview", map[string]string{"path": filepath.Join(root, "etc/nginx/nginx.conf")})},
		{"usage", query("/api/v1/files/usage", map[string]string{"path": root})},
		{"checksum", query("/api/v1/files/checksum", map[string]string{"path": filepath.Join(root, "index.html")})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := c.do(http.MethodGet, tc.path, "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("got %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
			}
			var body any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
		})
	}

	// The finder is the feature, not the plumbing: a typo'd query with the
	// characters in order has to find the file, or nobody uses it twice.
	w := c.do(http.MethodGet, query("/api/v1/files/find", map[string]string{"path": root, "q": "ngnxcnf"}), "", nil)
	var found files.FindResult
	if err := json.Unmarshal(w.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Hits) == 0 || filepath.Base(found.Hits[0].Path) != "nginx.conf" {
		t.Fatalf("fuzzy find returned %+v", found.Hits)
	}
}

// The page opens at home rather than at "/", and home has to be a path the
// very next request can list.
func TestPlacesStartsSomewhereListable(t *testing.T) {
	c, s := newClient(t)
	fileFixture(t, s)

	w := c.do(http.MethodGet, "/api/v1/files/places", "", nil)
	var places struct {
		Home   string        `json:"home"`
		Roots  []string      `json:"roots"`
		Places []files.Place `json:"places"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &places); err != nil {
		t.Fatal(err)
	}
	if places.Home == "" || len(places.Places) == 0 {
		t.Fatalf("places = %+v", places)
	}
	listing := c.do(http.MethodGet, query("/api/v1/files/list", map[string]string{"path": places.Home}), "", nil)
	if listing.Code != http.StatusOK {
		t.Fatalf("home %q does not list: %d %s", places.Home, listing.Code, listing.Body.String())
	}
	for _, p := range places.Places {
		if got := c.do(http.MethodGet, query("/api/v1/files/list", map[string]string{"path": p.Path}), "", nil); got.Code != http.StatusOK {
			t.Errorf("place %q (%s) does not list: %d", p.Path, p.Kind, got.Code)
		}
	}
}

// The raw route hands a file back with a content type the browser acts on, on
// the origin that holds the session. What it will serve is a closed list, and
// this is the test that keeps it closed.
func TestRawRefusesAnythingButMedia(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)

	w := c.do(http.MethodGet, query("/api/v1/files/raw", map[string]string{"path": filepath.Join(root, "index.html")}), "", nil)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("HTML was served inline: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, query("/api/v1/files/raw", map[string]string{"path": filepath.Join(root, "etc/nginx/nginx.conf")}), "", nil)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a config file was served inline: %d", w.Code)
	}

	png := filepath.Join(root, "logo.png")
	// The eight bytes of a PNG signature: enough for the route, which decides
	// on the name rather than on the content.
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodGet, query("/api/v1/files/raw", map[string]string{"path": png}), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("a PNG was refused: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("Content-Disposition = %q, want inline", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Errorf("CSP = %q, want the sandbox that neuters an inline SVG", got)
	}
}

// Containment reaches the new routes too. Every one of them takes a path from
// the client, and every one of them has to answer the same way to a path
// outside JD_FILE_ROOTS.
func TestNewFileRoutesRefusePathsOutsideTheRoots(t *testing.T) {
	c, _ := newClient(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		query("/api/v1/files/preview", map[string]string{"path": filepath.Join(outside, "secret.png")}),
		query("/api/v1/files/usage", map[string]string{"path": outside}),
		query("/api/v1/files/checksum", map[string]string{"path": filepath.Join(outside, "secret.png")}),
		query("/api/v1/files/raw", map[string]string{"path": filepath.Join(outside, "secret.png")}),
		query("/api/v1/files/find", map[string]string{"path": outside, "q": "secret"}),
	} {
		w := c.do(http.MethodGet, path, "", nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403 outside_root", path, w.Code)
		}
	}
}

// Bookmarks are a write, so they sit in the file.write group: a readonly
// session may read the rail and may not rearrange it for everybody else.
func TestBookmarksAreAWriteAndAreStored(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)

	body := `{"bookmarks":[{"path":"` + filepath.Join(root, "etc/nginx") + `","name":"nginx"}]}`
	if w := c.do(http.MethodPut, "/api/v1/files/bookmarks", body, nil); w.Code != http.StatusOK {
		t.Fatalf("saving a bookmark: %d %s", w.Code, w.Body.String())
	}
	w := c.do(http.MethodGet, "/api/v1/files/places", "", nil)
	var places struct {
		Bookmarks []struct{ Path, Name string } `json:"bookmarks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &places); err != nil {
		t.Fatal(err)
	}
	if len(places.Bookmarks) != 1 || places.Bookmarks[0].Name != "nginx" {
		t.Fatalf("bookmarks = %+v", places.Bookmarks)
	}
	// Stored resolved, so a bookmark can never be the way a path the roots
	// refuse gets remembered and offered.
	outside := t.TempDir()
	if w := c.do(http.MethodPut, "/api/v1/files/bookmarks",
		`{"bookmarks":[{"path":"`+outside+`"}]}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a bookmark outside the roots was accepted: %d", w.Code)
	}

	readonly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := readonly.do(http.MethodGet, "/api/v1/files/places", "", nil); w.Code != http.StatusOK {
		t.Fatalf("a reader cannot see the rail: %d", w.Code)
	}
	if w := readonly.do(http.MethodPut, "/api/v1/files/bookmarks", body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a reader rearranged the rail: %d %s", w.Code, w.Body.String())
	}
}

// A folder's colour is stored against its resolved path, follows the folder
// when it is renamed, is forgotten when it is deleted, and is a write.
func TestFolderColoursFollowTheFolder(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	colours := func() map[string]string {
		t.Helper()
		var places struct {
			Colours map[string]string `json:"colours"`
		}
		w := c.do(http.MethodGet, "/api/v1/files/places", "", nil)
		if err := json.Unmarshal(w.Body.Bytes(), &places); err != nil {
			t.Fatal(err)
		}
		return places.Colours
	}
	label := func(path, colour string) *httptest.ResponseRecorder {
		return c.do(http.MethodPut, "/api/v1/files/colours",
			`{"path":"`+path+`","colour":"`+colour+`"}`, nil)
	}

	etc := filepath.Join(root, "etc")
	nginx := filepath.Join(etc, "nginx")
	if w := label(nginx, "red"); w.Code != http.StatusOK {
		t.Fatalf("labelling a folder: %d %s", w.Code, w.Body.String())
	}
	if got := colours()[nginx]; got != "red" {
		t.Fatalf("colours = %+v", colours())
	}
	if w := label(nginx, "chartreuse"); w.Code != http.StatusBadRequest {
		t.Fatalf("a colour nothing can paint was accepted: %d", w.Code)
	}
	if w := label(t.TempDir(), "red"); w.Code != http.StatusForbidden {
		t.Fatalf("a folder outside the roots was labelled: %d", w.Code)
	}

	// Renaming the parent carries the label on the folder inside it.
	moved := filepath.Join(root, "config")
	if w := c.do(http.MethodPost, "/api/v1/files/move",
		`{"from":"`+etc+`","to":"`+moved+`"}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	now := colours()
	if _, stale := now[nginx]; stale || now[filepath.Join(moved, "nginx")] != "red" {
		t.Fatalf("the label did not follow the folder: %+v", now)
	}

	// Deleting it forgets the label, so a new folder of that name starts blue.
	target := filepath.Join(moved, "nginx")
	if w := c.do(http.MethodDelete, query("/api/v1/files/delete",
		map[string]string{"path": target, "recursive": "true"}),
		"", map[string]string{httpx.ConfirmHeader: "nginx"}); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if len(colours()) != 0 {
		t.Fatalf("a deleted folder kept its label: %+v", colours())
	}

	// Clearing is an empty colour.
	if w := label(root, "green"); w.Code != http.StatusOK {
		t.Fatalf("labelling: %d", w.Code)
	}
	if w := label(root, ""); w.Code != http.StatusOK || len(colours()) != 0 {
		t.Fatalf("clearing a label: %d %+v", w.Code, colours())
	}

	readonly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := readonly.do(http.MethodPut, "/api/v1/files/colours",
		`{"path":"`+root+`","colour":"red"}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a reader coloured a folder: %d %s", w.Code, w.Body.String())
	}
}

func TestDefaultFolderColourRecoloursEveryFolderAndKeepsIndividualChoice(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	nginx := filepath.Join(root, "etc", "nginx")
	putColour := func(path, colour string) *httptest.ResponseRecorder {
		return c.do(http.MethodPut, "/api/v1/files/colours",
			`{"path":"`+path+`","colour":"`+colour+`"}`, nil)
	}
	places := func() struct {
		DefaultColour string            `json:"defaultColour"`
		Colours       map[string]string `json:"colours"`
	} {
		t.Helper()
		var result struct {
			DefaultColour string            `json:"defaultColour"`
			Colours       map[string]string `json:"colours"`
		}
		w := c.do(http.MethodGet, "/api/v1/files/places", "", nil)
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if w := putColour(nginx, "red"); w.Code != http.StatusOK {
		t.Fatalf("labelling a folder: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPut, "/api/v1/files/colours/default", `{"colour":"yellow"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("setting the default: %d %s", w.Code, w.Body.String())
	}
	if got := places(); got.DefaultColour != "yellow" || len(got.Colours) != 0 {
		t.Fatalf("global colour did not replace folder labels: %+v", got)
	}
	if w := putColour(nginx, "green"); w.Code != http.StatusOK {
		t.Fatalf("labelling one folder after global choice: %d %s", w.Code, w.Body.String())
	}
	if got := places(); got.DefaultColour != "yellow" || got.Colours[nginx] != "green" {
		t.Fatalf("individual colour did not survive: %+v", got)
	}
	if w := c.do(http.MethodPut, "/api/v1/files/colours/default", `{"colour":"chartreuse"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown colour was accepted: %d", w.Code)
	}
	if got := places(); got.DefaultColour != "yellow" || got.Colours[nginx] != "green" {
		t.Fatalf("invalid choice changed colours: %+v", got)
	}
	readonly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := readonly.do(http.MethodPut, "/api/v1/files/colours/default", `{"colour":"blue"}`, nil); w.Code != http.StatusForbidden {
		t.Fatalf("a reader recoloured folders: %d %s", w.Code, w.Body.String())
	}
}

// The write verbs refuse to clobber unless told to. An upload used to
// overwrite by default and a move replaced the destination through rename(2),
// so "upload logo.png" and "move a.txt here" both quietly ate whatever held
// the name already; the page now hears 409 and asks.
func TestFileWritesRefuseToClobberWithoutOverwrite(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)

	upload := func(overwrite bool) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("file", "../../index.html")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("<b>replaced</b>")); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		path := query("/api/v1/files/upload", map[string]string{"path": root, "overwrite": strconv.FormatBool(overwrite)})
		req := httptest.NewRequest(http.MethodPost, path, &body)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("Cookie", c.cookie)
		req.Header.Set(httpx.CSRFHeader, "1")
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, req)
		return w
	}
	if w := upload(false); w.Code != http.StatusConflict {
		t.Fatalf("uploading over index.html without overwrite: %d %s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(root, "index.html")); string(b) != "<script>alert(1)</script>" {
		t.Fatalf("a refused upload changed the file: %q", b)
	}
	if w := upload(true); w.Code != http.StatusCreated {
		t.Fatalf("uploading with overwrite: %d %s", w.Code, w.Body.String())
	}
	// The filename's directory part was dropped rather than honoured.
	if b, _ := os.ReadFile(filepath.Join(root, "index.html")); string(b) != "<b>replaced</b>" {
		t.Fatalf("upload landed somewhere else: %q", b)
	}

	body := `{"from":"` + filepath.Join(root, "etc/nginx/nginx.conf") + `","to":"` + filepath.Join(root, "index.html") + `"}`
	if w := c.do(http.MethodPost, "/api/v1/files/move", body, nil); w.Code != http.StatusConflict {
		t.Fatalf("move onto an occupied name: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/files/move", `{"from":"","to":""}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("move with empty paths: %d", w.Code)
	}
	if w := c.do(http.MethodPost, "/api/v1/files/touch", `{"path":"`+filepath.Join(root, "index.html")+`"}`, nil); w.Code != http.StatusConflict {
		t.Fatalf("touch on an existing file: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/files/mkdir", `{"path":"`+filepath.Join(root, "etc")+`"}`, nil); w.Code != http.StatusConflict {
		t.Fatalf("mkdir on an existing folder: %d %s", w.Code, w.Body.String())
	}
	// An archive member outside its base is refused before any byte streams.
	archive := "/api/v1/files/archive?base=" + url.QueryEscape(filepath.Join(root, "etc")) +
		"&path=" + url.QueryEscape(filepath.Join(root, "index.html"))
	if w := c.do(http.MethodGet, archive, "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("archive member outside base: %d", w.Code)
	}
}
