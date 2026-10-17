package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// A limit is held to the machine it runs on: Docker refuses more processors
// than it has and accepts a memory limit beyond it that limits nothing.
func TestALimitIsHeldToTheMachine(t *testing.T) {
	host := dbHostCapacity{MemoryBytes: 8 << 30, CPUs: 4}
	for _, c := range []struct {
		memoryMB int64
		cpus     float64
		want     string
	}{
		{0, 0, ""},
		{6, 0.01, ""},
		{8192, 4, ""},
		{-1, 0, "cannot be negative"},
		{0, -0.5, "cannot be negative"},
		{5, 0, "at least 6 MiB"},
		{8193, 0, "8192 MiB of memory"},
		{1 << 62, 0, "8192 MiB of memory"},
		{0, 0.001, "at least 0.01"},
		{0, 4.5, "4 processors"},
	} {
		got := dbLimitsProblem(c.memoryMB, c.cpus, host)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%d MiB, %g cores: %q, want %q", c.memoryMB, c.cpus, got, c.want)
		}
	}
	// A machine Docker could not describe holds a limit only to Docker's floor.
	if got := dbLimitsProblem(64<<10, 64, dbHostCapacity{}); got != "" {
		t.Errorf("an unknown machine refused a limit: %q", got)
	}
}

// Docker changes a limit in place and cannot take one away, so a change is
// an update unless it lifts a limit, and only what changes is sent.
func TestALimitIsChangedInPlaceAndLiftedByReplacing(t *testing.T) {
	ptr := func(v int64) *int64 { return &v }
	cores := func(v float64) *float64 { return &v }
	current := dbResourceLimits{MemoryBytes: 1024 * mib, CPUs: 1.5}
	for _, c := range []struct {
		name    string
		req     dbResourcesRequest
		update  dockerx.ResourceLimits
		lifts   []string
		changed bool
	}{
		{"the same", dbResourcesRequest{MemoryMB: ptr(1024), CPUs: cores(1.5)}, dockerx.ResourceLimits{}, nil, false},
		{"memory only", dbResourcesRequest{MemoryMB: ptr(2048)}, dockerx.ResourceLimits{MemoryMB: 2048}, nil, true},
		{"both", dbResourcesRequest{MemoryMB: ptr(512), CPUs: cores(0.5)}, dockerx.ResourceLimits{MemoryMB: 512, CPUs: 0.5}, nil, true},
		{"memory lifted", dbResourcesRequest{MemoryMB: ptr(0), CPUs: cores(2)}, dockerx.ResourceLimits{CPUs: 2}, []string{"memory"}, true},
		{"everything lifted", dbResourcesRequest{MemoryMB: ptr(0), CPUs: cores(0)}, dockerx.ResourceLimits{}, []string{"memory", "cpus"}, true},
	} {
		plan := planDBLimits(current, c.req)
		if plan.update != c.update || !slices.Equal(plan.lifts, c.lifts) || plan.changed != c.changed {
			t.Errorf("%s: %+v", c.name, plan)
		}
	}
	// A processor limit Docker holds as 0.299999999 is the 0.3 that set it.
	if plan := planDBLimits(dbResourceLimits{CPUs: float64(nanoCPUs(0.3)) / 1e9}, dbResourcesRequest{CPUs: cores(0.3)}); plan.changed {
		t.Errorf("an unchanged processor limit was planned as a change: %+v", plan)
	}
	if plan := planDBLimits(dbResourceLimits{}, dbResourcesRequest{MemoryMB: ptr(256)}); plan.after.MemoryBytes != 256*mib || len(plan.lifts) != 0 {
		t.Errorf("a first memory limit: %+v", plan)
	}
}

// resourcesHarness is a Postgres in a container of the test engine's, behind
// the database routes with the audit trail mounted as the real router has it.
func resourcesHarness(t *testing.T, box *fakeDBContainer) (*connHarness, int64, func(role auth.Role) http.Handler) {
	t.Helper()
	h := newConnHarness(t)
	port, _ := deafPort(t)
	box.hostIP, box.hostPort, box.port = "127.0.0.1", port, 5432
	h.engine.containers = []*fakeDBContainer{box}
	id := h.add("shop", dbx.DriverPostgres, fmt.Sprintf("postgres://jd:pw@127.0.0.1:%d/shop?sslmode=disable", port))
	as := func(role auth.Role) http.Handler {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: role, Kind: "session", IP: "127.0.0.1"}
				next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
			})
		})
		r.Use(httpx.AuditMutations(h.s.Audit))
		h.s.mountDatabaseRoutes(r)
		return r
	}
	return h, id, as
}

type resourcesView struct {
	Container string `json:"container"`
	Editable  bool   `json:"editable"`
	Refusal   string `json:"refusal"`
	Running   bool   `json:"running"`
	Limits    struct {
		MemoryBytes int64   `json:"memoryBytes"`
		CPUs        float64 `json:"cpus"`
	} `json:"limits"`
	UsedBytes int64 `json:"usedBytes"`
	OOMKilled bool  `json:"oomKilled"`
	Host      struct {
		MemoryBytes int64 `json:"memoryBytes"`
		CPUs        int   `json:"cpus"`
	} `json:"host"`
	Buffer      *dbx.MemoryBuffer `json:"buffer"`
	BufferError string            `json:"bufferError"`
}

// The reading is the container's own configuration and the machine's, for
// any role: Home shows the same limits to everyone.
func TestResourcesAreReadOffTheContainer(t *testing.T) {
	box := &fakeDBContainer{id: "c0ffee", name: "shop-db", image: "postgres:16-alpine", state: "running",
		memory: 512 << 20, nanoCPUs: 1_500_000_000, used: 300 << 20, oomKilled: true}
	_, id, as := resourcesHarness(t, box)
	got := readJSON[resourcesView](t, do(t, as(auth.RoleReadOnly), http.MethodGet, pathf("/databases/%d/resources", id), ""))
	if got.Container != "shop-db" || !got.Editable || got.Refusal != "" || !got.Running {
		t.Errorf("a container the dashboard may change: %+v", got)
	}
	if got.Limits.MemoryBytes != 512<<20 || got.Limits.CPUs != 1.5 || got.UsedBytes != 300<<20 || !got.OOMKilled {
		t.Errorf("limits %+v, used %d, oomKilled %v", got.Limits, got.UsedBytes, got.OOMKilled)
	}
	if got.Host.MemoryBytes != 8<<30 || got.Host.CPUs != 8 {
		t.Errorf("the machine read as %+v", got.Host)
	}
	// The server behind the port does not answer, so its buffer is not known
	// and the page says why.
	if got.Buffer != nil || got.BufferError == "" {
		t.Errorf("buffer %+v, error %q", got.Buffer, got.BufferError)
	}

	// A stopped container keeps its limits, and is not dialled.
	box.state = "exited"
	got = readJSON[resourcesView](t, do(t, as(auth.RoleReadOnly), http.MethodGet, pathf("/databases/%d/resources", id), ""))
	if got.Running || got.Limits.MemoryBytes != 512<<20 || got.UsedBytes != 0 || got.BufferError != "" {
		t.Errorf("a stopped container: %+v", got)
	}
}

// Where the limits are not the dashboard's to set, the reading says whose
// they are and the change is refused with the same sentence.
func TestResourcesThatAreNotTheDashboardsSayWhose(t *testing.T) {
	for _, c := range []struct {
		name string
		box  *fakeDBContainer
		want string
	}{
		{"compose", &fakeDBContainer{id: "c1", name: "stack-db-1", image: "postgres:16-alpine", state: "running",
			labels: map[string]string{"com.docker.compose.project": "stack"}}, "compose project stack"},
		{"not the engine", &fakeDBContainer{id: "c2", name: "pooler", image: "edoburu/pgbouncer:1.23", state: "running"},
			"not one of this engine's"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, id, as := resourcesHarness(t, c.box)
			got := readJSON[resourcesView](t, do(t, as(auth.RoleAdmin), http.MethodGet, pathf("/databases/%d/resources", id), ""))
			if got.Editable || !strings.Contains(got.Refusal, c.want) {
				t.Errorf("reading: %+v", got)
			}
			rec := do(t, as(auth.RoleAdmin), http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":1024}`)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "resources_unavailable") ||
				!strings.Contains(rec.Body.String(), c.want) {
				t.Errorf("change = %d %s", rec.Code, rec.Body.String())
			}
			if slices.ContainsFunc(h.engine.did(), func(call string) bool { return strings.HasPrefix(call, "update") }) {
				t.Errorf("Docker was asked to change it: %v", h.engine.did())
			}
		})
	}

	h := newConnHarness(t)
	id := h.add("prod", dbx.DriverPostgres, "postgres://app:pw@db.example.com:5432/shop?sslmode=require")
	got := readJSON[resourcesView](t, do(t, h.as(auth.RoleAdmin), http.MethodGet, pathf("/databases/%d/resources", id), ""))
	if got.Editable || got.Container != "" || !strings.Contains(got.Refusal, "another machine (db.example.com)") {
		t.Errorf("a server elsewhere: %+v", got)
	}
}

// A change is `docker update` on the container, with only what changed, and
// is on the audit trail with the limits before and after.
func TestAChangeOfLimitsIsAppliedInPlaceAndAudited(t *testing.T) {
	box := &fakeDBContainer{id: "c0ffee", name: "shop-db", image: "postgres:16-alpine", state: "running",
		memory: 512 << 20, used: 300 << 20}
	h, id, as := resourcesHarness(t, box)
	rec := do(t, as(auth.RoleAdmin), http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":1024,"cpus":2}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"replaced":false`) {
		t.Fatalf("change = %d %s", rec.Code, rec.Body.String())
	}
	if box.memory != 1024<<20 || box.nanoCPUs != 2_000_000_000 {
		t.Errorf("the container holds %d bytes and %d nano-processors", box.memory, box.nanoCPUs)
	}
	if !slices.Contains(h.engine.did(), "update shop-db") || slices.ContainsFunc(h.engine.did(), func(call string) bool {
		return strings.HasPrefix(call, "stop") || strings.HasPrefix(call, "restart") || strings.HasPrefix(call, "rename")
	}) {
		t.Errorf("Docker was asked %v", h.engine.did())
	}
	var action, detail string
	if err := h.s.Store.DB.QueryRow(`SELECT action, detail FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&action, &detail); err != nil {
		t.Fatal(err)
	}
	if action != "database.resources.change" || !strings.Contains(detail, `"memoryBytes":536870912`) ||
		!strings.Contains(detail, `"memoryBytes":1073741824`) || !strings.Contains(detail, `"cpus":2`) {
		t.Errorf("audited as %s %s", action, detail)
	}

	// The same limits again change nothing and say so.
	rec = do(t, as(auth.RoleAdmin), http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":1024}`)
	if rec.Code != http.StatusOK || strings.Count(strings.Join(h.engine.did(), " "), "update") != 1 {
		t.Errorf("an unchanged limit = %d %s, Docker asked %v", rec.Code, rec.Body.String(), h.engine.did())
	}
}

// A limit the kernel would meet by killing the engine at once, one the
// machine cannot honour, and a lift that would start a stopped server are
// each refused before Docker is asked.
func TestALimitThatWouldStopTheServerIsRefused(t *testing.T) {
	box := &fakeDBContainer{id: "c0ffee", name: "shop-db", image: "postgres:16-alpine", state: "running",
		memory: 2048 << 20, used: 700 << 20}
	h, id, as := resourcesHarness(t, box)
	admin := as(auth.RoleAdmin)
	for body, want := range map[string]string{
		`{"memoryMb":512}`:  "uses 700 MiB now",
		`{"memoryMb":9000}`: "8192 MiB of memory",
		`{"cpus":9}`:        "8 processors",
		`{"memoryMb":3}`:    "at least 6 MiB",
		`{}`:                "name a memoryMb or a cpus",
	} {
		rec := do(t, admin, http.MethodPut, pathf("/databases/%d/resources", id), body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s = %d %s, want 400 saying %q", body, rec.Code, rec.Body.String(), want)
		}
	}
	box.state = "exited"
	rec := do(t, admin, http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":0}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "container_stopped") {
		t.Errorf("lifting a stopped container's limit = %d %s", rec.Code, rec.Body.String())
	}
	// Stopped, a new limit is still Docker's to hold until it starts.
	if rec := do(t, admin, http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":512}`); rec.Code != http.StatusOK || box.memory != 512<<20 {
		t.Errorf("a limit on a stopped container = %d %s, holds %d", rec.Code, rec.Body.String(), box.memory)
	}
	if calls := h.engine.did(); slices.ContainsFunc(calls, func(call string) bool {
		return strings.HasPrefix(call, "stop") || strings.HasPrefix(call, "rename")
	}) {
		t.Errorf("Docker was asked %v", calls)
	}
}

// Changing the limits is an administrator's; reading them is anyone's.
func TestChangingLimitsIsAnAdministrators(t *testing.T) {
	box := &fakeDBContainer{id: "c0ffee", name: "shop-db", image: "postgres:16-alpine", state: "running"}
	h, id, as := resourcesHarness(t, box)
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		if rec := do(t, as(role), http.MethodPut, pathf("/databases/%d/resources", id), `{"memoryMb":1024}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s changed the limits: %d %s", role, rec.Code, rec.Body.String())
		}
		if rec := do(t, as(role), http.MethodGet, pathf("/databases/%d/resources", id), ""); rec.Code != http.StatusOK {
			t.Errorf("%s could not read them: %d", role, rec.Code)
		}
	}
	if len(h.engine.did()) != 0 {
		t.Errorf("Docker was asked %v", h.engine.did())
	}
	if rec := do(t, as(auth.RoleLimited), http.MethodGet, "/databases/provision/capacity", ""); rec.Code != http.StatusForbidden {
		t.Errorf("the provisioning capacity read is an administrator's: %d", rec.Code)
	}
	got := readJSON[dbHostCapacity](t, do(t, as(auth.RoleAdmin), http.MethodGet, "/databases/provision/capacity", ""))
	if got.CPUs != 8 || got.MemoryBytes != 8<<30 {
		t.Errorf("capacity = %+v", got)
	}
}
