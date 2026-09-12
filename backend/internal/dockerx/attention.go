package dockerx

import "sort"

// Two questions that were being answered by one word.
//
// The overview used to show "Health: all good" over a containers page listing
// a privileged container with the Docker socket mounted and thirty gigabytes
// in a writable layer. Both statements were true of the thing they described
// and the page still contradicted itself, because `Diagnosis.Status` was the
// worst level of a list that mixes "this container is not running" with "this
// container is more exposed than it needs to be", and the tile above it was
// labelled Health.
//
// They are separated here rather than in the UI, because the UI is four
// components and the definition should be one:
//
//   - Runtime health is what Docker itself reports: running, exited, paused,
//     restarting, healthy, unhealthy. It is a fact about right now, it is what
//     "is anything down" means, and it goes back to green on its own.
//
//   - Attention is everything the dashboard has an opinion about: security
//     posture, disk, configuration, exposure, maintainability. None of it stops
//     the service, all of it costs something later, and it does not clear
//     itself.
//
// A container can be perfectly healthy and need attention. Saying so is the
// whole point.

// Severity ranks what a finding costs. Four levels rather than three: the
// difference between "this is broken" and "this could be tidier" is the
// difference between a panel worth reading and warning fatigue.
type Severity string

const (
	// SeverityCritical is already broken, or one step from it: an exposed
	// Docker socket, a restart loop, an unhealthy service.
	SeverityCritical Severity = "critical"
	// SeverityWarning is costing something now and will cost more: a writable
	// layer filling the disk, a failing health check, a public bind.
	SeverityWarning Severity = "warning"
	// SeverityRecommendation is a choice that will hurt later: a moving tag,
	// no health check, no memory limit, no restart policy.
	SeverityRecommendation Severity = "recommendation"
	// SeverityInfo is an operational fact worth stating and never worth
	// colouring red.
	SeverityInfo Severity = "info"
)

// Class is what kind of problem a finding is, which is what decides whether it
// belongs to runtime health or to attention.
type Class string

const (
	ClassRuntime       Class = "runtime"
	ClassSecurity      Class = "security"
	ClassStorage       Class = "storage"
	ClassConfiguration Class = "configuration"
	ClassExposure      Class = "exposure"
	ClassLifecycle     Class = "lifecycle"
)

// RuntimeOnly reports whether a class describes Docker's own state rather than
// the dashboard's opinion of it.
func (c Class) RuntimeOnly() bool { return c == ClassRuntime }

// legacyLevel maps the new severity onto the three words the API shipped with,
// so a browser holding an older bundle keeps working while the new fields are
// there for the one that replaces it.
func (s Severity) legacyLevel() string {
	switch s {
	case SeverityCritical:
		return "critical"
	case SeverityWarning:
		return "warning"
	default:
		return "notice"
	}
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	case SeverityRecommendation:
		return 2
	default:
		return 3
	}
}

// RuntimeHealth is what Docker says about what is running, counted.
//
// Every field is a count of containers, so the summary line — "12 running, 8
// healthy, 4 with no health check" — is arithmetic rather than prose, and
// "healthy" never silently includes the containers that have no check to fail.
type RuntimeHealth struct {
	Total      int `json:"total"`
	Running    int `json:"running"`
	Exited     int `json:"exited"`
	Created    int `json:"created"`
	Restarting int `json:"restarting"`
	Paused     int `json:"paused"`
	Dead       int `json:"dead"`
	Removing   int `json:"removing"`

	// Of the running containers: how many pass a health check, how many fail
	// one, how many are still inside their start period, and how many define
	// none at all. The last is the number that makes "all healthy" honest.
	Healthy     int `json:"healthy"`
	Unhealthy   int `json:"unhealthy"`
	Starting    int `json:"starting"`
	NoHealthchk int `json:"noHealthcheck"`

	// Status is the worst runtime state present: "ok", "degraded" (something
	// is down that looks like it should not be) or "critical" (something is
	// unhealthy, dead, or looping).
	Status string `json:"status"`
	// Summary is the sentence the overview tile shows.
	Summary string `json:"summary"`
}

// AttentionSummary counts what needs looking at, by severity.
type AttentionSummary struct {
	Critical        int `json:"critical"`
	Warning         int `json:"warning"`
	Recommendations int `json:"recommendations"`
	Info            int `json:"info"`
	// Issues is critical + warning: the things that are wrong now, as opposed
	// to the things that could be better. It is what a badge counts.
	Issues int `json:"issues"`
	Total  int `json:"total"`
}

// Any reports whether there is anything at all to say.
func (a AttentionSummary) Any() bool { return a.Total > 0 }

// summarizeRuntime counts container states. Taken from the listing rather than
// from the findings, because a count of problems cannot say how many things
// are fine.
func summarizeRuntime(list []Container, healthByID map[string]healthFacts) RuntimeHealth {
	rh := RuntimeHealth{Total: len(list)}
	for _, ct := range list {
		switch ct.State {
		case "running":
			rh.Running++
		case "exited":
			rh.Exited++
		case "created":
			rh.Created++
		case "restarting":
			rh.Restarting++
		case "paused":
			rh.Paused++
		case "dead":
			rh.Dead++
		case "removing":
			rh.Removing++
		}
		if ct.State != "running" {
			continue
		}
		switch facts := healthByID[ct.ID]; {
		case !facts.hasCheck:
			rh.NoHealthchk++
		case facts.status == "healthy":
			rh.Healthy++
		case facts.status == "unhealthy":
			rh.Unhealthy++
		case facts.status == "starting":
			rh.Starting++
		default:
			rh.NoHealthchk++
		}
	}
	rh.Status, rh.Summary = describeRuntime(rh)
	return rh
}

// healthFacts is what a container's health check reports, with "has one at
// all" kept separate from "what it says". Docker leaves the status empty for
// both a container with no check and a check that has not run, and treating
// those as the same thing is how "all healthy" comes to mean "nothing is
// being checked".
type healthFacts struct {
	hasCheck bool
	status   string
}

func describeRuntime(rh RuntimeHealth) (status, summary string) {
	switch {
	case rh.Total == 0:
		return "ok", "No containers on this server."
	case rh.Unhealthy > 0 || rh.Dead > 0:
		status = "critical"
	case rh.Restarting > 0 || rh.Paused > 0:
		status = "warning"
	case rh.Exited > 0:
		// Something stopped is not automatically a problem — a one-shot job
		// exits — so this is the level that says "look", not "act".
		status = "notice"
	default:
		status = "ok"
	}

	parts := []string{plural(rh.Running, "running", "running")}
	if rh.Unhealthy > 0 {
		parts = append(parts, itoa(rh.Unhealthy)+" unhealthy")
	}
	if rh.Restarting > 0 {
		parts = append(parts, itoa(rh.Restarting)+" restarting")
	}
	if rh.Paused > 0 {
		parts = append(parts, itoa(rh.Paused)+" paused")
	}
	if rh.Exited > 0 {
		parts = append(parts, itoa(rh.Exited)+" stopped")
	}
	if rh.NoHealthchk > 0 {
		parts = append(parts, itoa(rh.NoHealthchk)+" without a health check")
	}
	return status, join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// sortBySeverity orders findings worst-first, then by the object they concern
// so the same list comes back in the same order between polls.
func sortBySeverity(f []DockerFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		if severityRank(f[i].Severity) != severityRank(f[j].Severity) {
			return severityRank(f[i].Severity) < severityRank(f[j].Severity)
		}
		if f[i].Target != f[j].Target {
			return f[i].Target < f[j].Target
		}
		return f[i].ID < f[j].ID
	})
}

// summarizeAttention counts the findings that are not runtime facts.
func summarizeAttention(findings []DockerFinding) AttentionSummary {
	var a AttentionSummary
	for _, f := range findings {
		if f.Class.RuntimeOnly() {
			continue
		}
		a.Total++
		switch f.Severity {
		case SeverityCritical:
			a.Critical++
			a.Issues++
		case SeverityWarning:
			a.Warning++
			a.Issues++
		case SeverityRecommendation:
			a.Recommendations++
		default:
			a.Info++
		}
	}
	return a
}
