package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Static output is served by nginx with a configuration of the platform's
// own. nginx's stock server answered /about with 404 for every generator that
// writes about.html (SvelteKit's adapter-static, VitePress's clean URLs, Next's
// export), redirected a directory to http:// behind the TLS proxy, ignored the
// site's 404.html and served dotfiles. What the site's own files say — the
// sub-path it was built for, the page its client falls back to, the redirects
// and headers another host's files declare — is read from the tree when the
// build is prepared, as bounded data, and written into the configuration as
// literal rules that pass a strict character check. Nothing a repository holds
// is evaluated: a rule that needs more than a path, a status and a target is
// counted and left out, never approximated.

// staticServing is how nginx serves one site.
type staticServing struct {
	spaFallback bool
	// fallback is the page the single-page fallback answers with, from the
	// site's root: "/index.html", SvelteKit's configured "/200.html", or the
	// page a host's `/* /app.html 200` rule names.
	fallback string
	// basePath is the sub-path the site was built for ("/docs"), without a
	// trailing slash; the files are served under it and / redirects there.
	basePath string
	// apiFunctions says the site keeps functions its host serves under /api,
	// which this server does not run.
	apiFunctions bool
	rules        hostingRules
}

// staticRedirect is one redirect or rewrite rule. A splat rule matches every
// path under from, which then ends in a slash, and carries the rest of the
// path to a target that ends in ":splat".
type staticRedirect struct {
	from   string
	splat  bool
	to     string
	status int
}

// staticHeaderRule adds response headers to every path (path ""), to the
// paths under a prefix, or to one exact path.
type staticHeaderRule struct {
	path   string
	prefix bool
	values [][2]string
}

// hostingRules are the redirects and headers a site's hosting files declare,
// as far as nginx is given them.
type hostingRules struct {
	redirects []staticRedirect
	headers   []staticHeaderRule
	// spa says a rule rewrites every path to one page, which is the
	// single-page fallback by another name; fallback is that page when it
	// is not /index.html.
	spa      bool
	fallback string
	// proxied are the paths a rule sends to another host or to a Netlify
	// function, neither of which this server reaches: the single-page
	// fallback leaves them to answer 404 rather than with the page.
	proxied []string
	// unsupported counts the rules left out: placeholders, conditions, a
	// proxy to another host, a header that is not a plain value, a second
	// rule for a path an earlier one already took.
	unsupported int
	// files names the files the rules came from, for the evidence.
	files []string
}

const (
	staticMaxRedirects   = 100
	staticMaxHeaderRules = 64
	staticHostingFileMax = 64 << 10
)

var (
	staticRulePathRE   = regexp.MustCompile(`^/[A-Za-z0-9._~%/-]*$`)
	staticRuleTargetRE = regexp.MustCompile(`^(?:https?://[A-Za-z0-9.-]+(?::[0-9]{1,5})?)?(?:/[A-Za-z0-9._~%/-]*)?$`)
	staticHeaderNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)
	// A header value is printable ASCII without the characters that would end
	// nginx's quoted string or start one of its variables.
	staticHeaderValueRE = regexp.MustCompile(`^[\x20-\x21\x23\x25-\x5b\x5d-\x7a\x7c\x7e]+$`)
	staticBasePathRE    = regexp.MustCompile(`^/(?:[A-Za-z0-9_~-][A-Za-z0-9._~-]*/?)*$`)
	staticFallbackRE    = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*\.html$`)
	staticRuleStatusRE  = regexp.MustCompile(`^[0-9]{3}!?$`)
)

// staticHeaderRefused are headers a static response must not be given from a
// repository's file: framing and transport headers nginx owns, a second
// Content-Type, and Netlify's Basic-Auth, which is a password, not a header.
var staticHeaderRefused = map[string]bool{
	"content-length": true, "content-type": true, "transfer-encoding": true, "connection": true,
	"keep-alive": true, "upgrade": true, "host": true, "basic-auth": true, "trailer": true, "te": true,
	"proxy-connection": true, "date": true, "server": true,
}

// siteFiles reads a site's files by their path relative to its root, as
// bounded data; ok is false for a file that is not there.
type siteFiles func(relative string) ([]byte, bool)

func containedSiteFiles(root string) siteFiles {
	return func(relative string) ([]byte, bool) {
		if !safeRelativePath(relative) {
			return nil, false
		}
		content, err := readContainedRegular(root, relative, staticHostingFileMax)
		if err != nil {
			return nil, false
		}
		return manifestText(content), true
	}
}

// readStaticServing is how a site prepared from root is served: the plan's
// fallback switch, and what the site's own files say. framework is the
// recognised framework whose configuration names a base path, and output the
// directory served, relative to root.
func readStaticServing(boundary, root string, config BuildPlanConfig, framework string) staticServing {
	read := containedSiteFiles(root)
	serving := staticServing{spaFallback: config.SPAFallback, fallback: "/index.html"}
	var manifest nodeManifest
	if content, ok := read("package.json"); ok {
		parseNodeManifest(content, &manifest)
	}
	if base, _, _ := staticBasePath(read, framework, strings.TrimSpace(config.OutputDirectory), manifest); base != "" {
		serving.basePath = base
	}
	if framework == "sveltekit" {
		if fallback := svelteKitFallback(read); fallback != "" {
			serving.fallback = "/" + fallback
		}
	}
	serving.rules = readHostingRules(read, netlifyBaseFiles(boundary, root), staticHostingPlaces(config))
	if serving.rules.fallback != "" {
		serving.fallback = serving.rules.fallback
	}
	serving.apiFunctions = plainDirectoryHolds(root, "api", functionFile) || plainDirectoryHolds(root, "functions/api", functionFile)
	return serving
}

// staticHostingPlaces are where a site's _redirects and _headers are read
// from: beside its pages, and first in the directory a committed site
// serves, which is the one a host would have read them from.
func staticHostingPlaces(config BuildPlanConfig) []string {
	output := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(config.OutputDirectory), "./"), "/")
	if config.Method != BuildStatic || output == "" || output == "." || !safeRelativePath(output) || slices.Contains(staticHostingDirectories, output) {
		return staticHostingDirectories
	}
	return append([]string{path.Clean(output)}, staticHostingDirectories...)
}

// plainDirectoryHolds says a directory of root, reached without a symlink,
// holds a file the predicate accepts, directly or one directory down; a
// listing reads names only, and at most 256 of them in each directory.
func plainDirectoryHolds(root, relative string, accept func(relative string) bool) bool {
	if !safeRelativePath(relative) {
		return false
	}
	current := root
	for _, segment := range strings.Split(relative, "/") {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	var holds func(directory, relative string, depth int) bool
	holds = func(directory, relative string, depth int) bool {
		handle, err := os.Open(directory)
		if err != nil {
			return false
		}
		entries, _ := handle.ReadDir(256)
		_ = handle.Close()
		for _, entry := range entries {
			name := relative + "/" + entry.Name()
			switch {
			case entry.Type().IsRegular() && accept(name):
				return true
			case entry.IsDir() && depth == 0 && holds(filepath.Join(directory, entry.Name()), name, 1):
				return true
			}
		}
		return false
	}
	return holds(current, relative, 0)
}

// netlifyBaseFiles reads the netlify.toml above a root whose [build] base is
// that root, which Netlify reads from the repository's top.
func netlifyBaseFiles(boundary, root string) siteFiles {
	relative := checkoutPath(boundary, root)
	if relative == "" {
		return nil
	}
	content, err := readContainedRegular(boundary, "netlify.toml", staticHostingFileMax)
	if err != nil {
		return nil
	}
	base, ok := platformPath("", tomlText(readTOML(content), "build", "base"))
	if !ok || base != relative {
		return nil
	}
	text := manifestText(content)
	return func(relative string) ([]byte, bool) {
		if relative == "netlify.toml" {
			return text, true
		}
		return nil, false
	}
}

// staticHostingDirectories are where a site keeps the files a host copies
// into its output unchanged: _redirects and _headers belong beside the
// pages, so a framework's public/ or static/ folder carries them.
var staticHostingDirectories = []string{"", "public", "static"}

// readHostingRules reads Netlify's and Cloudflare Pages' _redirects and
// _headers from the first of places that holds each, netlify.toml and
// vercel.json, in the order those hosts apply them. above supplies a
// netlify.toml read from further up the checkout.
func readHostingRules(read siteFiles, above siteFiles, places []string) hostingRules {
	var rules hostingRules
	for _, name := range []string{"_redirects", "_headers"} {
		for _, directory := range places {
			relative := path.Join(directory, name)
			content, ok := read(relative)
			if !ok {
				continue
			}
			rules.files = append(rules.files, relative)
			if name == "_redirects" {
				rules.addRedirectsFile(content)
			} else {
				rules.addHeadersFile(content)
			}
			break
		}
	}
	netlify, ok := read("netlify.toml")
	if !ok && above != nil {
		netlify, ok = above("netlify.toml")
	}
	if ok {
		rules.files = append(rules.files, "netlify.toml")
		rules.addNetlifyTOML(netlify)
	}
	if content, ok := read("vercel.json"); ok {
		rules.files = append(rules.files, "vercel.json")
		rules.addVercelJSON(content)
	}
	return rules
}

// addRedirectsFile reads the _redirects line format: `from to [status][!]`,
// with anything after the status a condition this server cannot evaluate.
func (r *hostingRules) addRedirectsFile(content []byte) {
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			r.unsupported++
			continue
		}
		status, conditions := "", fields[2:]
		if len(conditions) > 0 && staticRuleStatusRE.MatchString(conditions[0]) {
			status, conditions = conditions[0], conditions[1:]
		}
		if len(conditions) > 0 {
			r.unsupported++
			continue
		}
		r.addRedirect(fields[0], fields[1], status)
	}
}

// addHeadersFile reads the _headers format: a path on its own line, then
// indented `Name: value` lines.
func (r *hostingRules) addHeadersFile(content []byte) {
	var current *staticHeaderRule
	valid := false
	flush := func() {
		if current != nil && valid && len(current.values) > 0 {
			r.addHeaderRule(*current)
		}
		current, valid = nil, false
	}
	for _, raw := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if raw[0] != ' ' && raw[0] != '\t' {
			flush()
			rule, ok := staticHeaderPath(trimmed)
			current, valid = &rule, ok
			if !ok {
				r.unsupported++
			}
			continue
		}
		if current == nil || !valid {
			continue
		}
		if name, value, found := strings.Cut(trimmed, ":"); found {
			r.addHeaderValue(current, name, value)
		} else {
			r.unsupported++
		}
	}
	flush()
}

func (r *hostingRules) addNetlifyTOML(content []byte) {
	type redirect struct {
		fields      map[string]string
		conditional bool
	}
	redirects := map[int]*redirect{}
	headers := map[int]*staticHeaderRule{}
	valid := map[int]bool{}
	var redirectOrder, headerOrder []int
	lastRedirect, lastHeader := -1, -1
	for _, entry := range readTOML(content) {
		switch {
		case entry.table == "redirects" && entry.index >= 0:
			lastRedirect = entry.index
			if redirects[entry.index] == nil {
				redirects[entry.index] = &redirect{fields: map[string]string{}}
				redirectOrder = append(redirectOrder, entry.index)
			}
			key, _, nested := strings.Cut(entry.key, ".")
			switch key {
			case "conditions", "query", "headers", "signed":
				redirects[entry.index].conditional = true
			default:
				if !nested {
					redirects[entry.index].fields[key] = entry.value.text
				}
			}
		case strings.HasPrefix(entry.table, "redirects.") && lastRedirect >= 0:
			// [redirects.conditions] and its kin belong to the rule above.
			redirects[lastRedirect].conditional = true
		case entry.table == "headers" && entry.index >= 0:
			lastHeader = entry.index
			if headers[entry.index] == nil {
				headers[entry.index] = &staticHeaderRule{}
				headerOrder = append(headerOrder, entry.index)
			}
			switch {
			case entry.key == "for":
				rule, ok := staticHeaderPath(entry.value.text)
				rule.values = headers[entry.index].values
				*headers[entry.index], valid[entry.index] = rule, ok
			case strings.HasPrefix(entry.key, "values."):
				r.addHeaderValue(headers[entry.index], strings.TrimPrefix(entry.key, "values."), entry.value.text)
			}
		case entry.table == "headers.values" && lastHeader >= 0:
			// [headers.values] belongs to the [[headers]] above it.
			r.addHeaderValue(headers[lastHeader], entry.key, entry.value.text)
		}
	}
	for _, index := range redirectOrder {
		rule := redirects[index]
		if rule.conditional {
			r.unsupported++
			continue
		}
		r.addRedirect(rule.fields["from"], rule.fields["to"], rule.fields["status"])
	}
	for _, index := range headerOrder {
		if !valid[index] {
			r.unsupported++
			continue
		}
		if len(headers[index].values) > 0 {
			r.addHeaderRule(*headers[index])
		}
	}
}

func (r *hostingRules) addVercelJSON(content []byte) {
	var document struct {
		Redirects []struct {
			Source      string          `json:"source"`
			Destination string          `json:"destination"`
			Permanent   *bool           `json:"permanent"`
			StatusCode  int             `json:"statusCode"`
			Has         json.RawMessage `json:"has"`
			Missing     json.RawMessage `json:"missing"`
		} `json:"redirects"`
		Rewrites []struct {
			Source      string          `json:"source"`
			Destination string          `json:"destination"`
			Has         json.RawMessage `json:"has"`
			Missing     json.RawMessage `json:"missing"`
		} `json:"rewrites"`
		Headers []struct {
			Source  string          `json:"source"`
			Has     json.RawMessage `json:"has"`
			Missing json.RawMessage `json:"missing"`
			Headers []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"headers"`
		} `json:"headers"`
	}
	if json.Unmarshal(content, &document) != nil {
		return
	}
	conditional := func(has, missing json.RawMessage) bool {
		return (len(has) > 0 && string(has) != "null" && string(has) != "[]") || (len(missing) > 0 && string(missing) != "null" && string(missing) != "[]")
	}
	for _, redirect := range document.Redirects {
		if conditional(redirect.Has, redirect.Missing) {
			r.unsupported++
			continue
		}
		status := redirect.StatusCode
		if status == 0 {
			status = 308
			if redirect.Permanent != nil && !*redirect.Permanent {
				status = 307
			}
		}
		r.addRedirect(vercelPattern(redirect.Source), vercelDestination(redirect.Destination), strconv.Itoa(status))
	}
	for _, rewrite := range document.Rewrites {
		if conditional(rewrite.Has, rewrite.Missing) {
			r.unsupported++
			continue
		}
		destination := vercelDestination(rewrite.Destination)
		if destination == "/index.html" || destination == "/" {
			if source := vercelPattern(rewrite.Source); source == "/*" {
				r.spa = true
				continue
			}
		}
		r.addRedirect(vercelPattern(rewrite.Source), destination, "200")
	}
	for _, header := range document.Headers {
		rule, ok := staticHeaderPath(vercelPattern(header.Source))
		if !ok || conditional(header.Has, header.Missing) {
			r.unsupported++
			continue
		}
		for _, value := range header.Headers {
			r.addHeaderValue(&rule, value.Key, value.Value)
		}
		if len(rule.values) > 0 {
			r.addHeaderRule(rule)
		}
	}
}

// vercelPattern rewrites the path-to-regexp tails Vercel's files use for
// "everything under here" into the splat form the other files share:
// /blog/:path*, /blog/(.*) and /blog/:slug(.*) become /blog/*, and the same
// parameter in a destination becomes :splat.
var vercelSplatRE = regexp.MustCompile(`/(?::[A-Za-z_][A-Za-z0-9_]*\*|\(\.\*\)|:[A-Za-z_][A-Za-z0-9_]*\(\.\*\))$`)

func vercelPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if strings.HasPrefix(pattern, "http://") || strings.HasPrefix(pattern, "https://") {
		scheme, rest, _ := strings.Cut(pattern, "://")
		host, route, found := strings.Cut(rest, "/")
		if !found {
			return pattern
		}
		return scheme + "://" + host + vercelPattern("/"+route)
	}
	if !vercelSplatRE.MatchString(pattern) {
		return pattern
	}
	return vercelSplatRE.ReplaceAllString(pattern, "/*")
}

// vercelDestination is a destination in the splat form: the parameter a source
// captured, carried to the end of the target.
func vercelDestination(destination string) string {
	if target := vercelPattern(destination); strings.HasSuffix(target, "/*") {
		return strings.TrimSuffix(target, "*") + ":splat"
	}
	return strings.TrimSpace(destination)
}

// addRedirect translates one rule, or counts it as left out. A redirect
// (3xx) may go to a path or an http(s) URL; a rewrite (200) only to a path
// of this site, since serving another host's response would be a proxy.
// The first rule for a path is the one kept, as the hosts apply the first
// rule that matches; nginx would not start with two blocks for one path.
func (r *hostingRules) addRedirect(from, to, status string) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	code := 301
	if status = strings.TrimSuffix(strings.TrimSpace(status), "!"); status != "" {
		parsed, err := strconv.Atoi(status)
		if err != nil {
			r.unsupported++
			return
		}
		code = parsed
	}
	if from == "/" && (to == "/index.html" || to == "/") && code == 200 {
		// The root rewritten to its own page changes nothing.
		return
	}
	if code == 404 && to == "/404.html" {
		// The server answers a missing page with the site's 404.html already.
		return
	}
	rule := staticRedirect{from: from, to: to, status: code}
	if strings.HasSuffix(rule.from, "/*") {
		rule.splat, rule.from = true, strings.TrimSuffix(rule.from, "*")
	}
	splatTarget := strings.HasSuffix(rule.to, ":splat")
	target := strings.TrimSuffix(rule.to, ":splat")
	local := strings.HasPrefix(target, "/")
	validFrom := staticRulePathRE.MatchString(rule.from) && !strings.Contains(rule.from, "//") && !staticHiddenPath(rule.from)
	if code == 200 && validFrom && (!local || strings.HasPrefix(target, "/.netlify/")) {
		// A rewrite to another host, or to a Netlify function, needs code
		// this server does not run.
		route := rule.from
		if !rule.splat {
			route = "= " + rule.from
		}
		if len(r.proxied) < 16 && !slices.Contains(r.proxied, route) {
			r.proxied = append(r.proxied, route)
		}
		r.unsupported++
		return
	}
	switch {
	case !validFrom, rule.from == "/404.html",
		!staticRuleTargetRE.MatchString(target) || target == "", local && staticHiddenPath(target),
		splatTarget && (!rule.splat || !strings.HasSuffix(target, "/")),
		code != 200 && code != 301 && code != 302 && code != 303 && code != 307 && code != 308,
		rule.from == target && !rule.splat:
		r.unsupported++
		return
	}
	if rule.splat && rule.from == "/" && code == 200 && !splatTarget {
		// Every path answering with one page is the single-page fallback;
		// only a page can be that answer, and only one page.
		page := target
		if page == "/" {
			page = "/index.html"
		}
		switch {
		case !strings.HasSuffix(page, ".html"), r.spa && r.fallbackPage() != page:
			r.unsupported++
		case page != "/index.html":
			r.spa, r.fallback = true, page
		default:
			r.spa = true
		}
		return
	}
	if rule.splat && local && strings.HasPrefix(target, rule.from) && (code != 200 || splatTarget) {
		// A redirect or rewrite into its own prefix would match itself again.
		r.unsupported++
		return
	}
	for _, existing := range r.redirects {
		if existing.match() == rule.match() {
			if existing != rule {
				r.unsupported++
			}
			return
		}
	}
	if len(r.redirects) >= staticMaxRedirects {
		r.unsupported++
		return
	}
	r.redirects = append(r.redirects, rule)
}

// fallbackPage is the page a rule made the single-page fallback answer with.
func (r hostingRules) fallbackPage() string {
	if r.fallback != "" {
		return r.fallback
	}
	return "/index.html"
}

// staticHiddenPath says a rule names a path the server never serves: a
// dot-file or dot-directory other than .well-known, or a host's own rules
// file. An exact location for it would end nginx's search before the
// refusal that keeps it private.
func staticHiddenPath(value string) bool {
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if strings.HasPrefix(segment, ".") && segment != ".well-known" {
			return true
		}
	}
	last := segments[len(segments)-1]
	return last == "_redirects" || last == "_headers"
}

// staticHeaderPath reads a header rule's path: /* for every path, /dir/* for
// a prefix, anything else an exact path.
func staticHeaderPath(pattern string) (staticHeaderRule, bool) {
	pattern = strings.TrimSpace(pattern)
	valid := func(value string) bool {
		return staticRulePathRE.MatchString(value) && !strings.Contains(value, "//") && !staticHiddenPath(value)
	}
	switch {
	case pattern == "/*":
		return staticHeaderRule{}, true
	case strings.HasSuffix(pattern, "/*"):
		prefix := strings.TrimSuffix(pattern, "*")
		return staticHeaderRule{path: prefix, prefix: true}, valid(prefix)
	}
	return staticHeaderRule{path: pattern}, valid(pattern)
}

// addHeaderValue adds one header to a rule when it is a plain value nginx
// can be given as written; anything else is counted as left out.
func (r *hostingRules) addHeaderValue(rule *staticHeaderRule, name, value string) bool {
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if !staticHeaderNameRE.MatchString(name) || staticHeaderRefused[strings.ToLower(name)] ||
		len(value) > 1024 || !staticHeaderValueRE.MatchString(value) || len(rule.values) >= 16 {
		r.unsupported++
		return false
	}
	rule.values = append(rule.values, [2]string{name, value})
	return true
}

func (r *hostingRules) addHeaderRule(rule staticHeaderRule) {
	for index := range r.headers {
		if r.headers[index].path == rule.path && r.headers[index].prefix == rule.prefix {
			// Two files declaring the same header for one path send it once.
			for _, value := range rule.values {
				if !slices.Contains(r.headers[index].values, value) {
					r.headers[index].values = append(r.headers[index].values, value)
				}
			}
			return
		}
	}
	if len(r.headers) >= staticMaxHeaderRules {
		r.unsupported++
		return
	}
	r.headers = append(r.headers, rule)
}

// translated is how many rules nginx is given.
func (r hostingRules) translated() int {
	count := len(r.redirects)
	for _, rule := range r.headers {
		count += len(rule.values)
	}
	return count
}

// staticLocation is one location block of the server: what it matches, the
// directive that answers, and the headers a rule adds to its responses.
type staticLocation struct {
	match, answer string
	headers       [][2]string
}

// staticLocations are the server's location blocks in the order they are
// written, which is the order nginx tries its regular expressions in. A
// match has one block: nginx refuses to start on a second `location = /x`
// or `location /x/`, so a later rule for the same match is not written and
// a header rule joins the block that is there.
type staticLocations struct {
	blocks []*staticLocation
	byKey  map[string]*staticLocation
}

func (l *staticLocations) add(match, answer string) {
	if l.byKey == nil {
		l.byKey = map[string]*staticLocation{}
	}
	if l.byKey[match] != nil {
		return
	}
	block := &staticLocation{match: match, answer: answer}
	l.blocks = append(l.blocks, block)
	l.byKey[match] = block
}

func (l *staticLocations) addHeaders(match, answer string, values [][2]string) {
	l.add(match, answer)
	for _, value := range values {
		if block := l.byKey[match]; !slices.Contains(block.headers, value) {
			block.headers = append(block.headers, value)
		}
	}
}

func (l staticLocations) lines(global [][2]string) []string {
	var lines []string
	for _, block := range l.blocks {
		lines = append(lines, "    location "+block.match+" {")
		if len(block.headers) > 0 {
			// A location with headers of its own inherits none from the
			// server, so the site-wide ones are repeated.
			lines = append(lines, staticHeaderLines("        ", global)...)
			lines = append(lines, staticHeaderLines("        ", block.headers)...)
		}
		lines = append(lines, "        "+block.answer, "    }")
	}
	return lines
}

// serverLines renders the site's nginx configuration as one RUN that writes
// it: each line is a single-quoted printf argument, so the configuration's
// own quotes and dollars reach the file exactly as written.
func (s staticServing) serverLines() []string {
	base := s.basePath
	try := "try_files $uri $uri.html $uri/ =404;"
	if s.spaFallback {
		fallback := s.fallback
		if fallback == "" {
			fallback = "/index.html"
		}
		try = "try_files $uri $uri.html $uri/ " + base + fallback + ";"
	}
	conf := []string{
		"server {", "    listen 80;", "    server_name _;", "    root /usr/share/nginx/html;",
		"    index index.html index.htm;",
		// Behind the proxy the browser speaks https to another port; the
		// slash nginx adds to a directory must keep both, so the Location
		// it sends is relative.
		"    absolute_redirect off;",
		"    gzip on;",
		"    gzip_types text/css text/plain text/xml application/javascript application/json application/xml image/svg+xml;",
	}
	var global [][2]string
	for _, rule := range s.rules.headers {
		if rule.path == "" {
			global = append(global, rule.values...)
		}
	}
	conf = append(conf, staticHeaderLines("    ", global)...)
	var locations staticLocations
	locations.add(`~ /\.(?!well-known/)`, "deny all;")
	// A host's rules files sit beside the pages and are configuration.
	locations.add(`~ /_(?:redirects|headers)$`, "return 404;")
	if base != "" {
		locations.add("= /", "return 302 "+base+"/;")
	}
	locations.add("/", try)
	// The site's 404.html is the answer to a missing page, never the
	// single-page fallback; without one nginx answers with its own page.
	locations.add("= "+base+"/404.html", "try_files $uri =404;")
	for _, redirect := range s.rules.redirects {
		redirect = redirect.under(base)
		locations.add(redirect.match(), redirect.answer())
	}
	if s.spaFallback {
		// The routes of functions another host ran, and of hosts it proxied
		// to, answer 404 rather than the application's page with a 200.
		missing := "try_files $uri $uri.html $uri/ =404;"
		if s.apiFunctions {
			locations.add(`~ ^/api(?:/|$)`, missing)
		}
		for _, route := range s.rules.proxied {
			if exact, found := strings.CutPrefix(route, "= "); found {
				locations.add("= "+underBase(base, exact), missing)
			} else {
				locations.add(underBase(base, route), missing)
			}
		}
	}
	for _, rule := range s.rules.headers {
		if rule.path == "" {
			continue
		}
		match := underBase(base, rule.path)
		if !rule.prefix {
			match = "= " + match
		}
		locations.addHeaders(match, try, rule.values)
	}
	conf = append(conf, locations.lines(global)...)
	conf = append(conf, "    error_page 404 "+base+"/404.html;", "}")
	quoted := make([]string, 0, len(conf))
	for _, line := range conf {
		quoted = append(quoted, "'"+strings.ReplaceAll(line, "'", `'\''`)+"'")
	}
	return []string{"RUN printf '%s\\n' " + strings.Join(quoted, " ") + " > /etc/nginx/conf.d/default.conf"}
}

// underBase moves a path a hosting rule names from the site's root to where
// the site is served: a site built for a sub-path is served under it, and
// its rules were written for its own root.
func underBase(base, value string) string {
	if base == "" || !strings.HasPrefix(value, "/") || value == base || strings.HasPrefix(value, base+"/") {
		return value
	}
	return base + value
}

func (r staticRedirect) under(base string) staticRedirect {
	r.from = underBase(base, r.from)
	if strings.HasPrefix(r.to, "/") {
		r.to = underBase(base, r.to)
	}
	return r
}

func staticHeaderLines(indent string, values [][2]string) []string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, indent+"add_header "+value[0]+` "`+value[1]+`" always;`)
	}
	return lines
}

// match is the rule's nginx location: an exact path, the prefix of a
// single-page application in a directory, or a regular expression that
// captures the rest of a splat's path.
func (r staticRedirect) match() string {
	switch {
	case !r.splat:
		return "= " + r.from
	case r.status == 200 && !strings.HasSuffix(r.to, ":splat"):
		return r.from
	}
	return "~ ^" + nginxRegexLiteral(r.from) + "(.*)$"
}

// answer is the directive the rule's location answers with.
func (r staticRedirect) answer() string {
	target := strings.TrimSuffix(r.to, ":splat")
	splatTarget := strings.HasSuffix(r.to, ":splat")
	status := strconv.Itoa(r.status)
	switch {
	case !r.splat && r.status == 200 && strings.HasSuffix(target, "/"):
		// A rewrite serves its target from this location, as the host does,
		// so the headers a rule adds to the path reach the response.
		return "try_files " + target + "index.html =404;"
	case !r.splat && r.status == 200:
		return "try_files " + target + " " + target + ".html " + target + "/index.html =404;"
	case !r.splat:
		return "return " + status + " " + target + "$is_args$args;"
	case r.status == 200 && !splatTarget:
		// Every path under the prefix answers with one page when it has no
		// file of its own: a single-page application in a sub-directory.
		return "try_files $uri $uri.html $uri/ " + target + ";"
	case r.status == 200:
		pattern := "^" + nginxRegexLiteral(r.from) + "(.*)$"
		return "rewrite " + pattern + " " + target + "$1 last;"
	case splatTarget:
		return "return " + status + " " + target + "$1$is_args$args;"
	}
	return "return " + status + " " + target + "$is_args$args;"
}

// nginxRegexLiteral escapes a rule path, which staticRulePathRE limits to
// characters of which only the dot means anything to a regular expression.
func nginxRegexLiteral(value string) string {
	return strings.ReplaceAll(value, ".", `\.`)
}

// stage renders the image that serves the site: nginx, its configuration,
// and the output copied from source (a build stage's path, or the context's
// own directory when from is empty) under the base path.
func (s staticServing) stage(nginx ResolvedImage, from, source string) []string {
	lines := append([]string{"FROM " + immutableImageReference(nginx)}, s.serverLines()...)
	copyFrom := "COPY "
	if from != "" {
		copyFrom = "COPY --from=" + from + " "
	}
	return append(lines, copyFrom+source+" /usr/share/nginx/html"+s.basePath+"/")
}

// staticServingStage is the serving stage every recipe with static output
// ends in, on the catalogue's nginx.
func staticServingStage(bases []ResolvedImage, serving staticServing, from, source string) ([]string, error) {
	nginx, err := resolveCatalogueImage(bases, recipeBaseCatalogue["static"][0])
	if err != nil {
		return nil, err
	}
	return serving.stage(nginx, from, source), nil
}

// staticOutputSource is where a build stage left a site's output, as the
// COPY source: workdir joined with the output directory, with a slash.
func staticOutputSource(workdir, output string) (string, error) {
	output = strings.TrimSpace(output)
	if output == "" || !validOutputDirectory(output) {
		return "", fmt.Errorf("%w: static output directory is invalid", ErrUnsupportedBuilder)
	}
	if output == "." {
		return strings.TrimSuffix(workdir, "/") + "/", nil
	}
	return strings.TrimSuffix(workdir, "/") + "/" + filepath.ToSlash(filepath.Clean(output)) + "/", nil
}

// cleanStaticBasePath is a sub-path in the form nginx serves it: a literal
// path of plain segments, without its trailing slash, or "" for the root.
func cleanStaticBasePath(value string) string {
	value = strings.TrimSpace(value)
	if !staticBasePathRE.MatchString(value) || strings.Contains(value, "//") {
		return ""
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return ""
		}
	}
	return strings.TrimSuffix(value, "/")
}
