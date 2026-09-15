package procs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
}

type Crontab struct {
	User    string    `json:"user"`
	Source  string    `json:"source"`
	Raw     string    `json:"raw"`
	Jobs    []CronJob `json:"jobs"`
	Env     []string  `json:"env"`
	Comment []string  `json:"comments"`
}

type Cron struct{}

func NewCron() *Cron { return &Cron{} }

func (c *Cron) Available() bool { return hostexec.AvailableOnHost("crontab") }

// UserCrontab reads one user's crontab through the crontab command rather than
// /var/spool, so the platform's own permission and locking rules apply.
func (c *Cron) UserCrontab(ctx context.Context, user string) (*Crontab, error) {
	if err := ValidateName(user); err != nil {
		return nil, err
	}
	res, err := runCron(ctx, user, "", false)
	if err != nil {
		// An empty crontab exits non-zero with this message; that is not an
		// error condition for a viewer.
		if strings.Contains(res.Stderr, "no crontab for") {
			return &Crontab{User: user, Source: "crontab -u " + user, Jobs: []CronJob{}, Env: []string{}, Comment: []string{}}, nil
		}
		return nil, err
	}
	ct := parseCrontab(res.Stdout, false)
	ct.User = user
	ct.Source = "crontab -u " + user
	return ct, nil
}

func (c *Cron) SetUserCrontab(ctx context.Context, user, content string) error {
	if err := ValidateName(user); err != nil {
		return err
	}
	if err := ValidateCrontab(content); err != nil {
		return err
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	_, err := runCron(ctx, user, content, true)
	return err
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
		if strings.Contains(trimmed, "=") && !strings.HasPrefix(trimmed, "@") {
			if idx := strings.Index(trimmed, "="); idx > 0 && !strings.ContainsAny(trimmed[:idx], " \t") {
				continue // environment assignment
			}
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

func parseCrontab(content string, withUser bool) *Crontab {
	ct := &Crontab{Raw: content, Jobs: []CronJob{}, Env: []string{}, Comment: []string{}}
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
				pendingComment = ""
				ct.Jobs = append(ct.Jobs, job)
				continue
			}
			pendingComment = body
			ct.Comment = append(ct.Comment, body)
			continue
		}
		if idx := strings.Index(trimmed, "="); idx > 0 && !strings.ContainsAny(trimmed[:idx], " \t") {
			ct.Env = append(ct.Env, trimmed)
			continue
		}
		if job, ok := splitCronLine(trimmed, withUser); ok {
			job.Line = lineNo
			job.Raw = raw
			job.Comment = pendingComment
			pendingComment = ""
			ct.Jobs = append(ct.Jobs, job)
		}
	}
	return ct
}

// cronNames are the symbolic month and weekday names cron accepts in place of
// a number.
var cronNames = map[string]bool{
	"jan": true, "feb": true, "mar": true, "apr": true, "may": true, "jun": true,
	"jul": true, "aug": true, "sep": true, "oct": true, "nov": true, "dec": true,
	"sun": true, "mon": true, "tue": true, "wed": true, "thu": true, "fri": true,
	"sat": true,
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
	if field == "" {
		return false
	}
	for _, part := range strings.FieldsFunc(field, func(r rune) bool {
		return r == ',' || r == '-' || r == '/'
	}) {
		if part == "*" {
			continue
		}
		if _, err := strconv.Atoi(part); err == nil {
			continue
		}
		if cronNames[strings.ToLower(part)] {
			continue
		}
		return false
	}
	return true
}

func isCronSchedule(fields []string) bool {
	for _, f := range fields {
		if !isCronField(f) {
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
