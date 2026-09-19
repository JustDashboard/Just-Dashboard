package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

const (
	faviconPageLimit  = 512 << 10
	faviconImageLimit = 1 << 20
	faviconTTL        = time.Hour
	faviconMissTTL    = 10 * time.Minute
	faviconTimeout    = 5 * time.Second
)

var errFaviconMissing = errors.New("the website declares no icon the dashboard could read")

type faviconEntry struct {
	endpoint    string
	body        []byte
	contentType string
	expires     time.Time
}

// faviconCache remembers each project's icon for an hour, and its absence
// for ten minutes, so a fleet of cards polling every five seconds asks each
// website once rather than every time.
type faviconCache struct {
	mu      sync.Mutex
	entries map[int64]faviconEntry
}

func (c *faviconCache) get(id int64, endpoint string) (faviconEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok || entry.endpoint != endpoint || time.Now().After(entry.expires) {
		return faviconEntry{}, false
	}
	return entry, true
}

func (c *faviconCache) put(id int64, entry faviconEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[int64]faviconEntry{}
	}
	c.entries[id] = entry
}

// handleDeploymentFavicon serves the icon a deployed website declares, so a
// project card carries the site's own mark. The dashboard's page policy
// allows images from its own origin only, which is why the browser cannot
// read the icon from the site directly. The fetch is bound to the recorded
// website address — the same origin the preview frame may show — follows
// redirects only within that host, reads at most half a megabyte of page and
// one of icon, and never asks any other host, whatever the page links to.
func (s *Server) handleDeploymentFavicon(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	summary, err := s.modules.deployRuns.DeploymentSummary(r.Context(), id, deploy.QueueBudget{})
	if err != nil {
		return mapDeployError(err)
	}
	origin, err := deploymentPreviewURL(summary.Endpoint)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	entry, ok := s.favicons.get(id, origin.String())
	if !ok {
		entry = faviconEntry{endpoint: origin.String(), expires: time.Now().Add(faviconTTL)}
		entry.body, entry.contentType, err = fetchSiteIcon(r.Context(), origin)
		if err != nil {
			entry.body, entry.contentType, entry.expires = nil, "", time.Now().Add(faviconMissTTL)
		}
		s.favicons.put(id, entry)
	}
	if entry.body == nil {
		return httpx.Err(http.StatusNotFound, "favicon_unavailable", errFaviconMissing.Error())
	}
	// An SVG icon is a document if somebody opens the address directly; the
	// sandbox and the empty policy keep it a picture.
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(entry.body)
	return err
}

// sameSite is the boundary every request stays inside: the recorded
// scheme and host, port included, so a page on one port cannot send the
// dashboard to a service on another.
func sameSite(origin, candidate *url.URL) bool {
	return candidate.Scheme == origin.Scheme && candidate.Host == origin.Host
}

func siteClient(origin *url.URL) *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   faviconTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if !sameSite(origin, req.URL) {
				return errors.New("redirect leaves the website")
			}
			return nil
		},
	}
}

// fetchSiteIcon tries the icons the page declares, then the conventional
// paths, and returns the first that is an image.
func fetchSiteIcon(ctx context.Context, origin *url.URL) ([]byte, string, error) {
	client := siteClient(origin)
	defer client.CloseIdleConnections()
	page := *origin
	if page.Path == "" {
		page.Path = "/"
	}
	candidates := declaredIcons(ctx, client, &page)
	for _, conventional := range []string{"/favicon.ico", "/favicon.png", "/apple-touch-icon.png"} {
		candidates = append(candidates, page.ResolveReference(&url.URL{Path: conventional}))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		key := candidate.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		body, contentType, err := fetchImage(ctx, client, candidate)
		if err == nil {
			return body, contentType, nil
		}
	}
	return nil, "", errFaviconMissing
}

// declaredIcons reads the page's head for `<link rel="icon">` and its
// relatives, plain icons first, keeping only addresses on the page's own
// host. A page that will not parse, or is not HTML, declares nothing.
func declaredIcons(ctx context.Context, client *http.Client, page *url.URL) []*url.URL {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Accept", "text/html")
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	if kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type")); kind != "text/html" && kind != "application/xhtml+xml" {
		return nil
	}
	base := page
	if response.Request != nil && response.Request.URL != nil {
		base = response.Request.URL
	}
	var plain, touch []*url.URL
	tokenizer := html.NewTokenizer(io.LimitReader(response.Body, faviconPageLimit))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return append(plain, touch...)
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.Data == "body" {
				return append(plain, touch...)
			}
			if token.Data != "link" {
				continue
			}
			var rel, href string
			for _, attribute := range token.Attr {
				switch attribute.Key {
				case "rel":
					rel = strings.ToLower(attribute.Val)
				case "href":
					href = strings.TrimSpace(attribute.Val)
				}
			}
			if href == "" {
				continue
			}
			resolved, err := base.Parse(href)
			if err != nil || !sameSite(page, resolved) {
				continue
			}
			for _, word := range strings.Fields(rel) {
				if word == "icon" {
					plain = append(plain, resolved)
					break
				}
				if word == "apple-touch-icon" || word == "apple-touch-icon-precomposed" {
					touch = append(touch, resolved)
					break
				}
			}
		case html.EndTagToken:
			if token := tokenizer.Token(); token.Data == "head" {
				return append(plain, touch...)
			}
		}
	}
}

func fetchImage(ctx context.Context, client *http.Client, address *url.URL) ([]byte, string, error) {
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
		return nil, "", fmt.Errorf("%s answered HTTP %d", address.Host, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, faviconImageLimit+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 || len(body) > faviconImageLimit {
		return nil, "", fmt.Errorf("icon at %s is empty or over %d bytes", address.Path, faviconImageLimit)
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(body)
	}
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("%s is not an image", address.Path)
	}
	return body, contentType, nil
}
