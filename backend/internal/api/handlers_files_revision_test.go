package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
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
