package api

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The host alerts' readings: each kind's source read once a pass, and only
// when an enabled rule needs it, then judged against each rule's line by a
// pure function a test can hand made-up readings to.

func (s *Server) newHostAlerts() *hostAlerts {
	return &hostAlerts{
		db:      s.Store.DB,
		observe: s.observeHostAlerts,
		deliver: func(ctx context.Context, channelID int64, envelope deploy.NotificationEnvelope) error {
			return s.modules.deployAutomation.DeliverNotification(ctx, nil, channelID, envelope)
		},
		channels: s.modules.deployAutomation.ListNotificationChannels,
		url:      s.dashboardEndpoint,
		server: func(ctx context.Context) string {
			info, err := s.modules.sys.Host(ctx)
			if err != nil || info == nil {
				return ""
			}
			return info.Hostname
		},
		now:      time.Now,
		interval: hostAlertInterval,
		log:      s.Log,
		restarts: map[string][]hostRestartMark{},
		exits:    map[string]hostCachedExit{},
	}
}

// observeHostAlerts takes each rule's reading. It runs inside a pass, under
// the pass's lock, which is what lets the container memory live unlocked on
// the evaluator.
func (s *Server) observeHostAlerts(ctx context.Context, rules []hostAlertRule, now time.Time) map[int64]hostAlertReading {
	alerts := s.modules.hostAlerts
	uses := map[string]bool{}
	for _, rule := range rules {
		uses[rule.Kind] = true
	}
	recorder := s.modules.metrics
	recording := recorder != nil && recorder.Enabled()
	interval := time.Duration(0)
	if recorder != nil {
		interval = recorder.Interval()
	}
	var mounts []hostMount
	var mountsAt time.Time
	var mountsErr error
	if uses[hostAlertDiskFilling] && recording {
		mounts, mountsAt, mountsErr = readHostMounts(ctx, s.Store.DB)
	}
	var containers []hostContainer
	containersRead := false
	if uses[hostAlertContainerDown] {
		containers, containersRead = s.observeHostContainers(ctx, alerts, now)
	}
	var jobs []dbAlertJob
	var leftToDB func(dbAlertJob) bool
	var jobsErr error
	if uses[hostAlertBackupFailed] {
		jobs, jobsErr = readAlertBackupJobs(ctx, s.Store.DB)
		if jobsErr == nil {
			leftToDB, jobsErr = databaseBackupRules(ctx, s.Store.DB)
		}
	}
	server := alerts.machine(ctx)

	out := map[int64]hostAlertReading{}
	for _, rule := range rules {
		if ctx.Err() != nil {
			break
		}
		switch rule.Kind {
		case hostAlertCPUHigh, hostAlertMemoryHigh:
			if !recording {
				out[rule.ID] = unjudgedHostAlert(metricsOff)
				continue
			}
			column := "cpu_percent"
			if rule.Kind == hostAlertMemoryHigh {
				column = "mem_percent"
			}
			from := now.Add(-time.Duration(rule.Params.Minutes) * time.Minute)
			window, err := readHostWindow(ctx, s.Store.DB, column, from, now)
			if err != nil {
				out[rule.ID] = unjudgedHostAlert("The recorded samples could not be read.")
				continue
			}
			reading := usageAlertReading(rule, window, interval, server, now)
			if len(reading.Firing) > 0 {
				// Who is using it is what the reader does next, and the
				// recorder already knows; asked only once there is a message.
				reading.Firing[0].Detail += busiestContainer(ctx, s.Store.DB, rule.Kind, from, now)
			}
			out[rule.ID] = reading
		case hostAlertDiskFilling:
			switch {
			case !recording:
				out[rule.ID] = unjudgedHostAlert(metricsOff)
			case mountsErr != nil:
				out[rule.ID] = unjudgedHostAlert("The recorded filesystem samples could not be read.")
			default:
				out[rule.ID] = diskAlertReading(rule, mounts, mountsAt, interval, now)
			}
		case hostAlertContainerDown:
			out[rule.ID] = containerAlertReading(rule, containers, containersRead)
		case hostAlertBackupFailed:
			if jobsErr != nil {
				out[rule.ID] = unjudgedHostAlert("The backup jobs could not be read.")
				continue
			}
			out[rule.ID] = backupAlertReading(jobs, leftToDB, now)
		}
	}
	return out
}

const metricsOff = "Metrics history is off on this host (JD_METRICS_RETENTION=0), so there is no record to judge."

// --- CPU and memory ------------------------------------------------------------

// hostWindow is one recorded series over a rule's window.
type hostWindow struct {
	Samples     int
	Mean, Peak  float64
	First, Last time.Time
}

// readHostWindow reads the mean and peak of one column of metric_samples over
// (from, to]. The column is one of two constants, never a request's.
func readHostWindow(ctx context.Context, db *sql.DB, column string, from, to time.Time) (hostWindow, error) {
	if column != "cpu_percent" && column != "mem_percent" {
		return hostWindow{}, fmt.Errorf("unknown metric column %q", column)
	}
	var w hostWindow
	var first, last int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(AVG(`+column+`),0), COALESCE(MAX(`+column+`),0),
		COALESCE(MIN(ts),0), COALESCE(MAX(ts),0) FROM metric_samples WHERE ts > ? AND ts <= ?`,
		from.Unix(), to.Unix()).Scan(&w.Samples, &w.Mean, &w.Peak, &first, &last)
	if err != nil {
		return w, err
	}
	if w.Samples > 0 {
		w.First, w.Last = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
	}
	return w, nil
}

// hostStale is how old the newest sample may be before the record is taken
// to have stopped: three intervals, and never under two minutes, so one
// sample written late is not an outage of the recorder.
func hostStale(interval time.Duration) time.Duration {
	return max(3*interval, 2*time.Minute)
}

// usageAlertReading judges CPU or memory: the mean of the recorded samples
// over the rule's window against its line. A window the record does not yet
// cover — the recorder started a few minutes ago, or stopped — is not judged,
// because a mean of three minutes is not ten minutes held.
func usageAlertReading(rule hostAlertRule, w hostWindow, interval time.Duration, server string, now time.Time) hostAlertReading {
	what, subject := "CPU", "cpu"
	if rule.Kind == hostAlertMemoryHigh {
		what, subject = "Memory", "memory"
	}
	minutes := rule.Params.Minutes
	if w.Samples < 2 || now.Sub(w.Last) > hostStale(interval) {
		return unjudgedHostAlert(fmt.Sprintf("No %s sample has been recorded in the last %d min.", strings.ToLower(what), minutes))
	}
	start := now.Add(-time.Duration(minutes) * time.Minute)
	if slack := max(2*interval, time.Minute); w.First.Sub(start) > slack {
		have := int(now.Sub(w.First).Minutes())
		return unjudgedHostAlert(fmt.Sprintf("Only %d of the %d minutes it averages are recorded so far.", have, minutes))
	}
	label := server
	if label == "" {
		label = "this server"
	}
	reading := newHostAlertReading()
	line := rule.Params.Threshold
	detail := fmt.Sprintf("%s averaged %s over the last %d min (peak %s)", what, dbAlertPercent(w.Mean), minutes, dbAlertPercent(w.Peak))
	switch {
	case w.Mean >= line:
		reading.Firing = append(reading.Firing, hostAlertFinding{subject, label,
			fmt.Sprintf("%s; the line is %s.", detail, dbAlertPercent(line))})
	case w.Mean < hostAlertClearLine(line):
		reading.Now[subject], reading.Labels[subject] = detail+".", label
	default:
		reading.Unknown[subject] = true
	}
	return reading
}

// busiestContainer names the container that used the most CPU or memory over
// the window, as a sentence to end the message with, or nothing when none
// was recorded.
func busiestContainer(ctx context.Context, db *sql.DB, kind string, from, to time.Time) string {
	column := "cpu_percent"
	if kind == hostAlertMemoryHigh {
		column = "mem_bytes"
	}
	var name string
	var mean float64
	err := db.QueryRowContext(ctx, `SELECT name, AVG(`+column+`) FROM metric_container_samples
		WHERE ts > ? AND ts <= ? GROUP BY name ORDER BY 2 DESC LIMIT 1`, from.Unix(), to.Unix()).Scan(&name, &mean)
	if err != nil || name == "" || mean <= 0 {
		return ""
	}
	if kind == hostAlertMemoryHigh {
		return fmt.Sprintf(" The largest container was %s, %s on average.", name, alertBytes(int64(mean)))
	}
	// Docker counts a core as 100%, where the host's figure is of all of them.
	return fmt.Sprintf(" The busiest container was %s, %.1f cores on average.", name, mean/100)
}

// --- filesystems ---------------------------------------------------------------

type hostMount struct {
	Mountpoint    string
	UsedPercent   float64
	Used, Total   int64
	InodesPercent float64
}

// readHostMounts reads the recorder's newest filesystem sample: every mount
// is written at the host sample's timestamp, so the newest one is all of them.
func readHostMounts(ctx context.Context, db *sql.DB) ([]hostMount, time.Time, error) {
	rows, err := db.QueryContext(ctx, `SELECT mountpoint, used_percent, used_bytes, total_bytes, inodes_percent, ts
		FROM metric_mount_samples WHERE ts = (SELECT MAX(ts) FROM metric_mount_samples) ORDER BY mountpoint`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	out := []hostMount{}
	var at int64
	for rows.Next() {
		var m hostMount
		if err := rows.Scan(&m.Mountpoint, &m.UsedPercent, &m.Used, &m.Total, &m.InodesPercent, &at); err != nil {
			return nil, time.Time{}, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	if at == 0 {
		return out, time.Time{}, nil
	}
	return out, time.Unix(at, 0).UTC(), nil
}

// diskAlertReading judges each filesystem by its space and its inodes,
// whichever is fuller: one that has run out of inodes reports free space and
// still refuses to create a file. A mount missing from the newest sample has
// been unmounted, and leaves.
func diskAlertReading(rule hostAlertRule, mounts []hostMount, at time.Time, interval time.Duration, now time.Time) hostAlertReading {
	if at.IsZero() || now.Sub(at) > hostStale(interval) {
		return unjudgedHostAlert("No filesystem has been recorded in the last few minutes.")
	}
	line := rule.Params.Threshold
	clear := hostAlertClearLine(line)
	reading := newHostAlertReading()
	for _, m := range mounts {
		space := fmt.Sprintf("%s of %s is used (%s of %s)", dbAlertPercent(m.UsedPercent), m.Mountpoint, alertBytes(m.Used), alertBytes(m.Total))
		parts := []string{}
		if m.UsedPercent >= line {
			parts = append(parts, space)
		}
		if m.InodesPercent >= line {
			parts = append(parts, fmt.Sprintf("%s of its inodes are used", dbAlertPercent(m.InodesPercent)))
		}
		switch {
		case len(parts) > 0:
			reading.Firing = append(reading.Firing, hostAlertFinding{m.Mountpoint, m.Mountpoint,
				fmt.Sprintf("%s; the line is %s.", strings.Join(parts, ", and "), dbAlertPercent(line))})
		case m.UsedPercent < clear && m.InodesPercent < clear:
			reading.Now[m.Mountpoint], reading.Labels[m.Mountpoint] = space+".", m.Mountpoint
		default:
			reading.Unknown[m.Mountpoint] = true
		}
	}
	return reading
}

// --- containers ----------------------------------------------------------------

// hostContainer is one container as a pass saw it, with what the pass
// gathered beyond the listing.
type hostContainer struct {
	ID, Name, State, Health string
	Labels                  map[string]string
	// RecentRestarts is how many times Docker restarted it, by its restart
	// policy, over the last hostRestartWindow, as the passes counted.
	RecentRestarts int
	// Exit is a stopped container's last run; nil when it could not be read.
	Exit *hostContainerExit
	// Kernel says why, for a MongoDB 8 container on a kernel it refuses to
	// start on (mongoKernelAlertLine); empty for every other.
	Kernel string
}

type hostContainerExit struct {
	dockerx.ContainerExit
	// Requested is a kill or stop Docker recorded for it while it ran:
	// somebody — this dashboard, a deploy's cutover, the CLI, compose —
	// asked it to stop.
	Requested bool
	// Witnessed says the event log was listening and still holds the
	// minutes before it stopped, so a kill missing from it is evidence.
	Witnessed bool
}

const (
	// hostRestartWindow and hostRestartLoop: three restarts in ten minutes is
	// a loop. Docker doubles its delay between restarts up to a minute, so a
	// crashing container restarts many more times than that; one restart
	// after an update is not a loop.
	hostRestartWindow = 10 * time.Minute
	hostRestartLoop   = 3
)

type hostRestartMark struct {
	at    time.Time
	count int
}

type hostCachedExit struct {
	key  string
	exit hostContainerExit
}

// noteRestarts keeps one running container's restart count and answers how
// many restarts the window holds. Docker counts only the restarts its policy
// made, so a deploy or an operator restarting it counts nothing.
func (a *hostAlerts) noteRestarts(id string, count int, now time.Time) int {
	marks := append(a.restarts[id], hostRestartMark{now, count})
	cut := 0
	for cut < len(marks)-1 && now.Sub(marks[cut].at) > hostRestartWindow {
		cut++
	}
	marks = marks[cut:]
	a.restarts[id] = marks
	return max(marks[len(marks)-1].count-marks[0].count, 0)
}

// exitStatus is the exit code Docker's listing prints for a stopped
// container, which is what a cached exit is checked against: a container that
// ran again and stopped differently is inspected again.
var exitStatus = regexp.MustCompile(`^Exited \((-?\d+)\)`)

// observeHostContainers lists every container and gathers, for the ones that
// stopped, how they stopped. Only a stopped container is inspected beyond the
// listing, and once while it stays stopped.
func (s *Server) observeHostContainers(ctx context.Context, a *hostAlerts, now time.Time) ([]hostContainer, bool) {
	if s.modules.docker == nil {
		return nil, false
	}
	read, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := s.modules.docker.ListContainers(read, true)
	if err != nil {
		return nil, false
	}
	kernel := s.hostKernel(read)
	seen := map[string]bool{}
	out := make([]hostContainer, 0, len(list))
	for _, ct := range list {
		seen[ct.ID] = true
		item := hostContainer{ID: ct.ID, Name: ct.Name, State: ct.State, Health: ct.Health, Labels: ct.Labels,
			Kernel: mongoKernelAlertLine(kernel, ct)}
		switch ct.State {
		case "running":
			delete(a.exits, ct.ID)
			// A container not inspected has no restart count to compare.
			if ct.Inspected {
				item.RecentRestarts = a.noteRestarts(ct.ID, ct.Restarts, now)
			}
		case "exited", "dead":
			key := ct.State
			if m := exitStatus.FindStringSubmatch(ct.Status); m != nil {
				key += ":" + m[1]
			}
			if cached, ok := a.exits[ct.ID]; ok && cached.key == key {
				exit := cached.exit
				item.Exit = &exit
				break
			}
			last, err := s.modules.docker.LastExit(read, ct.ID)
			if err != nil {
				break
			}
			exit := s.judgeStop(ct.ID, last)
			a.exits[ct.ID] = hostCachedExit{key, exit}
			item.Exit = &exit
		}
		out = append(out, item)
	}
	for id := range a.restarts {
		if !seen[id] {
			delete(a.restarts, id)
		}
	}
	for id := range a.exits {
		if !seen[id] {
			delete(a.exits, id)
		}
	}
	return out, true
}

// judgeStop asks the Docker event log whether a stop was asked for. Docker
// records a kill — docker stop's SIGTERM, compose's, a deploy's cutover — and
// a stop, but a process that crashes or is killed by the kernel gets only its
// die.
func (s *Server) judgeStop(id string, last dockerx.ContainerExit) hostContainerExit {
	exit := hostContainerExit{ContainerExit: last}
	events := s.modules.dockerEvents
	if events == nil || last.FinishedAt.IsZero() {
		return exit
	}
	from := last.FinishedAt.Add(-10 * time.Minute)
	if last.StartedAt.After(from) {
		from = last.StartedAt
	}
	exit.Witnessed = events.Covers(from)
	for _, ev := range events.Find(200, dockerx.EventFilter{Kinds: []string{"container"}, Container: id}) {
		if (ev.Action == "kill" || ev.Action == "stop") && !ev.Time.Before(from.Add(-time.Second)) &&
			!ev.Time.After(last.FinishedAt.Add(5*time.Second)) {
			exit.Requested = true
			break
		}
	}
	return exit
}

// hostContainerSkipped is a container that is never a subject: a deploy's
// release task, a migration rehearsal's throwaway copy of a database and a
// `compose run` are one-shot by design, and a container that was never
// started, or is being removed, has nothing to be down from.
func hostContainerSkipped(ct hostContainer) bool {
	if ct.Labels["io.just-dashboard.release-task"] != "" || ct.Labels[rehearsalLabel] != "" ||
		strings.EqualFold(ct.Labels["com.docker.compose.oneoff"], "true") {
		return true
	}
	return ct.State == "created" || ct.State == "removing"
}

// containerAlertReading judges every container. Running is fine unless its
// health check says unhealthy or Docker keeps restarting it; a stopped one is
// down only when it stopped after the rule began watching and nobody asked it
// to (judgeExit). A container removed — a deploy's cutover removes the old
// one — is not in the listing, and leaves.
func containerAlertReading(rule hostAlertRule, list []hostContainer, listed bool) hostAlertReading {
	if !listed {
		return unjudgedHostAlert("Docker did not answer, so its containers could not be listed.")
	}
	reading := newHostAlertReading()
	for _, ct := range list {
		if hostContainerSkipped(ct) {
			continue
		}
		subject := ct.Name
		if subject == "" {
			subject = dockerx.ShortID(ct.ID)
		}
		fire := func(detail string) {
			reading.Firing = append(reading.Firing, hostAlertFinding{subject, subject, detail})
		}
		fine := func(detail string) {
			reading.Now[subject], reading.Labels[subject] = detail, subject
		}
		switch ct.State {
		case "running":
			switch {
			case ct.Health == "unhealthy":
				fire("It is running, but its health check reports it unhealthy.")
			case ct.RecentRestarts >= hostRestartLoop:
				fire(withKernelLine(fmt.Sprintf("Docker restarted it %d times in the last %d min; it keeps crashing.", ct.RecentRestarts, int(hostRestartWindow.Minutes())), ct))
			case ct.RecentRestarts > 0:
				// One or two restarts: not a loop yet, and not over either.
				reading.Unknown[subject] = true
			case ct.Health == "healthy":
				fine("It is running and healthy.")
			default:
				fine("It is running.")
			}
		case "restarting":
			fire(withKernelLine("Docker is restarting it: it exited and its restart policy is bringing it back.", ct))
		case "paused":
			// A backup freezes a container while it archives its volumes.
			fine("It is paused, not stopped.")
		case "exited", "dead":
			if ct.Exit == nil {
				reading.Unknown[subject] = true
				continue
			}
			if !ct.Exit.FinishedAt.IsZero() && ct.Exit.FinishedAt.Before(rule.watchFrom) {
				// Stopped before the rule watched: not one it saw go down.
				continue
			}
			if down, detail := judgeExit(ct.State, *ct.Exit); down {
				fire(withKernelLine(detail, ct))
			} else {
				fine(detail)
			}
		}
	}
	return reading
}

// withKernelLine adds why to what a down container's alert says, where it is
// MongoDB 8 on a kernel it does not start on.
func withKernelLine(detail string, ct hostContainer) string {
	if ct.Kernel == "" {
		return detail
	}
	return detail + " " + ct.Kernel
}

// judgeExit says whether a stopped container is down, and how it stopped.
// What it cannot tell apart it leaves alone: a container killed while
// nothing was listening is taken to have been stopped, because docker stop
// kills what outlasts its grace period and a false page is worse than a
// late one.
func judgeExit(state string, exit hostContainerExit) (bool, string) {
	when := ""
	if !exit.FinishedAt.IsZero() {
		when = " at " + alertTime(exit.FinishedAt)
	}
	code := exit.ExitCode
	switch {
	case state == "dead":
		detail := "Docker marked it dead" + when + ": it could not be stopped or removed cleanly."
		if exit.Error != "" {
			detail += " Docker said: " + exit.Error
		}
		return true, detail
	case exit.OOMKilled:
		return true, fmt.Sprintf("It was killed for running out of memory%s (exit %d).", when, code)
	case exit.Requested:
		return false, fmt.Sprintf("It was stopped on purpose%s (exit %d).", when, code)
	case code == 0:
		return false, "It exited cleanly" + when + ": it finished rather than failed."
	case code == 130 || code == 143:
		return false, fmt.Sprintf("It was stopped by a signal%s (exit %d).", when, code)
	case code == 137 && !exit.Witnessed:
		return false, "It was killed" + when + " (exit 137), most likely by a stop that outlasted its grace period."
	case code == 137:
		return true, "It was killed" + when + " (exit 137) with no stop or kill from Docker; the kernel's out-of-memory killer is the usual cause."
	}
	detail := fmt.Sprintf("It exited with code %d%s.", code, when)
	if exit.Error != "" {
		detail += " Docker said: " + exit.Error
	}
	return true, detail
}

// --- backups -------------------------------------------------------------------

// databaseBackupRules answers whether an enabled database alert rule of kind
// backup_failed already follows a job. Such a job is left to that rule — the
// database's Alerts page owns it, with its cooldown and its link to the
// database — so one failed run is not told twice. A job no such rule follows
// (files, volumes, or dumps nobody watches from the database side) is the
// host rule's.
func databaseBackupRules(ctx context.Context, db *sql.DB) (func(dbAlertJob) bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT connection_id FROM db_alert_rules WHERE enabled=1 AND kind=?`, dbx.AlertBackupFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []*int64
	for rows.Next() {
		var conn sql.NullInt64
		if err := rows.Scan(&conn); err != nil {
			return nil, err
		}
		rules = append(rules, nullID(conn))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return func(job dbAlertJob) bool {
		for _, conn := range rules {
			if job.dumps(conn) {
				return true
			}
		}
		return false
	}, nil
}

// backupAlertReading judges every backup job by its newest finished run, in
// the database alerts' own words (judgeBackupJob): a job that failed stays
// failed until a run succeeds.
func backupAlertReading(jobs []dbAlertJob, leftToDB func(dbAlertJob) bool, now time.Time) hostAlertReading {
	reading := newHostAlertReading()
	rule := dbAlertRule{Kind: dbx.AlertBackupFailed}
	for _, job := range jobs {
		subject := "job:" + strconv.FormatInt(job.ID, 10)
		if leftToDB != nil && leftToDB(job) {
			reading.Now[subject], reading.Labels[subject] = "A database alert rule follows this job now, so this rule leaves it to that one.", job.Name
			continue
		}
		judged := judgeBackupJob(rule, job, now)
		switch {
		case judged.Unknown != "":
			reading.Unknown[subject] = true
		case judged.Over:
			reading.Firing = append(reading.Firing, hostAlertFinding{subject, job.Name, judged.Detail})
		default:
			reading.Now[subject], reading.Labels[subject] = judged.Detail, job.Name
		}
	}
	return reading
}
