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
	// What the timer is for and what it fires on, read with one
	// `systemctl show` across every timer, so the Scheduled page can say
	// "every day at 06:00 and 18:00, up to 12h later" in its list rather
	// than only when one timer is opened. A host whose systemctl will not
	// answer leaves them empty, and the list is still the list.
	Description string   `json:"description,omitempty"`
	Calendar    []string `json:"calendar,omitempty"`
	Monotonic   []string `json:"monotonic,omitempty"`
	RandomDelay string   `json:"randomDelay,omitempty"`
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
	schedules := map[string]timerSchedule{}
	if len(timers) > 0 {
		args := []string{"show", "--no-pager",
			"-p", "Id", "-p", "Description", "-p", "TimersCalendar",
			"-p", "TimersMonotonic", "-p", "RandomizedDelayUSec", "--"}
		for _, t := range timers {
			args = append(args, t.Unit)
		}
		if shown, err := run(ctx, 30*time.Second, "systemctl", args...); err == nil {
			schedules = parseTimerShow(shown.Stdout)
		}
	}
	for i := range timers {
		if sc, ok := schedules[timers[i].Unit]; ok {
			timers[i].Description = sc.Description
			timers[i].Calendar = sc.Calendar
			timers[i].Monotonic = sc.Monotonic
			timers[i].RandomDelay = sc.RandomDelay
		}
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

type timerSchedule struct {
	Description string
	Calendar    []string
	Monotonic   []string
	RandomDelay string
}

// parseTimerShow reads `systemctl show -p Id -p Description -p
// TimersCalendar -p TimersMonotonic -p RandomizedDelayUSec` over several
// timers: one block per unit, separated by a blank line, with `Id` wherever
// systemd puts it in the block. A calendar or an interval is one line per
// trigger, each `{ OnCalendar=*-*-* 06,18:00:00 ; next_elapse=… }`; only the
// trigger is kept, since the list already carries the next elapse. Recent
// systemd names the intervals `OnBootUSec` and `OnUnitActiveUSec`, older
// releases `OnBootSec`; both are given back as the unit file spells them.
func parseTimerShow(out string) map[string]timerSchedule {
	result := map[string]timerSchedule{}
	for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
		var id string
		var sc timerSchedule
		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			switch key {
			case "Id":
				id = value
			case "Description":
				sc.Description = value
			case "RandomizedDelayUSec":
				if value != "" && value != "0" && value != "infinity" {
					sc.RandomDelay = value
				}
			case "TimersCalendar":
				if spec := timerTrigger(value); spec != "" {
					if name, rest, ok := strings.Cut(spec, "="); ok && name == "OnCalendar" {
						sc.Calendar = append(sc.Calendar, rest)
					}
				}
			case "TimersMonotonic":
				if spec := timerTrigger(value); spec != "" {
					name, rest, _ := strings.Cut(spec, "=")
					if strings.HasSuffix(name, "USec") {
						name = strings.TrimSuffix(name, "USec") + "Sec"
					}
					sc.Monotonic = append(sc.Monotonic, name+"="+rest)
				}
			}
		}
		if id != "" {
			result[id] = sc
		}
	}
	return result
}

// timerTrigger takes `{ OnCalendar=Mon *-*-* 00:00:00 ; next_elapse=… }` to
// `OnCalendar=Mon *-*-* 00:00:00`.
func timerTrigger(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "{")
	value = strings.TrimSuffix(value, "}")
	first, _, _ := strings.Cut(value, ";")
	first = strings.TrimSpace(first)
	if !strings.Contains(first, "=") {
		return ""
	}
	return first
}
