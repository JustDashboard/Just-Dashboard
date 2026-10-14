package procs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// CronJob is one schedule line. Comment and environment lines are preserved
// separately so the editor can round-trip a crontab without destroying it.
type CronJob struct {
	Line     int    `json:"line"`
	Schedule string `json:"schedule"`
	// User is set only for /etc/crontab and /etc/cron.d entries, where a
	// username sits between the schedule and the command. A personal crontab
	// has no such field and leaves this empty.
	User     string `json:"user,omitempty"`
	Command  string `json:"command"`
	Comment  string `json:"comment,omitempty"`
	Raw      string `json:"raw"`
	Disabled bool   `json:"disabled"`
	TimeZone string `json:"timeZone,omitempty"`
}

type Crontab struct {
	User     string    `json:"user"`
	Source   string    `json:"source"`
	Raw      string    `json:"raw"`
	Jobs     []CronJob `json:"jobs"`
	Env      []string  `json:"env"`
	Comment  []string  `json:"comments"`
	Revision string    `json:"revision"`
	TimeZone string    `json:"timeZone,omitempty"`
}

type Cron struct {
	run func(context.Context, string, string, bool) (*CommandResult, error)
}

func NewCron() *Cron { return &Cron{} }

func (c *Cron) Available() bool { return hostexec.AvailableOnHost("crontab") }

// UserCrontab reads one user's crontab through the crontab command rather than
// /var/spool, so the platform's own permission and locking rules apply.
func (c *Cron) UserCrontab(ctx context.Context, user string) (*Crontab, error) {
	if err := ValidateName(user); err != nil {
		return nil, err
	}
	res, err := c.command(ctx, user, "", false)
	if err != nil {
		// An empty crontab exits non-zero with this message; that is not an
		// error condition for a viewer.
		if strings.Contains(res.Stderr, "no crontab for") {
			ct := parseCrontab("", false)
			ct.User, ct.Source = user, "crontab -u "+user
			return ct, nil
		}
		return nil, err
	}
	ct := parseCrontab(res.Stdout, false)
	ct.User = user
	ct.Source = "crontab -u " + user
	return ct, nil
}

func (c *Cron) SetUserCrontab(ctx context.Context, user, content string) error {
	return c.SetUserCrontabGuarded(ctx, user, content, nil)
}

func (c *Cron) SetUserCrontabGuarded(ctx context.Context, user, content string, expected *string) error {
	if err := ValidateName(user); err != nil {
		return err
	}
	if err := ValidateCrontab(content); err != nil {
		return err
	}
	unlock := contentguard.Lock("crontab:" + user)
	defer unlock()
	if expected != nil {
		current, err := c.UserCrontab(ctx, user)
		if err != nil {
			return err
		}
		if err := contentguard.Check(current.Raw, expected); err != nil {
			return err
		}
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	_, err := c.command(ctx, user, content, true)
	return err
}

func (c *Cron) command(ctx context.Context, user, content string, write bool) (*CommandResult, error) {
	if c.run != nil {
		return c.run(ctx, user, content, write)
	}
	return runCron(ctx, user, content, write)
}

// ValidateCrontab catches malformed schedules before they are installed.
// crontab itself validates on write, but rejecting here means the operator
// sees which line is wrong instead of a generic failure.
func ValidateCrontab(content string) error {
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cronEnvironment.FindStringSubmatch(trimmed) != nil {
			continue
		}
		if strings.HasPrefix(trimmed, "@") {
			// cronNicknames exists precisely to say which of these are real;
			// accepting any @word meant "@bogus /bin/sh" passed the
			// dashboard's validation and was rejected by crontab itself with
			// a generic error, which is the opposite of what validating here
			// is for.
			fields := strings.Fields(trimmed)
			if !cronNicknames[strings.ToLower(fields[0])] {
				return fmt.Errorf("line %d does not start with a valid schedule: %q", i+1, trimmed)
			}
			if len(fields) < 2 {
				return fmt.Errorf("line %d has a schedule but no command: %q", i+1, trimmed)
			}
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 6 {
			return fmt.Errorf("line %d is not a valid cron entry: %q", i+1, trimmed)
		}
		if !isCronSchedule(fields[:5]) {
			return fmt.Errorf("line %d does not start with a valid five-field schedule: %q", i+1, trimmed)
		}
	}
	return nil
}

var cronEnvironment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

func parseCrontab(content string, withUser bool) *Crontab {
	return parseCrontabIn(content, withUser, hostTimeZone())
}

func parseCrontabIn(content string, withUser bool, zone string) *Crontab {
	baseZone := zone
	ct := &Crontab{Raw: content, Revision: contentguard.Revision(content), TimeZone: zone, Jobs: []CronJob{}, Env: []string{}, Comment: []string{}}
	sc := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	var pendingComment string
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			// A commented-out schedule is a disabled job, not documentation.
			if job, ok := splitCronLine(body, withUser); ok {
				job.Line = lineNo
				job.Raw = raw
				job.Disabled = true
				job.Comment = pendingComment
				job.TimeZone = zone
				pendingComment = ""
				ct.Jobs = append(ct.Jobs, job)
				continue
			}
			pendingComment = body
			ct.Comment = append(ct.Comment, body)
			continue
		}
		if env := cronEnvironment.FindStringSubmatch(trimmed); env != nil {
			ct.Env = append(ct.Env, trimmed)
			if env[1] == "CRON_TZ" {
				zone = strings.Trim(strings.TrimSpace(env[2]), "\"'")
				zone = strings.TrimPrefix(zone, ":")
				if zone == "" {
					zone = baseZone
				}
				ct.TimeZone = zone
			}
			continue
		}
		if job, ok := splitCronLine(trimmed, withUser); ok {
			job.Line = lineNo
			job.Raw = raw
			job.Comment = pendingComment
			job.TimeZone = zone
			pendingComment = ""
			ct.Jobs = append(ct.Jobs, job)
		}
	}
	return ct
}

func hostTimeZone() string {
	if path, err := os.Readlink(hostexec.HostPath("/etc/localtime")); err == nil {
		if _, zone, ok := strings.Cut(path, "/zoneinfo/"); ok {
			return zone
		}
	}
	if raw, err := os.ReadFile(hostexec.HostPath("/etc/timezone")); err == nil {
		zone := strings.TrimSpace(string(raw))
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	return ""
}

// cronNicknames are the @-prefixed shorthands cron understands. Anything else
// after an @ is not a schedule.
var cronNicknames = map[string]bool{
	"@reboot": true, "@yearly": true, "@annually": true, "@monthly": true,
	"@weekly": true, "@daily": true, "@midnight": true, "@hourly": true,
}

// isCronField reports whether one whitespace-separated token is a plausible
// schedule field: a number, a star, a symbolic name, or those combined with
// cron's range, list and step syntax.
//
// Rejecting prose here is the point. /etc/cron.d files are mostly explanatory
// comments, and an English sentence of six or more words otherwise parses as a
// five-field schedule plus a command — which rendered documentation as if it
// were a job.
func isCronField(field string) bool {
	return validCronField(field, 0, 59, "jan feb mar apr may jun jul aug sep oct nov dec sun mon tue wed thu fri sat")
}

func isCronSchedule(fields []string) bool {
	if len(fields) != 5 {
		return false
	}
	for i, f := range fields {
		min := []int{0, 0, 1, 1, 0}[i]
		max := []int{59, 23, 31, 12, 7}[i]
		names := ""
		if i == 3 {
			names = "jan feb mar apr may jun jul aug sep oct nov dec"
		}
		if i == 4 {
			names = "sun mon tue wed thu fri sat"
		}
		if !validCronField(f, min, max, names) {
			return false
		}
	}
	return true
}

func validCronField(field string, min, max int, names string) bool {
	value := func(token string) (int, bool) {
		if n, err := strconv.Atoi(token); cronDigits(token) && err == nil && n >= min && n <= max {
			return n, true
		}
		for i, name := range strings.Fields(names) {
			if strings.EqualFold(token, name) {
				return min + i, true
			}
		}
		return 0, false
	}
	for _, part := range strings.Split(field, ",") {
		step := strings.Split(part, "/")
		if len(step) > 2 {
			return false
		}
		if len(step) == 2 {
			n, err := strconv.Atoi(step[1])
			if !cronDigits(step[1]) || err != nil || n <= 0 || n > 2147483647 {
				return false
			}
		}
		if step[0] == "*" {
			continue
		}
		span := strings.Split(step[0], "-")
		if len(span) > 2 || (len(step) == 2 && len(span) != 2) {
			return false
		}
		lo, ok := value(span[0])
		if !ok {
			return false
		}
		if len(span) == 2 {
			hi, ok := value(span[1])
			if !ok || hi < lo {
				return false
			}
		}
	}
	return true
}

func cronDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// splitCronLine parses one schedule line. withUser selects the /etc/crontab
// and /etc/cron.d dialect, where a username sits between the schedule and the
// command; a personal crontab has no such column.
func splitCronLine(line string, withUser bool) (CronJob, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return CronJob{}, false
	}
	if strings.HasPrefix(fields[0], "@") {
		if !cronNicknames[strings.ToLower(fields[0])] {
			return CronJob{}, false
		}
		rest := fields[1:]
		job := CronJob{Schedule: fields[0]}
		if withUser {
			if len(rest) < 2 {
				return CronJob{}, false
			}
			job.User = rest[0]
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return CronJob{}, false
		}
		job.Command = strings.Join(rest, " ")
		return job, true
	}
	need := 6
	if withUser {
		need = 7
	}
	if len(fields) < need {
		return CronJob{}, false
	}
	if !isCronSchedule(fields[:5]) {
		return CronJob{}, false
	}
	job := CronJob{Schedule: strings.Join(fields[:5], " ")}
	rest := fields[5:]
	if withUser {
		job.User = rest[0]
		rest = rest[1:]
	}
	job.Command = strings.Join(rest, " ")
	return job, true
}

// SystemCronFiles lists the drop-in crontabs that ship with packages. They are
// read-only here: editing them belongs to the package manager, and the file
// manager can be used deliberately if an operator really means to.
func (c *Cron) SystemCronFiles(ctx context.Context) ([]Crontab, error) {
	out := []Crontab{}
	paths := []string{"/etc/crontab"}
	for _, dir := range []string{"/etc/cron.d"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		ct := parseCrontab(string(b), true)
		ct.Source = p
		out = append(out, *ct)
	}
	return out, nil
}

// ListCrontabUsers returns users that have a personal crontab, so the UI can
// offer a picker instead of asking the operator to guess.
func (c *Cron) ListCrontabUsers(ctx context.Context) ([]string, error) {
	users := []string{}
	raw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tab, err := c.UserCrontab(ctx, fields[0])
		if err == nil && tab.Raw != "" {
			users = append(users, fields[0])
		}
	}
	sort.Strings(users)
	return users, nil
}

func runCron(ctx context.Context, user, content string, write bool) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	operation := "-l"
	if write {
		operation = "-"
	}
	args := []string{"-u", user, operation}
	command := hostexec.CommandOnHost(ctx, "crontab", args...)
	if write {
		command.Stdin = strings.NewReader(content)
	}
	return runPrepared(ctx, command, 15*time.Second, "crontab", args...)
}
