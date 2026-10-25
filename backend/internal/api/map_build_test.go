package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

var mapNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func mapGot[T any](v T) mapRead[T] { return mapRead[T]{Value: v, Have: true, At: mapNow} }

func kindOf(t *testing.T, m mapResponse, key string) mapKind {
	t.Helper()
	for _, k := range m.Kinds {
		if k.Key == key {
			return k
		}
	}
	t.Fatalf("no %s kind in %v", key, m.Kinds)
	return mapKind{}
}

func itemOf(t *testing.T, m mapResponse, key, id string) mapItem {
	t.Helper()
	for _, item := range kindOf(t, m, key).Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("no %s in %s", id, key)
	return mapItem{}
}

func hasLink(m mapResponse, from, to, via string) bool {
	for _, l := range m.Links {
		if l.From == from && l.To == to && l.Via == via {
			return true
		}
	}
	return false
}

func exited(code int) *int { return &code }

// A small server: one project with a domain, its container, a stack, a
// volume, a database it is bound to, a certificate and a nightly backup.
func sampleInputs() mapInputs {
	return mapInputs{
		now:    mapNow,
		absent: map[string]bool{},
		projects: mapGot([]deploy.DeploymentSummary{{
			ID: 7, Name: "shop", EnvironmentID: 70, LiveReleaseID: 3, Endpoint: "https://shop.example.com",
		}}),
		containers: mapGot([]dockerx.Container{
			{
				ID: "c-web", Name: "shop-web", Image: "node:22", State: "running", ImageID: "sha256:img",
				Labels:   map[string]string{"io.just-dashboard.environment-id": "70"},
				Mounts:   []dockerx.MountPoint{{Type: "volume", Name: "shop-data"}},
				Networks: []string{"shop-net", "bridge"},
			},
			{
				ID: "c-db", Name: "pg", Image: "postgres:17", State: "running", ComposeStack: "data",
				Ports: []dockerx.Port{{IP: "127.0.0.1", PrivatePort: 5432, PublicPort: 5432}},
			},
			{ID: "c-old", Name: "old", Image: "busybox", State: "exited", Status: "Exited (1) 2 hours ago", ComposeStack: "data"},
		}),
		volumes:  mapGot([]dockerx.Volume{{Name: "shop-data", Size: 2048}, {Name: "orphan"}}),
		networks: mapGot([]dockerx.Network{{ID: "n1", Name: "shop-net", Containers: 1}, {ID: "n0", Name: "host"}}),
		images:   mapGot([]dockerx.Image{{ID: "sha256:img", RepoTags: []string{"node:22"}, Containers: 1}}),
		databases: mapGot([]fleetEntry{{
			dbConnection: &dbConnection{ID: 4, Name: "main", Driver: "postgres", Database: "shop"},
			State:        dbStateRunning, Container: "pg",
		}}),
		bindings: mapGot([]topoBinding{{connID: 4, projectID: 7}}),
		vhosts: mapGot([]proxysvc.VHost{
			{Name: "shop.conf", Enabled: true, ServerNames: []string{"shop.example.com"}, Owner: &proxysvc.VHostOwner{ProjectID: 7, Project: "shop"}},
			{Name: "shop-tls.conf", Enabled: true, TLS: true, ServerNames: []string{"shop.example.com", "_"}},
			{Name: "db.conf", Enabled: true, ServerNames: []string{"db.example.com"}, Upstreams: []string{"http://127.0.0.1:5432"}},
		}),
		certs: mapGot([]proxysvc.Certificate{{Name: "shop", Path: "/etc/le/shop", DaysLeft: 12, Expiring: true, UsedBy: []string{"shop-tls.conf"}}}),
		backups: mapGot([]*backups.Job{{
			ID: 2, Name: "nightly", Enabled: true, DatabaseDumps: []int64{4},
			LastRun: &backups.Run{Status: backups.StatusFailed, StartedAt: mapNow.Add(-time.Hour)},
		}}),
	}
}

func TestMapJoinsWhatTheServerKnowsIsJoined(t *testing.T) {
	m := buildServerMap(sampleInputs())
	for _, want := range []struct{ from, to, via string }{
		{"project:7", "container:c-web", "release"},
		{"stack:data", "container:c-db", "member"},
		{"container:c-web", "volume:shop-data", "mount"},
		{"container:c-web", "network:n1", "network"},
		{"container:c-web", "image:sha256:img", "image"},
		{"container:c-db", "db:4", "serves"},
		{"project:7", "db:4", "binding"},
		{"domain:shop.example.com", "project:7", "route"},
		{"domain:shop.example.com", "cert:/etc/le/shop", "certificate"},
		{"domain:db.example.com", "container:c-db", "route"},
		{"db:4", "backup:2", "backup"},
	} {
		if !hasLink(m, want.from, want.to, want.via) {
			t.Errorf("missing %s -%s-> %s in %v", want.from, want.via, want.to, m.Links)
		}
	}
	// The default bridge and the host network are everybody's: no line.
	for _, l := range m.Links {
		if strings.Contains(l.To, "bridge") || l.To == "network:n0" {
			t.Errorf("drew a line to a system network: %v", l)
		}
	}
	if len(kindOf(t, m, "networks").Items) != 1 {
		t.Errorf("the host network is not a box: %v", kindOf(t, m, "networks").Items)
	}
}

func TestMapSaysWhatWasMeasured(t *testing.T) {
	m := buildServerMap(sampleInputs())
	project := itemOf(t, m, "projects", "project:7")
	if project.State != "ready" || project.Tone != mapOK || project.Detail != "shop.example.com" ||
		project.Icon != "/api/v1/deploy/7/favicon" {
		t.Errorf("project = %+v", project)
	}
	if c := itemOf(t, m, "containers", "container:c-old"); c.State != "exited (1)" || c.Tone != mapDanger {
		t.Errorf("a container that exited 1 on its own = %+v", c)
	}
	if s := itemOf(t, m, "stacks", "stack:data"); s.State != "1 of 2 running" || s.Tone != mapWarning {
		t.Errorf("stack = %+v", s)
	}
	if v := itemOf(t, m, "volumes", "volume:shop-data"); v.State != "in use" {
		t.Errorf("a mounted volume = %+v", v)
	}
	if v := itemOf(t, m, "volumes", "volume:orphan"); v.State != "unused" || v.Tone != mapIdle {
		t.Errorf("an unmounted volume = %+v", v)
	}
	if d := itemOf(t, m, "databases", "db:4"); d.State != "answers" || d.Tone != mapOK {
		t.Errorf("database = %+v", d)
	}
	if c := itemOf(t, m, "certificates", "cert:/etc/le/shop"); c.State != "12 days left" || c.Tone != mapWarning {
		t.Errorf("certificate = %+v", c)
	}
	if b := itemOf(t, m, "backups", "backup:2"); b.State != "last run failed" || b.Tone != mapDanger {
		t.Errorf("backup = %+v", b)
	}
	// The HTTP server and the TLS server both name it; the TLS one is drawn.
	domains := kindOf(t, m, "domains")
	if d := itemOf(t, m, "domains", "domain:shop.example.com"); d.State != "https" {
		t.Errorf("a name two sites serve = %+v", d)
	}
	for _, item := range domains.Items {
		if item.Name == "_" {
			t.Error("nginx's catch-all is not a domain")
		}
	}
}

func TestMapContainerStatesFollowTheExitCode(t *testing.T) {
	for _, tc := range []struct {
		c     dockerx.Container
		state string
		tone  mapTone
	}{
		{dockerx.Container{State: "running", Health: "unhealthy"}, "unhealthy", mapDanger},
		{dockerx.Container{State: "running", Health: "starting"}, "starting", mapWarning},
		{dockerx.Container{State: "running"}, "running", mapOK},
		{dockerx.Container{State: "restarting"}, "restarting", mapWarning},
		{dockerx.Container{State: "dead"}, "dead", mapDanger},
		{dockerx.Container{State: "exited", Exited: exited(0)}, "exited (0)", mapIdle},
		// What `docker stop` leaves behind is somebody's stop, not a crash.
		{dockerx.Container{State: "exited", Status: "Exited (143) 5 minutes ago"}, "exited (143)", mapIdle},
		{dockerx.Container{State: "exited", Status: "Exited (137) 5 minutes ago"}, "exited (137)", mapIdle},
		{dockerx.Container{State: "exited", Exited: exited(2)}, "exited (2)", mapDanger},
		{dockerx.Container{State: "exited", Status: "Exited"}, "exited", mapIdle},
	} {
		got := mapContainer(tc.c)
		if got.State != tc.state || got.Tone != tc.tone {
			t.Errorf("%+v → %s/%s, want %s/%s", tc.c, got.State, got.Tone, tc.state, tc.tone)
		}
	}
}

func TestMapProjectStatesAreTheDeploymentPagesOwn(t *testing.T) {
	failed := &deploy.EngineRun{State: deploy.RunFailed, ReleaseID: 9}
	for _, tc := range []struct {
		d     deploy.DeploymentSummary
		state string
		tone  mapTone
	}{
		{deploy.DeploymentSummary{ActiveRun: &deploy.EngineRun{}}, "deploying", mapWarning},
		{deploy.DeploymentSummary{LastRun: failed}, "failed", mapDanger},
		{deploy.DeploymentSummary{}, "not deployed", mapIdle},
		{deploy.DeploymentSummary{LiveReleaseID: 3, Stopped: true}, "stopped", mapIdle},
		{deploy.DeploymentSummary{LiveReleaseID: 3, LastRun: failed}, "failed", mapDanger},
		// A failure the live release has since replaced is history.
		{deploy.DeploymentSummary{LiveReleaseID: 9, LastRun: failed}, "ready", mapOK},
		{deploy.DeploymentSummary{LiveReleaseID: 3, Health: "failed"}, "unhealthy", mapDanger},
	} {
		got := mapProject(tc.d)
		if got.State != tc.state || got.Tone != tc.tone {
			t.Errorf("%+v → %s/%s, want %s/%s", tc.d, got.State, got.Tone, tc.state, tc.tone)
		}
	}
}

// A source that failed keeps what it last showed and says so; one that has
// never answered is a shelf that says it is being read, never an empty one.
func TestMapNeverDrawsAnUnreadSourceAsEmpty(t *testing.T) {
	in := sampleInputs()
	in.containers.Err = errors.New("docker: connection refused")
	in.containers.At = mapNow.Add(-3 * time.Minute)
	in.certs = mapRead[[]proxysvc.Certificate]{Err: errMapReading}
	in.absent["pm2"] = true
	m := buildServerMap(in)

	containers := kindOf(t, m, "containers")
	if containers.State != "unread" || len(containers.Items) != 3 ||
		containers.Reason != "Last read 3m ago: docker: connection refused" {
		t.Errorf("a failed read = %+v", containers)
	}
	certs := kindOf(t, m, "certificates")
	if certs.State != "unread" || certs.Reason != "Still being read" || len(certs.Items) != 0 {
		t.Errorf("a read in flight = %+v", certs)
	}
	if pm2 := kindOf(t, m, "pm2"); pm2.State != "absent" {
		t.Errorf("a module the server does not run = %+v", pm2)
	}
}

func TestMapPutsTheWorstFirstAndCapsALongShelf(t *testing.T) {
	list := []dockerx.Container{}
	for i := 0; i < mapItemCap+40; i++ {
		list = append(list, dockerx.Container{ID: fmt.Sprintf("c%03d", i), Name: fmt.Sprintf("c%03d", i), State: "running"})
	}
	list = append(list, dockerx.Container{ID: "zz", Name: "zz", State: "dead"})
	in := mapInputs{now: mapNow, absent: map[string]bool{}, containers: mapGot(list)}
	m := buildServerMap(in)
	containers := kindOf(t, m, "containers")
	if containers.Total != mapItemCap+41 || len(containers.Items) != mapItemCap {
		t.Fatalf("total %d, sent %d", containers.Total, len(containers.Items))
	}
	if containers.Items[0].ID != "container:zz" {
		t.Errorf("the dead container is not first: %s", containers.Items[0].ID)
	}
}

func TestMapLeavesOutLinesToThingsItDoesNotDraw(t *testing.T) {
	in := sampleInputs()
	in.volumes = mapRead[[]dockerx.Volume]{Err: errMapReading}
	m := buildServerMap(in)
	if hasLink(m, "container:c-web", "volume:shop-data", "mount") {
		t.Error("a line to a volume that is not on the map")
	}
}

func TestMapShowsEachAccountOnlyWhatItsPagesWould(t *testing.T) {
	in := sampleInputs()
	in.accounts = mapGot([]*auth.User{{ID: 1, Username: "root", Role: auth.RoleAdmin}})
	in.keys = mapGot([]auth.APIToken{{ID: 10, UserID: 1, Name: "ci"}, {ID: 11, UserID: 2, Name: "mine"}, {ID: 12, UserID: 2, Name: "old", Revoked: true}})
	in.terminals = mapGot([]mapTerminal{{ID: "w1", Title: "deploy"}})
	m := buildServerMap(in)

	admin := &httpx.Principal{User: &auth.User{ID: 1}, Role: auth.RoleAdmin, Kind: "session"}
	all := filterServerMap(m, admin)
	if keys := kindOf(t, all, "keys"); len(keys.Items) != 2 || keys.State != "read" {
		t.Errorf("an administrator sees every live key: %+v", keys)
	}
	if len(kindOf(t, all, "accounts").Items) != 1 || len(kindOf(t, all, "terminals").Items) != 1 {
		t.Error("an administrator sees accounts and shells")
	}

	reader := &httpx.Principal{User: &auth.User{ID: 2}, Role: auth.RoleReadOnly, Kind: "session"}
	mine := filterServerMap(m, reader)
	keys := kindOf(t, mine, "keys")
	if len(keys.Items) != 1 || keys.Items[0].ID != "key:11" || keys.State != "limited" {
		t.Errorf("a reader sees its own keys, said to be its own: %+v", keys)
	}
	for _, k := range mine.Kinds {
		if k.Key == "accounts" || k.Key == "terminals" || k.Key == "alerts" {
			t.Errorf("a reader was sent %s", k.Key)
		}
	}
}

// The scheduled shelf is two sources; one still being read is said as that,
// over what the other already answered, never as a read from the year one.
func TestMapScheduledSaysWhichHalfIsMissing(t *testing.T) {
	in := mapInputs{now: mapNow, absent: map[string]bool{}}
	in.cron = mapGot([]procs.Crontab{{Source: "/etc/crontab", Jobs: []procs.CronJob{{Line: 3, Schedule: "0 7 * * *", Command: "report"}}}})
	in.timers = mapRead[[]procs.Timer]{Err: errMapReading}
	k := kindOf(t, buildServerMap(in), "scheduled")
	if k.State != "unread" || k.Reason != "Still being read" || len(k.Items) != 1 {
		t.Errorf("timers still being read = %+v", k)
	}
	in.timers = mapGot([]procs.Timer{{Unit: "certbot.timer", ActiveState: "active"}})
	if k := kindOf(t, buildServerMap(in), "scheduled"); k.State != "read" || len(k.Items) != 2 {
		t.Errorf("both answered = %+v", k)
	}
}
