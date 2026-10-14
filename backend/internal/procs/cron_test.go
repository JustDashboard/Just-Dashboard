package procs

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

// The prose in a packaged /etc/cron.d file is what broke the old parser: any
// comment six words or longer parsed as a five-field schedule plus a command,
// so documentation rendered as if it were a scheduled job.
func TestParseCrontabKeepsProseOutOfJobs(t *testing.T) {
	const content = `# Unlike any other crontab you don't have to run the ` + "`crontab`" + `
# command to install the new version when you edit this file
# and files in /etc/cron.d. These files also have username fields.
17 *	* * *	root	cd / && run-parts --report /etc/cron.hourly
25 6	* * *	root	test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; }
`
	ct := parseCrontab(content, true)
	if len(ct.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d: %+v", len(ct.Jobs), ct.Jobs)
	}
	if len(ct.Comment) != 3 {
		t.Fatalf("expected 3 comment lines, got %d: %+v", len(ct.Comment), ct.Comment)
	}
	first := ct.Jobs[0]
	if first.Schedule != "17 * * * *" {
		t.Errorf("schedule = %q, want %q", first.Schedule, "17 * * * *")
	}
	if first.User != "root" {
		t.Errorf("user = %q, want root", first.User)
	}
	if first.Command != "cd / && run-parts --report /etc/cron.hourly" {
		t.Errorf("command = %q", first.Command)
	}
}

// A personal crontab has no username column, so the field after the schedule
// belongs to the command.
func TestParseCrontabUserDialect(t *testing.T) {
	ct := parseCrontab("*/5 * * * * /usr/bin/backup.sh --now\n", false)
	if len(ct.Jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(ct.Jobs))
	}
	if ct.Jobs[0].User != "" {
		t.Errorf("user = %q, want empty for a personal crontab", ct.Jobs[0].User)
	}
	if ct.Jobs[0].Command != "/usr/bin/backup.sh --now" {
		t.Errorf("command = %q", ct.Jobs[0].Command)
	}
}

// A commented-out schedule is a disabled job; a commented-out sentence is not.
func TestParseCrontabDisabledJob(t *testing.T) {
	ct := parseCrontab("# 30 4 * * * /usr/bin/rotate.sh\n# this one is just a note about it\n", false)
	if len(ct.Jobs) != 1 {
		t.Fatalf("expected 1 disabled job, got %d: %+v", len(ct.Jobs), ct.Jobs)
	}
	if !ct.Jobs[0].Disabled {
		t.Error("job should be marked disabled")
	}
	if len(ct.Comment) != 1 {
		t.Errorf("expected the note to stay a comment, got %+v", ct.Comment)
	}
}

func TestIsCronField(t *testing.T) {
	valid := []string{"*", "5", "*/5", "5-55/10", "1,2,3", "jan", "MON", "mon-fri", "0"}
	for _, f := range valid {
		if !isCronField(f) {
			t.Errorf("isCronField(%q) = false, want true", f)
		}
	}
	invalid := []string{"Unlike", "don't", "crontab", "", "run-parts", "hello,world"}
	for _, f := range invalid {
		if isCronField(f) {
			t.Errorf("isCronField(%q) = true, want false", f)
		}
	}
}

func TestValidateCrontabRejectsProse(t *testing.T) {
	if err := ValidateCrontab("this is not a cron line at all really\n"); err == nil {
		t.Error("expected prose to be rejected")
	}
	if err := ValidateCrontab("*/5 * * * * /usr/bin/backup.sh\n"); err != nil {
		t.Errorf("valid entry rejected: %v", err)
	}
}

func TestValidateCrontabRejectsMalformedFields(t *testing.T) {
	for _, schedule := range []string{"*/5/2 * * * *", "5/5 * * * *", "*/999999999999999999999999 * * * *", "1-2-3 * * * *", "1,,2 * * * *", "*/+5 * * * *", "+1 * * * *", "60 * * * *", "0 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * 8"} {
		if err := ValidateCrontab(schedule + " echo blocked\n"); err == nil {
			t.Errorf("malformed schedule %q accepted", schedule)
		}
	}
	for _, schedule := range []string{"5-59/5 * * * *", "0 0 * jan,dec sun-sat", "0 0 * * 7"} {
		if err := ValidateCrontab(schedule + " echo valid\n"); err != nil {
			t.Errorf("valid schedule %q rejected: %v", schedule, err)
		}
	}
}

func TestCrontabTimeZonesFollowEnvironmentPosition(t *testing.T) {
	const raw = "0 3 * * * first\nCRON_TZ=America/New_York\n0 3 * * * second\nCRON_TZ='Asia/Tokyo'\n# 0 3 * * * disabled\n"
	ct := parseCrontabIn(raw, false, "UTC")
	if ct.Raw != raw || ct.Revision != contentguard.Revision(raw) || ct.TimeZone != "Asia/Tokyo" {
		t.Fatalf("metadata: %+v", ct)
	}
	for i, zone := range []string{"UTC", "America/New_York", "Asia/Tokyo"} {
		if ct.Jobs[i].TimeZone != zone {
			t.Errorf("job %d zone = %q, want %q", i, ct.Jobs[i].TimeZone, zone)
		}
	}
}

func TestGuardedCrontabRefusesNativeEditsAndSerializesDashboardSaves(t *testing.T) {
	ctx := context.Background()
	const original = "15 * * * * echo original\n"
	var mu sync.Mutex
	content, writes := original, 0
	c := &Cron{run: func(_ context.Context, _ string, next string, write bool) (*CommandResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if write {
			content = next
			writes++
		}
		return &CommandResult{Stdout: content}, nil
	}}
	opened, err := c.UserCrontab(ctx, "audit")
	if err != nil {
		t.Fatal(err)
	}
	content = "30 * * * * echo native\n"
	if err := c.SetUserCrontabGuarded(ctx, "audit", original, &opened.Revision); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("native edit accepted: %v", err)
	}
	if writes != 0 || content != "30 * * * * echo native\n" {
		t.Fatal("native edit overwritten")
	}
	content = original
	results := make(chan error, 2)
	for _, next := range []string{"20 * * * * echo first\n", "40 * * * * echo second\n"} {
		go func() { results <- c.SetUserCrontabGuarded(ctx, "audit", next, &opened.Revision) }()
	}
	conflicts := 0
	for range 2 {
		if err := <-results; errors.Is(err, contentguard.ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 || conflicts != 1 {
		t.Fatalf("writes=%d conflicts=%d", writes, conflicts)
	}
}

func TestCrontabEnvironmentWhitespaceAndEmptyZone(t *testing.T) {
	const raw = "CRON_TZ = \"America/New_York\"\n0 3 * * * first\nCRON_TZ = \"\"\n0 3 * * * second\n"
	if err := ValidateCrontab(raw); err != nil {
		t.Fatal(err)
	}
	ct := parseCrontabIn(raw, false, "UTC")
	if len(ct.Jobs) != 2 || ct.Jobs[0].TimeZone != "America/New_York" || ct.Jobs[1].TimeZone != "UTC" {
		t.Fatalf("zones: %+v", ct)
	}
}
