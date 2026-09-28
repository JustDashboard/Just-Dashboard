package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The visitor's address behind Cloudflare or a load balancer.
//
// Behind a proxy every request arrives from the proxy, so the access log,
// the allow list, the rate limits and the maintenance bypass all see the
// proxy's address instead of the visitor's. nginx's realip module puts the
// visitor's address back, taken from a header — but only from connections it
// is told to believe, since anyone can send the header.
//
// PROXY protocol is not offered: nginx turns it on for every server sharing
// the listen address and port, so one site asking for it on :443 would make
// every other site on :443 refuse its direct visitors.
//
// Cloudflare's ranges live in two files under jd-realip that every site
// trusting Cloudflare includes, so a refresh is one write, one test and one
// reload for all of them: cloudflare.conf holds set_real_ip_from lines, and
// cloudflare.geo the same ranges as geo entries for the Cloudflare-only lock.

// SiteRealIP says where a site's visitor address comes from.
type SiteRealIP struct {
	// Source is "cloudflare", which believes CF-Connecting-IP from
	// Cloudflare's published ranges, or "proxies", which believes Header
	// from the addresses in Trusted.
	Source  string   `json:"source"`
	Trusted []string `json:"trusted,omitempty"`
	// Header is X-Forwarded-For, X-Real-IP or another header the proxies
	// send. With X-Forwarded-For the address is the last one in the chain
	// that is not itself a trusted proxy.
	Header string `json:"header,omitempty"`
	// CloudflareOnly closes every connection that does not come from
	// Cloudflare, so the origin cannot be reached around it.
	CloudflareOnly bool `json:"cloudflareOnly,omitempty"`
}

var realIPHeaderRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

// realIPEdgeIfRe is the lock's check in a server block.
var realIPEdgeIfRe = regexp.MustCompile(`^if\s*\(\$jd_\w+_edge\s*=\s*0\)\s*\{$`)

func (spec *SiteSpec) realIPCloudflare() bool {
	return spec.RealIP != nil && spec.RealIP.Source == "cloudflare"
}

func (spec *SiteSpec) edgeVar() string { return NginxIdent(spec.Name) + "_edge" }

func validateRealIP(spec *SiteSpec) error {
	r := spec.RealIP
	if r == nil {
		return nil
	}
	switch r.Source {
	case "cloudflare":
		if len(r.Trusted) > 0 || r.Header != "" {
			return fmt.Errorf("the visitor's address behind Cloudflare comes from Cloudflare's own ranges and header")
		}
	case "proxies":
		if r.CloudflareOnly {
			return fmt.Errorf("only Cloudflare can be the only way in when the address comes from Cloudflare")
		}
		if len(r.Trusted) == 0 {
			return fmt.Errorf("name the proxies whose header is believed")
		}
		if len(r.Trusted) > 256 {
			return fmt.Errorf("at most 256 trusted proxies")
		}
		for _, entry := range r.Trusted {
			if err := validAddress(entry); err != nil {
				return fmt.Errorf("trusted proxy: %w", err)
			}
			// Believing everyone lets any visitor choose the address the
			// allow list and the limits see.
			if p, err := netip.ParsePrefix(entry); err == nil && p.Bits() == 0 {
				return fmt.Errorf("trusted proxy: %s would believe the header from anyone", entry)
			}
		}
		if !realIPHeaderRe.MatchString(r.Header) {
			return fmt.Errorf("the address header is a header name such as X-Forwarded-For")
		}
	default:
		return fmt.Errorf("the visitor's address comes from cloudflare or proxies")
	}
	return nil
}

func realIPWarnings(spec *SiteSpec) []string {
	if spec.RealIP == nil || !spec.RealIP.CloudflareOnly {
		return nil
	}
	return []string{"Only Cloudflare may reach this site: the domain has to be proxied (orange cloud), or every visitor and every certificate check over HTTP is turned away."}
}

// renderRealIPGeo writes the lock's list at http level. $realip_remote_addr
// is the address that connected, before the visitor's is put in its place.
func renderRealIPGeo(l *lines, spec *SiteSpec) {
	l.add("# Only Cloudflare may reach this site: 1 for its ranges, 0 for")
	l.add("# everyone else, whose connection the servers below close.")
	l.add("geo $realip_remote_addr $%s {", spec.edgeVar())
	l.add("    default 0;")
	l.add("    include %s;", filepath.Join(spec.RealIPDir, cloudflareGeoFile))
	l.add("}")
}

// renderRealIPLock closes a connection from outside Cloudflare. 444 answers
// nothing at all, so a scanner learns nothing about what is behind it.
func renderRealIPLock(l *lines, spec *SiteSpec) {
	if spec.RealIP == nil || !spec.RealIP.CloudflareOnly {
		return
	}
	l.add("    if ($%s = 0) {", spec.edgeVar())
	l.add("        return 444;")
	l.add("    }")
}

func renderRealIP(l *lines, spec *SiteSpec) {
	r := spec.RealIP
	if r == nil {
		return
	}
	if r.Source == "cloudflare" {
		l.add("    # The visitor's address is the one Cloudflare sends, believed only")
		l.add("    # from Cloudflare's ranges, which the Sites page refreshes.")
		l.add("    include %s;", filepath.Join(spec.RealIPDir, cloudflareConfFile))
		l.add("    real_ip_header CF-Connecting-IP;")
	} else {
		l.add("    # The visitor's address is taken from %s, believed only from", r.Header)
		l.add("    # these proxies; the header from anyone else is ignored.")
		for _, entry := range r.Trusted {
			l.add("    set_real_ip_from %s;", entry)
		}
		l.add("    real_ip_header %s;", r.Header)
		if strings.EqualFold(r.Header, "X-Forwarded-For") {
			// The last address that is not a trusted proxy, not the first:
			// the first is whatever the visitor chose to send.
			l.add("    real_ip_recursive on;")
		}
	}
	renderRealIPLock(l, spec)
	l.blank()
}

// realIPParse reads the realip lines of a site back.
type realIPParse struct {
	r *SiteRealIP
}

func (p *realIPParse) get() *SiteRealIP {
	if p.r == nil {
		p.r = &SiteRealIP{}
	}
	return p.r
}

// ifOpen reads the lock's check.
func (p *realIPParse) ifOpen(raw string) bool {
	if !realIPEdgeIfRe.MatchString(raw) {
		return false
	}
	p.get().CloudflareOnly = true
	return true
}

func (p *realIPParse) directive(spec *SiteSpec, name, value string, serverLevel bool) bool {
	if !serverLevel {
		return false
	}
	switch name {
	case "include":
		if filepath.Base(value) != cloudflareConfFile || filepath.Base(filepath.Dir(value)) != realIPDirName {
			return false
		}
		p.get().Source = "cloudflare"
		spec.RealIPDir = filepath.Dir(value)
	case "set_real_ip_from":
		p.get().Trusted = append(p.get().Trusted, value)
	case "real_ip_header":
		p.get().Header = value
	case "real_ip_recursive":
		// Written for X-Forwarded-For and implied by it.
	default:
		return false
	}
	return true
}

func (p *realIPParse) settle(spec *SiteSpec) {
	r := p.r
	if r == nil {
		return
	}
	switch {
	case r.Source == "cloudflare" || (len(r.Trusted) == 0 && r.CloudflareOnly):
		r.Source, r.Trusted, r.Header = "cloudflare", nil, ""
	case len(r.Trusted) > 0:
		r.Source = "proxies"
		if r.Header == "" {
			// nginx's own default.
			r.Header = "X-Real-IP"
		}
	default:
		// A header with no one to believe it from changes nothing.
		return
	}
	spec.RealIP = r
}

const (
	realIPDirName      = "jd-realip"
	cloudflareConfFile = "cloudflare.conf"
	cloudflareGeoFile  = "cloudflare.geo"
	// maxRangeList bounds each list Cloudflare answers with.
	maxRangeList = 64 << 10
)

// cloudflareRangeURLs are where Cloudflare publishes its ranges.
var cloudflareRangeURLs = []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"}

// builtinCloudflareRanges are the ranges Cloudflare published when this was
// written, used until the first refresh so a site can trust Cloudflare
// without waiting on a download.
var builtinCloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// cloudflareSourceRe is the second line of the file this dashboard writes.
var cloudflareSourceRe = regexp.MustCompile(`^# source=(cloudflare|built-in) fetched=(\S+)$`)

// CloudflareRanges is the list sites trusting Cloudflare include.
type CloudflareRanges struct {
	Path   string   `json:"path"`
	Ranges []string `json:"ranges"`
	// Source is "cloudflare" after a refresh, "built-in" before one, and
	// empty when no site has needed the file yet.
	Source string `json:"source"`
	// Fetched is when the ranges were downloaded, or written from the
	// built-in list.
	Fetched *time.Time `json:"fetched,omitempty"`
}

// RealIPDir is where the shared realip files live.
func (s *Service) RealIPDir() string { return filepath.Join(s.nginxDir, realIPDirName) }

// SetRealIPDir points a spec's includes at this host's realip files. Like
// PagesDir it is the service's, never a request's.
func (s *Service) SetRealIPDir(spec *SiteSpec) {
	spec.RealIPDir = ""
	if spec.RealIP != nil {
		spec.RealIPDir = s.RealIPDir()
	}
}

// realIPFiles are the two files, refused unless the folder resolves to
// jd-realip directly under the nginx directory and each file is a plain file
// or not there yet: a folder replaced by a link would make a refresh a write
// anywhere.
func (s *Service) realIPFiles(create bool) (conf, geo string, err error) {
	dir := s.RealIPDir()
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", err
		}
	}
	root, err := filepath.EvalSymlinks(s.nginxDir)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", err
	}
	if resolved != filepath.Join(root, realIPDirName) {
		return "", "", fmt.Errorf("%w: %s", ErrUnsafePath, dir)
	}
	conf, geo = filepath.Join(resolved, cloudflareConfFile), filepath.Join(resolved, cloudflareGeoFile)
	for _, full := range []string{conf, geo} {
		if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
			return "", "", fmt.Errorf("%w: %s is not a plain file", ErrUnsafePath, full)
		}
	}
	return conf, geo, nil
}

// ReadCloudflareRanges says what the shared list holds and where it came from.
func (s *Service) ReadCloudflareRanges() (*CloudflareRanges, error) {
	out := &CloudflareRanges{Path: filepath.Join(s.RealIPDir(), cloudflareConfFile), Ranges: []string{}}
	conf, _, err := s.realIPFiles(false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(conf)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if m := cloudflareSourceRe.FindStringSubmatch(line); m != nil {
			out.Source = m[1]
			if t, err := time.Parse(time.RFC3339, m[2]); err == nil {
				out.Fetched = &t
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "set_real_ip_from "); ok {
			out.Ranges = append(out.Ranges, strings.TrimSuffix(v, ";"))
		}
	}
	return out, nil
}

// cloudflareFiles renders the two files from a list of ranges.
func cloudflareFiles(ranges []string, source string, at time.Time) (conf, geo string) {
	head := fmt.Sprintf("%s\n# Cloudflare's proxy ranges, shared by every site that trusts Cloudflare.\n# source=%s fetched=%s\n",
		managedMarker, source, at.UTC().Format(time.RFC3339))
	var c, g strings.Builder
	c.WriteString(head)
	g.WriteString(head)
	for _, r := range ranges {
		fmt.Fprintf(&c, "set_real_ip_from %s;\n", r)
		fmt.Fprintf(&g, "%s 1;\n", r)
	}
	return c.String(), g.String()
}

// ensureCloudflareRanges writes the built-in list when a site needs the
// files and they are not there yet, since nginx refuses an include of a
// file that does not exist. The caller holds s.mu.
func (s *Service) ensureCloudflareRanges(spec *SiteSpec) error {
	if !spec.realIPCloudflare() {
		return nil
	}
	conf, geo, err := s.realIPFiles(true)
	if err != nil {
		return err
	}
	_, confErr := os.Lstat(conf)
	_, geoErr := os.Lstat(geo)
	if confErr == nil && geoErr == nil {
		return nil
	}
	c, g := cloudflareFiles(builtinCloudflareRanges, "built-in", time.Now())
	if err := writeAtomic(conf, c); err != nil {
		return err
	}
	return writeAtomic(geo, g)
}

// parseRangeList reads one of Cloudflare's lists: every line a CIDR, none so
// wide that a tampered answer could make the whole internet Cloudflare.
func parseRangeList(body string, v6 bool) ([]string, error) {
	var out []string
	for i, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p, err := netip.ParsePrefix(line)
		if err != nil || p != p.Masked() || p.Addr().Is6() != v6 {
			return nil, fmt.Errorf("line %d is not a range: %q", i+1, truncate(line, 60))
		}
		if (!v6 && p.Bits() < 8) || (v6 && p.Bits() < 16) {
			return nil, fmt.Errorf("line %d is wider than any Cloudflare range: %s", i+1, line)
		}
		out = append(out, p.String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the list is empty")
	}
	if len(out) > 1000 {
		return nil, fmt.Errorf("the list has %d ranges, more than Cloudflare publishes", len(out))
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func fetchRangeList(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", url, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxRangeList+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxRangeList {
		return "", fmt.Errorf("%s answered more than %d KiB", url, maxRangeList>>10)
	}
	return string(b), nil
}

// CloudflareRefresh is what a refresh did.
type CloudflareRefresh struct {
	Ranges     *CloudflareRanges `json:"ranges"`
	Validation *ValidationResult `json:"validation"`
	Reloaded   bool              `json:"reloaded"`
	// ReloadError is a reload that failed after a clean test; the new
	// ranges are on disk and the next reload takes them.
	ReloadError string `json:"reloadError,omitempty"`
}

// RefreshCloudflareRanges downloads Cloudflare's ranges, writes both files,
// tests the configuration with them and puts the old files back if the test
// fails. The download has 10 seconds.
func (s *Service) RefreshCloudflareRanges(ctx context.Context) (*CloudflareRefresh, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var ranges []string
	for i, url := range cloudflareRangeURLs {
		body, err := fetchRangeList(fetchCtx, url)
		if err != nil {
			return nil, fmt.Errorf("Cloudflare's ranges could not be downloaded: %w", err)
		}
		list, err := parseRangeList(body, i == 1)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", url, err)
		}
		ranges = append(ranges, list...)
	}
	ranges = slices.Compact(ranges)
	confBody, geoBody := cloudflareFiles(ranges, "cloudflare", time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()
	conf, geo, err := s.realIPFiles(true)
	if err != nil {
		return nil, err
	}
	confBefore, confExisted := readIfPresent(conf)
	geoBefore, geoExisted := readIfPresent(geo)
	rollback := func() {
		restoreConfig(conf, confBefore, confExisted)
		restoreConfig(geo, geoBefore, geoExisted)
	}
	if err := writeAtomic(conf, confBody); err != nil {
		rollback()
		return nil, err
	}
	if err := writeAtomic(geo, geoBody); err != nil {
		rollback()
		return nil, err
	}
	res := &CloudflareRefresh{Validation: runValidator(ctx, "nginx", "-t")}
	if !res.Validation.Valid {
		rollback()
		return res, ErrInvalidConf
	}
	s.recordChange(ctx, Change{Path: conf, Action: ChangeWrite,
		Before: []byte(confBefore), BeforeExisted: confExisted, After: []byte(confBody)})
	s.recordChange(ctx, Change{Path: geo, Action: ChangeWrite,
		Before: []byte(geoBefore), BeforeExisted: geoExisted, After: []byte(geoBody)})
	if raw, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput(); err != nil {
		res.ReloadError = strings.TrimSpace(string(raw))
		if res.ReloadError == "" {
			res.ReloadError = err.Error()
		}
	} else {
		res.Reloaded = true
	}
	res.Ranges, err = s.ReadCloudflareRanges()
	return res, err
}
