package files_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func TestFilesAndComposeCannotBothSaveTheSameOpenedRevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(path, []byte("opened"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("opened")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- files.New([]string{root}).WriteChecked(path, "files draft", &expected) }()
	go func() { <-start; results <- dockerx.WriteComposeFileChecked(path, "compose draft", &expected) }()
	close(start)
	ok, conflict := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			ok++
		} else if errors.Is(err, contentguard.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("saves=%d conflicts=%d", ok, conflict)
	}
}
