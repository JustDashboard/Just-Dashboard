package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func newTestBoard(t *testing.T, c *client) (int64, string) {
	t.Helper()
	rec := c.do(http.MethodPost, "/api/v1/boards/", `{"name":"Files"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var b boardDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b.ID, fmt.Sprintf("/api/v1/boards/%d", b.ID)
}

func upload(c *client, path, fileID, contentType string, data []byte) *httptest.ResponseRecorder {
	return c.do(http.MethodPut, path+"/files/"+fileID, string(data), map[string]string{"Content-Type": contentType})
}

// saveScene saves a scene with the given elements and files JSON and answers
// the response; revision is the one the caller holds.
func saveScene(c *client, path string, revision int64, elements, files string, extra string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"name":"Files","revision":%d%s,"scene":{"elements":%s,"appState":{},"files":%s}}`, revision, extra, elements, files)
	return c.do(http.MethodPut, path, body, nil)
}

func decodeSaved(t *testing.T, rec *httptest.ResponseRecorder) boardSaved {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	var saved boardSaved
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func dataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func storedScene(t *testing.T, c *client, path string) (scene struct {
	Elements []map[string]any           `json:"elements"`
	Files    map[string]json.RawMessage `json:"files"`
}, fileIDs []string) {
	t.Helper()
	rec := c.do(http.MethodGet, path, "", nil)
	var b struct {
		Scene   json.RawMessage `json:"scene"`
		FileIDs []string        `json:"fileIds"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &b) != nil || json.Unmarshal(b.Scene, &scene) != nil {
		t.Fatalf("get = %d %.300s", rec.Code, rec.Body.String())
	}
	return scene, b.FileIDs
}

func TestBoardFilesUploadAndServe(t *testing.T) {
	admin, s := newClient(t)
	reader := &client{t: t, h: admin.h, cookie: signInAs(t, s, "filereader", auth.RoleReadOnly)}
	id, path := newTestBoard(t, admin)
	png := []byte("\x89PNG-bytes")

	if rec := upload(admin, path, "b_file-2", "image/png", png); rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != `{"id":"b_file-2"}` {
		t.Fatalf("first upload = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "b_file-2", "image/png", png); rec.Code != http.StatusOK {
		t.Fatalf("repeat upload = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "a1", "image/svg+xml; charset=utf-8", []byte("<svg/>")); rec.Code != http.StatusCreated {
		t.Fatalf("svg upload = %d %s", rec.Code, rec.Body.String())
	}

	rec := reader.do(http.MethodGet, path+"/files/b_file-2", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != string(png) {
		t.Fatalf("reader get = %d %q", rec.Code, rec.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/png",
		"Cache-Control":           "private, max-age=31536000, immutable",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := reader.do(http.MethodGet, path+"/files/a1", "", nil).Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("svg type = %q", got)
	}
	if _, ids := storedScene(t, admin, path); strings.Join(ids, ",") != "a1,b_file-2" {
		t.Errorf("fileIds = %v", ids)
	}

	if rec := upload(reader, path, "x", "image/png", png); rec.Code != http.StatusForbidden {
		t.Errorf("reader upload = %d %s", rec.Code, rec.Body.String())
	}
	for _, contentType := range []string{"text/html", "application/octet-stream", "application/json", "image/tiff", ""} {
		if rec := upload(admin, path, "x", contentType, png); rec.Code != http.StatusUnsupportedMediaType || !strings.Contains(rec.Body.String(), "board_file_type") {
			t.Errorf("type %q = %d %s", contentType, rec.Code, rec.Body.String())
		}
	}
	for _, fileID := range []string{"a.b", "a%20b", strings.Repeat("a", 129)} {
		if rec := upload(admin, path, fileID, "image/png", png); rec.Code != http.StatusBadRequest {
			t.Errorf("file id %q = %d %s", fileID, rec.Code, rec.Body.String())
		}
	}
	if rec := upload(admin, path, "x", "image/png", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("empty upload = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, "/api/v1/boards/9999", "x", "image/png", png); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "board_not_found") {
		t.Errorf("missing board = %d %s", rec.Code, rec.Body.String())
	}
	if rec := admin.do(http.MethodGet, path+"/files/nope", "", nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "board_file_not_found") {
		t.Errorf("missing file = %d %s", rec.Code, rec.Body.String())
	}
	other, otherPath := newTestBoard(t, admin)
	if rec := admin.do(http.MethodGet, otherPath+"/files/a1", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("another board's file = %d (board %d)", rec.Code, other)
	}

	if rec := upload(admin, path, "big", "image/png", make([]byte, maxBoardFileBytes+1)); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "board_file_too_large") {
		t.Errorf("oversized = %d %.200s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "edge", "image/png", make([]byte, maxBoardFileBytes)); rec.Code != http.StatusCreated {
		t.Errorf("largest file = %d %.200s", rec.Code, rec.Body.String())
	}

	var target string
	if err := s.Store.DB.QueryRow(`SELECT target FROM audit_log WHERE action = 'board.file.upload' AND success = 1 LIMIT 1`).Scan(&target); err != nil || target != "Files" {
		t.Errorf("audit target = %q (%v)", target, err)
	}
	_ = id
}

func TestBoardFilesQuotaAndCascade(t *testing.T) {
	admin, s := newClient(t)
	id, path := newTestBoard(t, admin)
	s.boards.maxFiles, s.boards.maxFileBytes = 3, 100

	if rec := upload(admin, path, "a", "image/png", make([]byte, 40)); rec.Code != http.StatusCreated {
		t.Fatalf("a = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "b", "image/png", make([]byte, 40)); rec.Code != http.StatusCreated {
		t.Fatalf("b = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "c", "image/png", make([]byte, 40)); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "board_files_too_large") {
		t.Fatalf("over the byte quota = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "a", "image/png", make([]byte, 40)); rec.Code != http.StatusOK {
		t.Fatalf("a file already held is not new = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "c", "image/png", make([]byte, 10)); rec.Code != http.StatusCreated {
		t.Fatalf("c = %d %s", rec.Code, rec.Body.String())
	}
	if rec := upload(admin, path, "d", "image/png", make([]byte, 1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the count quota = %d %s", rec.Code, rec.Body.String())
	}
	// Another board has its own quota.
	_, otherPath := newTestBoard(t, admin)
	if rec := upload(admin, otherPath, "a", "image/png", make([]byte, 40)); rec.Code != http.StatusCreated {
		t.Fatalf("other board = %d %s", rec.Code, rec.Body.String())
	}

	if rec := admin.do(http.MethodDelete, path, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	var mine, others int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM board_files WHERE board_id = ?`, id).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM board_files WHERE board_id != ?`, id).Scan(&others); err != nil {
		t.Fatal(err)
	}
	if mine != 0 || others != 1 {
		t.Errorf("files after delete: %d of the board's, %d of the other's", mine, others)
	}
}

func TestBoardSaveMovesEmbeddedImagesOut(t *testing.T) {
	admin, s := newClient(t)
	_, path := newTestBoard(t, admin)
	png, svg := []byte("\x89PNG-one"), []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")
	oversized := dataURL("image/png", make([]byte, maxBoardFileBytes+1))

	files := fmt.Sprintf(`{
		"png":{"id":"png","mimeType":"image/png","dataURL":%q,"created":1},
		"svg":{"id":"svg","mimeType":"image/svg+xml","dataURL":%q},
		"html":{"id":"html","dataURL":%q},
		"garbage":{"id":"garbage","dataURL":"data:image/png;base64,@@@@"},
		"nodata":{"id":"nodata","mimeType":"image/png"},
		"plain":"not an object",
		"has.dot":{"dataURL":%q},
		"big":{"id":"big","dataURL":%q}
	}`, dataURL("image/png", png), dataURL("image/svg+xml", svg), dataURL("text/html", []byte("<script>")),
		dataURL("image/png", []byte("x")), oversized)
	elements := `[{"id":"e1","type":"image","fileId":"png"},{"id":"e2","type":"image","fileId":"svg"},{"id":"e3","type":"image","fileId":"html"},{"id":"e4","type":"image","fileId":"big"}]`

	saved := decodeSaved(t, saveScene(admin, path, 1, elements, files, ""))
	if strings.Join(saved.FileIDs, ",") != "png,svg" || saved.Revision != 2 || saved.RemoteElements != nil {
		t.Fatalf("saved = %+v", saved)
	}
	scene, ids := storedScene(t, admin, path)
	if strings.Join(ids, ",") != "png,svg" {
		t.Errorf("fileIds = %v", ids)
	}
	for _, kept := range []string{"html", "garbage", "nodata", "plain", "has.dot", "big"} {
		if _, ok := scene.Files[kept]; !ok {
			t.Errorf("entry %q that does not validate was removed from the scene", kept)
		}
	}
	for _, moved := range []string{"png", "svg"} {
		if _, ok := scene.Files[moved]; ok {
			t.Errorf("entry %q stayed in the scene", moved)
		}
	}
	if len(scene.Elements) != 4 {
		t.Errorf("elements = %d", len(scene.Elements))
	}
	if rec := admin.do(http.MethodGet, path+"/files/png", "", nil); rec.Body.String() != string(png) || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("stored png = %q %s", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if rec := admin.do(http.MethodGet, path+"/files/svg", "", nil); rec.Body.String() != string(svg) || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("stored svg = %q %s", rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	// Sending the same image again stores nothing new and keeps the scene small.
	again := fmt.Sprintf(`{"png":{"dataURL":%q}}`, dataURL("image/png", png))
	saved = decodeSaved(t, saveScene(admin, path, 2, `[{"id":"e1","type":"image","fileId":"png"}]`, again, ""))
	if strings.Join(saved.FileIDs, ",") != "png,svg" {
		t.Errorf("fileIds after repeat = %v", saved.FileIDs)
	}

	// A board with no room keeps the image in its scene instead of losing it.
	s.boards.maxFiles = 2
	full := fmt.Sprintf(`{"late":{"dataURL":%q}}`, dataURL("image/png", []byte("late")))
	saved = decodeSaved(t, saveScene(admin, path, 3, `[{"id":"e5","fileId":"late"},{"id":"e1","fileId":"png"}]`, full, ""))
	scene, _ = storedScene(t, admin, path)
	if _, ok := scene.Files["late"]; !ok || len(saved.FileIDs) != 2 {
		t.Errorf("an image with no room: files %v, ids %v", scene.Files, saved.FileIDs)
	}
}

func TestBoardSaveCollectsUnreferencedFilesAfterTheGrace(t *testing.T) {
	admin, s := newClient(t)
	id, path := newTestBoard(t, admin)
	for _, fileID := range []string{"used", "deleted", "orphan", "fresh"} {
		if rec := upload(admin, path, fileID, "image/png", []byte(fileID)); rec.Code != http.StatusCreated {
			t.Fatalf("%s = %d %s", fileID, rec.Code, rec.Body.String())
		}
	}
	// "fresh" is one uploaded a moment ago by a tab whose scene has not been saved.
	if _, err := s.Store.DB.Exec(`UPDATE board_files SET created_at = created_at - 7200 WHERE board_id = ? AND file_id != 'fresh'`, id); err != nil {
		t.Fatal(err)
	}
	elements := `[{"id":"a","type":"image","fileId":"used"},{"id":"b","type":"image","fileId":"deleted","isDeleted":true}]`
	saved := decodeSaved(t, saveScene(admin, path, 1, elements, `{}`, ""))
	if got := strings.Join(saved.FileIDs, ","); got != "deleted,fresh,used" {
		t.Fatalf("fileIds = %s", got)
	}
	var left int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM board_files WHERE board_id = ?`, id).Scan(&left); err != nil || left != 3 {
		t.Errorf("rows = %d (%v)", left, err)
	}
	if rec := admin.do(http.MethodGet, path+"/files/orphan", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("collected file = %d", rec.Code)
	}

	// An hour on, the file nothing names goes too; a re-upload restarts its grace.
	if _, err := s.Store.DB.Exec(`UPDATE board_files SET created_at = created_at - 7200 WHERE board_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if rec := upload(admin, path, "fresh", "image/png", []byte("fresh")); rec.Code != http.StatusOK {
		t.Fatalf("re-upload = %d", rec.Code)
	}
	saved = decodeSaved(t, saveScene(admin, path, 2, elements, `{}`, ""))
	if got := strings.Join(saved.FileIDs, ","); got != "deleted,fresh,used" {
		t.Errorf("after the re-upload: %s", got)
	}
	saved = decodeSaved(t, saveScene(admin, path, 3, `[{"id":"a","type":"image","fileId":"used"}]`, `{}`, ""))
	if got := strings.Join(saved.FileIDs, ","); got != "fresh,used" {
		t.Errorf("a deleted element gone from the scene: %s", got)
	}
}
