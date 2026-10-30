package accountlink

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

// Automatic updates are a policy this dashboard runs itself, from its own
// update checker: the site stores the choice and shows it, but a site outage
// never blocks an update and never triggers one.
const (
	AutoOff   = "off"
	AutoPatch = "patch"
	AutoAll   = "all"
)

// Window is the part of the day an unattended update may start, as HH:MM in the
// server's local time. An end earlier than the start runs past midnight.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// UpdatePolicy is what dashboard.update.policy sets.
type UpdatePolicy struct {
	Auto   string `json:"auto"`
	Window Window `json:"window"`
}

func minutesOfDay(hhmm string) (int, bool) {
	if !clockPattern.MatchString(hhmm) {
		return 0, false
	}
	h, _ := strconv.Atoi(hhmm[:2])
	m, _ := strconv.Atoi(hhmm[3:])
	return h*60 + m, true
}

// validate refuses a policy the dashboard will not hold. An empty window is
// refused rather than read as "all day", because the safe reading of an
// ambiguous window is that nothing may start.
func (p UpdatePolicy) validate() error {
	switch p.Auto {
	case AutoOff, AutoPatch, AutoAll:
	default:
		return fmt.Errorf("auto must be %q, %q or %q", AutoOff, AutoPatch, AutoAll)
	}
	start, okStart := minutesOfDay(p.Window.Start)
	end, okEnd := minutesOfDay(p.Window.End)
	if !okStart || !okEnd {
		return errors.New("window times must be HH:MM on a 24-hour clock")
	}
	if p.Auto != AutoOff && start == end {
		return errors.New("window start and end must differ")
	}
	return nil
}

// contains reports whether the local time of day falls inside the window.
func (w Window) contains(local time.Time) bool {
	start, okStart := minutesOfDay(w.Start)
	end, okEnd := minutesOfDay(w.End)
	if !okStart || !okEnd || start == end {
		return false
	}
	now := local.Hour()*60 + local.Minute()
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end
}

// versionParts reads the numeric components of a version in this product's
// scheme, which has two or three of them and sometimes four.
func versionParts(v string) ([]int, bool) {
	if !selfupdate.ValidVersion(v) {
		return nil, false
	}
	fields := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// isPatchUpdate is true when latest is newer than current and differs from it
// only after the second component: 0.8.2 to 0.8.3, or 0.8 to 0.8.1, but not
// 0.8.2 to 0.9.
func isPatchUpdate(current, latest string) bool {
	cur, okCur := versionParts(current)
	lat, okLat := versionParts(latest)
	if !okCur || !okLat || selfupdate.Compare(latest, current) <= 0 {
		return false
	}
	at := func(v []int, i int) int {
		if i < len(v) {
			return v[i]
		}
		return 0
	}
	return at(cur, 0) == at(lat, 0) && at(cur, 1) == at(lat, 1)
}

// UpdateStatus is what the policy needs to know about this install's updates.
type UpdateStatus struct {
	Version   string
	Latest    string
	Available bool
	// Breaking is a newer release whose notes say it needs an operator.
	Breaking bool
	// Supported is whether this install can update itself at all.
	Supported bool
	// Busy is an update or restart already under way.
	Busy bool
}

// updateDue decides whether the policy installs an update now, and which
// version. It never decides to install a release marked breaking: those are
// the ones a person has to read first, and "all" means every ordinary release.
func updateDue(p UpdatePolicy, st UpdateStatus, local time.Time) (string, bool) {
	if p.Auto != AutoPatch && p.Auto != AutoAll {
		return "", false
	}
	if !st.Available || !st.Supported || st.Busy || st.Breaking || st.Latest == "" {
		return "", false
	}
	if p.Auto == AutoPatch && !isPatchUpdate(st.Version, st.Latest) {
		return "", false
	}
	if !p.Window.contains(local) {
		return "", false
	}
	return st.Latest, true
}
