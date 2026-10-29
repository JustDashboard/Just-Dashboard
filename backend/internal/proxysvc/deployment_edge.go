package proxysvc

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A deployment route's edge rules: an ordered list of matches and what to do
// with a request that meets one, decided at the proxy before the
// application sees anything. The first rule whose match a request meets
// decides; a request that meets none goes on as it would have. Both proxies
// render the same list natively — nginx as maps and a first-match chain of
// returns, Caddy as named matchers each excluding every rule before it — and
// each records the rule that decided in the request record, so the Domains
// page can say how often each one fired.
//
// Country is not a match: neither proxy this dashboard runs carries a GeoIP
// database, and a match that one proxy could not answer would be a rule that
// means different things on different hosts. Country blocking is the
// network gateway's (Network → Protection), for the whole host.

// EdgeRule is one rule of a route.
type EdgeRule struct {
	// Name is how the rule is told apart in the request record and on the
	// page: lower-case letters, digits and dashes.
	Name   string     `json:"name"`
	Match  EdgeMatch  `json:"match"`
	Action EdgeAction `json:"action"`
}

// EdgeMatch is what a request has to meet, every condition at once. At
// least one is set.
type EdgeMatch struct {
	// PathPrefix matches a path that starts with it, /admin matching
	// /admin and /administrator alike, as a location does.
	PathPrefix string `json:"pathPrefix,omitempty"`
	// PathRegex is a regular expression the path must match, in the
	// syntax both proxies read the same way (see validEdgeRegex).
	PathRegex string `json:"pathRegex,omitempty"`
	// Methods are the request methods matched, any of them.
	Methods []string `json:"methods,omitempty"`
	// UserAgent is a regular expression the User-Agent header must match,
	// ignoring case.
	UserAgent string `json:"userAgent,omitempty"`
	// ClientFrom are the client addresses and ranges matched, any of them.
	ClientFrom []string `json:"clientFrom,omitempty"`
}

// EdgeAction is what a matching request gets: "block" (403), "status" (an
// answer with that status and nothing else — no challenge page), "allow"
// (no later rule is asked; the request goes on) or "redirect" (to Location
// with Code).
type EdgeAction struct {
	Kind     string `json:"kind"`
	Status   int    `json:"status,omitempty"`
	Location string `json:"location,omitempty"`
	Code     int    `json:"code,omitempty"`
}

// DeploymentRateLimit is how many requests one client address may make to
// the route: Requests per second or minute, with Burst more let through at
// once before the rest are refused 429.
type DeploymentRateLimit struct {
	Requests int    `json:"requests"`
	Per      string `json:"per"`
	Burst    int    `json:"burst,omitempty"`
}

// Bounds on a route's edge rules. A route is one deployment's; these bound
// a paste or a typo, not a use case.
const (
	MaxEdgeRules         = 32
	MaxEdgeClients       = 64
	MaxEdgePattern       = 256
	MaxEdgeLocation      = 512
	MaxRateLimitRequests = 100000
	MaxRateLimitBurst    = 100000
)

// EdgeMethods are the methods a rule can match.
var EdgeMethods = []string{"DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"}

// EdgeActions are the kinds a rule can take.
var EdgeActions = []string{"allow", "block", "redirect", "status"}

var (
	edgeNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	// A path prefix is written into both configurations as text, so it is
	// held to the characters a path is made of, less the ones either
	// configuration reads: no quote, brace, backslash, wildcard or space.
	edgePrefixRe = regexp.MustCompile(`^/[A-Za-z0-9._~!&()+,=:@%/-]*$`)
	// An inline flag would mean one thing to PCRE and another to RE2 in
	// places; a non-capturing group (?: is not a flag.
	edgeFlagRe = regexp.MustCompile(`\(\?[A-Za-z-]+[:)]`)
	// A redirect goes to an http(s) address or a path of the route's own,
	// held to the same characters plus a query.
	edgeLocationRe = regexp.MustCompile(`^(https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?)?/[A-Za-z0-9._~!&()+,=:@%/?-]*$`)
)

// ValidateEdgeRules checks a route's rules as both proxies will read them,
// and that each is written the one way the plan keeps it (CanonicalEdgeRules).
func ValidateEdgeRules(rules []EdgeRule) error {
	if len(rules) > MaxEdgeRules {
		return fmt.Errorf("at most %d edge rules", MaxEdgeRules)
	}
	names := map[string]bool{}
	for index, rule := range rules {
		where := fmt.Sprintf("edge rule %d", index+1)
		if !edgeNameRe.MatchString(rule.Name) {
			return fmt.Errorf("%s: a name is lower-case letters, digits and dashes, at most 40", where)
		}
		where = "edge rule " + rule.Name
		if names[rule.Name] {
			return fmt.Errorf("%s is named twice", where)
		}
		names[rule.Name] = true
		if err := validateEdgeMatch(rule.Match); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if err := validateEdgeAction(rule.Action); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if canonical := canonicalEdgeRule(rule); !edgeRulesEqual(canonical, rule) {
			return fmt.Errorf("%s is not written the way it is kept; save it again", where)
		}
	}
	return nil
}

func validateEdgeMatch(match EdgeMatch) error {
	if match.PathPrefix == "" && match.PathRegex == "" && len(match.Methods) == 0 &&
		match.UserAgent == "" && len(match.ClientFrom) == 0 {
		return errors.New("a rule matches something: a path, a method, a user agent or an address")
	}
	if match.PathPrefix != "" && match.PathRegex != "" {
		return errors.New("a rule matches a path by its prefix or by a pattern, not both")
	}
	if match.PathPrefix != "" && (len(match.PathPrefix) > MaxEdgePattern || !edgePrefixRe.MatchString(match.PathPrefix)) {
		return errors.New("a path prefix starts with / and holds the characters of a path, without quotes, braces, wildcards or spaces")
	}
	if match.PathRegex != "" {
		if err := validEdgeRegex(match.PathRegex); err != nil {
			return fmt.Errorf("the path pattern %w", err)
		}
	}
	if match.UserAgent != "" {
		if err := validEdgeRegex(match.UserAgent); err != nil {
			return fmt.Errorf("the user agent pattern %w", err)
		}
	}
	for _, method := range match.Methods {
		if !slices.Contains(EdgeMethods, method) {
			return fmt.Errorf("%q is not one of %s", method, strings.Join(EdgeMethods, ", "))
		}
	}
	if len(match.ClientFrom) > MaxEdgeClients {
		return fmt.Errorf("at most %d addresses", MaxEdgeClients)
	}
	for _, entry := range match.ClientFrom {
		if _, err := NormalizeAccessEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// validEdgeRegex holds a pattern to what nginx's PCRE and Caddy's Go RE2 read
// alike, and to what both configurations can carry as text: it compiles as
// RE2 (so no lookaround or backreference PCRE would take and Caddy would
// not), and has no quote, brace, backslash-u or control character — a
// counted repetition is written out instead.
func validEdgeRegex(pattern string) error {
	if len(pattern) > MaxEdgePattern {
		return fmt.Errorf("is longer than %d characters", MaxEdgePattern)
	}
	if strings.ContainsAny(pattern, "\"'`{}") || strings.Contains(pattern, `\p`) || strings.Contains(pattern, `\P`) ||
		strings.Contains(pattern, `\\`) {
		return errors.New("cannot hold quotes, braces, an escaped backslash or Unicode classes: both proxies' configurations read them")
	}
	for _, r := range pattern {
		if r < 0x20 || r == 0x7f {
			return errors.New("cannot hold a control character")
		}
	}
	if edgeFlagRe.MatchString(pattern) {
		return errors.New("cannot set flags; a user agent is matched ignoring case, a path is not")
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("is not a regular expression both proxies read: %v", err)
	}
	return nil
}

func validateEdgeAction(action EdgeAction) error {
	switch action.Kind {
	case "block":
		if action.Status != 0 && action.Status != 403 || action.Location != "" || action.Code != 0 {
			return errors.New("a block answers 403 and takes nothing else")
		}
	case "allow":
		if action.Status != 0 || action.Location != "" || action.Code != 0 {
			return errors.New("an allow takes nothing else: it lets the request through and asks no later rule")
		}
	case "status":
		if action.Status < 200 || action.Status > 599 || action.Status/100 == 3 {
			return errors.New("a status answer is 200 to 599, other than a redirect's 3xx")
		}
		if action.Location != "" || action.Code != 0 {
			return errors.New("a status answer takes only its status")
		}
	case "redirect":
		if action.Location == "" || len(action.Location) > MaxEdgeLocation || !edgeLocationRe.MatchString(action.Location) {
			return errors.New("a redirect goes to an http or https address or to a path starting with /")
		}
		switch action.Code {
		case 301, 302, 307, 308:
		default:
			return errors.New("a redirect answers 301, 302, 307 or 308")
		}
		if action.Status != 0 {
			return errors.New("a redirect's status is its code")
		}
	default:
		return fmt.Errorf("the action is one of %s", strings.Join(EdgeActions, ", "))
	}
	return nil
}

// CanonicalEdgeRules is the rules as a plan keeps them: text trimmed,
// methods upper-cased, sorted and once each, addresses in their canonical
// spelling and once each, a block's status and a redirect's code filled in.
// Validation refuses anything else, so two plans that mean the same rule
// have the same bytes and the same digest.
func CanonicalEdgeRules(rules []EdgeRule) []EdgeRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]EdgeRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, canonicalEdgeRule(rule))
	}
	return out
}

func canonicalEdgeRule(rule EdgeRule) EdgeRule {
	out := EdgeRule{Name: strings.TrimSpace(rule.Name), Action: rule.Action}
	match := rule.Match
	out.Match.PathPrefix = strings.TrimSpace(match.PathPrefix)
	out.Match.PathRegex = strings.TrimSpace(match.PathRegex)
	out.Match.UserAgent = strings.TrimSpace(match.UserAgent)
	for _, method := range match.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method != "" && !slices.Contains(out.Match.Methods, method) {
			out.Match.Methods = append(out.Match.Methods, method)
		}
	}
	slices.Sort(out.Match.Methods)
	for _, entry := range match.ClientFrom {
		if normalized, err := NormalizeAccessEntry(entry); err == nil {
			entry = normalized
		} else {
			entry = strings.TrimSpace(entry)
		}
		if !slices.Contains(out.Match.ClientFrom, entry) {
			out.Match.ClientFrom = append(out.Match.ClientFrom, entry)
		}
	}
	out.Action.Kind = strings.ToLower(strings.TrimSpace(out.Action.Kind))
	out.Action.Location = strings.TrimSpace(out.Action.Location)
	switch out.Action.Kind {
	case "block":
		out.Action.Status = 403
	case "redirect":
		if out.Action.Code == 0 {
			out.Action.Code = 302
		}
	}
	return out
}

func edgeRulesEqual(a, b EdgeRule) bool {
	return a.Name == b.Name && a.Action == b.Action && a.Match.PathPrefix == b.Match.PathPrefix &&
		a.Match.PathRegex == b.Match.PathRegex && a.Match.UserAgent == b.Match.UserAgent &&
		slices.Equal(a.Match.Methods, b.Match.Methods) && slices.Equal(a.Match.ClientFrom, b.Match.ClientFrom)
}

// ValidateRateLimit checks a route's request rate.
func ValidateRateLimit(limit DeploymentRateLimit) error {
	if limit.Requests < 1 || limit.Requests > MaxRateLimitRequests {
		return fmt.Errorf("a rate limit lets between 1 and %d requests through", MaxRateLimitRequests)
	}
	if limit.Per != "second" && limit.Per != "minute" {
		return errors.New("a rate limit counts requests per second or per minute")
	}
	if limit.Burst < 0 || limit.Burst > MaxRateLimitBurst {
		return fmt.Errorf("the burst is between 0 and %d", MaxRateLimitBurst)
	}
	return nil
}

// NginxRate is the limit in nginx's own spelling, 10r/s or 60r/m.
func (limit DeploymentRateLimit) NginxRate() string {
	unit := "s"
	if limit.Per == "minute" {
		unit = "m"
	}
	return strconv.Itoa(limit.Requests) + "r/" + unit
}

// RetryAfter is the seconds a refused client is told to wait: the time one
// request's worth of allowance takes to come back, at least a second.
func (limit DeploymentRateLimit) RetryAfter() int {
	window := 1.0
	if limit.Per == "minute" {
		window = 60
	}
	if limit.Requests <= 0 {
		return int(window)
	}
	return max(1, int(math.Ceil(window/float64(limit.Requests))))
}

// edgeCrowdSecRule is the name a request record carries for a request the
// CrowdSec list refused, which is not one of the route's own rules.
const edgeCrowdSecRule = "crowdsec"

// The variables and log field the edge rules are rendered with on nginx.
// The decision is _rules: _edge is the Cloudflare lock's (site_realip.go).
func (spec *SiteSpec) edgeDecisionVar() string { return spec.varIdent() + "_rules" }
func (spec *SiteSpec) edgeFenceVar() string    { return spec.varIdent() + "_fence" }
func (spec *SiteSpec) edgeCrowdVar() string    { return spec.varIdent() + "_crowdsec" }
func (spec *SiteSpec) edgeRuleVar(index int, part string) string {
	name := spec.varIdent() + "_e" + strconv.Itoa(index+1)
	if part != "" {
		name += "_" + part
	}
	return name
}

// hasEdge says whether the route decides anything at the edge.
func (spec *SiteSpec) hasEdge() bool {
	return spec.Kind != "redirect" && (len(spec.edgeRules) > 0 || spec.crowdSecList != "")
}

// nginxEdgeRegex is a pattern as an nginx map source: a regex marker, the
// pattern, quoted. Validation keeps quotes and braces out of it.
func nginxEdgeRegex(caseless bool, pattern string) string {
	marker := "~"
	if caseless {
		marker = "~*"
	}
	return `"` + marker + pattern + `"`
}

// renderEdgeObjects writes the http-level half of the edge rules: one
// variable per condition, one per rule that is 1 when all its conditions
// are, and the decision — the name of the first rule a request meets, or
// nothing. A request outside the route's address fence is decided by the
// fence's 403, as Caddy decides it, so the fence comes first in the key.
func renderEdgeObjects(l *lines, spec *SiteSpec) {
	l.add("# Edge rules: the first rule a request meets decides what happens to it,")
	l.add("# before the application is asked. Its name is written to the access log.")
	keys := []string{}
	if len(spec.AllowFrom) > 0 {
		l.add("geo $%s {", spec.edgeFenceVar())
		l.add("    default 1;")
		for _, entry := range spec.AllowFrom {
			l.add("    %s 0;", entry)
		}
		l.add("}")
		keys = append(keys, "$"+spec.edgeFenceVar())
	}
	if spec.crowdSecList != "" {
		l.add("# The addresses CrowdSec's engine has banned, refreshed from its decisions.")
		l.add("geo $%s {", spec.edgeCrowdVar())
		l.add("    default 0;")
		l.add("    include %s;", spec.crowdSecList)
		l.add("}")
		keys = append(keys, "$"+spec.edgeCrowdVar())
	}
	for index, rule := range spec.edgeRules {
		parts := []string{}
		condition := func(part, head string, entries []string) {
			name := spec.edgeRuleVar(index, part)
			l.add("%s $%s {", head, name)
			l.add("    default 0;")
			for _, entry := range entries {
				l.add("    %s 1;", entry)
			}
			l.add("}")
			parts = append(parts, "$"+name)
		}
		if p := rule.Match.PathPrefix; p != "" {
			condition("prefix", "map $uri", []string{nginxEdgeRegex(false, "^"+regexp.QuoteMeta(p))})
		}
		if p := rule.Match.PathRegex; p != "" {
			condition("path", "map $uri", []string{nginxEdgeRegex(false, p)})
		}
		if len(rule.Match.Methods) > 0 {
			condition("method", "map $request_method", rule.Match.Methods)
		}
		if p := rule.Match.UserAgent; p != "" {
			condition("agent", "map $http_user_agent", []string{nginxEdgeRegex(true, p)})
		}
		if len(rule.Match.ClientFrom) > 0 {
			condition("client", "geo", rule.Match.ClientFrom)
		}
		name := spec.edgeRuleVar(index, "")
		l.add("map \"%s\" $%s {", strings.Join(parts, ""), name)
		l.add("    default 0;")
		l.add("    \"%s\" 1;", strings.Repeat("1", len(parts)))
		l.add("}")
		keys = append(keys, "$"+name)
	}
	l.add("map \"%s\" $%s {", strings.Join(keys, ""), spec.edgeDecisionVar())
	l.add("    default \"\";")
	position := 0
	if len(spec.AllowFrom) > 0 {
		position++
	}
	if spec.crowdSecList != "" {
		l.add("    \"~^%s1\" %s;", strings.Repeat("0", position), edgeCrowdSecRule)
		position++
	}
	for _, rule := range spec.edgeRules {
		l.add("    \"~^%s1\" %s;", strings.Repeat("0", position), rule.Name)
		position++
	}
	l.add("}")
}

// renderEdgeChecks writes the server-level half: what the deciding rule
// does. They are returns in the rewrite phase, after the maintenance check
// and before the address list and the password are asked, so a refused bot
// is never asked to sign in; an allow writes nothing, which is what lets it
// through without asking the rules after it.
func renderEdgeChecks(l *lines, spec *SiteSpec) {
	if !spec.hasEdge() {
		return
	}
	l.add("    # Edge rules, as the map above decides them.")
	if spec.crowdSecList != "" {
		l.add("    if ($%s = %s) {", spec.edgeDecisionVar(), edgeCrowdSecRule)
		l.add("        return 403;")
		l.add("    }")
	}
	for _, rule := range spec.edgeRules {
		answer := ""
		switch rule.Action.Kind {
		case "block":
			answer = "return 403;"
		case "status":
			answer = "return " + strconv.Itoa(rule.Action.Status) + ";"
		case "redirect":
			answer = "return " + strconv.Itoa(rule.Action.Code) + " " + rule.Action.Location + ";"
		default:
			continue
		}
		l.add("    if ($%s = %s) {", spec.edgeDecisionVar(), rule.Name)
		l.add("        %s", answer)
		l.add("    }")
	}
	l.blank()
}

// renderRateLimitAnswer tells a client refused by the route's rate limit when
// to come back: nginx answers limit_req with a bare 429, so the refusal goes
// to a location that adds Retry-After.
func renderRateLimitAnswer(l *lines, spec *SiteSpec) {
	line := rateLimitErrorPage(spec)
	if line == "" {
		return
	}
	l.add("    # Past the rate limit: 429, with how long the allowance takes to come back.")
	l.add("    %s", line)
	l.blank()
}

// rateLimitErrorPage is the error_page that sends the rate limit's 429 to the
// location saying when to come back, or "" for a route without a limit. A
// location that names error pages of its own says it again
// (serverErrorPages), since nginx then inherits none of the server's.
func rateLimitErrorPage(spec *SiteSpec) string {
	if spec.limitRetryAfter <= 0 || spec.Limits == nil || spec.Limits.Request == nil {
		return ""
	}
	return "error_page 429 = @" + spec.varIdent() + "_limited;"
}

func renderRateLimitLocation(l *lines, spec *SiteSpec) {
	if spec.limitRetryAfter <= 0 || spec.Limits == nil || spec.Limits.Request == nil {
		return
	}
	l.blank()
	l.add("    location @%s_limited {", spec.varIdent())
	l.add("        default_type text/plain;")
	l.add("        add_header Retry-After %d always;", spec.limitRetryAfter)
	l.add("        return 429 \"Too many requests. Try again in %s.\\n\";", plural(spec.limitRetryAfter, "second", "seconds"))
	l.add("    }")
}

// edgeLogField is the field the route's request record carries the deciding
// rule's name in, after nginx's combined format: er=<name>, or er= for a
// request no rule decided.
func (spec *SiteSpec) edgeLogField() string {
	if !spec.hasEdge() {
		return ""
	}
	return " er=$" + spec.edgeDecisionVar()
}

// caddyQuoted is a pattern as a quoted Caddyfile token. The Caddyfile reads
// a backslash inside quotes as itself, so the pattern is written as it is
// rather than Go-quoted; validation keeps quotes and braces out of it.
func caddyQuoted(pattern string) string { return `"` + pattern + `"` }

// caddyEdgeConditions is one rule's conditions as Caddy matchers, one per line.
func caddyEdgeConditions(match EdgeMatch) []string {
	conditions := []string{}
	// A prefix is a pattern anchored at the start rather than Caddy's path
	// matcher, which ignores case where nginx's prefix does not.
	if p := match.PathPrefix; p != "" {
		conditions = append(conditions, "path_regexp "+caddyQuoted("^"+regexp.QuoteMeta(p)))
	}
	if p := match.PathRegex; p != "" {
		conditions = append(conditions, "path_regexp "+caddyQuoted(p))
	}
	if len(match.Methods) > 0 {
		conditions = append(conditions, "method "+strings.Join(match.Methods, " "))
	}
	if p := match.UserAgent; p != "" {
		conditions = append(conditions, "header_regexp User-Agent "+caddyQuoted("(?i)"+p))
	}
	if len(match.ClientFrom) > 0 {
		conditions = append(conditions, "remote_ip "+strings.Join(match.ClientFrom, " "))
	}
	return conditions
}

// renderCaddyEdgeRules is the edge rules as directives of the route block,
// in the order Caddy runs them there. Each rule's matcher excludes every rule
// before it, so a request meets at most one: the first. Its name is set as a
// variable before anything answers, and log_append reads it when the request
// record is written, after whichever handler answered — its default; a key
// written <key would be read at once, before any rule had set it.
func renderCaddyEdgeRules(route DeploymentRoute, crowdSecFile string) string {
	if len(route.EdgeRules) == 0 && crowdSecFile == "" {
		return ""
	}
	var out strings.Builder
	out.WriteString("  log_append edge_rule {http.vars.jd_edge_rule}\n")
	if crowdSecFile != "" {
		// The banned addresses, refreshed from CrowdSec's decisions into a
		// file of their own that every route with the switch imports.
		out.WriteString("  import " + crowdSecFile + "\n")
	}
	earlier := [][]string{}
	for index, rule := range route.EdgeRules {
		name := "@jd_edge_" + strconv.Itoa(index+1)
		conditions := caddyEdgeConditions(rule.Match)
		out.WriteString("  " + name + " {\n")
		for _, condition := range conditions {
			out.WriteString("    " + condition + "\n")
		}
		for _, before := range earlier {
			out.WriteString("    not {\n")
			for _, condition := range before {
				out.WriteString("      " + condition + "\n")
			}
			out.WriteString("    }\n")
		}
		out.WriteString("  }\n")
		out.WriteString("  vars " + name + " jd_edge_rule " + rule.Name + "\n")
		switch rule.Action.Kind {
		case "block":
			out.WriteString("  respond " + name + " \"Forbidden\" 403\n")
		case "status":
			out.WriteString("  respond " + name + " " + strconv.Itoa(rule.Action.Status) + "\n")
		case "redirect":
			out.WriteString("  redir " + name + " " + rule.Action.Location + " " + strconv.Itoa(rule.Action.Code) + "\n")
		}
		earlier = append(earlier, conditions)
	}
	return out.String()
}

// renderCaddyCrowdSecFile is the shared file a route with the CrowdSec
// switch imports: the banned addresses as one matcher and its refusal. With
// nothing banned the file holds no matcher at all, since remote_ip with no
// range is not a matcher Caddy loads.
func renderCaddyCrowdSecFile(banned []string) string {
	var out strings.Builder
	out.WriteString("# Managed by Just Dashboard: the addresses CrowdSec's engine has banned,\n")
	out.WriteString("# refreshed from its decisions. Imported by every route that refuses them.\n")
	if len(banned) == 0 {
		return out.String()
	}
	out.WriteString("@jd_crowdsec remote_ip " + strings.Join(banned, " ") + "\n")
	out.WriteString("vars @jd_crowdsec jd_edge_rule " + edgeCrowdSecRule + "\n")
	out.WriteString("respond @jd_crowdsec \"Forbidden\" 403\n")
	return out.String()
}

// renderNginxCrowdSecFile is the shared list every route with the switch
// includes inside a geo block of its own.
func renderNginxCrowdSecFile(banned []string) string {
	var out strings.Builder
	out.WriteString("# Managed by Just Dashboard: the addresses CrowdSec's engine has banned,\n")
	out.WriteString("# refreshed from its decisions. Included by every route that refuses them.\n")
	for _, entry := range banned {
		out.WriteString(entry + " 1;\n")
	}
	return out.String()
}
