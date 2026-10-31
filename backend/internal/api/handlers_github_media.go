package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

const (
	forgeMediaLimit    = 25 << 20
	forgeMediaCacheMax = 64 << 20
	forgeMediaTTL      = time.Hour
	forgeMediaMissTTL  = 5 * time.Minute
	forgeMediaTimeout  = 30 * time.Second
	forgeMediaURLMax   = 2048
)

var (
	// github.com answers for far more than pictures, so only the paths that
	// hold what a request's body embeds are read there: an upload, or a file
	// of a repository.
	forgeMediaPath = regexp.MustCompile(`^/(user-attachments/assets/|[^/]+/[^/]+/(assets|raw|blob)/)`)
	// A redirect from an upload lands on GitHub's own storage buckets.
	forgeMediaBucket = regexp.MustCompile(`^github-production-[a-z0-9-]+\.s3\.amazonaws\.com$`)
	// One picture per read at a time is what a page of screenshots asks for,
	// and each read holds the whole file in memory, so they are bounded.
	forgeMediaGate = make(chan struct{}, 4)
	// Replaced by the tests, which have no route to GitHub.
	forgeMediaTransport http.RoundTripper = http.DefaultTransport
)

type forgeMediaEntry struct {
	body        []byte
	contentType string
	expires     time.Time
}

type forgeMediaCache struct {
	mu      sync.Mutex
	entries map[string]forgeMediaEntry
	size    int
}

func (c *forgeMediaCache) get(address string) (forgeMediaEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[address]
	if !ok || time.Now().After(entry.expires) {
		return forgeMediaEntry{}, false
	}
	return entry, true
}

func (c *forgeMediaCache) put(address string, entry forgeMediaEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bounded by bytes rather than entries, since a screenshot is fifty
	// avatars. Full, it is emptied: the honest failure of a cache is asking
	// GitHub again.
	if c.entries == nil || c.size+len(entry.body) > forgeMediaCacheMax || len(c.entries) >= 512 {
		c.entries, c.size = map[string]forgeMediaEntry{}, 0
	}
	if previous, ok := c.entries[address]; ok {
		c.size -= len(previous.body)
	}
	c.entries[address] = entry
	c.size += len(entry.body)
}

// isForgeMedia is the whole boundary of what this route will read: an HTTPS
// address on the hosts GitHub serves a request's images and videos from, with
// no credentials and no port.
func isForgeMedia(candidate *url.URL) bool {
	if candidate.Scheme != "https" || candidate.User != nil || candidate.Port() != "" {
		return false
	}
	host := candidate.Hostname()
	if host == "github.com" {
		return forgeMediaPath.MatchString(candidate.EscapedPath())
	}
	return strings.HasSuffix(host, ".githubusercontent.com")
}

// handleGitHubMedia serves an image or a video a pull request embeds, through
// this server, so the page can draw it.
//
// A description arrives holding addresses somebody else wrote. The page's
// policy is `img-src 'self'`, and keeping it means a comment cannot make the
// operator's browser fetch from anywhere it likes; this route is how the
// pictures get drawn without giving that up. It is not a general fetcher: the
// browser names the address, but `isForgeMedia` decides which addresses exist,
// redirects stay on GitHub, no credential is sent, and only a picture or a video
// comes back, served inert. So an address in a comment can reach GitHub's media
// hosts and nothing else, and a file in a private repository, which GitHub only
// gives to a signed-in reader, is an absence the page draws as a link.
func (s *Server) handleGitHubMedia(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	address, err := url.Parse(raw)
	if err != nil || len(raw) > forgeMediaURLMax || !isForgeMedia(address) {
		return httpx.BadRequest("that address is not one GitHub serves pull request media from")
	}
	key := address.String()
	entry, ok := s.forgeMedia.get(key)
	if !ok {
		select {
		case forgeMediaGate <- struct{}{}:
			defer func() { <-forgeMediaGate }()
		case <-r.Context().Done():
			return r.Context().Err()
		}
		entry = forgeMediaEntry{expires: time.Now().Add(forgeMediaTTL)}
		entry.body, entry.contentType, err = fetchForgeMedia(r.Context(), key)
		if err != nil {
			// A reader who left is not GitHub saying there is no such file.
			if r.Context().Err() != nil {
				return r.Context().Err()
			}
			entry.body, entry.contentType, entry.expires = nil, "", time.Now().Add(forgeMediaMissTTL)
		}
		s.forgeMedia.put(key, entry)
	}
	if entry.body == nil {
		return httpx.Err(http.StatusNotFound, "media_unavailable", "GitHub did not hand over that file")
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", "inline")
	// ServeContent for the ranges a video element asks for.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(entry.body))
	return nil
}

func fetchForgeMedia(ctx context.Context, address string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, forgeMediaTimeout)
	defer cancel()
	client := &http.Client{
		Transport: forgeMediaTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if !insideGitHub(req.URL) && !(req.URL.Scheme == "https" && forgeMediaBucket.MatchString(req.URL.Hostname())) {
				return errors.New("redirect leaves GitHub")
			}
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "image/*,video/*")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GitHub answered HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, forgeMediaLimit+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 || len(body) > forgeMediaLimit {
		return nil, "", fmt.Errorf("the file is empty or over %d bytes", forgeMediaLimit)
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if !servableForgeMedia(contentType) {
		contentType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	if !servableForgeMedia(contentType) {
		return nil, "", fmt.Errorf("%q is not a picture or a video", contentType)
	}
	return body, contentType, nil
}

func servableForgeMedia(contentType string) bool {
	return strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/")
}

// handleGitHubAuthors answers which GitHub account each commit email in this
// repository belongs to.
//
// A commit is a name and an email and git knows nothing else, so the history
// drew an initial where GitHub draws a face. GitHub has already matched those
// emails to accounts, so the recent commits are the map: one person's email
// does not change from commit to commit.
func (s *Server) handleGitHubAuthors(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	path, err := s.gitRepo(r)
	if err != nil {
		return err
	}
	authors, err := s.modules.github.CommitAuthors(r.Context(), path)
	if err != nil {
		return ghError(err)
	}
	httpx.JSON(w, http.StatusOK, authors)
	return nil
}
