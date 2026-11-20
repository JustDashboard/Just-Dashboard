package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func findAnswer(t *testing.T, c *client, q map[string]string) findResponse {
	t.Helper()
	w := c.do(http.MethodGet, query("/api/v1/files/find", q), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("find: %d %s", w.Code, w.Body.String())
	}
	var answer findResponse
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

// With JD_FILES_INDEX off Find is the walk; with it on, the index answers and
// says how old it is, and the folder on screen is still walked live.
func TestFindAnswersFromTheIndexAndTheFolderOnScreen(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	os.MkdirAll(filepath.Join(root, "srv", "app"), 0o755)
	os.WriteFile(filepath.Join(root, "srv", "app", "invoice.go"), []byte("x"), 0o644)

	off := findAnswer(t, c, map[string]string{"q": "invoice", "path": root})
	if off.Source != "walk" || off.Index != nil || len(off.Hits) != 1 {
		t.Fatalf("with the index off: %+v", off)
	}

	s.modules.fileIndex = files.NewNameIndex(s.modules.hostFiles, s.Cfg.DataDir, time.Hour)
	if err := s.modules.fileIndex.Build(t.Context()); err != nil {
		t.Fatal(err)
	}
	here := filepath.Join(root, "etc")
	os.WriteFile(filepath.Join(here, "invoice-new.conf"), []byte("x"), 0o644)
	on := findAnswer(t, c, map[string]string{"q": "invoice", "path": root, "here": here})
	if on.Source != "index" || on.Index == nil || !on.Index.Complete {
		t.Fatalf("with the index on: %+v", on)
	}
	var names []string
	for _, h := range on.Hits {
		names = append(names, h.Rel)
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, filepath.Join("srv", "app", "invoice.go")) || !strings.Contains(got, filepath.Join("etc", "invoice-new.conf")) {
		t.Fatalf("hits: %s", got)
	}
	if info, err := os.Stat(filepath.Join(s.Cfg.DataDir, files.IndexFolderName)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the index folder: %v %v", info, err)
	}
}
