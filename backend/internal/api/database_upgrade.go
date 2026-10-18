package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/docker/docker/errdefs"
)

// A major-version upgrade, carried out.
//
// The server is read where it runs and copied into a new container of the
// newer release, beside it, with the server's own tools: pg_dumpall for the
// roles and pg_dump into pg_restore for each database, run inside the two
// containers through `docker exec` with the bytes passing through this
// process. Nothing is copied over a network between the containers, nothing
// is run on the host, and no shell is involved: every command is an argv, and
// the roles' script is replayed by the server over a connection after the
// dashboard's own reader has cut it into statements.
//
// The old server is not touched until its copy can be made: before that the
// new release is started and asked what it carries, and a safety dump is
// taken. While the copy is made the old server takes reads only, so nothing
// written to it is lost with it. Only once the copy has been restarted and
// counted, table by table, does the new server take the old one's place: the
// old container is stopped and set aside under another name, kept with its
// data, and the new one is created under its name, on its port and its
// networks, with its settings. The saved connection, the deployments'
// ${{database.ID}} references and the port's exposure all name that place, so
// none of them changes; the deployment networks are attached to the new
// container at once rather than at reconciliation's next pass.
//
// Whatever fails before the cutover is undone by removing what was made and
// letting the old server take writes again; whatever fails during it, by
// putting the old container back. Each step is recorded in db_upgrades before
// it is taken, so a dashboard that stops in the middle puts the old server
// back when it starts (recoverDatabaseUpgrades).

// upgradeEngines are the templates whose servers are upgraded here:
// PostgreSQL and the two variants that differ from it by one extension.
// TimescaleDB is a template too and is left out, for the reason dbx's
// majorUpgrade flag gives.
var upgradeEngines = []string{"postgres", "pgvector", "postgis"}

const (
	// upgradeReady bounds a server's start: an initdb, the image's own
	// first start and the restart it does after it.
	upgradeReady = 2 * time.Minute
	// upgradeCutover bounds the switch itself. It runs on a context of its
	// own, because a cancel that lands between stopping one server and
	// starting the other would leave neither.
	upgradeCutover = 10 * time.Minute
	// upgradeRoleLabel marks the containers an upgrade makes for itself —
	// the release it asks what it carries, and the copy before it takes the
	// old one's place — so one left by a dashboard that stopped can be told
	// from anything else of that name.
	upgradeRoleLabel = "io.just-dashboard.database-upgrade"
	// upgradeOutputMax bounds what a psql answer is read into.
	upgradeOutputMax = 32 << 20
)

// upgradeSubject is a server this route can upgrade, read off the machine:
// the running container that publishes the connection's port, the template
// its image is a release of, and the newer releases of that template.
type upgradeSubject struct {
	conn   *dbConnection
	dsn    string
	info   *dbx.ConnInfo
	detail *dockerx.ContainerDetail
	engine string
	tmpl   provisionTemplate
	from   provisionVersion
	// targets are the template's newer releases, newest first.
	targets []provisionVersion
	// side is how the container's own clients sign in: as the account the
	// image made the server with, over the socket.
	side pgSide
	// bootstrap is the password the image makes a new server's account with.
	bootstrap string
	// data is the mount that holds the data directory.
	data dockerx.MountPoint
}

// upgradeUnavailable is the refusal of a server this cannot upgrade, which
// the status read also gives as its reason.
func upgradeUnavailable(format string, args ...any) error {
	return httpx.Err(http.StatusConflict, "upgrade_unavailable", fmt.Sprintf(format, args...))
}

// upgradeSubject resolves the server behind a connection, or says why it is
// not one this upgrades.
func (s *Server) upgradeSubject(ctx context.Context, conn *dbConnection, dsn string) (*upgradeSubject, error) {
	if conn.Driver != dbx.DriverPostgres {
		return nil, upgradeUnavailable("Only a PostgreSQL server started from Databases is upgraded here.")
	}
	if s.modules.docker == nil {
		return nil, httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return nil, httpx.BadRequest("the saved connection could not be read")
	}
	if !databaseLoopback(info.Host) {
		return nil, upgradeUnavailable("%s is reached at %s, not on this machine, so it is upgraded where it runs.", conn.Name, info.Host)
	}
	server := s.serverBehind(ctx, conn, info)
	if server == nil {
		return nil, upgradeUnavailable("No running container on this machine publishes port %s. Start the server first: it is read while it runs.", info.Port)
	}
	if project := server.container.Labels["com.docker.compose.project"]; project != "" {
		return nil, upgradeUnavailable("%s belongs to the compose project %s; change its image in the compose file instead.", server.container.Name, project)
	}
	detail, err := s.modules.docker.Inspect(ctx, server.container.ID)
	if err != nil {
		return nil, s.dockerErr(err)
	}
	engine, tmpl, from, ok := upgradeTemplateOf(detail.Image)
	if !ok {
		return nil, upgradeUnavailable("%s runs %s, which is not one of the images Databases starts servers from, so the release to move it to is not known here.", detail.Name, detail.Image)
	}
	env := envMap(detail.Env)
	data, ok := upgradeDataMount(detail, env["PGDATA"], tmpl.dataPath)
	if !ok {
		return nil, upgradeUnavailable("%s keeps its data in the container itself rather than on a volume, so the old data could not be kept beside the new.", detail.Name)
	}
	superuser := env["POSTGRES_USER"]
	if superuser == "" {
		superuser = "postgres"
	}
	// Over the socket the image's own rules let its account in without a
	// password; it is handed one in case somebody changed them.
	password := env["POSTGRES_PASSWORD"]
	if info.User == superuser && info.Password != "" {
		password = info.Password
	}
	sub := &upgradeSubject{
		conn: conn, dsn: dsn, info: info, detail: detail, engine: engine, tmpl: tmpl, from: from,
		targets: []provisionVersion{}, bootstrap: env["POSTGRES_PASSWORD"], data: data,
		side: pgSide{container: detail.ID, user: superuser, password: password, port: tmpl.port},
	}
	for _, v := range tmpl.versions {
		if versionNewer(v.id, from.id) {
			sub.targets = append(sub.targets, v)
		}
	}
	return sub, nil
}

// upgradeTemplateOf finds the template and release an image reference is.
func upgradeTemplateOf(image string) (string, provisionTemplate, provisionVersion, bool) {
	for _, engine := range upgradeEngines {
		tmpl := provisionTemplates[engine]
		for _, v := range tmpl.versions {
			if sameImage(v.image, image) {
				return engine, tmpl, v, true
			}
		}
	}
	return "", provisionTemplate{}, provisionVersion{}, false
}

// sameImage compares two references the way the Engine resolves them: a bare
// repository is Docker Hub's library.
func sameImage(a, b string) bool {
	norm := func(ref string) string {
		ref = dockerx.NormalizeImageRef(ref)
		ref = strings.TrimPrefix(ref, "docker.io/")
		return strings.TrimPrefix(ref, "library/")
	}
	return norm(a) == norm(b)
}

// upgradeDataMount is the volume that holds the data directory: the one
// mounted at it, or at a directory above it.
func upgradeDataMount(detail *dockerx.ContainerDetail, pgdata, fallback string) (dockerx.MountPoint, bool) {
	if pgdata == "" {
		pgdata = fallback
	}
	for _, m := range detail.Mounts {
		if m.Type != "volume" || m.Name == "" {
			continue
		}
		dest := strings.TrimSuffix(m.Destination, "/")
		if pgdata == dest || strings.HasPrefix(pgdata, dest+"/") {
			return m, true
		}
	}
	return dockerx.MountPoint{}, false
}

// versionNewer reports whether one release id is a later release than
// another, part by part: 17 after 16, 11.4 after 10.11.
func versionNewer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// upgradePlan is one server and the release it is moved to, with the names
// the containers and the volume take.
type upgradePlan struct {
	*upgradeSubject
	to provisionVersion
	// previous is where the old container is kept; candidate is the new
	// release while it is copied into; probe is the release asked what it
	// carries before anything is copied; volume holds the new data.
	previous, candidate, probe, volume string
}

// plan picks the release a request named, which has to be one of the
// template's newer ones.
func (sub *upgradeSubject) plan(version string) (*upgradePlan, error) {
	version = strings.TrimSpace(version)
	for _, v := range sub.targets {
		if v.id != version {
			continue
		}
		name := sub.detail.Name
		next := "pg" + v.id
		return &upgradePlan{
			upgradeSubject: sub, to: v,
			previous:  name + "-pg" + sub.from.id,
			candidate: name + "-" + next + "-upgrade",
			probe:     name + "-" + next + "-check",
			volume:    name + "-" + next + "-data",
		}, nil
	}
	if len(sub.targets) == 0 {
		return nil, upgradeUnavailable("%s %s is the newest release Databases offers.", sub.tmpl.label, sub.from.id)
	}
	ids := make([]string, 0, len(sub.targets))
	for _, v := range sub.targets {
		ids = append(ids, v.id)
	}
	return nil, httpx.BadRequest("%s %s is upgraded to %s", sub.tmpl.label, sub.from.id, strings.Join(ids, " or "))
}

// --- the servers' own clients ------------------------------------------------

// pgSide is a PostgreSQL server reached through its container: its clients
// run inside it, as the image's postgres account, over its socket.
type pgSide struct {
	container string
	user      string
	password  string
	// port is where the server listens inside the container.
	port int
}

func (p pgSide) env() []string {
	env := []string{"PGCONNECT_TIMEOUT=15"}
	if p.password != "" {
		env = append(env, "PGPASSWORD="+p.password)
	}
	return env
}

// tailBuffer keeps the end of what a tool printed: the line that says why it
// stopped is the last one.
type tailBuffer struct{ buf []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - 4096; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	return strings.TrimSpace(strings.ToValidUTF8(string(t.buf), ""))
}

// cappedBuffer holds a whole answer, up to a bound past which it is not one
// a query here gives.
type cappedBuffer struct{ bytes.Buffer }

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.Len()+len(p) > upgradeOutputMax {
		return 0, errors.New("the answer is larger than any this reads")
	}
	return c.Buffer.Write(p)
}

// pgTool runs one of the server's client programs in its container.
func (s *Server) pgTool(ctx context.Context, side pgSide, cmd []string, streams dockerx.ExecStreams) error {
	var stderr tailBuffer
	if streams.Stderr == nil {
		streams.Stderr = &stderr
	}
	code, err := s.modules.docker.ExecPipe(ctx, side.container, "postgres", cmd, side.env(), streams)
	if err != nil {
		return fmt.Errorf("%s: %w", cmd[0], err)
	}
	if code != 0 {
		if said := stderr.String(); said != "" {
			return fmt.Errorf("%s stopped: %s", cmd[0], said)
		}
		return fmt.Errorf("%s exited with code %d", cmd[0], code)
	}
	return nil
}

func psqlArgs(side pgSide, database string) []string {
	return []string{"psql", "-X", "-w", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", side.user, "-d", dbx.PostgresConnInfo(database)}
}

// psqlJSON runs one query that answers one JSON value, and decodes it.
func (s *Server) psqlJSON(ctx context.Context, side pgSide, database, query string, into any) error {
	var stdout cappedBuffer
	if err := s.pgTool(ctx, side, append(psqlArgs(side, database), "-c", query), dockerx.ExecStreams{Stdout: &stdout}); err != nil {
		return err
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), into); err != nil {
		return fmt.Errorf("psql's answer could not be read: %w", err)
	}
	return nil
}

// psqlRun runs statements one at a time, each its own transaction, as
// ALTER SYSTEM and DROP DATABASE have to be.
func (s *Server) psqlRun(ctx context.Context, side pgSide, database string, statements ...string) error {
	args := psqlArgs(side, database)
	for _, statement := range statements {
		args = append(args, "-c", statement)
	}
	return s.pgTool(ctx, side, args, dockerx.ExecStreams{})
}

// awaitPostgres waits for a server's container to answer over TCP. The
// image's first start runs initdb on a server that listens on its socket
// only, then restarts it; answering on the address is the real start.
func (s *Server) awaitPostgres(ctx context.Context, side pgSide) error {
	deadline := time.Now().Add(upgradeReady)
	for {
		err := s.pgTool(ctx, side, []string{"pg_isready", "-q", "-h", "127.0.0.1", "-p", strconv.Itoa(side.port), "-U", side.user}, dockerx.ExecStreams{})
		if err == nil {
			return nil
		}
		detail, ierr := s.modules.docker.Inspect(ctx, side.container)
		if ierr != nil {
			return ierr
		}
		if detail.State != "running" {
			return fmt.Errorf("%s stopped while it was starting: %s", detail.Name, s.containerLogTail(ctx, side.container))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer within %s: %s", detail.Name, upgradeReady, s.containerLogTail(ctx, side.container))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// containerLogTail is the last lines a container printed, which is where a
// server that would not start says why.
func (s *Server) containerLogTail(ctx context.Context, id string) string {
	read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	lines, closer, err := s.modules.docker.Logs(read, id, dockerx.LogOptions{Tail: "8"})
	if err != nil {
		return "its log could not be read"
	}
	defer closer.Close()
	out := []string{}
	for line := range lines {
		if text := strings.TrimSpace(line.Text); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return "it printed nothing"
	}
	return strings.Join(out, " / ")
}

// --- the containers -----------------------------------------------------------

// upgradeSpec is the container a newer release runs as: the old container's
// own settings as the create form reads them back, with what the old image
// supplied left for the new image to supply. A read-back cannot tell an
// image's defaults from the operator's choices — PATH, PG_MAJOR and
// PG_VERSION are the old image's environment, and its command and labels are
// the old image's too — and carried into the new image they would run the
// old release's paths in the new one. The data volume is the new one,
// mounted where the new image keeps its data.
func upgradeSpec(old dockerx.ContainerSpec, oldID string, oldImage, newImage *dockerx.ImageDetail, image string, data dockerx.MountPoint, volume string) dockerx.ContainerSpec {
	spec := old
	spec.Image = image
	defaults := make(map[string]bool, len(oldImage.Env))
	for _, e := range oldImage.Env {
		defaults[e] = true
	}
	spec.Env = []dockerx.EnvVar{}
	for _, e := range old.Env {
		if !defaults[e.Name+"="+e.Value] {
			spec.Env = append(spec.Env, e)
		}
	}
	if slices.Equal(old.Command, oldImage.Command) {
		spec.Command = nil
	}
	if slices.Equal(old.Entrypoint, oldImage.Entrypoint) {
		spec.Entrypoint = nil
	}
	if old.WorkingDir == oldImage.WorkingDir {
		spec.WorkingDir = ""
	}
	if old.User == oldImage.User {
		spec.User = ""
	}
	// Docker names a container's host after its id unless told otherwise.
	if len(oldID) >= 12 && old.Hostname == oldID[:12] {
		spec.Hostname = ""
	}
	spec.Labels = []dockerx.LabelSpec{}
	for _, l := range old.Labels {
		if v, ok := oldImage.Labels[l.Name]; ok && v == l.Value {
			continue
		}
		spec.Labels = append(spec.Labels, l)
	}
	target := data.Destination
	if !slices.ContainsFunc(spec.Env, func(e dockerx.EnvVar) bool { return e.Name == "PGDATA" }) {
		// PostgreSQL 18's image keeps its data a directory further down, under
		// a volume declared one level up; the new release decides where.
		pgdata := envMap(newImage.Env)["PGDATA"]
		for _, path := range newImage.VolumePaths {
			path = strings.TrimSuffix(path, "/")
			if pgdata == path || strings.HasPrefix(pgdata, path+"/") {
				target = path
				break
			}
		}
	}
	spec.Mounts = []dockerx.MountSpec{}
	for _, m := range old.Mounts {
		if m.Target == data.Destination {
			spec.Mounts = append(spec.Mounts, dockerx.MountSpec{Type: "volume", Source: volume, Target: target})
			continue
		}
		spec.Mounts = append(spec.Mounts, m)
	}
	spec.Start, spec.Pull = true, "missing"
	return spec
}

// scratchSpec is a container an upgrade makes for itself from the new
// release's spec: on the default network, restarting never, published on
// loopback only where the dashboard has to reach it, and labelled with what
// it is for. The image's first-start scripts are left out, because what they
// would create is about to arrive with the copy; and the release asked what
// it carries keeps nothing, so its data goes in the volume the image declares.
func scratchSpec(spec dockerx.ContainerSpec, name, role string, port, containerPort int, dataTarget string) dockerx.ContainerSpec {
	spec.Name = name
	spec.RestartPolicy, spec.MaxRetries = "no", 0
	spec.Networks, spec.NetworkMode = nil, ""
	spec.Ports = nil
	if port > 0 {
		spec.Ports = []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: containerPort, Protocol: "tcp"}}
	}
	spec.Labels = append(slices.Clone(spec.Labels), dockerx.LabelSpec{Name: upgradeRoleLabel, Value: role})
	mounts := []dockerx.MountSpec{}
	for _, m := range spec.Mounts {
		if strings.HasPrefix(m.Target+"/", "/docker-entrypoint-initdb.d/") {
			continue
		}
		if role == "check" && m.Target == dataTarget {
			continue
		}
		mounts = append(mounts, m)
	}
	spec.Mounts = mounts
	return spec
}

// upgradeImages reads the configuration the old container was created from —
// by the image it runs, which outlives a tag that has moved on — and the new
// release's, pulling it first where it is not on this machine yet.
func (s *Server) upgradeImages(ctx context.Context, plan *upgradePlan, out jobs.Emitter) (oldImage, newImage *dockerx.ImageDetail, err error) {
	if !s.modules.docker.ImagePresent(ctx, plan.to.image) {
		out.Status("Pulling %s", plan.to.image)
		progress := make(chan dockerx.PullProgress, 64)
		done := make(chan struct{})
		go func() {
			defer close(done)
			last := ""
			for p := range progress {
				// One line per layer state, not per byte.
				if line := strings.TrimSpace(p.ID + " " + p.Status); p.Progress == "" && line != last {
					out.Line("stdout", line)
					last = line
				}
			}
		}()
		err = s.modules.docker.PullImage(ctx, plan.to.image, progress)
		close(progress)
		<-done
		if err != nil {
			return nil, nil, fmt.Errorf("could not pull %s: %w", plan.to.image, err)
		}
	}
	if oldImage, err = s.modules.docker.InspectImage(ctx, plan.detail.ImageID); err != nil {
		if oldImage, err = s.modules.docker.InspectImage(ctx, plan.detail.Image); err != nil {
			return nil, nil, fmt.Errorf("the image %s was started from is no longer on this machine: %w", plan.detail.Name, err)
		}
	}
	if newImage, err = s.modules.docker.InspectImage(ctx, plan.to.image); err != nil {
		return nil, nil, err
	}
	return oldImage, newImage, nil
}

// startScratch creates and starts one of the upgrade's own containers and
// waits for its server to answer. A container left under the name by an
// upgrade that stopped is removed first; anything else of that name is not
// the upgrade's, and stops it.
func (s *Server) startScratch(ctx context.Context, spec dockerx.ContainerSpec, side *pgSide) (*dockerx.CreateResult, error) {
	if err := s.removeUpgradeLeftover(ctx, spec.Name); err != nil {
		return nil, err
	}
	created, err := s.modules.docker.Create(ctx, spec, nil)
	if err != nil {
		if created != nil && created.ID != "" {
			s.removeScratch(ctx, created.ID, "")
		}
		return nil, fmt.Errorf("could not start %s: %w", spec.Name, err)
	}
	side.container = created.ID
	if err := s.awaitPostgres(ctx, *side); err != nil {
		return created, err
	}
	return created, nil
}

// removeUpgradeLeftover removes a container of the upgrade's own under a
// name it is about to use again.
func (s *Server) removeUpgradeLeftover(ctx context.Context, name string) error {
	detail, err := s.modules.docker.Inspect(ctx, name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if detail.Labels[upgradeRoleLabel] == "" {
		return fmt.Errorf("a container named %s already exists and is not one an upgrade made; rename or remove it first", name)
	}
	return s.modules.docker.RemoveContainer(ctx, detail.ID, true, true)
}

// removeScratch removes one of the upgrade's own containers, with the volume
// it was given, however the work around it ended.
func (s *Server) removeScratch(ctx context.Context, id, volume string) []string {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	problems := []string{}
	if id != "" {
		if err := s.modules.docker.RemoveContainer(cleanup, id, true, true); err != nil && !errdefs.IsNotFound(err) {
			problems = append(problems, err.Error())
		}
	}
	if volume != "" {
		if err := s.modules.docker.RemoveVolume(cleanup, volume, true); err != nil && !errdefs.IsNotFound(err) {
			problems = append(problems, fmt.Sprintf("volume %s: %v", volume, err))
		}
	}
	return problems
}

// --- the checks --------------------------------------------------------------

// upgradeReport is what the checks found: the reading a check ends on, and
// the first thing an upgrade reads before it changes anything.
type upgradeReport struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Server string `json:"server"`
	Target string `json:"target"`
	Image  string `json:"image"`
	// Bytes is every database's size, which is about what is copied.
	Bytes     int64                  `json:"bytes"`
	Databases []dbx.PostgresDatabase `json:"databases"`
	Checks    []dbx.UpgradeCheck     `json:"checks"`
	// Ready is no check stopping the upgrade.
	Ready     bool      `json:"ready"`
	CheckedAt time.Time `json:"checkedAt"`
}

// blockers are the reasons a report stops an upgrade, in one sentence each.
func (r *upgradeReport) blockers() []string {
	out := []string{}
	for _, c := range r.Checks {
		if c.Verdict == dbx.UpgradeBlocker {
			out = append(out, c.Title+": "+c.Detail)
		}
	}
	return out
}

// readUpgradeReport asks the old server and the new release everything the
// checks need, and what the old server holds.
func (s *Server) readUpgradeReport(ctx context.Context, plan *upgradePlan, old, target pgSide, names dbx.UpgradeCheck) (*upgradeReport, *dbx.PostgresServerFacts, *dbx.PostgresServerFacts, error) {
	from, err := s.postgresFacts(ctx, old)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not read %s: %w", plan.detail.Name, err)
	}
	installed := map[string][]dbx.PostgresExtension{}
	for _, db := range from.Databases {
		var exts []dbx.PostgresExtension
		if err := s.psqlJSON(ctx, old, db.Name, dbx.PostgresExtensionsQuery, &exts); err != nil {
			return nil, nil, nil, fmt.Errorf("could not read the extensions of %s: %w", db.Name, err)
		}
		installed[db.Name] = exts
	}
	to, err := s.postgresFacts(ctx, target)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not read PostgreSQL %s: %w", plan.to.id, err)
	}
	to.Loadable = map[string]bool{}
	for _, library := range dbx.PostgresPreloadLibraries(from.Preload) {
		to.Loadable[library] = s.psqlRun(ctx, target, "postgres", dbx.PostgresLoad(library)) == nil
	}
	report := &upgradeReport{
		From: plan.from.id, To: plan.to.id, Server: from.Version, Target: to.Version, Image: plan.to.image,
		Databases: from.Databases, CheckedAt: time.Now().UTC(),
	}
	for _, db := range from.Databases {
		report.Bytes += db.Bytes
	}
	checks := dbx.PostgresUpgradeChecks(from, to, installed)
	// The machine's two answers go after the servers' own: the version and
	// the extensions are what a reader looks for first.
	report.Checks = slices.Insert(checks, min(2, len(checks)), s.upgradeDiskCheck(ctx, plan, old, from), names)
	report.Ready = len(report.blockers()) == 0
	return report, from, to, nil
}

func (s *Server) postgresFacts(ctx context.Context, side pgSide) (*dbx.PostgresServerFacts, error) {
	var raw json.RawMessage
	if err := s.psqlJSON(ctx, side, "postgres", dbx.PostgresServerFactsQuery, &raw); err != nil {
		return nil, err
	}
	return dbx.ParsePostgresServerFacts(raw)
}

// upgradeNamesCheck says where the old server and the new data will be, and
// stops the upgrade where either name is taken by something it did not make.
func (s *Server) upgradeNamesCheck(ctx context.Context, plan *upgradePlan) dbx.UpgradeCheck {
	c := dbx.UpgradeCheck{ID: "names", Title: "Containers and volume", Verdict: dbx.UpgradeOK}
	taken := []string{}
	for _, name := range []string{plan.previous, plan.candidate, plan.probe} {
		detail, err := s.modules.docker.Inspect(ctx, name)
		if err == nil && (name == plan.previous || detail.Labels[upgradeRoleLabel] == "") {
			taken = append(taken, "container "+name)
		}
	}
	if exists, err := s.modules.docker.VolumeExists(ctx, plan.volume); err == nil && exists {
		taken = append(taken, "volume "+plan.volume)
	}
	if len(taken) > 0 {
		c.Verdict, c.Items = dbx.UpgradeBlocker, taken
		c.Detail = "These names are taken, and the upgrade uses them for the old server and the new data. Rename or remove what holds them first."
		return c
	}
	c.Items = []string{plan.previous, plan.volume}
	c.Detail = fmt.Sprintf("The new server takes the name %s and its port. The old one is kept, stopped, as %s with its data until you finish or revert; the new data goes in %s.",
		plan.detail.Name, plan.previous, plan.volume)
	return c
}

// upgradeDiskCheck holds the copy and the safety dump to the space there is
// for them: the new volume is on the disk that holds the old one, and the
// dump goes where every dump of this connection does.
func (s *Server) upgradeDiskCheck(ctx context.Context, plan *upgradePlan, old pgSide, facts *dbx.PostgresServerFacts) dbx.UpgradeCheck {
	c := dbx.UpgradeCheck{ID: "disk", Title: "Disk space"}
	var total, own int64
	for _, db := range facts.Databases {
		total += db.Bytes
		if db.Name == plan.info.Database {
			own = db.Bytes
		}
	}
	// A restored database is about the size of the one dumped, often less:
	// it has none of the old one's dead rows. The margin is the server's
	// own write-ahead log while it loads.
	need := total + total/10 + 64<<20
	volumeFree, err := s.containerFreeBytes(ctx, old, facts.DataDirectory)
	if err != nil {
		c.Verdict = dbx.UpgradeWarning
		c.Detail = fmt.Sprintf("How much space is free for the new volume could not be read (%v). It needs about %s.", err, upgradeBytes(need))
		return c
	}
	dumpFree, err := freeBytesAt(s.dbDumpDir(plan.conn.Name))
	if err != nil {
		c.Verdict = dbx.UpgradeWarning
		c.Detail = fmt.Sprintf("How much space is free for the safety dump could not be read (%v).", err)
		return c
	}
	switch {
	case volumeFree < need:
		c.Verdict = dbx.UpgradeBlocker
		c.Detail = fmt.Sprintf("The copy needs about %s and %s is free on the disk that holds the Docker volumes.", upgradeBytes(need), upgradeBytes(volumeFree))
	case dumpFree < own:
		c.Verdict = dbx.UpgradeBlocker
		c.Detail = fmt.Sprintf("The safety dump of %s can take up to %s and %s is free where the dumps are kept.", plan.info.Database, upgradeBytes(own), upgradeBytes(dumpFree))
	default:
		c.Verdict = dbx.UpgradeOK
		c.Detail = fmt.Sprintf("The databases take %s. %s is free for the new volume and %s for the safety dump.", upgradeBytes(total), upgradeBytes(volumeFree), upgradeBytes(dumpFree))
	}
	return c
}

// containerFreeBytes is the space free on the filesystem that holds a path
// inside a container, as its own df reports it.
func (s *Server) containerFreeBytes(ctx context.Context, side pgSide, path string) (int64, error) {
	var stdout cappedBuffer
	if err := s.pgTool(ctx, side, []string{"df", "-P", "-k", path}, dockerx.ExecStreams{Stdout: &stdout}); err != nil {
		return 0, err
	}
	return parseDFAvailable(stdout.String())
}

// parseDFAvailable reads the available column of `df -P -k`.
func parseDFAvailable(text string) (int64, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) < 2 {
		return 0, errors.New("df printed no filesystem")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, errors.New("df printed a line it does not print")
	}
	kb, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}

// freeBytesAt is the space free for a directory, which may not exist yet: the
// nearest one above it that does stands for it.
func freeBytesAt(dir string) (int64, error) {
	for {
		var st syscall.Statfs_t
		err := syscall.Statfs(dir, &st)
		if err == nil {
			return int64(st.Bavail) * int64(st.Bsize), nil
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, os.ErrNotExist) || parent == dir {
			return 0, err
		}
		dir = parent
	}
}

func upgradeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sB", float64(b)/float64(div), []string{"K", "M", "G", "T", "P"}[exp])
}

// --- the check ---------------------------------------------------------------

// runUpgradeCheck starts the new release beside the old server, asks both
// what the checks need, and removes it again. Nothing on the old server
// changes.
func (s *Server) runUpgradeCheck(ctx context.Context, plan *upgradePlan, out jobs.Emitter) (*upgradeReport, error) {
	names := s.upgradeNamesCheck(ctx, plan)
	oldImage, newImage, err := s.upgradeImages(ctx, plan, out)
	if err != nil {
		return nil, err
	}
	base, err := s.modules.docker.SpecOf(ctx, plan.detail.ID)
	if err != nil {
		return nil, err
	}
	if base.AutoRemove {
		return nil, upgradeUnavailable("%s removes itself when it stops, so it could not be kept as the way back.", plan.detail.Name)
	}
	next := upgradeSpec(*base, plan.detail.ID, oldImage, newImage, plan.to.image, plan.data, plan.volume)
	dataTarget := ""
	for _, m := range next.Mounts {
		if m.Source == plan.volume {
			dataTarget = m.Target
		}
	}
	out.Status("Starting PostgreSQL %s beside it to see what it carries", plan.to.id)
	probe := pgSide{user: plan.side.user, port: plan.side.port}
	created, err := s.startScratch(ctx, scratchSpec(next, plan.probe, "check", 0, 0, dataTarget), &probe)
	if created != nil {
		defer s.removeScratch(ctx, created.ID, "")
	}
	if err != nil {
		return nil, err
	}
	out.Status("Reading %s and PostgreSQL %s", plan.detail.Name, plan.to.id)
	report, _, _, err := s.readUpgradeReport(ctx, plan, plan.side, probe, names)
	if err != nil {
		return nil, err
	}
	for _, c := range report.Checks {
		out.Line("stdout", fmt.Sprintf("%s: %s", c.Title, c.Detail))
	}
	return report, nil
}

// --- the upgrade -------------------------------------------------------------

// upgradeResult is the line an upgrade ends on.
type upgradeResult struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Server     string `json:"server"`
	Container  string `json:"container"`
	Previous   string `json:"previous"`
	Volume     string `json:"volume"`
	SafetyDump string `json:"safetyDump,omitempty"`
	Databases  int    `json:"databases"`
	Tables     int    `json:"tables"`
	Rows       int64  `json:"rows"`
	Summary    string `json:"summary"`
}

// upgradeRun is one upgrade in progress: the plan, its record, and what it
// has made so far, which is what undoing it removes.
type upgradeRun struct {
	s      *Server
	plan   *upgradePlan
	record int64
	out    jobs.Emitter
	base   audit.Entry
	by     string

	candidate pgSide
	// readOnly is set once the old server takes reads only; prior is what
	// its postgresql.auto.conf said before.
	readOnly bool
	prior    string
	// restart is the old container's restart policy, which it gets back
	// when it is put back.
	restart string
}

// step records one step of the upgrade in the audit trail.
func (u *upgradeRun) step(name string, detail map[string]any, err error) {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["step"] = name
	detail["from"], detail["to"] = u.plan.from.id, u.plan.to.id
	u.s.recordTransfer(u.base, "database.upgrade.step", u.plan.conn.Name, detail, err)
}

// runUpgrade carries one upgrade out, start to end, and undoes what it made
// when it cannot finish it.
func (s *Server) runUpgrade(ctx context.Context, u *upgradeRun) (*upgradeResult, error) {
	plan, out := u.plan, u.out
	result, err := u.copyAcross(ctx)
	if err != nil {
		u.undoCopy(ctx, err)
		return nil, err
	}
	// From here the work runs to its end however the job is stopped: a
	// cancel between the two servers changing places would leave neither.
	cut, cancel := context.WithTimeout(context.WithoutCancel(ctx), upgradeCutover)
	defer cancel()
	if err := u.cutover(cut, result); err != nil {
		return nil, err
	}
	s.markUpgraded(context.WithoutCancel(ctx), u.record)
	out.Status("Done. %s is kept, stopped, until you finish the upgrade or revert it.", plan.previous)
	return result, nil
}

// copyAcross is everything before the cutover: the checks, the safety dump,
// the new release beside the old server, and the copy into it, counted.
func (u *upgradeRun) copyAcross(ctx context.Context) (*upgradeResult, error) {
	s, plan, out := u.s, u.plan, u.out
	out.Status("Checking %s before anything changes", plan.detail.Name)
	names := s.upgradeNamesCheck(ctx, plan)
	if names.Verdict == dbx.UpgradeBlocker {
		return nil, fmt.Errorf("%s %s", names.Detail, strings.Join(names.Items, ", "))
	}
	oldImage, newImage, err := s.upgradeImages(ctx, plan, out)
	if err != nil {
		return nil, err
	}
	base, err := s.modules.docker.SpecOf(ctx, plan.detail.ID)
	if err != nil {
		return nil, err
	}
	if base.AutoRemove {
		return nil, upgradeUnavailable("%s removes itself when it stops, so it could not be kept as the way back.", plan.detail.Name)
	}
	next := upgradeSpec(*base, plan.detail.ID, oldImage, newImage, plan.to.image, plan.data, plan.volume)

	out.Status("Starting PostgreSQL %s beside it as %s", plan.to.id, plan.candidate)
	port, err := freeHostPort(atoiDefault(plan.info.Port, plan.tmpl.port)+1, "127.0.0.1")
	if err != nil {
		return nil, err
	}
	u.candidate = pgSide{user: plan.side.user, password: plan.bootstrap, port: plan.side.port}
	created, err := s.startScratch(ctx, scratchSpec(next, plan.candidate, "candidate", port, plan.tmpl.port, ""), &u.candidate)
	if created != nil {
		for _, binding := range created.Ports {
			if binding.ContainerPort == plan.tmpl.port && !strings.Contains(binding.HostIP, ":") {
				port = binding.HostPort
			}
		}
	}
	if err != nil {
		return nil, err
	}

	report, before, _, err := s.readUpgradeReport(ctx, plan, plan.side, u.candidate, names)
	if err != nil {
		return nil, err
	}
	if blockers := report.blockers(); len(blockers) > 0 {
		return nil, fmt.Errorf("the checks stop the upgrade: %s", strings.Join(blockers, " "))
	}
	for _, c := range report.Checks {
		if c.Verdict == dbx.UpgradeWarning {
			out.Line("stdout", c.Title+": "+c.Detail)
		}
	}

	out.Status("Dumping %s as a safety copy", plan.info.Database)
	dump, err := s.takeDump(ctx, plan.conn, plan.dsn, dbx.DumpOptions{},
		fmt.Sprintf("Before the upgrade to %s %s", plan.tmpl.label, plan.to.id), dbx.DumpOriginSafety, u.by, out)
	u.step("safety-dump", map[string]any{"file": dumpFile(dump)}, err)
	if err != nil {
		return nil, fmt.Errorf("the safety dump failed, so nothing was changed: %w", err)
	}
	s.setUpgradeSafetyDump(ctx, u.record, dump.File)

	// Writes stop here: one made after its database was copied would be lost
	// with the old server.
	u.prior, _ = dbx.PostgresAutoConfValue(before.AutoConf, "default_transaction_read_only")
	s.setUpgradePhase(ctx, u.record, "copy", u.prior)
	if err := s.psqlRun(ctx, plan.side, "postgres", dbx.PostgresTakeReadsOnly()...); err != nil {
		u.step("read-only", nil, err)
		return nil, fmt.Errorf("%s could not be made to take reads only: %w", plan.detail.Name, err)
	}
	u.readOnly = true
	ended, err := u.holdWrites(ctx)
	u.step("read-only", map[string]any{"sessionsEnded": ended}, err)
	if err != nil {
		return nil, err
	}
	out.Status("%s takes reads only until the cutover; %d %s disconnected", plan.detail.Name, ended, pluralWord(ended, "session was", "sessions were"))
	// Read again now that nothing changes: a database made since the checks
	// read the server is copied too.
	from, err := s.postgresFacts(ctx, plan.side)
	if err != nil {
		return nil, err
	}
	from.AutoConf = before.AutoConf

	started := time.Now()
	copied, err := u.copyData(ctx, from, port)
	u.step("copy", map[string]any{"databases": len(from.Databases), "bytes": copied}, err)
	if err != nil {
		return nil, err
	}
	out.Status("Copied the roles and %d %s in %s", len(from.Databases), pluralWord(len(from.Databases), "database", "databases"), time.Since(started).Round(time.Second))

	if err := u.carryParameters(ctx, from); err != nil {
		return nil, err
	}
	out.Status("Restarting PostgreSQL %s to load what was carried", plan.to.id)
	restart := 60
	if err := s.modules.docker.Lifecycle(ctx, u.candidate.container, dockerx.ActionRestart, &restart); err != nil {
		return nil, fmt.Errorf("could not restart %s: %w", plan.candidate, err)
	}
	if err := s.awaitPostgres(ctx, u.candidate); err != nil {
		return nil, err
	}

	out.Status("Counting every table on both servers")
	result, err := u.verify(ctx, from)
	u.step("verify", map[string]any{"tables": resultTables(result), "rows": resultRows(result)}, err)
	if err != nil {
		return nil, err
	}
	result.SafetyDump = dump.File
	return result, nil
}

// holdWrites waits for the switch to reach new sessions and then ends every
// session that began before it, which may still be inside a transaction that
// writes. It answers how many it ended.
func (u *upgradeRun) holdWrites(ctx context.Context) (int, error) {
	s, side := u.s, u.plan.side
	for range 20 {
		var on bool
		if err := s.psqlJSON(ctx, side, "postgres", dbx.PostgresReadsOnlyQuery, &on); err != nil {
			return 0, err
		}
		if on {
			var ended struct {
				Ended int `json:"ended"`
			}
			if err := s.psqlJSON(ctx, side, "postgres", dbx.PostgresEndSessionsQuery, &ended); err != nil {
				return 0, err
			}
			// The dashboard's own sessions were among them.
			for _, conn := range s.upgradeServerConnections(ctx, u.plan.conn, u.plan.info) {
				s.modules.dbs.Close(conn.ID)
			}
			return ended.Ended, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return 0, errors.New("the server did not take reads only after its configuration was reloaded")
}

// copyData copies the roles and then every database into the new server.
func (u *upgradeRun) copyData(ctx context.Context, from *dbx.PostgresServerFacts, port int) (int64, error) {
	s, plan, out := u.s, u.plan, u.out
	out.Status("Copying the roles")
	var globals cappedBuffer
	if err := s.pgTool(ctx, plan.side, []string{"pg_dumpall", "-w", "--globals-only", "-U", plan.side.user}, dockerx.ExecStreams{Stdout: &globals}); err != nil {
		return 0, err
	}
	statements, err := dbx.PostgresGlobals(&globals, plan.side.user)
	if err != nil {
		return 0, err
	}
	candidate := url.URL{
		Scheme: "postgres", User: url.UserPassword(u.candidate.user, plan.bootstrap),
		Host: "127.0.0.1:" + strconv.Itoa(port), Path: "/postgres", RawQuery: "sslmode=disable",
	}
	if err := dbx.ReplayPostgresStatements(ctx, candidate.String(), statements); err != nil {
		return 0, fmt.Errorf("the roles could not be copied: %w", withoutSecrets(candidate.String(), err))
	}
	// The new release made the image's first database for itself. The copy
	// brings the old server's, with its owner, encoding and settings.
	var made []dbx.PostgresDatabase
	if err := s.psqlJSON(ctx, u.candidate, "postgres",
		`SELECT coalesce(json_agg(json_build_object('name', datname) ORDER BY datname), '[]'::json) FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres'`, &made); err != nil {
		return 0, err
	}
	for _, db := range made {
		if err := s.psqlRun(ctx, u.candidate, "postgres", dbx.PostgresDropDatabase(db.Name)); err != nil {
			return 0, err
		}
	}
	var total int64
	for _, db := range from.Databases {
		n, err := u.copyDatabase(ctx, db)
		total += n
		if err != nil {
			return total, fmt.Errorf("%s could not be copied: %w", db.Name, err)
		}
	}
	return total, nil
}

// countingReader counts what passes, for the progress line.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// copyDatabase is one database's archive written by the old server's
// pg_dump and read by the new server's pg_restore as it is written. The
// databases every server has are restored into; every other one is made by
// the restore, as it was made on the old server.
func (u *upgradeRun) copyDatabase(ctx context.Context, db dbx.PostgresDatabase) (int64, error) {
	s, plan, out := u.s, u.plan, u.out
	out.Status("Copying %s (%s)", db.Name, upgradeBytes(db.Bytes))
	reader, writer := io.Pipe()
	counted := &countingReader{r: reader}
	dumped := make(chan error, 1)
	go func() {
		err := s.pgTool(ctx, plan.side, []string{"pg_dump", "-w", "-Fc", "-Z0", "-U", plan.side.user, "-d", dbx.PostgresConnInfo(db.Name)},
			dockerx.ExecStreams{Stdout: writer})
		if err != nil {
			writer.CloseWithError(err)
		} else {
			writer.Close()
		}
		dumped <- err
	}()
	restore := []string{"pg_restore", "-w", "--exit-on-error", "-U", u.candidate.user}
	if db.Template || db.Name == "postgres" {
		restore = append(restore, "-d", dbx.PostgresConnInfo(db.Name))
	} else {
		restore = append(restore, "--create", "-d", dbx.PostgresConnInfo("postgres"))
	}
	ticker := time.NewTicker(5 * time.Second)
	stopped := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopped:
				return
			case <-ticker.C:
				out.Line("stdout", fmt.Sprintf("%s: %s copied", db.Name, upgradeBytes(counted.n.Load())))
			}
		}
	}()
	restoreErr := s.pgTool(ctx, u.candidate, restore, dockerx.ExecStreams{Stdin: counted})
	ticker.Stop()
	close(stopped)
	// A restore that stopped early leaves pg_dump writing into a pipe nobody
	// reads; closing it is what ends the dump.
	reader.CloseWithError(errors.New("the restore ended"))
	dumpErr := <-dumped
	if dumpErr != nil && restoreErr == nil {
		return counted.n.Load(), dumpErr
	}
	return counted.n.Load(), restoreErr
}

// carryParameters writes what ALTER SYSTEM set on the old server into the new
// server's postgresql.auto.conf, less the parameters the new release no
// longer has. It takes effect when the new server restarts.
func (u *upgradeRun) carryParameters(ctx context.Context, from *dbx.PostgresServerFacts) error {
	s, out := u.s, u.out
	var names []string
	if err := s.psqlJSON(ctx, u.candidate, "postgres", `SELECT json_agg(name) FROM pg_settings`, &names); err != nil {
		return err
	}
	file, carried, dropped := dbx.CarryPostgresParameters(from.AutoConf, names)
	if len(dropped) > 0 {
		out.Line("stdout", "Left out, as PostgreSQL "+u.plan.to.id+" has no such parameter: "+strings.Join(dropped, ", "))
	}
	if len(carried) == 0 {
		return nil
	}
	out.Status("Carrying %d %s set with ALTER SYSTEM: %s", len(carried), pluralWord(len(carried), "parameter", "parameters"), strings.Join(carried, ", "))
	var dir string
	if err := s.psqlJSON(ctx, u.candidate, "postgres", `SELECT to_json(current_setting('data_directory'))`, &dir); err != nil {
		return err
	}
	err := s.pgTool(ctx, u.candidate, []string{"tee", dir + "/postgresql.auto.conf"},
		dockerx.ExecStreams{Stdin: strings.NewReader(file)})
	u.step("parameters", map[string]any{"carried": carried, "dropped": dropped}, err)
	return err
}

// verify holds the copy to the old server: the same databases, the same
// roles, the same extensions in each database, and the same number of rows
// in every table.
func (u *upgradeRun) verify(ctx context.Context, from *dbx.PostgresServerFacts) (*upgradeResult, error) {
	s, plan := u.s, u.plan
	to, err := s.postgresFacts(ctx, u.candidate)
	if err != nil {
		return nil, err
	}
	differences := []string{}
	names := func(dbs []dbx.PostgresDatabase) []string {
		out := []string{}
		for _, db := range dbs {
			out = append(out, db.Name)
		}
		return out
	}
	if a, b := names(from.Databases), names(to.Databases); !slices.Equal(a, b) {
		differences = append(differences, fmt.Sprintf("databases %s, not %s", strings.Join(b, ", "), strings.Join(a, ", ")))
	}
	if !slices.Equal(from.Roles, to.Roles) {
		differences = append(differences, fmt.Sprintf("roles %s, not %s", strings.Join(to.Roles, ", "), strings.Join(from.Roles, ", ")))
	}
	result := &upgradeResult{
		From: plan.from.id, To: plan.to.id, Server: to.Version, Container: plan.detail.Name,
		Previous: plan.previous, Volume: plan.volume, Databases: len(from.Databases),
	}
	for _, db := range from.Databases {
		var before, after []dbx.PostgresExtension
		if err := s.psqlJSON(ctx, plan.side, db.Name, dbx.PostgresExtensionsQuery, &before); err != nil {
			return nil, err
		}
		if err := s.psqlJSON(ctx, u.candidate, db.Name, dbx.PostgresExtensionsQuery, &after); err != nil {
			return nil, err
		}
		extNames := func(exts []dbx.PostgresExtension) []string {
			out := []string{}
			for _, e := range exts {
				out = append(out, e.Name)
			}
			return out
		}
		if a, b := extNames(before), extNames(after); !slices.Equal(a, b) {
			differences = append(differences, fmt.Sprintf("%s: extensions %s, not %s", db.Name, strings.Join(b, ", "), strings.Join(a, ", ")))
		}
		var rawBefore, rawAfter json.RawMessage
		if err := s.psqlJSON(ctx, plan.side, db.Name, dbx.PostgresRowCountsQuery, &rawBefore); err != nil {
			return nil, err
		}
		if err := s.psqlJSON(ctx, u.candidate, db.Name, dbx.PostgresRowCountsQuery, &rawAfter); err != nil {
			return nil, err
		}
		countsBefore, err := dbx.ParsePostgresRowCounts(rawBefore)
		if err != nil {
			return nil, err
		}
		countsAfter, err := dbx.ParsePostgresRowCounts(rawAfter)
		if err != nil {
			return nil, err
		}
		differences = append(differences, dbx.PostgresCopyDifferences(db.Name, countsBefore, countsAfter)...)
		result.Tables += len(countsBefore)
		for _, n := range countsBefore {
			result.Rows += n
		}
	}
	if len(differences) > 0 {
		return nil, fmt.Errorf("the copy does not match the old server: %s", strings.Join(differences, "; "))
	}
	rows := "rows"
	if result.Rows == 1 {
		rows = "row"
	}
	result.Summary = fmt.Sprintf("%d %s, %d %s and %d %s match", result.Databases, pluralWord(result.Databases, "database", "databases"),
		result.Tables, pluralWord(result.Tables, "table", "tables"), result.Rows, rows)
	u.out.Status("%s", result.Summary)
	return result, nil
}

// undoCopy removes what the copy made and lets the old server take writes
// again. The old server itself was never stopped.
func (u *upgradeRun) undoCopy(ctx context.Context, cause error) {
	s, plan := u.s, u.plan
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	u.out.Status("Undoing: removing %s and its volume", plan.candidate)
	problems := s.removeScratch(cleanup, u.candidate.container, plan.volume)
	if u.candidate.container == "" {
		// It may have been made before its id was known.
		problems = append(problems, s.removeScratch(cleanup, plan.candidate, plan.volume)...)
	}
	if u.readOnly {
		if err := s.psqlRun(cleanup, plan.side, "postgres", dbx.PostgresTakeWrites(u.prior, true)...); err != nil {
			problems = append(problems, fmt.Sprintf("%s still takes reads only: %v", plan.detail.Name, err))
		} else {
			u.out.Status("%s takes writes again", plan.detail.Name)
		}
		// The dashboard's sessions were ended with the others.
		for _, conn := range s.upgradeServerConnections(cleanup, plan.conn, plan.info) {
			s.modules.dbs.Close(conn.ID)
		}
	}
	u.step("undo", map[string]any{"problems": problems}, cause)
	s.endUpgrade(cleanup, u.record, "failed", upgradeFailure(cause, problems))
}

// upgradeFailure is the sentence a failed upgrade is recorded with.
func upgradeFailure(cause error, problems []string) string {
	text := cause.Error()
	if len(problems) > 0 {
		text += "; and it was not all undone: " + strings.Join(problems, "; ")
	}
	return text
}

// cutover puts the new server in the old one's place: the old container is
// stopped and set aside, the copy's container is removed and its volume
// mounted in a container that has the old one's name, port, networks and
// settings, and the deployments' networks are attached to it.
func (u *upgradeRun) cutover(ctx context.Context, result *upgradeResult) error {
	s, plan, out := u.s, u.plan, u.out
	s.setUpgradePhase(ctx, u.record, "cutover", u.prior)
	out.Status("Cutting over: stopping PostgreSQL %s", plan.from.id)
	base, err := s.modules.docker.SpecOf(ctx, plan.detail.ID)
	if err != nil {
		u.failCutover(ctx, err, false, "")
		return err
	}
	u.restart = base.RestartPolicy + ":" + strconv.Itoa(base.MaxRetries)
	s.setUpgradePrevious(ctx, u.record, plan.detail.ID, u.restart, plan.data.Name)
	// The old server's own configuration goes back as it was: it is kept to
	// be started again if the upgrade is reverted.
	if err := s.psqlRun(ctx, plan.side, "postgres", dbx.PostgresTakeWrites(u.prior, false)...); err != nil {
		u.failCutover(ctx, err, false, "")
		return err
	}
	grace := int(dbStopGrace.Seconds())
	if err := s.modules.docker.Lifecycle(ctx, plan.detail.ID, dockerx.ActionStop, &grace); err != nil {
		u.failCutover(ctx, fmt.Errorf("could not stop %s: %w", plan.detail.Name, err), false, "")
		return err
	}
	if err := s.modules.docker.Rename(ctx, plan.detail.ID, plan.previous); err != nil {
		u.failCutover(ctx, fmt.Errorf("could not set %s aside: %w", plan.detail.Name, err), false, "")
		return err
	}
	// Kept, and kept down: a reboot must not start it on the port the new
	// server now holds.
	if _, err := s.modules.docker.UpdateRestartPolicy(ctx, plan.detail.ID, "no", 0); err != nil {
		u.failCutover(ctx, err, true, "")
		return err
	}

	stop := 30
	if err := s.modules.docker.Lifecycle(ctx, u.candidate.container, dockerx.ActionStop, &stop); err != nil && !errdefs.IsNotFound(err) {
		u.failCutover(ctx, err, true, "")
		return err
	}
	if err := s.modules.docker.RemoveContainer(ctx, u.candidate.container, true, false); err != nil && !errdefs.IsNotFound(err) {
		u.failCutover(ctx, err, true, "")
		return err
	}

	oldImage, newImage, err := s.upgradeImages(ctx, plan, out)
	if err != nil {
		u.failCutover(ctx, err, true, "")
		return err
	}
	spec := upgradeSpec(*base, plan.detail.ID, oldImage, newImage, plan.to.image, plan.data, plan.volume)
	spec.Name = plan.detail.Name
	deployNets := s.deploymentNetworkNames(ctx)
	networks := []string{}
	for _, name := range base.Networks {
		if !deployNets[name] {
			networks = append(networks, name)
		}
	}
	if i := slices.Index(networks, "bridge"); i > 0 {
		networks[0], networks[i] = networks[i], networks[0]
	}
	spec.Networks = nil
	if len(networks) > 0 {
		spec.Networks = networks[:1]
	}
	out.Status("Starting PostgreSQL %s as %s", plan.to.id, plan.detail.Name)
	created, err := s.modules.docker.Create(ctx, spec, nil)
	createdID := ""
	if created != nil {
		createdID = created.ID
	}
	if err != nil {
		u.failCutover(ctx, fmt.Errorf("could not start PostgreSQL %s: %w", plan.to.id, err), true, createdID)
		return err
	}
	port := atoiDefault(plan.info.Port, 0)
	held := false
	for _, binding := range created.Ports {
		if binding.ContainerPort == plan.tmpl.port && binding.HostPort == port {
			held = true
		}
	}
	if !held {
		err := fmt.Errorf("port %d was taken by something else while the servers changed places", port)
		u.failCutover(ctx, err, true, created.ID)
		return err
	}
	for _, name := range networks[min(1, len(networks)):] {
		if err := s.modules.docker.ConnectNetwork(ctx, name, created.ID, upgradeAliases(plan.detail, name)); err != nil {
			u.failCutover(ctx, fmt.Errorf("could not join %s to %s: %w", plan.detail.Name, name, err), true, created.ID)
			return err
		}
	}
	if err := s.awaitConnection(ctx, plan.conn, plan.dsn, created.ID); err != nil {
		u.failCutover(ctx, err, true, created.ID)
		return err
	}
	out.Status("PostgreSQL %s answers at %s:%s", plan.to.id, plan.info.Host, plan.info.Port)
	moved, err := s.settleReplacedServer(ctx, plan.conn, plan.info)
	if err != nil {
		u.failCutover(ctx, fmt.Errorf("the deployments' networks could not be moved to the new server: %w", err), true, created.ID)
		return err
	}
	if moved > 0 {
		out.Status("Moved %d deployment %s to the new server", moved, pluralWord(moved, "link", "links"))
	}
	u.step("cutover", map[string]any{
		"container": plan.detail.Name, "previous": plan.previous, "volume": plan.volume,
		"port": port, "image": plan.to.image, "deploymentLinks": moved,
	}, nil)
	return nil
}

// failCutover puts the old server back where it was: the new container, if
// one was made, is removed with the new data, and the old one is given back
// its name and its restart policy, started and attached to the deployments'
// networks again.
func (u *upgradeRun) failCutover(ctx context.Context, cause error, renamed bool, createdID string) {
	s, plan := u.s, u.plan
	u.out.Status("Undoing the cutover: %s goes back to PostgreSQL %s", plan.detail.Name, plan.from.id)
	problems := s.removeScratch(ctx, createdID, "")
	problems = append(problems, s.removeScratch(ctx, u.candidate.container, plan.volume)...)
	restart := u.restart
	if restart == "" {
		restart = plan.detail.RestartPol
	}
	problems = append(problems, s.restorePrevious(ctx, plan.conn, plan.dsn, plan.detail.ID, plan.detail.Name, renamed, restart)...)
	// A server that was never stopped is still taking reads only.
	if err := s.psqlRun(ctx, plan.side, "postgres", dbx.PostgresTakeWrites(u.prior, true)...); err != nil {
		problems = append(problems, fmt.Sprintf("%s may still take reads only: %v", plan.detail.Name, err))
	}
	u.step("undo", map[string]any{"problems": problems}, cause)
	s.endUpgrade(ctx, u.record, "failed", upgradeFailure(cause, problems))
}

// restorePrevious gives the old container back its name and restart policy,
// starts it and waits for it to answer, and attaches it to the deployments'
// networks again. It says what it could not do.
func (s *Server) restorePrevious(ctx context.Context, conn *dbConnection, dsn, id, name string, renamed bool, restart string) []string {
	problems := []string{}
	if renamed {
		if err := s.modules.docker.Rename(ctx, id, name); err != nil {
			problems = append(problems, fmt.Sprintf("the old container could not be given back the name %s: %v", name, err))
		}
	}
	policy, retries := restartPolicyOf(restart)
	if _, err := s.modules.docker.UpdateRestartPolicy(ctx, id, policy, retries); err != nil {
		problems = append(problems, fmt.Sprintf("its restart policy could not be put back: %v", err))
	}
	if err := s.modules.docker.Lifecycle(ctx, id, dockerx.ActionStart, nil); err != nil {
		problems = append(problems, fmt.Sprintf("it could not be started: %v", err))
		return problems
	}
	if err := s.awaitConnection(ctx, conn, dsn, id); err != nil {
		problems = append(problems, err.Error())
	}
	if info, err := dbx.ParseDSN(conn.Driver, dsn); err == nil {
		if _, err := s.settleReplacedServer(ctx, conn, info); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return problems
}

// awaitConnection waits for the saved connection to sign in to the server
// that now answers at its address: the proof that nothing that dials it has
// to change.
func (s *Server) awaitConnection(ctx context.Context, conn *dbConnection, dsn, containerID string) error {
	deadline := time.Now().Add(upgradeReady)
	for {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := dbx.Probe(probe, conn.Driver, dsn)
		cancel()
		if err == nil {
			return nil
		}
		detail, ierr := s.modules.docker.Inspect(ctx, containerID)
		if ierr == nil && detail.State != "running" {
			return fmt.Errorf("%s stopped while it was starting: %s", detail.Name, s.containerLogTail(ctx, containerID))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not let the saved connection in within %s: %s", conn.Name, upgradeReady, connectError(dsn, err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// upgradeServerConnections are the saved connections to the upgraded server:
// the one the upgrade came through and any to its other databases, which all
// dial the same address.
func (s *Server) upgradeServerConnections(ctx context.Context, conn *dbConnection, info *dbx.ConnInfo) []*dbConnection {
	all, err := s.allConnections(ctx)
	if err != nil {
		return []*dbConnection{conn}
	}
	server := addressKey(info.Host, atoiDefault(info.Port, 0))
	out := []*dbConnection{}
	for _, other := range all {
		if other.ID == conn.ID || (!other.Broken && other.Driver == conn.Driver &&
			addressKey(other.Host, atoiDefault(other.Port, 0)) == server) {
			out = append(out, other)
		}
	}
	return out
}

// settleReplacedServer is what follows a server being replaced at its
// address: every saved connection to it drops its pool and what was kept
// about the server, and is attached to its deployments' networks again. It
// answers how many links were moved.
func (s *Server) settleReplacedServer(ctx context.Context, through *dbConnection, info *dbx.ConnInfo) (int, error) {
	moved := 0
	var errs []error
	for _, conn := range s.upgradeServerConnections(ctx, through, info) {
		s.modules.dbs.Close(conn.ID)
		s.dbConns.forget(conn.ID)
		n, err := s.modules.deployDatabases.rebind(ctx, conn.ID)
		moved += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return moved, errors.Join(errs...)
}

// rebind attaches the container that now answers for a connection to each
// environment network it is bound on, as reconciliation does on its next
// pass — at once, so a deployment reaches the new server under its alias as
// soon as it answers.
func (o *deploymentDatabaseNetworks) rebind(ctx context.Context, connectionID int64) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	rows, err := o.server.Store.DB.QueryContext(ctx, `SELECT environment_id FROM deploy_database_bindings WHERE connection_id=? ORDER BY environment_id`, connectionID)
	if err != nil {
		return 0, err
	}
	environments := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		environments = append(environments, id)
	}
	rows.Close()
	moved := 0
	for _, environment := range environments {
		network, err := o.network(ctx, environment, true)
		if err == nil {
			err = o.bind(ctx, network, connectionID)
		}
		if err != nil {
			o.markUnavailable(ctx, environment, connectionID, err)
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// deploymentNetworkNames are the environment networks the deployments own.
// The new server joins them through rebind, under its alias, not as a copy
// of the old container's membership.
func (s *Server) deploymentNetworkNames(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT network_name FROM deploy_database_networks`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			out[name] = true
		}
	}
	return out
}

// upgradeAliases are the names the old container answered to on a network,
// less the short id Docker gives every container there.
func upgradeAliases(detail *dockerx.ContainerDetail, network string) []string {
	out := []string{}
	for _, endpoint := range detail.NetworkList {
		if endpoint.Name != network {
			continue
		}
		for _, alias := range endpoint.Aliases {
			if alias != "" && !strings.HasPrefix(detail.ID, alias) && alias != detail.Name {
				out = append(out, alias)
			}
		}
	}
	return out
}

// restartPolicyOf reads a restart policy kept as one word, with the retries
// of on-failure after a colon.
func restartPolicyOf(word string) (string, int) {
	name, retries, _ := strings.Cut(word, ":")
	n, _ := strconv.Atoi(retries)
	if name == "" {
		name = "no"
	}
	return name, n
}

func dumpFile(dump *dbx.DumpResult) string {
	if dump == nil {
		return ""
	}
	return dump.File
}

func resultTables(result *upgradeResult) int {
	if result == nil {
		return 0
	}
	return result.Tables
}

func resultRows(result *upgradeResult) int64 {
	if result == nil {
		return 0
	}
	return result.Rows
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
