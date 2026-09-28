package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ConfigFile is one file of the configuration nginx actually loads, as
// `nginx -T` prints it.
type ConfigFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

const effectiveMarker = "# configuration file "

// ParseEffective splits `nginx -T` output into the files it names, in the
// order nginx read them, the main configuration first.
//
// nginx writes each file as a marker line, the file's bytes, and one line
// feed of its own, so dropping that one line feed gives the file back exactly
// — with or without a newline at its end. Anything before the first marker is
// the test's own verdict, not configuration.
//
// nginx prints the files as they are, so a comment inside one can read like
// a marker. nginx always names the file by its full path, which is what tells
// the two apart for a comment such as "# configuration file for the app:";
// a comment that names an absolute path and ends in a colon is
// indistinguishable from a marker in nginx's format, and is taken for one.
func ParseEffective(output string) []ConfigFile {
	files := []ConfigFile{}
	var current *ConfigFile
	var content strings.Builder
	flush := func() {
		if current != nil {
			current.Content = strings.TrimSuffix(content.String(), "\n")
			files = append(files, *current)
		}
		content.Reset()
	}
	for _, line := range strings.SplitAfter(output, "\n") {
		trimmed := strings.TrimSuffix(line, "\n")
		if name, ok := strings.CutPrefix(trimmed, effectiveMarker); ok &&
			strings.HasSuffix(name, ":") && filepath.IsAbs(name) {
			flush()
			current = &ConfigFile{Path: strings.TrimSuffix(name, ":")}
			continue
		}
		if current != nil {
			content.WriteString(line)
		}
	}
	flush()
	return files
}

// effectiveTTL bounds how stale a cached `nginx -T` may be. Every change this
// service makes forgets the cache at once; the TTL is for the changes it does
// not see — an operator's editor, a package upgrade, certbot.
const effectiveTTL = 10 * time.Second

type effectiveCache struct {
	mu    sync.Mutex
	files []ConfigFile
	// at is when the cached dump was read.
	at time.Time
	// gen moves on every change, so a dump that was running while a file
	// changed is returned to its caller but never cached as current.
	gen uint64
	// running is the dump in flight, which every reader that finds the
	// cache stale waits for rather than starting its own.
	running *effectiveDump
}

type effectiveDump struct {
	done  chan struct{}
	files []ConfigFile
	at    time.Time
	err   error
}

// forgetEffective drops the cached dump. A caller that changes files calls it
// within the same hold of s.mu, before or after the change, so no dump can
// run between the two.
func (s *Service) forgetEffective() {
	s.effective.mu.Lock()
	s.effective.files = nil
	s.effective.gen++
	s.effective.mu.Unlock()
}

// EffectiveConfig is every file nginx loads, as nginx itself resolves the
// includes, cached for effectiveTTL.
//
// Only the files ReadConfig would show are returned: the result is read by
// pages every signed-in account can open. A password file, or a file that
// resolves outside the proxy's directories — certbot's options under
// /etc/letsencrypt, a module's load_module file under /usr/share — is left
// out even though nginx reads it, and so are its directives from NginxTree.
//
// The dump takes s.mu, so it never reads a candidate Validate has staged for
// the length of one test, and s.mu can be held for minutes by a certificate
// order. A reader therefore waits only as long as its ctx allows, and
// readers that arrive while a dump is pending share it, so a queue behind a
// long change runs nginx once. It must not be called with s.mu held.
func (s *Service) EffectiveConfig(ctx context.Context) ([]ConfigFile, error) {
	files, _, err := s.EffectiveConfigAt(ctx)
	return files, err
}

// EffectiveConfigAt is EffectiveConfig and when nginx printed it, which the
// cache can put up to effectiveTTL in the past.
func (s *Service) EffectiveConfigAt(ctx context.Context) ([]ConfigFile, time.Time, error) {
	cache := &s.effective
	cache.mu.Lock()
	if cache.files != nil && time.Since(cache.at) < effectiveTTL {
		files, at := append([]ConfigFile(nil), cache.files...), cache.at
		cache.mu.Unlock()
		return files, at, nil
	}
	dump := cache.running
	if dump == nil {
		dump = &effectiveDump{done: make(chan struct{})}
		cache.running = dump
		go s.runEffectiveDump(dump)
	}
	cache.mu.Unlock()

	select {
	case <-dump.done:
		if dump.err != nil {
			return nil, time.Time{}, dump.err
		}
		return append([]ConfigFile(nil), dump.files...), dump.at, nil
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
}

// runEffectiveDump runs one `nginx -T` for every reader waiting on it. It is
// detached from their requests, since one reader giving up must not fail the
// others; the single goroutine parked on s.mu is the whole cost of a reader
// that gave up. The result is published before s.mu is released, so no
// change can land between the dump and the cache.
func (s *Service) runEffectiveDump(dump *effectiveDump) {
	cache := &s.effective
	s.mu.Lock()
	defer s.mu.Unlock()
	cache.mu.Lock()
	gen := cache.gen
	cache.mu.Unlock()

	dump.at = time.Now()
	dump.files, dump.err = s.dumpNginx(context.Background())

	cache.mu.Lock()
	if dump.err == nil && cache.gen == gen {
		cache.files, cache.at = dump.files, dump.at
	}
	cache.running = nil
	cache.mu.Unlock()
	close(dump.done)
}

// DumpRefusedError is an `nginx -T` that nginx turned down: the configuration
// on disk fails its test, so there is nothing it loads to print. Validation is
// that test, placed as a config test's is.
type DumpRefusedError struct {
	Validation *ValidationResult
}

func (e *DumpRefusedError) Error() string {
	return "nginx -T: " + e.Validation.Output
}

// Unwrap is ErrInvalidConf: the refusal is about the configuration.
func (e *DumpRefusedError) Unwrap() error { return ErrInvalidConf }

// dumpNginx runs `nginx -T` and must be called with s.mu held.
func (s *Service) dumpNginx(ctx context.Context) ([]ConfigFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := hostexec.Command(ctx, "nginx", "-T")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && ctx.Err() == nil {
			// nginx's own refusal: it prints what it loads only for a
			// configuration that passes its test.
			res := &ValidationResult{Output: message, Command: "nginx -T"}
			res.diagnose("nginx")
			return nil, &DumpRefusedError{Validation: res}
		}
		if message != "" {
			return nil, fmt.Errorf("nginx -T: %s", message)
		}
		return nil, fmt.Errorf("nginx -T: %w", err)
	}
	files := []ConfigFile{}
	for i, file := range ParseEffective(stdout.String()) {
		full, err := s.allowedPath(file.Path)
		if err != nil && i == 0 {
			// NginxTree roots every include at the main file, and a tree
			// rooted at whichever file came next would be wrong without
			// saying so.
			return nil, fmt.Errorf("nginx loads %s, which is outside %s — set JD_NGINX_DIR to the directory of the nginx configuration", file.Path, s.nginxDir)
		}
		if err == nil && !s.isPasswordFile(full) {
			files = append(files, file)
		}
	}
	return files, nil
}

// PlacedDirective is one statement of the configuration nginx loads, without
// the statements inside it, for a search by directive: its words, where it is
// written, and the blocks it sits in as the configuration reads them.
type PlacedDirective struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
	File string   `json:"file"`
	Line int      `json:"line"`
	// Within names the enclosing blocks, outermost first, each with its
	// arguments — "http", "server app.example.com", "location /api". A server
	// block is named by its first server_name, which is how a reader tells
	// one from the next.
	Within []string `json:"within"`
	// Opens marks a directive that opens a block: "server", "location /api".
	Opens bool `json:"opens"`
}

// PlaceDirectives lists every directive of a tree NginxTree built, in the
// order nginx reads them.
func PlaceDirectives(tree []Directive) []PlacedDirective {
	out := []PlacedDirective{}
	var walk func(directives []Directive, within []string)
	walk = func(directives []Directive, within []string) {
		for _, d := range directives {
			out = append(out, PlacedDirective{
				Name: d.Name, Args: d.Args, File: d.File, Line: d.Line,
				Within: within, Opens: d.Block != nil,
			})
			if d.Block != nil {
				walk(d.Block, append(append([]string{}, within...), blockLabel(d)))
			}
		}
	}
	walk(tree, []string{})
	return out
}

// blockLabel is how a block is named in a directive's Within.
func blockLabel(d Directive) string {
	words := append([]string{d.Name}, d.Args...)
	if d.Name == "server" && len(d.Args) == 0 {
		for _, inner := range d.Block {
			if inner.Name == "server_name" && len(inner.Args) > 0 {
				words = append(words, inner.Args[0])
				break
			}
		}
	}
	return strings.Join(words, " ")
}
