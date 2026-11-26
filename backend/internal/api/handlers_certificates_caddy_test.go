package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

const caddyStorage = "/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory"

// oneIngress answers the ingress lookup with one public Caddy container.
type oneIngress struct{}

func (oneIngress) InspectRunning(context.Context) ([]byte, error) {
	return []byte(`[{"ID":"4f1c0ffee","Name":"/edge","State":{"Running":true},
"Config":{"Cmd":["caddy","run","--config","/etc/caddy/Caddyfile","--adapter","caddyfile"]},
"Mounts":[{"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/caddy/Caddyfile","RW":false},
{"Type":"volume","Source":"/var/lib/docker/volumes/edge-config/_data","Destination":"/config","RW":true},
{"Type":"volume","Source":"/var/lib/docker/volumes/edge-data/_data","Destination":"/data","RW":true}],
"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]},"Networks":{}}}]`), nil
}

func (oneIngress) InspectOne(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("no such container")
}

// caddyIngressServer is a server whose proxy is a Docker Caddy ingress kept
// in a temporary directory: a docker on PATH runs each exec's fixed script
// over it, answers Caddy's admin API with the names routed, and `caddy
// adapt` with the name a route's block opens on. held is the names Caddy
// keeps a certificate for, and routed maps a route's name to the name it
// serves.
func caddyIngressServer(t *testing.T, held []string, routed map[string]string) (*client, *Server, string) {
	t.Helper()
	// Its own certbot and import directories, not this machine's.
	useFakeCertbot(t)
	s := testServer(t)
	root, bin := t.TempDir(), t.TempDir()
	script := `#!/bin/sh
[ "$1" = exec ] || exit 1
shift 3
case "$1" in
wget) cat "` + root + `/admin.json"; exit $? ;;
caddy) printf '{"host":["%s"]}' "$(sed -n 's/^\([a-z0-9*.-]*\) {$/\1/p' | head -n 1)"; exit 0 ;;
esac
[ "$1" = sh ] && [ "$2" = -c ] || exit 1
body=$3
shift 4
n=$#
while [ "$n" -gt 0 ]; do
	arg=$1
	shift
	case "$arg" in /config/*|/data/*) arg="` + root + `$arg" ;; esac
	set -- "$@" "$arg"
	n=$((n-1))
done
sh -c "$body" sh "$@" | sed "s#` + root + `/#/#g"
`
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bin, "docker"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range held {
		certPEM, _ := testCertificateUntil(t, []string{name}, time.Now().Add(60*24*time.Hour))
		write(filepath.Join(root, caddyStorage, name, name+".crt"), certPEM)
	}
	hosts := []string{}
	if err := os.MkdirAll(filepath.Join(root, "config", "just-dashboard", "routes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for route, name := range routed {
		write(filepath.Join(root, "config", "just-dashboard", "routes", route+".caddy"),
			"# Managed by Just Dashboard\n"+name+" {\n  respond 204\n}\n")
		hosts = append(hosts, `"`+name+`"`)
	}
	write(filepath.Join(root, "admin.json"), `{"host":[`+strings.Join(hosts, ",")+`]}`)
	s.modules.proxy = proxysvc.NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile")).WithContainerReader(oneIngress{})
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, s, root
}

// Removing a certificate from Caddy's storage is an administrator's,
// destructive, with a plain confirmation; what it removed is in the audit
// trail, and a refusal removes nothing.
func TestCaddyCertificateDeleteIsAdminOnlyDestructiveAndAudited(t *testing.T) {
	c, s, root := caddyIngressServer(t, []string{"old.example.com"}, nil)
	gates := routeGates(t, c.h)
	if got := gates["DELETE /api/v1/certificates/caddy"]; got != [2]int{2, 2} {
		t.Errorf("gates = %v, want two capability checks and two budgets", got)
	}
	path := caddyStorage + "/old.example.com/old.example.com.crt"
	body := `{"path":"` + path + `"}`
	onDisk := filepath.Join(root, path)

	for role, name := range map[auth.Role]string{auth.RoleReadOnly: "reader", auth.RoleLimited: "limited"} {
		other := &client{t: t, h: c.h, cookie: signInAs(t, s, name, role)}
		if w := other.do(http.MethodDelete, "/api/v1/certificates/caddy", body, nil); w.Code != http.StatusForbidden {
			t.Errorf("a %s account got %d", name, w.Code)
		}
	}
	if w := c.do(http.MethodDelete, "/api/v1/certificates/caddy", `{"path":"/data/caddy/certificates/../../etc/passwd.crt"}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("a path outside Caddy's certificates: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, "/api/v1/certificates/caddy", `{"path":"`+caddyStorage+`/gone.example.com/gone.example.com.crt"}`, nil); w.Code != http.StatusNotFound {
		t.Errorf("a certificate Caddy does not hold: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("a refused delete removed the certificate: %v", err)
	}

	w := c.do(http.MethodDelete, "/api/v1/certificates/caddy", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("an unused certificate: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(onDisk)); !os.IsNotExist(err) {
		t.Errorf("the certificate's directory is still there: %v", err)
	}
	if entry := lastAudit(t, s, "certificates.caddy.delete"); !entry.Success || entry.Target != "old.example.com" {
		t.Errorf("audit = %+v", entry)
	}
}

// A certificate a deployment's route serves is refused in the operator's
// words: the project, not the route's file name.
func TestCaddyCertificateDeleteNamesTheProjectThatServesIt(t *testing.T) {
	c, s, root := caddyIngressServer(t, []string{"shop.example.com", "left.example.com"}, nil)
	_, environmentID := seedDeployment(t, s, "shop")
	routes := filepath.Join(root, "config", "just-dashboard", "routes")
	for route, name := range map[string]string{
		fmt.Sprintf("just-dashboard-env-%d.conf", environmentID): "shop.example.com",
		"just-dashboard-env-999999.conf":                         "left.example.com",
	} {
		if err := os.WriteFile(filepath.Join(routes, route+".caddy"), []byte("# Managed by Just Dashboard\n"+name+" {\n  respond 204\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "admin.json"), []byte(`{"host":["shop.example.com","left.example.com"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"shop.example.com": "shop · production",
		"left.example.com": "the leftover route just-dashboard-env-999999.conf",
	} {
		path := caddyStorage + "/" + name + "/" + name + ".crt"
		w := c.do(http.MethodDelete, "/api/v1/certificates/caddy", `{"path":"`+path+`"}`, nil)
		var refusal struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &refusal) != nil ||
			refusal.Error.Code != "in_use" || !strings.Contains(refusal.Error.Message, "is served by "+want+".") {
			t.Errorf("%s: %d %s, want in_use naming %q", name, w.Code, w.Body.String(), want)
		}
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("%s was removed while a route serves it: %v", name, err)
		}
	}

	// The list says the same, with where each opens.
	w := c.do(http.MethodGet, "/api/v1/certificates/", "", nil)
	var listed []proxysvc.Certificate
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed) != 2 {
		t.Fatalf("list = %d: %s", w.Code, w.Body.String())
	}
	for _, cert := range listed {
		if len(cert.UsedByLabels) != 1 || cert.UsedByLabels[0].Site != cert.UsedBy[0] {
			t.Fatalf("%s: %+v for %v", cert.Name, cert.UsedByLabels, cert.UsedBy)
		}
		user := cert.UsedByLabels[0]
		switch cert.Name {
		case "shop.example.com":
			if user.Label != "shop · production" || !strings.HasPrefix(user.Href, "/deploy/") || user.Leftover {
				t.Errorf("a deployment's route reads %+v", user)
			}
		case "left.example.com":
			if user.Label != "leftover route" || user.Href != "/proxy/sites/just-dashboard-env-999999.conf" || !user.Leftover {
				t.Errorf("a leftover route reads %+v", user)
			}
		}
	}
}

// A site that is nobody's route keeps its name, and a host of the ingress's
// own Caddyfile is that host.
func TestCertificateUserKeepsAPlainSitesName(t *testing.T) {
	for name, want := range map[string]string{
		"blog.example.com":                  "blog.example.com",
		"docker-caddy:edge:www.example.com": "www.example.com",
		"docker-caddy:edge:":                "docker-caddy:edge:",
	} {
		user := certificateUser(proxysvc.VHost{Name: name})
		if user.Label != want || user.Leftover || !strings.HasPrefix(user.Href, "/proxy/sites/") {
			t.Errorf("%s reads %+v, want %q", name, user, want)
		}
	}
}
