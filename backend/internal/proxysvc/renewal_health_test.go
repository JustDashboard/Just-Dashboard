package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The renewal schedule's record, read from a systemd and a journal the test
// owns. The journal is this host's own: certbot.timer active, and the last
// three runs of certbot.service each failing on betbots.site.

// hostJournal is `journalctl -u certbot.service -o json` on the host this was
// written on, for its last three runs, with the fields the reader uses.
const hostJournal = `{"MESSAGE": "Starting certbot.service - Certbot...", "__REALTIME_TIMESTAMP": "1783001161832883", "INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "6", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Failed to renew certificate betbots.site with error: Some challenges have failed.", "__REALTIME_TIMESTAMP": "1783001177174199", "_SYSTEMD_INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "6", "_PID": "45174", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "All renewals failed. The following certificates could not be renewed:", "__REALTIME_TIMESTAMP": "1783001177179173", "_SYSTEMD_INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "6", "_PID": "45174", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "  /etc/letsencrypt/live/betbots.site/fullchain.pem (failure)", "__REALTIME_TIMESTAMP": "1783001177179323", "_SYSTEMD_INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "6", "_PID": "45174", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "1 renew failure(s), 0 parse failure(s)", "__REALTIME_TIMESTAMP": "1783001177181025", "_SYSTEMD_INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "6", "_PID": "45174", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "certbot.service: Main process exited, code=exited, status=1/FAILURE", "__REALTIME_TIMESTAMP": "1783001177274321", "INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "5", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "certbot.service: Failed with result 'exit-code'.", "__REALTIME_TIMESTAMP": "1783001177274879", "INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "4", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Failed to start certbot.service - Certbot.", "__REALTIME_TIMESTAMP": "1783001177275515", "INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "3", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "certbot.service: Consumed 1.395s CPU time, 61.4M memory peak.", "__REALTIME_TIMESTAMP": "1783001177275969", "INVOCATION_ID": "113cc64c666f42ea8e8347e3e3b5fee7", "PRIORITY": "6", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Starting certbot.service - Certbot...", "__REALTIME_TIMESTAMP": "1783037081832250", "INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "6", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Failed to renew certificate betbots.site with error: Some challenges have failed.", "__REALTIME_TIMESTAMP": "1783037090303306", "_SYSTEMD_INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "6", "_PID": "104177", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "All renewals failed. The following certificates could not be renewed:", "__REALTIME_TIMESTAMP": "1783037090308789", "_SYSTEMD_INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "6", "_PID": "104177", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "  /etc/letsencrypt/live/betbots.site/fullchain.pem (failure)", "__REALTIME_TIMESTAMP": "1783037090308984", "_SYSTEMD_INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "6", "_PID": "104177", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "1 renew failure(s), 0 parse failure(s)", "__REALTIME_TIMESTAMP": "1783037090310573", "_SYSTEMD_INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "6", "_PID": "104177", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "certbot.service: Main process exited, code=exited, status=1/FAILURE", "__REALTIME_TIMESTAMP": "1783037090385068", "INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "5", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "certbot.service: Failed with result 'exit-code'.", "__REALTIME_TIMESTAMP": "1783037090385486", "INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "4", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Failed to start certbot.service - Certbot.", "__REALTIME_TIMESTAMP": "1783037090385849", "INVOCATION_ID": "cf4d1042db8a4d3fb699d54c3648ba4b", "PRIORITY": "3", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Starting certbot.service - Certbot...", "__REALTIME_TIMESTAMP": "1790543591171306", "INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "6", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Failed to renew certificate betbots.site with error: Some challenges have failed.", "__REALTIME_TIMESTAMP": "1790543597190152", "_SYSTEMD_INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "6", "_PID": "2427688", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "All renewals failed. The following certificates could not be renewed:", "__REALTIME_TIMESTAMP": "1790543597197020", "_SYSTEMD_INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "6", "_PID": "2427688", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "  /etc/letsencrypt/live/betbots.site/fullchain.pem (failure)", "__REALTIME_TIMESTAMP": "1790543597197221", "_SYSTEMD_INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "6", "_PID": "2427688", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "1 renew failure(s), 0 parse failure(s)", "__REALTIME_TIMESTAMP": "1790543597199468", "_SYSTEMD_INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "6", "_PID": "2427688", "SYSLOG_IDENTIFIER": "certbot"}
{"MESSAGE": "certbot.service: Main process exited, code=exited, status=1/FAILURE", "__REALTIME_TIMESTAMP": "1790543597321908", "INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "5", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "certbot.service: Failed with result 'exit-code'.", "__REALTIME_TIMESTAMP": "1790543597322034", "INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "4", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
{"MESSAGE": "Failed to start certbot.service - Certbot.", "__REALTIME_TIMESTAMP": "1790543597322138", "INVOCATION_ID": "4516b948258345549c58d82297023a05", "PRIORITY": "3", "_PID": "1", "SYSLOG_IDENTIFIER": "systemd"}
`

// A certbot 2.11 log of a `certbot -q renew` that failed, from a run of the
// real certbot against a lineage issued with the manual plugin and no auth
// hook, with its directories renamed to certbot's defaults.
const failedRenewalLog = `2026-09-28 02:26:01,919:DEBUG:certbot._internal.main:certbot version: 2.11.0
2026-09-28 02:26:01,920:DEBUG:certbot._internal.main:Location of certbot entry point: /usr/bin/certbot
2026-09-28 02:26:01,920:DEBUG:certbot._internal.main:Arguments: ['-q', '--no-random-sleep-on-renew', '--config-dir', '/etc/letsencrypt', '--work-dir', '/var/lib/letsencrypt', '--logs-dir', '/var/log/letsencrypt']
2026-09-28 02:26:01,920:DEBUG:certbot._internal.main:Discovered plugins: PluginsRegistry(PluginEntryPoint#manual,PluginEntryPoint#nginx,PluginEntryPoint#null,PluginEntryPoint#standalone,PluginEntryPoint#webroot)
2026-09-28 02:26:01,929:DEBUG:certbot._internal.log:Root logging level set at 40
2026-09-28 02:26:01,930:DEBUG:certbot._internal.display.obj:Notifying user: Processing
/etc/letsencrypt/renewal/x.test.conf
2026-09-28 02:26:01,931:DEBUG:certbot._internal.plugins.selection:Requested authenticator None and installer None
2026-09-28 02:26:01,958:INFO:certbot.ocsp:Cannot extract OCSP URI from /etc/letsencrypt/archive/x.test/cert1.pem
2026-09-28 02:26:01,962:DEBUG:certbot._internal.storage:Should renew, less than 30 days before certificate expiry 2026-10-08 02:26:01 UTC.
2026-09-28 02:26:01,962:INFO:certbot._internal.renewal:Certificate is due for renewal, auto-renewing...
2026-09-28 02:26:01,962:DEBUG:certbot._internal.plugins.selection:Requested authenticator manual and installer None
2026-09-28 02:26:01,963:DEBUG:certbot._internal.plugins.disco:Other error:(PluginEntryPoint#manual): An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.
Traceback (most recent call last):
  File "/usr/lib/python3/dist-packages/certbot/_internal/plugins/disco.py", line 112, in prepare
    self._initialized.prepare()
    ~~~~~~~~~~~~~~~~~~~~~~~~~^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/plugins/manual.py", line 115, in prepare
    raise errors.PluginError(
    ...<2 lines>...
            self.option_name('auth-hook')))
certbot.errors.PluginError: An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.
2026-09-28 02:26:01,964:DEBUG:certbot._internal.plugins.selection:No candidate plugin
2026-09-28 02:26:01,964:ERROR:certbot._internal.renewal:Failed to renew certificate x.test with error: The manual plugin is not working; there may be problems with your existing configuration.
The error was: PluginError('An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.')
2026-09-28 02:26:01,968:DEBUG:certbot._internal.renewal:Traceback was:
Traceback (most recent call last):
  File "/usr/lib/python3/dist-packages/certbot/_internal/renewal.py", line 540, in handle_renewal_request
    main.renew_cert(lineage_config, plugins, renewal_candidate)
    ~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/main.py", line 1547, in renew_cert
    installer, auth = plug_sel.choose_configurator_plugins(config, plugins, "certonly")
                      ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/plugins/selection.py", line 256, in choose_configurator_plugins
    diagnose_configurator_problem("authenticator", req_auth, plugins)
    ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/plugins/selection.py", line 374, in diagnose_configurator_problem
    raise errors.PluginSelectionError(msg)
certbot.errors.PluginSelectionError: The manual plugin is not working; there may be problems with your existing configuration.
The error was: PluginError('An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.')

2026-09-28 02:26:01,969:DEBUG:certbot._internal.display.obj:Notifying user: 
- - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -
2026-09-28 02:26:01,969:ERROR:certbot._internal.renewal:All renewals failed. The following certificates could not be renewed:
2026-09-28 02:26:01,969:ERROR:certbot._internal.renewal:  /etc/letsencrypt/live/x.test/fullchain.pem (failure)
2026-09-28 02:26:01,969:DEBUG:certbot._internal.display.obj:Notifying user: - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -
2026-09-28 02:26:01,969:DEBUG:certbot._internal.log:Exiting abnormally:
Traceback (most recent call last):
  File "/usr/bin/certbot", line 33, in <module>
    sys.exit(load_entry_point('certbot==2.11.0', 'console_scripts', 'certbot')())
             ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~^^
  File "/usr/lib/python3/dist-packages/certbot/main.py", line 19, in main
    return internal_main.main(cli_args)
           ~~~~~~~~~~~~~~~~~~^^^^^^^^^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/main.py", line 1894, in main
    return config.func(config, plugins)
           ~~~~~~~~~~~^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/main.py", line 1642, in renew
    renewed_domains, failed_domains = renewal.handle_renewal_request(config)
                                      ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~^^^^^^^^
  File "/usr/lib/python3/dist-packages/certbot/_internal/renewal.py", line 568, in handle_renewal_request
    raise errors.Error(
        f"{len(renew_failures)} renew failure(s), {len(parse_failures)} parse failure(s)")
certbot.errors.Error: 1 renew failure(s), 0 parse failure(s)
2026-09-28 02:26:01,971:ERROR:certbot._internal.log:1 renew failure(s), 0 parse failure(s)
`

// fakeSystemd puts a systemctl and a journalctl on PATH that answer from files
// in the directory it returns: <unit>.show for `systemctl show <unit>`,
// <unit>.active for is-active, <unit>.start for what `systemctl start <unit>`
// does, and journal.json for journalctl. Every call is logged to argv.log
// with the zone it was asked in.
func fakeSystemd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "systemctl"), fmt.Sprintf(`#!/bin/sh
d=%q
echo "systemctl $* TZ=$TZ" >> "$d/argv.log"
case "$1" in
is-active) if [ -f "$d/$2.active" ]; then echo active; exit 0; fi; echo inactive; exit 3 ;;
show)
	if [ "$2" = "-p" ]; then echo not-found; exit 0; fi
	if [ -f "$d/$2.show" ]; then cat "$d/$2.show"; fi
	exit 0 ;;
start) if [ -f "$d/$2.start" ]; then . "$d/$2.start"; fi; echo "Failed to start $2: Unit $2 not found." >&2; exit 5 ;;
esac
exit 1
`, dir))
	writeExecutable(t, filepath.Join(dir, "journalctl"), fmt.Sprintf(`#!/bin/sh
d=%q
echo "journalctl $*" >> "$d/argv.log"
[ -f "$d/journal.json" ] && cat "$d/journal.json"
exit 0
`, dir))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// failingHost is systemd as it stood on the host: the timer active and due at
// 09:12, the service's last run failed at 21:13.
func failingHost(t *testing.T) string {
	t.Helper()
	dir := fakeSystemd(t)
	writeFile(t, filepath.Join(dir, "certbot.timer.active"), "")
	writeFile(t, filepath.Join(dir, "certbot.timer.show"), "Unit=certbot.service\nLastTriggerUSec=Sun 2026-09-27 21:13:11 UTC\nNextElapseUSecRealtime=Mon 2026-09-28 09:12:44 UTC\n")
	writeFile(t, filepath.Join(dir, "certbot.service.show"), "Result=exit-code\nExecMainStatus=1\nExecMainStartTimestamp=Sun 2026-09-27 21:13:11 UTC\nActiveState=failed\nInvocationID=4516b948258345549c58d82297023a05\n")
	writeFile(t, filepath.Join(dir, "journal.json"), hostJournal)
	return dir
}

func at(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

// The host's own record: the timer is active, which the page used to call
// "Scheduled" in green, and the last run failed on betbots.site — as did
// every run the journal still has.
func TestRenewalHealthReadsAFailingTimer(t *testing.T) {
	dir := failingHost(t)
	lastRun := at("2026-09-27T21:13:11Z")
	health := renewalHealth(context.Background(), "certbot.timer",
		map[string]time.Time{"betbots.site": lastRun.Add(-80 * 24 * time.Hour)})

	if health.State != "failed" || health.Service != "certbot.service" || health.Source != "certbot.service" || health.Error != "" {
		t.Fatalf("health = %+v", health)
	}
	if !health.LastRun.Equal(lastRun) || !health.NextRun.Equal(at("2026-09-28T09:12:44Z")) || health.ExitStatus != 1 {
		t.Fatalf("last %v next %v exit %d", health.LastRun, health.NextRun, health.ExitStatus)
	}
	if len(health.Failures) != 1 || health.Failures[0] != (RenewalFailure{Lineage: "betbots.site", Reason: "Some challenges have failed."}) {
		t.Fatalf("failures = %+v", health.Failures)
	}
	if health.Reason != "" {
		t.Fatalf("a run whose certificate says why also has reason %q", health.Reason)
	}
	// The journal reaches back to July, and every run since failed.
	if want := time.UnixMicro(1783001161832883).UTC(); health.FailingSince == nil || !health.FailingSince.Equal(want) {
		t.Fatalf("failing since %v, want %v", health.FailingSince, want)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "argv.log"))
	for _, want := range []string{
		"systemctl show certbot.timer -p Unit,LastTriggerUSec,NextElapseUSecRealtime TZ=UTC",
		"systemctl show certbot.service -p Result,ExecMainStatus,ExecMainStartTimestamp,ActiveState,InvocationID TZ=UTC",
		"journalctl -u certbot.service --since -14d -n 400 --output=json --no-pager",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("%q was not asked:\n%s", want, raw)
		}
	}
}

// A failure a later renewal fixed no longer describes the certificate, and a
// lineage deleted since cannot fail again.
func TestRenewalHealthRecoversOnceTheCertificateIsRenewed(t *testing.T) {
	failingHost(t)
	lastRun := at("2026-09-27T21:13:11Z")
	health := renewalHealth(context.Background(), "certbot.timer", map[string]time.Time{"betbots.site": lastRun.Add(time.Hour)})
	if health.State != "recovered" || len(health.Failures) != 1 || !health.Failures[0].RenewedSince {
		t.Fatalf("renewed since: %+v", health)
	}
	health = renewalHealth(context.Background(), "certbot.timer", map[string]time.Time{"other.example.com": lastRun})
	if health.State != "recovered" || len(health.Failures) != 0 {
		t.Fatalf("deleted since: %+v", health)
	}
	// Lineages that could not be read say nothing either way.
	health = renewalHealth(context.Background(), "certbot.timer", nil)
	if health.State != "failed" || len(health.Failures) != 1 || health.Failures[0].RenewedSince {
		t.Fatalf("unknown lineages: %+v", health)
	}
}

// A run can fail before any certificate: another certbot holding the lock is
// the common one. What certbot said last is the reason, not its help footer
// and not systemd's own "Failed to start".
func TestRenewalHealthGivesTheRunsReasonWhenNoCertificateFailed(t *testing.T) {
	dir := failingHost(t)
	id := "4516b948258345549c58d82297023a05"
	record := func(pid, us int, message string) string {
		key := "_SYSTEMD_INVOCATION_ID"
		if pid == 1 {
			key = "INVOCATION_ID"
		}
		return fmt.Sprintf(`{"MESSAGE":%q,"__REALTIME_TIMESTAMP":"%d","%s":"%s","PRIORITY":"6","_PID":"%d"}`+"\n", message, us, key, id, pid)
	}
	writeFile(t, filepath.Join(dir, "journal.json"), record(1, 1790543591171306, "Starting certbot.service - Certbot...")+
		record(88, 1790543591900000, "Another instance of Certbot is already running.")+
		record(88, 1790543591900001, "Ask for help or search for solutions at https://community.letsencrypt.org. See the logfile /var/log/letsencrypt/letsencrypt.log or re-run Certbot with -v for more details.")+
		record(1, 1790543592000000, "certbot.service: Main process exited, code=exited, status=1/FAILURE")+
		record(1, 1790543592000001, "Failed to start certbot.service - Certbot."))
	health := renewalHealth(context.Background(), "certbot.timer", map[string]time.Time{})
	if health.State != "failed" || len(health.Failures) != 0 || health.Reason != "Another instance of Certbot is already running." {
		t.Fatalf("health = %+v", health)
	}
	// The only failed run on record starts no streak.
	if health.FailingSince != nil {
		t.Fatalf("failing since %v", health.FailingSince)
	}
}

// The other states the service can be in: its last run succeeded, it is
// running now, it has never run. None of them needs the journal.
func TestRenewalHealthReadsEachStateOfTheService(t *testing.T) {
	for _, c := range []struct{ show, want string }{
		{"Result=success\nExecMainStatus=0\nExecMainStartTimestamp=Sun 2026-09-27 21:13:11 UTC\nActiveState=inactive\n", "ok"},
		{"Result=success\nExecMainStatus=0\nExecMainStartTimestamp=Mon 2026-09-28 09:12:44 UTC\nActiveState=activating\n", "running"},
		{"Result=success\nExecMainStatus=0\nExecMainStartTimestamp=\nActiveState=inactive\n", "never"},
	} {
		dir := failingHost(t)
		writeFile(t, filepath.Join(dir, "certbot.timer.show"), "Unit=certbot.service\nLastTriggerUSec=n/a\nNextElapseUSecRealtime=Mon 2026-09-28 09:12:44 UTC\n")
		writeFile(t, filepath.Join(dir, "certbot.service.show"), c.show)
		health := renewalHealth(context.Background(), "certbot.timer", map[string]time.Time{})
		if health.State != c.want || len(health.Failures) != 0 || health.NextRun == nil {
			t.Fatalf("%q: health = %+v", c.show, health)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "argv.log")); strings.Contains(string(raw), "journalctl") {
			t.Fatalf("%s read the journal", c.want)
		}
	}
}

// Every certbot package names its service after its timer, which is where
// the service is looked for when systemd does not say.
func TestRenewalServiceFollowsEachPackagesTimer(t *testing.T) {
	for timer, want := range map[string]string{
		"certbot.timer":            "certbot.service",
		"certbot-renew.timer":      "certbot-renew.service",
		"snap.certbot.renew.timer": "snap.certbot.renew.service",
	} {
		if got := renewalService(timer, map[string]string{}); got != want {
			t.Fatalf("%s starts %s, want %s", timer, got, want)
		}
	}
	if got := renewalService("certbot.timer", map[string]string{"Unit": "renew-everything.service"}); got != "renew-everything.service" {
		t.Fatalf("systemd's own answer was not taken: %s", got)
	}
	if systemdTime("n/a") != nil || systemdTime("") != nil {
		t.Fatal("never read as a time")
	}
}

// certbot's failure lines as it prints them, one per journal record: a
// plugin's error comes on a line of its own, and a renewal configuration it
// cannot use is a failure of that lineage too.
func TestRunFailuresReadCertbotsOwnLines(t *testing.T) {
	lines := []RenewalLine{
		{Text: "Starting certbot.service - Certbot...", Systemd: true},
		{Text: "Failed to renew certificate x.test with error: The manual plugin is not working; there may be problems with your existing configuration."},
		{Text: "The error was: PluginError('An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.')"},
		{Text: "Renewal configuration file /etc/letsencrypt/renewal/old.example.com.conf (cert: old.example.com) produced an unexpected error: 'NoneType' object has no attribute 'lower'. Skipping."},
		{Text: "All renewals failed. The following certificates could not be renewed:"},
		{Text: "  /etc/letsencrypt/live/x.test/fullchain.pem (failure)"},
		{Text: "1 renew failure(s), 1 parse failure(s)"},
		{Text: "Failed to start certbot.service - Certbot.", Systemd: true, Error: true},
	}
	failures, _ := runFailures(lines)
	want := []RenewalFailure{
		{Lineage: "x.test", Reason: "The manual plugin is not working; there may be problems with your existing configuration. The error was: PluginError('An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.')"},
		{Lineage: "old.example.com", Reason: "'NoneType' object has no attribute 'lower'"},
	}
	if fmt.Sprint(failures) != fmt.Sprint(want) {
		t.Fatalf("failures = %+v", failures)
	}
}

// The log panel reads the same record, newest run first, with systemd's
// lines told apart from certbot's.
func TestRenewalLogListsTheRunsNewestFirst(t *testing.T) {
	failingHost(t)
	log, err := New(t.TempDir(), "").RenewalLog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if log.Source != "certbot.service" || len(log.Runs) != 3 {
		t.Fatalf("log = %+v", log)
	}
	newest := log.Runs[0]
	if !newest.Start.Equal(time.UnixMicro(1790543591171306).UTC()) || newest.Result != "failed" || len(newest.Lines) != 8 {
		t.Fatalf("newest run = %+v", newest)
	}
	if !newest.Lines[0].Systemd || newest.Lines[1].Systemd || !newest.Lines[1].Error ||
		newest.Lines[1].Text != "Failed to renew certificate betbots.site with error: Some challenges have failed." {
		t.Fatalf("lines = %+v", newest.Lines)
	}
	if log.Runs[2].Start.After(log.Runs[1].Start) {
		t.Fatal("the runs are not newest first")
	}
}

// A job that starts the service reads back the run it started: not the one
// before it, and with what the run said.
func TestRenewalRunAfterIsTheRunAJobStarted(t *testing.T) {
	failingHost(t)
	ctx := context.Background()
	if _, _, _, err := RenewalRunAfter(ctx, "certbot.service", "4516b948258345549c58d82297023a05"); err == nil {
		t.Fatal("the run before the start read as the one it started")
	}
	run, failures, _, err := RenewalRunAfter(ctx, "certbot.service", "cf4d1042db8a4d3fb699d54c3648ba4b")
	if err != nil {
		t.Fatal(err)
	}
	if run.Result != "failed" || len(failures) != 1 || failures[0].Lineage != "betbots.site" {
		t.Fatalf("run = %+v, failures %+v", run, failures)
	}
}

// writeLog lays a certbot log down with its lines stamped in a zone of the
// log's own and the file's time at its last line, as certbot leaves one.
func writeLog(t *testing.T, path, content string, zone time.Duration, ended time.Time) {
	t.Helper()
	lines := strings.Split(content, "\n")
	var last time.Time
	for _, line := range lines {
		if m := certbotLogLine.FindStringSubmatch(line); m != nil {
			last, _ = time.Parse(certbotLogTime, m[1])
		}
	}
	// Shift every stamp so the last one is ended in the given zone.
	shift := ended.Add(zone).Sub(last)
	for i, line := range lines {
		if m := certbotLogLine.FindStringSubmatch(line); m != nil {
			wall, _ := time.Parse(certbotLogTime, m[1])
			lines[i] = wall.Add(shift).Format(certbotLogTime) + line[len(m[1]):]
		}
	}
	writeFile(t, path, strings.Join(lines, "\n"))
	if err := os.Chtimes(path, ended, ended); err != nil {
		t.Fatal(err)
	}
}

// A host that renews from cron has no service record: certbot's own log is
// the record, one file per invocation. The newest is often not a renewal —
// an issuance, a dry run — and the failed renewal behind them is the one
// that counts. certbot stamps it in the host's zone, which the file's own
// time gives away.
func TestCronRenewalHealthReadsCertbotsLog(t *testing.T) {
	dir := t.TempDir()
	ended := at("2026-09-28T00:26:02Z")
	writeLog(t, filepath.Join(dir, "letsencrypt.log.2"), failedRenewalLog, 2*time.Hour, ended)
	dry := strings.Replace(failedRenewalLog, "Arguments: ['-q',", "Arguments: ['--dry-run', '-q',", 1)
	writeLog(t, filepath.Join(dir, "letsencrypt.log.1"), dry, 2*time.Hour, ended.Add(time.Hour))
	issuance := "2026-09-28 05:00:00,001:DEBUG:certbot._internal.main:certbot version: 2.11.0\n" +
		"2026-09-28 05:00:00,002:DEBUG:certbot._internal.main:Arguments: ['--webroot', '-w', '/var/www/html', '-d', 'x.test']\n" +
		"2026-09-28 05:00:01,001:INFO:certbot._internal.main:Obtaining a new certificate\n"
	writeLog(t, filepath.Join(dir, "letsencrypt.log"), issuance, 2*time.Hour, ended.Add(2*time.Hour))

	health := logRenewalHealth(dir, map[string]time.Time{"x.test": ended.Add(-time.Hour)})
	if health.State != "failed" || health.Source != filepath.Join(dir, "letsencrypt.log.2") || health.NextRun != nil {
		t.Fatalf("health = %+v", health)
	}
	// The first line is 52ms before the last.
	if want := ended.Add(-52 * time.Millisecond); health.LastRun == nil || !health.LastRun.Equal(want) {
		t.Fatalf("last run %v, want about %v", health.LastRun, want)
	}
	if len(health.Failures) != 1 || health.Failures[0].Lineage != "x.test" ||
		!strings.HasSuffix(health.Failures[0].Reason, "The error was: PluginError('An authentication script must be provided with --manual-auth-hook when using the manual plugin non-interactively.')") {
		t.Fatalf("failures = %+v", health.Failures)
	}
	// Renewed by hand after it: recovered.
	if health := logRenewalHealth(dir, map[string]time.Time{"x.test": ended.Add(time.Minute)}); health.State != "recovered" {
		t.Fatalf("renewed since: %+v", health)
	}
	// A renewal that succeeded is the newest renewal: ok.
	ok := strings.NewReplacer(
		"ERROR:certbot._internal.renewal:Failed to renew", "INFO:certbot._internal.renewal:Renewed",
		"DEBUG:certbot._internal.log:Exiting abnormally:", "DEBUG:certbot._internal.renewal:no renewal failures",
		"ERROR:certbot._internal.log:1 renew failure(s), 0 parse failure(s)", "DEBUG:certbot._internal.main:done",
	).Replace(failedRenewalLog)
	writeLog(t, filepath.Join(dir, "letsencrypt.log.1"), ok, 2*time.Hour, ended.Add(time.Hour))
	if health := logRenewalHealth(dir, nil); health.State != "ok" || !strings.HasSuffix(health.Source, "letsencrypt.log.1") {
		t.Fatalf("succeeded: %+v", health)
	}
	// Debian's cli.ini turns certbot's rotation off, so every invocation is
	// appended to one file: the renewal is found inside it, among the rest.
	single := t.TempDir()
	writeLog(t, filepath.Join(single, "letsencrypt.log"), failedRenewalLog+dry+issuance, 2*time.Hour, ended)
	health = logRenewalHealth(single, nil)
	last, _ := time.Parse(certbotLogTime, "2026-09-28 05:00:01,001")
	first, _ := time.Parse(certbotLogTime, "2026-09-28 02:26:01,919")
	if health.State != "failed" || len(health.Failures) != 1 || health.LastRun == nil || !health.LastRun.Equal(ended.Add(-last.Sub(first))) {
		t.Fatalf("one appended log: %+v", health)
	}
	// No renewal logged at all.
	if health := logRenewalHealth(filepath.Join(dir, "absent"), nil); health.State != "unknown" || health.Error != "" {
		t.Fatalf("no logs: %+v", health)
	}
}
