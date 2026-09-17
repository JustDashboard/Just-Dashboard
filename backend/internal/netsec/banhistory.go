package netsec

import (
	"bufio"
	"context"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// BanEvent is one ban or unban, read from fail2ban's own log.
//
// The jail status only ever lists the bans in force right now, and a ban
// expires — so the attack that filled the log at 04:00 and was banned for ten
// minutes leaves nothing behind by the time anyone looks. Rather than poll the
// jail and diff the set, which would invent events and miss every ban shorter
// than the polling interval, this reads the log fail2ban already writes. It is
// the record; the dashboard was simply not showing it.
type BanEvent struct {
	// Action is "ban" or "unban".
	Action string    `json:"action"`
	Jail   string    `json:"jail"`
	IP     string    `json:"ip"`
	At     time.Time `json:"at"`
}

// Where fail2ban writes by default. A host that logs only to the journal has
// no file here, which is reported as "no readable record" rather than as an
// error — it is a configuration, not a fault.
var fail2banLogs = []string{
	"/var/log/fail2ban.log",
	"/var/log/fail2ban.log.1",
}

// banLine matches the lines fail2ban writes when it acts. The format has been
// stable across releases:
//
//	2026-08-20 03:15:22,123 fail2ban.actions [1234]: NOTICE  [sshd] Ban 203.0.113.9
//
// Only Ban and Unban are taken. "Found" lines are one per failed attempt and
// would bury the actions in noise; failed attempts are btmp's answer, and the
// Security page reads that separately.
var banLine = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})[,.]\d+\s+\S+\s+\[\d+\]:\s+\S+\s+\[([^\]]+)\]\s+(Ban|Unban)\s+(\S+)`)

// BanHistory returns recent ban activity, newest first.
func (s *Service) BanHistory(ctx context.Context, limit int) ([]BanEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	events := []BanEvent{}
	// Oldest rotation first, so that if a log is mid-rotation the ordering
	// still comes out right after the sort below.
	for i := len(fail2banLogs) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			break
		}
		found, err := readBanLog(fail2banLogs[i])
		if err != nil {
			continue
		}
		events = append(events, found...)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

// banLogTailBytes bounds how much of one log file is read. fail2ban writes a
// "Found" line for every failed attempt, so on a host under a sustained
// campaign the log runs to hundreds of megabytes — and the history panel, the
// offenders panel and the posture check each read it every minute or two. The
// tail holds the recent events, which are the ones every reader wants; the
// head is history nobody scrolls back to. A variable so a test can shrink it.
var banLogTailBytes int64 = 8 << 20

func readBanLog(path string) ([]BanEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []BanEvent
	scanner := bufio.NewScanner(f)
	// A single log line is short, but a corrupted file can present a very long
	// one; the default 64K limit would stop the scan there and silently
	// truncate the history.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if st, err := f.Stat(); err == nil && st.Size() > banLogTailBytes {
		if _, err := f.Seek(st.Size()-banLogTailBytes, io.SeekStart); err == nil {
			// The seek lands mid-line. The torn first line is dropped rather
			// than parsed: half a timestamp is not an event.
			scanner.Scan()
		}
	}
	for scanner.Scan() {
		if ev, ok := parseBanLine(scanner.Text()); ok {
			events = append(events, ev)
		}
	}
	return events, scanner.Err()
}

func parseBanLine(line string) (BanEvent, bool) {
	m := banLine.FindStringSubmatch(line)
	if m == nil {
		return BanEvent{}, false
	}
	at, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local)
	if err != nil {
		return BanEvent{}, false
	}
	return BanEvent{
		Action: strings.ToLower(m[3]),
		Jail:   m[2],
		IP:     m[4],
		At:     at,
	}, true
}

// Offender is one address, summarised across the whole ban record.
//
// A ban list answers "who is banned"; the log answers "who keeps coming
// back", which is the more useful question and the one no panel in this class
// asks. An address banned eleven times in a week is not a passing scanner —
// it is somebody working through your host, and it is worth a permanent
// firewall rule rather than another ten-minute ban.
type Offender struct {
	IP    string    `json:"ip"`
	Bans  int       `json:"bans"`
	Jails []string  `json:"jails"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

// BanSummary is the ban record turned into something to read.
type BanSummary struct {
	Total     int        `json:"total"`
	Bans      int        `json:"bans"`
	Unbans    int        `json:"unbans"`
	Offenders []Offender `json:"offenders"`
	// ByJail counts bans per jail, so a jail that is doing all the work — or
	// none of it — is visible without reading the list.
	ByJail map[string]int `json:"byJail"`
	// PerDay is the ban count per calendar day, oldest first, for a sparkline.
	// A histogram of a week says "this started on Tuesday", which the list
	// cannot.
	PerDay []DayCount `json:"perDay"`
	Since  *time.Time `json:"since,omitempty"`
}

type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// SummariseBans folds the ban log. Unbans are counted but do not contribute to
// an offender's tally: every ban is eventually followed by one, so counting
// both would double every number and make an expired ban look like a second
// attack.
func SummariseBans(events []BanEvent, topN int) BanSummary {
	if topN <= 0 {
		topN = 10
	}
	sum := BanSummary{Offenders: []Offender{}, ByJail: map[string]int{}, PerDay: []DayCount{}}
	sum.Total = len(events)
	byIP := map[string]*Offender{}
	byDay := map[string]int{}
	for _, e := range events {
		if e.Action != "ban" {
			sum.Unbans++
			continue
		}
		sum.Bans++
		sum.ByJail[e.Jail]++
		byDay[e.At.Format("2006-01-02")]++
		o, ok := byIP[e.IP]
		if !ok {
			o = &Offender{IP: e.IP, Jails: []string{}, First: e.At, Last: e.At}
			byIP[e.IP] = o
		}
		o.Bans++
		if e.At.Before(o.First) {
			o.First = e.At
		}
		if e.At.After(o.Last) {
			o.Last = e.At
		}
		if !slicesContains(o.Jails, e.Jail) {
			o.Jails = append(o.Jails, e.Jail)
		}
	}
	for _, o := range byIP {
		sort.Strings(o.Jails)
		sum.Offenders = append(sum.Offenders, *o)
	}
	// Most persistent first, then most recent, then by address so the order
	// is stable between polls rather than map-random.
	sort.Slice(sum.Offenders, func(i, j int) bool {
		a, b := sum.Offenders[i], sum.Offenders[j]
		if a.Bans != b.Bans {
			return a.Bans > b.Bans
		}
		if !a.Last.Equal(b.Last) {
			return a.Last.After(b.Last)
		}
		return a.IP < b.IP
	})
	if len(sum.Offenders) > topN {
		sum.Offenders = sum.Offenders[:topN]
	}
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		sum.PerDay = append(sum.PerDay, DayCount{Day: d, Count: byDay[d]})
	}
	for _, e := range events {
		if sum.Since == nil || e.At.Before(*sum.Since) {
			at := e.At
			sum.Since = &at
		}
	}
	return sum
}

func slicesContains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
