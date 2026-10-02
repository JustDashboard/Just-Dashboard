package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

func searchFixture(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"app.conf": "PORT=80\nport=443\nHOST=localhost\n",
		".secret":  "PORT=8080\n",
		"binary":   "PORT=80\x00",
		"wide.txt": strings.Repeat("界", 150) + "PORT=9000" + strings.Repeat("界", 150),
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "app.conf"), []byte("PORT=3000"), 0600); err != nil {
		t.Fatal(err)
	}
	return New([]string{root}), root
}

func TestSearchDetailedReturnsEachLineAndHonorsOptions(t *testing.T) {
	s, root := searchFixture(t)
	result, err := s.SearchDetailed(context.Background(), SearchOptions{
		Root: root, Query: "PORT", Content: true, AllLines: true, IgnoreCase: true, SkipHidden: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 3 || result.Truncated {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Hits[0].Line != 1 || result.Hits[1].Line != 2 {
		t.Fatalf("line numbers: %+v", result.Hits)
	}
	for _, hit := range result.Hits {
		if !utf8.ValidString(hit.Snippet) || len(hit.Ranges) == 0 {
			t.Fatalf("unusable snippet: %+v", hit)
		}
	}
	if !strings.Contains(result.Hits[2].Snippet, "PORT=9000") {
		t.Fatalf("match cut from long line: %+v", result.Hits[2])
	}
	legacy, err := s.Search(context.Background(), SearchOptions{Root: root, Query: "PORT", Content: true, IgnoreCase: true, SkipHidden: true})
	if err != nil || len(legacy) != 2 {
		t.Fatalf("legacy first-line behavior: %+v, %v", legacy, err)
	}
	sensitive, err := s.SearchDetailed(context.Background(), SearchOptions{Root: root, Query: "PORT", Content: true, AllLines: true})
	if err != nil || len(sensitive.Hits) != 4 {
		t.Fatalf("case-sensitive hidden results: %+v, %v", sensitive, err)
	}
}

func TestSearchReportsLimitsCancellationAndInvalidRegex(t *testing.T) {
	s, root := searchFixture(t)
	for _, opts := range []SearchOptions{
		{Root: root, Query: "PORT", Content: true, AllLines: true, Limit: 1},
		{Root: root, Query: "PORT", Content: true, AllLines: true, MaxVisits: 1},
	} {
		result, err := s.SearchDetailed(context.Background(), opts)
		if err != nil || !result.Truncated {
			t.Fatalf("partial result: %+v, %v", result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := s.SearchDetailed(ctx, SearchOptions{Root: root, Query: "PORT", Content: true})
	if err != nil || !result.Truncated {
		t.Fatalf("canceled result: %+v, %v", result, err)
	}
	_, err = s.SearchDetailed(context.Background(), SearchOptions{Root: root, Query: "[", Regex: true})
	if !errors.Is(err, ErrInvalidSearch) {
		t.Fatalf("invalid regex error: %v", err)
	}
}

func TestSearchDoesNotReadSymlinksSpecialFilesOrOutsideRoots(t *testing.T) {
	s, root := searchFixture(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("escaped"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.SearchDetailed(context.Background(), SearchOptions{Root: root, Query: "escaped", Content: true})
	if err != nil || len(result.Hits) != 0 {
		t.Fatalf("escaped result: %+v, %v", result, err)
	}
	_, err = s.SearchDetailed(context.Background(), SearchOptions{Root: outside, Query: "escaped", Content: true})
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside-root error: %v", err)
	}
}
