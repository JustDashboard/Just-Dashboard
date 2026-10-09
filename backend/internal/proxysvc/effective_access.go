package proxysvc

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Who may reach a URL from one address, layer by layer.
//
// Access to a site is decided in several places that each look correct on
// their own: the firewall in front of the port, a rule that closes every
// connection not from Cloudflare, the maintenance switch, nginx's allow and
// deny lines (the site's own, a shared access list it includes, or a path's,
// which replaces the site's), a password, a sign-in through an auth server,
// a client certificate — and `satisfy any`, which lets an address through
// without the password. Reading them one file at a time, nobody could say
// whether a given office address would get in. This replays them in the
// order nginx runs them, for the server block and location ResolveRoute
// picks, and says which layer decides and what it cannot see.

// Layer verdicts.
const (
	LayerAdmits   = "admits"
	LayerRefuses  = "refuses"
	LayerRequires = "requires"
	LayerUnknown  = "unknown"
	LayerSkipped  = "skipped"
)

// Explanation verdicts.
const (
	AccessAdmitted    = "admitted"
	AccessRefused     = "refused"
	AccessCredentials = "credentials"
	AccessUnknown     = "unknown"
	AccessNoRoute     = "no-route"
)

// AccessLayer is one place access is decided.
type AccessLayer struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Owner     string `json:"owner"`
	OwnerPath string `json:"ownerPath,omitempty"`
	Verdict   string `json:"verdict"`
	Detail    string `json:"detail"`
	// Rules are the directives that decide it, the deciding one first.
	Rules []RouteDirective `json:"rules,omitempty"`

	// allowed is an explicit allow line matching the address: under
	// satisfy any it alone lets a visitor past the sign-in. An address no
	// line names is declined, not allowed, and is still asked.
	allowed bool
}

// AccessExplanation is the route a URL takes and who may take it from
// Source.
type AccessExplanation struct {
	Route   RouteResolution `json:"route"`
	Source  string          `json:"source"`
	Verdict string          `json:"verdict"`
	Summary string          `json:"summary"`
	Layers  []AccessLayer   `json:"layers"`

	satisfyAny bool
	// routeCertain is the route's certainty less its server-level ifs, each
	// of which is a layer of its own here.
	routeCertain bool
}

// AddLayer puts a layer another owner judged — the host firewall in front of
// the port — after the first at positions and reads the verdict again.
func (a *AccessExplanation) AddLayer(at int, layer AccessLayer) {
	if a.Verdict == AccessNoRoute {
		return
	}
	at = min(max(at, 0), len(a.Layers))
	a.Layers = append(a.Layers[:at], append([]AccessLayer{layer}, a.Layers[at:]...)...)
	a.Verdict, a.Summary = combineLayers(a.Layers, a.satisfyAny, a.routeCertain)
}

// ListenPort is the port and address the route arrives on, for the firewall
// in front of it: "" for a wildcard.
func (a *AccessExplanation) ListenPort() (address string, port int) {
	if a.Route.Server == nil {
		return "", a.Route.Port
	}
	listen := a.Route.Server.Listen
	if i := strings.LastIndexByte(listen, ':'); i >= 0 {
		host := strings.Trim(listen[:i], "[]")
		if host == "*" || host == "::" {
			host = ""
		}
		return host, a.Route.Port
	}
	return "", a.Route.Port
}

// ErrAccessSource is a source that is not one IP address.
var ErrAccessSource = errors.New("invalid source address")

// ExplainAccess resolves rawURL through tree and evaluates every nginx layer
// of access for source, which must be one IP address: the address nginx
// sees, after any real_ip translation.
func ExplainAccess(tree []Directive, rawURL, source string) (*AccessExplanation, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(source))
	if err != nil || addr.Zone() != "" {
		return nil, fmt.Errorf("%w: enter one IPv4 or IPv6 address, as nginx sees the visitor", ErrAccessSource)
	}
	addr = addr.Unmap()
	route, err := ResolveRoute(tree, rawURL)
	if err != nil {
		return nil, err
	}
	out := &AccessExplanation{Route: route, Source: addr.String(), Layers: []AccessLayer{}}
	switch route.Outcome {
	case "refused", "tls-failed", "plain-to-tls":
		out.Verdict = AccessNoRoute
		out.Summary = "No site takes this URL from anyone: " + noRouteWords[route.Outcome]
		return out, nil
	}
	server, chain := routeBlocks(tree, route)
	if server == nil {
		out.Verdict = AccessUnknown
		out.Summary = "The server block the route names could not be found again in the configuration."
		return out, nil
	}
	scopes := append([]Directive{server.http, server.d}, chain...)
	e := accessEval{tree: tree, scopes: scopes, server: server.d, source: addr, uri: route.Path}

	out.Layers = append(out.Layers, AccessLayer{ID: "outside", Title: "Outside this host", Owner: "Provider, CDN or network",
		Verdict: LayerUnknown, Detail: "A provider firewall, a security group, a CDN or NAT in front of this host is not visible from its configuration."})
	out.Layers = append(out.Layers, e.serverIfs()...)
	returns := route.Outcome == "return"
	out.Layers = append(out.Layers, e.addressRules(returns), e.credentials(returns))
	if limit := e.rateLimit(); limit != nil {
		out.Layers = append(out.Layers, *limit)
	}
	if cert := e.clientCertificate(); cert != nil {
		out.Layers = append(out.Layers, *cert)
	}
	out.satisfyAny, out.routeCertain = e.satisfyAny(), routeCertain(route)
	out.Verdict, out.Summary = combineLayers(out.Layers, out.satisfyAny, out.routeCertain)
	return out, nil
}

var noRouteWords = map[string]string{
	"refused":      "nothing listens on the address and port it names.",
	"tls-failed":   "the port answers plain HTTP, so the TLS handshake fails.",
	"plain-to-tls": "the port speaks TLS, so a plain HTTP request is answered 400.",
}

// routeBlocks finds the server block and the location chain a resolution
// names, by their files and lines.
func routeBlocks(tree []Directive, route RouteResolution) (*routeServer, []Directive) {
	if route.Server == nil {
		return nil, nil
	}
	for _, s := range routeServers(tree) {
		if s.d.File != route.Server.File || s.d.Line != route.Server.Line {
			continue
		}
		if route.Location == nil {
			return &s, nil
		}
		var chain []Directive
		var find func([]Directive) bool
		find = func(block []Directive) bool {
			for _, d := range block {
				if d.Name != "location" {
					continue
				}
				chain = append(chain, d)
				if d.File == route.Location.File && d.Line == route.Location.Line || find(d.Block) {
					return true
				}
				chain = chain[:len(chain)-1]
			}
			return false
		}
		if find(s.d.Block) {
			return &s, chain
		}
		return &s, nil
	}
	return nil, nil
}

type accessEval struct {
	tree []Directive
	// scopes are http, server and the locations, outermost first.
	scopes []Directive
	server Directive
	source netip.Addr
	uri    string
}

// innermost is the innermost scope that sets any of names, and those
// directives in it, in order: how nginx inherits array settings such as
// allow and deny, where a level that sets one replaces every outer one.
func (e accessEval) innermost(names ...string) []Directive {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		var found []Directive
		for _, d := range e.scopes[i].Block {
			for _, n := range names {
				if d.Name == n {
					found = append(found, d)
				}
			}
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

func (e accessEval) setting(name string) (Directive, bool) {
	found := e.innermost(name)
	if len(found) == 0 {
		return Directive{}, false
	}
	return found[len(found)-1], true
}

func (e accessEval) satisfyAny() bool {
	d, ok := e.setting("satisfy")
	return ok && len(d.Args) == 1 && d.Args[0] == "any"
}

var (
	maintenanceIfRe = regexp.MustCompile(`^\(\$(jd_\w+)_maint\)$`)
	edgeIfRe        = regexp.MustCompile(`^\(\$(jd_\w+)_edge\s*=\s*0\)$`)
	botIfArgs       = regexp.MustCompile(`^\(\$jd_\w+_bot\)$`)
	jdAnyIfRe       = regexp.MustCompile(`^\(\$jd_\w+_(preflight|hotlink\w*|maint)\)$|^\(\$uri\s`)
)

// serverIfs reads the checks a site's server block makes before access is
// looked at, in its rewrite phase: the ones this dashboard writes, which can
// be judged for an address, and any other, which cannot.
func (e accessEval) serverIfs() []AccessLayer {
	var out []AccessLayer
	for _, d := range e.server.Block {
		if d.Name != "if" {
			continue
		}
		cond := strings.Join(d.Args, " ")
		switch {
		case maintenanceIfRe.MatchString(cond):
			ident := maintenanceIfRe.FindStringSubmatch(cond)[1]
			layer := AccessLayer{ID: "maintenance", Title: "Maintenance", Owner: "Site form", Rules: []RouteDirective{quoteDirective(d, false)}}
			value, ok := e.geo(ident + "_maint_ip")
			switch {
			case maintenanceExemptRe.MatchString(e.uri):
				layer.Verdict, layer.Detail = LayerAdmits, "Maintenance is on, and this path is one it always lets through."
			case !ok:
				layer.Verdict, layer.Detail = LayerUnknown, "Maintenance is on; its list of addresses that get through could not be read."
			case value == "0":
				layer.Verdict, layer.Detail = LayerAdmits, "Maintenance is on, and "+e.source.String()+" is on its list of addresses that reach the site as usual."
			default:
				layer.Verdict, layer.Detail = LayerRefuses, "Maintenance is on: nginx answers 503 with the maintenance page, ahead of every other check."
			}
			out = append(out, layer)
		case edgeIfRe.MatchString(cond):
			ident := edgeIfRe.FindStringSubmatch(cond)[1]
			layer := AccessLayer{ID: "edge", Title: "Cloudflare only", Owner: "Site form", Rules: []RouteDirective{quoteDirective(d, false)}}
			value, ok := e.geo(ident + "_edge")
			switch {
			case !ok:
				layer.Verdict, layer.Detail = LayerUnknown, "Connections not from Cloudflare are closed; Cloudflare's ranges could not be read to judge this address."
			case value == "0":
				layer.Verdict, layer.Detail = LayerRefuses, e.source.String()+" is not one of Cloudflare's addresses, and the site closes every connection that does not come from Cloudflare (444)."
			default:
				layer.Verdict, layer.Detail = LayerAdmits, e.source.String()+" is one of Cloudflare's addresses, which the site takes connections from."
			}
			out = append(out, layer)
		case botIfArgs.MatchString(cond):
			out = append(out, AccessLayer{ID: "crawlers", Title: "Crawler block", Owner: "Site form", Verdict: LayerAdmits,
				Detail: "Requests whose User-Agent names a blocked crawler are refused 403; an ordinary browser's are not.", Rules: []RouteDirective{quoteDirective(d, false)}})
		case jdAnyIfRe.MatchString(cond):
			// CORS preflights, hotlinked media and the maintenance path
			// check answer particular requests, not addresses.
		default:
			out = append(out, AccessLayer{ID: "server-if", Title: "A check in the server block", Owner: "Configuration file", Verdict: LayerUnknown,
				Detail: "The server block checks if " + cond + " before access is looked at, and may answer the request there; that condition is not judged here.",
				Rules:  []RouteDirective{quoteDirective(d, false)}})
		}
	}
	return out
}

// maintenanceExemptRe is maintenanceExempt as Go reads it.
var maintenanceExemptRe = regexp.MustCompile(`^/(\.well-known/acme-challenge/|__jd/maintenance\.html$)`)

// geo is the value a geo block gives the source for the variable $name: the
// longest range or address that holds it, else its default.
func (e accessEval) geo(name string) (string, bool) {
	var block *Directive
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for i, d := range ds {
			if d.Name == "geo" && len(d.Args) > 0 && d.Args[len(d.Args)-1] == "$"+name {
				block = &ds[i]
				return
			}
			walk(d.Block)
		}
	}
	walk(e.tree)
	if block == nil {
		return "", false
	}
	value, best := "", -1
	var read func([]Directive) bool
	read = func(entries []Directive) bool {
		for _, entry := range entries {
			if entry.Name == "ranges" {
				return false
			}
			if entry.Name == "include" {
				continue
			}
			if len(entry.Args) == 0 {
				read(entry.Block)
				continue
			}
			if entry.Name == "default" {
				if best < 0 {
					value = entry.Args[0]
				}
				continue
			}
			prefix, err := netip.ParsePrefix(entry.Name)
			if err != nil {
				if a, aerr := netip.ParseAddr(entry.Name); aerr == nil {
					prefix = netip.PrefixFrom(a, a.BitLen())
				} else {
					continue
				}
			}
			if prefix.Contains(e.source) && prefix.Bits() > best {
				value, best = entry.Args[0], prefix.Bits()
			}
		}
		return true
	}
	if !read(block.Block) {
		return "", false
	}
	return value, true
}

// addressRules judges nginx's allow and deny lines in effect for the
// location, first match, as ngx_http_access_module does.
func (e accessEval) addressRules(returns bool) AccessLayer {
	layer := AccessLayer{ID: "addresses", Title: "Allowed addresses", Owner: "Site or access list", OwnerPath: "/proxy/sites"}
	if returns {
		layer.Verdict = LayerSkipped
		layer.Detail = "The request is answered by a return before nginx checks addresses, so no allow or deny line applies to it."
		return layer
	}
	rules := e.innermost("allow", "deny")
	if len(rules) == 0 {
		layer.Verdict, layer.Detail = LayerAdmits, "No allow or deny line applies to this path, so every address is let through here."
		return layer
	}
	for _, r := range rules {
		if len(r.Args) != 1 || !matchesSource(r.Args[0], e.source) {
			continue
		}
		layer.Rules = append(layer.Rules, quoteDirective(r, r.File != e.scopes[len(e.scopes)-1].File))
		if r.Name == "allow" {
			layer.Verdict, layer.Detail = LayerAdmits, fmt.Sprintf("%s is allowed by the first line that matches it.", e.source)
			layer.allowed = true
		} else {
			layer.Verdict, layer.Detail = LayerRefuses, fmt.Sprintf("%s is denied by the first line that matches it: nginx answers 403.", e.source)
		}
		if strings.Contains(r.File, "/jd-access/") {
			layer.Owner, layer.OwnerPath = "Access list "+strings.TrimSuffix(lastPathPart(r.File), ".conf"), "/proxy/sites#access-lists"
			layer.Detail += " The line is in the shared access list " + lastPathPart(r.File) + "."
		}
		return layer
	}
	layer.Verdict, layer.Detail = LayerAdmits, fmt.Sprintf("No allow or deny line matches %s, and nginx lets an address no line names through.", e.source)
	return layer
}

func lastPathPart(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func matchesSource(arg string, source netip.Addr) bool {
	if arg == "all" {
		return true
	}
	if prefix, err := netip.ParsePrefix(arg); err == nil {
		return prefix.Contains(source)
	}
	if a, err := netip.ParseAddr(arg); err == nil {
		return a.Unmap() == source
	}
	return false
}

// credentials reads what nginx asks the visitor for once the address is let
// through: a password, a sign-in through an auth server.
func (e accessEval) credentials(returns bool) AccessLayer {
	layer := AccessLayer{ID: "credentials", Title: "Sign-in", Owner: "Site form", OwnerPath: "/proxy/sites"}
	if returns {
		layer.Verdict, layer.Detail = LayerSkipped, "The request is answered by a return before nginx asks for any password or sign-in."
		return layer
	}
	var asks []string
	if d, ok := e.setting("auth_basic"); ok && len(d.Args) == 1 && d.Args[0] != "off" {
		layer.Rules = append(layer.Rules, quoteDirective(d, false))
		ask := "a password (realm “" + d.Args[0] + "”"
		if file, ok := e.setting("auth_basic_user_file"); ok && len(file.Args) == 1 {
			ask += ", users in " + file.Args[0]
			layer.Rules = append(layer.Rules, quoteDirective(file, false))
		}
		asks = append(asks, ask+")")
	}
	if d, ok := e.setting("auth_request"); ok && len(d.Args) == 1 && d.Args[0] != "off" {
		layer.Rules = append(layer.Rules, quoteDirective(d, false))
		asks = append(asks, "a sign-in the auth server behind "+d.Args[0]+" accepts (a 401 there sends the visitor to sign in)")
	}
	if len(asks) == 0 {
		layer.Verdict, layer.Detail = LayerAdmits, "nginx asks for no password or sign-in on this path."
		return layer
	}
	layer.Verdict = LayerRequires
	layer.Detail = "nginx asks for " + strings.Join(asks, " and ") + "."
	if e.satisfyAny() {
		layer.Detail += " satisfy any is set: an address the allow lines let through is not asked for it."
	}
	return layer
}

// rateLimit reads the request limit in effect, if one applies to the source.
func (e accessEval) rateLimit() *AccessLayer {
	d, ok := e.setting("limit_req")
	if !ok || len(d.Args) == 0 {
		return nil
	}
	zone := strings.TrimPrefix(d.Args[0], "zone=")
	layer := &AccessLayer{ID: "rate-limit", Title: "Request limit", Owner: "Site form", Verdict: LayerAdmits, Rules: []RouteDirective{quoteDirective(d, false)}}
	rate := ""
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, z := range ds {
			if z.Name == "limit_req_zone" && slicesContainsPrefix(z.Args, "zone="+zone+":") {
				for _, a := range z.Args {
					if strings.HasPrefix(a, "rate=") {
						rate = strings.TrimPrefix(a, "rate=")
					}
				}
			}
			walk(z.Block)
		}
	}
	walk(e.tree)
	if exempt, ok := e.geo(limitIdent(zone) + "_limit_exempt"); ok && exempt == "1" {
		layer.Detail = e.source.String() + " is on the site's list of addresses no limit counts."
		return layer
	}
	layer.Detail = "Requests are let through up to " + rate + " per address; more are answered 429."
	if rate == "" {
		layer.Detail = "Requests are let through up to the zone " + zone + "'s rate per address; more are refused."
	}
	return layer
}

// limitZoneRe is a zone the site form names: the site's ident, then _req
// for the site-wide limit or _req_p<n> for its n-th path.
var limitZoneRe = regexp.MustCompile(`_req(_p\d+)?$`)

// limitIdent is the site ident a form-written zone was named from, which its
// exempt list is named from too.
func limitIdent(zone string) string {
	return limitZoneRe.ReplaceAllString(zone, "")
}

func slicesContainsPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// clientCertificate reads whether the TLS handshake asks for a certificate.
func (e accessEval) clientCertificate() *AccessLayer {
	d, ok := e.setting("ssl_verify_client")
	if !ok || len(d.Args) != 1 || d.Args[0] == "off" {
		return nil
	}
	layer := &AccessLayer{ID: "client-certificate", Title: "Client certificate", Owner: "Site form", Rules: []RouteDirective{quoteDirective(d, false)}}
	if d.Args[0] == "on" {
		layer.Verdict = LayerRequires
		layer.Detail = "The TLS handshake requires a client certificate signed by the site's authority; without one nginx answers 400."
	} else {
		layer.Verdict = LayerAdmits
		layer.Detail = "The TLS handshake asks for a client certificate but lets a visitor without one through; the application decides."
	}
	return layer
}

// combineLayers is the answer the layers give together: refused where one
// refuses (an address refused under satisfy any can still sign in), asked to
// sign in where one requires it, unknown where one could not be judged.
func combineLayers(layers []AccessLayer, any bool, certain bool) (string, string) {
	var refused, requires, unknown []string
	addressRefused := false
	for _, l := range layers {
		switch l.Verdict {
		case LayerRefuses:
			if l.ID == "addresses" {
				addressRefused = true
				continue
			}
			refused = append(refused, l.Title)
		case LayerRequires:
			requires = append(requires, l.Title)
		case LayerUnknown:
			if l.ID != "outside" {
				unknown = append(unknown, l.Title)
			}
		}
	}
	signIn, allowed := false, false
	for _, l := range layers {
		if l.ID == "credentials" && l.Verdict == LayerRequires {
			signIn = true
		}
		if l.ID == "addresses" && l.allowed {
			allowed = true
		}
	}
	switch {
	case addressRefused && any && signIn:
		// satisfy any: the sign-in is the other way in.
	case addressRefused:
		refused = append(refused, "Allowed addresses")
	case any && signIn && allowed:
		// satisfy any lets an explicitly allowed address past the sign-in;
		// one no line names is declined, and nginx still asks.
		requires = removeTitle(requires, "Sign-in")
	}
	suffix := " Anything in front of this host is not seen here."
	if !certain {
		unknown = append(unknown, "the route itself")
	}
	switch {
	case len(refused) > 0:
		return AccessRefused, "Refused at " + strings.Join(refused, " and ") + "." + suffix
	case len(unknown) > 0:
		return AccessUnknown, "Not decided here: " + strings.Join(unknown, " and ") + " could not be judged for this address." + suffix
	case len(requires) > 0:
		return AccessCredentials, "Let through once the visitor presents " + strings.ToLower(strings.Join(requires, " and ")) + "." + suffix
	}
	return AccessAdmitted, "Let through by every layer nginx decides here." + suffix
}

// routeCertain is whether the route was worked out exactly, setting aside
// the server block's ifs, which serverIfs judges or names as layers.
func routeCertain(route RouteResolution) bool {
	for _, step := range route.Steps {
		if !step.Caution || step.Stage == "listen" || step.Stage == "tls" {
			continue
		}
		if step.Stage == "server" && strings.HasPrefix(step.Message, "A server-level if") {
			continue
		}
		return false
	}
	return true
}

func removeTitle(titles []string, title string) []string {
	out := titles[:0]
	for _, t := range titles {
		if t != title {
			out = append(out, t)
		}
	}
	return out
}
