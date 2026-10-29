package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

// The Databases owner's side of data-aware rollback (deploy/data_rollback.go).
//
// When a release goes live, each database its environment links is read for
// where it could be brought back to: the server's point-in-time archive, when
// it writes one that reaches back that far, or the newest backup run holding
// its dump. Asked for later, a copy of the database as it was at that point is
// restored through the migration rehearsal's own path (prepareCopy), so it is
// the production server's container configuration on a fresh volume, with
// archiving off and none of production's ports, labels or writable mounts —
// but on the deployment's own database network rather than one of its own,
// under a name of its own (db-<connection>-copy-<copy>.jd.internal, never the
// production names), and with a password made for it. It carries the
// rehearsal label with "copy-<id>", so the rehearsal's sweep at start finds it
// like any other copy and removes it unless it is still within its lifetime,
// and the owner here removes it when that lifetime ends.

// dataCopyLabel marks what a data copy makes with the copy's id, beside the
// rehearsal label.
const dataCopyLabel = "io.just-dashboard.data-copy"

func dataCopyContainer(environmentID, copyID int64) string {
	return fmt.Sprintf("jd-e%d-copy%d-db", environmentID, copyID)
}

// dataCopyHost is the copy's name on the deployment's network. It is not a
// managed database's name, and a variable that uses it is refused before a
// release starts (databaseURLConnectionIDs): a copy is removed when it
// expires, and production never runs against it.
func dataCopyHost(connectionID, copyID int64) string {
	return fmt.Sprintf("db-%d-copy-%d.jd.internal", connectionID, copyID)
}

var dataCopyHostRE = regexp.MustCompile(`^db-[0-9]+-copy-[0-9]+\.jd\.internal$`)

func dataCopyRehearsal(copyID int64) string { return "copy-" + strconv.FormatInt(copyID, 10) }

func dataCopyLabels(environmentID, copyID int64) map[string]string {
	return map[string]string{
		"io.just-dashboard.managed":        "true",
		"io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10),
		rehearsalLabel:                     dataCopyRehearsal(copyID),
		dataCopyLabel:                      strconv.FormatInt(copyID, 10),
	}
}

// dataCopySecret is the copy's sign-in, sealed in its row: the account the
// application's URL names, with the password made for the copy.
type dataCopySecret struct {
	User     string `json:"user"`
	Password string `json:"password"`
	URL      string `json:"url"`
}

// releaseDataFacts reads, from the store alone, what is known about each
// database an environment links at the moment one of its releases goes live:
// its archive by the container its binding recorded, and its newest dump.
type releaseDataFacts struct{ server *Server }

func (r releaseDataFacts) ReleaseDataFacts(ctx context.Context, environmentID int64, at time.Time) ([]deploy.DataPointFacts, error) {
	s := r.server
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT b.connection_id, b.container_name, c.name, c.driver
		  FROM deploy_database_bindings b JOIN db_connections c ON c.id = b.connection_id
		 WHERE b.environment_id = ? ORDER BY c.name, b.connection_id`, environmentID)
	if err != nil {
		return nil, err
	}
	type linked struct {
		id                    int64
		container, name, kind string
	}
	links := []linked{}
	for rows.Next() {
		var link linked
		if err := rows.Scan(&link.id, &link.container, &link.name, &link.kind); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]deploy.DataPointFacts, 0, len(links))
	for _, link := range links {
		facts := deploy.DataPointFacts{ConnectionID: link.id, Name: link.name, Engine: link.kind}
		switch dbx.Driver(link.kind) {
		case dbx.DriverPostgres, dbx.DriverMySQL:
		default:
			facts.Unsupported = fmt.Sprintf("a copy is restored for PostgreSQL, MySQL and MariaDB databases, and %s is %s", link.name, link.kind)
			out = append(out, facts)
			continue
		}
		if archive, err := scanPITRArchive(s.Store.DB.QueryRowContext(ctx, `SELECT `+pitrArchiveCols+` FROM db_wal_archives
			 WHERE ended_at = 0 AND (container = ? OR connection_id = ?) ORDER BY created_at DESC, id DESC LIMIT 1`,
			strings.TrimPrefix(link.container, "/"), link.id)); err == nil {
			facts.ArchiveID = archive.ID
			// The kept listing's oldest base backup, or, before the archive
			// has been listed, the base backup the schedule last took: either
			// is a backup the log can be replayed on top of to this moment.
			switch {
			case archive.Reading != nil && archive.Reading.Window.From != nil:
				facts.ArchiveFrom = *archive.Reading.Window.From
			case archive.LastBackupAt != nil:
				facts.ArchiveFrom = *archive.LastBackupAt
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if s.modules.backupStore != nil {
			runID, started, _, err := s.modules.backupStore.LatestDatabaseDump(ctx, link.id)
			switch {
			case err == nil:
				facts.DumpRunID, facts.DumpAt = runID, started
				if run, err := s.modules.backupStore.Run(ctx, runID); err == nil {
					facts.DumpJobID = run.JobID
				}
			case !errors.Is(err, backups.ErrNoDatabaseDump):
				return nil, err
			}
		}
		out = append(out, facts)
	}
	return out, nil
}

// dataCopyOwner restores the copies asked for, one at a time, and removes
// each when its lifetime ends or somebody removes it.
type dataCopyOwner struct {
	server    *Server
	rehearser *migrationRehearser
	poke      chan struct{}
	// busy holds the one restore under way; a copy waits for the next pass
	// rather than restoring beside another.
	busy   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
	every  time.Duration
	// restores is waited for on Stop, so a restore under way is not left
	// half made by a process that exits cleanly.
	restores sync.WaitGroup
}

func newDataCopyOwner(s *Server, rehearser *migrationRehearser) *dataCopyOwner {
	return &dataCopyOwner{server: s, rehearser: rehearser, poke: make(chan struct{}, 1), busy: make(chan struct{}, 1), every: 15 * time.Second}
}

// Poke asks for a pass now: a copy just asked for starts without waiting.
func (o *dataCopyOwner) Poke() {
	if o == nil {
		return
	}
	select {
	case o.poke <- struct{}{}:
	default:
	}
}

func (o *dataCopyOwner) Start(ctx context.Context) {
	if o == nil || o.cancel != nil {
		return
	}
	ctx, o.cancel = context.WithCancel(ctx)
	o.done = make(chan struct{})
	go func() {
		defer close(o.done)
		ticker := time.NewTicker(o.every)
		defer ticker.Stop()
		for {
			o.pass(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-o.poke:
			}
		}
	}()
}

func (o *dataCopyOwner) Stop() {
	if o == nil || o.cancel == nil {
		return
	}
	o.cancel()
	<-o.done
	o.restores.Wait()
	o.cancel = nil
}

// SettleInterrupted ends every copy a previous process was restoring: the
// process that was running it is gone, and what it made is the rehearsal
// sweep's to remove, which runs next.
func (o *dataCopyOwner) SettleInterrupted(ctx context.Context) {
	store := o.server.modules.deployRuns
	copies, err := store.DataCopiesInState(ctx, deploy.DataCopyRestoring)
	if err != nil {
		return
	}
	for _, copy := range copies {
		if _, err := store.EndDataCopy(ctx, copy.ID, deploy.DataCopyFailed, "the dashboard restarted while the copy was being restored; ask for it again"); err == nil {
			o.record(ctx, "deploy.data_copy.finish", copy, false, "interrupted by a restart")
		}
	}
}

// liveDataCopies are the rehearsal-label values of copies still within
// their lifetime, which the sweep at start leaves.
func (s *Server) liveDataCopies(ctx context.Context) map[string]bool {
	keep := map[string]bool{}
	if s.modules.deployRuns == nil {
		return keep
	}
	copies, err := s.modules.deployRuns.DataCopiesInState(ctx, deploy.DataCopyReady)
	if err != nil {
		return keep
	}
	now := time.Now()
	for _, copy := range copies {
		if !deploy.DataCopyExpired(copy, now) {
			keep[dataCopyRehearsal(copy.ID)] = true
		}
	}
	return keep
}

func (o *dataCopyOwner) pass(ctx context.Context) {
	store := o.server.modules.deployRuns
	if waiting, err := store.DataCopiesInState(ctx, deploy.DataCopyWaiting); err == nil {
		for _, copy := range waiting {
			if ctx.Err() != nil {
				return
			}
			var run *deploy.EngineRun
			if copy.AfterRunID > 0 {
				if found, err := store.Run(ctx, copy.AfterRunID); err == nil {
					run = found
				} else if !errors.Is(err, deploy.ErrRunNotFound) {
					continue
				}
			}
			start, skip := deploy.WaitingDataCopyDecision(copy, run)
			if skip != "" {
				if ended, err := store.EndDataCopy(ctx, copy.ID, deploy.DataCopySkipped, skip); err == nil {
					o.record(ctx, "deploy.data_copy.finish", *ended, false, skip)
				}
				continue
			}
			if !start {
				continue
			}
			select {
			case o.busy <- struct{}{}:
			default:
				// One restore at a time; this one waits for the next pass.
				continue
			}
			claimed, err := store.ClaimDataCopy(ctx, copy.ID)
			if err != nil || !claimed {
				<-o.busy
				continue
			}
			o.restores.Add(1)
			go func(copy deploy.DataCopy) {
				defer o.restores.Done()
				defer func() { <-o.busy }()
				o.restore(ctx, copy)
				o.Poke()
			}(copy)
		}
	}
	if ready, err := store.DataCopiesInState(ctx, deploy.DataCopyReady); err == nil {
		now := time.Now()
		for _, copy := range ready {
			if deploy.DataCopyExpired(copy, now) {
				_, _ = o.Remove(ctx, copy, "it expired", "system")
			}
		}
	}
	o.sweepOrphans(ctx)
}

// sweepOrphans removes what a copy made whose row no longer says it is
// waiting, restoring or ready: one removed while its restore was finishing,
// or a project deleted with its copies.
func (o *dataCopyOwner) sweepOrphans(ctx context.Context) {
	docker := o.server.modules.docker
	if docker == nil {
		return
	}
	containers, err := docker.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.managed": "true"})
	if err != nil {
		return
	}
	volumes, _ := docker.ListVolumes(ctx)
	ids := map[int64]bool{}
	for _, item := range containers {
		if id, err := strconv.ParseInt(item.Labels[dataCopyLabel], 10, 64); err == nil && id > 0 {
			ids[id] = true
		}
	}
	for _, volume := range volumes {
		if id, err := strconv.ParseInt(volume.Labels[dataCopyLabel], 10, 64); err == nil && id > 0 {
			ids[id] = true
		}
	}
	for id := range ids {
		copy, err := o.server.modules.deployRuns.DataCopy(ctx, id)
		if err == nil && deploy.DataCopyActive(copy.State) {
			continue
		}
		if err != nil && !errors.Is(err, deploy.ErrDataCopyNotFound) {
			continue
		}
		if err := o.rehearser.removeRehearsals(ctx, map[string]string{rehearsalLabel: dataCopyRehearsal(id)}, nil); err != nil {
			o.server.Log.Warn("a data copy's leftovers could not be removed yet", "copy", id, "err", err)
		}
	}
}

// restore makes one copy. However it ends, the copy is either ready or ended
// with its reason and everything it made removed.
func (o *dataCopyOwner) restore(ctx context.Context, copy deploy.DataCopy) {
	s := o.server
	store := s.modules.deployRuns
	started := time.Now()
	fail := func(reason string) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		_ = o.rehearser.removeRehearsals(cleanup, map[string]string{rehearsalLabel: dataCopyRehearsal(copy.ID)}, nil)
		if ended, err := store.EndDataCopy(cleanup, copy.ID, deploy.DataCopyFailed, reason); err == nil {
			o.record(cleanup, "deploy.data_copy.finish", *ended, false, reason)
		}
	}
	policy, err := store.RehearsalPolicy(ctx, copy.EnvironmentID)
	if err != nil {
		fail("the environment's restore limits could not be read")
		return
	}
	network, err := s.modules.deployDatabases.network(ctx, copy.EnvironmentID, false)
	if err != nil {
		fail("the deployment has no database network to put the copy on yet: deploy it once with the database linked")
		return
	}
	container, host := dataCopyContainer(copy.EnvironmentID, copy.ID), dataCopyHost(copy.ConnectionID, copy.ID)
	plan := copyPlan{
		connectionID: copy.ConnectionID, container: container, network: network.Name, aliases: []string{host},
		labels: dataCopyLabels(copy.EnvironmentID, copy.ID), maxBytes: int64(policy.MaxBackupMB) << 20,
	}
	switch copy.Source.Kind {
	case deploy.DataPointPITR:
		if copy.Source.At == nil {
			fail("the recorded point names no moment")
			return
		}
		plan.archiveID, plan.at = copy.Source.ArchiveID, *copy.Source.At
	case deploy.DataPointDump:
		plan.dumpRunID = copy.Source.BackupRunID
	default:
		fail("the recorded point is not one a copy is restored from")
		return
	}
	restoreCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.RestoreTimeoutSeconds)*time.Second)
	defer cancel()
	last := ""
	made, err := o.rehearser.prepareCopy(restoreCtx, plan, func(line deploy.BuildLog) error {
		if strings.TrimSpace(line.Text) != "" {
			last = line.Text
		}
		return nil
	})
	if err != nil {
		reason := strings.TrimPrefix(err.Error(), deploy.ErrRehearsalUnavailable.Error()+": ")
		if ctx.Err() != nil {
			reason = "the dashboard stopped while the copy was being restored; ask for it again"
		} else if restoreCtx.Err() != nil {
			reason = fmt.Sprintf("the copy was not restored within %d seconds (the environment's restore limit)", policy.RestoreTimeoutSeconds)
		} else if last != "" && !strings.Contains(reason, last) && len(last) < 300 {
			reason += " (" + last + ")"
		}
		fail(reason)
		return
	}
	password, err := dataCopyPassword()
	if err != nil {
		fail("a password for the copy could not be made")
		return
	}
	if err := o.rehearser.setCopyPassword(restoreCtx, made, password); err != nil {
		fail("the copy could not be given a sign-in of its own: " + err.Error())
		return
	}
	appURL, port, err := dataCopyURL(ctx, s, made, host, password)
	if err != nil {
		fail("the copy's address could not be written: " + err.Error())
		return
	}
	raw, _ := json.Marshal(dataCopySecret{User: made.info.User, Password: password, URL: appURL})
	sealed, err := s.Sealer.Seal(string(raw))
	if err != nil {
		fail("the copy's sign-in could not be sealed")
		return
	}
	var recovered *time.Time
	if !made.recoveredTo.IsZero() {
		at := made.recoveredTo.UTC()
		recovered = &at
	}
	ready, err := store.DataCopyReady(context.WithoutCancel(ctx), copy.ID, deploy.DataCopyReadiness{
		Container: container, Network: network.Name, Host: host, Port: port, Database: made.info.Database,
		SecretSealed: sealed, RecoveredTo: recovered, RestoreMS: time.Since(started).Milliseconds(),
	})
	if err != nil {
		// Removed while it was being restored: what was made goes too.
		fail("it was removed before its restore finished")
		return
	}
	o.record(ctx, "deploy.data_copy.finish", *ready, true, "")
}

// Remove removes what a copy made and ends it, in that order, so a copy
// that reads removed is gone. One whose containers Docker would not remove
// yet is ended all the same: the next pass's sweep finds them by label.
func (o *dataCopyOwner) Remove(ctx context.Context, copy deploy.DataCopy, reason, actor string) (*deploy.DataCopy, error) {
	if !deploy.DataCopyActive(copy.State) {
		return nil, deploy.ErrDataCopyEnded
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	removeErr := o.rehearser.removeRehearsals(cleanup, map[string]string{rehearsalLabel: dataCopyRehearsal(copy.ID)}, nil)
	ended, err := o.server.modules.deployRuns.EndDataCopy(cleanup, copy.ID, deploy.DataCopyRemoved, reason)
	if err != nil {
		return nil, err
	}
	if actor == "system" {
		o.record(cleanup, "deploy.data_copy.remove", *ended, removeErr == nil, reason)
	}
	return ended, removeErr
}

// removeEnvironmentCopies removes every copy an environment holds, before
// its database network is removed: a copy is an endpoint on that network
// the removal would otherwise refuse as somebody's application.
func (o *dataCopyOwner) removeEnvironmentCopies(ctx context.Context, environmentID int64) error {
	if o == nil {
		return nil
	}
	copies, err := o.server.modules.deployRuns.DataCopiesInState(ctx, deploy.DataCopyWaiting, deploy.DataCopyRestoring, deploy.DataCopyReady)
	if err != nil {
		return err
	}
	failures := []error{}
	for _, copy := range copies {
		if copy.EnvironmentID != environmentID {
			continue
		}
		if _, err := o.Remove(ctx, copy, "its deployment's network was removed", "system"); err != nil && !errors.Is(err, deploy.ErrDataCopyEnded) {
			failures = append(failures, err)
		}
	}
	// What a copy ended before this left is found by its label.
	docker := o.server.modules.docker
	if docker != nil {
		containers, err := docker.ListContainersWithLabels(ctx, map[string]string{
			"io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": strconv.FormatInt(environmentID, 10),
		})
		if err == nil {
			for _, item := range containers {
				if id, err := strconv.ParseInt(item.Labels[dataCopyLabel], 10, 64); err == nil && id > 0 {
					failures = append(failures, o.rehearser.removeRehearsals(ctx, map[string]string{rehearsalLabel: dataCopyRehearsal(id)}, nil))
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (o *dataCopyOwner) record(ctx context.Context, action string, copy deploy.DataCopy, success bool, reason string) {
	detail, _ := json.Marshal(map[string]any{
		"deploymentId": copy.ProjectID, "environmentId": copy.EnvironmentID, "releaseId": copy.ReleaseID,
		"connectionId": copy.ConnectionID, "state": copy.State, "source": copy.Source.Kind, "reason": reason,
		"container": copy.Container,
	})
	o.server.Audit.Record(ctx, audit.Entry{Actor: "system", Action: action, Target: strconv.FormatInt(copy.ID, 10), Success: success, Detail: string(detail)})
}

// dataCopyPassword is the copy's own password: letters and digits only, so
// it needs no quoting in a URL or a statement.
func dataCopyPassword() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// setCopyPassword gives the account the application signs in as a password
// made for the copy, so the copy's address carries no password production
// accepts. The server's own superuser, which the dashboard reaches the copy
// through with docker exec, is left as it is unless it is that account.
func (m *migrationRehearser) setCopyPassword(ctx context.Context, made *madeCopy, password string) error {
	user := made.info.User
	if user == "" {
		return errors.New("the connection names no account")
	}
	if made.engine.name == "postgres" {
		return m.execSQL(ctx, made.copy.Container, made.engine, made.info.Database,
			fmt.Sprintf(`ALTER ROLE %s WITH LOGIN PASSWORD %s`, sqlIdentifier(user), sqlLiteral(password)))
	}
	hosts, err := m.execMySQL(ctx, made.copy.Container, made.engine, fmt.Sprintf(`SELECT host FROM mysql.user WHERE user = %s`, sqlLiteral(user)))
	if err != nil {
		return err
	}
	statements := []string{}
	for _, host := range hosts {
		// localhost is the socket docker exec signs in through; the network
		// only ever reaches the other hosts.
		if host == "" || host == "localhost" {
			continue
		}
		statements = append(statements, fmt.Sprintf(`ALTER USER %s@%s IDENTIFIED BY %s`, sqlLiteral(user), sqlLiteral(host), sqlLiteral(password)))
	}
	if len(statements) == 0 {
		return fmt.Errorf("%s has no account the network signs in to", user)
	}
	_, err = m.execMySQL(ctx, made.copy.Container, made.engine, strings.Join(statements, "; "))
	return err
}

// execMySQL runs statements in a MySQL or MariaDB copy as its root, through
// the socket, and answers what they printed on standard output, a row a
// line. The password goes in through MYSQL_PWD, which both clients read, so
// neither warns about one on its command line — a warning that would
// otherwise be read back as a row.
func (m *migrationRehearser) execMySQL(ctx context.Context, container string, engine rehearsalEngine, statements string) ([]string, error) {
	var out []string
	var refusal string
	code, err := m.server.modules.docker.ExecStream(ctx, container,
		[]string{"sh", "-c", `client=mysql; command -v mysql >/dev/null 2>&1 || client=mariadb; "$client" -uroot -N -B -e "$1"`, "sh", statements},
		"", []string{"MYSQL_PWD=" + engine.password},
		func(line dockerx.LogLine) {
			if line.Stream == "stdout" {
				out = append(out, strings.TrimSpace(line.Text))
			} else if strings.TrimSpace(line.Text) != "" {
				refusal = line.Text
			}
		})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if refusal == "" {
			refusal = fmt.Sprintf("the client exited with code %d", code)
		}
		return nil, errors.New(refusal)
	}
	return out, nil
}

// dataCopyURL is the copy's address as an application on the deployment's
// network would use it: the production URL the environment's variables
// resolve to, with the copy's name and password in it.
func dataCopyURL(ctx context.Context, s *Server, made *madeCopy, host, password string) (string, int, error) {
	production, err := s.databaseApplicationURL(ctx, made.conn, made.dsn)
	if err != nil {
		return "", 0, err
	}
	u, err := url.Parse(production)
	if err != nil || u.Host == "" {
		return "", 0, errors.New("the application URL is not one a copy's can be written from")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return "", 0, errors.New("the application URL names no port")
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	u.User = url.UserPassword(made.info.User, password)
	return u.String(), port, nil
}

// copyDSN is how this process reaches a copy: the saved connection's own
// string — its driver options and TLS as the dashboard reads them — at the
// copy's address on the deployment's network and with the copy's sign-in.
func copyDSN(driver dbx.Driver, saved string, info *dbx.ConnInfo, address string, port int, secret dataCopySecret) (string, error) {
	dsn, err := applicationConnectionURL(driver, saved, info, address, strconv.Itoa(port))
	if err != nil {
		return "", err
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "", errors.New("the saved connection is not one a copy's address can be written from")
	}
	u.User = url.UserPassword(secret.User, secret.Password)
	return u.String(), nil
}

// DataCopyTable is one table of a copy beside the live database's.
type DataCopyTable struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	CopyRows  *int64 `json:"copyRows"`
	LiveRows  *int64 `json:"liveRows"`
	Estimated bool   `json:"estimated,omitempty"`
	// Only names the side a table is on when the other has no such table.
	Only string `json:"only,omitempty"`
}

type DataCopyComparison struct {
	CopyID    int64           `json:"copyId"`
	Database  string          `json:"database"`
	Tables    []DataCopyTable `json:"tables"`
	Truncated bool            `json:"truncated"`
	CopyError string          `json:"copyError,omitempty"`
	LiveError string          `json:"liveError,omitempty"`
	ReadAt    time.Time       `json:"readAt"`
}

// dataCopyTableLimit bounds a comparison: every table is counted on both
// sides, and a count is a scan.
const dataCopyTableLimit = 200

// compare lists the copy's tables and the live database's with the rows
// each holds, through the readers the Databases pages use.
func (o *dataCopyOwner) compare(ctx context.Context, copy deploy.DataCopy) (*DataCopyComparison, error) {
	s := o.server
	if copy.State != deploy.DataCopyReady {
		return nil, deploy.ErrDataCopyNotReady
	}
	sealed, err := s.modules.deployRuns.DataCopySecret(ctx, copy.ID)
	if err != nil {
		return nil, err
	}
	plain, err := s.Sealer.Open(sealed)
	if err != nil {
		return nil, errors.New("the copy's sign-in could not be opened")
	}
	var secret dataCopySecret
	if err := json.Unmarshal([]byte(plain), &secret); err != nil {
		return nil, errors.New("the copy's sign-in is malformed")
	}
	conn, dsn, err := s.dbConnRow(ctx, copy.ConnectionID)
	if err != nil {
		return nil, err
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return nil, errors.New("the saved connection could not be read")
	}
	out := &DataCopyComparison{CopyID: copy.ID, Database: copy.Database, Tables: []DataCopyTable{}, ReadAt: time.Now().UTC()}
	schema := ""
	if conn.Driver == dbx.DriverMySQL {
		schema = copy.Database
	}
	readSide := func(db *sql.DB) (map[string]DataCopyTable, []string, bool, error) {
		tables, err := dbx.ListTables(ctx, db, conn.Driver, schema)
		if err != nil {
			return nil, nil, false, err
		}
		counted := map[string]DataCopyTable{}
		order := []string{}
		truncated := false
		for _, table := range tables {
			switch table.Type {
			case dbx.TableTypeView, dbx.TableTypeMaterializedView, dbx.TableTypePartition, "sequence":
				continue
			}
			if len(order) >= dataCopyTableLimit {
				truncated = true
				break
			}
			key := table.Schema + "." + table.Name
			entry := DataCopyTable{Schema: table.Schema, Name: table.Name}
			countCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			n, err := dbx.Count(countCtx, db, conn.Driver, dbx.BrowseOptions{Schema: table.Schema, Table: table.Name})
			cancel()
			if err == nil {
				entry.CopyRows = &n
			} else if table.Rows >= 0 {
				estimate := table.Rows
				entry.CopyRows, entry.Estimated = &estimate, true
			}
			counted[key] = entry
			order = append(order, key)
		}
		return counted, order, truncated, nil
	}
	var copyTables, liveTables map[string]DataCopyTable
	var copyOrder, liveOrder []string
	address, err := o.copyAddress(ctx, copy)
	if err == nil {
		var target string
		if target, err = copyDSN(conn.Driver, dsn, info, address, copy.Port, secret); err == nil {
			var db *sql.DB
			if db, err = dbx.OpenDatabase(ctx, conn.Driver, target, copy.Database); err == nil {
				var truncated bool
				copyTables, copyOrder, truncated, err = readSide(db)
				out.Truncated = out.Truncated || truncated
				_ = db.Close()
			}
		}
	}
	if err != nil {
		out.CopyError = err.Error()
	}
	live, _, err := s.dbPool(ctx, copy.ConnectionID)
	if err == nil {
		var truncated bool
		liveTables, liveOrder, truncated, err = readSide(live)
		out.Truncated = out.Truncated || truncated
	}
	if err != nil {
		out.LiveError = err.Error()
	}
	seen := map[string]bool{}
	for _, key := range append(append([]string{}, copyOrder...), liveOrder...) {
		if seen[key] {
			continue
		}
		seen[key] = true
		fromCopy, inCopy := copyTables[key]
		fromLive, inLive := liveTables[key]
		entry := fromCopy
		if !inCopy {
			entry = DataCopyTable{Schema: fromLive.Schema, Name: fromLive.Name}
		}
		entry.LiveRows, entry.Estimated = fromLive.CopyRows, fromCopy.Estimated || fromLive.Estimated
		switch {
		case inCopy && !inLive && out.LiveError == "":
			entry.Only = "copy"
		case inLive && !inCopy && out.CopyError == "":
			entry.Only = "live"
		}
		out.Tables = append(out.Tables, entry)
	}
	sort.SliceStable(out.Tables, func(i, j int) bool {
		if out.Tables[i].Schema != out.Tables[j].Schema {
			return out.Tables[i].Schema < out.Tables[j].Schema
		}
		return out.Tables[i].Name < out.Tables[j].Name
	})
	return out, nil
}

// copyAddress is the copy's address on the deployment's network, which this
// process — on the host's network — reaches directly.
func (o *dataCopyOwner) copyAddress(ctx context.Context, copy deploy.DataCopy) (string, error) {
	detail, err := o.server.modules.docker.Inspect(ctx, copy.Container)
	if errdefs.IsNotFound(err) {
		return "", errors.New("the copy's container is gone")
	}
	if err != nil {
		return "", err
	}
	for _, endpoint := range detail.NetworkList {
		if endpoint.Name == copy.Network && endpoint.IPAddress != "" {
			return endpoint.IPAddress, nil
		}
	}
	return "", errors.New("the copy is not on the deployment's network")
}
