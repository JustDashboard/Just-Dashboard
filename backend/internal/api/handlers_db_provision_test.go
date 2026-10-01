package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// provisionCreated is what the test engine was asked to create, in the
// Engine API's own shape.
type provisionCreated struct {
	Image      string   `json:"Image"`
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
}

func provisionNameEngine(t *testing.T, s *Server, containers, volumes map[string]bool) *[]provisionCreated {
	t.Helper()
	var mu sync.Mutex
	created := &[]provisionCreated{}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/v1.47")
		switch {
		case path == "/_ping":
			w.Header().Set("API-Version", "1.47")
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			if !containers[name] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such container"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": name, "Name": "/" + name,
				"Config": map[string]any{"Image": "postgres:16-alpine"},
				"State":  map[string]any{"Status": "running", "Running": true},
				"HostConfig": map[string]any{
					"NetworkMode": "bridge",
				},
				"NetworkSettings": map[string]any{"Ports": map[string]any{
					"5432/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "5432"}},
				}},
			})
		case strings.HasPrefix(path, "/volumes/"):
			name := strings.TrimPrefix(path, "/volumes/")
			if !volumes[name] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such volume"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Name": name})
		case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
			_, _ = w.Write([]byte(`{"Id":"postgres-image"}`))
		case path == "/containers/create":
			name := r.URL.Query().Get("name")
			if containers[name] || volumes[name+"-data"] {
				t.Errorf("provision tried to reuse %s or its persistent data", name)
				w.WriteHeader(http.StatusConflict)
				return
			}
			var spec provisionCreated
			if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
				t.Errorf("the create request is not a container spec: %v", err)
			}
			*created = append(*created, spec)
			containers[name], volumes[name+"-data"] = true, true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": name})
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	return created
}

func TestDatabaseProvisionDefaultsSkipContainersAndRetainedVolumes(t *testing.T) {
	s, router := dbTestRouter(t)
	provisionNameEngine(t, s, map[string]bool{"jd-postgres": true}, map[string]bool{"jd-postgres-2-data": true})
	for _, want := range []string{"jd-postgres-3", "jd-postgres-4"} {
		response := do(t, router, http.MethodPost, "/databases/provision", `{"engine":"postgres","exposure":"local"}`)
		if response.Code != http.StatusAccepted {
			t.Fatalf("provision = %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Container string `json:"container"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Container != want {
			t.Fatalf("provision = %#v, %v; want %s", result, err, want)
		}
	}
	for _, tc := range []struct{ name, code string }{
		{"jd-postgres", "container_exists"},
		{"jd-postgres-2", "volume_exists"},
	} {
		response := do(t, router, http.MethodPost, "/databases/provision",
			`{"engine":"postgres","exposure":"local","name":"`+tc.name+`"}`)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), tc.code) {
			t.Fatalf("explicit name %s = %d %s", tc.name, response.Code, response.Body.String())
		}
	}
}

func TestDatabaseProvisionReservesNamesBeforeImagePullAndReleasesFailures(t *testing.T) {
	s := testServer(t)
	provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
	first, releaseFirst, err := s.reserveDatabaseName(t.Context(), "", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := s.reserveDatabaseName(t.Context(), "", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if first != "jd-postgres" || second != "jd-postgres-2" {
		t.Fatalf("in-flight names = %q, %q", first, second)
	}
	if _, _, err := s.reserveDatabaseName(t.Context(), first, "postgres"); err == nil {
		t.Fatal("an explicit name collided with an in-flight provision")
	}
	releaseFirst()
	retry, releaseRetry, err := s.reserveDatabaseName(t.Context(), "", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseRetry()
	if retry != first {
		t.Fatalf("failed operation kept its name: got %s, want %s", retry, first)
	}
}

// The options are what the page builds its form from, in a fixed order, with
// the releases a request may choose and the one it gets when it chooses none.
func TestProvisionOptionsListEveryTemplateWithItsVersions(t *testing.T) {
	_, router := dbTestRouter(t)
	rec := do(t, router, http.MethodGet, "/databases/provision/options", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("options = %d %s", rec.Code, rec.Body.String())
	}
	var options []struct {
		Engine   string `json:"engine"`
		Label    string `json:"label"`
		Image    string `json:"image"`
		Driver   string `json:"driver"`
		Flavor   string `json:"flavor"`
		Versions []struct {
			Version string `json:"version"`
			Image   string `json:"image"`
		} `json:"versions"`
		DefaultVersion string `json:"defaultVersion"`
		Port           int    `json:"port"`
		DefaultUser    string `json:"defaultUser"`
		Database       bool   `json:"database"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &options); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	var order []string
	for _, o := range options {
		listed[o.Engine] = true
		order = append(order, o.Engine)
		tmpl, ok := provisionTemplates[o.Engine]
		if !ok {
			t.Errorf("option %q has no template", o.Engine)
			continue
		}
		if o.Label == "" || o.Image != tmpl.image || o.Driver != string(tmpl.driver) || o.Flavor != tmpl.flavor || o.Port != tmpl.port {
			t.Errorf("%s: option %+v disagrees with its template", o.Engine, o)
		}
		if len(o.Versions) == 0 || o.DefaultVersion == "" {
			t.Errorf("%s: no versions or no default: %+v", o.Engine, o)
		}
		defaults := 0
		seen := map[string]bool{}
		for _, v := range o.Versions {
			if v.Version == "" || v.Image == "" || seen[v.Version] {
				t.Errorf("%s: version %+v is incomplete or repeated", o.Engine, v)
			}
			seen[v.Version] = true
			if v.Version == o.DefaultVersion {
				defaults++
				if v.Image != o.Image {
					t.Errorf("%s: the default version starts %q, the option says %q", o.Engine, v.Image, o.Image)
				}
			}
		}
		if defaults != 1 {
			t.Errorf("%s: %d versions are the default", o.Engine, defaults)
		}
		if (o.DefaultUser == "") != (tmpl.driver == "redis") {
			t.Errorf("%s: default user %q; only the password-only engines have none", o.Engine, o.DefaultUser)
		}
	}
	for engine := range provisionTemplates {
		// PostGIS is left out where its image cannot run, and nowhere else.
		if !listed[engine] && engine != "postgis" {
			t.Errorf("template %q is not offered", engine)
		}
	}
	if got := strings.Join(order, ","); !strings.HasPrefix(got, "postgres,pgvector,") || !strings.HasSuffix(got, ",mongodb,clickhouse") {
		t.Errorf("options are not in the fixed order: %s", got)
	}
}

// A request names a version from the template's own list, an account and a
// password within what the image can take, and gets exactly that container —
// on this server only, unless it asked for more.
func TestProvisionStartsTheVersionAndAccountAsked(t *testing.T) {
	s, router := dbTestRouter(t)
	created := provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})

	rec := do(t, router, http.MethodPost, "/databases/provision",
		`{"engine":"postgres","name":"jd-shop","version":"17","user":"shop","password":"correct-horse-9","database":"orders"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("provision = %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["version"] != "17" || result["image"] != "postgres:17-alpine" || result["user"] != "shop" ||
		result["database"] != "orders" || result["exposure"] != "local" || result["flavor"] != "postgres" {
		t.Errorf("provision result = %v", result)
	}
	if strings.Contains(rec.Body.String(), "correct-horse-9") {
		t.Error("the password came back in the response")
	}
	if len(*created) != 1 {
		t.Fatalf("%d containers were created", len(*created))
	}
	spec := (*created)[0]
	env := strings.Join(spec.Env, "\n")
	if spec.Image != "postgres:17-alpine" || !strings.Contains(env, "POSTGRES_USER=shop") ||
		!strings.Contains(env, "POSTGRES_PASSWORD=correct-horse-9") || !strings.Contains(env, "POSTGRES_DB=orders") {
		t.Errorf("container spec = %+v", spec)
	}
	for _, bindings := range spec.HostConfig.PortBindings {
		for _, b := range bindings {
			if b.HostIP != "127.0.0.1" {
				t.Errorf("a request that named no exposure was published on %q", b.HostIP)
			}
		}
	}

	// A password the request did not supply is generated, and is not the
	// same one twice.
	for _, name := range []string{"jd-a", "jd-b"} {
		if rec := do(t, router, http.MethodPost, "/databases/provision", `{"engine":"valkey","name":"`+name+`"}`); rec.Code != http.StatusAccepted {
			t.Fatalf("provision valkey = %d %s", rec.Code, rec.Body.String())
		}
	}
	a, b := (*created)[1], (*created)[2]
	if a.Image != "valkey/valkey:8-alpine" || len(a.Env) != 1 || !strings.HasPrefix(a.Env[0], "REDIS_PASSWORD=") ||
		len(strings.TrimPrefix(a.Env[0], "REDIS_PASSWORD=")) < 24 || a.Env[0] == b.Env[0] {
		t.Errorf("valkey specs = %+v / %+v", a, b)
	}
	// The password reaches the server through its environment, never through
	// the command it is started with.
	command := strings.Join(a.Cmd, " ")
	if !strings.Contains(command, "valkey-server") || !strings.Contains(command, "$REDIS_PASSWORD") ||
		strings.Contains(command, strings.TrimPrefix(a.Env[0], "REDIS_PASSWORD=")) {
		t.Errorf("valkey command = %q", command)
	}
}

func TestProvisionRefusesWhatItsTemplateDoesNotOffer(t *testing.T) {
	s, router := dbTestRouter(t)
	created := provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
	for _, body := range []string{
		`{"engine":"postgres","version":"18"}`,
		`{"engine":"postgres","version":"16-alpine"}`,
		`{"engine":"postgres","version":"latest; rm -rf /"}`,
		`{"engine":"postgres","image":"evil/postgres:16"}`,
		`{"engine":"cockroachdb"}`,
		`{"engine":"postgres","user":"drop table"}`,
		`{"engine":"postgres","user":"9lives"}`,
		`{"engine":"mysql","user":"root"}`,
		`{"engine":"mariadb","user":"ROOT"}`,
		`{"engine":"redis","user":"app"}`,
		`{"engine":"dragonfly","user":"app"}`,
		`{"engine":"postgres","password":"short"}`,
		`{"engine":"postgres","password":"has a space in it"}`,
		`{"engine":"postgres","password":"quote'\"breaks#conf"}`,
		`{"engine":"mysql","password":"at@sign:slash/"}`,
		`{"engine":"clickhouse","password":"<xml>&entity;"}`,
		`{"engine":"postgres","database":"my-db"}`,
		`{"engine":"postgres","exposure":"private"}`,
	} {
		rec := do(t, router, http.MethodPost, "/databases/provision", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("provision %s = %d %s, want 400", body, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if len(*created) != 0 {
		t.Fatalf("a refused request created %d containers", len(*created))
	}
	// An engine with no database to name drops the word rather than refusing
	// it: the same form is posted for every engine.
	if rec := do(t, router, http.MethodPost, "/databases/provision", `{"engine":"redis","database":"app"}`); rec.Code != http.StatusAccepted {
		t.Errorf("redis with a database field = %d %s", rec.Code, rec.Body.String())
	}
}

// The bootstrap scripts are constants. The password is the one thing in them
// that varies, and it is read from the environment at run time.
func TestProvisionBootstrapsCarryNoRequestText(t *testing.T) {
	for engine, tmpl := range provisionTemplates {
		if tmpl.bootstrap == "" {
			continue
		}
		if strings.Count(tmpl.bootstrap, "$REDIS_PASSWORD") != 1 || strings.Contains(tmpl.bootstrap, "%!") {
			t.Errorf("%s: bootstrap = %q", engine, tmpl.bootstrap)
		}
		if !strings.Contains(tmpl.bootstrap, "umask 077") || !strings.Contains(tmpl.bootstrap, "exec docker-entrypoint.sh ") {
			t.Errorf("%s: the bootstrap writes a readable file or does not hand over to the image: %q", engine, tmpl.bootstrap)
		}
		// The configuration is removed before it is rewritten. A second start
		// otherwise cannot open the file the first one gave to the server's
		// account, and the container never comes back from a restart.
		remove, write := strings.Index(tmpl.bootstrap, `rm -f "$conf"`), strings.Index(tmpl.bootstrap, `> "$conf"`)
		if remove < 0 || write < remove {
			t.Errorf("%s: the bootstrap does not clear its configuration before writing it: %q", engine, tmpl.bootstrap)
		}
	}
	const redis = `set -eu; umask 077; conf=/tmp/jd-redis-server.conf; rm -f "$conf"; printf 'requirepass %s\n' "$REDIS_PASSWORD" > "$conf"; chown redis:redis "$conf"; exec docker-entrypoint.sh redis-server "$conf"`
	if provisionTemplates["redis"].bootstrap != redis {
		t.Errorf("redis bootstrap = %q", provisionTemplates["redis"].bootstrap)
	}
}
