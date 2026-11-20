package files

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitFetch(t *testing.T, f *Fetches, id string) Fetch {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := f.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "running" {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("fetch still running: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func noLeftovers(t *testing.T, dir string) {
	t.Helper()
	if left := stages(t, dir); len(left) != 0 {
		t.Fatalf("staging left behind: %v", left)
	}
}

func TestFetchSavesTheLinkAndSendsNothingElse(t *testing.T) {
	var mu sync.Mutex
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Disposition", `attachment; filename="release.tar.gz"`)
		w.Header().Set("Set-Cookie", "tracker=1")
		fmt.Fprint(w, "tarball bytes")
	}))
	defer srv.Close()
	root := t.TempDir()
	f := NewFetches(New([]string{root}))
	defer f.Close()

	started, err := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/dl?token=secret#frag"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(started.URL, "secret") || strings.Contains(started.URL, "frag") {
		t.Fatalf("the shown link keeps its query: %s", started.URL)
	}
	done := waitFetch(t, f, started.ID)
	if done.Status != "done" || done.Name != "release.tar.gz" || done.Received != 13 || done.Size != 13 {
		t.Fatalf("fetch %+v", done)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "release.tar.gz")); string(got) != "tarball bytes" {
		t.Fatalf("saved %q", got)
	}
	noLeftovers(t, root)
	mu.Lock()
	for key := range seen {
		if key != "User-Agent" {
			t.Errorf("the fetch sent a header of its own: %s: %v", key, seen[key])
		}
	}
	if seen.Get("User-Agent") != FetchUserAgent {
		t.Errorf("user agent %q", seen.Get("User-Agent"))
	}
	mu.Unlock()

	// The cookie the site set is not sent back on the next fetch.
	second, _ := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/again"})
	waitFetch(t, f, second.ID)
	mu.Lock()
	cookie := seen.Get("Cookie")
	mu.Unlock()
	if cookie != "" {
		t.Fatalf("a cookie was sent: %q", cookie)
	}
}

func TestFetchFollowsARedirectChainAndCountsIt(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
		switch {
		case r.URL.Path == "/files/tool.bin":
			fmt.Fprint(w, "binary")
		case r.URL.Path == "/forever":
			http.Redirect(w, r, "/forever", http.StatusFound)
		case r.URL.Path == "/to-file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case hop < 3:
			http.Redirect(w, r, fmt.Sprintf("/hop/%d", hop+1), http.StatusMovedPermanently)
		default:
			http.Redirect(w, r, srv.URL+"/files/tool.bin", http.StatusFound)
		}
	}))
	defer srv.Close()
	root := t.TempDir()
	f := NewFetches(New([]string{root}))
	defer f.Close()

	started, err := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/hop/0"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitFetch(t, f, started.ID)
	if done.Status != "done" || done.Name != "tool.bin" || done.Redirects != 4 {
		t.Fatalf("redirected fetch %+v", done)
	}
	for _, path := range []string{"/forever", "/to-file"} {
		started, err := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + path})
		if err != nil {
			t.Fatal(err)
		}
		if got := waitFetch(t, f, started.ID); got.Status != "failed" || got.Error == "" {
			t.Fatalf("%s: %+v", path, got)
		}
	}
	noLeftovers(t, root)
}

func TestFetchRefusesWhatIsNotAWebLinkOrNotAFolder(t *testing.T) {
	root := t.TempDir()
	f := NewFetches(New([]string{root}))
	defer f.Close()
	for _, link := range []string{"ftp://example.test/x", "file:///etc/passwd", "javascript:alert(1)", "not a link"} {
		if _, err := f.Start("a", FetchRequest{Dir: root, URL: link}); err == nil {
			t.Errorf("%q was accepted", link)
		}
	}
	if _, err := f.Start("a", FetchRequest{Dir: t.TempDir(), URL: "https://example.test/x"}); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("a folder outside the roots returned %v", err)
	}
	os.WriteFile(filepath.Join(root, "file"), nil, 0o644)
	if _, err := f.Start("a", FetchRequest{Dir: filepath.Join(root, "file"), URL: "https://example.test/x"}); err == nil {
		t.Error("a file as the folder was accepted")
	}
	if _, err := f.Start("a", FetchRequest{Dir: root, URL: "https://example.test/x", Name: ".."}); err == nil {
		t.Error("an unsafe name was accepted")
	}
}

func TestFetchDropsTheFolderPartOfAName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/traversal":
			w.Header().Set("Content-Disposition", `attachment; filename="../../etc/cron.d/evil"`)
		case "/dots":
			w.Header().Set("Content-Disposition", `attachment; filename=".."`)
		}
		fmt.Fprint(w, "x")
	}))
	defer srv.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "downloads")
	os.Mkdir(dir, 0o755)
	f := NewFetches(New([]string{root}))
	defer f.Close()

	started, _ := f.Start("a", FetchRequest{Dir: dir, URL: srv.URL + "/traversal"})
	if got := waitFetch(t, f, started.ID); got.Status != "done" || got.Name != "evil" {
		t.Fatalf("traversal name: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err != nil {
		t.Fatal("the file is not in the folder asked for")
	}
	started, _ = f.Start("a", FetchRequest{Dir: dir, URL: srv.URL + "/dots"})
	if got := waitFetch(t, f, started.ID); got.Status != "failed" {
		t.Fatalf("a name of dots was published: %+v", got)
	}
	started, _ = f.Start("a", FetchRequest{Dir: dir, URL: srv.URL + "/some%20dir/a%20file.txt"})
	if got := waitFetch(t, f, started.ID); got.Name != "a file.txt" {
		t.Fatalf("name from the link: %+v", got)
	}
	noLeftovers(t, dir)
}

func TestFetchNeverClobbersUnlessAsked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "fetched")
	}))
	defer srv.Close()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("mine"), 0o644)
	f := NewFetches(New([]string{root}))
	defer f.Close()
	started, _ := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/notes.txt"})
	if got := waitFetch(t, f, started.ID); got.Name != "notes (2).txt" {
		t.Fatalf("a taken name: %+v", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "notes.txt")); string(got) != "mine" {
		t.Fatal("the existing file was replaced")
	}
	started, _ = f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/notes.txt", Overwrite: true})
	waitFetch(t, f, started.ID)
	if got, _ := os.ReadFile(filepath.Join(root, "notes.txt")); string(got) != "fetched" {
		t.Fatal("overwrite did not replace")
	}
	if numbered("site.tar.gz", 2) != "site (2).tar.gz" || numbered(".env", 3) != ".env (3)" {
		t.Fatalf("numbered names %q %q", numbered("site.tar.gz", 2), numbered(".env", 3))
	}
}

func TestFetchCancelledOrCutOffLeavesNothing(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow.iso":
			w.Header().Set("Content-Length", "1000000")
			w.Write([]byte(strings.Repeat("a", 1000)))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/cut.iso":
			w.Header().Set("Content-Length", "1000000")
			w.Write([]byte(strings.Repeat("b", 5000)))
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()
	defer close(release)
	root := t.TempDir()
	f := NewFetches(New([]string{root}))
	defer f.Close()

	started, _ := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/slow.iso"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := f.Get(started.ID)
		if got.Received > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slow download never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(stages(t, root)) != 1 {
		t.Fatal("a running fetch is not staged")
	}
	if _, err := os.Stat(filepath.Join(root, "slow.iso")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a running fetch is visible under its name")
	}
	cancelled, err := f.Cancel(started.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	noLeftovers(t, root)

	started, _ = f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/cut.iso"})
	if got := waitFetch(t, f, started.ID); got.Status != "failed" || got.Error == "" {
		t.Fatalf("a cut connection: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "cut.iso")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a cut download was published")
	}
	noLeftovers(t, root)

	// A finished fetch is forgotten when dismissed.
	if _, err := f.Cancel(started.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(started.ID); !errors.Is(err, ErrFetchNotFound) {
		t.Fatal("a dismissed fetch is still listed")
	}
}

func TestFetchStopsBeforeTheDiskIsFull(t *testing.T) {
	big := strings.Repeat("z", 40<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/unsized" {
			// No length: the room is checked as it arrives.
			w.Header().Set("Transfer-Encoding", "chunked")
			for i := 0; i < len(big); i += 1 << 20 {
				w.Write([]byte(big[i : i+1<<20]))
			}
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(big)))
		w.Write([]byte(big))
	}))
	defer srv.Close()
	root := t.TempDir()
	free := uint64(64<<20 + 20<<20)
	restore := diskRoom
	diskRoom = func(*os.File) (uint64, uint64, error) { return free, 1 << 30, nil }
	t.Cleanup(func() { diskRoom = restore })
	f := NewFetches(New([]string{root}))
	defer f.Close()

	// 40 MiB into 20 MiB above the reserve: refused before a byte is staged.
	started, _ := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/sized.img"})
	got := waitFetch(t, f, started.ID)
	if got.Status != "failed" || !strings.Contains(got.Error, "room") {
		t.Fatalf("a sized download past the room: %+v", got)
	}
	noLeftovers(t, root)

	// Of unknown size: stopped while it arrives, once the room runs out.
	free = 64<<20 + 10<<20
	started, _ = f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/unsized"})
	got = waitFetch(t, f, started.ID)
	if got.Status != "failed" || !strings.Contains(got.Error, "before the disk filled") {
		t.Fatalf("an unsized download past the room: %+v", got)
	}
	noLeftovers(t, root)
	if _, err := os.Stat(filepath.Join(root, "unsized")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a stopped download was published")
	}
}

func TestFetchLimitsAndShownURL(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	root := t.TempDir()
	f := NewFetches(New([]string{root}))
	defer f.Close()
	for i := range fetchesPerOwner {
		if _, err := f.Start("a", FetchRequest{Dir: root, URL: fmt.Sprintf("%s/%d", srv.URL, i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Start("a", FetchRequest{Dir: root, URL: srv.URL + "/one-more"}); !errors.Is(err, ErrFetchLimit) {
		t.Fatalf("past the per-account bound: %v", err)
	}
	if len(f.List()) != fetchesPerOwner {
		t.Fatalf("list holds %d", len(f.List()))
	}
	if got := ShownURL("https://user:pw@example.test/a/b.zip?sig=abc#x"); got != "https://example.test/a/b.zip" {
		t.Fatalf("shown %q", got)
	}
}
