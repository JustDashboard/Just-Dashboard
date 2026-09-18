package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Timer is one systemd timer: the schedule side of a unit pair. It sits
// beside cron on the Scheduled page because on a modern host it is where
// half of the scheduled work actually lives — apt, logrotate, certbot and
// fstrim all run from timers — and an operator asking "what runs at night"
// was being shown only the half in crontabs.
type Timer struct {
	Unit        string     `json:"unit"`
	Activates   string     `json:"activates"`
	ActiveState string     `json:"activeState"`
	SubState    string     `json:"subState"`
	UnitFile    string     `json:"unitFileState"`
	Enabled     bool       `json:"enabled"`
	Next        *time.Time `json:"next,omitempty"`
	Last        *time.Time `json:"last,omitempty"`
}

// Timers lists every timer, active or not, with when it fires next and when
// it last did. `list-timers` has the schedule and no state; `list-units` has
// the state and no schedule; the two are joined on the unit name.
func (s *Systemd) Timers(ctx context.Context) ([]Timer, error) {
	if !s.Available() {
		return nil, fmt.Errorf("systemctl %w", ErrNotInstalled)
	}
	res, err := run(ctx, 30*time.Second, "systemctl",
		"list-timers", "--all", "--no-pager", "--no-legend", "--output=json")
	if err != nil {
		return nil, err
	}
	timers, err := parseTimerList([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	states := map[string][2]string{}
	if units, err := run(ctx, 30*time.Second, "systemctl",
		"list-units", "--type=timer", "--all", "--no-pager", "--no-legend", "--output=json"); err == nil {
		var raw []struct {
			Unit   string `json:"unit"`
			Active string `json:"active"`
			Sub    string `json:"sub"`
		}
		if json.Unmarshal([]byte(units.Stdout), &raw) == nil {
			for _, r := range raw {
				states[r.Unit] = [2]string{r.Active, r.Sub}
			}
		}
	}
	files := map[string]string{}
	if list, err := run(ctx, 30*time.Second, "systemctl",
		"list-unit-files", "--type=timer", "--no-pager", "--no-legend", "--output=json"); err == nil {
		var raw []struct {
			UnitFile string `json:"unit_file"`
			State    string `json:"state"`
		}
		if json.Unmarshal([]byte(list.Stdout), &raw) == nil {
			for _, r := range raw {
				files[r.UnitFile] = r.State
			}
		}
	}
	for i := range timers {
		if st, ok := states[timers[i].Unit]; ok {
			timers[i].ActiveState, timers[i].SubState = st[0], st[1]
		}
		if file, ok := files[timers[i].Unit]; ok {
			timers[i].UnitFile = file
			timers[i].Enabled = file == "enabled" || file == "enabled-runtime" || file == "static"
		}
	}
	return timers, nil
}

// parseTimerList reads `systemctl list-timers --output=json`. `next` and
// `last` are microsecond epoch timestamps, or null for a timer that has not
// fired or will not. `left` and `passed` are ignored: across systemd
// versions they have been a string, a duration in microseconds and — on
// some 257 builds — a copy of `next`, and the client can subtract two
// timestamps itself.
func parseTimerList(data []byte) ([]Timer, error) {
	var raw []struct {
		Unit      string `json:"unit"`
		Activates string `json:"activates"`
		Next      any    `json:"next"`
		Last      any    `json:"last"`
	}
	if strings.TrimSpace(string(data)) == "" {
		return []Timer{}, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse systemctl list-timers output: %w", err)
	}
	out := make([]Timer, 0, len(raw))
	for _, r := range raw {
		if r.Unit == "" {
			continue
		}
		t := Timer{Unit: r.Unit, Activates: r.Activates}
		t.Next = journalMicros(r.Next)
		t.Last = journalMicros(r.Last)
		out = append(out, t)
	}
	// Soonest first; timers with nothing scheduled sink to the end.
	sort.SliceStable(out, func(i, j int) bool {
		switch {
		case out[i].Next == nil && out[j].Next == nil:
			return out[i].Unit < out[j].Unit
		case out[i].Next == nil:
			return false
		case out[j].Next == nil:
			return true
		}
		return out[i].Next.Before(*out[j].Next)
	})
	return out, nil
}

func journalMicros(v any) *time.Time {
	f, ok := v.(float64)
	if !ok || f <= 0 {
		return nil
	}
	t := time.UnixMicro(int64(f)).UTC()
	return &t
}
