package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestNginxIdent(t *testing.T) {
	// Three site names that fold to the same characters must still name
	// three zones: a duplicate zone name is an emergency at the next reload.
	seen := map[string]string{}
	for _, name := range []string{"a-b", "a_b", "a.b"} {
		ident := NginxIdent(name)
		if other, ok := seen[ident]; ok {
			t.Fatalf("%q and %q share %q", name, other, ident)
		}
		seen[ident] = name
	}

	valid := regexp.MustCompile(`^jd_[a-z0-9_]+$`)
	for name, want := range map[string]string{
		// Nothing folded: the name reads as itself.
		"app":     "jd_app",
		"app2024": "jd_app2024",
		// Pinned so a stored zone name never changes under a site.
		"a-b":             "jd_a_b_2a89df",
		"app.example.com": "jd_app_example_com_dd46c9",
		"App":             "jd__pp_81f51a",
	} {
		got := NginxIdent(name)
		if got != want {
			t.Errorf("NginxIdent(%q) = %q, want %q", name, got, want)
		}
		if !valid.MatchString(got) {
			t.Errorf("NginxIdent(%q) = %q is not a name nginx takes everywhere", name, got)
		}
	}
}

// longSiteName is as long as a site name may be.
var longSiteName = "customer-portal." + strings.Repeat("x", 48)

// everyVariableSite turns on each part of the form that names an nginx
// variable, so the variables a site can own are all in one file.
func everyVariableSite(name string) *SiteSpec {
	spec := plainSpec(name, "vars.example.test")
	spec.BlockBots = &SiteBotBlock{AI: true}
	spec.Hotlink = &SiteHotlink{}
	spec.StaticCache = &StaticCache{MaxAge: "30d", Immutable: true}
	spec.Maintenance = &SiteMaintenance{On: true, BypassFrom: []string{"192.0.2.0/24"}}
	spec.RealIP = &SiteRealIP{Source: "cloudflare", CloudflareOnly: true}
	spec.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://127.0.0.1:9091/api/verify", SignIn: "https://auth.example.test"}
	spec.Limits = &SiteLimits{Request: &RequestLimit{Rate: "10r/s", Burst: 20, NoDelay: true}, ConnPerIP: 8, ExemptFrom: []string{"10.0.0.0/8"}}
	spec.Headers = &SiteHeaders{CORS: &SiteCORS{Origins: []string{"https://app.example.test"}, Methods: []string{"GET", "POST"}}}
	spec.Locations = []SiteLocation{{Path: "/api/", Upstream: "http://127.0.0.1:3001", RateLimit: &RequestLimit{Rate: "5r/s"}}}
	return spec
}

var jdVarRe = regexp.MustCompile(`\$(jd_[a-z0-9_]+)`)

// jdVars are the dashboard's own variables a rendered file mentions.
func jdVars(content string) []string {
	seen := map[string]bool{}
	for _, m := range jdVarRe.FindAllStringSubmatch(content, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// One variable name longer than nginx's default hash takes fails nginx -t
// for the whole configuration, so every name the dashboard writes has to
// fit: a deployment route of any environment, a site or stream with the
// longest name allowed, and every part of the form that names a variable.
func TestEveryVariableFitsNginxsDefaultHash(t *testing.T) {
	rendered := map[string]string{}
	for _, id := range []string{"9", "10", "4242424242"} {
		name := "just-dashboard-env-" + id + ".conf"
		rendered[name] = render(t, deploymentSiteSpec(DeploymentRoute{Name: name, Domains: []string{"app.example.test"}, Upstream: "http://127.0.0.1:3000"}, ""))
	}
	site := render(t, everyVariableSite(longSiteName))
	rendered[longSiteName] = site
	stream, err := RenderStream(&StreamSpec{Name: longSiteName, Listen: 8443, Protocol: "tcp", Upstream: "10.0.0.5:443", AllowFrom: []string{},
		Routes: []StreamRoute{{Name: "a.example.test", Upstream: "10.0.0.6:443"}}})
	if err != nil {
		t.Fatal(err)
	}
	rendered["stream "+longSiteName] = stream

	for name, content := range rendered {
		vars := jdVars(content)
		if len(vars) == 0 {
			t.Fatalf("%s names no variables, so it tests nothing", name)
		}
		for _, v := range vars {
			if len(v) > maxNginxVarLen {
				t.Errorf("%s: $%s is %d characters; nginx's default variables_hash_bucket_size takes %d", name, v, len(v), maxNginxVarLen)
			}
		}
	}
	// The fixture has to reach every variable the form can name, or a new
	// one with a longer suffix would pass unseen.
	for _, suffix := range []string{"_bot", "_hotlink_type", "_hotlink", "_edge", "_asset_expires", "_asset_immutable", "_sso_user",
		"_sso_email", "_maint", "_maint_ip", "_connection", "_cors", "_preflight", "_limit_exempt", "_req_key", "_conn_key", "_req_p1_key"} {
		if !strings.Contains(site, suffix+" ") && !strings.Contains(site, suffix+";") && !strings.Contains(site, suffix+")") {
			t.Errorf("the fixture names no variable ending %s", suffix)
		}
	}
}

// A file whose variables already fit keeps every name it has, so nothing an
// install has written changes under it and no site reads as edited by hand;
// a file that has to shorten them shortens all of them alike, because the
// readers find a variable's partner by swapping the suffix.
func TestVariableNamesChangeOnlyWhereTheyHaveTo(t *testing.T) {
	route := render(t, deploymentSiteSpec(DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: []string{"a.example.test"}, Upstream: "http://127.0.0.1:3000"}, ""))
	if want := "$" + NginxIdent("just-dashboard-env-9.conf") + "_connection"; !strings.Contains(route, want) {
		t.Fatalf("environment 9's route no longer names %s:\n%s", want, route)
	}
	short := everyVariableSite("app.example.com")
	for _, v := range jdVars(render(t, short)) {
		if !strings.HasPrefix(v, NginxIdent("app.example.com")+"_") {
			t.Errorf("$%s: a short site's variable was renamed", v)
		}
	}

	long := everyVariableSite(longSiteName)
	ident := NginxVarIdent(longSiteName)
	if len(ident) == len(NginxIdent(longSiteName)) {
		t.Fatalf("%q kept its whole ident", longSiteName)
	}
	for _, v := range jdVars(render(t, long)) {
		if !strings.HasPrefix(v, ident+"_") {
			t.Errorf("$%s does not share the site's ident %s", v, ident)
		}
	}
	// Two long names that share their start are still two sets of
	// variables and zones.
	other := longSiteName[:len(longSiteName)-1] + "y"
	if NginxVarIdent(other) == ident {
		t.Fatalf("%q and %q share %s", longSiteName, other, ident)
	}
}

// What the form writes for a long-named site is read back as the same form:
// its own lines are recognised under the shortened names, so saving the form
// again writes the same file and nothing reads as edited by hand.
func TestLongNamedSiteReadsBackUnderItsShortenedNames(t *testing.T) {
	spec := everyVariableSite(longSiteName)
	content := render(t, spec)
	parsed, managed := ParseSiteSpec(longSiteName, content)
	if !managed {
		t.Fatalf("the form's own file is not read as the form's:\n%s", content)
	}
	if again := render(t, parsed); again != content {
		t.Fatalf("read back and written again, the file changed:\n--- written\n%s\n--- again\n%s", content, again)
	}
	stream := &StreamSpec{Name: longSiteName, Listen: 8443, Protocol: "tcp", Upstream: "10.0.0.5:443", AllowFrom: []string{},
		Routes: []StreamRoute{{Name: "a.example.test", Upstream: "10.0.0.6:443"}}}
	written, err := RenderStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	read, ok, _ := ParseStreamSpec(longSiteName, written)
	if !ok || len(read.Routes) != 1 || read.Routes[0].Name != "a.example.test" {
		t.Fatalf("the stream's routes were not read back: %+v\n%s", read, written)
	}
}

// Against the real nginx: a deployment route for environment 10 and a site
// with the longest name and every variable both pass nginx -t under nginx's
// default hash sizes.
func TestLiveNginxTakesTheVariablesOfLongNames(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	if err := os.MkdirAll(svc.RealIPDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(svc.RealIPDir(), cloudflareGeoFile), "173.245.48.0/20 1;\n")
	writeFile(t, filepath.Join(svc.RealIPDir(), cloudflareConfFile), "set_real_ip_from 173.245.48.0/20;\n")
	specs := []*SiteSpec{everyVariableSite(longSiteName)}
	for _, id := range []string{"10", "4242424242"} {
		name := "just-dashboard-env-" + id + ".conf"
		specs = append(specs, deploymentSiteSpec(DeploymentRoute{Name: name, Domains: []string{"env" + id + ".example.test"}, Upstream: "http://127.0.0.1:3000"}, ""))
	}
	port, tlsPort := freePort(t), freePort(t)
	for _, spec := range specs {
		svc.SetRealIPDir(spec)
		svc.SetPagesDir(spec)
		installPreset(t, root, spec, port, tlsPort)
	}
	res, err := svc.Test(context.Background(), KindNginx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("nginx -t refused the files:\n%s", res.Output)
	}
}
