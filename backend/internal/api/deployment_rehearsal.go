package api

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

// migrationRehearser is the Databases owner's side of a migration rehearsal
// (deploy/rehearsal.go): the throwaway copy of a linked database, its lock
// table read while a release task runs against it, and its removal.
//
// The copy is the production server's own container configuration — image,
// settings, accounts, the certificates it serves TLS with — on a fresh volume
// and on a network of its own that Docker gives no route out of, answering by
// the production database's own names there. It is restored from the
// point-in-time archive when the server has one, otherwise from the newest
// native dump a backup job took of the database. Nothing is published, and
// the dashboard reaches the copy only through `docker exec`, so the copy has
// no address anything else on the host could use.
type migrationRehearser struct {
	server *Server
}

const rehearsalLabel = "io.just-dashboard.rehearsal"

func rehearsalUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", deploy.ErrRehearsalUnavailable, fmt.Sprintf(format, args...))
}

func rehearsalNames(run deploy.EngineRun) (network, container string) {
	base := fmt.Sprintf("jd-e%d-run%d-rehearsal", run.EnvironmentID, run.ID)
	return base, base + "-db"
}

func rehearsalLabels(run deploy.EngineRun) map[string]string {
	return map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(run.EnvironmentID, 10),
		"io.just-dashboard.run-id":         strconv.FormatInt(run.ID, 10),
		rehearsalLabel:                     strconv.FormatInt(run.ID, 10),
	}
}

// rehearsalEngine is how a copy of one engine is started, restored and read.
type rehearsalEngine struct {
	name    string
	dataDir string
	// account is the superuser the copy is restored as, read from the
	// server's own environment.
	user, password string
}

func rehearsalEngineFor(conn *dbConnection, spec *dockerx.ContainerSpec) (rehearsalEngine, error) {
	env := map[string]string{}
	for _, e := range spec.Env {
		env[e.Name] = e.Value
	}
	switch conn.Driver {
	case dbx.DriverPostgres:
		engine := rehearsalEngine{name: "postgres", dataDir: dbx.PostgresDataDir, user: "postgres", password: env["POSTGRES_PASSWORD"]}
		if env["POSTGRES_USER"] != "" {
			engine.user = env["POSTGRES_USER"]
		}
		if env["PGDATA"] != "" {
			engine.dataDir = env["PGDATA"]
		}
		return engine, nil
	case dbx.DriverMySQL:
		name := "mysql"
		if strings.Contains(strings.ToLower(spec.Image), "mariadb") {
			name = "mariadb"
		}
		password := env["MYSQL_ROOT_PASSWORD"]
		if password == "" {
			password = env["MARIADB_ROOT_PASSWORD"]
		}
		return rehearsalEngine{name: name, dataDir: "/var/lib/mysql", user: "root", password: password}, nil
	}
	return rehearsalEngine{}, rehearsalUnavailable("a copy is restored for PostgreSQL, MySQL and MariaDB databases, and %s is %s", conn.Name, conn.Driver)
}

func (m *migrationRehearser) PrepareRehearsalCopy(ctx context.Context, request deploy.RehearsalCopyRequest, emit func(deploy.BuildLog) error) (*deploy.RehearsalCopy, error) {
	networkName, containerName := rehearsalNames(request.Run)
	made, err := m.prepareCopy(ctx, copyPlan{
		connectionID: request.ConnectionID, container: containerName, network: networkName, ownNetwork: true,
		// The names the production database answers by on the environment's
		// network: every variable that reaches it reaches the copy instead.
		aliases: []string{databaseDNSName(request.ConnectionID), poolerDNSName(request.ConnectionID)},
		labels:  rehearsalLabels(request.Run), maxBytes: int64(request.Policy.MaxBackupMB) << 20, latest: true,
	}, emit)
	if err != nil {
		return nil, err
	}
	return &made.copy, nil
}

// copyPlan is what a copy of a managed database is made with: where it runs
// and by which names, what it is labelled with, and the backup it is
// restored from — the newest there is, for a rehearsal, or a point recorded
// earlier, for a copy of the data as of a release.
type copyPlan struct {
	connectionID int64
	container    string
	network      string
	// ownNetwork makes the network, Internal and for the copy alone;
	// otherwise the copy joins one that exists (a deployment's).
	ownNetwork bool
	aliases    []string
	labels     map[string]string
	maxBytes   int64
	// latest restores the newest backup; otherwise dumpRunID names the
	// backup run whose dump it is, or archiveID the archive recovered to at.
	latest    bool
	dumpRunID int64
	archiveID int64
	at        time.Time
}

// madeCopy is a restored copy with what it was made from.
type madeCopy struct {
	copy   deploy.RehearsalCopy
	conn   *dbConnection
	dsn    string
	info   *dbx.ConnInfo
	engine rehearsalEngine
	// recoveredTo is the moment a point-in-time recovery reached.
	recoveredTo time.Time
}

func (m *migrationRehearser) prepareCopy(ctx context.Context, plan copyPlan, emit func(deploy.BuildLog) error) (*madeCopy, error) {
	s := m.server
	docker := s.modules.docker
	if docker == nil {
		return nil, rehearsalUnavailable("this host has no Docker socket to start a copy with")
	}
	status := func(format string, args ...any) {
		_ = emit(deploy.BuildLog{Stream: "status", Text: fmt.Sprintf(format, args...)})
	}
	conn, dsn, err := s.dbConnRow(ctx, plan.connectionID)
	if err != nil {
		return nil, rehearsalUnavailable("database connection %d could not be opened", plan.connectionID)
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return nil, rehearsalUnavailable("%s's address could not be read", conn.Name)
	}
	detail, _, err := s.databaseContainer(ctx, conn, info)
	if err != nil {
		return nil, rehearsalUnavailable("%s is not a database this dashboard runs in a container, so there is no server configuration to copy", conn.Name)
	}
	spec, err := docker.SpecOf(ctx, detail.ID)
	if err != nil {
		return nil, rehearsalUnavailable("%s's container could not be read", conn.Name)
	}
	engine, err := rehearsalEngineFor(conn, spec)
	if err != nil {
		return nil, err
	}
	made := &madeCopy{
		conn: conn, dsn: dsn, info: info, engine: engine,
		copy: deploy.RehearsalCopy{
			Network: plan.network, Container: plan.container,
			Database: deploy.RehearsalTarget{ConnectionID: conn.ID, Name: conn.Name, Engine: engine.name, Database: info.Database, Image: spec.Image},
		},
	}
	started := time.Now()
	if plan.ownNetwork {
		// Internal: Docker gives the network no gateway out, so a task that
		// somehow named a production address could not reach it.
		if _, err := docker.CreateNetwork(ctx, dockerx.NetworkSpec{Name: plan.network, Driver: "bridge", Internal: true, Labels: plan.labels}); err != nil {
			return nil, rehearsalUnavailable("the copy's network could not be made: %v", err)
		}
	}

	var source deploy.RehearsalSource
	if engine.name == "postgres" && (plan.latest || plan.archiveID > 0) {
		srv := s.pitrServerOf(ctx, conn, dsn)
		var archive *pitrArchive
		target := dbx.RecoveryTarget{Kind: "latest"}
		switch {
		case plan.latest && srv.reason == "":
			archive = s.activeArchiveOf(ctx, srv.name)
		case plan.archiveID > 0:
			if srv.spec == nil {
				return nil, rehearsalUnavailable("%s's server could not be read to recover a copy of it: %s", conn.Name, srv.reason)
			}
			if archive, err = s.pitrArchiveByID(ctx, plan.archiveID); err != nil {
				return nil, rehearsalUnavailable("the point-in-time archive %s was recorded in is no longer kept", conn.Name)
			}
			target = dbx.RecoveryTarget{Kind: "time", Time: plan.at}
		}
		if archive != nil {
			if target.Kind == "latest" {
				status("Recovering %s from its point-in-time archive", conn.Name)
			} else {
				status("Recovering %s from its point-in-time archive to %s", conn.Name, plan.at.UTC().Format(time.RFC3339))
			}
			source, made.recoveredTo, err = m.recoverPITRCopy(ctx, srv, archive, target, plan.container, plan.network, plan.aliases, plan.labels, plan.maxBytes, status)
			if err != nil {
				return nil, rehearsalUnavailable("%s could not be recovered from its point-in-time archive: %v", conn.Name, err)
			}
			made.copy.Source, made.copy.RestoreMS = source, time.Since(started).Milliseconds()
			return made, nil
		}
	}

	var runID int64
	var takenAt time.Time
	var dump backups.DatabaseDump
	if plan.dumpRunID > 0 {
		run, err := s.modules.backupStore.Run(ctx, plan.dumpRunID)
		if err != nil || run.Status != backups.StatusSuccess || !run.Manifest.CoversDatabase(conn.ID) {
			return nil, rehearsalUnavailable("backup run %d, which held %s's dump, is no longer a successful backup holding it", plan.dumpRunID, conn.Name)
		}
		for _, entry := range run.Manifest.DatabaseDumps {
			if entry.ConnectionID == conn.ID {
				dump = entry
			}
		}
		runID, takenAt = run.ID, run.StartedAt.UTC()
	} else {
		runID, takenAt, dump, err = s.modules.backupStore.LatestDatabaseDump(ctx, conn.ID)
		if errors.Is(err, backups.ErrNoDatabaseDump) {
			return nil, rehearsalUnavailable("no backup holds %s to restore a copy from: add it to a backup job's database dumps, or turn on point-in-time recovery", conn.Name)
		}
		if err != nil {
			return nil, rehearsalUnavailable("the backups of %s could not be read", conn.Name)
		}
	}
	if dump.Bytes > plan.maxBytes {
		return nil, rehearsalUnavailable("the dump of %s is %d MB, over the %d MB a copy is restored from", conn.Name, dump.Bytes>>20, plan.maxBytes>>20)
	}
	source = deploy.RehearsalSource{Kind: "dump", BackupRunID: runID, TakenAt: &takenAt, Bytes: dump.Bytes, Method: dump.Method}
	status("Restoring %s from the backup of %s (%s, %d KB)", conn.Name, takenAt.Format("2006-01-02 15:04 UTC"), dump.Method, dump.Bytes>>10)

	dataVolume := plan.container + "-data"
	if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: dataVolume, Labels: plan.labels}); err != nil {
		return nil, rehearsalUnavailable("the copy's volume could not be made: %v", err)
	}
	copySpec := rehearsalCopySpec(spec, engine, plan.container, plan.network, dataVolume, plan.aliases, plan.labels)
	if _, err := docker.Create(ctx, copySpec, nil); err != nil {
		return nil, rehearsalUnavailable("the copy's container could not be started: %v", err)
	}
	if err := m.waitForCopy(ctx, plan.container, engine); err != nil {
		return nil, rehearsalUnavailable("the copy did not start: %v", err)
	}
	if engine.name == "postgres" && info.User != "" && info.User != engine.user {
		// A dump holds one database, not the accounts: the one the
		// application signs in as is made with the password it signs in with.
		if err := m.execSQL(ctx, plan.container, engine, info.Database, fmt.Sprintf(
			`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN CREATE ROLE %s LOGIN PASSWORD %s; END IF; END $$; GRANT ALL ON DATABASE %s TO %s; ALTER DATABASE %s OWNER TO %s`,
			sqlLiteral(info.User), sqlIdentifier(info.User), sqlLiteral(info.Password), sqlIdentifier(info.Database),
			sqlIdentifier(info.User), sqlIdentifier(info.Database), sqlIdentifier(info.User))); err != nil {
			return nil, rehearsalUnavailable("the application's account could not be made in the copy: %v", err)
		}
	}
	staging, err := os.MkdirTemp(filepath.Join(s.Cfg.DataDir, "staging"), "rehearsal-*")
	if err != nil {
		if mkErr := os.MkdirAll(filepath.Join(s.Cfg.DataDir, "staging"), 0o700); mkErr == nil {
			staging, err = os.MkdirTemp(filepath.Join(s.Cfg.DataDir, "staging"), "rehearsal-*")
		}
		if err != nil {
			return nil, rehearsalUnavailable("no room to stage the dump: %v", err)
		}
	}
	defer os.RemoveAll(staging)
	path, _, err := s.modules.backupRunner.ExtractDatabaseDump(ctx, runID, conn.ID, staging)
	if err != nil {
		return nil, rehearsalUnavailable("the dump could not be read out of backup run %d: %v", runID, err)
	}
	if err := m.restoreDump(ctx, plan.container, engine, info, path, emit); err != nil {
		return nil, rehearsalUnavailable("the dump could not be restored into the copy: %v", err)
	}
	made.copy.Source, made.copy.RestoreMS = source, time.Since(started).Milliseconds()
	return made, nil
}

// rehearsalCopySpec is the production server's container as a copy: its
// image, command, environment and every mount but its data, which is a
// fresh volume; none of its ports, labels or networks.
func rehearsalCopySpec(server *dockerx.ContainerSpec, engine rehearsalEngine, name, network, dataVolume string, aliases []string, labels map[string]string) dockerx.ContainerSpec {
	spec := *server
	spec.Name, spec.Hostname = name, ""
	spec.Ports, spec.NetworkMode = nil, ""
	spec.Networks, spec.NetworkAliases = []string{network}, aliases
	spec.Labels = nil
	for key, value := range labels {
		spec.Labels = append(spec.Labels, dockerx.LabelSpec{Name: key, Value: value})
	}
	spec.Mounts = copyMounts(server.Mounts, engine.dataDir, dataVolume)
	if engine.name == "postgres" && dbx.IsPostgresServerCommand(spec.Command) {
		spec.Command = copyCommand(spec.Command)
	}
	spec.RestartPolicy, spec.MaxRetries, spec.Start, spec.Pull = "no", 0, true, "missing"
	return spec
}

// copyMounts are a production server's mounts as its copy's: the data
// directory on the copy's own volume, the read-only ones — its
// configuration, its certificates — as they are, and none of the writable
// rest. A writable mount is somewhere production writes (a socket directory,
// a log, the point-in-time archive and its key), and a copy must not write
// there beside it.
func copyMounts(mounts []dockerx.MountSpec, dataDir, dataVolume string) []dockerx.MountSpec {
	out := []dockerx.MountSpec{}
	replaced := false
	for _, mount := range withPITRMounts(mounts, nil) {
		switch {
		case mount.Target == dataDir || strings.HasPrefix(dataDir, strings.TrimSuffix(mount.Target, "/")+"/"):
			out = append(out, dockerx.MountSpec{Type: "volume", Source: dataVolume, Target: mount.Target})
			replaced = true
		case mount.ReadOnly || mount.Type == "tmpfs":
			out = append(out, mount)
		}
	}
	if !replaced {
		out = append(out, dockerx.MountSpec{Type: "volume", Source: dataVolume, Target: dataDir})
	}
	return out
}

// copyCommand is a PostgreSQL server's command line with its archiving
// turned off: a copy that archived would write into production's archive.
// Given on the command line, it outranks anything the data directory says.
func copyCommand(command []string) []string {
	return dbx.PostgresCommandSettings(dbx.WithoutWALSettings(command), map[string]string{"archive_mode": "off"})
}

// waitForCopy waits until the copy answers on its own TCP port, which the
// images' entrypoints open only once initialisation is over.
func (m *migrationRehearser) waitForCopy(ctx context.Context, container string, engine rehearsalEngine) error {
	check := []string{"pg_isready", "-h", "127.0.0.1", "-U", engine.user}
	if engine.name != "postgres" {
		check = []string{"sh", "-c", `mysqladmin ping -h 127.0.0.1 -uroot --password="$REHEARSAL_PASSWORD" --silent || mariadb-admin ping -h 127.0.0.1 -uroot --password="$REHEARSAL_PASSWORD" --silent`}
	}
	last := ""
	for {
		code, err := m.server.modules.docker.ExecStream(ctx, container, check, "", []string{"REHEARSAL_PASSWORD=" + engine.password}, func(line dockerx.LogLine) { last = line.Text })
		if err == nil && code == 0 {
			return nil
		}
		if detail, inspectErr := m.server.modules.docker.Inspect(ctx, container); inspectErr == nil && detail.State != "running" {
			return fmt.Errorf("the copy's server stopped (%s)", detail.State)
		}
		select {
		case <-ctx.Done():
			if last != "" {
				return fmt.Errorf("%w: %s", ctx.Err(), last)
			}
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// restoreDump copies the dump into the copy and replays it there with the
// server's own tools, so the restore never depends on a client of another
// release in the dashboard's image.
func (m *migrationRehearser) restoreDump(ctx context.Context, container string, engine rehearsalEngine, info *dbx.ConnInfo, path string, emit func(deploy.BuildLog) error) error {
	kind := dbx.DumpKind(path)
	target := "/tmp/rehearsal-dump"
	if err := copyFileInto(ctx, m.server.modules.docker, container, path, "/tmp", "rehearsal-dump"); err != nil {
		return err
	}
	var command []string
	switch {
	case engine.name == "postgres" && kind == "pg_dump archive":
		args := []string{"pg_restore", "--no-owner", "--no-privileges", "--exit-on-error", "-U", engine.user, "-d", info.Database}
		if info.User != "" && info.User != engine.user {
			args = append(args, "--role="+info.User)
		}
		command = append(args, target)
	case engine.name == "postgres" && kind == "SQL":
		// The rows a dump's SELECTs return (setval's) are not the run's to
		// read; errors still are.
		command = []string{"psql", "-v", "ON_ERROR_STOP=1", "-q", "-o", "/dev/null", "-U", engine.user, "-d", info.Database, "-f", target}
	case engine.name == "postgres" && kind == "compressed SQL":
		command = []string{"sh", "-c", `gunzip -c "$1" | psql -v ON_ERROR_STOP=1 -q -o /dev/null -U "$2" -d "$3"`, "sh", target, engine.user, info.Database}
	case engine.name != "postgres" && (kind == "SQL" || kind == "compressed SQL"):
		reader := `cat "$1"`
		if kind == "compressed SQL" {
			reader = `gunzip -c "$1"`
		}
		// MariaDB's images from 11 on carry only the mariadb client, MySQL's
		// only mysql.
		command = []string{"sh", "-c", `client=mysql; command -v mysql >/dev/null 2>&1 || client=mariadb; ` +
			reader + ` | "$client" -uroot --password="$REHEARSAL_PASSWORD" "$2" 2>&1`, "sh", target, info.Database}
	default:
		return fmt.Errorf("a %s dump of %s is not one a rehearsal can replay", kind, engine.name)
	}
	var lastLine string
	// A dump's DROP … IF EXISTS on an empty copy is a NOTICE per table:
	// warnings and errors are what the transcript keeps.
	env := []string{"REHEARSAL_PASSWORD=" + engine.password, "PGOPTIONS=-c client_min_messages=warning"}
	code, err := m.server.modules.docker.ExecStream(ctx, container, command, "", env,
		func(line dockerx.LogLine) {
			lastLine = line.Text
			_ = emit(deploy.BuildLog{Stream: line.Stream, Text: line.Text})
		})
	if err != nil {
		return err
	}
	if code != 0 {
		if lastLine == "" {
			lastLine = fmt.Sprintf("the restore exited with code %d", code)
		}
		return errors.New(lastLine)
	}
	return nil
}

func (m *migrationRehearser) execSQL(ctx context.Context, container string, engine rehearsalEngine, database, statement string) error {
	var last string
	code, err := m.server.modules.docker.ExecStream(ctx, container,
		[]string{"psql", "-v", "ON_ERROR_STOP=1", "-q", "-U", engine.user, "-d", database, "-c", statement}, "", nil,
		func(line dockerx.LogLine) { last = line.Text })
	if err != nil {
		return err
	}
	if code != 0 {
		return errors.New(last)
	}
	return nil
}

// rehearsalLockQuery reads the sessions waiting for a lock, and the locks
// held that would stop an application's reads or writes of a table — the
// ACCESS EXCLUSIVE an ALTER TABLE takes, the SHARE a CREATE INDEX without
// CONCURRENTLY takes — leaving out the catalogue and the reading session.
const rehearsalLockQuery = `SELECT CASE WHEN l.granted THEN 'held' ELSE 'wait' END, coalesce(c.relname, l.locktype), l.mode
  FROM pg_locks l LEFT JOIN pg_class c ON c.oid = l.relation
 WHERE l.pid <> pg_backend_pid()
   AND (NOT l.granted OR (l.mode IN ('AccessExclusiveLock', 'ExclusiveLock', 'ShareRowExclusiveLock', 'ShareLock')
        AND c.relnamespace IS NOT NULL AND c.relnamespace <> 'pg_catalog'::regnamespace AND c.relkind IN ('r', 'p', 'm', 'i')))`

func (m *migrationRehearser) SampleRehearsalLocks(ctx context.Context, prepared deploy.RehearsalCopy) ([]deploy.RehearsalLockSample, bool, error) {
	if prepared.Database.Engine != "postgres" {
		return nil, false, nil
	}
	detail, err := m.server.modules.docker.Inspect(ctx, prepared.Container)
	if err != nil {
		return nil, true, err
	}
	spec, err := m.server.modules.docker.SpecOf(ctx, detail.ID)
	if err != nil {
		return nil, true, err
	}
	user := "postgres"
	for _, e := range spec.Env {
		if e.Name == "POSTGRES_USER" && e.Value != "" {
			user = e.Value
		}
	}
	samples := []deploy.RehearsalLockSample{}
	code, err := m.server.modules.docker.ExecStream(ctx, prepared.Container,
		[]string{"psql", "-At", "-F", "|", "-U", user, "-d", prepared.Database.Database, "-c", rehearsalLockQuery}, "", nil,
		func(line dockerx.LogLine) {
			if line.Stream != "stdout" {
				return
			}
			parts := strings.Split(line.Text, "|")
			if len(parts) != 3 {
				return
			}
			samples = append(samples, deploy.RehearsalLockSample{Waiting: parts[0] == "wait", Relation: parts[1], Mode: parts[2]})
		})
	if err != nil {
		return nil, true, err
	}
	if code != 0 {
		return nil, true, fmt.Errorf("the lock table could not be read (exit %d)", code)
	}
	return samples, true, nil
}

// RemoveRehearsalCopy removes everything one run's rehearsal made: its task
// containers, the copy and its volumes, and the network, found by the label
// the run's id is in, whatever process made them.
func (m *migrationRehearser) RemoveRehearsalCopy(ctx context.Context, run deploy.EngineRun) error {
	return m.removeRehearsals(ctx, map[string]string{rehearsalLabel: strconv.FormatInt(run.ID, 10)}, nil)
}

// RemoveOrphanedRehearsals removes every rehearsal at start: the process
// that was running it is gone, and a resumed run makes its copy again. A
// copy of a release's data that is still within its lifetime is the one
// thing that carries the label and is left (deployment_data_rollback.go):
// it is nobody's step, and its owner removes it when its time is up.
func (m *migrationRehearser) RemoveOrphanedRehearsals(ctx context.Context) error {
	if m.server.modules.docker == nil {
		return nil
	}
	keep := m.server.liveDataCopies(ctx)
	for value := range m.server.livePreviewBranches(ctx) {
		keep[value] = true
	}
	err := m.removeRehearsals(ctx, nil, keep)
	if errors.Is(err, dockerx.ErrUnavailable) {
		return nil
	}
	return err
}

// removeRehearsals removes the containers, volumes and networks carrying the
// rehearsal label — those whose value match names, or all of them — except
// the values keep holds.
func (m *migrationRehearser) removeRehearsals(ctx context.Context, match map[string]string, keep map[string]bool) error {
	docker := m.server.modules.docker
	if docker == nil {
		return nil
	}
	filter := map[string]string{"io.just-dashboard.managed": "true"}
	for key, value := range match {
		filter[key] = value
	}
	failures := []error{}
	containers, err := docker.ListContainersWithLabels(ctx, filter)
	if err != nil {
		return err
	}
	for _, item := range containers {
		if item.Labels[rehearsalLabel] == "" || keep[item.Labels[rehearsalLabel]] {
			continue
		}
		if err := docker.RemoveContainer(ctx, item.ID, true, true); err != nil && !errdefs.IsNotFound(err) {
			failures = append(failures, err)
		}
	}
	volumes, err := docker.ListVolumes(ctx)
	if err != nil {
		failures = append(failures, err)
	}
	for _, volume := range volumes {
		if volume.Labels[rehearsalLabel] == "" || keep[volume.Labels[rehearsalLabel]] || (match != nil && volume.Labels[rehearsalLabel] != match[rehearsalLabel]) {
			continue
		}
		if err := docker.RemoveVolume(ctx, volume.Name, true); err != nil && !errdefs.IsNotFound(err) {
			failures = append(failures, err)
		}
	}
	networks, err := docker.ListNetworks(ctx)
	if err != nil {
		failures = append(failures, err)
	}
	for _, network := range networks {
		if network.Labels[rehearsalLabel] == "" || keep[network.Labels[rehearsalLabel]] || (match != nil && network.Labels[rehearsalLabel] != match[rehearsalLabel]) {
			continue
		}
		if err := docker.RemoveNetwork(ctx, network.ID); err != nil && !errdefs.IsNotFound(err) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// activeArchiveOf is the point-in-time archive a server is writing now.
func (s *Server) activeArchiveOf(ctx context.Context, container string) *pitrArchive {
	archives, err := s.pitrArchivesOf(ctx, container)
	if err != nil {
		return nil
	}
	for _, archive := range archives {
		if archive.EndedAt == nil {
			return archive
		}
	}
	return nil
}

// recoverPITRCopy recovers the newest state the archive holds into the copy:
// WAL-G fetches the newest base backup into the copy's volume and the copy
// replays the log to its end, the way a recovery from the Databases page
// does, but on the rehearsal's network and with nothing saved. Once it has
// left recovery the copy is started again without the archive and its key,
// so a release task never runs beside them.
func (m *migrationRehearser) recoverPITRCopy(ctx context.Context, srv *pitrServer, archive *pitrArchive, target dbx.RecoveryTarget, name, network string, aliases []string, labels map[string]string, maxBytes int64, status func(string, ...any)) (deploy.RehearsalSource, time.Time, error) {
	s := m.server
	docker := s.modules.docker
	source := deploy.RehearsalSource{Kind: "pitr", Method: "WAL-G"}
	reading, err := s.readPITRArchive(ctx, srv, archive)
	if err != nil {
		return source, time.Time{}, err
	}
	base, err := s.pitrBaseFor(ctx, archive, reading, target)
	if err != nil {
		return source, time.Time{}, err
	}
	if !base.FinishedAt.IsZero() {
		at := base.FinishedAt.UTC()
		source.TakenAt = &at
	}
	// What the copy's data directory grows to, before the log replayed on
	// top of it.
	source.Bytes = base.DataSize
	if source.Bytes == 0 {
		source.Bytes = base.Size
	}
	if source.Bytes > maxBytes {
		return source, time.Time{}, fmt.Errorf("its base backup unpacks to %d MB, over the %d MB a copy is restored from", source.Bytes>>20, maxBytes>>20)
	}
	image := archive.Image
	if image == "" {
		image = srv.image
	}
	dataVolume, toolVolume := name+"-data", name+"-pitr"
	private, err := s.pitrPrivateKey(archive)
	if err != nil {
		return source, time.Time{}, err
	}
	cfg, err := s.pitrConfig(archive, private, srv.account)
	if err != nil {
		return source, time.Time{}, err
	}
	// Made here rather than by installWALG so that it carries the run's
	// label: the volume holds the archive's key, and the rehearsal's cleanup
	// finds what it removes by that label.
	toolLabels := pitrLabels(name, pitrRoleRestore)
	for key, value := range labels {
		toolLabels[key] = value
	}
	if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: toolVolume, Labels: toolLabels}); err != nil {
		return source, time.Time{}, err
	}
	if _, err := s.installWALG(ctx, toolVolume, pitrRoleRestore, image, name, cfg, true); err != nil {
		return source, time.Time{}, err
	}
	if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: dataVolume, Labels: labels}); err != nil {
		return source, time.Time{}, err
	}
	status("Fetching the base backup %s", base.Name)
	fetchMounts := append(pitrMounts(toolVolume, archive, true),
		dockerx.MountSpec{Type: "volume", Source: dataVolume, Target: dbx.PostgresDataDir})
	if _, err := s.runWALG(ctx, image, name, fetchMounts, []string{"backup-fetch", dbx.PostgresDataDir, base.Name}, nil); err != nil {
		return source, time.Time{}, err
	}
	spec := pitrCopySpec(srv, archive, name, toolVolume, 0, target)
	spec.Image = image
	spec.Ports = nil
	spec.Mounts = withPITRMounts(copyMounts(spec.Mounts, dbx.PostgresDataDir, dataVolume), pitrMounts(toolVolume, archive, true))
	spec.Networks, spec.NetworkAliases, spec.NetworkMode = []string{network}, aliases, ""
	for key, value := range labels {
		spec.Labels = append(spec.Labels, dockerx.LabelSpec{Name: key, Value: value})
	}
	res, err := docker.Create(ctx, *spec, nil)
	if err != nil {
		return source, time.Time{}, err
	}
	uid, gid, err := s.pitrAccount(ctx, res.ID)
	if err != nil {
		return source, time.Time{}, err
	}
	if err := docker.CopyArchive(ctx, res.ID, dbx.PostgresDataDir, recoverySignal(uid, gid)); err != nil {
		return source, time.Time{}, err
	}
	if target.Kind == "time" {
		status("Replaying the archive's log to %s", target.Time.UTC().Format(time.RFC3339))
	} else {
		status("Replaying the archive's log to its end")
	}
	created, recoveredTo, err := s.recoverCopy(ctx, res.ID, true, srv, target, rehearsalEmitter{status: status})
	if err != nil {
		return source, time.Time{}, err
	}
	if !recoveredTo.IsZero() {
		source.TakenAt = &recoveredTo
	}

	status("Starting the copy without the archive")
	copySpec, err := docker.SpecOf(ctx, created)
	if err != nil {
		return source, time.Time{}, err
	}
	copySpec.Command = copyCommand(copySpec.Command)
	copySpec.Mounts = copyMounts(copySpec.Mounts, dbx.PostgresDataDir, dataVolume)
	copySpec.Ports = nil
	copySpec.Networks, copySpec.NetworkAliases, copySpec.NetworkMode = []string{network}, aliases, ""
	copySpec.Start = true
	grace := int(dbStopGrace / time.Second)
	if err := docker.Lifecycle(ctx, created, dockerx.ActionStop, &grace); err != nil {
		return source, time.Time{}, err
	}
	// Removed and made again rather than recreated in place: the container
	// is a throwaway with nothing to put back, and removing it takes any
	// anonymous volume the image declares with it.
	if err := docker.RemoveContainer(ctx, created, true, true); err != nil {
		return source, time.Time{}, err
	}
	if err := docker.RemoveVolume(ctx, toolVolume, true); err != nil && !errdefs.IsNotFound(err) {
		return source, time.Time{}, err
	}
	started, err := docker.Create(ctx, *copySpec, nil)
	if err != nil {
		return source, time.Time{}, err
	}
	if err := s.waitForPostgres(ctx, started.ID, srv.account, 2*time.Minute); err != nil {
		return source, time.Time{}, err
	}
	return source, recoveredTo, nil
}

// rehearsalEmitter hands a recovery's headings to the run's transcript and
// keeps the server's own log out of it: those lines are the copy's, not the
// migration's.
type rehearsalEmitter struct {
	status func(string, ...any)
}

func (e rehearsalEmitter) Status(format string, args ...any) { e.status(format, args...) }
func (e rehearsalEmitter) Line(string, string)               {}
func (e rehearsalEmitter) Run(context.Context, string, ...string) (int, error) {
	return -1, errors.New("a rehearsal runs no host command")
}
func (e rehearsalEmitter) RunEnv(context.Context, []string, string, ...string) (int, error) {
	return -1, errors.New("a rehearsal runs no host command")
}
func (e rehearsalEmitter) RunDetached(context.Context, []string, []string, string, ...string) (int, error) {
	return -1, errors.New("a rehearsal runs no host command")
}
func (e rehearsalEmitter) RunCmd(*exec.Cmd, []string) (int, error) {
	return -1, errors.New("a rehearsal runs no host command")
}

// copyFileInto puts one host file into a container's directory under name.
func copyFileInto(ctx context.Context, docker *dockerx.Client, container, path, directory, name string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	go func() {
		tw := tar.NewWriter(writer)
		err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: info.Size(), ModTime: time.Now()})
		if err == nil {
			_, err = io.Copy(tw, file)
		}
		if err == nil {
			err = tw.Close()
		}
		writer.CloseWithError(err)
	}()
	return docker.CopyArchive(ctx, container, directory, reader)
}

func sqlIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
