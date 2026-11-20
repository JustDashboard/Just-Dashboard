package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

type versionsBody struct {
	Path     string `json:"path"`
	Keeping  bool   `json:"keeping"`
	TooLarge bool   `json:"tooLarge"`
	Backups  bool   `json:"backups"`
	Versions []struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		By      string `json:"by"`
		Reason  string `json:"reason"`
		Outside bool   `json:"outside"`
	} `json:"versions"`
}

func versionsClient(t *testing.T) (*client, *Server, string) {
	t.Helper()
	c, s := newClient(t)
	s.Cfg.FileVersions, s.Cfg.FileVersionsMB = 50, 512
	return c, s, fileFixture(t, s)
}

func versionsOf(t *testing.T, c *client, path string) versionsBody {
	t.Helper()
	w := c.do(http.MethodGet, query("/api/v1/files/versions", map[string]string{"path": path}), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("versions: %d %s", w.Code, w.Body)
	}
	var body versionsBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func saveThroughFiles(t *testing.T, c *client, path, content string, expected *string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"path": path, "content": content, "expectedRevision": expected})
	return c.do(http.MethodPut, "/api/v1/files/write", string(body), nil)
}

func versionText(t *testing.T, c *client, path, id string) string {
	t.Helper()
	w := c.do(http.MethodGet, query("/api/v1/files/versions/content", map[string]string{"path": path, "version": id}), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("content: %d %s", w.Code, w.Body)
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Content
}

func TestSavesKeepWhatTheyReplacedAndRestoreIsASave(t *testing.T) {
	c, s, root := versionsClient(t)
	path := filepath.Join(root, "etc", "nginx", "nginx.conf")
	original := "server {\n  listen 80;\n}\n"
	revision := contentguard.Revision(original)

	// A new file keeps nothing.
	fresh := filepath.Join(root, "new.conf")
	empty := ""
	if w := saveThroughFiles(t, c, fresh, "new", &empty); w.Code != http.StatusOK {
		t.Fatalf("new file: %d %s", w.Code, w.Body)
	}
	if got := versionsOf(t, c, fresh); len(got.Versions) != 0 || !got.Keeping {
		t.Fatalf("a new file has versions: %+v", got)
	}

	if w := saveThroughFiles(t, c, path, "server {\n  listen 8080;\n}\n", &revision); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	// Edited in a shell between two saves, then saved again: captured.
	if err := os.WriteFile(path, []byte("edited in a shell\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell := contentguard.Revision("edited in a shell\n")
	if w := saveThroughFiles(t, c, path, "saved again\n", &shell); w.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", w.Code, w.Body)
	}
	got := versionsOf(t, c, path)
	if len(got.Versions) != 3 || !got.Versions[0].Outside || got.Versions[0].By == "" || got.Versions[1].Reason != "displaced" {
		t.Fatalf("history: %+v", got.Versions)
	}
	first := got.Versions[2]
	if text := versionText(t, c, path, first.ID); text != original {
		t.Fatalf("first version: %q", text)
	}

	restore := func(id string, expected string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"path": path, "version": id, "expectedRevision": expected})
		return c.do(http.MethodPost, "/api/v1/files/versions/restore", string(body), nil)
	}
	// A stale revision is a conflict, exactly as a save's is.
	if w := restore(first.ID, revision); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "content_conflict") {
		t.Fatalf("stale restore: %d %s", w.Code, w.Body)
	}
	if current, _ := os.ReadFile(path); string(current) != "saved again\n" {
		t.Fatalf("a refused restore changed the file: %q", current)
	}
	w := restore(first.ID, contentguard.Revision("saved again\n"))
	if w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if current, _ := os.ReadFile(path); string(current) != original {
		t.Fatalf("restored: %q", current)
	}
	// The restore kept what it replaced, so it can be undone the same way.
	after := versionsOf(t, c, path)
	if len(after.Versions) != 4 || versionText(t, c, path, after.Versions[0].ID) != "saved again\n" {
		t.Fatalf("after restore: %+v", after.Versions)
	}
	_ = s
}

func TestAFailureToKeepAVersionDoesNotFailTheSave(t *testing.T) {
	c, s, root := versionsClient(t)
	// Something in the way of the folder versions are kept in.
	if err := os.WriteFile(filepath.Join(s.Cfg.DataDir, "file-versions"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "index.html")
	revision := contentguard.Revision("<script>alert(1)</script>")
	if w := saveThroughFiles(t, c, path, "saved", &revision); w.Code != http.StatusOK {
		t.Fatalf("the save failed with its version: %d %s", w.Code, w.Body)
	}
	if current, _ := os.ReadFile(path); string(current) != "saved" {
		t.Fatalf("saved: %q", current)
	}
}

func TestHistoryFollowsAMoveThroughTheDashboard(t *testing.T) {
	c, _, root := versionsClient(t)
	dir := filepath.Join(root, "etc", "nginx")
	path := filepath.Join(dir, "nginx.conf")
	revision := contentguard.Revision("server {\n  listen 80;\n}\n")
	if w := saveThroughFiles(t, c, path, "changed\n", &revision); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	body, _ := json.Marshal(map[string]any{"from": dir, "to": filepath.Join(root, "etc", "web")})
	if w := c.do(http.MethodPost, "/api/v1/files/move", string(body), nil); w.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	moved := filepath.Join(root, "etc", "web", "nginx.conf")
	if got := versionsOf(t, c, moved); len(got.Versions) != 1 {
		t.Fatalf("history stayed behind: %+v", got)
	}
	if got := versionsOf(t, c, path); len(got.Versions) != 0 {
		t.Fatalf("history at the old path: %+v", got)
	}
}

func TestVersionContentAnswersOnlyThisPathInsideTheRoots(t *testing.T) {
	c, _, root := versionsClient(t)
	path := filepath.Join(root, "index.html")
	revision := contentguard.Revision("<script>alert(1)</script>")
	if w := saveThroughFiles(t, c, path, "saved", &revision); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	id := versionsOf(t, c, path).Versions[0].ID

	outside := c.do(http.MethodGet, query("/api/v1/files/versions/content", map[string]string{"path": "/etc/passwd", "version": id}), "", nil)
	if outside.Code != http.StatusForbidden || !strings.Contains(outside.Body.String(), "outside_root") {
		t.Fatalf("outside the roots: %d %s", outside.Code, outside.Body)
	}
	if w := c.do(http.MethodGet, query("/api/v1/files/versions", map[string]string{"path": "/etc/passwd"}), "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("history outside the roots: %d %s", w.Code, w.Body)
	}
	other := filepath.Join(root, "etc", "nginx", "nginx.conf")
	if w := c.do(http.MethodGet, query("/api/v1/files/versions/content", map[string]string{"path": other, "version": id}), "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("another file's version: %d %s", w.Code, w.Body)
	}
	// HTML is never served inline, a version included.
	if w := c.do(http.MethodGet, query("/api/v1/files/versions/content", map[string]string{"path": path, "version": id, "as": "image"}), "", nil); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("HTML inline: %d %s", w.Code, w.Body)
	}
	download := c.do(http.MethodGet, query("/api/v1/files/versions/content", map[string]string{"path": path, "version": id, "as": "download"}), "", nil)
	if download.Code != http.StatusOK || download.Body.String() != "<script>alert(1)</script>" ||
		!strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment") || download.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download: %d %v", download.Code, download.Header())
	}
	// A backup's copy is restored behind the backups' own gate, and an edit
	// version is not restored there.
	body, _ := json.Marshal(map[string]any{"path": path, "version": id, "expectedRevision": contentguard.Revision("saved")})
	if w := c.do(http.MethodPost, "/api/v1/files/versions/restore-backup", string(body), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("edit version through the backup route: %d %s", w.Code, w.Body)
	}
}

func TestAPictureSavedOverItsOriginalKeepsTheOriginal(t *testing.T) {
	c, _, root := versionsClient(t)
	path := filepath.Join(root, "logo.png")
	original := []byte("\x89PNG\x00original")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "logo.png")
	part.Write([]byte("\x89PNG\x00edited"))
	mw.Close()
	url := query("/api/v1/files/upload", map[string]string{"path": root, "overwrite": "true", "expectedRevision": contentguard.RevisionBytes(original)})
	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set(httpx.CSRFHeader, "1")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	got := versionsOf(t, c, path)
	if len(got.Versions) != 1 || got.Versions[0].Reason != "image" {
		t.Fatalf("versions: %+v", got.Versions)
	}
	image := c.do(http.MethodGet, query("/api/v1/files/versions/content", map[string]string{"path": path, "version": got.Versions[0].ID, "as": "image"}), "", nil)
	if image.Code != http.StatusOK || !bytes.Equal(image.Body.Bytes(), original) || image.Header().Get("Content-Type") != "image/png" ||
		!strings.Contains(image.Header().Get("Content-Security-Policy"), "sandbox") || image.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("picture: %d %v", image.Code, image.Header())
	}
}
