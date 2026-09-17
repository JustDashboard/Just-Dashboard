package netsec

import (
	"bufio"
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// LoginRecord is one entry from the host's own login accounting.
//
// The Security page could previously only answer "who is signed in right
// now", which is the one moment an operator is not worried about. The
// question that matters on a machine exposed to the internet — who got in
// overnight, and how many people tried — is answered by wtmp and btmp, which
// the kernel and sshd have been writing all along. Nothing here is recorded by
// the dashboard; it is the host's record, finally shown.
type LoginRecord struct {
	// Kind is "login" for a session, "boot" for a restart, "shutdown" for a
	// clean stop. Reboots come back from the same file and are worth keeping:
	// "the box rebooted at 04:12" explains a great deal about the rows around
	// it.
	Kind      string     `json:"kind"`
	User      string     `json:"user"`
	TTY       string     `json:"tty"`
	From      string     `json:"from"`
	LoginTime *time.Time `json:"loginTime,omitempty"`
	EndTime   *time.Time `json:"endTime,omitempty"`
	// Ended is how the session finished when there is no end time: "down" for
	// a shutdown, "crash" for a host that never got to write one.
	Ended    string `json:"ended,omitempty"`
	Duration string `json:"duration,omitempty"`
	Active   bool   `json:"active"`
}

// LoginHistory reads successful logins from wtmp, newest first.
//
// The command runs on the host rather than in the container: the image has its
// own empty /var/log/wtmp, and reading that would report, with total
// confidence, that nobody has ever logged in.
func (s *Service) LoginHistory(ctx context.Context, limit int) ([]LoginRecord, error) {
	return runLast(ctx, "last", limit)
}

// FailedLogins reads btmp — the failed-attempt log. On an internet-facing host
// this is usually the longest file on the machine, which is itself the point:
// the size of the number is the finding.
func (s *Service) FailedLogins(ctx context.Context, limit int) ([]LoginRecord, error) {
	return runLast(ctx, "lastb", limit)
}

// failedLoginSample is how far back the posture check reads. Larger than the
// page's own limit on purpose: the verdict's thresholds are counts, and a
// sample smaller than the highest of them makes that threshold unreachable
// however busy the host is.
const failedLoginSample = 5000

// FailedLoginVolume counts recent failed attempts.
//
// The count needs a window and a sample big enough to fill it, and the posture
// check had neither: it took the length of a 500-record listing of the whole
// file. So the "sustained attempts" threshold of 2000 could never be reached
// by a number that stopped at 500, and the notice threshold of 200 fired on
// any host whose btmp had ever accumulated that many — which is every host
// with a public SSH port, permanently, regardless of what happened this week.
type FailedLoginVolume struct {
	Count  int           `json:"count"`
	Window time.Duration `json:"window"`
	// Capped reports that the sample ran out before the window did, so the
	// count is a floor rather than a total.
	Capped bool `json:"capped"`
}

func (s *Service) FailedLoginVolume(ctx context.Context, window time.Duration) (FailedLoginVolume, error) {
	records, err := s.FailedLogins(ctx, failedLoginSample)
	if err != nil {
		return FailedLoginVolume{}, err
	}
	return countWithin(records, window, time.Now()), nil
}

// countWithin is the counting itself, separated from the subprocess so the
// window arithmetic can be tested without a btmp.
func countWithin(records []LoginRecord, window time.Duration, now time.Time) FailedLoginVolume {
	vol := FailedLoginVolume{Window: window, Capped: len(records) >= failedLoginSample}
	cutoff := now.Add(-window)
	for _, r := range records {
		// A record with no timestamp is one the parser could not read, not one
		// from outside the window; counting it would inflate the figure the
		// verdict is built on.
		if r.LoginTime == nil || r.LoginTime.Before(cutoff) {
			continue
		}
		vol.Count++
	}
	// The listing is newest-first, so running out of sample inside the window
	// is the only way the cap matters. Past the window the cap says nothing.
	if vol.Capped && vol.Count < len(records) {
		vol.Capped = false
	}
	return vol
}

func runLast(ctx context.Context, tool string, limit int) ([]LoginRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	// -F prints whole timestamps including the year, without which a record
	// from December is indistinguishable from one this week. -w stops long
	// usernames and hostnames being truncated into something that no longer
	// identifies anyone. Both are util-linux options; a host whose `last` is
	// busybox refuses them, so the plain form is tried second and parses just
	// as well — it simply carries no year.
	out, err := hostexec.CommandOnHost(ctx, tool, "-F", "-w", "-n", strconv.Itoa(limit)).Output()
	if err != nil {
		out, err = hostexec.CommandOnHost(ctx, tool, "-n", strconv.Itoa(limit)).Output()
		if err != nil {
			return nil, err
		}
	}
	return parseLast(string(out), limit), nil
}

var weekdays = map[string]bool{
	"Mon": true, "Tue": true, "Wed": true, "Thu": true,
	"Fri": true, "Sat": true, "Sun": true,
}

// parseLast reads `last`/`lastb` output.
//
// The columns are padded rather than delimited, and the "from" column is empty
// for a console login, so splitting on width or on a fixed field count both
// break on real hosts. The timestamp is the reliable landmark: it always
// starts with a weekday abbreviation, and everything before it is the user,
// the terminal and wherever they came from. A line that does not contain such
// a landmark is a header or a footer, and is skipped rather than guessed at.
func parseLast(out string, limit int) []LoginRecord {
	records := make([]LoginRecord, 0, limit)
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "wtmp begins") || strings.HasPrefix(line, "btmp begins") {
			continue
		}
		rec, ok := parseLastLine(line)
		if !ok {
			continue
		}
		records = append(records, rec)
		if len(records) >= limit {
			break
		}
	}
	return records
}

func parseLastLine(line string) (LoginRecord, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return LoginRecord{}, false
	}
	start := -1
	for i := 2; i < len(fields); i++ {
		if weekdays[fields[i]] {
			start = i
			break
		}
	}
	if start < 0 {
		return LoginRecord{}, false
	}

	rec := LoginRecord{Kind: "login", User: fields[0]}
	// A restart is not a session and does not have a terminal: its second
	// column is the literal words "system boot" (or "system down"), and the
	// column a session would use for its origin carries the kernel that came
	// up. Reading those two words as a tty leaves the kernel version in a
	// field labelled "from a terminal".
	if start > 3 && fields[1] == "system" && (fields[2] == "boot" || fields[2] == "down") {
		if fields[2] == "boot" {
			rec.Kind = "boot"
		} else {
			rec.Kind = "shutdown"
		}
		rec.From = strings.Join(fields[3:start], " ")
	} else {
		rec.TTY = fields[1]
		rec.From = strings.Join(fields[2:start], " ")
	}

	rest := fields[start:]
	if at, used := parseLastTime(rest); used > 0 {
		rec.LoginTime = at
		rest = rest[used:]
	} else {
		return LoginRecord{}, false
	}

	rec.Duration = trimParens(rest)
	rest = dropParens(rest)

	if len(rest) > 0 && rest[0] == "still" {
		rec.Active = true
		return rec, true
	}
	if len(rest) > 1 && rest[0] == "-" {
		tail := rest[1:]
		switch tail[0] {
		case "down", "crash":
			rec.Ended = tail[0]
		default:
			if at, used := parseLastTime(tail); used > 0 {
				rec.EndTime = at
			}
		}
	}
	return rec, true
}

// parseLastTime reads the timestamp at the head of fields, in either the long
// form -F produces or the short form plain `last` does, and reports how many
// fields it consumed.
func parseLastTime(fields []string) (*time.Time, int) {
	// "Wed Aug 20 03:15:22 2026"
	if len(fields) >= 5 {
		if at, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(fields[:5], " "), time.Local); err == nil {
			return &at, 5
		}
	}
	// "Wed Aug 20 03:15" — no year, so it is read against the current one.
	// December records read in January will be a year out; that is a property
	// of the output, not of the parsing, and -F is preferred for exactly this
	// reason.
	if len(fields) >= 4 {
		if at, err := time.ParseInLocation("Mon Jan 2 15:04", strings.Join(fields[:4], " "), time.Local); err == nil {
			// Rebuilt rather than year-shifted: the parsed value lands in
			// year zero, and adding years to a February date there walks into
			// a leap-day that the current year may not have.
			dated := time.Date(time.Now().Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, time.Local)
			return &dated, 4
		}
	}
	return nil, 0
}

func trimParens(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	last := fields[len(fields)-1]
	if strings.HasPrefix(last, "(") && strings.HasSuffix(last, ")") {
		return strings.Trim(last, "()")
	}
	return ""
}

func dropParens(fields []string) []string {
	if trimParens(fields) != "" {
		return fields[:len(fields)-1]
	}
	return fields
}

// Attacker is one address folded across the failed-login record.
//
// The listing answers "what was tried"; this answers "who is trying". Five
// hundred lines of the same address are one attacker, and the address on every
// page of the listing is the one worth a firewall rule rather than another
// ten-minute ban.
type Attacker struct {
	Address  string `json:"address"`
	Attempts int    `json:"attempts"`
	// Users are the account names tried from this address, most tried first.
	// "root, admin, ubuntu" is a scanner working through a wordlist; a single
	// real account name is somebody who knows the host.
	Users []string  `json:"users"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

// AttackSummary is btmp turned into something to act on.
type AttackSummary struct {
	// Attempts counts every failed attempt inside the window, including the
	// ones with no address to fold under — a console prompt records none.
	Attempts  int `json:"attempts"`
	Addresses int `json:"addresses"`
	// WindowHours is how far back the count reaches.
	WindowHours int `json:"windowHours"`
	// Capped says the sample ran out before the window did, so every figure
	// here is a floor rather than a total.
	Capped    bool       `json:"capped"`
	Attackers []Attacker `json:"attackers"`
	// Since is the oldest record inside the window, so "attempts in the last
	// week" can say how much of the week it actually saw.
	Since *time.Time `json:"since,omitempty"`
}

// FailedLoginSummary folds the recent failed attempts by address.
func (s *Service) FailedLoginSummary(ctx context.Context, window time.Duration, topN int) (*AttackSummary, error) {
	records, err := s.FailedLogins(ctx, failedLoginSample)
	if err != nil {
		return nil, err
	}
	return SummariseFailedLogins(records, window, time.Now(), topN), nil
}

// attackerUserNames is how many of the tried account names an attacker row
// carries. A scanner tries hundreds; the first few say what kind of scanner.
const attackerUserNames = 5

// SummariseFailedLogins is the folding itself, separated from the subprocess
// so the arithmetic can be tested without a btmp.
func SummariseFailedLogins(records []LoginRecord, window time.Duration, now time.Time, topN int) *AttackSummary {
	if topN <= 0 {
		topN = 25
	}
	sum := &AttackSummary{Attackers: []Attacker{}, WindowHours: int(window.Hours())}
	cutoff := now.Add(-window)
	byAddr := map[string]*Attacker{}
	users := map[string]map[string]int{}
	inWindow := 0
	for _, r := range records {
		if r.LoginTime == nil || r.LoginTime.Before(cutoff) {
			continue
		}
		inWindow++
		sum.Attempts++
		if sum.Since == nil || r.LoginTime.Before(*sum.Since) {
			at := *r.LoginTime
			sum.Since = &at
		}
		if r.From == "" {
			continue
		}
		a, ok := byAddr[r.From]
		if !ok {
			a = &Attacker{Address: r.From, Users: []string{}, First: *r.LoginTime, Last: *r.LoginTime}
			byAddr[r.From] = a
			users[r.From] = map[string]int{}
		}
		a.Attempts++
		if r.LoginTime.Before(a.First) {
			a.First = *r.LoginTime
		}
		if r.LoginTime.After(a.Last) {
			a.Last = *r.LoginTime
		}
		if r.User != "" {
			users[r.From][r.User]++
		}
	}
	// The same cap arithmetic as countWithin: a sample that ran out while
	// still inside the window is a floor, one that reached past it is not.
	sum.Capped = len(records) >= failedLoginSample && inWindow == len(records)
	for addr, a := range byAddr {
		names := make([]string, 0, len(users[addr]))
		for name := range users[addr] {
			names = append(names, name)
		}
		counts := users[addr]
		sort.Slice(names, func(i, j int) bool {
			if counts[names[i]] != counts[names[j]] {
				return counts[names[i]] > counts[names[j]]
			}
			return names[i] < names[j]
		})
		if len(names) > attackerUserNames {
			names = names[:attackerUserNames]
		}
		a.Users = names
		sum.Attackers = append(sum.Attackers, *a)
	}
	sum.Addresses = len(sum.Attackers)
	// Most persistent first, then most recent, then by address so the order
	// is stable between polls rather than map-random.
	sort.Slice(sum.Attackers, func(i, j int) bool {
		a, b := sum.Attackers[i], sum.Attackers[j]
		if a.Attempts != b.Attempts {
			return a.Attempts > b.Attempts
		}
		if !a.Last.Equal(b.Last) {
			return a.Last.After(b.Last)
		}
		return a.Address < b.Address
	})
	if len(sum.Attackers) > topN {
		sum.Attackers = sum.Attackers[:topN]
	}
	return sum
}
