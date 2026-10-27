package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The rehearsal's terms are read by anyone who may read the environment and
// saved by an administrator alone, each save audited with what it set.
func TestRehearsalRouteIsAdministrativeAndAudited(t *testing.T) {
	s := testServer(t)
	routes := s.Routes()
	admin := &client{t: t, h: routes, cookie: signInAs(t, s, "rehearsal-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: routes, cookie: signInAs(t, s, "rehearsal-operator", auth.RoleLimited)}
	reader := &client{t: t, h: routes, cookie: signInAs(t, s, "rehearsal-reader", auth.RoleReadOnly)}
	projectID, environmentID, _ := insertDeploymentConfigurationAPI(t, s)
	path := fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rehearsal", projectID, environmentID)

	read := reader.do(http.MethodGet, path, "", nil)
	var policy deploy.RehearsalPolicy
	if err := json.Unmarshal(read.Body.Bytes(), &policy); err != nil || read.Code != http.StatusOK || policy.Enabled || policy.WhenUnavailable != "block" {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	body := `{"enabled":true,"connectionId":7,"whenUnavailable":"warn","restoreTimeoutSeconds":300,"maxBackupMb":512}`
	for _, who := range []*client{reader, limited} {
		if res := who.do(http.MethodPut, path, body, nil); res.Code != http.StatusForbidden {
			t.Fatalf("a save without system.admin = %d %s", res.Code, res.Body.String())
		}
	}
	if res := admin.do(http.MethodPut, path, `{"enabled":true,"whenUnavailable":"ignore","restoreTimeoutSeconds":300,"maxBackupMb":512}`, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown answer = %d %s", res.Code, res.Body.String())
	}
	res := admin.do(http.MethodPut, path, body, nil)
	if err := json.Unmarshal(res.Body.Bytes(), &policy); err != nil || res.Code != http.StatusOK || !policy.Enabled || policy.ConnectionID != 7 || policy.UpdatedBy != "rehearsal-admin" {
		t.Fatalf("save = %d %s", res.Code, res.Body.String())
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'deploy.rehearsal.update' AND success = 1`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"enabled":true`) || !strings.Contains(detail, `"whenUnavailable":"warn"`) {
		t.Fatalf("audit = %q, %v", detail, err)
	}
	if res := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/deploy/%d/environments/%d/rehearsal", projectID, environmentID+99), "", nil); res.Code != http.StatusNotFound {
		t.Fatalf("another environment = %d", res.Code)
	}
}

// The copy is the production server's container with nothing of
// production's left on it: none of its ports, labels or networks, its data on
// a fresh volume, none of the writable places production writes to, and no
// archiving into production's point-in-time archive.
func TestRehearsalCopyKeepsNothingOfProduction(t *testing.T) {
	server := &dockerx.ContainerSpec{
		Name: "orders-db", Image: "postgres:17-alpine", Hostname: "orders-db",
		Env:      []dockerx.EnvVar{{Name: "POSTGRES_USER", Value: "jd"}, {Name: "POSTGRES_PASSWORD", Value: "secret"}},
		Command:  []string{"postgres", "-c", "archive_mode=on", "-c", "archive_command=" + dbx.WALGCommand + " wal-push %p", "-c", "max_connections=200"},
		Ports:    []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"}},
		Networks: []string{"jd-e4-db"}, Labels: []dockerx.LabelSpec{{Name: "io.just-dashboard.database", Value: "4"}},
		Mounts: []dockerx.MountSpec{
			{Type: "volume", Source: "orders-db-data", Target: dbx.PostgresDataDir},
			{Type: "bind", Source: "/etc/orders/postgresql.conf", Target: "/etc/postgresql/postgresql.conf", ReadOnly: true},
			{Type: "bind", Source: "/run/orders", Target: "/var/run/postgresql"},
			{Type: "volume", Source: "jd-pitr-tool", Target: dbx.WALGMount, ReadOnly: true},
			{Type: "bind", Source: "/srv/archive", Target: dbx.WALGArchiveMount},
			{Type: "tmpfs", Target: "/dev/shm"},
		},
		RestartPolicy: "unless-stopped",
	}
	engine := rehearsalEngine{name: "postgres", dataDir: dbx.PostgresDataDir, user: "jd"}
	labels := map[string]string{"io.just-dashboard.managed": "true", rehearsalLabel: "12"}
	spec := rehearsalCopySpec(server, engine, "jd-e4-run12-rehearsal-db", "jd-e4-run12-rehearsal", "jd-e4-run12-rehearsal-db-data",
		[]string{"db-4.jd.internal", "db-4-pool.jd.internal"}, labels)

	if len(spec.Ports) != 0 || spec.Hostname != "" || spec.RestartPolicy != "no" {
		t.Fatalf("ports %+v, hostname %q, restart %q", spec.Ports, spec.Hostname, spec.RestartPolicy)
	}
	if len(spec.Networks) != 1 || spec.Networks[0] != "jd-e4-run12-rehearsal" || strings.Join(spec.NetworkAliases, ",") != "db-4.jd.internal,db-4-pool.jd.internal" {
		t.Fatalf("networks %q aliases %q", spec.Networks, spec.NetworkAliases)
	}
	for _, label := range spec.Labels {
		if label.Name == "io.just-dashboard.database" {
			t.Fatalf("the copy carries production's label %+v", label)
		}
	}
	targets := []string{}
	for _, mount := range spec.Mounts {
		targets = append(targets, mount.Target)
		if mount.Target == dbx.PostgresDataDir && mount.Source != "jd-e4-run12-rehearsal-db-data" {
			t.Fatalf("the copy's data is on %q", mount.Source)
		}
	}
	if got := strings.Join(targets, ","); got != dbx.PostgresDataDir+",/etc/postgresql/postgresql.conf,/dev/shm" {
		t.Fatalf("the copy mounts %s", got)
	}
	settings := dbx.ReadPostgresCommandSettings(spec.Command)
	if settings["archive_mode"] != "off" || settings["archive_command"] != "" || settings["max_connections"] != "200" {
		t.Fatalf("the copy's command = %q", spec.Command)
	}
	if server.Ports == nil || len(server.Mounts) != 6 || server.Command[2] != "archive_mode=on" {
		t.Fatal("making the copy changed the production server's spec")
	}
}
