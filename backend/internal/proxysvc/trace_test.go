package proxysvc

import (
	"regexp"
	"strings"
	"testing"
)

func TestACaddyRouteSendsAndRecordsATraceparentBeforeAnythingElse(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-9.conf", Domains: []string{"shop.example.test"}, AccessLog: true, Trace: true,
		AllowFrom: []string{"203.0.113.0/24"},
	}
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.5:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"map {http.request.uuid} {jd_new_trace} {jd_span} {",
		`~^([0-9a-f]{8})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{4})-([0-9a-f]{12})$ "${1}${2}${3}${4}${5}" "${4}${5}"`,
		"vars jd_incoming {http.request.header.traceparent}",
		"map {http.vars.jd_incoming} {jd_trace} {jd_parent} {",
		`~^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$ "${1}" "${2}"`,
		`default {jd_new_trace} ""`,
		`request_header Traceparent "00-{jd_trace}-{jd_span}-01"`,
		"log_append trace_id {jd_trace}", "log_append span_id {jd_span}", "log_append parent_span_id {jd_parent}",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the route does not carry %q:\n%s", want, content)
		}
	}
	// First in the handling: a request the fence refuses is recorded with
	// its trace too, and the header is set before reverse_proxy reads it.
	if strings.Index(content, "request_header Traceparent") > strings.Index(content, "@outside not remote_ip") ||
		strings.Index(content, "request_header Traceparent") > strings.Index(content, "reverse_proxy") {
		t.Fatalf("the trace is not first in the handling:\n%s", content)
	}

	// A route that serves nothing yet, and the nameless route a certificate
	// is issued through, have no application to tell.
	for _, quiet := range []struct {
		route    DeploymentRoute
		upstream string
	}{
		{route, ""},
		{DeploymentRoute{Domains: []string{"shop.example.test"}, Trace: true}, "http://10.0.0.5:3000"},
	} {
		content, err := renderDockerCaddyRoute(quiet.route, quiet.upstream)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(content, "Traceparent") {
			t.Fatalf("a route without an application sends a trace:\n%s", content)
		}
	}
	route.Trace = false
	if untraced, _ := renderDockerCaddyRoute(route, "http://10.0.0.5:3000"); strings.Contains(untraced, "jd_trace") {
		t.Fatalf("an untraced route carries the trace:\n%s", untraced)
	}
}

func TestOnlyACaddyWithLogAppendIsAskedToRecordATrace(t *testing.T) {
	for version, want := range map[string]bool{
		"v2.11.7 h1:yj0Y4fYZGPkSvibBJ1sTWE33xC0fxztVyXEW5iIdUT4=": true,
		"v2.8.0 h1:x":  true,
		"v2.7.6 h1:w":  false,
		"2.10.2":       true,
		"v3.0.0":       true,
		"(devel)":      false,
		"":             false,
		"caddy v2.9.1": false,
	} {
		if got := caddyLogAppend(version); got != want {
			t.Errorf("%q = %v, want %v", version, got, want)
		}
	}
}

func TestAnNginxRouteSendsAndRecordsATraceparentAndReadsBackAsTraced(t *testing.T) {
	route := DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: []string{"shop.example.test"}, Upstream: "http://127.0.0.1:3000", AccessLog: true, Trace: true}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	// The variables are named from the site's short ident when the long one
	// would be too long for nginx's hash, as this one is.
	named := regexp.MustCompile(`map \$http_traceparent \$(jd_\w+)_trace_id \{`).FindStringSubmatch(content)
	if named == nil {
		t.Fatalf("the route has no trace map:\n%s", content)
	}
	ident := named[1]
	for _, want := range []string{
		"map $http_traceparent $" + ident + "_trace_id {",
		`"~^00-(?<jd_tp_id>[0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$" $jd_tp_id;`,
		"default $request_id;",
		"map $http_traceparent $" + ident + "_trace_parent {",
		"map $request_id $" + ident + "_trace_span {",
		`proxy_set_header traceparent       "00-$` + ident + `_trace_id-$` + ident + `_trace_span-01";`,
		" trace=$" + ident + "_trace_id span=$" + ident + "_trace_span parent=$" + ident + "_trace_parent'",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the route does not carry %q:\n%s", want, content)
		}
	}
	back, ok := ParseSiteSpec(route.Name, content)
	if !ok || !back.Trace {
		t.Fatalf("the route read back as untraced: %+v", back)
	}
	route.Trace = false
	if untraced := mustRender(t, deploymentSiteSpec(route, "")); strings.Contains(untraced, "traceparent") || strings.Contains(untraced, "trace=") {
		t.Fatalf("an untraced route carries the trace:\n%s", untraced)
	}
}
