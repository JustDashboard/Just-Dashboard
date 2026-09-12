package dockerx

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Why did it stop, and why does it keep stopping.
//
// The facts are all on screen already and none of them is an answer:
//
//	Restart count: 17
//	Exit code: 137
//	Memory limit: 512 MB
//
// An operator who has done this for years reads that as "it is exceeding its
// memory limit and the kernel is killing it"; everybody else reads three
// numbers. This assembles them into one, and — because the assembly is an
// inference and not a reading — labels the conclusion as likely rather than
// certain. "Likely cause" is a phrase somebody can disagree with. "Cause" is
// not, and being confidently wrong about why a service is down is worse than
// saying nothing.

// FailureDiagnosis is everything known about why a container is not working.
type FailureDiagnosis struct {
	ContainerID string    `json:"containerId"`
	Name        string    `json:"name"`
	CheckedAt   time.Time `json:"checkedAt"`

	// State is what it is doing now: "healthy", "stopped", "looping",
	// "unhealthy", "flapping" or "running". A container that has been stable
	// for a week is a valid answer and is worth saying so.
	State string `json:"state"`
	// Headline is the one-line summary; Likely is the inferred cause, empty
	// when nothing can honestly be concluded.
	Headline string `json:"headline"`
	Likely   string `json:"likely,omitempty"`
	// Confidence is "observed" when the evidence states the cause outright —
	// the kernel recording an OOM kill, say — and "inferred" when it was
	// worked out. Nothing is ever reported as certain that was not read
	// directly.
	Confidence string `json:"confidence"`

	// The evidence, each item a fact with its source named.
	Evidence []FailureEvidence `json:"evidence"`
	// Restarts describes the cadence, which is what separates "it crashed
	// once" from "it is looping".
	Restarts RestartPattern `json:"restarts"`
	// Suggestions are what to do next, in order of how likely they are to
	// help.
	Suggestions []string `json:"suggestions"`
	// LogWindow is the time range worth reading logs from, so the UI can open
	// the log viewer already pointed at the failure rather than at the tail.
	LogWindow *LogWindow `json:"logWindow,omitempty"`
}

// FailureEvidence is one fact, and where it came from.
type FailureEvidence struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Source string `json:"source"`
	// Weight is "decisive", "supporting" or "context", so the UI can lead with
	// what actually settles the question.
	Weight string `json:"weight"`
}

// RestartPattern is how often it has come back.
type RestartPattern struct {
	Count int `json:"count"`
	// Window is how much time the events below cover, and Recent is how many
	// restarts happened inside it. A count of 400 accumulated over a year is
	// not the same as 17 in twelve minutes, and Docker's RestartCount alone
	// cannot tell them apart.
	Window  string    `json:"window,omitempty"`
	Recent  int       `json:"recent"`
	Looping bool      `json:"looping"`
	Since   time.Time `json:"since,omitempty"`
	Summary string    `json:"summary,omitempty"`
}

// LogWindow is a time range to read logs over.
type LogWindow struct {
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

// DiagnoseFailure explains what is wrong with one container.
//
// The event log is consulted when one is available: Docker keeps no history of
// its own, so "17 restarts in 12 minutes" is only sayable because this process
// has been listening. Without it the diagnosis still works and simply says
// less, rather than inventing a cadence from a lifetime counter.
func (c *Client) DiagnoseFailure(ctx context.Context, id string, events *EventLog) (*FailureDiagnosis, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(insp.Name, "/")
	d := &FailureDiagnosis{
		ContainerID: insp.ID,
		Name:        name,
		CheckedAt:   time.Now().UTC(),
		Confidence:  "inferred",
		Evidence:    []FailureEvidence{},
		Suggestions: []string{},
	}
	d.Restarts = restartPattern(insp, events, name)
	collectFailureEvidence(d, insp)
	concludeFailure(d, insp)
	return d, nil
}

// restartPattern works out the cadence from the event log where there is one.
func restartPattern(insp containerInspect, events *EventLog, name string) RestartPattern {
	p := RestartPattern{Count: insp.RestartCount}
	if insp.State != nil {
		if started := parseDockerTime(insp.State.StartedAt); !started.IsZero() {
			p.Since = started.UTC()
		}
	}
	if events == nil {
		if p.Count >= restartLoopCount && !p.Since.IsZero() && time.Since(p.Since) < time.Hour {
			p.Looping = true
			p.Summary = itoa(p.Count) + " restarts, the most recent " + humanDuration(time.Since(p.Since)) + " ago"
		} else if p.Count > 0 {
			p.Summary = plural(p.Count, "restart since it was created", "restarts since it was created")
		}
		return p
	}

	// Count the starts this process actually watched. This is the number that
	// distinguishes a loop from a long uptime with an old scar.
	var first, last time.Time
	for _, ev := range events.Recent(eventBufferSize, []string{"container"}, "") {
		if ev.Name != name {
			continue
		}
		if ev.Action != "start" && ev.Action != "restart" {
			continue
		}
		p.Recent++
		if last.IsZero() || ev.Time.After(last) {
			last = ev.Time
		}
		if first.IsZero() || ev.Time.Before(first) {
			first = ev.Time
		}
	}
	if p.Recent > 1 && !first.IsZero() {
		span := last.Sub(first)
		p.Window = humanDuration(span)
		// Two or more starts inside an hour is a loop; the same two across a
		// week is a service that was restarted twice.
		p.Looping = p.Recent >= restartLoopCount && span < time.Hour
		p.Summary = itoa(p.Recent) + " starts in " + p.Window
	} else if p.Count > 0 {
		p.Summary = plural(p.Count, "restart since it was created", "restarts since it was created")
	}
	return p
}

func collectFailureEvidence(d *FailureDiagnosis, insp containerInspect) {
	add := func(label, value, source, weight string) {
		if value == "" {
			return
		}
		d.Evidence = append(d.Evidence, FailureEvidence{
			Label: label, Value: value, Source: source, Weight: weight,
		})
	}
	state := insp.State
	if state == nil {
		return
	}
	add("State", state.Status, "docker inspect", "context")
	if !state.Running {
		add("Exit code", itoa(state.ExitCode)+" — "+exitCodeMeaning(state.ExitCode), "docker inspect", "decisive")
	}
	if state.OOMKilled {
		add("OOM killed", "yes — the kernel stopped it for exceeding its memory allowance", "the kernel, via docker inspect", "decisive")
	}
	if state.Error != "" {
		add("Docker's own error", state.Error, "docker inspect", "decisive")
	}
	if insp.HostConfig != nil {
		if insp.HostConfig.Memory > 0 {
			add("Memory limit", humanBytes(insp.HostConfig.Memory), "docker inspect", "supporting")
		} else {
			add("Memory limit", "none set, so it competes with everything else on this server", "docker inspect", "supporting")
		}
		if policy := string(insp.HostConfig.RestartPolicy.Name); policy != "" && policy != "no" {
			add("Restart policy", policy+" — Docker keeps bringing it back, which is why a failure can repeat quietly", "docker inspect", "context")
		}
	}
	if state.Health != nil {
		add("Health check", state.Health.Status+" after "+itoa(state.Health.FailingStreak)+" consecutive failures",
			"the container's own health check", "decisive")
		if n := len(state.Health.Log); n > 0 {
			if out := strings.TrimSpace(state.Health.Log[n-1].Output); out != "" {
				add("Health check output", truncate(out, 400), "the container's own health check", "decisive")
			}
		}
	}
	if d.Restarts.Summary != "" {
		source := "docker inspect"
		if d.Restarts.Recent > 0 {
			source = "the dashboard's event log"
		}
		add("Restarts", d.Restarts.Summary, source, "supporting")
	}
	sort.SliceStable(d.Evidence, func(i, j int) bool {
		return evidenceRank(d.Evidence[i].Weight) < evidenceRank(d.Evidence[j].Weight)
	})
}

func evidenceRank(w string) int {
	switch w {
	case "decisive":
		return 0
	case "supporting":
		return 1
	default:
		return 2
	}
}

// concludeFailure picks the state, the headline and — where the evidence
// supports one — a likely cause.
func concludeFailure(d *FailureDiagnosis, insp containerInspect) {
	state := insp.State
	if state == nil {
		d.State, d.Headline = "unknown", "Docker reported no state for this container."
		return
	}
	limit := int64(0)
	if insp.HostConfig != nil {
		limit = insp.HostConfig.Memory
	}

	switch {
	case state.OOMKilled:
		// The one case where the cause is read rather than inferred: the
		// kernel wrote it down.
		d.State = "stopped"
		d.Confidence = "observed"
		d.Headline = d.Name + " was killed for using too much memory."
		if limit > 0 {
			d.Likely = "It exceeded its " + humanBytes(limit) + " memory limit and the kernel stopped it."
			d.Suggestions = append(d.Suggestions,
				"Look at its memory history before raising the limit: a steady climb is a leak that a bigger limit only delays, a spike is one expensive request.",
				"If the workload genuinely needs more, raise the limit rather than removing it — a container with no limit takes the whole server down with it instead.")
		} else {
			d.Likely = "It has no memory limit, so it competed with everything else on this server until the kernel picked a victim."
			d.Suggestions = append(d.Suggestions,
				"Set a memory limit. Without one the kernel chooses what to kill by its own arithmetic, and the process it picks is often not the one that caused the problem.")
		}

	case d.Restarts.Looping:
		d.State = "looping"
		d.Headline = "Restart loop detected: " + d.Restarts.Summary + "."
		d.Likely, d.Suggestions = loopCause(state, limit)
		if since := d.Restarts.Since; !since.IsZero() {
			d.LogWindow = &LogWindow{
				Since:  since.Add(-2 * time.Minute),
				Until:  time.Now().UTC(),
				Reason: "the window around the most recent start, which is where the failure repeats",
			}
		}

	case !state.Running && state.ExitCode != 0:
		d.State = "stopped"
		d.Headline = d.Name + " exited with status " + itoa(state.ExitCode) + "."
		d.Likely = exitCodeMeaning(state.ExitCode)
		d.Suggestions = append(d.Suggestions,
			"Its last log lines before the exit are where the reason will be — the program's own, not Docker's.")

	case !state.Running:
		d.State = "stopped"
		d.Headline = d.Name + " exited cleanly."
		d.Likely = "Status 0 means the program decided its work was done. For a one-off job that is success; for a service it means something told it to stop, or its main process finished."

	case state.Health != nil && state.Health.Status == "unhealthy":
		d.State = "unhealthy"
		d.Confidence = "observed"
		d.Headline = d.Name + " is running but failing its own health check."
		d.Likely = "The process is alive and is not answering. Whatever depends on it is failing while Docker reports the container as up."
		d.Suggestions = append(d.Suggestions,
			"The health check's own output is the first thing to read — it is in the evidence above.",
			"A service that starts and then fails its check usually cannot reach something else: a database, a queue, a file it expects.")

	case state.Restarting:
		d.State = "flapping"
		d.Headline = d.Name + " is restarting."
		d.Likely = "Docker is between attempts. If this persists it is a loop rather than a recovery."

	default:
		d.State = "running"
		d.Confidence = "observed"
		if started := parseDockerTime(state.StartedAt); !started.IsZero() {
			d.Headline = d.Name + " has been running for " + humanDuration(time.Since(started)) + " with nothing to report."
		} else {
			d.Headline = d.Name + " is running with nothing to report."
		}
		if d.Restarts.Count > 0 {
			d.Headline += " It has restarted " + plural(d.Restarts.Count, "time", "times") + " since it was created."
		}
	}

	if !state.Running && d.LogWindow == nil {
		if finished := parseDockerTime(state.FinishedAt); !finished.IsZero() {
			d.LogWindow = &LogWindow{
				Since:  finished.Add(-5 * time.Minute),
				Until:  finished.Add(time.Minute),
				Reason: "the five minutes before it exited",
			}
		}
	}
	if len(d.Suggestions) == 0 && d.State != "running" {
		d.Suggestions = append(d.Suggestions, "Read its logs from around the failure rather than the tail — by the time you look, the tail is the last restart's start-up.")
	}
}

// loopCause is the inference the whole feature exists for: what is behind a
// container that keeps coming back.
func loopCause(state *containerState, limit int64) (string, []string) {
	switch {
	case state.OOMKilled || state.ExitCode == 137:
		cause := "Likely cause: it is being killed for memory."
		if limit > 0 {
			cause = "Likely cause: it exceeds its " + humanBytes(limit) + " memory limit shortly after starting, is killed, and the restart policy brings it back to do it again."
		}
		return cause, []string{
			"Its memory history shows whether it climbs to the limit gradually or arrives there immediately — the two have different fixes.",
			"Stop the loop first if the restarts are costing something: a container with a restart policy will keep failing quietly rather than staying down where you would notice.",
		}
	case state.ExitCode == 1:
		return "Likely cause: the program is failing at start-up. Status 1 is the program's own \"something went wrong\", and a loop means it fails the same way every time.",
			[]string{
				"Read the *first* fifty lines after a start, not the last — the failure is at the beginning of each attempt and the tail is the beginning of the next one.",
				"A configuration or connection failure at start-up is the usual cause: a missing environment variable, or a database that is not up yet.",
			}
	case state.ExitCode == 127 || state.ExitCode == 126:
		return "Likely cause: the command this container runs is not in its image, or is not executable.",
			[]string{"Compare the container's command against what the image actually ships — a shell that exists as /bin/sh but not /bin/bash is the usual one."}
	case state.ExitCode != 0:
		return "Likely cause: it fails the same way every time and the restart policy keeps bringing it back. Status " +
				itoa(state.ExitCode) + ": " + exitCodeMeaning(state.ExitCode),
			[]string{"The logs from one failed attempt explain all of them — they are identical each time round the loop."}
	default:
		return "It exits cleanly and is restarted anyway, which usually means its main process finishes rather than staying up.",
			[]string{"A container whose command runs and returns is a job, not a service. Check that the command is the long-running one."}
	}
}

// containerState is the Engine's state block, aliased so the inference above
// can be exercised against a hand-built one with no daemon present.
type containerState = container.State

func exitCodeMeaning(code int) string {
	switch {
	case code == 0:
		return "a clean exit — the program decided its work was done"
	case code == 1:
		return "the program's own general failure"
	case code == 125:
		return "Docker itself could not create the container with the settings it was given"
	case code == 126:
		return "the command was found but could not be executed"
	case code == 127:
		return "the command was not found in the image"
	case code == 137:
		return "killed with SIGKILL — usually the kernel running out of memory, or a stop that timed out and was forced"
	case code == 139:
		return "a segmentation fault — the program crashed in a way it could not handle"
	case code == 143:
		return "stopped cleanly on SIGTERM — something asked it to stop and it did"
	case code > 128 && code < 165:
		return "killed by signal " + itoa(code-128)
	default:
		return "a status from the program inside rather than from Docker"
	}
}
