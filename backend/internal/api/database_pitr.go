package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/docker/docker/errdefs"
)

// Point-in-time recovery for the PostgreSQL servers this dashboard runs.
//
// A server set up for it hands every finished segment of its write-ahead log
// to WAL-G, which compresses it, encrypts it to the archive's public key and
// writes it into a backup destination: a directory on this server, an S3
// bucket or a Backblaze B2 one, the three a backup job writes to. Beside the
// log, a base backup is taken on a schedule and retention keeps as many days
// of both as asked. A recovery unpacks the base backup that precedes the
// moment asked for into a new volume and starts a new container on it, which
// replays the log up to that moment and becomes a server of its own. The
// server it was recovered from is never touched; the operator then keeps both
// or switches the copy in for it.
//
// WAL-G always runs inside a container of the server's own image, as the
// server's own account: in the server itself while it runs (the archiver
// runs it there anyway), and otherwise in a container made for the one
// command. Nothing here reads the archive with this process's own eyes, so a
// directory destination is only ever a bind mount of the operator's path, and
// what WAL-G writes belongs to the account that reads it back.

const (
	// pitrBackupTimeout bounds one base backup, and pitrRestoreTimeout one
	// recovery: both are the size of the database, and both can be stopped.
	pitrBackupTimeout  = 6 * time.Hour
	pitrRestoreTimeout = 12 * time.Hour
	// pitrReadEvery is how old the record of what an archive holds may be
	// before a reading asks the destination again.
	pitrReadEvery = time.Minute
	// pitrDefaultSchedule is a base backup every night, and pitrDefaultDays
	// the week of moments a new archive keeps.
	pitrDefaultSchedule = "0 3 * * *"
	pitrDefaultDays     = 7
	// pitrLabel marks every volume and container this feature makes, with
	// the server it was made for, so one can always be traced back.
	pitrLabel = "io.just-dashboard.pitr"
)

// pitrJobPrefix is the kind every point-in-time recovery job of one server
// starts with: one at a time per server, whichever connection asked.
func pitrJobPrefix(container string) string { return "database.pitr." + container + "." }

// pitrToolVolume is the volume one archive's WAL-G and configuration live in.
// It is the archive's rather than the server's: a server set aside by a
// switch keeps the one it archived with, and started again by hand it goes on
// writing its own timeline into its own archive, never into its successor's.
func pitrToolVolume(stream string) string { return stream + "-pitr" }

// pitrServer is the container behind a connection, read for point-in-time
// recovery.
type pitrServer struct {
	conn    *dbConnection
	dsn     string
	id      string
	name    string
	image   string
	spec    *dockerx.ContainerSpec
	running bool
	// volume is the named volume the cluster lives in.
	volume  string
	account dbx.WALGAccount
	// reason says why this server cannot be set up for point-in-time
	// recovery, and is empty when it can.
	reason string
}

// pitrImages is every PostgreSQL image the provisioning list starts. Those
// are the images whose layout — the official entrypoint, the data directory,
// the server's account — the settings and mounts here are written for.
func pitrImages() map[string]bool {
	out := map[string]bool{}
	for _, t := range provisionTemplates {
		if t.driver != dbx.DriverPostgres {
			continue
		}
		for _, v := range t.versions {
			out[v.image] = true
		}
	}
	return out
}

// pitrServerOf finds the server behind a connection and says whether it can
// be archived. The container is found running or stopped: a server that has
// stopped answering is when its archive matters most.
func (s *Server) pitrServerOf(ctx context.Context, conn *dbConnection, dsn string) *pitrServer {
	out := &pitrServer{conn: conn, dsn: dsn}
	if conn.Driver != dbx.DriverPostgres {
		out.reason = "Point-in-time recovery archives PostgreSQL's write-ahead log, and this is not a PostgreSQL server."
		return out
	}
	if s.modules.docker == nil {
		out.reason = "This host has no Docker socket, and point-in-time recovery is set up for a server this dashboard runs in a container."
		return out
	}
	place := s.newDBHostView(ctx).place(ctx, conn, dsn)
	if place.Container == nil {
		out.reason = "Point-in-time recovery is set up for a PostgreSQL server this dashboard runs in a container, and this one is not in a container on this machine."
		return out
	}
	out.id, out.name = place.Container.ID, place.Container.Name
	out.running = place.Container.State == "running"
	if place.foreign {
		out.reason = fmt.Sprintf("The connection is answered by the container %s, whose image is not PostgreSQL's.", out.name)
		return out
	}
	if project := place.Container.ComposeProject; project != "" {
		out.reason = fmt.Sprintf("%s belongs to the compose project %s, so its settings and mounts are its compose file's; archive it from there.", out.name, project)
		return out
	}
	s.readPITRSpec(ctx, out)
	return out
}

// pitrServerNamed is a server found by its container's name alone, for the
// schedule, which acts for no connection.
func (s *Server) pitrServerNamed(ctx context.Context, name string) *pitrServer {
	out := &pitrServer{name: name}
	detail, err := s.modules.docker.Inspect(ctx, name)
	if err != nil {
		out.reason = fmt.Sprintf("The container %s is not on this server: %v", name, err)
		return out
	}
	out.id, out.running = detail.ID, detail.State == "running"
	s.readPITRSpec(ctx, out)
	return out
}

// readPITRSpec reads a server's configuration and holds it to the layout the
// settings and mounts here are written for.
func (s *Server) readPITRSpec(ctx context.Context, out *pitrServer) {
	spec, err := s.modules.docker.SpecOf(ctx, out.id)
	if err != nil {
		out.reason = fmt.Sprintf("The container %s could not be read: %v", out.name, err)
		return
	}
	out.spec, out.image = spec, spec.Image
	if !pitrImages()[spec.Image] {
		out.reason = fmt.Sprintf("%s runs %s, which is not one of the PostgreSQL images this dashboard starts. Point-in-time recovery is set up for those, whose layout it knows.", out.name, spec.Image)
		return
	}
	for _, m := range spec.Mounts {
		if m.Target == dbx.PostgresDataDir && (m.Type == "volume" || m.Type == "") && m.Source != "" {
			out.volume = m.Source
		}
	}
	if out.volume == "" {
		out.reason = fmt.Sprintf("%s keeps its data outside a named volume, so a copy cannot be recovered into a volume beside it.", out.name)
		return
	}
	if !dbx.IsPostgresServerCommand(spec.Command) {
		out.reason = fmt.Sprintf("%s is started with a command of its own rather than the server's, so the archive's settings cannot be given on it.", out.name)
		return
	}
	out.account.User = "postgres"
	for _, e := range spec.Env {
		switch e.Name {
		case "POSTGRES_USER":
			if e.Value != "" {
				out.account.User = e.Value
			}
		case "POSTGRES_PASSWORD":
			out.account.Password = e.Value
		}
	}
	out.account.Port = 5432
	if port, err := strconv.Atoi(dbx.ReadPostgresCommandSettings(spec.Command)["port"]); err == nil && port > 0 {
		out.account.Port = port
	}
}

// pitrStorage is an archive's destination as WAL-G inside the container
// addresses it, with its keys opened.
func (s *Server) pitrStorage(a *pitrArchive) (dbx.WALStorage, error) {
	secrets, err := s.pitrSecrets(a)
	if err != nil {
		return dbx.WALStorage{}, err
	}
	return dbx.WALStorage{
		Kind: string(a.TargetKind), Bucket: a.Target.Bucket, Region: a.Target.Region,
		Endpoint: a.Target.Endpoint, Prefix: a.Target.Prefix,
		AccessKeyID: secrets.AccessKeyID, SecretAccessKey: secrets.SecretAccessKey,
	}, nil
}

// pitrArchiveDir is a directory destination's folder for one archive, on this
// server.
func pitrArchiveDir(a *pitrArchive) string {
	return strings.TrimRight(a.Target.Path, "/") + "/" + a.Stream
}

// pitrMounts is what a container running WAL-G for an archive mounts: the
// tool and its configuration, read-only, and a directory destination.
func pitrMounts(toolVolume string, a *pitrArchive, archiveReadOnly bool) []dockerx.MountSpec {
	mounts := []dockerx.MountSpec{{Type: "volume", Source: toolVolume, Target: dbx.WALGMount, ReadOnly: true}}
	if a.TargetKind == backups.TargetLocal {
		mounts = append(mounts, dockerx.MountSpec{Type: "bind", Source: pitrArchiveDir(a), Target: dbx.WALGArchiveMount, ReadOnly: archiveReadOnly})
	}
	return mounts
}

// withPITRMounts is a container's mounts with this feature's taken out and,
// when given, put back as they should be.
func withPITRMounts(mounts []dockerx.MountSpec, add []dockerx.MountSpec) []dockerx.MountSpec {
	out := []dockerx.MountSpec{}
	for _, m := range mounts {
		if m.Target == dbx.WALGMount || m.Target == dbx.WALGArchiveMount {
			continue
		}
		out = append(out, m)
	}
	return append(out, add...)
}

// pitrAccount reads the server's account out of an image: who the archive's
// files must belong to for the server to read them.
func (s *Server) pitrAccount(ctx context.Context, container string) (uid, gid int, err error) {
	passwd, err := s.modules.docker.ReadContainerFile(ctx, container, "/etc/passwd")
	if err != nil {
		return 0, 0, fmt.Errorf("the image's accounts could not be read: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(passwd))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 4 || fields[0] != "postgres" {
			continue
		}
		uid, uerr := strconv.Atoi(fields[2])
		gid, gerr := strconv.Atoi(fields[3])
		if uerr != nil || gerr != nil {
			break
		}
		return uid, gid, nil
	}
	return 0, 0, errors.New("the image has no postgres account")
}

// installWALG fills a volume with WAL-G and one configuration, through a
// container of the server's image that is created and never started: the
// files land with the ownership the archive needs, and the image's own
// accounts are read from it. It answers the build of WAL-G it installed.
func (s *Server) installWALG(ctx context.Context, volume, image, owner string, cfg map[string]string, withTool bool) (string, error) {
	docker := s.modules.docker
	if _, err := docker.CreateVolume(ctx, dockerx.VolumeSpec{Name: volume, Labels: map[string]string{pitrLabel: owner}}); err != nil {
		if exists, xerr := docker.VolumeExists(ctx, volume); xerr != nil || !exists {
			return "", err
		}
	}
	helper, err := docker.Create(ctx, dockerx.ContainerSpec{
		Name: volume + "-setup-" + randomSuffix(), Image: image, Entrypoint: []string{"true"},
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: volume, Target: dbx.WALGMount}},
		Labels: []dockerx.LabelSpec{{Name: pitrLabel, Value: owner}}, RestartPolicy: "no",
	}, nil)
	if err != nil {
		return "", fmt.Errorf("a container to install WAL-G with could not be made: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		// The image declares its data directory a volume, and the helper got
		// an anonymous one for it; the named volume it filled is kept.
		_ = docker.RemoveContainer(cleanup, helper.ID, true, true)
	}()
	uid, gid, err := s.pitrAccount(ctx, helper.ID)
	if err != nil {
		return "", err
	}
	digest := ""
	if withTool {
		tool, err := dbx.LoadWALGTool()
		if err != nil {
			return "", err
		}
		var archive bytes.Buffer
		if err := tool.WriteTar(&archive); err != nil {
			return "", err
		}
		if err := docker.CopyArchive(ctx, helper.ID, dbx.WALGMount, &archive); err != nil {
			return "", fmt.Errorf("WAL-G could not be copied into %s: %w", volume, err)
		}
		digest = tool.Digest
	}
	var config bytes.Buffer
	if err := dbx.WriteWALGConfigTar(&config, cfg, uid, gid); err != nil {
		return "", err
	}
	if err := docker.CopyArchive(ctx, helper.ID, dbx.WALGMount, &config); err != nil {
		return "", fmt.Errorf("WAL-G's configuration could not be written into %s: %w", volume, err)
	}
	return digest, nil
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// walgResult is what one WAL-G command said: its standard output, which is
// what its listings are, and the end of what it printed besides.
type walgResult struct {
	stdout bytes.Buffer
	tail   []string
}

func (w *walgResult) line(line dockerx.LogLine, progress func(string)) {
	if line.Stream == "stdout" {
		if w.stdout.Len() < 16<<20 {
			w.stdout.WriteString(line.Text)
			w.stdout.WriteByte('\n')
		}
		return
	}
	w.tail = append(w.tail, line.Text)
	if len(w.tail) > 40 {
		w.tail = w.tail[len(w.tail)-40:]
	}
	if progress != nil {
		progress(line.Text)
	}
}

// failure is the error WAL-G gave for a command that did not succeed: its
// last ERROR line, which names the cause, else the last thing it said.
func (w *walgResult) failure(code int) error {
	for i := len(w.tail) - 1; i >= 0; i-- {
		if _, message, ok := strings.Cut(w.tail[i], "ERROR: "); ok {
			fields := strings.SplitN(strings.TrimSpace(message), " ", 3)
			if len(fields) == 3 && strings.Count(fields[0], "/") == 2 {
				return errors.New("WAL-G: " + fields[2])
			}
			return errors.New("WAL-G: " + message)
		}
	}
	if len(w.tail) > 0 {
		return fmt.Errorf("WAL-G exited with %d: %s", code, w.tail[len(w.tail)-1])
	}
	return fmt.Errorf("WAL-G exited with %d", code)
}

// execWALG runs WAL-G in a server's running container, as its account.
func (s *Server) execWALG(ctx context.Context, container string, args []string, progress func(string)) (*walgResult, error) {
	res := &walgResult{}
	code, err := s.modules.docker.ExecStream(ctx, container, append([]string{dbx.WALGCommand}, args...), "postgres", nil,
		func(line dockerx.LogLine) { res.line(line, progress) })
	if err != nil {
		return res, err
	}
	if code != 0 {
		return res, res.failure(code)
	}
	return res, nil
}

// runWALG runs WAL-G in a container of its own made from image, with the
// tool volume and a directory destination mounted, and removes it however
// the command ends. It is how an archive is read, pruned or restored from
// while its server is not running.
func (s *Server) runWALG(ctx context.Context, image, owner string, mounts []dockerx.MountSpec, args []string, progress func(string)) (*walgResult, error) {
	res := &walgResult{}
	lines := make(chan dockerx.LogLine, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for line := range lines {
			res.line(line, progress)
		}
	}()
	code, removed, err := s.modules.docker.RunToCompletion(ctx, dockerx.ContainerSpec{
		Name: owner + "-walg-" + randomSuffix(), Image: image, User: "postgres",
		Entrypoint: []string{dbx.WALGCommand}, Command: args, Mounts: mounts,
		Labels: []dockerx.LabelSpec{{Name: pitrLabel, Value: owner}},
		// WAL-G streams what it moves; its memory does not grow with the
		// database, and a run of it is not allowed to take the host's.
		Limits: dockerx.ResourceLimits{MemoryMB: 512},
	}, lines)
	close(lines)
	<-done
	if err == nil && !removed {
		err = errors.New("the container WAL-G ran in could not be removed")
	}
	if err != nil {
		return res, err
	}
	if code != 0 {
		return res, res.failure(code)
	}
	return res, nil
}

// walgFor runs a WAL-G command against an archive wherever it can be run:
// in its server while the server is running and still writes into it, in a
// container with the server's own tool volume while the server is stopped,
// and otherwise in a container with a volume made for the one command and
// removed after it.
func (s *Server) walgFor(ctx context.Context, srv *pitrServer, a *pitrArchive, args []string, progress func(string)) (*walgResult, error) {
	active := a.EndedAt == nil && srv != nil && srv.name == a.Container
	if active && srv.running {
		return s.execWALG(ctx, srv.id, args, progress)
	}
	image := a.Image
	if srv != nil && srv.image != "" {
		image = srv.image
	}
	if active {
		if exists, err := s.modules.docker.VolumeExists(ctx, pitrToolVolume(a.Stream)); err == nil && exists {
			return s.runWALG(ctx, image, a.Container, pitrMounts(pitrToolVolume(a.Stream), a, false), args, progress)
		}
	}
	volume := "jd-pitr-run-" + randomSuffix()
	cfg, err := s.pitrConfig(a, a.publicKey, dbx.WALGAccount{})
	if err != nil {
		return nil, err
	}
	if _, err := s.installWALG(ctx, volume, image, a.Container, cfg, true); err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = s.modules.docker.RemoveVolume(cleanup, volume, true)
	}()
	return s.runWALG(ctx, image, a.Container, pitrMounts(volume, a, false), args, progress)
}

// pitrConfig is the configuration WAL-G is given for an archive.
func (s *Server) pitrConfig(a *pitrArchive, key string, account dbx.WALGAccount) (map[string]string, error) {
	storage, err := s.pitrStorage(a)
	if err != nil {
		return nil, err
	}
	return dbx.WALGConfig(storage, a.Stream, key, account)
}

// readPITRArchive asks the destination what an archive holds and keeps the
// answer.
func (s *Server) readPITRArchive(ctx context.Context, srv *pitrServer, a *pitrArchive) (*pitrReading, error) {
	listed, err := s.walgFor(ctx, srv, a, []string{"backup-list", "--json", "--detail"}, nil)
	if err != nil {
		return nil, err
	}
	backups, err := dbx.ParseWALGBackups(listed.stdout.Bytes())
	if err != nil {
		return nil, err
	}
	objects, err := s.walgFor(ctx, srv, a, []string{"st", "ls", "wal_005/"}, nil)
	if err != nil {
		return nil, err
	}
	reading := &pitrReading{Backups: backups, ReadAt: time.Now().UTC()}
	segments := dbx.ParseWALGObjects(objects.stdout.Bytes())
	var latest time.Time
	if newest, ok := dbx.LatestWALSegment(segments); ok {
		reading.Latest = strings.SplitN(newest.Name, ".", 2)[0]
		latest = newest.Modified
		reading.LatestAt = &latest
	}
	for _, o := range segments {
		reading.Size += o.Size
	}
	for _, b := range backups {
		reading.Size += b.Size
	}
	reading.Window = dbx.RecoverableWindow(backups, latest)
	if err := s.recordPITRReading(ctx, a.ID, reading); err != nil {
		return nil, err
	}
	a.Reading = reading
	return reading, nil
}

// pitrState is what point-in-time recovery keeps in memory: one reading of
// each archive at a time, and the schedule of base backups.
type pitrState struct {
	reading sync.Map // archive id → *sync.Mutex
	sched   pitrSchedule
}

// freshPITRReading reads an archive again when the kept reading is older
// than pitrReadEvery. A reading already under way for the same archive is not
// waited for: the kept one is answered, as it is when the destination does
// not answer, with the error beside it.
func (s *Server) freshPITRReading(ctx context.Context, srv *pitrServer, a *pitrArchive) string {
	if a.Reading != nil && time.Since(a.Reading.ReadAt) < pitrReadEvery {
		return ""
	}
	lock, _ := s.pitr.reading.LoadOrStore(a.ID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	if !mu.TryLock() {
		return ""
	}
	defer mu.Unlock()
	if _, err := s.readPITRArchive(ctx, srv, a); err != nil {
		return err.Error()
	}
	return ""
}

// pitrRefused is the answer to a request this server cannot carry out.
func pitrRefused(reason string) error {
	return httpx.Err(http.StatusConflict, "pitr_unavailable", reason)
}

// removeContainerAndVolumes takes away a container this feature made and the
// volumes named, for a recovery that did not finish. Nothing that was there
// before is among them.
func (s *Server) removeContainerAndVolumes(ctx context.Context, container string, volumes ...string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if container != "" {
		if err := s.modules.docker.RemoveContainer(cleanup, container, true, false); err != nil && !errdefs.IsNotFound(err) {
			s.Log.Warn("a recovered copy that did not finish could not be removed", "container", container, "err", err)
		}
	}
	for _, v := range volumes {
		if err := s.modules.docker.RemoveVolume(cleanup, v, true); err != nil && !errdefs.IsNotFound(err) {
			s.Log.Warn("a volume of a recovered copy that did not finish could not be removed", "volume", v, "err", err)
		}
	}
}
