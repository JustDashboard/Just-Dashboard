package proxysvc

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A site's maintenance and error pages are plain HTML files nginx serves from
// <nginxDir>/jd-pages/<site>/, one per page. They are files rather than text
// inside the site's configuration so that editing one needs no reload and no
// test, and so a page can be as long as it likes without the site file
// becoming a page of markup.

//go:embed pages/*.html
var defaultPages embed.FS

// SitePages are the pages a site can have, by the name its file carries.
var SitePages = []string{"maintenance", "404", "502", "503", "504", "security", "robots"}

// pageFileName is the file a page is kept in: security.txt and robots.txt
// are served as the text they are, every other page as HTML.
func pageFileName(page string) string {
	if page == "security" || page == "robots" {
		return page + ".txt"
	}
	return page + ".html"
}

// sitePageDefault is what a site gets for a page it has no file for.
// security.txt has to name a contact and an expiry (RFC 9116), so its
// default is written for the site: a year from now, and an address at its
// first domain the operator is told to check.
func sitePageDefault(spec *SiteSpec, page string) (string, error) {
	switch page {
	case "security":
		domain := strings.TrimPrefix(firstOr(spec.Domains, "example.com"), "*.")
		return "# Served at " + securityTxtPath + " (RFC 9116). Check that the contact\n" +
			"# reaches whoever handles security reports, and move Expires on before\n" +
			"# it passes: an expired file tells researchers to ignore it.\n" +
			"Contact: mailto:security@" + domain + "\n" +
			"Expires: " + time.Now().UTC().AddDate(1, 0, 0).Truncate(time.Second).Format(time.RFC3339) + "\n", nil
	case "robots":
		return "# Served at " + robotsTxtPath + ". Crawlers that honour it read it first;\n" +
			"# the ones that do not are what blocking by user agent is for.\n" +
			"User-agent: *\n" +
			"Disallow:\n", nil
	}
	return DefaultPage(page)
}

// MaxPageSize is the largest page accepted. A page nginx sends on every
// error is not the place for an inline video.
const MaxPageSize = 256 << 10

// ErrNotManaged refuses a quick change to a site the form did not write, or
// that holds lines the form would drop: the change is a save of the form's
// reading of the file.
var ErrNotManaged = errors.New("this site's file is not one the form can save without losing something")

func (s *Service) pagesRoot() string { return filepath.Join(s.nginxDir, "jd-pages") }

// SitePagesDir is the folder a site's pages are served from.
func (s *Service) SitePagesDir(name string) string {
	return filepath.Join(s.pagesRoot(), name)
}

// SetPagesDir points a spec's pages at this host's folder for them. A save
// does it itself; a preview calls it to render what the save would.
func (s *Service) SetPagesDir(spec *SiteSpec) {
	spec.PagesDir = ""
	if spec.usesPages() {
		spec.PagesDir = s.SitePagesDir(spec.Name)
	}
}

// DefaultPage is the page the dashboard ships for a page name.
func DefaultPage(page string) (string, error) {
	b, err := defaultPages.ReadFile("pages/" + page + ".html")
	if err != nil {
		return "", fmt.Errorf("there is no page called %s", page)
	}
	return string(b), nil
}

// pageFile is the file a site's page lives in, refused unless it resolves
// inside the pages folder and is not a link: a site folder replaced by a
// link to /etc would otherwise make a page save a write anywhere.
func (s *Service) pageFile(site, page string, create bool) (string, error) {
	if !siteNameRe.MatchString(site) || !slices.Contains(SitePages, page) {
		return "", fmt.Errorf("invalid page")
	}
	dir := s.SitePagesDir(site)
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	root, err := filepath.EvalSymlinks(s.pagesRoot())
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if filepath.Dir(resolved) != root {
		return "", fmt.Errorf("%w: %s", ErrUnsafePath, dir)
	}
	if _, err := s.allowedPath(resolved); err != nil {
		return "", err
	}
	full := filepath.Join(resolved, pageFileName(page))
	if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a plain file", ErrUnsafePath, full)
	}
	return full, nil
}

// SitePage is one of a site's pages as the editor shows it.
type SitePage struct {
	Site    string `json:"site"`
	Page    string `json:"page"`
	Content string `json:"content"`
	// Custom says the site has a file of its own for the page; without one
	// the content is the shipped default, which a save writes.
	Custom bool `json:"custom"`
}

// ReadSitePage reads a site's page, or the default when it has none yet.
func (s *Service) ReadSitePage(name, page string) (*SitePage, error) {
	spec, _, _, err := s.ReadSiteSpec(name)
	if err != nil {
		return nil, err
	}
	fallback, err := sitePageDefault(spec, page)
	if err != nil {
		return nil, err
	}
	out := &SitePage{Site: spec.Name, Page: page, Content: fallback}
	full, err := s.pageFile(spec.Name, page, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.Content, out.Custom = string(b), true
	return out, nil
}

// WriteSitePage saves a site's page. nginx reads the file on every response
// that uses it, so there is nothing to test and nothing to reload.
func (s *Service) WriteSitePage(name, page, content string) (*SitePage, error) {
	if len(content) > MaxPageSize {
		return nil, fmt.Errorf("a page may be at most %d KiB", MaxPageSize>>10)
	}
	spec, _, _, err := s.ReadSiteSpec(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	full, err := s.pageFile(spec.Name, page, true)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(full, content); err != nil {
		return nil, err
	}
	// Readable by nginx's workers whatever mode a file there had before.
	if err := os.Chmod(full, 0o644); err != nil {
		return nil, err
	}
	return &SitePage{Site: spec.Name, Page: page, Content: content, Custom: true}, nil
}

// writeMissingPages gives a site the default of every page its spec serves
// and it has no file for, so nginx never answers with a page that is not
// there. The caller holds s.mu.
func (s *Service) writeMissingPages(spec *SiteSpec) error {
	var pages []string
	if spec.Maintenance != nil {
		pages = append(pages, "maintenance")
	}
	for _, code := range spec.ErrorPages {
		pages = append(pages, strconv.Itoa(code))
	}
	if spec.SecurityTxt {
		pages = append(pages, "security")
	}
	if spec.RobotsTxt {
		pages = append(pages, "robots")
	}
	for _, page := range pages {
		full, err := s.pageFile(spec.Name, page, true)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(full); err == nil {
			continue
		}
		content, err := sitePageDefault(spec, page)
		if err != nil {
			return err
		}
		if err := writeAtomic(full, content); err != nil {
			return err
		}
		if err := os.Chmod(full, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// MaintenanceChange is a quick change to a site's maintenance switch. A nil
// RetryAfter or BypassFrom keeps what the site has.
type MaintenanceChange struct {
	On         bool      `json:"on"`
	RetryAfter *int      `json:"retryAfter"`
	BypassFrom *[]string `json:"bypassFrom"`
}

// SetMaintenance turns a site's maintenance on or off: the site as the form
// reads it, with the switch changed, saved and reloaded. Only a file the form
// wrote and would save whole, since anything else would lose lines to a click
// that says nothing about them.
func (s *Service) SetMaintenance(ctx context.Context, name string, change MaintenanceChange) (*SiteResult, error) {
	spec, managed, content, err := s.ReadSiteSpec(name)
	if err != nil {
		return nil, err
	}
	if !managed {
		return nil, fmt.Errorf("%w: it was written by hand, so open it in the form", ErrNotManaged)
	}
	file, err := s.SiteFile(spec.Name)
	if err != nil {
		return nil, err
	}
	dropped, err := s.SiteDrift(spec.Name, file.Path, content, nil)
	if err != nil {
		return nil, err
	}
	if len(dropped) > 0 {
		return nil, fmt.Errorf("%w: it holds lines added by hand the form would drop, so open it in the form", ErrNotManaged)
	}
	m := spec.Maintenance
	if m == nil {
		m = &SiteMaintenance{RetryAfter: 300, BypassFrom: []string{}}
	}
	m.On = change.On
	if change.RetryAfter != nil {
		m.RetryAfter = *change.RetryAfter
	}
	if change.BypassFrom != nil {
		m.BypassFrom = *change.BypassFrom
	}
	spec.Maintenance = m
	// The names are unchanged, so any conflict over them is one the site
	// already lives with; refusing the switch over it would keep the site
	// up only because another block shares a name.
	return s.SaveSite(ctx, spec, SiteSave{
		Reload: true, Overwrite: true, AllowConflict: true, BaseDigest: ContentDigest(content),
	})
}
