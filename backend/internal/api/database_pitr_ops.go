package api

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/docker/docker/errdefs"
)

// What point-in-time recovery does to a server, each a job on the server
// that a page attaches to: setting archiving up and taking it down (a
// restart each), a base backup and the retention after it, a recovery into a
// new container, and switching a recovered copy in for the server.

// pitrResult is the last line a point-in-time recovery job writes, on the
// "result" stream.
type pitrResult struct {
	Archive      int64      `json:"archive,omitempty"`
	Backup       string     `json:"backup,omitempty"`
	Container    string     `json:"container,omitempty"`
	ConnectionID int64      `json:"connectionId,omitempty"`
	Port         int        `json:"port,omitempty"`
	RecoveredTo  *time.Time `json:"recoveredTo,omitempty"`
	Replaced     string     `json:"replaced,omitempty"`
	Summary      string     `json:"summary,omitempty"`
}

func emitPITRResult(out jobs.Emitter, res pitrResult) {
	if b, err := json.Marshal(res); err == nil {
		out.Line("result", string(b))
	}
}

// walgProgress forwards what WAL-G prints to a job, its errors as errors.
func walgProgress(out jobs.Emitter) func(string) {
	return func(line string) {
		if strings.HasPrefix(line, "ERROR:") || strings.HasPrefix(line, "FATAL:") {
			out.Line("stderr", line)
			return
		}
		out.Line("stdout", line)
	}
}

// startPITRJob starts a job for a server unless one is already running for
// it, or a dump or restore is running against the connection.
func (s *Server) startPITRJob(w http.ResponseWriter, r *http.Request, srv *pitrServer, what, title string, timeout time.Duration, run jobs.Runner) error {
	if job, running := s.runningTransfer(srv.conn.ID); running {
		return httpx.Err(http.StatusConflict, "transfer_running",
			fmt.Sprintf("%s is running against this connection; wait for it to finish first.", job.Title)).Describe("transfer", job.ID)
	}
	job, started := s.startPITR(srv.name, jobs.Spec{
		Kind: pitrJobPrefix(srv.name) + what, Title: title, Target: srv.name,
		Timeout: timeout, StartedBy: httpx.MustPrincipal(r).Username(),
	}, run)
	if !started {
		return httpx.Err(http.StatusConflict, "pitr_running",
			fmt.Sprintf("%s is already running for %s (started by %s). Wait for it to finish, or stop it.",
				job.Title, srv.name, job.StartedBy)).Describe(what, job.ID)
	}
	httpx.JSON(w, http.StatusAccepted, job)
	return nil
}

func (s *Server) startPITR(container string, spec jobs.Spec, run jobs.Runner) (jobs.Job, bool) {
	return s.modules.jobs.StartExclusive(pitrJobPrefix(container), spec, run)
}

// runningPITRJob is the point-in-time recovery job running for a server.
func (s *Server) runningPITRJob(container string) (jobs.Job, bool) {
	prefix := pitrJobPrefix(container)
	for _, job := range s.modules.jobs.List() {
		if job.Status == jobs.StatusRunning && strings.HasPrefix(job.Kind, prefix) {
			return job, true
		}
	}
	return jobs.Job{}, false
}

// pitrArchiveSpec is the server's container as it runs with archiving into
// an archive: its own command with the archive's settings, and the tool and
// a directory destination mounted. Every other part of it is left as it was.
func pitrArchiveSpec(spec *dockerx.ContainerSpec, a *pitrArchive, walLevel string) *dockerx.ContainerSpec {
	out := *spec
	out.Command = dbx.PostgresCommandSettings(dbx.WithoutWALSettings(spec.Command), dbx.WALArchiveSettings(walLevel))
	out.Mounts = withPITRMounts(spec.Mounts, pitrMounts(pitrToolVolume(a.Stream), a, false))
	return &out
}

// pitrPlainSpec is the server's container with every trace of this feature
// taken out of its command and mounts.
func pitrPlainSpec(spec *dockerx.ContainerSpec) *dockerx.ContainerSpec {
	out := *spec
	out.Command = dbx.WithoutWALSettings(spec.Command)
	out.Mounts = withPITRMounts(spec.Mounts, nil)
	return &out
}

// restartPITRServer stops a server the way the power route does — a
// checkpoint first and a database's grace to finish — and recreates it with
// spec on the same volume. A recreate that fails puts the original back, and
// the original is started again.
func (s *Server) restartPITRServer(ctx context.Context, srv *pitrServer, spec *dockerx.ContainerSpec, out jobs.Emitter) (*dockerx.CreateResult, error) {
	docker := s.modules.docker
	if srv.running {
		s.checkpointPITRServer(ctx, srv)
		grace := int(dbStopGrace / time.Second)
		out.Status("Stopping %s", srv.name)
		if err := docker.Lifecycle(ctx, srv.id, dockerx.ActionStop, &grace); err != nil {
			return nil, fmt.Errorf("%s could not be stopped: %w", srv.name, err)
		}
	}
	out.Status("Starting %s again", srv.name)
	res, err := docker.Recreate(ctx, srv.id, dockerx.RecreateOptions{Spec: spec}, nil)
	if err != nil {
		if srv.running {
			_ = docker.Lifecycle(context.WithoutCancel(ctx), srv.name, dockerx.ActionStart, nil)
		}
		return nil, fmt.Errorf("%s could not be started with the new settings, and runs as it did: %w", srv.name, err)
	}
	for _, warning := range res.Warnings {
		out.Status("%s", warning)
	}
	if srv.conn != nil {
		s.modules.dbs.Close(srv.conn.ID)
		s.dbConns.stale(srv.conn.ID)
	}
	srv.id, srv.running, srv.spec = res.ID, true, spec
	return res, nil
}

// checkpointPITRServer asks a server to write what it holds before it is
// stopped, so the stop has little left to do.
func (s *Server) checkpointPITRServer(ctx context.Context, srv *pitrServer) {
	if srv.conn == nil {
		return
	}
	if pool, err := s.modules.dbs.Pool(ctx, srv.conn.ID, srv.conn.Driver, srv.dsn); err == nil {
		_, _ = pool.ExecContext(ctx, `CHECKPOINT`)
	}
}

// waitForPostgres waits until a server answers its own superuser over its
// socket and has left recovery.
func (s *Server) waitForPostgres(ctx context.Context, container string, account dbx.WALGAccount, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	env := []string{}
	if account.Password != "" {
		env = append(env, "PGPASSWORD="+account.Password)
	}
	var last string
	for {
		var answer strings.Builder
		code, err := s.modules.docker.ExecStream(ctx, container,
			[]string{"psql", "-U", account.User, "-d", "postgres", "-Atc", "SELECT pg_is_in_recovery()"}, "postgres", env,
			func(line dockerx.LogLine) {
				if line.Stream == "stdout" {
					answer.WriteString(strings.TrimSpace(line.Text))
				} else if line.Text != "" {
					last = line.Text
				}
			})
		if err == nil && code == 0 && answer.String() == "f" {
			return nil
		}
		if err != nil {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer within %s: %s", container, limit, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// --- setting archiving up and taking it down ---------------------------------

// pitrPlan is what setting archiving up will do, for the form to show before
// it is asked.
type pitrPlan struct {
	Container string            `json:"container"`
	Stream    string            `json:"stream"`
	Location  string            `json:"location"`
	Settings  map[string]string `json:"settings"`
	// Command is the server's command line as it will be started.
	Command []string `json:"command"`
	Mounts  []string `json:"mounts"`
}

// pitrLocation is where an archive is, as the operator would look for it.
func pitrLocation(a *pitrArchive) string {
	if a.TargetKind == backups.TargetLocal {
		return pitrArchiveDir(a)
	}
	parts := []string{}
	for _, part := range []string{a.Target.Bucket, a.Target.Prefix, a.Stream} {
		if part = strings.Trim(part, "/"); part != "" {
			parts = append(parts, part)
		}
	}
	return "s3://" + strings.Join(parts, "/")
}

func planPITR(srv *pitrServer, a *pitrArchive, walLevel string) pitrPlan {
	spec := pitrArchiveSpec(srv.spec, a, walLevel)
	mounts := []string{}
	for _, m := range pitrMounts(pitrToolVolume(a.Stream), a, false) {
		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		mounts = append(mounts, m.Source+":"+m.Target+":"+mode)
	}
	return pitrPlan{
		Container: srv.name, Stream: a.Stream, Location: pitrLocation(a),
		Settings: dbx.WALArchiveSettings(walLevel), Command: spec.Command, Mounts: mounts,
	}
}

// pitrWALLevel is the server's wal_level, read where it answers.
func (s *Server) pitrWALLevel(ctx context.Context, srv *pitrServer) string {
	pool, err := s.modules.dbs.Pool(ctx, srv.conn.ID, srv.conn.Driver, srv.dsn)
	if err != nil {
		return "replica"
	}
	var level string
	if err := pool.QueryRowContext(ctx, `SHOW wal_level`).Scan(&level); err != nil {
		return "replica"
	}
	return level
}

// preparePITRDirectory makes a directory destination's folder for an archive
// the server's account's own. Docker creates the folder for the bind as root;
// a container of the server's image, as root, hands it over with a constant
// command, since only the image knows the account's number.
func (s *Server) preparePITRDirectory(ctx context.Context, image string, a *pitrArchive) error {
	_, err := s.runAsRoot(ctx, image, a.Container, []dockerx.MountSpec{
		{Type: "bind", Source: pitrArchiveDir(a), Target: dbx.WALGArchiveMount},
	}, []string{"sh", "-c", "chown postgres:postgres " + dbx.WALGArchiveMount + " && chmod 0700 " + dbx.WALGArchiveMount})
	return err
}

// removePITRDirectory takes an archive's folder in a directory destination
// away again where it holds nothing, after a setup that did not finish. Only
// what is empty goes: an archive that holds anything is left as it is.
func (s *Server) removePITRDirectory(ctx context.Context, image string, a *pitrArchive) {
	if a.TargetKind != backups.TargetLocal {
		return
	}
	_, _ = s.runAsRoot(context.WithoutCancel(ctx), image, a.Container, []dockerx.MountSpec{
		{Type: "bind", Source: strings.TrimRight(a.Target.Path, "/"), Target: "/jd-pitr-root"},
	}, []string{"find", "/jd-pitr-root/" + a.Stream, "-depth", "-empty", "-delete"})
}

func (s *Server) runAsRoot(ctx context.Context, image, owner string, mounts []dockerx.MountSpec, cmd []string) (int, error) {
	code, removed, err := s.modules.docker.RunToCompletion(ctx, dockerx.ContainerSpec{
		Name: owner + "-pitr-prepare-" + randomSuffix(), Image: image, User: "root",
		Entrypoint: cmd[:1], Command: cmd[1:], Mounts: mounts, NetworkMode: "none",
		Labels: pitrLabelSpecs(owner, pitrRoleRun),
	}, nil)
	if err == nil && !removed {
		err = errors.New("the container that prepared the archive could not be removed")
	}
	if err == nil && code != 0 {
		err = fmt.Errorf("preparing the archive's folder exited with %d", code)
	}
	return code, err
}

// enablePITR sets a server up to archive into a new archive: the key, the
// tool and its configuration, a check from the server's own side that the
// destination takes a file, the restart with the settings, and the first
// base backup.
func (s *Server) enablePITR(ctx context.Context, srv *pitrServer, a *pitrArchive, secrets *backups.TargetSecrets, walLevel string, out jobs.Emitter) error {
	out.Status("Making the key the archive is encrypted with")
	public, private, err := dbx.NewWALKey(srv.name)
	if err != nil {
		return err
	}
	a.publicKey = public
	storage := dbx.WALStorage{
		Kind: string(a.TargetKind), Bucket: a.Target.Bucket, Region: a.Target.Region,
		Endpoint: a.Target.Endpoint, Prefix: a.Target.Prefix,
	}
	if secrets != nil {
		storage.AccessKeyID, storage.SecretAccessKey = secrets.AccessKeyID, secrets.SecretAccessKey
	}
	cfg, err := dbx.WALGConfig(storage, a.Stream, public, srv.account)
	if err != nil {
		return err
	}
	volume := pitrToolVolume(a.Stream)
	out.Status("Installing WAL-G into the volume %s", volume)
	if a.tool, err = s.installWALG(ctx, volume, pitrRoleTool, srv.image, srv.name, cfg, true); err != nil {
		return err
	}
	cleanVolume, marked := true, false
	defer func() {
		if !cleanVolume {
			return
		}
		// A setup that did not finish leaves nothing behind it: not the
		// file it proved the destination with, and not the volume holding
		// the destination's keys.
		if marked {
			_, _ = s.runWALG(context.WithoutCancel(ctx), srv.image, srv.name, pitrMounts(volume, a, false),
				[]string{"st", "rm", "jd-pitr/tool.sha256"}, nil)
		}
		s.removePITRDirectory(ctx, srv.image, a)
		s.removeContainerAndVolumes(ctx, "", volume)
	}()
	if a.TargetKind == backups.TargetLocal {
		out.Status("Preparing %s", pitrArchiveDir(a))
		if err := s.preparePITRDirectory(ctx, srv.image, a); err != nil {
			return err
		}
	}
	out.Status("Checking from the server's side that %s takes a file", pitrLocation(a))
	if _, err := s.runWALG(ctx, srv.image, srv.name, pitrMounts(volume, a, false),
		[]string{"st", "put", dbx.WALGMount + "/lib/tool.sha256", "jd-pitr/tool.sha256"}, walgProgress(out)); err != nil {
		return fmt.Errorf("the archive could not be written from inside %s: %w", srv.name, err)
	}
	marked = true

	if err := s.sealPITRArchive(a, secrets, private); err != nil {
		return err
	}
	id, err := s.insertPITRArchive(ctx, a)
	if err != nil {
		return err
	}
	a.ID = id
	if _, err := s.restartPITRServer(ctx, srv, pitrArchiveSpec(srv.spec, a, walLevel), out); err != nil {
		_ = s.deletePITRArchive(context.WithoutCancel(ctx), id)
		return err
	}
	cleanVolume = false
	out.Status("Waiting for %s to answer", srv.name)
	if err := s.waitForPostgres(ctx, srv.id, srv.account, 2*time.Minute); err != nil {
		return err
	}
	s.reloadPITRSchedule(context.WithoutCancel(ctx))
	return s.runPITRBackup(ctx, srv, a, out)
}

// disablePITR restarts a server without archiving. The archive itself is
// kept, and with it every moment it can still recover to, unless the
// operator also asked for it to be deleted.
func (s *Server) disablePITR(ctx context.Context, srv *pitrServer, a *pitrArchive, deleteArchive bool, out jobs.Emitter) error {
	if _, err := s.restartPITRServer(ctx, srv, pitrPlainSpec(srv.spec), out); err != nil {
		return err
	}
	if err := s.endPITRArchive(ctx, a.ID); err != nil {
		return err
	}
	ended := time.Now().UTC()
	a.EndedAt = &ended
	s.reloadPITRSchedule(context.WithoutCancel(ctx))
	// The server no longer mounts the tool, and the configuration in it
	// holds the destination's keys.
	s.removeContainerAndVolumes(ctx, "", pitrToolVolume(a.Stream))
	if deleteArchive {
		return s.deletePITRArchiveContents(ctx, srv, a, out)
	}
	return nil
}

// deletePITRArchiveContents deletes everything an archive holds in its
// destination, and its record.
func (s *Server) deletePITRArchiveContents(ctx context.Context, srv *pitrServer, a *pitrArchive, out jobs.Emitter) error {
	out.Status("Deleting the archive %s", pitrLocation(a))
	if _, err := s.walgFor(ctx, srv, a, []string{"delete", "everything", "FORCE", "--confirm"}, walgProgress(out)); err != nil {
		return err
	}
	_, _ = s.walgFor(ctx, srv, a, []string{"st", "rm", "jd-pitr/tool.sha256"}, nil)
	return s.deletePITRArchive(ctx, a.ID)
}

// --- base backups ------------------------------------------------------------

// runPITRBackup takes a base backup into an archive and then applies the
// archive's retention.
func (s *Server) runPITRBackup(ctx context.Context, srv *pitrServer, a *pitrArchive, out jobs.Emitter) error {
	if !srv.running {
		err := fmt.Errorf("%s is not running, so no base backup can be taken", srv.name)
		_ = s.recordPITRBackup(context.WithoutCancel(ctx), a.ID, err)
		return err
	}
	out.Status("Taking a base backup of %s", srv.name)
	_, err := s.execWALG(ctx, srv.id, []string{"backup-push", dbx.PostgresDataDir}, walgProgress(out))
	if recordErr := s.recordPITRBackup(context.WithoutCancel(ctx), a.ID, err); recordErr != nil && err == nil {
		err = recordErr
	}
	if err != nil {
		return err
	}
	reading, err := s.readPITRArchive(ctx, srv, a)
	if err != nil {
		return err
	}
	if keep, ok := dbx.WALRetentionKeep(reading.Backups, time.Now(), a.RetentionDays); ok {
		out.Status("Removing what is older than %s, which %d days of recovery no longer need", keep.Name, a.RetentionDays)
		if _, err := s.execWALG(ctx, srv.id, []string{"delete", "before", "FIND_FULL", keep.Name, "--confirm"}, walgProgress(out)); err != nil {
			return fmt.Errorf("the base backup was taken, and retention could not remove the older ones: %w", err)
		}
		if reading, err = s.readPITRArchive(ctx, srv, a); err != nil {
			return err
		}
	}
	if len(reading.Backups) == 0 || reading.Window.From == nil {
		return errors.New("WAL-G reported the base backup written, and the archive lists none")
	}
	newest := reading.Backups[len(reading.Backups)-1]
	out.Status("Base backup %s taken; the archive can recover to any moment since %s",
		newest.Name, reading.Window.From.Format(time.RFC3339))
	emitPITRResult(out, pitrResult{Archive: a.ID, Backup: newest.Name})
	return nil
}

// --- recovery ------------------------------------------------------------------

// pitrRestorePlan is what a recovery will do, for the form to show before it
// is asked: where the copy will run, which base backup it starts from and
// what it is told to stop at.
type pitrRestorePlan struct {
	Container string            `json:"container"`
	Port      int               `json:"port"`
	Backup    string            `json:"backup"`
	BackupAt  time.Time         `json:"backupAt"`
	Settings  map[string]string `json:"settings"`
}

// pitrBaseFor is the base backup a recovery to target starts from. A named
// point's moment is the one recorded when it was made here; a name made
// elsewhere is reached from the oldest backup, which is before any of them.
func (s *Server) pitrBaseFor(ctx context.Context, a *pitrArchive, reading *pitrReading, target dbx.RecoveryTarget) (dbx.WALBackup, error) {
	switch target.Kind {
	case "time":
		if target.Time.After(time.Now().Add(time.Minute)) {
			return dbx.WALBackup{}, errors.New("that moment has not happened yet")
		}
		return dbx.BaseBackupFor(reading.Backups, target.Time)
	case "name":
		points, err := s.pitrRestorePoints(ctx, a.ID)
		if err != nil {
			return dbx.WALBackup{}, err
		}
		for _, p := range points {
			if p.Name == target.Name {
				return dbx.BaseBackupFor(reading.Backups, p.CreatedAt)
			}
		}
		if len(reading.Backups) == 0 {
			return dbx.BaseBackupFor(nil, time.Time{})
		}
		return reading.Backups[0], nil
	}
	return dbx.BaseBackupFor(reading.Backups, time.Time{})
}

// pitrCopyName is the container a recovery runs in unless the request named
// one: the server's name, then when it was recovered, within the 63
// characters a container's name may have.
func pitrCopyName(source string, at time.Time) string {
	suffix := "-restored-" + at.UTC().Format("20060102-150405")
	if len(source)+len(suffix) > 63 {
		source = source[:63-len(suffix)]
	}
	return source + suffix
}

// pitrCopySpec is the copy's container: the server's own image, account and
// settings on a volume of its own, published on loopback at a port of its
// own, on Docker's default network and nobody else's, and told to recover.
// The deployment labels and networks of the server are not given to it: a
// copy that joined an application's network would be one more database the
// application can reach.
func pitrCopySpec(srv *pitrServer, a *pitrArchive, name, toolVolume string, port int, target dbx.RecoveryTarget) *dockerx.ContainerSpec {
	spec := *srv.spec
	spec.Name = name
	spec.Ports = []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: srv.account.Port, Protocol: "tcp"}}
	spec.Networks, spec.NetworkMode, spec.Hostname = nil, "", ""
	labels := []dockerx.LabelSpec{}
	for _, l := range spec.Labels {
		if !strings.HasPrefix(l.Name, "io.just-dashboard.") && !strings.HasPrefix(l.Name, "com.docker.compose.") {
			labels = append(labels, l)
		}
	}
	spec.Labels = append(labels, pitrLabelSpecs(srv.name, pitrRoleCopy)...)
	mounts := []dockerx.MountSpec{}
	for _, m := range spec.Mounts {
		if m.Target == dbx.PostgresDataDir {
			m = dockerx.MountSpec{Type: "volume", Source: name + "-data", Target: dbx.PostgresDataDir}
		}
		mounts = append(mounts, m)
	}
	spec.Mounts = withPITRMounts(mounts, pitrMounts(toolVolume, a, true))
	spec.Command = dbx.PostgresCommandSettings(dbx.WithoutWALSettings(srv.spec.Command), target.RecoverySettings())
	// No restart while it recovers: a recovery that cannot finish stops the
	// server, and Docker starting it again would replay the same refusal for
	// ever. The copy is given a restart policy once it runs on its own.
	spec.RestartPolicy, spec.MaxRetries, spec.Start, spec.Pull = "no", 0, false, "missing"
	return &spec
}

// restorePITR recovers an archive into a new container and saves a
// connection to it. The copy is recorded as recovering before anything is
// made, so that a recovery this process does not live to finish is swept at
// its next start; whatever a recovery made is removed again if it fails
// before the copy runs on its own. The server it recovered from is never
// touched.
func (s *Server) restorePITR(ctx context.Context, srv *pitrServer, a *pitrArchive, plan pitrRestorePlan, target dbx.RecoveryTarget, by string, out jobs.Emitter) (*pitrRestore, error) {
	docker := s.modules.docker
	name := plan.Container
	dataVolume, toolVolume := name+"-data", name+"-pitr-restore"
	// The release the archive was written by reads it back: a server since
	// moved to a newer major cannot start a cluster an older one wrote.
	image := a.Image
	if image == "" {
		image = srv.image
	}
	restore := &pitrRestore{
		ArchiveID: a.ID, Source: srv.name, Container: name, Port: plan.Port, Target: target,
		BaseBackup: plan.Backup, State: "recovering", CreatedBy: by,
	}
	id, err := s.insertPITRRestore(ctx, restore)
	if err != nil {
		return nil, err
	}
	restore.ID = id
	created := ""
	finished := false
	defer func() {
		if !finished {
			out.Status("Removing what the recovery made")
			s.removeContainerAndVolumes(ctx, created, dataVolume, toolVolume)
			_ = s.deletePITRRestore(context.WithoutCancel(ctx), id)
		}
	}()

	private, err := s.pitrPrivateKey(a)
	if err != nil {
		return nil, err
	}
	cfg, err := s.pitrConfig(a, private, srv.account)
	if err != nil {
		return nil, err
	}
	out.Status("Installing WAL-G and the archive's key into %s", toolVolume)
	if _, err := s.installWALG(ctx, toolVolume, pitrRoleRestore, image, name, cfg, true); err != nil {
		return nil, err
	}
	if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: dataVolume, Labels: pitrLabels(srv.name, pitrRoleCopyData)}); err != nil {
		return nil, err
	}
	out.Status("Fetching the base backup %s into %s", plan.Backup, dataVolume)
	fetchMounts := append(pitrMounts(toolVolume, a, true),
		dockerx.MountSpec{Type: "volume", Source: dataVolume, Target: dbx.PostgresDataDir})
	if _, err := s.runWALG(ctx, image, name, fetchMounts,
		[]string{"backup-fetch", dbx.PostgresDataDir, plan.Backup}, walgProgress(out)); err != nil {
		return nil, err
	}

	spec := pitrCopySpec(srv, a, name, toolVolume, plan.Port, target)
	spec.Image = image
	res, err := docker.Create(ctx, *spec, nil)
	if err != nil {
		return nil, fmt.Errorf("the copy's container could not be made: %w", err)
	}
	created = res.ID
	uid, gid, err := s.pitrAccount(ctx, created)
	if err != nil {
		return nil, err
	}
	// recovery.signal is what makes the server recover rather than start.
	if err := docker.CopyArchive(ctx, created, dbx.PostgresDataDir, recoverySignal(uid, gid)); err != nil {
		return nil, err
	}
	out.Status("Starting %s to replay the log", name)
	created, recoveredTo, err := s.recoverCopy(ctx, created, true, srv, target, out)
	if err != nil {
		return nil, err
	}

	// The recovery is over: the copy no longer needs the archive or its key.
	out.Status("Starting %s on its own", name)
	copySpec, err := docker.SpecOf(ctx, created)
	if err != nil {
		return nil, err
	}
	copySpec.Command = dbx.WithoutWALSettings(copySpec.Command)
	copySpec.Mounts = withPITRMounts(copySpec.Mounts, nil)
	copySpec.RestartPolicy = "unless-stopped"
	grace := int(dbStopGrace / time.Second)
	if err := docker.Lifecycle(ctx, created, dockerx.ActionStop, &grace); err != nil {
		return nil, err
	}
	recreated, err := docker.Recreate(ctx, created, dockerx.RecreateOptions{Spec: copySpec}, nil)
	if err != nil {
		return nil, err
	}
	created = recreated.ID
	s.removeContainerAndVolumes(ctx, "", toolVolume)

	// From here the copy is a database of its own, whatever else goes wrong.
	finished = true
	record := context.WithoutCancel(ctx)
	if !recoveredTo.IsZero() {
		restore.RecoveredTo = &recoveredTo
	}
	restore.State = "ready"
	if err := s.finishPITRRestore(record, restore); err != nil {
		return restore, err
	}
	if err := s.waitForPostgres(ctx, created, srv.account, 2*time.Minute); err != nil {
		return restore, err
	}
	if restore.ConnectionID, err = s.saveCopyConnection(ctx, srv, name, plan.Port); err != nil {
		out.Status("The copy runs, and a connection to it could not be saved: %v", err)
		return restore, nil
	}
	return restore, s.finishPITRRestore(record, restore)
}

// recoverySignal is the empty file a server recovers on, as an archive to
// unpack into its data directory, belonging to its account.
func recoverySignal(uid, gid int) *bytes.Buffer {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	_ = tw.WriteHeader(&tar.Header{
		Name: "recovery.signal", Typeflag: tar.TypeReg, Mode: 0o600, Uid: uid, Gid: gid, ModTime: time.Now().UTC(),
	})
	_ = tw.Close()
	return &archive
}

// recoverCopy starts a copy, follows its log into the job and waits for it to
// leave recovery, returning the container it ended in and the moment it
// recovered to. A moment later than every commit the archive holds is never
// reached — the server stops rather than guess — and every commit before it
// is then all there is: the copy is started again to recover to the end of
// the log, which is the same state.
func (s *Server) recoverCopy(ctx context.Context, id string, start bool, srv *pitrServer, target dbx.RecoveryTarget, out jobs.Emitter) (string, time.Time, error) {
	docker := s.modules.docker
	if start {
		if err := docker.Lifecycle(ctx, id, dockerx.ActionStart, nil); err != nil {
			return id, time.Time{}, err
		}
	}
	lines, closer, err := docker.Logs(ctx, id, dockerx.LogOptions{Follow: true, Tail: "all"})
	if err != nil {
		return id, time.Time{}, err
	}
	var logged []string
	followed := make(chan struct{})
	go func() {
		defer close(followed)
		for line := range lines {
			logged = append(logged, line.Text)
			out.Line(line.Stream, line.Text)
		}
	}()
	stop := func() []string {
		closer.Close()
		<-followed
		return logged
	}
	for {
		if s.waitForPostgres(ctx, id, srv.account, 3*time.Second) == nil {
			at, _ := dbx.RecoveredTo(stop())
			return id, at, nil
		}
		if ctx.Err() != nil {
			stop()
			return id, time.Time{}, ctx.Err()
		}
		detail, err := docker.Inspect(ctx, id)
		if errdefs.IsNotFound(err) {
			stop()
			return id, time.Time{}, fmt.Errorf("the copy's container was removed while it recovered")
		}
		if err != nil || detail.State == "running" {
			continue
		}
		failure := dbx.RecoveryFailure(stop())
		if target.Kind != "time" || !strings.Contains(failure, "before configured recovery target was reached") {
			if failure == "" {
				failure = "the copy stopped before it finished recovering; its log is above"
			}
			return id, time.Time{}, errors.New(failure)
		}
		out.Status("The archive holds nothing after %s, so the copy is recovered to the end of its log, which is the same state",
			target.Time.UTC().Format(time.RFC3339))
		spec, err := docker.SpecOf(ctx, id)
		if err != nil {
			return id, time.Time{}, err
		}
		latest := dbx.RecoveryTarget{Kind: "latest"}
		spec.Command = dbx.PostgresCommandSettings(dbx.WithoutWALSettings(spec.Command), latest.RecoverySettings())
		// Recreate starts what it makes, and the new container's log is its
		// own from its first line.
		replaced, err := docker.Recreate(ctx, id, dockerx.RecreateOptions{Spec: spec}, nil)
		if err != nil {
			return id, time.Time{}, err
		}
		return s.recoverCopy(ctx, replaced.ID, false, srv, latest, out)
	}
}

// saveCopyConnection saves a connection to a recovered copy: the server's
// own, at the copy's port. The copy holds the accounts the server had at the
// moment it was recovered to.
func (s *Server) saveCopyConnection(ctx context.Context, srv *pitrServer, name string, port int) (int64, error) {
	info, err := dbx.ParseDSN(dbx.DriverPostgres, srv.dsn)
	if err != nil {
		return 0, err
	}
	dsn, err := applicationConnectionURL(dbx.DriverPostgres, srv.dsn, info, "127.0.0.1", strconv.Itoa(port))
	if err != nil {
		return 0, err
	}
	names := map[string]string{}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, name FROM db_connections`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id int64
		var existing string
		if err := rows.Scan(&id, &existing); err == nil {
			names[strconv.FormatInt(id, 10)] = existing
		}
	}
	rows.Close()
	saved, err := s.insertConnection(ctx, "docker:"+name, uniqueConnectionName(name, names), dbx.DriverPostgres, dsn)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.Store.DB.QueryRowContext(ctx, `SELECT id FROM db_connections WHERE name = ?`, saved).Scan(&id)
	return id, err
}

// --- switching a copy in ---------------------------------------------------------

// switchPITR puts a recovered copy in its server's place: the server is
// stopped and set aside under another name, kept with its volume, and the
// copy takes its name, its port and its networks, so every application and
// every saved connection reaches the copy without being told. A server that
// archived goes on archiving, into an archive of its own, since the copy's
// history parted from the old one's at the moment it was recovered to.
func (s *Server) switchPITR(ctx context.Context, srv *pitrServer, copyRow *pitrRestore, out jobs.Emitter) (string, error) {
	docker := s.modules.docker
	copyDetail, err := docker.Inspect(ctx, copyRow.Container)
	if err != nil {
		return "", fmt.Errorf("the copy %s is not on this server any more: %w", copyRow.Container, err)
	}
	copySpec, err := docker.SpecOf(ctx, copyDetail.ID)
	if err != nil {
		return "", err
	}
	archives, err := s.pitrArchivesOf(ctx, srv.name)
	if err != nil {
		return "", err
	}
	var active *pitrArchive
	for _, a := range archives {
		if a.EndedAt == nil {
			active = a
		}
	}

	// The copy's place: everything that makes the server the server, on
	// the copy's data.
	spec := *srv.spec
	mounts := []dockerx.MountSpec{}
	for _, m := range withPITRMounts(srv.spec.Mounts, nil) {
		if m.Target == dbx.PostgresDataDir {
			for _, cm := range copySpec.Mounts {
				if cm.Target == dbx.PostgresDataDir {
					m = cm
				}
			}
		}
		mounts = append(mounts, m)
	}
	spec.Mounts = mounts
	spec.Command = dbx.WithoutWALSettings(srv.spec.Command)
	// The copy's release, which is the one its data was written by.
	spec.Image = copySpec.Image
	var next *pitrArchive
	walLevel := s.pitrWALLevel(ctx, srv)
	switched := false
	defer func() {
		if next != nil && !switched {
			s.removePITRDirectory(ctx, copySpec.Image, next)
			s.removeContainerAndVolumes(ctx, "", pitrToolVolume(next.Stream))
		}
	}()
	if active != nil {
		next = &pitrArchive{
			Container: srv.name, ConnectionID: srv.conn.ID, Stream: dbx.WALStreamName(srv.name, time.Now()),
			TargetKind: active.TargetKind, Target: active.Target, Schedule: active.Schedule,
			RetentionDays: active.RetentionDays, Image: copySpec.Image, CreatedBy: active.CreatedBy,
			secretsEnc: active.secretsEnc, publicKey: active.publicKey, privateKeyEnc: active.privateKeyEnc,
		}
		cfg, err := s.pitrConfig(next, next.publicKey, srv.account)
		if err != nil {
			return "", err
		}
		out.Status("Installing WAL-G for the archive %s will write next", srv.name)
		if next.tool, err = s.installWALG(ctx, pitrToolVolume(next.Stream), pitrRoleTool, copySpec.Image, srv.name, cfg, true); err != nil {
			return "", err
		}
		if next.TargetKind == backups.TargetLocal {
			if err := s.preparePITRDirectory(ctx, copySpec.Image, next); err != nil {
				return "", err
			}
		}
		spec = *pitrArchiveSpec(&spec, next, walLevel)
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	replaced := srv.name + "-replaced-" + stamp
	grace := int(dbStopGrace / time.Second)
	out.Status("Stopping %s and setting it aside as %s", srv.name, replaced)
	if srv.running {
		s.checkpointPITRServer(ctx, srv)
		if err := docker.Lifecycle(ctx, srv.id, dockerx.ActionStop, &grace); err != nil {
			return "", s.undoPITRSwitch(ctx, srv, "", copyRow, "", err)
		}
	}
	if err := docker.Rename(ctx, srv.id, replaced); err != nil {
		return "", s.undoPITRSwitch(ctx, srv, "", copyRow, "", fmt.Errorf("%s could not be set aside: %w", srv.name, err))
	}
	if _, err := docker.UpdateRestartPolicy(ctx, srv.id, "no", 0); err != nil {
		out.Status("%s will start again with Docker unless it is stopped by hand: %v", replaced, err)
	}
	if err := docker.Lifecycle(ctx, copyDetail.ID, dockerx.ActionStop, &grace); err != nil {
		return "", s.undoPITRSwitch(ctx, srv, replaced, copyRow, copyDetail.ID, err)
	}
	if err := docker.Rename(ctx, copyDetail.ID, srv.name); err != nil {
		return "", s.undoPITRSwitch(ctx, srv, replaced, copyRow, copyDetail.ID, err)
	}
	out.Status("Starting the copy as %s", srv.name)
	recreated, err := docker.Recreate(ctx, copyDetail.ID, dockerx.RecreateOptions{Spec: &spec}, nil)
	if err != nil {
		return "", s.undoPITRSwitch(ctx, srv, replaced, copyRow, copyDetail.ID, err)
	}
	switched = true

	// From here the copy is the server, and the records say so before
	// anything else is waited for.
	record := context.WithoutCancel(ctx)
	for _, conn := range []int64{srv.conn.ID, copyRow.ConnectionID} {
		s.modules.dbs.Close(conn)
		s.dbConns.stale(conn)
	}
	if copyRow.ConnectionID != 0 {
		if _, err := s.forgetConnection(record, copyRow.ConnectionID, "system"); err != nil {
			out.Status("The copy's own connection could not be removed: %v", err)
		}
	}
	if err := s.markPITRRestoreSwitched(record, copyRow.ID, srv.name, replaced); err != nil {
		return replaced, err
	}
	if active != nil {
		// The old archive ends here and is kept under the server's name: it
		// is the only place the moments before the switch are, and the
		// server set aside still mounts its tool if it is started again.
		if err := s.endPITRArchive(record, active.ID); err != nil {
			return replaced, err
		}
		if next.ID, err = s.insertPITRArchive(record, next); err != nil {
			return replaced, err
		}
		s.reloadPITRSchedule(record)
	}
	if err := s.waitForPostgres(ctx, recreated.ID, srv.account, 2*time.Minute); err != nil {
		return replaced, err
	}
	if active != nil {
		srv.id, srv.running, srv.spec = recreated.ID, true, &spec
		if err := s.runPITRBackup(ctx, srv, next, out); err != nil {
			return replaced, fmt.Errorf("the copy is in place and archiving, and its first base backup failed: %w", err)
		}
	}
	return replaced, nil
}

// undoPITRSwitch puts both back where they were after a switch that did not
// get through: the copy under its own name and running, the server under its
// name with its restart policy, and running if it was.
func (s *Server) undoPITRSwitch(ctx context.Context, srv *pitrServer, replaced string, copyRow *pitrRestore, copyID string, cause error) error {
	docker := s.modules.docker
	ctx = context.WithoutCancel(ctx)
	if copyID != "" {
		if detail, err := docker.Inspect(ctx, copyID); err == nil && detail.Name != copyRow.Container {
			_ = docker.Rename(ctx, copyID, copyRow.Container)
		}
		_ = docker.Lifecycle(ctx, copyID, dockerx.ActionStart, nil)
	}
	if replaced != "" {
		_ = docker.Rename(ctx, srv.id, srv.name)
		policy := "unless-stopped"
		retries := 0
		if srv.spec != nil && srv.spec.RestartPolicy != "" {
			policy, retries = srv.spec.RestartPolicy, srv.spec.MaxRetries
		}
		_, _ = docker.UpdateRestartPolicy(ctx, srv.id, policy, retries)
	}
	if srv.running {
		_ = docker.Lifecycle(ctx, srv.id, dockerx.ActionStart, nil)
	}
	return fmt.Errorf("the switch did not happen and %s is back as it was: %w", srv.name, cause)
}
