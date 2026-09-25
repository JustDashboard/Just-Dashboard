package deploy

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// staticConfig renders a serving configuration and returns the nginx
// configuration as the shell writes it: each printf argument unquoted.
func staticConfig(t *testing.T, serving staticServing) string {
	t.Helper()
	lines := serving.serverLines()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "RUN printf '%s\\n' ") ||
		!strings.HasSuffix(lines[0], " > /etc/nginx/conf.d/default.conf") {
		t.Fatalf("server lines = %q", lines)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(lines[0], "RUN printf '%s\\n' "), " > /etc/nginx/conf.d/default.conf")
	return strings.Join(quotedPrintfWords(t, body), "\n")
}

// quotedPrintfWords splits single-quoted shell words the way sh does, including
// the '\” sequence that puts a quote inside one.
func quotedPrintfWords(t *testing.T, text string) []string {
	t.Helper()
	var words []string
	var current strings.Builder
	quoted, inWord := false, false
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case quoted && character == '\'':
			quoted = false
		case quoted:
			current.WriteByte(character)
		case character == '\'':
			quoted, inWord = true, true
		case character == '\\' && index+1 < len(text):
			index++
			current.WriteByte(text[index])
			inWord = true
		case character == ' ':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			t.Fatalf("unquoted %q in %q", character, text)
		}
	}
	if quoted {
		t.Fatalf("unterminated quote in %q", text)
	}
	if inWord {
		words = append(words, current.String())
	}
	return words
}

func TestStaticServerServesCleanURLsAndTheSitesOwn404(t *testing.T) {
	t.Parallel()
	conf := staticConfig(t, staticServing{fallback: "/index.html"})
	for _, want := range []string{
		"    absolute_redirect off;", "    gzip on;", `    location ~ /\.(?!well-known/) {`,
		"    location / {\n        try_files $uri $uri.html $uri/ =404;\n    }",
		"    location = /404.html {\n        try_files $uri =404;\n    }", "    error_page 404 /404.html;",
		"    location ~ /_(?:redirects|headers)$ {\n        return 404;",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("configuration lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "/index.html;") || strings.Contains(conf, "^/api") {
		t.Fatalf("a multi-page site fell back to index.html:\n%s", conf)
	}
	// The directory redirect nginx adds must not name a scheme or host: the
	// proxy speaks https to the browser and http to nginx.
	if strings.Index(conf, "absolute_redirect off") > strings.Index(conf, "location /") {
		t.Fatal("absolute_redirect is not a server-wide setting")
	}
}

func TestSinglePageFallbackLeavesTheAPIAnd404Alone(t *testing.T) {
	t.Parallel()
	conf := staticConfig(t, staticServing{spaFallback: true, fallback: "/200.html"})
	for _, want := range []string{
		"        try_files $uri $uri.html $uri/ /200.html;",
		"    location ~ ^/api(?:/|$) {\n        try_files $uri $uri.html $uri/ =404;",
		"    location = /404.html {\n        try_files $uri =404;",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("configuration lacks %q:\n%s", want, conf)
		}
	}
	// The API location is a regular expression, so it must come after the
	// dot-path refusal, which nginx tries first.
	if strings.Index(conf, "^/api") < strings.Index(conf, `/\.(?!well-known/)`) {
		t.Fatalf("the API location precedes the dot-path refusal:\n%s", conf)
	}
}

func TestSubPathSitesAreServedUnderTheirBase(t *testing.T) {
	t.Parallel()
	serving := staticServing{spaFallback: true, fallback: "/index.html", basePath: "/docs"}
	serving.rules.addRedirect("/old", "/new", "301")
	serving.rules.addHeadersFile([]byte("/assets/*\n  Cache-Control: max-age=60\n"))
	conf := staticConfig(t, serving)
	for _, want := range []string{
		"    location = / {\n        return 302 /docs/;", "        try_files $uri $uri.html $uri/ /docs/index.html;",
		"    error_page 404 /docs/404.html;", "    location = /docs/old {\n        return 301 /docs/new$is_args$args;",
		"    location /docs/assets/ {",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("configuration lacks %q:\n%s", want, conf)
		}
	}
	stage := serving.stage(ResolvedImage{Reference: "nginx:1.29-alpine", Digest: "sha256:" + strings.Repeat("a", 64)}, "build", "/app/build/")
	if stage[len(stage)-1] != "COPY --from=build /app/build/ /usr/share/nginx/html/docs/" {
		t.Fatalf("stage = %q", stage)
	}
}

func TestHostingRulesBecomeLiteralNginxRules(t *testing.T) {
	t.Parallel()
	rules := hostingRules{}
	rules.addRedirectsFile([]byte(strings.Join([]string{
		"# comment", "", "/old /new", "/home / 302!", "/blog/* /news/:splat 301", "/docs/* https://docs.example.com/:splat 308",
		"/app/* /app/index.html 200", "/about /about-us.html 200",
		// Left out: placeholders, conditions, a proxy, a rewrite into its own prefix, a status nginx has no rule for.
		"/post/:id /p/:id 301", "/de/* /de/index.html 200 Country=de", "/api/* https://api.example.com/:splat 200",
		"/a/* /a/b/:splat 200", "/x /y 418",
		// The single-page rule is the fallback switch, and a 404 page is served already.
		"/* /index.html 200", "/* /404.html 404",
	}, "\n")))
	if !rules.spa || rules.unsupported != 5 || len(rules.redirects) != 6 {
		t.Fatalf("rules = %+v", rules)
	}
	conf := staticConfig(t, staticServing{rules: rules, fallback: "/index.html"})
	for _, want := range []string{
		"    location = /old {\n        return 301 /new$is_args$args;",
		"    location = /home {\n        return 302 /$is_args$args;",
		`    location ~ ^/blog/(.*)$ {` + "\n        return 301 /news/$1$is_args$args;",
		`    location ~ ^/docs/(.*)$ {` + "\n        return 308 https://docs.example.com/$1$is_args$args;",
		"    location /app/ {\n        try_files $uri $uri.html $uri/ /app/index.html;",
		"    location = /about {\n        rewrite ^ /about-us.html last;",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("configuration lacks %q:\n%s", want, conf)
		}
	}
}

func TestHostingHeadersAreQuotedAndRefusedWhenUnsafe(t *testing.T) {
	t.Parallel()
	rules := hostingRules{}
	rules.addHeadersFile([]byte("/*\n  X-Frame-Options: DENY\n  Content-Security-Policy: default-src 'self'; img-src *\n" +
		"  Basic-Auth: someone:hunter2\n  X-Home: $HOME\n  X-Quote: \"a\"\n" +
		"/assets/*\n  Cache-Control: public, max-age=31536000, immutable\n" +
		"/feed.xml\n  X-Robots-Tag: noindex\n" +
		"/:lang/*\n  X-Lang: yes\n"))
	if rules.unsupported != 4 || len(rules.headers) != 3 || rules.translated() != 4 {
		t.Fatalf("rules = %+v", rules)
	}
	conf := staticConfig(t, staticServing{rules: rules, fallback: "/index.html"})
	for _, want := range []string{
		`    add_header X-Frame-Options "DENY" always;`,
		`    add_header Content-Security-Policy "default-src 'self'; img-src *" always;`,
		"    location /assets/ {\n        add_header X-Frame-Options \"DENY\" always;",
		`        add_header Cache-Control "public, max-age=31536000, immutable" always;`,
		"    location = /feed.xml {",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("configuration lacks %q:\n%s", want, conf)
		}
	}
	for _, refused := range []string{"hunter2", "Basic-Auth", "$HOME", "X-Lang"} {
		if strings.Contains(conf, refused) {
			t.Fatalf("configuration carries %q:\n%s", refused, conf)
		}
	}
}

// The rendered line is run by /bin/sh in the build; what it writes must be
// the configuration exactly, whatever quotes a header value holds.
func TestServerLinesSurviveTheShell(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	rules := hostingRules{}
	rules.addHeadersFile([]byte("/*\n  Content-Security-Policy: default-src 'self' 'unsafe-inline'; frame-ancestors 'none'\n"))
	serving := staticServing{spaFallback: true, fallback: "/index.html", rules: rules}
	line := strings.TrimSuffix(strings.TrimPrefix(serving.serverLines()[0], "RUN "), " > /etc/nginx/conf.d/default.conf")
	output, err := exec.CommandContext(context.Background(), "sh", "-c", line).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `add_header Content-Security-Policy "default-src 'self' 'unsafe-inline'; frame-ancestors 'none'" always;`) ||
		string(output) != staticConfig(t, serving)+"\n" {
		t.Fatalf("shell wrote:\n%s", output)
	}
}

func TestNetlifyAndVercelRulesAreRead(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"netlify.toml": `[build]
  publish = "dist"
[[redirects]]
  from = "/old"
  to = "/new"
  status = 301
  force = true
[[redirects]]
  from = "/de/*"
  to = "/de/index.html"
  status = 200
  [redirects.conditions]
    Language = ["de"]
[[redirects]]
  from = "/q"
  to = "/r"
  query = {id = ":id"}
[[headers]]
  for = "/*"
  [headers.values]
    X-Frame-Options = "DENY"
[[headers]]
  for = "/fonts/*"
  values = { Cache-Control = "max-age=600" }
`,
		"vercel.json": `{"cleanUrls": true, "redirects": [{"source": "/blog/:path*", "destination": "/news/:path*"},
			{"source": "/tmp", "destination": "/", "permanent": false},
			{"source": "/only-mobile", "destination": "/m", "has": [{"type": "header", "key": "x"}]}],
		"rewrites": [{"source": "/(.*)", "destination": "/index.html"}],
		"headers": [{"source": "/(.*)", "headers": [{"key": "X-Content-Type-Options", "value": "nosniff"}]}]}`,
	}
	read := func(name string) ([]byte, bool) {
		content, ok := files[name]
		return []byte(content), ok
	}
	rules := readHostingRules(read, nil)
	if !rules.spa || rules.unsupported != 3 || strings.Join(rules.files, ",") != "netlify.toml,vercel.json" {
		t.Fatalf("rules = %+v", rules)
	}
	got := []string{}
	for _, redirect := range rules.redirects {
		got = append(got, fmt.Sprintf("%s %v %s %d", redirect.from, redirect.splat, redirect.to, redirect.status))
	}
	if strings.Join(got, "; ") != "/old false /new 301; /blog/ true /news/:splat 308; /tmp false / 307" {
		t.Fatalf("redirects = %q", got)
	}
	headers := map[string]string{}
	for _, rule := range rules.headers {
		for _, value := range rule.values {
			headers[rule.path+" "+value[0]] = value[1]
		}
	}
	if headers[" X-Frame-Options"] != "DENY" || headers["/fonts/ Cache-Control"] != "max-age=600" || headers[" X-Content-Type-Options"] != "nosniff" {
		t.Fatalf("headers = %v", headers)
	}
}

func TestHostingRulesAreBounded(t *testing.T) {
	t.Parallel()
	var file strings.Builder
	for index := 0; index < staticMaxRedirects+20; index++ {
		fmt.Fprintf(&file, "/old-%d /new-%d 301\n", index, index)
	}
	rules := hostingRules{}
	rules.addRedirectsFile([]byte(file.String()))
	if len(rules.redirects) != staticMaxRedirects || rules.unsupported != 20 {
		t.Fatalf("redirects %d, left out %d", len(rules.redirects), rules.unsupported)
	}
	// A path with anything nginx would read as syntax never reaches it.
	for _, line := range []string{"/a;b /c", "/a /b;return", "/a{ /b", "/a /b$x", "/a /'b", "/a /b\"", "/a\\b /c", "//evil /x"} {
		rules := hostingRules{}
		rules.addRedirectsFile([]byte(line))
		if len(rules.redirects) != 0 || rules.unsupported != 1 {
			t.Fatalf("%q = %+v", line, rules)
		}
	}
}

func TestStaticBasePathIsAPlainPath(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]string{
		"/docs/": "/docs", "/docs": "/docs", "/a/b/": "/a/b", "/": "", "": "", "docs": "", "/../x": "", "/a//b": "",
		"/a b": "", "/$x": "", "/.hidden/": "", "/v1.2/": "/v1.2",
	} {
		if got := cleanStaticBasePath(value); got != want {
			t.Fatalf("cleanStaticBasePath(%q) = %q, want %q", value, got, want)
		}
	}
	for pattern, want := range map[string]string{
		"/blog/:path*": "/blog/*", "/(.*)": "/*", "/docs/:slug(.*)": "/docs/*", "/exact": "/exact",
		"https://example.com/:path*": "https://example.com/*",
	} {
		if got := vercelPattern(pattern); got != want {
			t.Fatalf("vercelPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

// Prepare reads the rules from the commit, next to the pages: a Vite site
// keeps _redirects in public/, which the build copies into its output.
func TestPreparedStaticOutputCarriesTheSitesRules(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"7"}}`, "package-lock.json": "{}",
		"vite.config.ts":    "export default defineConfig({ base: '/repo/', plugins: [] })",
		"public/_redirects": "/old /new 301\n", "public/_headers": "/*\n  X-Frame-Options: DENY\n",
	} {
		writeBuildFixture(t, root, name, content)
	}
	prepared, err := NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), root,
		BuildPlanConfig{Method: BuildRecipe, Recipe: "node", BuildCommand: "npm run build", OutputDirectory: "dist", SPAFallback: true}, false, "t:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'    location = /repo/old {'", `'    add_header X-Frame-Options "DENY" always;'`,
		"'        return 302 /repo/;'", "COPY --from=build /app/dist/ /usr/share/nginx/html/repo/"} {
		if !strings.Contains(prepared.DockerfilePreview, want) {
			t.Fatalf("Dockerfile lacks %q:\n%s", want, prepared.DockerfilePreview)
		}
	}
	// A plain HTML site reads them from its own root.
	static := t.TempDir()
	writeBuildFixture(t, static, "index.html", "<h1>x</h1>")
	writeBuildFixture(t, static, "_redirects", "/team/* /about 302\n")
	prepared, err = NewArtifactBuilder(&artifactBackendFake{}).Prepare(context.Background(), static, BuildPlanConfig{Method: BuildStatic}, false, "t:1")
	if err != nil || !strings.Contains(prepared.DockerfilePreview, `'    location ~ ^/team/(.*)$ {' '        return 302 /about$is_args$args;'`) {
		t.Fatalf("static = %v\n%s", err, prepared.DockerfilePreview)
	}
}
