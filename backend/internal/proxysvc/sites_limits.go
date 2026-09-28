package proxysvc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SiteLimits is how much one client may ask of a site: requests at a rate,
// connections at once, and who is exempt from both.
//
// Every limit counts by the address nginx sees. Behind a CDN or a load
// balancer that is the CDN's, unless nginx is told to read the visitor's
// from a header, and SpecWarnings says so.
type SiteLimits struct {
	// Request is the site-wide request rate; nil sets none. A path with a
	// limit of its own replaces it there rather than adding to it, which is
	// how nginx inherits limit_req.
	Request *RequestLimit `json:"request,omitempty"`
	// ConnPerIP caps the connections one address holds open at once; 0 sets
	// no cap.
	ConnPerIP int `json:"connPerIp,omitempty"`
	// ExemptFrom are the addresses no limit of the site counts.
	ExemptFrom []string `json:"exemptFrom"`
	// DryRun logs what would have been refused and refuses nothing.
	DryRun bool `json:"dryRun,omitempty"`
}

// RequestLimit is a request rate with the burst allowed above it.
type RequestLimit struct {
	// Rate is nginx's own spelling: 10r/s or 60r/m.
	Rate string `json:"rate"`
	// Burst is how many requests past the rate are queued rather than
	// refused.
	Burst int `json:"burst,omitempty"`
	// NoDelay answers the burst at once instead of spacing it out at the
	// rate.
	NoDelay bool `json:"noDelay,omitempty"`
	// Key is "ip", one allowance per address, or "ip_path", one per address
	// and path. Empty is ip.
	Key string `json:"key,omitempty"`
}

var rateRe = regexp.MustCompile(`^[1-9]\d{0,5}r/[sm]$`)

const maxLimitCount = 100000

// hasLimits says whether the rendered site limits anything, which is when
// the http-level objects the limits count in are written.
func (spec *SiteSpec) hasLimits() bool {
	if spec.Kind == "redirect" {
		return false
	}
	if l := spec.Limits; l != nil && (l.Request != nil || l.ConnPerIP > 0) {
		return true
	}
	return spec.limitedLocations()
}

// limitedLocations says whether a path the renderer writes has a limit of its
// own. Extra paths are written only for a proxy site.
func (spec *SiteSpec) limitedLocations() bool {
	if spec.Kind != "proxy" {
		return false
	}
	for _, loc := range spec.Locations {
		if loc.RateLimit != nil {
			return true
		}
	}
	return false
}

// The site's zones and the variables they count by. Each zone's key is a map
// variable of its own, always, rather than $binary_remote_addr written
// straight in: nginx refuses a reload that finds a live zone counting by a
// different key than before, so turning exemptions on or counting by path
// would otherwise fail every reload until nginx was restarted.
func (spec *SiteSpec) limitExemptVar() string { return NginxIdent(spec.Name) + "_limit_exempt" }

func (spec *SiteSpec) reqZone() string { return NginxIdent(spec.Name) + "_req" }

func (spec *SiteSpec) connZone() string { return NginxIdent(spec.Name) + "_conn" }

// locationZone is the zone of the i-th extra path's own limit.
func (spec *SiteSpec) locationZone(i int) string {
	return spec.reqZone() + "_p" + strconv.Itoa(i+1)
}

func validateLimits(spec *SiteSpec) error {
	limited := spec.Limits != nil && (spec.Limits.Request != nil || spec.Limits.ConnPerIP > 0)
	for _, loc := range spec.Locations {
		limited = limited || loc.RateLimit != nil
	}
	if !limited {
		if spec.Limits != nil && (len(spec.Limits.ExemptFrom) > 0 || spec.Limits.DryRun) {
			return fmt.Errorf("exemptions and log-only mode need a request rate or a connection cap to apply to")
		}
		return nil
	}
	// A redirect is answered by `return` in the rewrite phase, before the
	// phase limit_req and limit_conn count in: the limit would be written
	// and never apply.
	if spec.Kind == "redirect" {
		return fmt.Errorf("a redirect is answered before nginx applies limits — limit whatever it points at instead")
	}
	if l := spec.Limits; l != nil {
		if l.Request != nil {
			if err := validRequestLimit(l.Request, "the site"); err != nil {
				return err
			}
		}
		if l.ConnPerIP < 0 || l.ConnPerIP > maxLimitCount {
			return fmt.Errorf("the connection cap must be between 0 and %d", maxLimitCount)
		}
		for _, entry := range l.ExemptFrom {
			if err := validAddress(entry); err != nil {
				return err
			}
		}
	}
	for _, loc := range spec.Locations {
		if loc.RateLimit != nil {
			if err := validRequestLimit(loc.RateLimit, loc.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

func validRequestLimit(r *RequestLimit, where string) error {
	if !rateRe.MatchString(r.Rate) {
		return fmt.Errorf("%s: the rate must be requests per second or minute, like 10r/s or 60r/m", where)
	}
	if r.Burst < 0 || r.Burst > maxLimitCount {
		return fmt.Errorf("%s: the burst must be between 0 and %d", where, maxLimitCount)
	}
	if r.Key != "" && r.Key != "ip" && r.Key != "ip_path" {
		return fmt.Errorf("%s: requests are counted by ip or ip_path", where)
	}
	return nil
}

// limitKeyExpr is what a request limit counts by.
func limitKeyExpr(r *RequestLimit) string {
	if r.Key == "ip_path" {
		return "$binary_remote_addr$uri"
	}
	return "$binary_remote_addr"
}

// renderLimitZones writes the http-level half of the limits: who is exempt,
// what each zone counts by, and the zones. An empty key is not counted,
// which is how an exempt address passes every limit.
func renderLimitZones(l *lines, spec *SiteSpec) {
	var exempt []string
	if spec.Limits != nil {
		exempt = spec.Limits.ExemptFrom
	}
	l.add("# Limits: requests and connections are counted per client address;")
	l.add("# the addresses marked 1 here are not counted at all.")
	l.add("geo $%s {", spec.limitExemptVar())
	l.add("    default 0;")
	for _, entry := range exempt {
		l.add("    %s 1;", entry)
	}
	l.add("}")
	zone := func(directive, name, expr, rate string) {
		l.add("map $%s $%s_key {", spec.limitExemptVar(), name)
		l.add("    1       \"\";")
		l.add("    default %s;", expr)
		l.add("}")
		if rate != "" {
			l.add("%s $%s_key zone=%s:10m rate=%s;", directive, name, name, rate)
		} else {
			l.add("%s $%s_key zone=%s:10m;", directive, name, name)
		}
	}
	if spec.Limits != nil && spec.Limits.Request != nil {
		r := spec.Limits.Request
		zone("limit_req_zone", spec.reqZone(), limitKeyExpr(r), r.Rate)
	}
	if spec.Limits != nil && spec.Limits.ConnPerIP > 0 {
		zone("limit_conn_zone", spec.connZone(), "$binary_remote_addr", "")
	}
	if spec.Kind == "proxy" {
		for i, loc := range spec.Locations {
			if loc.RateLimit != nil {
				zone("limit_req_zone", spec.locationZone(i), limitKeyExpr(loc.RateLimit), loc.RateLimit.Rate)
			}
		}
	}
}

// renderServerLimits writes the server-level half. Every location inherits
// it, except that a path with a limit_req of its own replaces the site's.
func renderServerLimits(l *lines, spec *SiteSpec) {
	if !spec.hasLimits() {
		return
	}
	lim := spec.Limits
	if lim == nil {
		lim = &SiteLimits{}
	}
	l.add("    # A client past its limit is answered 429 Too Many Requests rather")
	l.add("    # than nginx's default 503, which reads as the site being down.")
	if lim.Request != nil {
		l.add("    limit_req %s;", limitReqArgs(spec.reqZone(), lim.Request))
	}
	if lim.Request != nil || spec.limitedLocations() {
		l.add("    limit_req_status 429;")
	}
	if lim.ConnPerIP > 0 {
		l.add("    limit_conn %s %d;", spec.connZone(), lim.ConnPerIP)
		l.add("    limit_conn_status 429;")
	}
	if lim.DryRun {
		l.add("    # Log only: what would be refused is written to the error log as")
		l.add("    # \"dry run\", and every request is served.")
		l.add("    limit_req_dry_run on;")
		l.add("    limit_conn_dry_run on;")
	}
	l.blank()
}

// renderLocationLimit writes an extra path's own request limit, which
// replaces the site's on that path.
func renderLocationLimit(l *lines, loc SiteLocation, spec *SiteSpec) {
	if loc.RateLimit == nil {
		return
	}
	for i, other := range spec.Locations {
		if other.Path == loc.Path && other.Match == loc.Match {
			l.add("        # This path's own limit, in place of the site's.")
			l.add("        limit_req %s;", limitReqArgs(spec.locationZone(i), loc.RateLimit))
			return
		}
	}
}

func limitReqArgs(zone string, r *RequestLimit) string {
	args := "zone=" + zone
	if r.Burst > 0 {
		args += " burst=" + strconv.Itoa(r.Burst)
	}
	if r.NoDelay {
		args += " nodelay"
	}
	return args
}

// limitsWarnings are the limits' legal choices that probably are not meant.
func limitsWarnings(spec *SiteSpec) []string {
	if !spec.hasLimits() {
		return nil
	}
	var warnings []string
	// A real_ip_header in the extra configuration is the one sign the form
	// can see that nginx reads the visitor's address from a proxy in front;
	// one in nginx.conf does the same and is not visible from here.
	if !strings.Contains(spec.Custom, "real_ip_header") {
		warnings = append(warnings,
			"Limits count by the address nginx sees. Behind a CDN or a load balancer that is the CDN's, and every visitor shares its allowance — unless nginx is set to read the visitor's address from it (set_real_ip_from and real_ip_header).")
	}
	if l := spec.Limits; l != nil && l.Request != nil && l.Request.NoDelay && l.Request.Burst == 0 {
		warnings = append(warnings,
			"Without a burst, answer at once changes nothing: every request past the rate is refused.")
	}
	if spec.Limits != nil && spec.Limits.DryRun {
		warnings = append(warnings,
			"Log only: nothing is refused. Requests that would have been are logged in the error log as \"dry run\".")
	}
	return warnings
}

// limitParse collects what the limits' directives say as the parser reads
// them, and settles it once the whole file is read: the zones that carry the
// rate and the key are above the servers that name them, but a hand-edited
// file may put them anywhere.
type limitParse struct {
	rates    map[string]string // zone → rate
	keys     map[string]string // zone → key expression
	exempt   []string
	inExempt bool
	mapZone  string

	site    string // the server's limit_req zone
	siteReq *RequestLimit
	conn    int
	dryRun  bool
	// locs are the zones of the extra paths' own limits, by their index in
	// spec.Locations while it is being read.
	locs map[int]string
}

var (
	limitGeoRe = regexp.MustCompile(`^\$jd_\w+_limit_exempt\s*\{$`)
	limitMapRe = regexp.MustCompile(`^\$jd_\w+_limit_exempt\s+\$(jd_\w+)_key\s*\{$`)
	zoneArgRe  = regexp.MustCompile(`zone=(\w+)`)
	rateArgRe  = regexp.MustCompile(`rate=(\S+)`)
	burstArgRe = regexp.MustCompile(`burst=(\d+)`)
)

func newLimitParse() *limitParse {
	return &limitParse{rates: map[string]string{}, keys: map[string]string{}, locs: map[int]string{}}
}

// object reads an http-level statement: the opener of a geo or map, or a
// zone. It says whether the block it opens is one of the limits'.
func (p *limitParse) object(name, value string) {
	switch {
	case name == "geo" && limitGeoRe.MatchString(value):
		p.inExempt = true
	case name == "map":
		if m := limitMapRe.FindStringSubmatch(value); m != nil {
			p.mapZone = m[1]
		}
	case name == "limit_req_zone":
		if z, r := zoneArgRe.FindStringSubmatch(value), rateArgRe.FindStringSubmatch(value); z != nil && r != nil {
			p.rates[z[1]] = r[1]
		}
	}
}

// entry reads a line inside a geo or map the parser is stepping over.
func (p *limitParse) entry(raw string) {
	fields := strings.Fields(strings.TrimSuffix(raw, ";"))
	if len(fields) != 2 {
		return
	}
	switch {
	case p.inExempt && fields[0] != "default":
		p.exempt = append(p.exempt, fields[0])
	case p.mapZone != "" && fields[0] == "default":
		p.keys[p.mapZone] = fields[1]
	}
}

// closed ends whichever limits block was open.
func (p *limitParse) closed() { p.inExempt, p.mapZone = false, "" }

// directive reads a limits directive inside a server, into the extra path at
// index when current is one. It says whether it was one.
func (p *limitParse) directive(name, value, location string, current *SiteLocation, index int) bool {
	switch name {
	case "limit_req":
		z := zoneArgRe.FindStringSubmatch(value)
		if z == nil {
			return true
		}
		r := &RequestLimit{NoDelay: hasField(value, "nodelay")}
		if b := burstArgRe.FindStringSubmatch(value); b != nil {
			r.Burst, _ = strconv.Atoi(b[1])
		}
		if current != nil {
			current.RateLimit = r
			p.locs[index] = z[1]
		} else if location == "" {
			p.site, p.siteReq = z[1], r
		}
	case "limit_conn":
		if fields := strings.Fields(value); location == "" && len(fields) == 2 {
			p.conn, _ = strconv.Atoi(fields[1])
		}
	case "limit_req_dry_run", "limit_conn_dry_run":
		p.dryRun = p.dryRun || (location == "" && value == "on")
	case "limit_req_status", "limit_conn_status":
	default:
		return false
	}
	return true
}

// settle fills the spec's limits from what was read, once its kind is known
// and before any extra path is dropped. A limit whose zone the file does not
// define is dropped, since the form has no rate to show for it.
func (p *limitParse) settle(spec *SiteSpec) {
	fill := func(r *RequestLimit, zone string) bool {
		rate, ok := p.rates[zone]
		if !ok || !rateRe.MatchString(rate) {
			return false
		}
		r.Rate = rate
		if p.keys[zone] == "$binary_remote_addr$uri" {
			r.Key = "ip_path"
		}
		return true
	}
	for i := range spec.Locations {
		loc := &spec.Locations[i]
		if loc.RateLimit != nil && !fill(loc.RateLimit, p.locs[i]) {
			loc.RateLimit = nil
		}
	}
	lim := &SiteLimits{ConnPerIP: p.conn, ExemptFrom: p.exempt, DryRun: p.dryRun}
	if p.siteReq != nil && fill(p.siteReq, p.site) {
		lim.Request = p.siteReq
	}
	if lim.ExemptFrom == nil {
		lim.ExemptFrom = []string{}
	}
	if lim.Request != nil || lim.ConnPerIP > 0 || spec.limitedLocations() {
		spec.Limits = lim
	}
	// Exemptions and log only with nothing left to apply to are what the
	// validator refuses; the paths' own limits keep them otherwise.
	if spec.Limits != nil && spec.Limits.Request == nil && spec.Limits.ConnPerIP == 0 &&
		len(spec.Limits.ExemptFrom) == 0 && !spec.Limits.DryRun {
		spec.Limits = nil
	}
}
