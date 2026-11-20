package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostname"
)

// namedServer stands in for the host, so no test renames the machine it
// runs on.
type namedServer struct {
	name    string
	tailnet string
	err     error
	asked   []string
}

func (n *namedServer) Current(context.Context) (*hostname.State, error) {
	return &hostname.State{Hostname: n.name, TailscaleName: n.tailnet}, nil
}

func (n *namedServer) Rename(_ context.Context, name string) (*hostname.Result, error) {
	n.asked = append(n.asked, name)
	if n.err != nil {
		return nil, n.err
	}
	result := &hostname.Result{Hostname: name, Previous: n.name, TailscaleName: n.tailnet}
	n.name = name
	return result, nil
}

func renameClient(t *testing.T, role auth.Role) (*client, *Server, *namedServer) {
	t.Helper()
	s := testServer(t)
	host := &namedServer{name: "vps-1", tailnet: "vps-1"}
	s.modules.hostname = host
	return &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "renamer", role)}, s, host
}

func TestHostnameRenamesAndRecordsWhoDidIt(t *testing.T) {
	c, s, host := renameClient(t, auth.RoleAdmin)

	w := c.do(http.MethodGet, "/api/v1/system/hostname/", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hostname":"vps-1"`) || !strings.Contains(w.Body.String(), `"tailscaleName":"vps-1"`) {
		t.Fatalf("read answered %d %s", w.Code, w.Body)
	}

	w = c.do(http.MethodPut, "/api/v1/system/hostname/", `{"hostname":" Atlas "}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("rename answered %d %s", w.Code, w.Body)
	}
	var result hostname.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Hostname != "atlas" || result.Previous != "vps-1" || result.TailscaleName != "vps-1" {
		t.Fatalf("result = %+v", result)
	}
	if len(host.asked) != 1 || host.asked[0] != "atlas" {
		t.Fatalf("the host was asked for %v", host.asked)
	}

	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "system.hostname.set"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Target != "atlas" || !strings.Contains(entries[0].Detail, "vps-1") {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestHostnameRefusesANameTheServerCannotTake(t *testing.T) {
	c, _, host := renameClient(t, auth.RoleAdmin)
	for _, body := range []string{`{}`, `{"hostname":""}`, `{"hostname":"my server"}`, `{"hostname":"localhost"}`, `{"hostname":"-a"}`} {
		w := c.do(http.MethodPut, "/api/v1/system/hostname/", body, nil)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_hostname") {
			t.Errorf("%s answered %d %s", body, w.Code, w.Body)
		}
	}
	if len(host.asked) != 0 {
		t.Fatalf("the host was asked for %v", host.asked)
	}
}

func TestHostnameSaysWhatTheHostSaid(t *testing.T) {
	c, _, host := renameClient(t, auth.RoleAdmin)
	host.err = errors.New("hostnamectl: Could not set static hostname: Access denied")
	w := c.do(http.MethodPut, "/api/v1/system/hostname/", `{"hostname":"atlas"}`, nil)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "hostname_failed") || !strings.Contains(w.Body.String(), "Access denied") {
		t.Fatalf("answered %d %s", w.Code, w.Body)
	}
}

// Renaming the server is the operator's. Neither other role reads the route
// or changes anything through it.
func TestHostnameIsTheOperators(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		c, _, host := renameClient(t, role)
		if w := c.do(http.MethodGet, "/api/v1/system/hostname/", "", nil); w.Code != http.StatusForbidden {
			t.Errorf("%s read it: %d", role, w.Code)
		}
		if w := c.do(http.MethodPut, "/api/v1/system/hostname/", `{"hostname":"atlas"}`, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s renamed the server: %d %s", role, w.Code, w.Body)
		}
		if len(host.asked) != 0 {
			t.Errorf("%s reached the host: %v", role, host.asked)
		}
	}
}
