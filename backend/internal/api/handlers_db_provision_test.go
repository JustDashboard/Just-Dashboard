package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
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
		Mounts []struct {
			Type   string `json:"Type"`
			Source string `json:"Source"`
			Target string `json:"Target"`
		} `json:"Mounts"`
	} `json:"HostConfig"`
}

func provisionNameEngine(t *testing.T, s *Server, containers, volumes map[string]bool) *[]provisionCreated {
	return provisionEngine(t, s, containers, volumes, nil)
}

// provisionEngine is provisionNameEngine with images of the server's own:
// the labels each one built here carries, by reference. Every other image is
// one the engine has, unlabelled, except the dashboard's own names, which it
// has not.
func provisionEngine(t *testing.T, s *Server, containers, volumes map[string]bool, built map[string]map[string]string) *[]provisionCreated {
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
			ref := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
			if labels, ok := built[ref]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": ref, "Config": map[string]any{"Labels": labels}})
				return
			}
			if strings.HasPrefix(ref, "jd-") {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such image"}`))
				return
			}
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

type provisionedOption struct {
	Engine   string `json:"engine"`
	Versions []struct {
		Version   string         `json:"version"`
		Image     string         `json:"image"`
		EndOfLife *dbx.EndOfLife `json:"endOfLife"`
		Build     *struct {
			Ready bool   `json:"ready"`
			Job   string `json:"job"`
		} `json:"build"`
	} `json:"versions"`
	DefaultVersion string   `json:"defaultVersion"`
	Extensions     []string `json:"extensions"`
}

// Each release says where it stands as a date, where the table has one, and a
// release whose image is built here says whether it already is — from this
// recipe, not an older one.
func TestProvisionOptionsSayWhereEachReleaseStands(t *testing.T) {
	s, router := dbTestRouter(t)
	provisionEngine(t, s, map[string]bool{}, map[string]bool{}, map[string]map[string]string{
		"jd-postgres-full:18": {recipeLabel: recipeDigest(postgresFullRecipe)},
		"jd-postgres-full:17": {recipeLabel: "a1b2c3d4e5f6"},
	})
	options := readJSON[[]provisionedOption](t, do(t, router, http.MethodGet, "/databases/provision/options", ""))
	byEngine := map[string]provisionedOption{}
	for _, o := range options {
		byEngine[o.Engine] = o
	}
	release := func(engine, version string) (found struct {
		eol   *dbx.EndOfLife
		ready *bool
	}) {
		t.Helper()
		for _, v := range byEngine[engine].Versions {
			if v.Version == version {
				found.eol = v.EndOfLife
				if v.Build != nil {
					found.ready = &v.Build.Ready
				}
				return found
			}
		}
		t.Fatalf("%s offers no %s", engine, version)
		return found
	}
	if eol := release("postgres", "18").eol; eol == nil || eol.Date.Format("2006-01-02") != "2030-11-14" || eol.Product != "PostgreSQL" {
		t.Errorf("PostgreSQL 18 = %+v", eol)
	}
	if eol := release("mysql", "8.0").eol; eol == nil || !eol.Past {
		t.Errorf("MySQL 8.0 ended on 2026-04-30 and reads as %+v", eol)
	}
	if eol := release("mariadb", "10.6").eol; eol == nil || !eol.Past {
		t.Errorf("MariaDB 10.6 ended on 2026-07-06 and reads as %+v", eol)
	}
	if eol := release("timescaledb", "17").eol; eol == nil || eol.Release != "17" {
		t.Errorf("TimescaleDB on 17 is PostgreSQL 17's: %+v", eol)
	}
	if eol := release("redis", "8").eol; eol != nil {
		t.Errorf("Redis has no dates in the table and was given %+v", eol)
	}
	if ready := release("postgres-full", "18").ready; ready == nil || !*ready {
		t.Error("an image built from this recipe does not read as built")
	}
	if ready := release("postgres-full", "17").ready; ready == nil || *ready {
		t.Error("an image built from an older recipe reads as built")
	}
	for engine, o := range byEngine {
		recipe := provisionTemplates[engine].recipe != ""
		for _, v := range o.Versions {
			if (v.Build != nil) != recipe {
				t.Errorf("%s %s: build state %+v on a template whose recipe is %v", engine, v.Version, v.Build, recipe)
			}
		}
		if (len(o.Extensions) > 0) != recipe {
			t.Errorf("%s: extensions %v", engine, o.Extensions)
		}
	}
}

// The catalogue as refreshed on 2026-10-17: every release it offers is one
// the end-of-life table can date, where the table speaks for the engine at
// all, and every default was being maintained that day. An offered release
// with no date would be drawn with nothing to say whether it is current.
func TestProvisionCatalogueDefaultsWereMaintainedWhenItWasRefreshed(t *testing.T) {
	refreshed := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	dated := map[string]bool{
		dbx.FlavorPostgres: true, dbx.FlavorTimescaleDB: true, dbx.FlavorMySQL: true,
		dbx.FlavorMariaDB: true, dbx.FlavorMongoDB: true, dbx.FlavorClickHouse: true,
	}
	for engine, tmpl := range provisionTemplates {
		for _, v := range tmpl.versions {
			eol, ok := dbx.ReleaseEndOfLife(tmpl.flavor, v.id, refreshed)
			if ok != dated[tmpl.flavor] {
				t.Errorf("%s %s: dated %v, and the table speaks for %s: %v", engine, v.id, ok, tmpl.flavor, dated[tmpl.flavor])
			}
			if v.image == tmpl.image && ok && eol.Past {
				t.Errorf("%s: the default %s ended on %s", engine, v.id, eol.Date.Format("2006-01-02"))
			}
		}
		if engine == "postgres" || engine == "pgvector" || engine == "postgis" || engine == "timescaledb" || engine == "postgres-full" {
			if def, _ := tmpl.version(""); def.id != "18" {
				t.Errorf("%s defaults to %s, not 18", engine, def.id)
			}
		}
	}
	// A floating tag is a different server on a different day, and a date
	// that belongs to the line it floated off.
	for engine, tmpl := range provisionTemplates {
		for _, v := range tmpl.versions {
			if strings.Contains(v.image, ":latest-") || (strings.HasSuffix(v.image, ":latest") && engine != "keydb" && engine != "dragonfly") {
				t.Errorf("%s %s floats: %s", engine, v.id, v.image)
			}
		}
	}
}

// PostgreSQL 18's image keeps its cluster under /var/lib/postgresql and
// declares that the volume; earlier majors keep it a directory further down.
// The named volume has to be where each one writes, or the data goes into an
// anonymous volume that a recreate leaves behind.
func TestProvisionMountsTheVolumeWhereEachMajorKeepsItsData(t *testing.T) {
	s, router := dbTestRouter(t)
	created := provisionNameEngine(t, s, map[string]bool{}, map[string]bool{})
	for _, c := range []struct{ body, image, target string }{
		{`{"engine":"postgres","name":"jd-a"}`, "postgres:18-alpine", "/var/lib/postgresql"},
		{`{"engine":"postgres","name":"jd-b","version":"17"}`, "postgres:17-alpine", "/var/lib/postgresql/data"},
		{`{"engine":"timescaledb","name":"jd-c"}`, "timescale/timescaledb:2.30.2-pg18", "/var/lib/postgresql"},
		{`{"engine":"pgvector","name":"jd-d","version":"16"}`, "pgvector/pgvector:pg16", "/var/lib/postgresql/data"},
		{`{"engine":"mariadb","name":"jd-e"}`, "mariadb:11.8", "/var/lib/mysql"},
	} {
		before := len(*created)
		if rec := do(t, router, http.MethodPost, "/databases/provision", c.body); rec.Code != http.StatusAccepted {
			t.Fatalf("provision %s = %d %s", c.body, rec.Code, rec.Body.String())
		}
		spec := (*created)[before]
		if spec.Image != c.image || len(spec.HostConfig.Mounts) != 1 || spec.HostConfig.Mounts[0].Target != c.target ||
			spec.HostConfig.Mounts[0].Type != "volume" || !strings.HasSuffix(spec.HostConfig.Mounts[0].Source, "-data") {
			t.Errorf("%s: image %s mounts %+v, want its volume at %s", c.body, spec.Image, spec.HostConfig.Mounts, c.target)
		}
		if len(spec.Cmd) != 0 {
			t.Errorf("%s: started with %q rather than the image's own command", c.body, spec.Cmd)
		}
	}
}

// The full template is never pulled: until its image has been built here from
// the current recipe, a request is refused before anything is created. Once
// it has, the server starts like any other, on the image's own command: the
// image loads what its extensions need, where the Parameters page can change
// it afterwards.
func TestTheFullTemplateStartsOnlyFromTheImageBuiltHere(t *testing.T) {
	s, router := dbTestRouter(t)
	built := map[string]map[string]string{"jd-postgres-full:17": {recipeLabel: "a1b2c3d4e5f6"}}
	created := provisionEngine(t, s, map[string]bool{}, map[string]bool{}, built)
	for _, body := range []string{`{"engine":"postgres-full"}`, `{"engine":"postgres-full","version":"17"}`} {
		rec := do(t, router, http.MethodPost, "/databases/provision", body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "image_not_built") {
			t.Errorf("%s with no current build = %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if len(*created) != 0 {
		t.Fatalf("an image that was never built started %d containers", len(*created))
	}

	built["jd-postgres-full:18"] = map[string]string{recipeLabel: recipeDigest(postgresFullRecipe)}
	rec := do(t, router, http.MethodPost, "/databases/provision", `{"engine":"postgres-full","database":"orders"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("provision = %d %s", rec.Code, rec.Body.String())
	}
	if len(*created) != 1 {
		t.Fatalf("%d containers were created", len(*created))
	}
	spec := (*created)[0]
	if spec.Image != "jd-postgres-full:18" || len(spec.Cmd) != 0 || spec.HostConfig.Mounts[0].Target != "/var/lib/postgresql" ||
		!strings.Contains(strings.Join(spec.Env, "\n"), "POSTGRES_DB=orders") {
		t.Errorf("full template spec: image %s, command %q, env %q, mounts %+v", spec.Image, spec.Cmd, spec.Env, spec.HostConfig.Mounts)
	}
}

// The recipe is the official image and the PostgreSQL project's packages of
// the extensions the template says it carries, and it labels what it builds
// with the digest the readiness check compares. The libraries two of them
// need loaded at start are named in the configuration a new cluster is made
// from, never on the command line, where they would outrank every change
// made from the Parameters page.
func TestTheFullRecipeIsTheOfficialImageAndItsOwnRepository(t *testing.T) {
	recipe := postgresFullRecipe
	if !strings.Contains(recipe, "FROM postgres:${PG_MAJOR}-trixie") ||
		!strings.Contains(recipe, `LABEL io.just-dashboard.recipe="${JD_RECIPE}"`) || recipeLabel != "io.just-dashboard.recipe" {
		t.Errorf("the recipe does not start from the official image or does not label its build:\n%s", recipe)
	}
	if !strings.Contains(recipe, `\1 = 'pg_cron,pgaudit'/" /usr/share/postgresql/postgresql.conf.sample`) ||
		!strings.Contains(recipe, "ALTER SYSTEM SET cron.database_name = %L', current_database()") ||
		!strings.Contains(recipe, "> /docker-entrypoint-initdb.d/") {
		t.Errorf("the recipe does not preload pg_cron and pgaudit in the sample configuration, or does not point pg_cron at the server's database:\n%s", recipe)
	}
	if strings.Contains(recipe, "CMD") || strings.Contains(recipe, "ENTRYPOINT") || len(provisionTemplates["postgres-full"].bootstrap) != 0 {
		t.Error("the full template replaces the official image's command")
	}
	packages := map[string]string{
		"vector": "pgvector", "postgis": "postgis-3", "pg_cron": "cron", "pg_partman": "partman",
		"pgaudit": "pgaudit", "hypopg": "hypopg", "pg_repack": "repack",
	}
	tmpl := provisionTemplates["postgres-full"]
	if len(tmpl.extensions) != len(packages) {
		t.Errorf("the template says it carries %v", tmpl.extensions)
	}
	for _, extension := range tmpl.extensions {
		if !strings.Contains(recipe, `"postgresql-${PG_MAJOR}-`+packages[extension]+`"`) {
			t.Errorf("%s is said to be carried and the recipe installs no package of it", extension)
		}
	}
	for _, line := range strings.Split(recipe, "\n") {
		if strings.Contains(line, "curl") || strings.Contains(line, "wget") || strings.Contains(line, "http") {
			t.Errorf("the recipe fetches from outside the image's own repository: %q", line)
		}
	}
	for _, library := range []string{"pg_cron", "pgaudit"} {
		if !slices.Contains(tmpl.extensions, library) {
			t.Errorf("%s is preloaded and not carried", library)
		}
	}
	if recipeDigest(recipe) == recipeDigest(recipe+" ") || len(recipeDigest(recipe)) != 12 {
		t.Error("the digest does not follow the recipe")
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
		`{"engine":"postgres","version":"13"}`,
		`{"engine":"postgres","version":"16-alpine"}`,
		`{"engine":"mariadb","version":"11"}`,
		`{"engine":"timescaledb","version":"15"}`,
		`{"engine":"postgres-full","version":"16"}`,
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
