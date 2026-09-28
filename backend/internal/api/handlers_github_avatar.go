package api

import (
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

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubapp"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

const (
	avatarImageLimit = 512 << 10
	avatarTTL        = time.Hour
	avatarMissTTL    = 10 * time.Minute
	avatarTimeout    = 5 * time.Second
	// The dashboard draws at most a handful of accounts — the App's
	// installations and whoever gh is signed in as — but the login arrives
	// from the browser, so the map is bounded rather than trusted to stay
	// small. Full, it is emptied: a cache is an optimisation, and the honest
	// failure of one is asking GitHub again.
	avatarCacheMax = 32
)

var errAvatarMissing = errors.New("GitHub has no picture for that account")

// githubWebURL is github.com, which serves every account's picture without an
// API call or a token. Tests point it at a local server; nothing else ever
// changes it — the same arrangement `githubapp.DefaultWebURL` documents.
var githubWebURL = githubapp.DefaultWebURL

// GitHub's own rule for a login: letters, digits and hyphens, no hyphen at
// either end, 39 characters at most. Applied before the address is built, so
// the path segment cannot be anything but an account name — no dot, no slash,
// no host, nothing that escapes the one address this route reads.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

type avatarEntry struct {
	body        []byte
	contentType string
	expires     time.Time
}

type avatarCache struct {
	mu      sync.Mutex
	entries map[string]avatarEntry
}

func (c *avatarCache) get(login string) (avatarEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[login]
	if !ok || time.Now().After(entry.expires) {
		return avatarEntry{}, false
	}
	return entry, true
}

func (c *avatarCache) put(login string, entry avatarEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= avatarCacheMax {
		c.entries = map[string]avatarEntry{}
	}
	c.entries[login] = entry
}

// handleGitHubAvatar serves an account's picture from GitHub through this
// server, so the page can draw it.
//
// The dashboard's page policy allows images from its own origin only
// (`frontend/src/proxy.ts`), which is the same reason a deployed site's icon
// is fetched here rather than by the browser. Widening it would have the
// operator's browser announce every view of the deploy pages to GitHub; this
// way one server-side request an hour per account does, and an install with no
// route to GitHub degrades to the initials the page already draws.
//
// The address is fixed — `github.com/<login>.png`, the picture every account
// serves without an API call or a token — and the login is checked against
// GitHub's own rule for one before it is built, so nothing the browser sends
// chooses a host. Redirects stay inside GitHub, which is what the answer is:
// github.com hands the request to avatars.githubusercontent.com.
func (s *Server) handleGitHubAvatar(w http.ResponseWriter, r *http.Request) error {
	login := strings.TrimSpace(r.URL.Query().Get("account"))
	if !githubLogin.MatchString(login) {
		return httpx.BadRequest("name the GitHub account whose picture to read")
	}
	entry, ok := s.avatars.get(login)
	if !ok {
		entry = avatarEntry{expires: time.Now().Add(avatarTTL)}
		var err error
		entry.body, entry.contentType, err = fetchGitHubAvatar(r.Context(), githubWebURL, login)
		if err != nil {
			entry.body, entry.contentType, entry.expires = nil, "", time.Now().Add(avatarMissTTL)
		}
		s.avatars.put(login, entry)
	}
	if entry.body == nil {
		return httpx.Err(http.StatusNotFound, "avatar_unavailable", errAvatarMissing.Error())
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(entry.body)
	return err
}

// insideGitHub is the boundary the fetch stays within: github.com and the
// host it serves pictures from, over HTTPS only.
func insideGitHub(candidate *url.URL) bool {
	if candidate.Scheme != "https" {
		return false
	}
	host := candidate.Hostname()
	return host == "github.com" ||
		host == "githubusercontent.com" ||
		strings.HasSuffix(host, ".githubusercontent.com")
}

func fetchGitHubAvatar(ctx context.Context, base, login string) ([]byte, string, error) {
	address, err := url.Parse(base)
	if err != nil {
		return nil, "", err
	}
	address = address.JoinPath(login + ".png")
	address.RawQuery = "size=80"
	client := &http.Client{
		Timeout: avatarTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if !insideGitHub(req.URL) {
				return errors.New("redirect leaves GitHub")
			}
			return nil
		},
	}
	defer client.CloseIdleConnections()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "image/*")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GitHub answered HTTP %d for %s", response.StatusCode, login)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, avatarImageLimit+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 || len(body) > avatarImageLimit {
		return nil, "", fmt.Errorf("the picture for %s is empty or over %d bytes", login, avatarImageLimit)
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(body)
	}
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("GitHub did not answer with a picture for %s", login)
	}
	return body, contentType, nil
}
