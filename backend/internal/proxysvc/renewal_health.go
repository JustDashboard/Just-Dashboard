package proxysvc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// What the renewal schedule did, not only whether there is one.
//
// A timer that is active says certbot is asked to renew twice a day; it says
// nothing about whether it does. On the host this was written on, certbot.timer
// was active and every run of certbot.service had failed since July, while
// the page reported "Scheduled" in green. The answer is in systemd — the
// service's last result and when the timer fires next — and in the journal,
// where certbot names each certificate it failed to renew and why. Hosts that
// renew from cron have neither, and certbot's own log stands in.

// RenewalHealth is the last renewal run and the next one.
type RenewalHealth struct {
	// Source is where the runs were read: the service the timer starts,
	// or certbot's log on a host that renews from cron.
	Source string `json:"source"`
	// Service is that systemd unit, the one "Run now" starts. Empty on a
	// cron host.
	Service string `json:"service,omitempty"`
	// State is one of:
	//   ok        the last run succeeded
	//   failed    the last run failed, and a certificate it failed on is
	//             still not renewed (or the run failed before any)
	//   recovered the last run failed, but every certificate it failed on
	//             has been renewed since
	//   running   a run is in progress
	//   never     the timer has not run yet
	//   unknown   there is no record of a run to read
	State   string     `json:"state"`
	LastRun *time.Time `json:"lastRun,omitempty"`
	NextRun *time.Time `json:"nextRun,omitempty"`
	// ExitStatus is what the last run exited with.
	ExitStatus int `json:"exitStatus,omitempty"`
	// Reason is why the last run failed when no certificate's failure says
	// it: another certbot holding the lock, a configuration certbot could
	// not parse.
	Reason string `json:"reason,omitempty"`
	// Failures are the certificates the last run failed to renew.
	Failures []RenewalFailure `json:"failures"`
	// FailingSince is the first run of the unbroken streak of failed runs
	// that ends with the last one, as far back as the record reaches; nil
	// when the last run is the first failure on record.
	FailingSince *time.Time `json:"failingSince,omitempty"`
	// HookFailures are the hooks the last run ran that exited with an
	// error. certbot only warns about one, so a run whose reload hook
	// refused to reload nginx still passes.
	HookFailures []HookFailure `json:"hookFailures,omitempty"`
	// Problems are what the last failed run's authority or certbot reported,
	// each at the stage of validation it failed and that stage's owner.
	Problems []IssuanceDiagnosis `json:"problems,omitempty"`
	// Error is why the runs could not be read.
	Error string `json:"error,omitempty"`
}

// HookFailure is one hook certbot ran that exited with an error.
type HookFailure struct {
	// Kind is certbot's name for it: pre-hook, deploy-hook, post-hook.
	Kind string `json:"kind"`
	// Command is what certbot ran, where its log says.
	Command string `json:"command,omitempty"`
	Code    int    `json:"code"`
	// Output is what the hook wrote to its error output.
	Output string `json:"output,omitempty"`
}

// RenewalFailure is one certificate a run failed to renew, in certbot's words.
type RenewalFailure struct {
	Lineage string `json:"lineage"`
	Reason  string `json:"reason"`
	// RenewedSince is a certificate saved after the failed run: the
	// failure no longer describes it.
	RenewedSince bool `json:"renewedSince,omitempty"`
}

// RenewalRun is one run of the renewal, with what it printed.
type RenewalRun struct {
	Start time.Time `json:"start"`
	// Result is "succeeded", "failed", or "" while it runs or when the
	// record does not say.
	Result string        `json:"result"`
	Lines  []RenewalLine `json:"lines"`
}

// RenewalLine is one line a run printed. Error marks the lines worth
// reading first, and Systemd the ones systemd wrote about the run rather
// than certbot.
type RenewalLine struct {
	Time    time.Time `json:"time"`
	Text    string    `json:"text"`
	Error   bool      `json:"error,omitempty"`
	Systemd bool      `json:"systemd,omitempty"`
}

// RenewalLog is the recent renewal runs, newest first.
type RenewalLog struct {
	Source string       `json:"source"`
	Runs   []RenewalRun `json:"runs"`
}

var (
	// renewal.py: logger.error("Failed to renew certificate %s with error: %s").
	renewFailureRe = regexp.MustCompile(`^Failed to renew certificate (\S+) with error: (.+)$`)
	// renewal.py, a renewal configuration certbot could not use.
	parseFailureRe = regexp.MustCompile(`^Renewal configuration file \S+ \(cert: (\S+)\) produced an unexpected error: (.+?)\.? Skipping\.$`)
	// certbot's summary line, which says nothing a failure line did not.
	renewSummaryRe = regexp.MustCompile(`^\d+ renew failure\(s\), \d+ parse failure\(s\)$`)
	// A hook starting, and one that exited with an error: certbot 2
	// (display/ops.py report_executed_command) and certbot 1 (misc.py
	// execute_command) word it differently. The error output follows on
	// lines of its own, each indented by a space.
	hookRunningRe = regexp.MustCompile(`^Running ([\w-]+) command: (.+)$`)
	hookCodeRe    = regexp.MustCompile(`^Hook '([\w-]+)' reported error code (\d+)$`)
	hookCodeV1Re  = regexp.MustCompile(`^([\w-]+) command "(.+)" returned error code (\d+)$`)
	hookOutputRe  = regexp.MustCompile(`^(?:Hook '([\w-]+)' ran with error output|Error output from ([\w-]+) command \S+):$`)
	// systemd's line on how the service's main process ended.
	mainExitRe = regexp.MustCompile(`Main process exited, code=\w+, status=(\d+)/`)
)

// renewalJournalSince bounds how far back the journal is read, and renewalLines
// how much of it: two runs a day for two weeks, with room for what a failing
// run prints.
const (
	renewalJournalSince = "-14d"
	renewalLines        = 400
)

// renewalHealth reads the runs of whatever renews: source is what
// renewalScheduled found, a timer or a cron file. written says when each
// lineage's certificate was last saved, by name — the zero time when that is
// unknown — and is nil when the lineages could not be read.
func renewalHealth(ctx context.Context, source string, written map[string]time.Time) *RenewalHealth {
	if strings.HasSuffix(source, ".timer") {
		return systemdRenewalHealth(ctx, source, written)
	}
	return logRenewalHealth(certbotLogsDir, written)
}

// systemctlShow reads properties of units in the form `systemctl show`
// prints them, with timestamps in UTC so they parse the same whatever the
// host's zone. The container shares the host's PID namespace, and systemd
// reads that as a chroot unless told otherwise (procs.run says more).
func systemctlShow(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, "systemctl", "show", unit, "-p", strings.Join(props, ","))
	cmd.Env = append(cmd.Environ(), "TZ=UTC", "LC_ALL=C", "SYSTEMD_IGNORE_CHROOT=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	return values, nil
}

// systemdTime parses a timestamp systemctl printed in UTC: "Sun 2026-09-27
// 21:13:11 UTC". Empty and "n/a" are never.
func systemdTime(value string) *time.Time {
	t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", value)
	if err != nil || t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// renewalService is the service a certbot timer starts: its Unit property,
// or the timer's own name, which is the service's for every certbot package.
func renewalService(timer string, props map[string]string) string {
	if unit := props["Unit"]; unit != "" {
		return unit
	}
	return strings.TrimSuffix(timer, ".timer") + ".service"
}

func systemdRenewalHealth(ctx context.Context, timer string, written map[string]time.Time) *RenewalHealth {
	health := &RenewalHealth{Source: timer, State: "unknown", Failures: []RenewalFailure{}}
	timerProps, err := systemctlShow(ctx, timer, "Unit", "LastTriggerUSec", "NextElapseUSecRealtime")
	if err != nil {
		health.Error = err.Error()
		return health
	}
	health.Service = renewalService(timer, timerProps)
	health.Source = health.Service
	health.NextRun = systemdTime(timerProps["NextElapseUSecRealtime"])
	service, err := systemctlShow(ctx, health.Service,
		"Result", "ExecMainStatus", "ExecMainStartTimestamp", "ActiveState", "InvocationID")
	if err != nil {
		health.Error = err.Error()
		return health
	}
	// systemd keeps a unit's last run only until the host restarts; after
	// that the service reads as a unit that never ran, and Result=success
	// is only systemd's default. A Persistent timer keeps its last trigger
	// across the restart, so the run it names is judged by the journal.
	health.LastRun = systemdTime(service["ExecMainStartTimestamp"])
	thisBoot := health.LastRun != nil
	if !thisBoot {
		health.LastRun = systemdTime(timerProps["LastTriggerUSec"])
	}
	switch {
	case service["ActiveState"] == "activating" || service["ActiveState"] == "deactivating":
		health.State = "running"
	case health.LastRun == nil:
		health.State = "never"
	case !thisBoot:
		judgeRunBeforeRestart(ctx, health, written)
	case service["Result"] == "success":
		health.State = "ok"
		health.HookFailures = runHookFailures(*health.LastRun, nil)
	default:
		health.ExitStatus, _ = strconv.Atoi(service["ExecMainStatus"])
		// Only a failed run needs its lines: which certificates, and why.
		runs, err := journalRuns(ctx, health.Service)
		if err != nil {
			health.Error = err.Error()
		}
		var last *journalRun
		for i := range runs {
			if id := service["InvocationID"]; id != "" && runs[i].id == id {
				last = &runs[i]
			}
		}
		judgeFailedRun(health, last, written)
		health.FailingSince = failingSince(runs, last)
		var lines []RenewalLine
		if last != nil {
			lines = last.lines
		}
		health.HookFailures = runHookFailures(*health.LastRun, lines)
	}
	return health
}

// judgeRunBeforeRestart reads the timer's last run from the journal, for a
// host restarted since: the newest run of the service it holds, judged by
// systemd's own lines about it. A journal that does not reach the timer's
// last trigger — kept in memory only, or cleared since — has no record of
// that run, and the answer is unknown rather than systemd's success.
func judgeRunBeforeRestart(ctx context.Context, health *RenewalHealth, written map[string]time.Time) {
	runs, err := journalRuns(ctx, health.Service)
	if err != nil {
		health.Error = err.Error()
		return
	}
	if len(runs) == 0 {
		return
	}
	last := &runs[len(runs)-1]
	if last.start().Before(health.LastRun.Add(-time.Minute)) {
		return
	}
	switch last.result() {
	case "failed":
		start := last.start()
		health.LastRun = &start
		health.ExitStatus = last.exitStatus
		judgeFailedRun(health, last, written)
		health.FailingSince = failingSince(runs, last)
	case "succeeded":
		start := last.start()
		health.LastRun = &start
		health.State = "ok"
	default:
		return
	}
	health.HookFailures = runHookFailures(*health.LastRun, last.lines)
}

// judgeFailedRun fills in a failed run: the certificates it failed on and
// whether each has been renewed since, and the run's own reason when no
// certificate's says why.
func judgeFailedRun(health *RenewalHealth, run *journalRun, written map[string]time.Time) {
	health.State = "failed"
	if run == nil {
		return
	}
	failures, reason := runFailures(run.lines)
	health.Problems = runProblems(run.lines)
	start := run.start()
	for _, f := range failures {
		if written != nil {
			saved, exists := written[f.Lineage]
			// A lineage deleted since cannot fail again.
			if !exists {
				continue
			}
			f.RenewedSince = saved.After(start)
		}
		health.Failures = append(health.Failures, f)
	}
	if len(failures) == 0 {
		health.Reason = reason
	}
	if len(health.Failures) > 0 && !slices.ContainsFunc(health.Failures, func(f RenewalFailure) bool { return !f.RenewedSince }) {
		health.State = "recovered"
	}
	// Every certificate it failed on is gone: nothing left to fail.
	if len(failures) > 0 && len(health.Failures) == 0 {
		health.State = "recovered"
	}
}

// runFailures reads certbot's lines from one run: each certificate it failed
// to renew, and otherwise the last thing it said, which is why the whole run
// failed. certbot writes a plugin's error on a line of its own after the
// failure ("The error was: …"), and the journal keeps each line apart.
// runProblems reads a failed run's lines into its problems by stage.
func runProblems(lines []RenewalLine) []IssuanceDiagnosis {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		if !line.Systemd {
			texts = append(texts, line.Text)
		}
	}
	return DiagnoseIssuanceHere(texts)
}

func runFailures(lines []RenewalLine) ([]RenewalFailure, string) {
	var failures []RenewalFailure
	// What certbot said, and of that what it logged as an error: certbot's
	// own log marks its errors, which are the reason when there are any.
	var said, loggedErrors []string
	for _, line := range lines {
		text := strings.TrimSpace(line.Text)
		if line.Systemd {
			continue
		}
		if m := renewFailureRe.FindStringSubmatch(text); m != nil {
			failures = append(failures, RenewalFailure{Lineage: m[1], Reason: m[2]})
			continue
		}
		if m := parseFailureRe.FindStringSubmatch(text); m != nil {
			failures = append(failures, RenewalFailure{Lineage: m[1], Reason: m[2]})
			continue
		}
		if strings.HasPrefix(text, "The error was: ") && len(failures) > 0 {
			failures[len(failures)-1].Reason += " " + text
			continue
		}
		if text != "" && !renewSummaryRe.MatchString(text) {
			said = append(said, text)
			if line.Error {
				loggedErrors = append(loggedErrors, text)
			}
		}
	}
	if len(loggedErrors) > 0 {
		said = loggedErrors
	}
	reason := ""
	if len(said) > 0 {
		reason = lastMeaningfulLine(strings.Join(said, "\n"))
	}
	return failures, reason
}

// hookFailures reads what certbot said about the hooks it ran: each that
// exited with an error, with what it wrote to its error output. A hook that
// wrote to it and exited 0 did not fail.
func hookFailures(lines []RenewalLine) []HookFailure {
	var failures []HookFailure
	running := map[string]string{}
	// The failure whose error output the lines after it carry.
	collecting := -1
	for _, line := range lines {
		if line.Systemd {
			continue
		}
		text := strings.TrimSpace(line.Text)
		if collecting >= 0 && strings.HasPrefix(line.Text, " ") && text != "" {
			f := &failures[collecting]
			f.Output = strings.TrimSpace(f.Output + " " + text)
			continue
		}
		collecting = -1
		if m := hookRunningRe.FindStringSubmatch(text); m != nil {
			running[m[1]] = m[2]
			continue
		}
		if m := hookCodeRe.FindStringSubmatch(text); m != nil {
			code, _ := strconv.Atoi(m[2])
			failures = append(failures, HookFailure{Kind: m[1], Command: running[m[1]], Code: code})
			continue
		}
		if m := hookCodeV1Re.FindStringSubmatch(text); m != nil {
			code, _ := strconv.Atoi(m[3])
			failures = append(failures, HookFailure{Kind: m[1], Command: m[2], Code: code})
			continue
		}
		if m := hookOutputRe.FindStringSubmatch(text); m != nil {
			kind := m[1] + m[2]
			// certbot reports the code first, then the output.
			if n := len(failures) - 1; n >= 0 && failures[n].Kind == kind && failures[n].Output == "" {
				collecting = n
			}
		}
	}
	return failures
}

// runHookFailures is what the hooks of the run that started at start did.
// certbot's own log keeps it whatever the run printed: the timer's
// `certbot -q` shows only errors, and a hook's failure is a warning, so the
// journal's lines have it only from a certbot run without -q.
func runHookFailures(start time.Time, journal []RenewalLine) []HookFailure {
	if run, _ := loggedRenewalNear(certbotLogsDir, start); run != nil {
		return hookFailures(run.Lines)
	}
	return hookFailures(journal)
}

// failingSince walks back from the last run while runs failed, and is nil
// when the last run is the only failed one the journal holds.
func failingSince(runs []journalRun, last *journalRun) *time.Time {
	if last == nil {
		return nil
	}
	since := last.start()
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		if run.start().After(since) || run.id == last.id {
			continue
		}
		if run.result() != "failed" {
			break
		}
		since = run.start()
	}
	if since.Equal(last.start()) {
		return nil
	}
	return &since
}

// journalRun is one invocation of the renewal service, as the journal keeps
// it: certbot's lines carry the invocation as _SYSTEMD_INVOCATION_ID, and
// systemd's own about the unit as INVOCATION_ID.
type journalRun struct {
	id    string
	lines []RenewalLine
	// failed and finished are systemd's verdict on the run, when the
	// journal still has it, and exitStatus what its main process exited with.
	failed, finished bool
	exitStatus       int
}

func (r journalRun) start() time.Time {
	if len(r.lines) == 0 {
		return time.Time{}
	}
	return r.lines[0].Time
}

func (r journalRun) result() string {
	switch {
	case r.failed:
		return "failed"
	case r.finished:
		return "succeeded"
	}
	return ""
}

type journalRecord struct {
	Message    json.RawMessage `json:"MESSAGE"`
	Realtime   string          `json:"__REALTIME_TIMESTAMP"`
	Invocation string          `json:"_SYSTEMD_INVOCATION_ID"`
	UnitRun    string          `json:"INVOCATION_ID"`
	Priority   string          `json:"PRIORITY"`
	PID        string          `json:"_PID"`
}

// journalMessage is a record's MESSAGE, which the journal writes as a string
// for text and as an array of bytes for anything else.
func journalMessage(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var bytes []byte
	var numbers []int
	if json.Unmarshal(raw, &numbers) == nil {
		for _, n := range numbers {
			bytes = append(bytes, byte(n))
		}
	}
	return string(bytes)
}

// journalRuns reads the renewal service's recent runs, oldest first.
func journalRuns(ctx context.Context, service string) ([]journalRun, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, "journalctl", "-u", service, "--since", renewalJournalSince,
		"-n", strconv.Itoa(renewalLines), "--output=json", "--no-pager")
	cmd.Env = append(cmd.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("the journal of %s could not be read: %w", service, err)
	}
	return parseJournalRuns(out), nil
}

func parseJournalRuns(out []byte) []journalRun {
	var runs []journalRun
	index := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var rec journalRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		id := rec.Invocation
		if id == "" {
			id = rec.UnitRun
		}
		text := journalMessage(rec.Message)
		if id == "" || text == "" {
			continue
		}
		us, _ := strconv.ParseInt(rec.Realtime, 10, 64)
		priority, err := strconv.Atoi(rec.Priority)
		if err != nil {
			priority = 6
		}
		i, ok := index[id]
		if !ok {
			i = len(runs)
			index[id] = i
			runs = append(runs, journalRun{id: id})
		}
		run := &runs[i]
		fromSystemd := rec.PID == "1"
		if fromSystemd {
			switch {
			case strings.Contains(text, "Failed with result"), strings.HasPrefix(text, "Failed to start"):
				run.failed = true
			case strings.HasPrefix(text, "Finished "), strings.HasSuffix(text, "Deactivated successfully."):
				run.finished = true
			}
			if m := mainExitRe.FindStringSubmatch(text); m != nil {
				run.exitStatus, _ = strconv.Atoi(m[1])
			}
		}
		trimmed := strings.TrimSpace(text)
		run.lines = append(run.lines, RenewalLine{
			Time:    time.UnixMicro(us).UTC(),
			Text:    text,
			Error:   priority <= 3 || (!fromSystemd && (renewFailureRe.MatchString(trimmed) || parseFailureRe.MatchString(trimmed))),
			Systemd: fromSystemd,
		})
	}
	return runs
}

// RenewalLog is the recent runs of whatever renews, for the page's log
// panel: the service's journal on a systemd host, certbot's own log on a
// cron one.
func (s *Service) RenewalLog(ctx context.Context) (*RenewalLog, error) {
	scheduled, source := renewalScheduled(ctx)
	if !scheduled {
		return nil, fmt.Errorf("nothing is scheduled to renew certificates here")
	}
	if strings.HasSuffix(source, ".timer") {
		props, err := systemctlShow(ctx, source, "Unit")
		if err != nil {
			return nil, err
		}
		service := renewalService(source, props)
		runs, err := journalRuns(ctx, service)
		if err != nil {
			return nil, err
		}
		log := &RenewalLog{Source: service, Runs: []RenewalRun{}}
		for i := len(runs) - 1; i >= 0; i-- {
			log.Runs = append(log.Runs, RenewalRun{Start: runs[i].start(), Result: runs[i].result(), Lines: runs[i].lines})
		}
		return log, nil
	}
	run, path, err := lastLoggedRenewal(certbotLogsDir)
	if err != nil {
		return nil, err
	}
	log := &RenewalLog{Source: path, Runs: []RenewalRun{}}
	if run != nil {
		log.Runs = append(log.Runs, *run)
	}
	return log, nil
}

// ErrRenewalNotSystemd is a host whose renewal is not a systemd timer's: a
// cron entry has no service to start.
var ErrRenewalNotSystemd = errors.New("certbot renews from cron here, not from a systemd timer")

// serviceNameRe is a unit name systemctl can be handed as one argument.
var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,200}\.service$`)

// RenewalServiceFor is the service the active certbot timer starts.
func RenewalServiceFor(ctx context.Context) (string, error) {
	scheduled, source := renewalScheduled(ctx)
	if !scheduled {
		return "", fmt.Errorf("nothing is scheduled to renew certificates here")
	}
	if !strings.HasSuffix(source, ".timer") {
		return "", ErrRenewalNotSystemd
	}
	props, err := systemctlShow(ctx, source, "Unit")
	if err != nil {
		return "", err
	}
	service := renewalService(source, props)
	if !serviceNameRe.MatchString(service) {
		return "", fmt.Errorf("%s starts %q, which is not a service name", source, service)
	}
	return service, nil
}

// StartRenewalCommand starts the renewal service and waits for its run to end:
// the timer's services are oneshot, so `systemctl start` returns when certbot
// has finished, with the run's failure as its own.
func StartRenewalCommand(ctx context.Context, service string) *exec.Cmd {
	cmd := hostexec.CommandOnHost(ctx, "systemctl", "start", service)
	cmd.Env = append(cmd.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	return cmd
}

// StopRenewalCommand stops the renewal service and waits until it has
// stopped: systemd cancels a start still queued and ends certbot's run. The
// systemctl that waits on a start is only a client, and killing it leaves
// the run going.
func StopRenewalCommand(ctx context.Context, service string) *exec.Cmd {
	cmd := hostexec.CommandOnHost(ctx, "systemctl", "stop", service)
	cmd.Env = append(cmd.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	return cmd
}

// RenewalInvocation is the service's current run, by systemd's invocation
// ID, and whether it is running now: what a job that starts the service reads
// first, so that afterwards it can tell the run it started from the one
// before it.
func RenewalInvocation(ctx context.Context, service string) (string, bool, error) {
	props, err := systemctlShow(ctx, service, "InvocationID", "ActiveState")
	if err != nil {
		return "", false, err
	}
	active := props["ActiveState"] == "activating" || props["ActiveState"] == "deactivating"
	return props["InvocationID"], active, nil
}

// RenewalRunAfter is the service's last run, with what it printed, the
// certificates it failed on and otherwise why it failed — provided it is not
// the run previous names. A `systemctl start` that joined a run already in
// progress passes previous as "", since that run is the one it waited for.
func RenewalRunAfter(ctx context.Context, service, previous string) (*RenewalRun, []RenewalFailure, string, error) {
	props, err := systemctlShow(ctx, service, "InvocationID", "Result")
	if err != nil {
		return nil, nil, "", err
	}
	id := props["InvocationID"]
	if id == "" || id == previous {
		return nil, nil, "", fmt.Errorf("%s did not run", service)
	}
	runs, err := journalRuns(ctx, service)
	if err != nil {
		return nil, nil, "", err
	}
	for _, r := range runs {
		if r.id != id {
			continue
		}
		run := &RenewalRun{Start: r.start(), Result: "failed", Lines: r.lines}
		if props["Result"] == "success" {
			run.Result = "succeeded"
		}
		failures, reason := runFailures(r.lines)
		return run, failures, reason, nil
	}
	return nil, nil, "", fmt.Errorf("the journal has no record of the run of %s", service)
}

// certbotLogsDir is certbot's default log directory, which a cron entry's
// `certbot renew` writes to. A variable for tests.
var certbotLogsDir = "/var/log/letsencrypt"

// certbotLogLine is one line of certbot's log file:
// "2026-09-28 02:26:01,964:ERROR:certbot._internal.renewal:Failed to …".
var certbotLogLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3}):([A-Z]+):[\w.]+:(.*)$`)

// certbotLogTime is how Python's logging stamps a line: milliseconds after a
// comma.
const certbotLogTime = "2006-01-02 15:04:05,000"

// maxLogScan bounds the search for the last renewal: certbot rotates its log
// on every invocation, keeping up to a thousand.
const (
	maxLogScan  = 30
	maxLogBytes = 4 << 20
)

// logRenewalHealth reads the last renewal run certbot logged, for a host
// that renews from cron. There is no next run to report: cron's schedule
// is its own, and certbot sleeps a random while before renewing anyway.
func logRenewalHealth(dir string, written map[string]time.Time) *RenewalHealth {
	health := &RenewalHealth{Source: filepath.Join(dir, "letsencrypt.log"), State: "unknown", Failures: []RenewalFailure{}}
	run, path, err := lastLoggedRenewal(dir)
	if err != nil {
		health.Error = err.Error()
		return health
	}
	if run == nil {
		return health
	}
	health.Source = path
	start := run.Start
	health.LastRun = &start
	health.HookFailures = hookFailures(run.Lines)
	if run.Result != "failed" {
		health.State = "ok"
		return health
	}
	judgeFailedRun(health, &journalRun{lines: run.Lines, failed: true}, written)
	return health
}

// lastLoggedRenewal finds the last renewal certbot logged — a `certbot renew`
// that was not a dry run — searching its logs newest first. certbot either
// starts a new log for every invocation, keeping the old ones numbered, or,
// as Debian's cli.ini has it (max-log-backups = 0, logrotate doing the
// rotating), appends every invocation to the one file; either way the
// newest invocation is often not a renewal but an issuance or a listing.
func lastLoggedRenewal(dir string) (*RenewalRun, string, error) {
	logs, err := certbotLogs(dir)
	if err != nil {
		return nil, "", err
	}
	for _, log := range logs {
		runs, err := loggedRenewals(log.path, log.mod)
		if err != nil {
			return nil, "", err
		}
		if len(runs) > 0 {
			return &runs[len(runs)-1], log.path, nil
		}
	}
	return nil, "", nil
}

// loggedRenewalNear is the renewal certbot logged that began within a
// minute of at, or two after it: the one a run of the renewal service that
// started at at made, found among whatever else certbot has logged since.
func loggedRenewalNear(dir string, at time.Time) (*RenewalRun, error) {
	logs, err := certbotLogs(dir)
	if err != nil {
		return nil, err
	}
	for _, log := range logs {
		// Newest first: a log last written before the run began, and every
		// one after it, holds nothing of the run.
		if log.mod.Before(at) {
			break
		}
		runs, err := loggedRenewals(log.path, log.mod)
		if err != nil {
			return nil, err
		}
		for i := len(runs) - 1; i >= 0; i-- {
			if d := runs[i].Start.Sub(at); d > -time.Minute && d < 2*time.Minute {
				return &runs[i], nil
			}
		}
	}
	return nil, nil
}

type certbotLog struct {
	path string
	mod  time.Time
}

// certbotLogs are certbot's log files, newest first, as many as are worth
// searching.
func certbotLogs(dir string) ([]certbotLog, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("certbot's logs could not be read: %w", err)
	}
	var logs []certbotLog
	for _, e := range entries {
		if e.Name() != "letsencrypt.log" && !strings.HasPrefix(e.Name(), "letsencrypt.log.") {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			logs = append(logs, certbotLog{filepath.Join(dir, e.Name()), info.ModTime()})
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].mod.After(logs[j].mod) })
	if len(logs) > maxLogScan {
		logs = logs[:maxLogScan]
	}
	return logs, nil
}

// loggedRenewals reads the renewals one certbot log holds, oldest first.
// Each invocation opens with the version it is; a renewal is the only
// command that announces each configuration it is "Processing", and a dry
// run says so in its arguments. Past maxLogBytes only the end of the file is
// read, and the invocation cut in half at the start of it is left out.
//
// certbot stamps each line with the host's wall-clock time and no zone. The
// file's modification time is the moment of its last line, so the two
// together give the zone, to the quarter hour zones come in.
func loggedRenewals(path string, modified time.Time) ([]RenewalRun, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("certbot's log could not be read: %w", err)
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() > maxLogBytes {
		if _, err := file.Seek(-maxLogBytes, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	type logged struct {
		wall time.Time
		line RenewalLine
	}
	type invocation struct {
		first                    time.Time
		lines                    []logged
		renewal, dryRun, aborted bool
	}
	var invocations []*invocation
	var current *invocation
	var lastWall time.Time
	previousLevel, previousError := "", false
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		text := sc.Text()
		m := certbotLogLine.FindStringSubmatch(text)
		if m == nil {
			// A message that runs on past its first line: a plugin's
			// error after a failure ("The error was: …"), a hook's error
			// output after its warning. A debug record's are tracebacks.
			if current != nil && previousLevel != "" && previousLevel != "DEBUG" && strings.TrimSpace(text) != "" {
				current.lines = append(current.lines, logged{lastWall, RenewalLine{Text: text, Error: previousError}})
			}
			continue
		}
		wall, _ := time.Parse(certbotLogTime, m[1])
		lastWall = wall
		level, message := m[2], m[3]
		previousLevel = level
		if strings.HasPrefix(message, "certbot version: ") {
			current = &invocation{first: wall}
			invocations = append(invocations, current)
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(message, "Arguments: "):
			current.dryRun = strings.Contains(message, "'--dry-run'")
		case strings.HasPrefix(message, "Notifying user: Processing"):
			current.renewal = true
		case message == "Exiting abnormally:":
			current.aborted = true
		}
		previousError = level == "ERROR" || level == "CRITICAL"
		if level != "DEBUG" {
			current.lines = append(current.lines, logged{wall, RenewalLine{Text: message, Error: previousError}})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	zone := lastWall.Sub(modified.UTC()).Round(15 * time.Minute)
	var runs []RenewalRun
	for _, inv := range invocations {
		if !inv.renewal || inv.dryRun {
			continue
		}
		run := RenewalRun{Start: inv.first.Add(-zone), Result: "succeeded", Lines: []RenewalLine{}}
		for _, l := range inv.lines {
			l.line.Time = l.wall.Add(-zone)
			run.Lines = append(run.Lines, l.line)
		}
		if failures, _ := runFailures(run.Lines); inv.aborted || len(failures) > 0 {
			run.Result = "failed"
		}
		runs = append(runs, run)
	}
	return runs, nil
}
