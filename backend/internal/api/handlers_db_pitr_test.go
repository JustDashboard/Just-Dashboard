package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// pitrRouterAs is the database routes as one role sees them, over a
// PostgreSQL connection to a server somewhere else: every route gets past its
// capability and stops at the server not being one this dashboard runs.
func pitrRouterAs(t *testing.T, role auth.Role) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	// No Docker: nothing here may reach the host's.
	s.modules.docker = nil
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	s.mountDatabaseRoutes(r)
	sealed, err := s.Sealer.Seal("postgres://jd:pw@db.example.com:5432/app?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"elsewhere", string(dbx.DriverPostgres), sealed, 0); err != nil {
		t.Fatal(err)
	}
	return s, r
}

type pitrRoute struct{ method, path, body string }

var (
	pitrReadSurface    = []pitrRoute{{http.MethodGet, "/pitr", ""}}
	pitrServiceControl = []pitrRoute{
		{http.MethodPost, "/pitr/backup", `{}`},
		{http.MethodPost, "/pitr/restore-points", `{"name":"before-migration"}`},
	}
	pitrSystemAdmin = []pitrRoute{
		{http.MethodPut, "/pitr", `{"schedule":"0 4 * * *"}`},
		{http.MethodPost, "/pitr/restore", `{"target":{"kind":"latest"}}`},
	}
	pitrDestructive = []pitrRoute{
		{http.MethodPost, "/pitr", `{"targetKind":"local","target":{"path":"/tmp"}}`},
		{http.MethodDelete, "/pitr", `{}`},
		{http.MethodPost, "/pitr/restores/1/switch", `{}`},
		{http.MethodDelete, "/pitr/archives/1", `{}`},
	}
)

func pitrDo(router http.Handler, rt pitrRoute) *httptest.ResponseRecorder {
	req := httptest.NewRequest(rt.method, "/databases/1"+rt.path, strings.NewReader(rt.body))
	if rt.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPITRRoutesAskForTheirCapability(t *testing.T) {
	check := func(role auth.Role, routes []pitrRoute, allowed bool) {
		t.Helper()
		_, router := pitrRouterAs(t, role)
		for _, rt := range routes {
			rec := pitrDo(router, rt)
			refused := rec.Code == http.StatusForbidden && mongoErrorCode(rec) == "forbidden"
			if allowed && refused {
				t.Errorf("as %s: %s %s was refused: %s", role, rt.method, rt.path, strings.TrimSpace(rec.Body.String()))
			}
			if !allowed && !refused {
				t.Errorf("as %s: %s %s was not refused: %d %s", role, rt.method, rt.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		}
	}
	check(auth.RoleReadOnly, pitrReadSurface, true)
	check(auth.RoleReadOnly, pitrServiceControl, false)
	check(auth.RoleReadOnly, pitrSystemAdmin, false)
	check(auth.RoleReadOnly, pitrDestructive, false)
	check(auth.RoleLimited, pitrServiceControl, true)
	check(auth.RoleLimited, pitrSystemAdmin, false)
	check(auth.RoleLimited, pitrDestructive, false)
	check(auth.RoleAdmin, pitrSystemAdmin, true)
	check(auth.RoleAdmin, pitrDestructive, true)
}

// The lists above are every point-in-time recovery route; one mounted and in
// none of them has had no capability decided for it.
func TestEveryPITRRouteIsInACapabilityList(t *testing.T) {
	listed := map[string]bool{}
	for _, list := range [][]pitrRoute{pitrReadSurface, pitrServiceControl, pitrSystemAdmin, pitrDestructive} {
		for _, rt := range list {
			path := strings.NewReplacer("/restores/1/", "/restores/{rid}/", "/archives/1", "/archives/{aid}").Replace(rt.path)
			listed[rt.method+" "+path] = true
		}
	}
	_, router := pitrRouterAs(t, auth.RoleAdmin)
	found := 0
	err := chi.Walk(router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		path, ok := strings.CutPrefix(route, "/databases/{id}")
		if !ok || !strings.HasPrefix(path, "/pitr") {
			return nil
		}
		found++
		if !listed[method+" "+path] {
			t.Errorf("%s %s is mounted and is in no capability list", method, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(listed) {
		t.Errorf("%d point-in-time recovery routes are mounted and %d are listed", found, len(listed))
	}
}

// Restarting, switching and deleting an archive take the ordinary
// confirmation in the page; none is one of the deletions that take a phrase.
func TestPITRRoutesDoNotAskForAPhrase(t *testing.T) {
	_, router := pitrRouterAs(t, auth.RoleAdmin)
	for _, list := range [][]pitrRoute{pitrServiceControl, pitrSystemAdmin, pitrDestructive} {
		for _, rt := range list {
			rec := pitrDo(router, rt)
			if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
				t.Errorf("%s %s asked for a phrase: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
			}
		}
	}
}

// A server this dashboard does not run in a container is read, with the
// reason, and every change to it is refused before anything is touched.
func TestPITRRefusesAServerItCannotArchive(t *testing.T) {
	s, router := pitrRouterAs(t, auth.RoleAdmin)
	rec := pitrDo(router, pitrRoute{http.MethodGet, "/pitr", ""})
	var reading dbPITR
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reading) != nil {
		t.Fatalf("GET /pitr: %d %s", rec.Code, rec.Body.String())
	}
	if reading.Available || !strings.Contains(reading.Reason, "Docker") || reading.Archive != nil ||
		reading.Earlier == nil || reading.Restores == nil || reading.RestorePoints == nil {
		t.Errorf("a server elsewhere read as %+v", reading)
	}
	if reading.Defaults.RetentionDays != pitrDefaultDays || reading.Defaults.Schedule != pitrDefaultSchedule ||
		!strings.HasSuffix(reading.Defaults.Directory, "/wal") {
		t.Errorf("setup defaults %+v", reading.Defaults)
	}
	for _, rt := range append(append([]pitrRoute{}, pitrServiceControl...), append(pitrSystemAdmin, pitrDestructive...)...) {
		rec := pitrDo(router, rt)
		if rec.Code != http.StatusConflict && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s on a server elsewhere: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
	// A refused request is still recorded, as a failure.
	var status, success int
	if err := s.Store.DB.QueryRow(`SELECT status, success FROM audit_log WHERE method = 'POST' AND path = '/databases/1/pitr'
		ORDER BY id DESC LIMIT 1`).Scan(&status, &success); err != nil || status != http.StatusConflict || success != 0 {
		t.Errorf("a refused setup was audited with %d, success %d (%v)", status, success, err)
	}

	// Validation that needs no server answers first.
	for _, rt := range []pitrRoute{
		{http.MethodPost, "/pitr/restore-points", `{"name":"two words"}`},
		{http.MethodPost, "/pitr/restore", `{"target":{"kind":"time"}}`},
		{http.MethodPost, "/pitr/restore", `{"target":{"kind":"tomorrow"}}`},
		{http.MethodPost, "/pitr/restores/x/switch", `{}`},
	} {
		if rec := pitrDo(router, rt); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d %s, want 400", rt.method, rt.path, rt.body, rec.Code, rec.Body.String())
		}
	}

	// A SQLite file has no write-ahead log to archive.
	sealed, _ := s.Sealer.Seal(s.Cfg.FileRoots[0] + "/app.db")
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"file", string(dbx.DriverSQLite), sealed, 0); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/databases/2/pitr", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "not a PostgreSQL server") {
		t.Errorf("a SQLite connection read as %s", rec.Body.String())
	}
}

func TestPITRScheduleAndRetentionAreHeldToWhatCanRun(t *testing.T) {
	never := "every full moon"
	if _, _, err := readPITRSchedule(&never, nil, "", 7); err == nil {
		t.Error("a schedule cron cannot read was accepted")
	}
	manual := ""
	if schedule, days, err := readPITRSchedule(&manual, nil, pitrDefaultSchedule, 7); err != nil || schedule != "" || days != 7 {
		t.Errorf("an empty schedule is by hand only: %q %d %v", schedule, days, err)
	}
	if schedule, days, err := readPITRSchedule(nil, nil, pitrDefaultSchedule, 7); err != nil || schedule != pitrDefaultSchedule || days != 7 {
		t.Errorf("nothing sent keeps what is there: %q %d %v", schedule, days, err)
	}
	for _, days := range []int{-1, 3651} {
		if _, _, err := readPITRSchedule(nil, &days, "", 7); err == nil {
			t.Errorf("%d days was accepted", days)
		}
	}
}

func TestTheServersSpecsCarryOnlyWhatPITRMeans(t *testing.T) {
	archive := &pitrArchive{Container: "jd-postgres", Stream: "jd-postgres-20261017-000000", TargetKind: backups.TargetLocal,
		Target: backups.TargetConfig{Path: "/var/backups/just-dashboard/wal"}}
	server := &pitrServer{
		name: "jd-postgres", image: "postgres:17-alpine", account: dbx.WALGAccount{User: "jd", Port: 5432},
		spec: &dockerx.ContainerSpec{
			Name: "jd-postgres", Image: "postgres:17-alpine", Command: []string{"postgres", "-c", "max_connections=200"},
			Ports:    []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: 5433, ContainerPort: 5432, Protocol: "tcp"}},
			Mounts:   []dockerx.MountSpec{{Type: "volume", Source: "jd-postgres-data", Target: dbx.PostgresDataDir}},
			Networks: []string{"jd-env-3"}, Hostname: "abc",
			Labels: []dockerx.LabelSpec{
				{Name: "io.just-dashboard.environment-id", Value: "3"}, {Name: "owner", Value: "shop"},
			},
			RestartPolicy: "unless-stopped", Limits: dockerx.ResourceLimits{MemoryMB: 512},
		},
	}
	archiving := pitrArchiveSpec(server.spec, archive, "replica")
	if !dbx.ArchivesWithWALG(archiving.Command) || archiving.Command[2] != "max_connections=200" {
		t.Errorf("archiving command %q", archiving.Command)
	}
	if len(archiving.Mounts) != 3 || archiving.Mounts[1].Source != archive.Stream+"-pitr" || !archiving.Mounts[1].ReadOnly ||
		archiving.Mounts[2].Source != "/var/backups/just-dashboard/wal/"+archive.Stream || archiving.Mounts[2].ReadOnly {
		t.Errorf("archiving mounts %+v", archiving.Mounts)
	}
	if len(server.spec.Mounts) != 1 {
		t.Error("the server's own spec was changed in place")
	}
	plain := pitrPlainSpec(archiving)
	if dbx.ArchivesWithWALG(plain.Command) || len(plain.Mounts) != 1 || plain.Mounts[0].Target != dbx.PostgresDataDir {
		t.Errorf("turned off, the server is %q %+v", plain.Command, plain.Mounts)
	}

	copySpec := pitrCopySpec(server, archive, "jd-postgres-restored-x", "jd-postgres-restored-x-pitr-restore", 5434,
		dbx.RecoveryTarget{Kind: "name", Name: "before"})
	if copySpec.Name != "jd-postgres-restored-x" || len(copySpec.Ports) != 1 || copySpec.Ports[0].HostPort != 5434 ||
		copySpec.Ports[0].HostIP != "127.0.0.1" || copySpec.Networks != nil || copySpec.Hostname != "" ||
		copySpec.RestartPolicy != "no" || copySpec.Start || copySpec.Limits.MemoryMB != 512 {
		t.Errorf("copy spec %+v", copySpec)
	}
	for _, label := range copySpec.Labels {
		if strings.HasPrefix(label.Name, "io.just-dashboard.environment") {
			t.Errorf("the copy carries the deployment's label %s", label.Name)
		}
	}
	if copySpec.Mounts[0].Source != "jd-postgres-restored-x-data" || !copySpec.Mounts[2].ReadOnly {
		t.Errorf("copy mounts %+v", copySpec.Mounts)
	}
	settings := dbx.ReadPostgresCommandSettings(copySpec.Command)
	if settings["recovery_target_name"] != "before" || settings["archive_mode"] != "off" || settings["max_connections"] != "200" {
		t.Errorf("copy command %q", copySpec.Command)
	}

	if name := pitrCopyName(strings.Repeat("a", 60), time.Now()); len(name) != 63 || !containerNameRe.MatchString(name) {
		t.Errorf("a long server's copy is named %q", name)
	}
	if got := pitrLocation(&pitrArchive{TargetKind: backups.TargetS3, Stream: "s", Target: backups.TargetConfig{Bucket: "b/", Prefix: "/p/"}}); got != "s3://b/p/s" {
		t.Errorf("S3 location %q", got)
	}
}

// What the store keeps of an archive: the destination's keys and the private
// key sealed, the public key and fingerprint readable, and an ended archive
// kept apart from the one a server writes.
func TestPITRArchiveRecordsSealWhatIsSecret(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	public, private, err := dbx.NewWALKey("jd-postgres")
	if err != nil {
		t.Fatal(err)
	}
	a := &pitrArchive{
		Container: "jd-postgres", Stream: "jd-postgres-1", TargetKind: backups.TargetS3,
		Target: backups.TargetConfig{Bucket: "b"}, Schedule: pitrDefaultSchedule, RetentionDays: 7, publicKey: public,
	}
	if err := s.sealPITRArchive(a, &backups.TargetSecrets{AccessKeyID: "AKIA", SecretAccessKey: "very-secret"}, private); err != nil {
		t.Fatal(err)
	}
	if a.ID, err = s.insertPITRArchive(ctx, a); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.Store.DB.QueryRow(`SELECT secrets_enc || private_key_enc FROM db_wal_archives WHERE id = ?`, a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "very-secret") || strings.Contains(raw, "PRIVATE KEY") {
		t.Fatal("a secret is stored in the clear")
	}
	stored, err := s.pitrArchiveByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.HasCredentials || stored.KeyFingerprint == "" || stored.KeyFingerprint != dbx.WALKeyFingerprint(public) {
		t.Errorf("stored archive %+v", stored)
	}
	encoded, _ := json.Marshal(stored)
	if strings.Contains(string(encoded), "very-secret") || strings.Contains(string(encoded), "PGP") {
		t.Errorf("the archive's JSON carries a key: %s", encoded)
	}
	secrets, err := s.pitrSecrets(stored)
	if err != nil || secrets.SecretAccessKey != "very-secret" {
		t.Errorf("secrets open as %+v %v", secrets, err)
	}
	if key, err := s.pitrPrivateKey(stored); err != nil || key != private {
		t.Errorf("the private key does not open: %v", err)
	}

	reading := &pitrReading{Backups: []dbx.WALBackup{{Name: "base_1", FinishedAt: time.Now().UTC()}}, ReadAt: time.Now().UTC()}
	if err := s.recordPITRReading(ctx, a.ID, reading); err != nil {
		t.Fatal(err)
	}
	if err := s.recordPITRBackup(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.insertPITRRestorePoint(ctx, a.ID, "before", "0/3000000", "tester"); err != nil {
		t.Fatal(err)
	}
	if err := s.endPITRArchive(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := s.pitrArchivesOf(ctx, "jd-postgres")
	if err != nil || len(listed) != 1 || listed[0].EndedAt == nil || listed[0].Reading == nil ||
		listed[0].Reading.Backups[0].Name != "base_1" || listed[0].LastBackupAt == nil {
		t.Fatalf("listed %+v %v", listed, err)
	}
	if active, _ := s.activePITRArchives(ctx); len(active) != 0 {
		t.Error("an ended archive is still scheduled")
	}
	points, err := s.pitrRestorePoints(ctx, a.ID)
	if err != nil || len(points) != 1 || points[0].Name != "before" || points[0].LSN != "0/3000000" {
		t.Errorf("restore points %+v %v", points, err)
	}
	restore := &pitrRestore{ArchiveID: a.ID, Source: "jd-postgres", Container: "jd-postgres-restored-1", Port: 5434,
		Target: dbx.RecoveryTarget{Kind: "latest"}, BaseBackup: "base_1"}
	if restore.ID, err = s.insertPITRRestore(ctx, restore); err != nil {
		t.Fatal(err)
	}
	if err := s.markPITRRestoreSwitched(ctx, restore.ID, "jd-postgres", "jd-postgres-replaced-1"); err != nil {
		t.Fatal(err)
	}
	restores, err := s.pitrRestoresOf(ctx, "jd-postgres")
	if err != nil || len(restores) != 1 || restores[0].State != "switched" || restores[0].Replaced != "jd-postgres-replaced-1" ||
		restores[0].SwitchedAt == nil || restores[0].Target.Kind != "latest" {
		t.Errorf("restores %+v %v", restores, err)
	}
	if err := s.deletePITRArchive(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.pitrRestoresOf(ctx, "jd-postgres"); len(left) != 0 {
		t.Error("an archive's copies outlived it")
	}
}
