package updates

import (
	"strings"
	"testing"
)

// Each parser reads a real command's real output. The samples are the shapes
// those tools actually print, including the parts that look like data and are
// not — a heading row, an "Obsoleting" section, an indented continuation.

func TestParseDNFCheckUpdate(t *testing.T) {
	out := strings.Join([]string{
		"",
		"kernel.x86_64                    5.14.0-503.el9         baseos",
		"openssl.x86_64                   1:3.0.7-27.el9_3       appstream",
		"openssl-libs.x86_64              1:3.0.7-27.el9_3       appstream",
		"",
		"Obsoleting Packages",
		"grub2-tools.x86_64               1:2.06-70.el9          baseos",
		"    grub2-tools-minimal.x86_64   1:2.06-61.el9          @System",
	}, "\n")
	got := parseDNFCheckUpdate(out)
	if len(got) != 4 {
		t.Fatalf("got %d packages: %+v", len(got), got)
	}
	if got[0].Name != "kernel" || got[0].Architecture != "x86_64" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Candidate != "1:3.0.7-27.el9_3" || got[1].Origin != "appstream" {
		t.Errorf("second = %+v", got[1])
	}
	// The indented line under Obsoleting describes what is being replaced, not
	// a fifth thing to upgrade.
	for _, p := range got {
		if p.Name == "grub2-tools-minimal" {
			t.Error("read an obsoleted package as an upgrade")
		}
	}
}

func TestParseDNFSecurity(t *testing.T) {
	out := strings.Join([]string{
		"RHSA-2024:1234 Important/Sec. openssl-1:3.0.7-27.el9_3.x86_64",
		"RHSA-2024:1235 Moderate/Sec.  kernel-5.14.0-503.el9.x86_64",
		"Last metadata expiration check: 0:12:01 ago.",
	}, "\n")
	names := parseDNFSecurity(out)
	if !names["openssl"] || !names["kernel"] {
		t.Fatalf("got %v", names)
	}
}

// An RPM identifier has no separator between the name and the version, so
// this is the one place a heuristic is unavoidable — and getting it wrong
// marks the wrong packages as security updates.
func TestNameFromNEVRA(t *testing.T) {
	cases := map[string]string{
		"openssl-1:3.0.7-27.el9_3.x86_64":             "openssl",
		"kernel-5.14.0-503.el9.x86_64":                "kernel",
		"python3-dnf-plugins-core-4.3.0-1.el9.noarch": "python3-dnf-plugins-core",
		"nss-softokn-freebl-3.90.0-6.el9.x86_64":      "nss-softokn-freebl",
	}
	for nevra, want := range cases {
		if got := nameFromNEVRA(nevra); got != want {
			t.Errorf("%s = %q, want %q", nevra, got, want)
		}
	}
}

func TestParseZypperUpdates(t *testing.T) {
	out := strings.Join([]string{
		"S | Repository          | Name    | Current Version | Available Version | Arch",
		"--+---------------------+---------+-----------------+-------------------+-------",
		"v | Update repository   | openssl | 3.0.8-150500.1  | 3.0.8-150500.2    | x86_64",
		"v | Update repository   | curl    | 8.0.1-150500.1  | 8.0.1-150500.3    | x86_64",
	}, "\n")
	got := parseZypperUpdates(out)
	if len(got) != 2 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].Name != "openssl" || got[0].Current != "3.0.8-150500.1" ||
		got[0].Candidate != "3.0.8-150500.2" || got[0].Architecture != "x86_64" {
		t.Fatalf("first = %+v", got[0])
	}
}

func TestParsePacmanUpdates(t *testing.T) {
	out := strings.Join([]string{
		"linux 6.10.6.arch1-1 -> 6.10.7.arch1-1",
		"openssl 3.3.1-1 -> 3.3.2-1",
		":: Starting full system upgrade...",
	}, "\n")
	got := parsePacmanUpdates(out)
	if len(got) != 2 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[1].Name != "openssl" || got[1].Current != "3.3.1-1" || got[1].Candidate != "3.3.2-1" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestParseApkVersions(t *testing.T) {
	out := strings.Join([]string{
		"Installed:                Available:",
		"busybox-1.36.1-r5       < 1.36.1-r7",
		"ca-certificates-20240226-r0 < 20240705-r0",
		"musl-1.2.5-r0           = 1.2.5-r0",
	}, "\n")
	got := parseApkVersions(out)
	if len(got) != 2 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].Name != "busybox" || got[0].Current != "1.36.1-r5" || got[0].Candidate != "1.36.1-r7" {
		t.Fatalf("first = %+v", got[0])
	}
	// A package at the same version is not an upgrade, and apk prints it in
	// the same table.
	for _, p := range got {
		if p.Name == "musl" {
			t.Error("an equal version was read as an upgrade")
		}
	}
}

// A dash inside the name is the case that breaks a naive split.
func TestSplitApkVersion(t *testing.T) {
	cases := map[string][2]string{
		"busybox-1.36.1-r5":           {"busybox", "1.36.1-r5"},
		"ca-certificates-20240226-r0": {"ca-certificates", "20240226-r0"},
		"py3-setuptools-68.0.0-r0":    {"py3-setuptools", "68.0.0-r0"},
	}
	for id, want := range cases {
		name, version := splitApkVersion(id)
		if name != want[0] || version != want[1] {
			t.Errorf("%s = %q/%q, want %q/%q", id, name, version, want[0], want[1])
		}
	}
}

// Offering a security-only upgrade where the manager has no such notion would
// silently apply everything, which is the opposite of what the switch says.
func TestSecurityOnlySupportIsDeclaredHonestly(t *testing.T) {
	cases := map[manager]bool{
		aptManager{}:              true,
		dnfManager{binary: "dnf"}: true,
		zypperManager{}:           true,
		pacmanManager{}:           false,
		apkManager{}:              false,
	}
	for m, want := range cases {
		if m.SupportsSecurityOnly() != want {
			t.Errorf("%s = %v, want %v", m.Name(), m.SupportsSecurityOnly(), want)
		}
	}
}

// The argv is the contract now that a job runs it, so it is read back rather
// than trusted — and the environment matters as much: an apt run without
// NEEDRESTART_MODE opens a full-screen prompt and never returns.
func TestUpgradeCommands(t *testing.T) {
	name, args, env := aptManager{}.UpgradeCommand(false)
	if name != "apt-get" {
		t.Fatalf("name = %q", name)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-y") || !strings.Contains(joined, "upgrade") {
		t.Errorf("apt args = %q", joined)
	}
	envJoined := strings.Join(env, " ")
	for _, want := range []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"} {
		if !strings.Contains(envJoined, want) {
			t.Errorf("missing %q from the environment %q", want, envJoined)
		}
	}

	// Security-only is an explicit target list, never a suite preference.
	_, secArgs, _ := aptManager{}.UpgradeCommand(true)
	if !strings.Contains(strings.Join(secArgs, " "), "--only-upgrade --no-remove") || secArgs[len(secArgs)-1] != "install" {
		t.Errorf("security-only apt should require explicit install targets: %q", secArgs)
	}

	_, dnfArgs, _ := dnfManager{binary: "dnf"}.UpgradeCommand(true)
	if !strings.Contains(strings.Join(dnfArgs, " "), "--security") {
		t.Errorf("dnf security args = %q", dnfArgs)
	}

	// Arch cannot do a partial upgrade, so both forms are the same full -Syu.
	_, pacFull, _ := pacmanManager{}.UpgradeCommand(false)
	_, pacSec, _ := pacmanManager{}.UpgradeCommand(true)
	if strings.Join(pacFull, " ") != strings.Join(pacSec, " ") {
		t.Errorf("pacman should not pretend to narrow: %q vs %q", pacFull, pacSec)
	}
	if !strings.Contains(strings.Join(pacFull, " "), "-Syu") {
		t.Errorf("pacman args = %q", pacFull)
	}

	// Every manager must produce something runnable.
	for _, m := range managers() {
		n, a, _ := m.UpgradeCommand(false)
		if n == "" || len(a) == 0 {
			t.Errorf("%s produced no command", m.Name())
		}
	}
}

func TestGuardSecurityOnly(t *testing.T) {
	if err := guardSecurityOnly(aptManager{}, true); err != nil {
		t.Errorf("apt can narrow to security and was refused: %v", err)
	}
	if err := guardSecurityOnly(pacmanManager{}, true); err == nil {
		t.Error("pacman has no security pocket, so the narrowed upgrade must be refused rather than silently widened")
	}
	if err := guardSecurityOnly(pacmanManager{}, false); err != nil {
		t.Errorf("an ordinary upgrade was refused: %v", err)
	}
}

// Every manager has to name itself, or the Updates page has nothing to show
// for "what runs this host".
func TestEveryManagerIsNamed(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range managers() {
		if m.Name() == "" {
			t.Fatalf("%T has no name", m)
		}
		if seen[m.Name()] {
			t.Fatalf("two managers called %q", m.Name())
		}
		seen[m.Name()] = true
	}
	if len(seen) < 5 {
		t.Fatalf("only %d managers registered", len(seen))
	}
}
