package proxysvc

import (
	"bytes"
	"context"
	"fmt"
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
		if strings.HasPrefix(trimmed, effectiveMarker) && strings.HasSuffix(trimmed, ":") {
			flush()
			current = &ConfigFile{Path: strings.TrimSuffix(strings.TrimPrefix(trimmed, effectiveMarker), ":")}
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
	at    time.Time
	// gen moves on every change, so a dump that was running while a file
	// changed is returned to its caller but never cached as current.
	gen uint64
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
// Password files are left out even when a configuration includes one: the
// result is read by pages every signed-in account can open, which is the
// reason ReadConfig refuses them. It must not be called with s.mu held; it
// takes the lock so it never dumps a candidate that Validate has staged for
// the length of one test.
func (s *Service) EffectiveConfig(ctx context.Context) ([]ConfigFile, error) {
	cache := &s.effective
	cache.mu.Lock()
	if cache.files != nil && time.Since(cache.at) < effectiveTTL {
		files := append([]ConfigFile(nil), cache.files...)
		cache.mu.Unlock()
		return files, nil
	}
	gen := cache.gen
	cache.mu.Unlock()

	s.mu.Lock()
	files, err := s.dumpNginx(ctx)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	cache.mu.Lock()
	if cache.gen == gen {
		cache.files, cache.at = files, time.Now()
	}
	cache.mu.Unlock()
	return append([]ConfigFile(nil), files...), nil
}

// dumpNginx runs `nginx -T` and must be called with s.mu held.
func (s *Service) dumpNginx(ctx context.Context) ([]ConfigFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := hostexec.Command(ctx, "nginx", "-T")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return nil, fmt.Errorf("nginx -T: %s", message)
		}
		return nil, fmt.Errorf("nginx -T: %w", err)
	}
	files := []ConfigFile{}
	for _, file := range ParseEffective(stdout.String()) {
		if !s.isPasswordFile(file.Path) {
			files = append(files, file)
		}
	}
	return files, nil
}
