package deploy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// baseResolutionTTL is how long a reviewed base's tag → digest answer is
// reused without asking the registry again. Each preparation used to ask for
// every base (and for each Bun, Deno, site or language image it settles),
// 2–4 s a run, and a registry outage or Docker Hub's anonymous limit failed a
// deploy whose bases were all on this server. Ten minutes keeps a burst of
// deploys off the registry; a tag that moves is picked up by the next run
// after that, and the daily scan's newer-base check (image-vulnerabilities.md)
// still reads every release's bases against the registry itself.
const baseResolutionTTL = 10 * time.Minute

// baseResolutions remembers the last answer the registry gave for each
// reviewed base, shared by every preparation in the process. Entries are
// keyed by reference alone: the answer is the tag's index digest and the
// platforms it lists, the same for every platform asked about, and
// requireBasePlatform judges the target platform from that list.
//
// An answer older than the TTL is asked again. When the registry does not
// answer then — unreachable, rate-limited or failing with a 5xx — the last
// answer is used and the run says so, as a note. A registry that answers
// "no such tag" is believed. When a file is set the answers outlive the
// process, so a dashboard restarted during an outage still builds; the
// file holds public references and digests, nothing else.
type baseResolutions struct {
	mu      sync.Mutex
	entries map[string]baseResolution
	path    string
	now     func() time.Time
}

type baseResolution struct {
	Image ResolvedImage `json:"image"`
	At    time.Time     `json:"resolvedAt,omitzero"`
}

func newBaseResolutions() *baseResolutions {
	return &baseResolutions{entries: map[string]baseResolution{}, now: time.Now}
}

// WithBaseResolutionFile keeps the last answer for each base in path, read
// now and written after each fresh answer, so the fallback survives a
// restart. A missing or unreadable file starts empty.
func (b *ArtifactBuilder) WithBaseResolutionFile(path string) *ArtifactBuilder {
	if b == nil || b.bases == nil || path == "" {
		return b
	}
	b.bases.mu.Lock()
	defer b.bases.mu.Unlock()
	b.bases.path = path
	raw, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	stored := map[string]baseResolution{}
	if json.Unmarshal(raw, &stored) != nil {
		return b
	}
	for reference, entry := range stored {
		if _, known := b.bases.entries[reference]; !known && validateResolvedImage(entry.Image) == nil && !entry.At.IsZero() {
			b.bases.entries[reference] = entry
		}
	}
	return b
}

// resolveBase is the registry's answer for one reviewed base: the
// remembered one while it is fresh, the last one with a note when the
// registry does not answer. Preparation reads every catalogue image through
// it, never the backend directly.
func (b *ArtifactBuilder) resolveBase(ctx context.Context, reference string) (ResolvedImage, error) {
	if b.bases == nil {
		return b.backend.ResolveImage(ctx, reference, "")
	}
	image, note, err := b.bases.resolve(ctx, b.backend, reference)
	if note != "" && b.baseNotes != nil && !containsString(*b.baseNotes, note) {
		*b.baseNotes = append(*b.baseNotes, note)
	}
	return image, err
}

// withBaseNotes is the builder for one preparation, collecting the notes
// its base resolutions make.
func (b *ArtifactBuilder) withBaseNotes(notes *[]string) *ArtifactBuilder {
	if b == nil {
		return nil
	}
	copied := *b
	copied.baseNotes = notes
	return &copied
}

func (r *baseResolutions) resolve(ctx context.Context, backend BuildBackend, reference string) (ResolvedImage, string, error) {
	now := r.now()
	r.mu.Lock()
	entry, known := r.entries[reference]
	r.mu.Unlock()
	if known && !now.Before(entry.At) && now.Sub(entry.At) < baseResolutionTTL {
		return copyResolvedImage(entry.Image), "", nil
	}
	image, err := backend.ResolveImage(ctx, reference, "")
	if err == nil {
		if validateResolvedImage(image) == nil {
			r.remember(reference, image, now)
		}
		return image, "", nil
	}
	if !known || ctx.Err() != nil {
		return ResolvedImage{}, "", err
	}
	code := registryOutageCode(strings.ToLower(err.Error()))
	if code == "" {
		return ResolvedImage{}, "", err
	}
	return copyResolvedImage(entry.Image), baseFallbackNote(reference, code, entry.At), nil
}

func (r *baseResolutions) remember(reference string, image ResolvedImage, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[reference] = baseResolution{Image: copyResolvedImage(image), At: at.UTC()}
	if r.path == "" {
		return
	}
	raw, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return
	}
	// A cache: a write that fails leaves the previous file, and the next
	// fresh answer tries again.
	temporary, err := os.CreateTemp(filepath.Dir(r.path), ".base-resolutions-")
	if err != nil {
		return
	}
	_, writeErr := temporary.Write(append(raw, '\n'))
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil || os.Rename(temporary.Name(), r.path) != nil {
		_ = os.Remove(temporary.Name())
	}
}

func copyResolvedImage(image ResolvedImage) ResolvedImage {
	image.Platforms = append([]string{}, image.Platforms...)
	return image
}

// registryOutageCode names a registry that did not answer: Docker Hub's
// anonymous limit, a network that does not reach it, or a registry failing
// on its side. "" is any other answer, a missing tag or a refusal among
// them, which no remembered digest overrides.
func registryOutageCode(lower string) string {
	switch {
	case strings.Contains(lower, "toomanyrequests") || strings.Contains(lower, "pull rate limit") ||
		strings.Contains(lower, "429 too many requests"):
		return "registry_rate_limited"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "tls handshake timeout") || strings.Contains(lower, "temporary failure in name resolution") ||
		strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "client.timeout exceeded") ||
		strings.Contains(lower, "request canceled while waiting for connection") || strings.Contains(lower, "server misbehaving"):
		return "registry_unreachable"
	case strings.Contains(lower, "connection reset by peer") ||
		strings.Contains(lower, "500 internal server error") || strings.Contains(lower, "502 bad gateway") ||
		strings.Contains(lower, "503 service unavailable") || strings.Contains(lower, "504 gateway"):
		// The registry failing on its side. A failed build names it as it
		// always has (builderUnavailableFailure reads only the two above).
		return "registry_failing"
	}
	return ""
}

// baseFallbackNote is the run's note for a base taken from the last answer:
// "Docker Hub did not answer; used node:22-alpine as resolved at 14:02 UTC
// on 22 Nov 2026".
func baseFallbackNote(reference, code string, at time.Time) string {
	registry := registryDisplayName(reference)
	what := registry + " did not answer"
	if code == "registry_rate_limited" {
		what = registry + " refused this server's lookups for its rate limit"
	}
	return what + "; used " + reference + " as resolved at " + at.UTC().Format("15:04 UTC on 2 Jan 2006")
}

// registryDisplayName is the registry a reference is served from, as a
// person names it: Docker Hub for an unqualified one.
func registryDisplayName(reference string) string {
	first, _, qualified := strings.Cut(reference, "/")
	if !qualified || (!strings.ContainsAny(first, ".:") && first != "localhost") || first == "docker.io" {
		return "Docker Hub"
	}
	return first
}
