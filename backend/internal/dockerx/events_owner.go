package dockerx

import (
	"fmt"
	"strconv"
	"strings"
)

// Which deployment an event belongs to, from the event alone.
//
// A container says so itself: Docker puts an object's labels in the event's
// actor attributes, so the dashboard's own namespace rides along on every
// container event and the answer is a map lookup. A network does not — the
// daemon sends `name` and `type` for one and nothing else, however it was
// created — so the one thing left is the name, and that this dashboard chose:
// `jd-e7-db-…` for a database network and `jd-preview-e7…` for a preview's
// both carry the environment in them. The trailing separator is what keeps
// environment 7 from claiming environment 70's.

// EnvironmentID is the deployment environment an event happened to something
// of, and whether it happened to one at all.
func (ev Event) EnvironmentID() (int64, bool) {
	if raw := ev.Owner["environment-id"]; raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		return id, err == nil && id > 0
	}
	if ev.Type != "network" {
		return 0, false
	}
	return networkEnvironment(ev.Name)
}

// OwnedBy reports whether an event happened to something this environment owns.
func (ev Event) OwnedBy(environmentID int64) bool {
	id, ok := ev.EnvironmentID()
	return ok && id == environmentID
}

// networkEnvironment reads the environment out of a network the dashboard
// named: `jd-e<id>-…` or `jd-preview-e<id>`, alone or followed by `-…`.
func networkEnvironment(name string) (int64, bool) {
	var rest string
	switch {
	case strings.HasPrefix(name, "jd-preview-e"):
		rest = strings.TrimPrefix(name, "jd-preview-e")
	case strings.HasPrefix(name, "jd-e"):
		rest = strings.TrimPrefix(name, "jd-e")
	default:
		return 0, false
	}
	digits := rest
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		digits = rest[:i]
	} else if strings.HasPrefix(name, "jd-e") {
		// A database network always has a suffix; a bare `jd-e7` is not
		// one of ours.
		return 0, false
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	// The round trip is the separator test: `jd-e70-…` must not read as 7.
	preview := fmt.Sprintf("jd-preview-e%d", id)
	if name == preview || strings.HasPrefix(name, preview+"-") || strings.HasPrefix(name, fmt.Sprintf("jd-e%d-", id)) {
		return id, true
	}
	return 0, false
}

// ownerInt reads one of the dashboard's numeric labels off an event.
func (ev Event) ownerInt(key string) int64 {
	n, _ := strconv.ParseInt(ev.Owner[key], 10, 64)
	return n
}
