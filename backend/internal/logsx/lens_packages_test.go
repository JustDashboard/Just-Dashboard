package logsx

import "testing"

// apt's history.log on this host, three transactions of it: one asked for
// by a user, one run unattended and cut off with no End-Date (the disk was
// full), and one whose dependencies outnumber what the list keeps.
func TestLensPackagesHistory(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "packages", []string{
		"Start-Date: 2026-09-25  12:31:40",
		"Commandline: apt-get install -y -q postgresql",
		"Requested-By: ubuntu (1000)",
		"Install: libtypes-serialiser-perl:amd64 (1.01-1, automatic), ssl-cert:amd64 (1.1.3ubuntu1, automatic), postgresql-17:amd64 (17.7-0ubuntu0.25.04.1, automatic), postgresql-common-dev:amd64 (274, automatic), libjson-perl:amd64 (4.10000-1, automatic), postgresql-client-17:amd64 (17.7-0ubuntu0.25.04.1, automatic), libcommon-sense-perl:amd64 (3.75-3build5, automatic), libipc-run-perl:amd64 (20231003.0-2, automatic), postgresql-common:amd64 (274, automatic), postgresql-client-common:amd64 (274, automatic), libio-pty-perl:amd64 (1:1.20-1build3, automatic), postgresql:amd64 (17+274), libjson-xs-perl:amd64 (4.040-0ubuntu0.25.04.1, automatic), libpq5:amd64 (17.7-0ubuntu0.25.04.1, automatic)",
		"End-Date: 2026-09-25  12:32:03",
		"",
		"Start-Date: 2026-01-07  06:56:40",
		"Commandline: /usr/bin/unattended-upgrade",
		"Upgrade: libpng16-16t64:amd64 (1.6.47-1.1, 1.6.47-1.1ubuntu0.1)",
		"",
		"Start-Date: 2026-09-02  03:46:55",
		"Commandline: apt install cloc",
		"Requested-By: ubuntu (1000)",
		"Install: libvariable-magic-perl:amd64 (0.64-1build1, automatic), libtry-tiny-perl:amd64 (0.32-1, automatic), libsub-quote-perl:amd64 (2.006008-1ubuntu1, automatic), librole-tiny-perl:amd64 (2.002004-1, automatic), libparallel-forkmanager-perl:amd64 (2.03-1, automatic), libb-hooks-endofscope-perl:amd64 (0.28-1, automatic), libdevel-callchecker-perl:amd64 (0.009-1build1, automatic), libdynaloader-functions-perl:amd64 (0.004-1, automatic), libclass-xsaccessor-perl:amd64 (1.19-4build6, automatic), libmoo-perl:amd64 (2.005005-1, automatic), libnamespace-clean-perl:amd64 (0.27-2, automatic), libregexp-common-perl:amd64 (2024080801-1, automatic), libimport-into-perl:amd64 (1.002005-2, automatic), libmodule-implementation-perl:amd64 (0.09-2, automatic), libparams-classify-perl:amd64 (0.015-2build6, automatic), libsub-identify-perl:amd64 (0.14-3build4, automatic), libmodule-runtime-perl:amd64 (0.016-2, automatic), libpackage-stash-xs-perl:amd64 (0.30-1build5, automatic), libpackage-stash-perl:amd64 (0.40-1, automatic), cloc:amd64 (2.04-1), libb-hooks-op-check-perl:amd64 (0.22-3build2, automatic), libsub-exporter-progressive-perl:amd64 (0.001013-3, automatic), libclass-method-modifiers-perl:amd64 (2.15-1, automatic), libsub-name-perl:amd64 (0.28-1, automatic)",
		"End-Date: 2026-09-02  03:46:57",
	}, []appLensWant{
		{event: "transaction_start", level: "info", at: "2026-09-25T09:31:40Z"},
		{event: "command", level: "info", at: "2026-09-25T09:31:40Z", attrs: map[string]string{"command": "apt-get install -y -q postgresql"}},
		{level: "info", at: "2026-09-25T09:31:40Z", attrs: map[string]string{"command": "apt-get install -y -q postgresql", "user": "ubuntu"}},
		{event: "install", level: "info", at: "2026-09-25T09:31:40Z", attrs: map[string]string{
			"command": "apt-get install -y -q postgresql", "user": "ubuntu", "packages": "14",
			"package": "postgresql, libtypes-serialiser-perl, ssl-cert, postgresql-17, postgresql-common-dev, libjson-perl, postgresql-client-17, libcommon-sense-perl, libipc-run-perl, postgresql-common +4 more",
		}},
		{event: "transaction_end", level: "info", at: "2026-09-25T09:32:03Z", attrs: map[string]string{"command": "apt-get install -y -q postgresql", "user": "ubuntu", "duration_ms": "23000"}},
		{},
		{event: "transaction_start", level: "info", at: "2026-01-07T03:56:40Z"},
		{event: "command", level: "info", at: "2026-01-07T03:56:40Z", attrs: map[string]string{"command": "/usr/bin/unattended-upgrade"}},
		{event: "upgrade", level: "info", at: "2026-01-07T03:56:40Z", attrs: map[string]string{
			"command": "/usr/bin/unattended-upgrade", "packages": "1", "package": "libpng16-16t64",
			"version": "1.6.47-1.1ubuntu0.1", "old_version": "1.6.47-1.1",
		}},
		{},
		{event: "transaction_start", level: "info", at: "2026-09-02T00:46:55Z"},
		{event: "command", level: "info", at: "2026-09-02T00:46:55Z", attrs: map[string]string{"command": "apt install cloc"}},
		{level: "info", at: "2026-09-02T00:46:55Z", attrs: map[string]string{"command": "apt install cloc", "user": "ubuntu"}},
		{event: "install", level: "info", at: "2026-09-02T00:46:55Z", attrs: map[string]string{
			"command": "apt install cloc", "user": "ubuntu", "packages": "24",
			"package": "cloc, libvariable-magic-perl, libtry-tiny-perl, libsub-quote-perl, librole-tiny-perl, libparallel-forkmanager-perl, libb-hooks-endofscope-perl, libdevel-callchecker-perl, libdynaloader-functions-perl +15 more",
		}},
		{event: "transaction_end", level: "info", at: "2026-09-02T00:46:57Z", attrs: map[string]string{"command": "apt install cloc", "user": "ubuntu", "duration_ms": "2000"}},
	})
}

// history.log's Remove, Purge and Error fields, in the format apt writes
// them; this host's history holds none.
func TestLensPackagesHistoryRemovals(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "packages", []string{
		"Start-Date: 2026-09-26  10:00:00",
		"Commandline: apt-get purge -y nginx",
		"Remove: nginx:amd64 (1.26.3-2ubuntu1.2)",
		"Purge: nginx-common:amd64 (1.26.3-2ubuntu1.2)",
		"Error: Sub-process /usr/bin/dpkg returned an error code (1)",
		"End-Date: 2026-09-26  10:00:04",
	}, []appLensWant{
		{event: "transaction_start", level: "info", at: "2026-09-26T07:00:00Z"},
		{event: "command", level: "info", at: "2026-09-26T07:00:00Z", attrs: map[string]string{"command": "apt-get purge -y nginx"}},
		{event: "remove", level: "info", at: "2026-09-26T07:00:00Z", attrs: map[string]string{"command": "apt-get purge -y nginx", "packages": "1", "package": "nginx", "version": "1.26.3-2ubuntu1.2"}},
		{event: "purge", level: "info", at: "2026-09-26T07:00:00Z", attrs: map[string]string{"command": "apt-get purge -y nginx", "packages": "1", "package": "nginx-common", "version": "1.26.3-2ubuntu1.2"}},
		{event: "error", level: "error", at: "2026-09-26T07:00:00Z", attrs: map[string]string{"command": "apt-get purge -y nginx"}},
		{event: "transaction_end", level: "info", at: "2026-09-26T07:00:04Z", attrs: map[string]string{"command": "apt-get purge -y nginx", "duration_ms": "4000"}},
	})
}

// dpkg.log on this host. Its status, trigproc and startup lines are the
// steps of an action and are neither events nor attrs, so a package's
// history counts each change once; the word scan's reading of
// "liberror-perl" as an error does not survive either.
func TestLensPackagesDpkg(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "packages", []string{
		"2026-09-02 03:46:55 startup archives unpack",
		"2026-09-02 03:46:55 install libclass-method-modifiers-perl:all <none> 2.15-1",
		"2026-09-02 03:46:55 status half-installed libclass-method-modifiers-perl:all 2.15-1",
		"2026-09-02 03:46:56 configure libdynaloader-functions-perl:all 0.004-1 <none>",
		"2026-09-02 03:46:56 status installed libdynaloader-functions-perl:all 0.004-1",
		"2026-09-02 03:46:56 trigproc man-db:amd64 2.13.0-1 <none>",
		"2026-09-05 22:34:18 upgrade docker-ce-cli:amd64 5:29.7.2-1~ubuntu.24.04~noble 5:29.8.0-1~ubuntu.24.04~noble",
		"2026-09-05 22:34:18 status half-configured liberror-perl:all 0.17029-2",
		// remove and purge in dpkg's format; this host's dpkg.log has none.
		"2026-09-26 10:00:01 remove nginx:amd64 1.26.3-2ubuntu1.2 <none>",
		"2026-09-26 10:00:02 purge nginx-common:all 1.26.3-2ubuntu1.2 <none>",
	}, []appLensWant{
		{level: "info", at: "2026-09-02T00:46:55Z"},
		{event: "install", level: "info", at: "2026-09-02T00:46:55Z", attrs: map[string]string{"package": "libclass-method-modifiers-perl", "version": "2.15-1"}},
		{level: "info", at: "2026-09-02T00:46:55Z"},
		{event: "configure", level: "info", at: "2026-09-02T00:46:56Z", attrs: map[string]string{"package": "libdynaloader-functions-perl", "version": "0.004-1"}},
		{level: "info", at: "2026-09-02T00:46:56Z"},
		{level: "info", at: "2026-09-02T00:46:56Z"},
		{event: "upgrade", level: "info", at: "2026-09-05T19:34:18Z", attrs: map[string]string{"package": "docker-ce-cli", "version": "5:29.8.0-1~ubuntu.24.04~noble", "old_version": "5:29.7.2-1~ubuntu.24.04~noble"}},
		{level: "info", at: "2026-09-05T19:34:18Z"},
		{event: "remove", level: "info", at: "2026-09-26T07:00:01Z", attrs: map[string]string{"package": "nginx", "version": "1.26.3-2ubuntu1.2"}},
		{event: "purge", level: "info", at: "2026-09-26T07:00:02Z", attrs: map[string]string{"package": "nginx-common", "version": "1.26.3-2ubuntu1.2"}},
	})
}

// apt's term.log, and unattended-upgrades-dpkg.log which is the same thing
// (this host's, from its January kernel update), filed under the session's
// "Log started" time.
func TestLensPackagesTerm(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "packages", []string{
		"Log started: 2026-01-07  06:56:35",
		"Preparing to unpack .../bpftool_7.6.0+6.14.0-37.37_amd64.deb ...",
		"Unpacking bpftool (7.6.0+6.14.0-37.37) over (7.6.0+6.14.0-35.35) ...",
		"Selecting previously unselected package libclass-method-modifiers-perl.",
		"Unpacking libclass-method-modifiers-perl (2.15-1) ...",
		"Setting up bpftool (7.6.0+6.14.0-37.37) ...",
		"Processing triggers for man-db (2.13.0-1) ...",
		// A maintainer script's own sentence is not a removal.
		"Removing obsolete dictionary files:",
		// dpkg's removal and failure sentences; this host's term.log has none.
		"Removing nginx (1.26.3-2ubuntu1.2) ...",
		"Purging configuration files for nginx (1.26.3-2ubuntu1.2) ...",
		"dpkg: error processing package nginx (--configure):",
		"E: Sub-process /usr/bin/dpkg returned an error code (1)",
		"Log ended: 2026-01-07  06:56:39",
	}, []appLensWant{
		{event: "transaction_start", level: "info", at: "2026-01-07T03:56:35Z"},
		{at: "2026-01-07T03:56:35Z"},
		{event: "upgrade", level: "info", at: "2026-01-07T03:56:35Z", attrs: map[string]string{"package": "bpftool", "version": "7.6.0+6.14.0-37.37", "old_version": "7.6.0+6.14.0-35.35"}},
		{at: "2026-01-07T03:56:35Z"},
		{event: "install", level: "info", at: "2026-01-07T03:56:35Z", attrs: map[string]string{"package": "libclass-method-modifiers-perl", "version": "2.15-1"}},
		{event: "configure", level: "info", at: "2026-01-07T03:56:35Z", attrs: map[string]string{"package": "bpftool", "version": "7.6.0+6.14.0-37.37"}},
		{at: "2026-01-07T03:56:35Z"},
		{at: "2026-01-07T03:56:35Z"},
		{event: "remove", level: "info", at: "2026-01-07T03:56:35Z", attrs: map[string]string{"package": "nginx", "version": "1.26.3-2ubuntu1.2"}},
		{event: "purge", level: "info", at: "2026-01-07T03:56:35Z", attrs: map[string]string{"package": "nginx", "version": "1.26.3-2ubuntu1.2"}},
		{event: "error", level: "error", at: "2026-01-07T03:56:35Z", attrs: map[string]string{"package": "nginx"}},
		{event: "error", level: "error", at: "2026-01-07T03:56:35Z"},
		{event: "transaction_end", level: "info", at: "2026-01-07T03:56:39Z", attrs: map[string]string{"duration_ms": "4000"}},
	})
}

// unattended-upgrades.log on this host: a run with nothing to do, and the
// July run that failed because the disk was full.
func TestLensPackagesUnattended(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "packages", []string{
		"2026-09-01 06:33:15,454 WARNING Could not figure out development release: Distribution data outdated. Please check for an update for distro-info-data. See /usr/share/doc/distro-info-data/README.Debian for details.",
		"2026-09-01 06:33:15,455 INFO Starting unattended upgrades script",
		"2026-09-01 06:33:15,456 INFO Allowed origins are: o=Ubuntu,a=plucky, o=Ubuntu,a=plucky-security, o=UbuntuESMApps,a=plucky-apps-security, o=UbuntuESM,a=plucky-infra-security",
		"2026-09-01 06:33:16,526 INFO No packages found that can be upgraded unattended and no pending auto-removals",
		"2026-07-03 01:46:44,576 ERROR Apt returned an error, exiting",
		"2026-07-03 01:46:44,576 ERROR error message: E:Write error - write (28: No space left on device), E:IO Error saving source cache, E:The package lists or status file could not be parsed or opened.",
		// A run that upgrades, in unattended-upgrades' own words.
		"2026-01-07 06:56:34,512 INFO Packages that will be upgraded: libpng16-16t64 linux-libc-dev",
		"2026-01-07 06:57:14,101 INFO All upgrades installed",
	}, []appLensWant{
		{level: "warn", at: "2026-09-01T03:33:15.454Z"},
		{event: "unattended_run", level: "info", at: "2026-09-01T03:33:15.455Z"},
		{level: "info", at: "2026-09-01T03:33:15.456Z"},
		{event: "unattended_done", level: "info", at: "2026-09-01T03:33:16.526Z"},
		{event: "error", level: "error", at: "2026-07-02T22:46:44.576Z"},
		{event: "error", level: "error", at: "2026-07-02T22:46:44.576Z"},
		{event: "upgrade", level: "info", at: "2026-01-07T03:56:34.512Z", attrs: map[string]string{"packages": "2", "package": "libpng16-16t64, linux-libc-dev"}},
		{event: "unattended_done", level: "info", at: "2026-01-07T03:57:14.101Z"},
	})
}

// dnf's logs, in the format dnf 4 writes them: dnf.log names the command,
// dnf.rpm.log each package. There is no dnf on this host.
func TestLensPackagesDnf(t *testing.T) {
	appLensCheck(t, "packages", []string{
		"2026-09-26T10:00:00+0000 DDEBUG Command: dnf install -y nginx",
		"2026-09-26T10:00:01+0000 ERROR Error: Unable to find a match: ngnix",
		"2026-09-26T10:00:05+0000 SUBDEBUG Installed: nginx-1:1.26.3-1.fc41.x86_64",
		"2026-09-26T10:00:06+0000 SUBDEBUG Upgrade: openssl-libs-1:3.2.2-9.fc41.x86_64",
		"2026-09-26T10:00:06+0000 SUBDEBUG Upgraded: openssl-libs-1:3.2.2-8.fc41.x86_64",
		"2026-09-26T10:00:07+0000 SUBDEBUG Erase: nano-8.1-1.fc41.x86_64",
	}, []appLensWant{
		{event: "command", level: "debug", at: "2026-09-26T10:00:00Z", attrs: map[string]string{"command": "dnf install -y nginx"}},
		{event: "error", level: "error", at: "2026-09-26T10:00:01Z", attrs: map[string]string{"command": "dnf install -y nginx"}},
		{event: "install", level: "debug", at: "2026-09-26T10:00:05Z", attrs: map[string]string{"package": "nginx", "version": "1:1.26.3-1.fc41"}},
		{event: "upgrade", level: "debug", at: "2026-09-26T10:00:06Z", attrs: map[string]string{"package": "openssl-libs", "version": "1:3.2.2-9.fc41"}},
		{level: "debug", at: "2026-09-26T10:00:06Z"},
		{event: "remove", level: "debug", at: "2026-09-26T10:00:07Z", attrs: map[string]string{"package": "nano", "version": "8.1-1.fc41"}},
	})
}

func BenchmarkLensPackages(b *testing.B) {
	appLensBenchmark(b, "packages", []string{
		"2026-09-02 03:46:55 install libclass-method-modifiers-perl:all <none> 2.15-1",
		"2026-09-02 03:46:55 status half-installed libclass-method-modifiers-perl:all 2.15-1",
		"2026-09-02 03:46:55 status unpacked libclass-method-modifiers-perl:all 2.15-1",
		"2026-09-02 03:46:56 configure libdynaloader-functions-perl:all 0.004-1 <none>",
		"2026-09-01 06:33:15,456 INFO Allowed origins are: o=Ubuntu,a=plucky, o=Ubuntu,a=plucky-security",
		"Preparing to unpack .../bpftool_7.6.0+6.14.0-37.37_amd64.deb ...",
		"Unpacking bpftool (7.6.0+6.14.0-37.37) over (7.6.0+6.14.0-35.35) ...",
		"Setting up bpftool (7.6.0+6.14.0-37.37) ...",
	})
}
