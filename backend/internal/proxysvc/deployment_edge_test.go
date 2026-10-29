package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func edgeRoute() DeploymentRoute {
	return DeploymentRoute{
		Name: "just-dashboard-env-12.conf", Domains: []string{"edge.example.test"}, Upstream: "http://127.0.0.1:32123",
		AllowFrom: []string{"10.0.0.0/8"},
		RateLimit: &DeploymentRateLimit{Requests: 5, Per: "second", Burst: 10},
		EdgeRules: CanonicalEdgeRules([]EdgeRule{
			{Name: "office", Match: EdgeMatch{PathPrefix: "/admin", ClientFrom: []string{"10.1.0.0/16"}}, Action: EdgeAction{Kind: "allow"}},
			{Name: "admin", Match: EdgeMatch{PathPrefix: "/admin"}, Action: EdgeAction{Kind: "block"}},
			{Name: "scrapers", Match: EdgeMatch{UserAgent: "curl|python-requests", Methods: []string{"post", "GET", "get"}}, Action: EdgeAction{Kind: "status", Status: 429}},
			{Name: "old-blog", Match: EdgeMatch{PathRegex: `^/blog/[0-9]+\.html$`}, Action: EdgeAction{Kind: "redirect", Location: "https://blog.example.test/archive"}},
		}),
	}
}

func TestEdgeRulesAreKeptInOneSpelling(t *testing.T) {
	t.Parallel()
	rules := edgeRoute().EdgeRules
	if rules[1].Action.Status != 403 || rules[3].Action.Code != 302 {
		t.Fatalf("a block's status and a redirect's code are not filled in: %+v", rules)
	}
	if got := strings.Join(rules[2].Match.Methods, ","); got != "GET,POST" {
		t.Fatalf("methods = %s, want upper-cased, sorted, once each", got)
	}
	if err := ValidateEdgeRules(rules); err != nil {
		t.Fatalf("canonical rules refused: %v", err)
	}
	loose := []EdgeRule{{Name: "a", Match: EdgeMatch{Methods: []string{"POST", "GET"}}, Action: EdgeAction{Kind: "block", Status: 403}}}
	if err := ValidateEdgeRules(loose); err == nil || !strings.Contains(err.Error(), "save it again") {
		t.Fatalf("a rule not written canonically = %v", err)
	}
}

func TestEdgeRulesRefusedAsBothProxiesWouldMisread(t *testing.T) {
	t.Parallel()
	block := EdgeAction{Kind: "block", Status: 403}
	for name, rule := range map[string]EdgeRule{
		"no condition":            {Name: "a", Action: block},
		"an uppercase name":       {Name: "Admin", Match: EdgeMatch{PathPrefix: "/a"}, Action: block},
		"a prefix and a pattern":  {Name: "a", Match: EdgeMatch{PathPrefix: "/a", PathRegex: "^/b"}, Action: block},
		"a relative prefix":       {Name: "a", Match: EdgeMatch{PathPrefix: "admin"}, Action: block},
		"a wildcard prefix":       {Name: "a", Match: EdgeMatch{PathPrefix: "/admin*"}, Action: block},
		"a quoted prefix":         {Name: "a", Match: EdgeMatch{PathPrefix: `/a"b`}, Action: block},
		"a lookahead":             {Name: "a", Match: EdgeMatch{PathRegex: "^/(?=x)"}, Action: block},
		"a counted repetition":    {Name: "a", Match: EdgeMatch{PathRegex: "^/a{2}"}, Action: block},
		"an inline flag":          {Name: "a", Match: EdgeMatch{UserAgent: "(?i)curl"}, Action: block},
		"an escaped backslash":    {Name: "a", Match: EdgeMatch{PathRegex: `^/a\\b`}, Action: block},
		"a control character":     {Name: "a", Match: EdgeMatch{UserAgent: "a\tb"}, Action: block},
		"an unknown method":       {Name: "a", Match: EdgeMatch{Methods: []string{"TRACE"}}, Action: block},
		"a bad address":           {Name: "a", Match: EdgeMatch{ClientFrom: []string{"10.0.0.1/8"}}, Action: block},
		"a block with a status":   {Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: EdgeAction{Kind: "block", Status: 404}},
		"a status that redirects": {Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: EdgeAction{Kind: "status", Status: 301}},
		"a redirect to a script":  {Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: EdgeAction{Kind: "redirect", Location: "javascript:alert(1)", Code: 302}},
		"a redirect with a space": {Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: EdgeAction{Kind: "redirect", Location: "/a b", Code: 302}},
		"a redirect with nginx's variable": {Name: "a", Match: EdgeMatch{PathPrefix: "/a"},
			Action: EdgeAction{Kind: "redirect", Location: "/$host", Code: 302}},
		"a redirect's own code": {Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: EdgeAction{Kind: "redirect", Location: "/b", Code: 303}},
		"an unknown action":     {Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: EdgeAction{Kind: "challenge"}},
	} {
		if err := ValidateEdgeRules([]EdgeRule{rule}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	twice := []EdgeRule{
		{Name: "a", Match: EdgeMatch{PathPrefix: "/a"}, Action: block},
		{Name: "a", Match: EdgeMatch{PathPrefix: "/b"}, Action: block},
	}
	if err := ValidateEdgeRules(twice); err == nil {
		t.Error("two rules of one name were accepted")
	}
	for name, limit := range map[string]DeploymentRateLimit{
		"no requests": {Per: "second"}, "per hour": {Requests: 1, Per: "hour"}, "a negative burst": {Requests: 1, Per: "minute", Burst: -1},
	} {
		if err := ValidateRateLimit(limit); err == nil {
			t.Errorf("rate limit %s was accepted", name)
		}
	}
}

func TestRateLimitIsSpelledForNginxAndSaysWhenToComeBack(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		limit DeploymentRateLimit
		rate  string
		retry int
	}{
		{DeploymentRateLimit{Requests: 10, Per: "second"}, "10r/s", 1},
		{DeploymentRateLimit{Requests: 60, Per: "minute"}, "60r/m", 1},
		{DeploymentRateLimit{Requests: 7, Per: "minute"}, "7r/m", 9},
		{DeploymentRateLimit{Requests: 1, Per: "minute"}, "1r/m", 60},
	} {
		if got := testCase.limit.NginxRate(); got != testCase.rate {
			t.Errorf("%+v rate = %s, want %s", testCase.limit, got, testCase.rate)
		}
		if got := testCase.limit.RetryAfter(); got != testCase.retry {
			t.Errorf("%+v Retry-After = %d, want %d", testCase.limit, got, testCase.retry)
		}
	}
}

// On nginx every condition is a map of its own, every rule a map of its
// conditions, and the decision the first rule a request meets — after the
// fence, so an address outside it is the fence's 403 as on Caddy. The
// returns come after the maintenance check and before the address list.
func TestEdgeRulesOnNginx(t *testing.T) {
	t.Parallel()
	service := New(t.TempDir(), "")
	service.SetTrustedProxies([]string{"203.0.113.0/24"})
	route := edgeRoute()
	route.CrowdSec = true
	spec := service.deploymentSpec(route)
	content := mustRender(t, spec)
	// The variables are named from the short ident once the long one would
	// pass nginx's variable length, so the test reads which one was used.
	found := regexp.MustCompile(`\$(jd_[a-z0-9_]+)_rules\b`).FindStringSubmatch(content)
	if found == nil {
		t.Fatalf("no decision variable:\n%s", content)
	}
	ident := found[1]
	for _, want := range []string{
		"geo $" + ident + "_fence {\n    default 1;\n    10.0.0.0/8 0;\n}",
		"geo $" + ident + "_crowdsec {\n    default 0;\n    include " + filepath.Join(service.nginxDir, "jd-edge", "crowdsec.conf") + ";\n}",
		"map $uri $" + ident + "_e1_prefix {\n    default 0;\n    \"~^/admin\" 1;\n}",
		"geo $" + ident + "_e1_client {\n    default 0;\n    10.1.0.0/16 1;\n}",
		"map \"$" + ident + "_e1_prefix$" + ident + "_e1_client\" $" + ident + "_e1 {\n    default 0;\n    \"11\" 1;\n}",
		"map $request_method $" + ident + "_e3_method {\n    default 0;\n    GET 1;\n    POST 1;\n}",
		"map $http_user_agent $" + ident + "_e3_agent {\n    default 0;\n    \"~*curl|python-requests\" 1;\n}",
		"map $uri $" + ident + "_e4_path {\n    default 0;\n    \"~^/blog/[0-9]+\\.html$\" 1;\n}",
		"    \"~^01\" crowdsec;\n    \"~^001\" office;\n    \"~^0001\" admin;\n    \"~^00001\" scrapers;\n    \"~^000001\" old-blog;\n}",
		"    if ($" + ident + "_rules = admin) {\n        return 403;\n    }",
		"    if ($" + ident + "_rules = scrapers) {\n        return 429;\n    }",
		"    if ($" + ident + "_rules = old-blog) {\n        return 302 https://blog.example.test/archive;\n    }",
		" er=$" + ident + "_rules'",
		"limit_req_zone $" + ident + "_req_key zone=" + ident + "_req:10m rate=5r/s;",
		"    limit_req zone=" + ident + "_req burst=10 nodelay;\n",
		"    limit_req_status 429;\n",
		"    error_page 429 = @" + ident + "_limited;\n",
		"    location @" + ident + "_limited {\n        default_type text/plain;\n        add_header Retry-After 1 always;\n        return 429 \"Too many requests. Try again in 1 second.\\n\";\n    }",
		"    set_real_ip_from 203.0.113.0/24;\n    real_ip_header X-Forwarded-For;\n    real_ip_recursive on;\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("nginx route is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "_rules = office") {
		t.Fatalf("an allow rule answers something:\n%s", content)
	}
	maintenance := strings.Index(content, "    set $"+ident+"_maint 0;")
	edge := strings.Index(content, "    # Edge rules, as the map above decides them.")
	fence := strings.Index(content, "    allow 10.0.0.0/8;\n    deny all;")
	if maintenance < 0 || edge < maintenance || fence < edge {
		t.Fatalf("edge rules are not between maintenance and the fence:\n%s", content)
	}

	// Without the rules, the route renders none of it and believes no proxy.
	plain := mustRender(t, service.deploymentSpec(DeploymentRoute{Name: route.Name, Domains: route.Domains, Upstream: route.Upstream}))
	for _, absent := range []string{"_rules", "er=", "limit_req", "set_real_ip_from", "crowdsec"} {
		if strings.Contains(plain, absent) {
			t.Fatalf("a route without edge rules carries %q:\n%s", absent, plain)
		}
	}
}

// On Caddy a rule's matcher excludes every rule before it, so a request
// meets one at most: the first. The name is set before anything answers and
// appended to the request record late, whichever handler answered.
func TestEdgeRulesOnCaddy(t *testing.T) {
	t.Parallel()
	route := edgeRoute()
	route.CrowdSec = true
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    log_append edge_rule {http.vars.jd_edge_rule}\n    import " + dockerCaddyCrowdSecFile + "\n",
		"    @jd_edge_1 {\n      path_regexp \"^/admin\"\n      remote_ip 10.1.0.0/16\n    }\n    vars @jd_edge_1 jd_edge_rule office\n    @jd_edge_2",
		"    @jd_edge_2 {\n      path_regexp \"^/admin\"\n      not {\n        path_regexp \"^/admin\"\n        remote_ip 10.1.0.0/16\n      }\n    }\n",
		"    respond @jd_edge_2 \"Forbidden\" 403\n",
		"      method GET POST\n      header_regexp User-Agent \"(?i)curl|python-requests\"\n",
		"    respond @jd_edge_3 429\n",
		"      path_regexp \"^/blog/[0-9]+\\.html$\"\n",
		"    redir @jd_edge_4 https://blog.example.test/archive 302\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Caddy route is missing %q:\n%s", want, content)
		}
	}
	if strings.Count(content, "      not {\n") != 0+1+2+3 {
		t.Fatalf("each rule does not exclude every rule before it:\n%s", content)
	}
	fence := strings.Index(content, "    respond @outside \"Forbidden\" 403\n")
	edge := strings.Index(content, "    log_append edge_rule")
	maintenance := strings.Index(content, "    @jd_maintenance {")
	proxy := strings.Index(content, "    reverse_proxy \"http://10.0.0.2:3000\"")
	if fence < 0 || edge < fence || maintenance < edge || proxy < maintenance {
		t.Fatalf("Caddy order is not fence, edge rules, maintenance, application:\n%s", content)
	}
	// The rate limit is the host's on Caddy, which has no module for it.
	if strings.Contains(content, "rate") {
		t.Fatalf("Caddy was given a rate limit it has no module for:\n%s", content)
	}
}

func TestCrowdSecListKeepsBansOnAddressesOnce(t *testing.T) {
	t.Parallel()
	banned, omitted := CrowdSecBanned([]CrowdSecBan{
		{Scope: "Ip", Value: "192.0.2.7", Type: "ban"},
		{Scope: "Range", Value: "198.51.100.9/24", Type: "ban"},
		{Scope: "Ip", Value: "192.0.2.7", Type: "ban"},
		{Scope: "Ip", Value: "192.0.2.8", Type: "captcha"},
		{Scope: "Country", Value: "XX", Type: "ban"},
		{Scope: "Ip", Value: "not-an-address", Type: "ban"},
		{Scope: "Ip", Value: "2001:db8::1", Type: "ban"},
	})
	if strings.Join(banned, " ") != "192.0.2.7 198.51.100.0/24 2001:db8::1" || omitted != 0 {
		t.Fatalf("banned = %v, omitted %d", banned, omitted)
	}
	many := make([]CrowdSecBan, MaxCrowdSecEntries+3)
	for index := range many {
		many[index] = CrowdSecBan{Scope: "Ip", Value: "10." + strconv.Itoa(index/65536) + "." + strconv.Itoa(index/256%256) + "." + strconv.Itoa(index%256), Type: "ban"}
	}
	if capped, omitted := CrowdSecBanned(many); len(capped) != MaxCrowdSecEntries || omitted != 3 {
		t.Fatalf("cap = %d kept, %d omitted", len(capped), omitted)
	}
	if file := renderCaddyCrowdSecFile(nil); strings.Contains(file, "remote_ip") {
		t.Fatalf("an empty list wrote a matcher Caddy does not load:\n%s", file)
	}
	if file := renderNginxCrowdSecFile(banned); !strings.Contains(file, "198.51.100.0/24 1;\n") {
		t.Fatalf("nginx list = %s", file)
	}
}

// The nginx list is written whole, kept when it has not changed, and only
// reloaded for when a route reads it.
func TestCrowdSecListIsRefreshedForNginxOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := New(root, "")
	first, err := service.refreshNginxCrowdSec(context.Background(), []string{"192.0.2.7"})
	if err != nil || !first.Changed || first.Reloaded {
		t.Fatalf("first refresh = %+v, %v", first, err)
	}
	again, err := service.refreshNginxCrowdSec(context.Background(), []string{"192.0.2.7"})
	if err != nil || again.Changed {
		t.Fatalf("an unchanged list was rewritten: %+v, %v", again, err)
	}
	if service.nginxReadsCrowdSec() {
		t.Fatal("a list no site includes was read as included")
	}
	if err := os.MkdirAll(filepath.Join(root, "sites-enabled"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sites-enabled", "a"), []byte("geo $x {\n    include "+service.nginxCrowdSecList()+";\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !service.nginxReadsCrowdSec() {
		t.Fatal("a site including the list was not seen")
	}
	if _, err := service.RefreshCrowdSecList(context.Background(), []string{"192.0.2.7/8"}); err == nil {
		t.Fatal("a non-canonical entry reached the list")
	}
}
