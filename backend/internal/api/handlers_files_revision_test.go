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
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func TestFileWriteRevisionConflictUsesTheGuardedRoute(t *testing.T) {
	c, s := newClient(t)
	path := filepath.Join(fileFixture(t, s), "index.html")
	opened := c.do(http.MethodGet, query("/api/v1/files/read", map[string]string{"path": path}), "", nil)
	var content files.FileContent
	if err := json.Unmarshal(opened.Body.Bytes(), &content); err != nil {
		t.Fatal(err)
	}
	if content.Revision == "" {
		t.Fatal("read omitted revision")
	}
	if err := os.WriteFile(path, []byte("native edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"path": path, "content": "stale draft", "expectedRevision": content.Revision})
	refused := c.do(http.MethodPut, "/api/v1/files/write", string(body), nil)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "content_conflict") {
		t.Fatalf("%d %s", refused.Code, refused.Body)
	}
	current, _ := os.ReadFile(path)
	if string(current) != "native edit" {
		t.Fatalf("new content lost: %q", current)
	}
}

func TestImageRevisionRawAndUploadProtectLoadedBytes(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	path := filepath.Join(root, "logo.png")
	openedBytes := []byte("\x89PNG\x00opened")
	if err := os.WriteFile(path, openedBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	raw := query("/api/v1/files/raw", map[string]string{"path": path, "revision": "true"})
	opened := c.do(http.MethodGet, raw, "", nil)
	expected := opened.Header().Get("X-JD-Content-Revision")
	if opened.Code != http.StatusOK || expected != contentguard.RevisionBytes(opened.Body.Bytes()) || !bytes.Equal(opened.Body.Bytes(), openedBytes) {
		t.Fatalf("snapshot: %d %s revision=%q", opened.Code, opened.Body, expected)
	}
	if opened.Header().Get("Cache-Control") != "no-store" || !strings.Contains(opened.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("snapshot headers: %v", opened.Header())
	}
	if refused := c.do(http.MethodGet, raw, "", map[string]string{"Range": "bytes=0-2"}); refused.Code != http.StatusBadRequest {
		t.Fatalf("partial snapshot: %d %s", refused.Code, refused.Body)
	}
	if refused := c.do(http.MethodGet, query("/api/v1/files/raw", map[string]string{"path": filepath.Join(root, "index.html"), "revision": "true"}), "", nil); refused.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("HTML snapshot: %d %s", refused.Code, refused.Body)
	}
	upload := func(revision string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("file", "../../logo.png")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte("\x89PNG\x00draft"))
		mw.Close()
		url := query("/api/v1/files/upload", map[string]string{"path": root, "overwrite": "true", "expectedRevision": revision})
		req := httptest.NewRequest(http.MethodPost, url, &body)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("Cookie", c.cookie)
		req.Header.Set(httpx.CSRFHeader, "1")
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, req)
		return w
	}
	if err := os.WriteFile(path, []byte("native picture"), 0o640); err != nil {
		t.Fatal(err)
	}
	refused := upload(expected)
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "content_conflict") {
		t.Fatalf("stale image: %d %s", refused.Code, refused.Body)
	}
	current, _ := os.ReadFile(path)
	if string(current) != "native picture" {
		t.Fatalf("native image lost: %q", current)
	}
	fresh := c.do(http.MethodGet, raw, "", nil)
	saved := upload(fresh.Header().Get("X-JD-Content-Revision"))
	if saved.Code != http.StatusCreated {
		t.Fatalf("fresh upload: %d %s", saved.Code, saved.Body)
	}
	var response struct {
		Revisions map[string]string `json:"revisions"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	current, _ = os.ReadFile(path)
	if response.Revisions["logo.png"] != contentguard.RevisionBytes(current) {
		t.Fatalf("saved revision = %v", response.Revisions)
	}
	if refused := upload(expected); refused.Code != http.StatusConflict {
		t.Fatalf("reused revision: %d %s", refused.Code, refused.Body)
	}
	if refused := upload("bad revision"); refused.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision: %d %s", refused.Code, refused.Body)
	}
}
