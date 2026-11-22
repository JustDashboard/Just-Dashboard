package gitx

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// httpGitServer serves the repositories under root over git's smart HTTP
// protocol with git http-backend, recording the Authorization header of
// every request.
func httpGitServer(t *testing.T, root string) (*httptest.Server, func() []string) {
	t.Helper()
	backend := filepath.Join(gitExecPath(t), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git http-backend is not installed")
	}
	var mu sync.Mutex
	var seen []string
	handler := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

func gitExecPath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("git is not installed")
	}
	return strings.TrimSpace(string(out))
}

func TestAStreamedCloneCarriesItsHeaderOnlyInTheEnvironment(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "README.md", "hello\n")
	git("add", "-A")
	git("commit", "-qm", "first")
	server, seen := httpGitServer(t, filepath.Dir(dir))
	remote := server.URL + "/" + filepath.Base(dir)

	destination := t.TempDir()
	s := New([]string{destination})
	plan, err := s.PrepareClone(destination, remote, "copy", CloneOptions{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:secret-token-value"))
	extra := map[string]string{
		"GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": "",
		"GIT_CONFIG_KEY_1": "http." + remote + ".extraHeader", "GIT_CONFIG_VALUE_1": header,
	}
	var lines []string
	var mu sync.Mutex
	err = s.CloneStreaming(context.Background(), plan, extra, func(stream, text string) {
		mu.Lock()
		lines = append(lines, stream+": "+text)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("clone: %v\n%s", err, strings.Join(lines, "\n"))
	}
	if data, err := os.ReadFile(filepath.Join(plan.Target, "README.md")); err != nil || string(data) != "hello\n" {
		t.Fatalf("cloned file: %q, %v", data, err)
	}
	if !slices.Contains(seen(), strings.TrimPrefix(header, "Authorization: ")) {
		t.Fatalf("the server never saw the header: %q", seen())
	}
	if strings.Contains(plan.Command(), "secret-token-value") || !strings.HasPrefix(plan.Command(), "git clone --depth 1 -- ") {
		t.Fatalf("command = %q", plan.Command())
	}
	config, err := os.ReadFile(filepath.Join(plan.Target, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "secret-token-value") || strings.Contains(string(config), "extraHeader") || strings.Contains(string(config), base64.StdEncoding.EncodeToString([]byte("x-access-token:secret-token-value"))) {
		t.Fatalf(".git/config kept the credential:\n%s", config)
	}
	if !strings.Contains(string(config), "url = "+remote) {
		t.Fatalf(".git/config has no plain remote:\n%s", config)
	}
	for _, line := range lines {
		if strings.Contains(line, "secret-token-value") {
			t.Fatalf("a console line carries the token: %q", line)
		}
	}

	// The target exists now: the same plan, or a new one, is refused.
	if err := s.CloneStreaming(context.Background(), plan, nil, func(string, string) {}); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("second clone into the same target = %v", err)
	}
	if _, err := s.PrepareClone(destination, remote, "copy", CloneOptions{}); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("existing target = %v", err)
	}
}

func TestAStreamedCloneEndsWithGitsReason(t *testing.T) {
	root := t.TempDir()
	server, _ := httpGitServer(t, root)
	destination := t.TempDir()
	s := New([]string{destination})
	plan, err := s.PrepareClone(destination, server.URL+"/missing.git", "missing", CloneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = s.CloneStreaming(context.Background(), plan, nil, func(string, string) {})
	if err == nil || !strings.HasPrefix(err.Error(), "git clone failed: ") || strings.TrimPrefix(err.Error(), "git clone failed: ") == "" {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(plan.Target); !os.IsNotExist(err) {
		t.Fatalf("a failed clone left %s: %v", plan.Target, err)
	}
}

func TestPrepareCloneHoldsTheCloneRules(t *testing.T) {
	destination := t.TempDir()
	outside := t.TempDir()
	s := New([]string{destination})
	for _, c := range []struct{ parent, url, name string }{
		{destination, "https://github.com/octo/app.git", "../escape"},
		{destination, "https://github.com/octo/app.git", ".hidden"},
		{destination, "https://github.com/octo/app.git", "-option"},
		{destination, "https://github.com/octo/app.git", "a/b"},
		{outside, "https://github.com/octo/app.git", "app"},
		{destination + "/../" + filepath.Base(outside), "https://github.com/octo/app.git", "app"},
		{destination, "--upload-pack=touch /tmp/x", "app"},
	} {
		if plan, err := s.PrepareClone(c.parent, c.url, c.name, CloneOptions{}); err == nil {
			t.Errorf("PrepareClone(%q, %q, %q) = %+v, want a refusal", c.parent, c.url, c.name, plan)
		}
	}
	plan, err := s.PrepareClone(destination, "https://github.com/octo/app.git", "", CloneOptions{Branch: "main"})
	if err != nil || plan.Name != "app" || plan.Target != filepath.Join(destination, "app") {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if plan.Command() != "git clone --branch main -- https://github.com/octo/app.git app" {
		t.Fatalf("command = %q", plan.Command())
	}
}

func TestProgressIsKeptToOneLineASecondPerPhase(t *testing.T) {
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	input := strings.Builder{}
	input.WriteString("Cloning into 'app'...\n")
	steps := []struct {
		text    string
		advance time.Duration
	}{
		{"Receiving objects:   1% (1/100)\r", 0},
		{"Receiving objects:   2% (2/100)\r", 200 * time.Millisecond},
		{"Receiving objects:   3% (3/100)\r", 200 * time.Millisecond},
		{"Receiving objects:  50% (50/100)\r", 700 * time.Millisecond},
		{"Receiving objects:  60% (60/100)\r", 100 * time.Millisecond},
		{"Receiving objects: 100% (100/100), done.\n", 100 * time.Millisecond},
		{"Resolving deltas:  10% (1/10)\r", 0},
		{"Resolving deltas:  90% (9/10)\r", 100 * time.Millisecond},
	}
	chunks := []string{input.String()}
	advances := []time.Duration{0}
	for _, step := range steps {
		chunks = append(chunks, step.text)
		advances = append(advances, step.advance)
	}
	var got []string
	collapseProgress(&steppingReader{chunks: chunks, advances: advances, clock: &clock}, now, func(text string) { got = append(got, text) })
	want := []string{
		"Cloning into 'app'...",
		"Receiving objects:   1% (1/100)",
		"Receiving objects:  50% (50/100)",
		"Receiving objects: 100% (100/100), done.",
		"Resolving deltas:  10% (1/10)",
		"Resolving deltas:  90% (9/10)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// steppingReader hands out one chunk a Read, moving the clock on before
// each, so a redraw is processed at the time it was written.
type steppingReader struct {
	chunks   []string
	advances []time.Duration
	clock    *time.Time
}

func (r *steppingReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	*r.clock = r.clock.Add(r.advances[0])
	n := copy(p, r.chunks[0])
	r.chunks, r.advances = r.chunks[1:], r.advances[1:]
	return n, nil
}
