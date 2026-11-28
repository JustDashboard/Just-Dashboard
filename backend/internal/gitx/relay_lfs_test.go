package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Git LFS through the relay, against a repository served over HTTPS with a
// small LFS server beside it (the batch API and its basic transfers), both
// answering 401 without the token.

type lfsServer struct {
	mu      sync.Mutex
	objects map[string][]byte
	asked   []string
}

func (l *lfsServer) put(content string) string {
	sum := sha256.Sum256([]byte(content))
	oid := hex.EncodeToString(sum[:])
	l.mu.Lock()
	l.objects[oid] = []byte(content)
	l.mu.Unlock()
	return oid
}

func (l *lfsServer) has(oid string) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	content, ok := l.objects[oid]
	return content, ok
}

// lfsPointer is the pointer git commits for content.
func lfsPointer(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "version https://git-lfs.github.com/spec/v1\noid sha256:" + hex.EncodeToString(sum[:]) + "\nsize " + strconv.Itoa(len(content)) + "\n"
}

func (l *lfsServer) serve(w http.ResponseWriter, r *http.Request, base string) {
	l.mu.Lock()
	l.asked = append(l.asked, r.Method+" "+r.URL.Path)
	l.mu.Unlock()
	objects := base + "/acme/shop.git/info/lfs/objects/"
	switch {
	case !strings.Contains(r.URL.Path, ".git/info/lfs/"):
		// As GitHub and GitLab answer: the LFS API is beside <repository>.git.
		http.Error(w, "unprocessable", http.StatusUnprocessableEntity)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/info/lfs/objects/batch"):
		var batch struct {
			Operation string `json:"operation"`
			Objects   []struct {
				OID  string `json:"oid"`
				Size int64  `json:"size"`
			} `json:"objects"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answer := []map[string]any{}
		for _, object := range batch.Objects {
			entry := map[string]any{"oid": object.OID, "size": object.Size, "authenticated": true}
			action := map[string]any{"href": objects + object.OID, "header": map[string]string{"Authorization": r.Header.Get("Authorization")}}
			_, have := l.has(object.OID)
			switch {
			case batch.Operation == "download" && have:
				entry["actions"] = map[string]any{"download": action}
			case batch.Operation == "download":
				entry["error"] = map[string]any{"code": 404, "message": "not found"}
			case batch.Operation == "upload" && !have:
				entry["actions"] = map[string]any{"upload": action}
			}
			answer = append(answer, entry)
		}
		w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "objects": answer})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/info/lfs/objects/"):
		content, ok := l.has(filepath.Base(r.URL.Path))
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(content)
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/info/lfs/objects/"):
		content, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != filepath.Base(r.URL.Path) {
			http.Error(w, "content is not its id", http.StatusUnprocessableEntity)
			return
		}
		l.mu.Lock()
		l.objects[filepath.Base(r.URL.Path)] = content
		l.mu.Unlock()
	default:
		http.NotFound(w, r)
	}
}

// lfsRemote serves a bare repository whose main holds big.bin in Git LFS.
func lfsRemote(t *testing.T) (bare, remote, seed string, lfs *lfsServer) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if !lfsInstalled() {
		t.Skip("git-lfs is not installed")
	}
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "account-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	lfs = &lfsServer{objects: map[string][]byte{}}
	base := t.TempDir()
	bare = filepath.Join(base, "acme", "shop.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	relayGit(t, bare, "init", "--bare", "-q")
	relayGit(t, bare, "config", "http.receivepack", "true")
	seed = filepath.Join(t.TempDir(), "seed")
	relayGit(t, filepath.Dir(seed), "clone", "-q", bare, "seed")
	write(t, seed, ".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	write(t, seed, "app.js", "console.log('one')\n")
	lfs.put("the first large file\n")
	write(t, seed, "big.bin", lfsPointer("the first large file\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "Begin")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	execPath := relayGit(t, base, "--exec-path")
	backend := &cgi.Handler{
		Path: filepath.Join(execPath, "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + base, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "REMOTE_USER=deploy"},
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+relayToken))
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.Path, "/info/lfs/") {
			lfs.serve(w, r, server.URL)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return bare, server.URL + "/acme/shop.git", seed, lfs
}

func TestARelayCarriesGitLFSFilesBothWays(t *testing.T) {
	bare, remote, seed, lfs := lfsRemote(t)
	log := logEveryGit(t)
	ctx := context.Background()
	root := realDir(t, t.TempDir())
	base := filepath.Join(realDir(t, t.TempDir()), "workspace-git")
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, base, remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)

	// A clone: the LFS file is the file, and git reads it back as its pointer.
	said := []string{}
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{Note: func(text string) { said = append(said, text) }}, extra)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "big.bin")); string(raw) != "the first large file\n" {
		t.Fatalf("big.bin = %q, said %q", raw, said)
	}
	if len(said) != 1 || !strings.Contains(said[0], "1 Git LFS file was fetched") {
		t.Fatalf("the clone said %q", said)
	}
	if status := relayGit(t, folder, "status", "--porcelain"); status != "" {
		t.Fatalf("the clone is not clean: %s", status)
	}

	// A fetch that brings a new LFS object leaves it in the checkout's store.
	second := lfs.put("the second large file\n")
	write(t, seed, "more.bin", lfsPointer("the second large file\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "More")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	res, err := s.FetchThrough(ctx, relay, folder, extra)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(lfsPath(filepath.Join(folder, ".git", "lfs", "objects"), second)); err != nil || string(raw) != "the second large file\n" {
		t.Fatalf("the fetched LFS object = %q, %v (said %q)", raw, err, res.Output)
	}
	relayGit(t, folder, "merge", "-q", "--ff-only", "origin/main")
	if raw, _ := os.ReadFile(filepath.Join(folder, "more.bin")); string(raw) != "the second large file\n" {
		t.Fatalf("more.bin after the merge = %q", raw)
	}

	// A push of a commit with a new LFS file uploads it first.
	write(t, folder, "made.bin", "a large file made here\n")
	relayGit(t, folder, "add", "-A")
	relayGit(t, folder, "commit", "-q", "-m", "Made")
	pushed, err := s.PushThrough(ctx, relay, folder, "", extra)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("a large file made here\n"))
	if content, ok := lfs.has(hex.EncodeToString(sum[:])); !ok || string(content) != "a large file made here\n" {
		t.Fatalf("the pushed LFS object = %q, %v (said %q)", content, ok, pushed.Output)
	}
	if blob := relayGit(t, bare, "show", "main:made.bin"); !strings.Contains(blob, "oid sha256:"+hex.EncodeToString(sum[:])) {
		t.Fatalf("the remote's made.bin is %q, not its pointer", blob)
	}
	if !strings.Contains(pushed.Output, "1 Git LFS object was uploaded") {
		t.Fatalf("the push said %q", pushed.Output)
	}

	// Every git and git-lfs run with the credential ran in the relay.
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	credentialed, inCheckout := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) == 3 && within(parts[1], root) {
			inCheckout++
		}
		if len(parts) != 3 || parts[0] != "1" {
			continue
		}
		credentialed++
		if !within(parts[1], base) || within(parts[1], root) {
			t.Fatalf("a git with the credential ran in %s, outside the relay %s", parts[1], base)
		}
	}
	if credentialed == 0 || inCheckout == 0 {
		t.Fatalf("%d runs with the credential, %d in the checkout", credentialed, inCheckout)
	}
	lfs.mu.Lock()
	asked := strings.Join(lfs.asked, "\n")
	lfs.mu.Unlock()
	if !strings.Contains(asked, "PUT ") || !strings.Contains(asked, "GET ") {
		t.Fatalf("the LFS server was asked:\n%s", asked)
	}
}

// Without git-lfs on the server, a clone of a repository with LFS files leaves
// them as pointers, as the account's own clone did, and says so.
func TestWithoutGitLFSAClonesLFSFilesStayPointersAndItSaysSo(t *testing.T) {
	_, remote, _, _ := lfsRemote(t)
	installed := lfsInstalled
	lfsInstalled = func() bool { return false }
	t.Cleanup(func() { lfsInstalled = installed })
	ctx := context.Background()
	root := t.TempDir()
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	said := []string{}
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{Note: func(text string) { said = append(said, text) }}, relayCredential(remote))
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "big.bin")); string(raw) != lfsPointer("the first large file\n") {
		t.Fatalf("big.bin = %q", raw)
	}
	if len(said) != 1 || said[0] != lfsNotInstalled {
		t.Fatalf("the clone said %q", said)
	}
}
