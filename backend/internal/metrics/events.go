package metrics

import (
	"context"
	"fmt"
	"time"
)

// Event is something that happened to the server, positioned in time so a
// chart can mark it.
//
// This is the half of a metrics story that a graph on its own cannot tell.
// "Memory climbed from 40% to 85% at 14:20" is an observation; "memory climbed
// from 40% to 85% at 14:20, and api-server was deployed at 14:19" is a cause.
// Grafana calls these annotations and expects you to wire up a data source to
// supply them; the dashboard is already the thing that ran the deploy, took
// the backup and rebooted the box, so it can simply answer.
type Event struct {
	TS   time.Time `json:"ts"`
	Kind string    `json:"kind"`
	// Title is what goes on the marker; Detail is what goes in its tooltip.
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Severity picks the marker's colour: "info", "warning" or "error".
	Severity string `json:"severity"`
	// DurationSeconds is non-zero for events that occupied a span rather than
	// an instant, so a twenty-minute deploy can be drawn as the band it was
	// instead of a line at the moment it started.
	DurationSeconds int `json:"durationSeconds,omitempty"`
	// ProjectID is the project a deploy event released, so its row can carry
	// the project's own icon.
	ProjectID int64 `json:"projectId,omitempty"`
}

// Events collects everything notable in a window, newest last so a client can
// walk it alongside an ascending series.
//
// Every source is optional in the sense that a failure to read one is not
// allowed to lose the others: a missing deploy table on some future stripped
// build should cost the deploy markers, not the whole overlay.
func (r *Recorder) Events(ctx context.Context, from, to time.Time, limit int) ([]Event, error) {
	if to.Before(from) {
		from, to = to, from
	}
	if limit < 1 {
		limit = 200
	}
	out := make([]Event, 0, 32)
	// A per-source cap rather than one shared budget: a chatty audit log
	// would otherwise crowd out the handful of deploy markers, which are the
	// ones worth seeing.
	per := limit/4 + 1

	out = append(out, r.deployEvents(ctx, from, to, per)...)
	out = append(out, r.backupEvents(ctx, from, to, per)...)
	out = append(out, r.rebootEvents(ctx, from, to)...)
	out = append(out, r.auditEvents(ctx, from, to, per)...)

	sortEvents(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (r *Recorder) deployEvents(ctx context.Context, from, to time.Time, limit int) []Event {
	rows, err := r.db.QueryContext(ctx, `
		SELECT d.started_at, d.ended_at, d.status, d.trigger, d.actor, p.id, p.name
		  FROM deploy_runs d
		  JOIN deploy_projects p ON p.id = d.project_id
		 WHERE d.started_at >= ? AND d.started_at <= ?
		 ORDER BY d.started_at DESC
		 LIMIT ?`, from.Unix(), to.Unix(), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var started, ended, project int64
		var status, trigger, actor, name string
		if err := rows.Scan(&started, &ended, &status, &trigger, &actor, &project, &name); err != nil {
			return out
		}
		e := Event{
			TS:        time.Unix(started, 0).UTC(),
			Kind:      "deploy",
			Title:     name,
			Severity:  statusSeverity(status),
			Detail:    fmt.Sprintf("deploy %s (%s)", status, trigger),
			ProjectID: project,
		}
		if actor != "" {
			e.Detail += " by " + actor
		}
		if ended > started {
			e.DurationSeconds = int(ended - started)
		}
		out = append(out, e)
	}
	return out
}

func (r *Recorder) backupEvents(ctx context.Context, from, to time.Time, limit int) []Event {
	rows, err := r.db.QueryContext(ctx, `
		SELECT b.started_at, b.ended_at, b.status, b.size_bytes, j.name
		  FROM backup_runs b
		  JOIN backup_jobs j ON j.id = b.job_id
		 WHERE b.started_at >= ? AND b.started_at <= ? AND b.imported = 0
		 ORDER BY b.started_at DESC
		 LIMIT ?`, from.Unix(), to.Unix(), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var started, ended, size int64
		var status, name string
		if err := rows.Scan(&started, &ended, &status, &size, &name); err != nil {
			return out
		}
		e := Event{
			TS:       time.Unix(started, 0).UTC(),
			Kind:     "backup",
			Title:    name,
			Severity: statusSeverity(status),
			Detail:   fmt.Sprintf("backup %s", status),
		}
		if size > 0 {
			e.Detail += fmt.Sprintf(" · %s", humanBytes(size))
		}
		if ended > started {
			e.DurationSeconds = int(ended - started)
		}
		out = append(out, e)
	}
	return out
}

// rebootEvents finds restarts in the recorded series itself.
//
// Nothing writes a "the server rebooted" row, and nothing needs to: uptime is
// already stored on every sample, and a sample whose uptime is lower than the
// one before it can only mean the machine went down in between. The moment is
// the reboot, not the sample, so it is placed by subtracting the new uptime
// from the sample's own timestamp.
//
// This also catches the case a marker is most wanted for: the reboot nobody
// initiated from this dashboard, which by definition left no audit entry.
func (r *Recorder) rebootEvents(ctx context.Context, from, to time.Time) []Event {
	// One sample of slack on the lower bound so a reboot in the first bucket
	// of the window still has a predecessor to be compared against.
	lower := from.Add(-2 * r.interval).Unix()
	rows, err := r.db.QueryContext(ctx, `
		SELECT ts, uptime_seconds FROM metric_samples
		 WHERE ts >= ? AND ts <= ? AND uptime_seconds > 0
		 ORDER BY ts`, lower, to.Unix())
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Event
	var prev int64 = -1
	for rows.Next() {
		var ts, uptime int64
		if err := rows.Scan(&ts, &uptime); err != nil {
			return out
		}
		if prev >= 0 && uptime < prev {
			at := time.Unix(ts-uptime, 0).UTC()
			// A reboot detected from a sample just before the window opened
			// belongs to the previous window, not this one.
			if !at.Before(from) && !at.After(to) {
				out = append(out, Event{
					TS:       at,
					Kind:     "reboot",
					Title:    "Server restarted",
					Severity: "warning",
					Detail:   "uptime counter reset",
				})
			}
		}
		prev = uptime
	}
	return out
}

// auditEvents surfaces the state-changing requests worth a mark on a chart.
//
// Deliberately not every audited request: a chart peppered with a marker for
// every GET-shaped mutation is a chart nobody reads. The filter is "actions
// that plausibly explain a step change in a graph" — restarts, stops, deploys,
// service control — plus every failure, since a failed destructive action is
// worth seeing next to whatever the machine did afterwards.
func (r *Recorder) auditEvents(ctx context.Context, from, to time.Time, limit int) []Event {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ts, action, target, username, success
		  FROM audit_log
		 WHERE ts >= ? AND ts <= ?
		   AND (success = 0 OR action LIKE '%restart%' OR action LIKE '%stop%'
		        OR action LIKE '%start%' OR action LIKE '%kill%'
		        OR action LIKE '%signal%' OR action LIKE '%prune%'
		        OR action LIKE '%remove%' OR action LIKE '%delete%')
		 ORDER BY ts DESC
		 LIMIT ?`, from.Unix(), to.Unix(), limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ts int64
		var action, target, username string
		var success bool
		if err := rows.Scan(&ts, &action, &target, &username, &success); err != nil {
			return out
		}
		severity := "info"
		if !success {
			severity = "error"
		}
		title := action
		if target != "" {
			title = action + " " + target
		}
		out = append(out, Event{
			TS:       time.Unix(ts, 0).UTC(),
			Kind:     "action",
			Title:    title,
			Severity: severity,
			Detail:   "by " + username,
		})
	}
	return out
}

func statusSeverity(status string) string {
	switch status {
	case "failed", "error":
		return "error"
	case "running", "pending":
		return "info"
	default:
		return "info"
	}
}

// sortEvents orders ascending by time. An insertion sort rather than
// sort.Slice: the slice is a few dozen entries assembled from four already
// sorted runs, and this keeps equal timestamps in source order so a deploy and
// the restart it caused do not swap places between requests.
func sortEvents(events []Event) {
	for i := 1; i < len(events); i++ {
		e := events[i]
		j := i - 1
		for j >= 0 && events[j].TS.After(e.TS) {
			events[j+1] = events[j]
			j--
		}
		events[j+1] = e
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
