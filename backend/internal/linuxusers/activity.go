package linuxusers

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What an account is doing and has done, read from the host's own records. None
// of it is kept by the dashboard.

// authLogPaths are the two names a distribution gives the file.
var authLogPaths = []string{"/var/log/auth.log", "/var/log/secure"}

// authLogTail is how far back the file is read. A busy public host writes
// megabytes a day of refused attempts, and the newest are the ones asked about.
const authLogTail = 8 << 20

// authLines is the newest part of the auth log, oldest line first, from the file
// where one exists and otherwise the journal.
func (s *Service) authLines(ctx context.Context) []string {
	for _, path := range authLogPaths {
		if lines, ok := tailLines(path, authLogTail); ok {
			return lines
		}
	}
	out, err := s.run(ctx, "", "journalctl", "-t", "sshd", "-t", "sshd-session", "-t", "sudo",
		"-o", "short-iso", "-n", "20000", "--no-pager")
	if err != nil {
		return nil
	}
	return strings.Split(out, "\n")
}

func tailLines(path string, limit int64) ([]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	partial := false
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return nil, false
		}
		partial = true
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	// A read that began mid-file began mid-line.
	if partial {
		if at := bytes.IndexByte(b, '\n'); at >= 0 {
			b = b[at+1:]
		}
	}
	return strings.Split(string(b), "\n"), true
}

var syslogStamp = regexp.MustCompile(`^([A-Z][a-z]{2}\s+\d+\s+\d\d:\d\d:\d\d)\s`)

// logLineTime reads either stamp syslog writes: RFC 3339, or the older form that
// has no year and is taken as the most recent time it could mean.
func logLineTime(line string, now time.Time) (time.Time, bool) {
	first, _, _ := strings.Cut(line, " ")
	if ts, err := time.Parse(time.RFC3339Nano, first); err == nil {
		return ts.UTC(), true
	}
	if ts, err := time.Parse("2006-01-02T15:04:05-0700", first); err == nil {
		return ts.UTC(), true
	}
	if m := syslogStamp.FindStringSubmatch(line); m != nil {
		ts, err := time.ParseInLocation("Jan 2 15:04:05", strings.Join(strings.Fields(m[1]), " "), time.Local)
		if err != nil {
			return time.Time{}, false
		}
		ts = ts.AddDate(now.Year(), 0, 0)
		if ts.After(now.Add(24 * time.Hour)) {
			ts = ts.AddDate(-1, 0, 0)
		}
		return ts.UTC(), true
	}
	return time.Time{}, false
}

type keyUse struct {
	at   time.Time
	from string
}

// logLinePrefix is a line's timestamp and host, in either form syslog writes. These
// reads are anchored on it and on the program's own tag because the rest of a line
// can be anybody's words: sshd logs the username a stranger tries, and an unanchored
// match let that username forge a sudo command or a key's last use.
const logLinePrefix = `^(?:\S+|[A-Z][a-z]{2}\s+\d+\s+\S+)\s+\S+\s+`

var acceptedKeyRe = regexp.MustCompile(logLinePrefix + `sshd(?:-session)?(?:\[\d+\])?:\s+Accepted publickey for (\S+) from (\S+) port \d+ ssh2: \S+ (SHA256:\S+)`)

// keyUses is the newest sign-in per fingerprint for one account.
func keyUses(lines []string, username string, now time.Time) map[string]keyUse {
	out := map[string]keyUse{}
	for _, line := range lines {
		m := acceptedKeyRe.FindStringSubmatch(line)
		if m == nil || m[1] != username {
			continue
		}
		at, ok := logLineTime(line, now)
		if !ok {
			continue
		}
		if current, seen := out[m[3]]; !seen || at.After(current.at) {
			out[m[3]] = keyUse{at: at, from: m[2]}
		}
	}
	return out
}

// SudoEvent is one use of sudo, or one refusal.
type SudoEvent struct {
	At      time.Time `json:"at"`
	RunAs   string    `json:"runAs"`
	Command string    `json:"command"`
	Failed  bool      `json:"failed"`
	Reason  string    `json:"reason,omitempty"`
}

var (
	// Between USER and COMMAND sudo may write TSID, ENV, GROUP and the like, and
	// a working directory can have spaces in it.
	sudoRanRe = regexp.MustCompile(logLinePrefix + `sudo(?:\[\d+\])?:\s+(\S+) : (?:([^;]*?) ; )?(?:TTY=\S+ ; )?PWD=.*? ; USER=(\S+) ;(?: [A-Z]+=[^;]* ;)*(?: COMMAND=(.*))?$`)
)

// SudoHistory is an account's sudo commands, newest first. Sudo logs the reason a
// command was refused where the TTY would go.
func (s *Service) SudoHistory(ctx context.Context, username string, limit int) []SudoEvent {
	return parseSudoHistory(s.authLines(ctx), username, limit, s.now())
}

func parseSudoHistory(lines []string, username string, limit int, now time.Time) []SudoEvent {
	out := []SudoEvent{}
	for _, line := range lines {
		m := sudoRanRe.FindStringSubmatch(line)
		if m == nil || m[1] != username {
			continue
		}
		at, ok := logLineTime(line, now)
		if !ok {
			continue
		}
		event := SudoEvent{At: at, RunAs: m[3], Command: strings.TrimSpace(m[4])}
		if reason := strings.TrimSpace(m[2]); reason != "" && !strings.HasPrefix(reason, "TTY=") {
			event.Failed = true
			event.Reason = reason
		}
		out = append(out, event)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ProcessRow is one process an account owns.
type ProcessRow struct {
	PID     int     `json:"pid"`
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	Seconds int64   `json:"seconds"`
	Name    string  `json:"name"`
}

// Processes reads `ps` on the host: the count, and the busiest few.
func (s *Service) Processes(ctx context.Context, username string, top int) (int, []ProcessRow, error) {
	if err := ValidateUsername(username); err != nil {
		return 0, nil, err
	}
	out, err := s.run(ctx, "", "ps", "-u", username, "-o", "pid=,pcpu=,pmem=,etimes=,comm=")
	if err != nil {
		// ps exits 1 for "no processes", which is an answer and not a failure.
		if strings.TrimSpace(out) == "" {
			return 0, []ProcessRow{}, nil
		}
		return 0, nil, err
	}
	rows := parseProcesses(out)
	count := len(rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CPU > rows[j].CPU })
	if top > 0 && len(rows) > top {
		rows = rows[:top]
	}
	return count, rows, nil
}

func parseProcesses(out string) []ProcessRow {
	rows := []ProcessRow{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[1], 64)
		mem, _ := strconv.ParseFloat(fields[2], 64)
		seconds, _ := strconv.ParseInt(fields[3], 10, 64)
		rows = append(rows, ProcessRow{PID: pid, CPU: cpu, Memory: mem, Seconds: seconds,
			Name: strings.Join(fields[4:], " ")})
	}
	return rows
}

// DiskUsage is how much the home directory holds. It can take seconds on a large
// home, so it is asked for on its own rather than with the account.
func (s *Service) DiskUsage(ctx context.Context, username string) (int64, error) {
	u, err := s.Get(ctx, username)
	if err != nil {
		return 0, err
	}
	return s.pathBytes(ctx, u.Home)
}

func (s *Service) pathBytes(ctx context.Context, path string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out, err := s.run(ctx, "", "du", "-sxb", "--", path)
	if err != nil && strings.TrimSpace(out) == "" {
		return 0, err
	}
	// du reports what it could read and exits 1 when something was unreadable;
	// the figure it printed is still the useful one.
	fields := strings.Fields(lastLine(out))
	if len(fields) > 0 {
		if bytes, parseErr := strconv.ParseInt(fields[0], 10, 64); parseErr == nil {
			return bytes, nil
		}
	}
	return 0, fmt.Errorf("du gave no figure for %s", path)
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

// ErrSharedNumber is an account whose number another account also has. A process
// has a number and no name, so everything aimed at this one's processes or files
// would land on the other's too. A second uid 0 is the case that matters: ending its
// sessions would end sshd, dockerd and this dashboard.
var ErrSharedNumber = errors.New("another account has the same number")

// numberIsShared says which other account, if any, this one shares a number with.
func (s *Service) numberIsShared(u User) string {
	users, err := parsePasswd(s.path("passwd"))
	if err != nil {
		return ""
	}
	for _, other := range users {
		if other.Username != u.Username && other.UID == u.UID {
			return other.Username
		}
	}
	return ""
}

// EndSessions stops every process the account owns. loginctl goes first so the
// sessions are recorded as ended; whatever is left, a detached job included, is
// what pkill is for. Root is refused: that is the machine.
func (s *Service) EndSessions(ctx context.Context, username string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if protected[username] {
		return fmt.Errorf("%w: %s", ErrProtected, username)
	}
	u, err := s.lookup(username)
	if err != nil {
		return err
	}
	if u.UID == 0 {
		return fmt.Errorf("%w: %s is uid 0, so its processes are every root process on this server", ErrSharedNumber, username)
	}
	if other := s.numberIsShared(*u); other != "" {
		return fmt.Errorf("%w: ending %s's processes would end %s's", ErrSharedNumber, username, other)
	}
	_, _ = s.run(ctx, "", "loginctl", "terminate-user", username)
	// pkill exits 1 when nothing matched, and nothing left is the goal.
	if out, err := s.run(ctx, "", "pkill", "--signal", "KILL", "--uid", strconv.Itoa(u.UID)); err != nil &&
		strings.TrimSpace(out) != "" {
		return err
	}
	return nil
}
