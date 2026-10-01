package dockerx

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func changesEngine(t *testing.T, files map[string]*tar.Header, content map[string][]byte) *Client {
	t.Helper()
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
		case strings.HasSuffix(r.URL.Path, "/containers/app/changes"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Path": "/data", "Kind": 1},
				{"Path": "/data/app.db", "Kind": 1},
				{"Path": "/etc/hosts", "Kind": 0},
				{"Path": "/tmp/gone.db", "Kind": 2},
			})
		case strings.HasSuffix(r.URL.Path, "/containers/app/archive"):
			path := r.URL.Query().Get("path")
			header, ok := files[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "no such file"})
				return
			}
			stat, _ := json.Marshal(map[string]any{"name": header.Name, "size": header.Size, "mode": 0o644})
			w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString(stat))
			tw := tar.NewWriter(w)
			_ = tw.WriteHeader(header)
			_, _ = tw.Write(content[path])
			_ = tw.Close()
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(engine.Close)
	c := New(engine.URL)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// What a container changed is what it added or modified. A deleted path is
// not a file to look into.
func TestChangedFilesLeavesOutDeletions(t *testing.T) {
	c := changesEngine(t, nil, nil)
	got, err := c.ChangedFiles(t.Context(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "/data,/data/app.db,/etc/hosts" {
		t.Errorf("changed = %v", got)
	}
}

// The head of a file is read without taking the file: a database of any size
// answers with its first bytes, its size and its time.
func TestContainerFileHeadReadsOnlyTheHead(t *testing.T) {
	large := append([]byte("SQLite format 3\x00"), bytes.Repeat([]byte{7}, 8<<20)...)
	c := changesEngine(t,
		map[string]*tar.Header{
			"/data/app.db": {Name: "app.db", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(large))},
			"/data/link":   {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow"},
			"/data/tiny":   {Name: "tiny", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3},
		},
		map[string][]byte{"/data/app.db": large, "/data/tiny": []byte("abc")},
	)
	head, size, _, err := c.ContainerFileHead(t.Context(), "app", "/data/app.db", 16)
	if err != nil {
		t.Fatal(err)
	}
	if string(head) != "SQLite format 3\x00" || size != int64(len(large)) {
		t.Errorf("head %q size %d", head, size)
	}
	if head, _, _, err := c.ContainerFileHead(t.Context(), "app", "/data/tiny", 16); err != nil || string(head) != "abc" {
		t.Errorf("a file shorter than the head: %q %v", head, err)
	}
	// A link planted in the container must not redirect the read.
	if _, _, _, err := c.ContainerFileHead(t.Context(), "app", "/data/link", 16); err == nil {
		t.Error("a symbolic link was read as a file")
	}
	for _, path := range []string{"relative.db", "/data/../etc/passwd", "/data//x.db"} {
		if _, _, _, err := c.ContainerFileHead(t.Context(), "app", path, 16); err == nil {
			t.Errorf("%q was accepted as a container path", path)
		}
	}
}
