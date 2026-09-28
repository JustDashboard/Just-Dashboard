package proxysvc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// StaticCache has browsers keep a site's static files — styles, scripts,
// images, fonts — for MaxAge instead of asking again on every page.
//
// It is decided by the response's Content-Type rather than by a path or an
// extension, so the same switch works for files nginx serves itself and for
// ones an application sends, and a page is never kept by mistake.
type StaticCache struct {
	// MaxAge is nginx's own spelling of a time: 30d, 12h, 1y.
	MaxAge string `json:"maxAge"`
	// Immutable tells a browser the file will never change under its name,
	// so it does not ask again even on a reload. Right only for file names
	// that change when their content does (app.3f9c1.js).
	Immutable bool `json:"immutable,omitempty"`
}

// ProxyCache keeps the application's responses on disk and answers repeat
// requests from there.
//
// A request carrying an Authorization header is never answered from the
// cache nor stored in it: nginx on its own caches such a response and hands
// one user's page to the next. Cookies are treated the same unless
// CacheCookies says otherwise.
type ProxyCache struct {
	// MaxSize caps the cache on disk: 512m, 2g.
	MaxSize string `json:"maxSize"`
	// Valid is how long a 200, 301 or 302 is kept when the application's
	// own Cache-Control or Expires does not say; they take precedence.
	Valid string `json:"valid"`
	// ServeStale answers with the last copy it has while the application is
	// down or erroring, and refreshes an expired copy in the background.
	ServeStale bool `json:"serveStale,omitempty"`
	// CacheCookies caches requests that carry a cookie too. Off, any cookie
	// sends the request to the application, which is what keeps a logged-in
	// page out of the cache.
	CacheCookies bool `json:"cacheCookies,omitempty"`
}

// siteCacheRoot holds every site's proxy cache, one folder per site. It is
// shared between the dashboard's container and the host, so the dashboard
// can measure and empty what the host's nginx writes. A var so tests can
// move it.
var siteCacheRoot = "/var/lib/just-dashboard/nginx-cache"

var (
	cacheTimeRe = regexp.MustCompile(`^[1-9]\d{0,3}[smhdwMy]$`)
	cacheSizeRe = regexp.MustCompile(`^[1-9]\d{0,5}[mMgG]$`)
)

func (spec *SiteSpec) cacheZone() string { return NginxIdent(spec.Name) + "_cache" }

func (spec *SiteSpec) assetExpiresVar() string { return NginxIdent(spec.Name) + "_asset_expires" }

func (spec *SiteSpec) assetImmutableVar() string {
	return NginxIdent(spec.Name) + "_asset_immutable"
}

// cachesStatic and cachesProxy say whether the rendered site writes each
// cache. A redirect answers before either applies, and only a site that
// forwards has responses to keep.
func (spec *SiteSpec) cachesStatic() bool {
	return spec.StaticCache != nil && spec.Kind != "redirect"
}

func (spec *SiteSpec) cachesProxy() bool { return spec.ProxyCache != nil && spec.Kind == "proxy" }

// siteCacheDir is where a site's proxy cache lives.
func siteCacheDir(name string) string { return filepath.Join(siteCacheRoot, name) }

func validateCache(spec *SiteSpec) error {
	if c := spec.StaticCache; c != nil {
		if spec.Kind == "redirect" {
			return fmt.Errorf("a redirect has no files for browsers to keep")
		}
		if !cacheTimeRe.MatchString(c.MaxAge) {
			return fmt.Errorf("how long browsers keep static files must be a time like 30d or 12h")
		}
	}
	if c := spec.ProxyCache; c != nil {
		if spec.Kind != "proxy" {
			return fmt.Errorf("the proxy cache keeps an application's responses, and this site forwards to none")
		}
		if !cacheSizeRe.MatchString(c.MaxSize) {
			return fmt.Errorf("the cache size must be a size like 512m or 2g")
		}
		if !cacheTimeRe.MatchString(c.Valid) {
			return fmt.Errorf("how long responses are kept must be a time like 10m or 1h")
		}
		// nginx stores nothing it does not buffer, so a cache with buffering
		// off would be written and never used.
		if !spec.Buffering {
			return fmt.Errorf("the proxy cache needs response buffering on")
		}
	}
	return nil
}

// renderCacheObjects writes the http-level half: the Content-Type maps the
// static cache decides by, and the proxy cache's folder and zone.
func renderCacheObjects(l *lines, spec *SiteSpec) {
	if spec.cachesStatic() {
		c := spec.StaticCache
		l.add("# Static files: browsers keep styles, scripts, images and fonts for")
		l.add("# %s. Decided by Content-Type, so a page is never kept.", c.MaxAge)
		l.add("map $sent_http_content_type $%s {", spec.assetExpiresVar())
		l.add("    default                           off;")
		for _, pattern := range staticContentTypes {
			l.add("    %-33s %s;", pattern, c.MaxAge)
		}
		l.add("}")
		if c.Immutable {
			l.add("map $%s $%s {", spec.assetExpiresVar(), spec.assetImmutableVar())
			l.add("    off     \"\";")
			l.add("    default immutable;")
			l.add("}")
		}
	}
	if spec.cachesProxy() {
		if spec.cachesStatic() {
			l.blank()
		}
		l.add("# The proxy cache. nginx creates the folder and gives it to its workers;")
		l.add("# entries unused for a week are removed, and the oldest go first past")
		l.add("# the size.")
		l.add("proxy_cache_path %s levels=1:2 keys_zone=%s:10m max_size=%s inactive=7d use_temp_path=off;",
			siteCacheDir(spec.Name), spec.cacheZone(), spec.ProxyCache.MaxSize)
	}
}

// staticContentTypes are the types the static cache keeps.
var staticContentTypes = []string{
	"~^text/css",
	"~^(application|text)/javascript",
	"~^image/",
	"~^font/",
	"~^application/(font-woff|font-woff2|vnd.ms-fontobject|wasm)",
}

// cacheHeaders are the response headers the caches add. They are written
// with the site's other headers, and repeated with them in any location
// that sets one of its own, since nginx then drops the server's.
func cacheHeaders(l *lines, spec *SiteSpec) {
	if spec.cachesStatic() && spec.StaticCache.Immutable {
		l.add("    add_header Cache-Control $%s;", spec.assetImmutableVar())
	}
	if spec.cachesProxy() {
		l.add("    # HIT, MISS, BYPASS, EXPIRED, STALE or UPDATING, for checking the cache")
		l.add("    # from a browser or curl -I.")
		l.add("    add_header X-Cache-Status $upstream_cache_status always;")
	}
}

// renderServerCache writes the server-level half, which every location that
// forwards inherits. A path with buffering off is not cached.
func renderServerCache(l *lines, spec *SiteSpec) {
	if spec.cachesStatic() {
		l.add("    expires $%s;", spec.assetExpiresVar())
	}
	if c := spec.ProxyCache; spec.cachesProxy() {
		l.add("    proxy_cache %s;", spec.cacheZone())
		l.add("    # By the name asked for, so two domains of the site never share a page.")
		l.add("    proxy_cache_key $scheme$host$request_uri;")
		l.add("    proxy_cache_valid 200 301 302 %s;", c.Valid)
		l.add("    # One request fills a missing entry; the others wait for it.")
		l.add("    proxy_cache_lock on;")
		if c.ServeStale {
			l.add("    # The last copy answers while the application is down or failing,")
			l.add("    # and an expired one is refreshed behind the visitor's back.")
			l.add("    proxy_cache_use_stale error timeout updating http_500 http_502 http_503 http_504;")
			l.add("    proxy_cache_background_update on;")
		}
		skip := "$http_authorization"
		if !c.CacheCookies {
			skip += " $http_cookie"
		}
		l.add("    # A signed-in request is answered by the application and not stored.")
		l.add("    proxy_cache_bypass %s;", skip)
		l.add("    proxy_no_cache %s;", skip)
	}
	if spec.cachesStatic() || spec.cachesProxy() {
		l.blank()
	}
}

func cacheWarnings(spec *SiteSpec) []string {
	var warnings []string
	if spec.cachesProxy() {
		if spec.ProxyCache.CacheCookies {
			warnings = append(warnings,
				"Requests with cookies are cached: a page the application personalises by cookie, and does not mark private, is served to the next visitor.")
		}
		for _, loc := range spec.Locations {
			if loc.Buffering == "off" && !loc.servesFolder() {
				warnings = append(warnings, fmt.Sprintf(
					"%s has buffering off, so its responses are not cached.", loc.Path))
			}
		}
	}
	if spec.cachesStatic() && spec.StaticCache.Immutable {
		warnings = append(warnings,
			"Immutable tells browsers a static file never changes under its name. A file edited in place, such as style.css, stays old in browsers until the time runs out.")
	}
	return warnings
}

// cacheParse collects the caches' directives as the parser reads them.
type cacheParse struct {
	inExpires   bool
	maxAge      string
	maxSize     string
	expires     bool
	immutable   bool
	proxy       bool
	valid       string
	stale       bool
	skipsCookie bool
}

var (
	assetMapRe   = regexp.MustCompile(`^\$sent_http_content_type\s+\$jd_\w+_asset_expires\s*\{$`)
	cachePathRe  = regexp.MustCompile(`^/\S+\s.*keys_zone=jd_\w+_cache:`)
	maxSizeArgRe = regexp.MustCompile(`max_size=(\S+)`)
)

func (p *cacheParse) object(name, value string) {
	switch {
	case name == "map" && assetMapRe.MatchString(value):
		p.inExpires = true
	case name == "proxy_cache_path" && cachePathRe.MatchString(value):
		if m := maxSizeArgRe.FindStringSubmatch(value); m != nil {
			p.maxSize = m[1]
		}
	}
}

func (p *cacheParse) entry(raw string) {
	fields := strings.Fields(strings.TrimSuffix(raw, ";"))
	if p.inExpires && len(fields) == 2 && fields[0] != "default" && p.maxAge == "" {
		p.maxAge = fields[1]
	}
}

func (p *cacheParse) closed() { p.inExpires = false }

// directive reads a server-level cache directive. It says whether it was one.
func (p *cacheParse) directive(name, value, location string) bool {
	if location != "" {
		return false
	}
	switch name {
	case "expires":
		p.expires = strings.HasPrefix(value, "$jd_") && strings.HasSuffix(value, "_asset_expires")
	case "proxy_cache":
		p.proxy = strings.HasPrefix(value, "jd_") && strings.HasSuffix(value, "_cache")
	case "proxy_cache_valid":
		if fields := strings.Fields(value); len(fields) > 0 {
			p.valid = fields[len(fields)-1]
		}
	case "proxy_cache_use_stale":
		p.stale = value != "off"
	case "proxy_cache_bypass":
		p.skipsCookie = hasField(value, "$http_cookie")
	case "add_header":
		switch {
		case strings.HasPrefix(value, "Cache-Control $jd_"):
			p.immutable = true
		case strings.HasPrefix(value, "X-Cache-Status "):
		default:
			return false
		}
	case "proxy_cache_key", "proxy_cache_lock", "proxy_cache_background_update", "proxy_no_cache":
	default:
		return false
	}
	return true
}

// settle fills the spec's caches from what was read. One whose settings the
// file does not carry whole is left off, since the form has nothing to show
// for it.
func (p *cacheParse) settle(spec *SiteSpec) {
	if p.expires && cacheTimeRe.MatchString(p.maxAge) {
		spec.StaticCache = &StaticCache{MaxAge: p.maxAge, Immutable: p.immutable}
	}
	if p.proxy && cacheSizeRe.MatchString(p.maxSize) && cacheTimeRe.MatchString(p.valid) {
		spec.ProxyCache = &ProxyCache{
			MaxSize: p.maxSize, Valid: p.valid, ServeStale: p.stale, CacheCookies: !p.skipsCookie,
		}
	}
}

// ensureCacheRoot makes the folder the sites' caches go in. nginx makes a
// site's own folder when it reads the config, but not the folders above it,
// and a test that cannot is a failed save.
func ensureCacheRoot(spec *SiteSpec) error {
	if !spec.cachesProxy() {
		return nil
	}
	return os.MkdirAll(siteCacheRoot, 0o755)
}

// SiteCacheUsage is how much of the disk a site's proxy cache holds.
type SiteCacheUsage struct {
	Site  string `json:"site"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	// Exists says nginx has made the folder; a cache nothing has been
	// stored in yet, or one never turned on, has none.
	Exists bool `json:"exists"`
}

// openSiteCache opens a site's cache folder, refused when it or the folder
// above it is a link: the purge empties it, and a link would point that at
// whatever it leads to. Nothing to open is (nil, nil).
func openSiteCache(name string) (*os.Root, error) {
	if !siteNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid site name")
	}
	if resolved, err := filepath.EvalSymlinks(siteCacheRoot); err == nil && resolved != filepath.Clean(siteCacheRoot) {
		return nil, fmt.Errorf("%w: %s is a link", ErrUnsafePath, siteCacheRoot)
	}
	dir := siteCacheDir(name)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a plain folder", ErrUnsafePath, dir)
	}
	return os.OpenRoot(dir)
}

// SiteCacheUsage measures a site's proxy cache: the blocks its files take
// on disk, which is what fills it.
func (s *Service) SiteCacheUsage(name string) (*SiteCacheUsage, error) {
	root, err := openSiteCache(name)
	usage := &SiteCacheUsage{Site: name, Path: siteCacheDir(name)}
	if err != nil || root == nil {
		return usage, err
	}
	defer root.Close()
	usage.Exists = true
	err = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		usage.Files++
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			usage.Bytes += st.Blocks * 512
		} else {
			usage.Bytes += info.Size()
		}
		return nil
	})
	return usage, err
}

// PurgeSiteCache empties a site's proxy cache and keeps the folder, which
// nginx made for its workers. nginx takes an entry whose file is gone as a
// miss, so the next request goes to the application. Everything is removed
// through the folder's os.Root, which does not follow a link out of it.
func (s *Service) PurgeSiteCache(name string) (*SiteCacheUsage, error) {
	before, err := s.SiteCacheUsage(name)
	if err != nil || !before.Exists {
		return before, err
	}
	root, err := openSiteCache(name)
	if err != nil || root == nil {
		return before, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return before, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return before, err
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.Name()); err != nil {
			return before, err
		}
	}
	return before, nil
}
