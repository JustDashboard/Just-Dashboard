package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
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
	// site's root: "/index.html", or SvelteKit's configured "/200.html".
	fallback string
	// basePath is the sub-path the site was built for ("/docs"), without a
	// trailing slash; the files are served under it and / redirects there.
	basePath string
	rules    hostingRules
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
	// spa says a rule rewrites every path to /index.html, which is the
	// single-page fallback by another name.
	spa bool
	// unsupported counts the rules left out: placeholders, conditions, a
	// proxy to another host, a header that is not a plain value.
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
	serving.rules = readHostingRules(read, netlifyBaseFiles(boundary, root))
	return serving
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
// _headers, netlify.toml and vercel.json, in the order those hosts apply
// them. above supplies a netlify.toml read from further up the checkout.
func readHostingRules(read siteFiles, above siteFiles) hostingRules {
	var rules hostingRules
	for _, name := range []string{"_redirects", "_headers"} {
		for _, directory := range staticHostingDirectories {
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
	if (from == "/*" || from == "/") && (to == "/index.html" || to == "/") && code == 200 {
		r.spa = true
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
	switch {
	case len(r.redirects) >= staticMaxRedirects,
		!staticRulePathRE.MatchString(rule.from), strings.Contains(rule.from, "//"),
		!staticRuleTargetRE.MatchString(target) || target == "",
		splatTarget && (!rule.splat || !strings.HasSuffix(target, "/")),
		code != 200 && code != 301 && code != 302 && code != 303 && code != 307 && code != 308,
		code == 200 && !strings.HasPrefix(target, "/"),
		rule.from == target && !rule.splat:
		r.unsupported++
		return
	}
	if code == 200 && splatTarget && strings.HasPrefix(target, rule.from) {
		// A rewrite into its own prefix would match itself again.
		r.unsupported++
		return
	}
	r.redirects = append(r.redirects, rule)
}

// staticHeaderPath reads a header rule's path: /* for every path, /dir/* for
// a prefix, anything else an exact path.
func staticHeaderPath(pattern string) (staticHeaderRule, bool) {
	pattern = strings.TrimSpace(pattern)
	switch {
	case pattern == "/*":
		return staticHeaderRule{}, true
	case strings.HasSuffix(pattern, "/*"):
		prefix := strings.TrimSuffix(pattern, "*")
		return staticHeaderRule{path: prefix, prefix: true}, staticRulePathRE.MatchString(prefix) && !strings.Contains(prefix, "//")
	}
	return staticHeaderRule{path: pattern}, staticRulePathRE.MatchString(pattern) && !strings.Contains(pattern, "//")
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
	if len(r.headers) >= staticMaxHeaderRules {
		r.unsupported++
		return
	}
	for index := range r.headers {
		if r.headers[index].path == rule.path && r.headers[index].prefix == rule.prefix {
			r.headers[index].values = append(r.headers[index].values, rule.values...)
			return
		}
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
	conf = append(conf, `    location ~ /\.(?!well-known/) {`, "        deny all;", "    }",
		// A host's rules files sit beside the pages and are configuration.
		`    location ~ /_(?:redirects|headers)$ {`, "        return 404;", "    }")
	if base != "" {
		conf = append(conf, "    location = / {", "        return 302 "+base+"/;", "    }")
	}
	for _, redirect := range s.rules.redirects {
		conf = append(conf, redirect.under(base).lines(try)...)
	}
	if s.spaFallback {
		// A static site has no API; a request for one answers 404 rather
		// than the application's page with a 200.
		conf = append(conf, `    location ~ ^/api(?:/|$) {`, "        try_files $uri $uri.html $uri/ =404;", "    }")
	}
	for _, rule := range s.rules.headers {
		if rule.path == "" {
			continue
		}
		rule.path = underBase(base, rule.path)
		match := "= " + rule.path
		if rule.prefix {
			match = rule.path
		}
		// A location with headers of its own inherits none from the server,
		// so the site-wide ones are repeated.
		conf = append(conf, "    location "+match+" {")
		conf = append(conf, staticHeaderLines("        ", global)...)
		conf = append(conf, staticHeaderLines("        ", rule.values)...)
		conf = append(conf, "        "+try, "    }")
	}
	conf = append(conf, "    location / {", "        "+try, "    }",
		// The site's 404.html is the answer to a missing page, never the
		// single-page fallback; without one nginx answers with its own page.
		"    location = "+base+"/404.html {", "        try_files $uri =404;", "    }",
		"    error_page 404 "+base+"/404.html;", "}")
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

func (r staticRedirect) lines(try string) []string {
	target := strings.TrimSuffix(r.to, ":splat")
	splatTarget := strings.HasSuffix(r.to, ":splat")
	if !r.splat {
		if r.status == 200 {
			return []string{"    location = " + r.from + " {", "        rewrite ^ " + target + " last;", "    }"}
		}
		return []string{"    location = " + r.from + " {", "        return " + strconv.Itoa(r.status) + " " + target + "$is_args$args;", "    }"}
	}
	pattern := "^" + nginxRegexLiteral(r.from) + "(.*)$"
	switch {
	case r.status == 200 && !splatTarget:
		// Every path under the prefix answers with one page when it has no
		// file of its own: a single-page application in a sub-directory.
		return []string{"    location " + r.from + " {", "        " + strings.Replace(try, "=404", target, 1), "    }"}
	case r.status == 200:
		return []string{"    location ~ " + pattern + " {", "        rewrite " + pattern + " " + target + "$1 last;", "    }"}
	case splatTarget:
		return []string{"    location ~ " + pattern + " {", "        return " + strconv.Itoa(r.status) + " " + target + "$1$is_args$args;", "    }"}
	}
	return []string{"    location ~ " + pattern + " {", "        return " + strconv.Itoa(r.status) + " " + target + "$is_args$args;", "    }"}
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
