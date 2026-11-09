package procs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeProc lays out the files of one process the way /proc does, under a
// temporary root, with init's namespaces beside it.
func fakeProc(t *testing.T, pid string, files map[string]string, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, pid, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range links {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const nginxStatus = `Name:	nginx
Umask:	0022
State:	S (sleeping)
Uid:	33	33	33	33
Gid:	33	33	33	33
Groups:	33 4
NSpid:	812	1
VmPeak:	  60000 kB
VmSize:	  58000 kB
VmLck:	      0 kB
VmHWM:	  12000 kB
VmRSS:	  10000 kB
RssAnon:	   4000 kB
RssFile:	   5000 kB
RssShmem:	   1000 kB
VmData:	   3000 kB
VmStk:	    132 kB
VmExe:	   1200 kB
VmLib:	   8000 kB
VmPTE:	    120 kB
VmSwap:	     64 kB
Threads:	2
SigPnd:	0000000000000000
ShdPnd:	0000000000000200
SigBlk:	0000000000000000
SigIgn:	0000000000001000
SigCgt:	0000000188014a07
CapInh:	0000000000000000
CapPrm:	0000000000003400
CapEff:	0000000000003400
CapBnd:	000001ffffffffff
CapAmb:	0000000000000000
NoNewPrivs:	1
Seccomp:	2
Seccomp_filters:	1
Cpus_allowed_list:	0-3,6
voluntary_ctxt_switches:	1500
nonvoluntary_ctxt_switches:	42
`

// The stat line's name holds a space and a parenthesis, which is what the
// fields are counted from the last ')' for.
const nginxStat = "812 (nginx: master) S 1 812 812 34817 812 4194560 900 0 3 0 250 125 10 5 20 0 2 0 1000 59392000 2500 18446744073709551615 1 1 0 0 0 0 0 4096 402738695 0 0 0 17 2 0 0 7 0 0 0 0 0 0 0 0 0 0\n"

const nginxLimits = `Limit                     Soft Limit           Hard Limit           Units
Max cpu time              unlimited            unlimited            seconds
Max open files            1024                 524288               files
Max processes             63432                63432                processes
`

func TestInspectReadsEverythingAProcessTableHolds(t *testing.T) {
	root := fakeProc(t, "812", map[string]string{
		"cmdline":       "nginx: master process\x00-g\x00daemon off;\x00",
		"status":        nginxStatus,
		"stat":          nginxStat,
		"wchan":         "do_epoll_wait",
		"oom_score":     "667\n",
		"oom_score_adj": "-500\n",
		"io":            "rchar: 100\nwchar: 200\nsyscr: 3\nsyscw: 4\nread_bytes: 4096\nwrite_bytes: 8192\ncancelled_write_bytes: 0\n",
		"smaps_rollup":  "00400000-7ffd rw-p 00000000 00:00 0 [rollup]\nRss: 10000 kB\nPss: 6000 kB\nPrivate_Clean: 1000 kB\nPrivate_Dirty: 3000 kB\n",
		"limits":        nginxLimits,
		"environ":       "PATH=/usr/bin\x00DB_PASSWORD=hunter2\x00",
		"cgroup":        "0::/system.slice/nginx.service\n",
		"attr/current":  "nginx (enforce)\n",
		"maps": "55e0 r-xp 0 08:01 1 /usr/sbin/nginx\n" +
			"7f00 r-xp 0 08:01 2 /usr/lib/x86_64-linux-gnu/libssl.so.3\n" +
			"7f01 r-xp 0 08:01 2 /usr/lib/x86_64-linux-gnu/libssl.so.3\n" +
			"7f02 r-xp 0 08:01 3 /usr/lib/x86_64-linux-gnu/libpcre2-8.so.0.11.2 (deleted)\n" +
			"7f03 rw-p 0 00:00 0 [heap]\n",
		"task/812/stat": nginxStat,
		"task/813/stat": "813 (nginx worker) R 1 812 812 0 -1 0 0 0 0 0 900 100 0 0 20 0 2 0 1000 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 3 0 0 0 0\n",
	}, map[string]string{
		"812/exe":     "/usr/sbin/nginx (deleted)",
		"812/root":    "/",
		"812/fd/0":    "/dev/null",
		"812/fd/3":    "socket:[1234]",
		"812/fd/4":    "pipe:[99]",
		"812/fd/5":    "/var/log/nginx/access.log",
		"812/fd/6":    "/var/log/nginx/old.log (deleted)",
		"812/fd/7":    "anon_inode:[eventpoll]",
		"812/ns/net":  "net:[4026532000]",
		"812/ns/pid":  "pid:[4026531836]",
		"1/ns/net":    "net:[4026531840]",
		"1/ns/pid":    "pid:[4026531836]",
		"1/ns/mnt":    "mnt:[1]",
		"812/ns/mnt":  "mnt:[1]",
		"812/ns/user": "user:[4026531837]",
	})
	names := map[uint32]string{33: "www-data", 4: "adm"}
	lookup := func(id uint32) string { return names[id] }
	got := InspectAt(root, 812, lookup, lookup)

	if !slices.Equal(got.Argv, []string{"nginx: master process", "-g", "daemon off;"}) {
		t.Errorf("argv = %q", got.Argv)
	}
	if got.Exe != "/usr/sbin/nginx" || !got.ExeDeleted {
		t.Errorf("exe = %q deleted %v, want the replaced binary", got.Exe, got.ExeDeleted)
	}
	if got.WaitChannel != "do_epoll_wait" || got.NamespacePID != 1 || got.Root != "/" {
		t.Errorf("wchan %q, ns pid %d, root %q", got.WaitChannel, got.NamespacePID, got.Root)
	}

	id := got.Identity
	if !id.Ready || id.User != (InspectID{33, "www-data"}) || id.EffectiveGroup.Name != "www-data" {
		t.Errorf("identity = %+v", id)
	}
	if len(id.Groups) != 2 || id.Groups[1].Name != "adm" {
		t.Errorf("groups = %+v", id.Groups)
	}
	if id.Terminal != "pts/1" || !id.Foreground || id.Session != 812 || id.ProcessGroup != 812 {
		t.Errorf("terminal %q foreground %v session %d group %d", id.Terminal, id.Foreground, id.Session, id.ProcessGroup)
	}

	s := got.Scheduling
	if s.Policy != "normal" || s.CPUs != "0-3,6" || s.CPUCount != 5 || s.LastCPU != 2 {
		t.Errorf("scheduling = %+v", s)
	}
	if s.UserSeconds != 2.5 || s.SystemSeconds != 1.25 || s.IOWaitSeconds != 0.07 {
		t.Errorf("times = %v %v %v", s.UserSeconds, s.SystemSeconds, s.IOWaitSeconds)
	}
	if s.VoluntarySwitches != 1500 || s.InvoluntarySwitches != 42 || s.MinorFaults != 900 || s.MajorFaults != 3 {
		t.Errorf("counters = %+v", s)
	}

	m := got.Memory
	if !m.Ready || m.Resident != 10000*1024 || m.Anonymous != 4000*1024 || m.Peak != 12000*1024 || m.Swap != 64*1024 {
		t.Errorf("memory = %+v", m)
	}
	if !m.RollupReady || m.Proportional != 6000*1024 || m.Unique != 4000*1024 {
		t.Errorf("rollup = pss %d uss %d", m.Proportional, m.Unique)
	}
	if !m.OOMReady || m.OOMScore != 667 || m.OOMScoreAdj != -500 {
		t.Errorf("oom = %d %d", m.OOMScore, m.OOMScoreAdj)
	}
	if !got.IO.Ready || got.IO.CharsRead != 100 || got.IO.WriteBytes != 8192 {
		t.Errorf("io = %+v", got.IO)
	}

	sec := got.Security
	if !slices.Equal(sec.Capabilities, []string{"CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN", "CAP_NET_RAW"}) || sec.AllCapabilities {
		t.Errorf("capabilities = %v all %v", sec.Capabilities, sec.AllCapabilities)
	}
	if !sec.BoundingAll || !sec.NoNewPrivs || sec.Seccomp != "filter" || sec.SeccompFilters != 1 || sec.Label != "nginx (enforce)" {
		t.Errorf("security = %+v", sec)
	}

	sig := got.Signals
	if !slices.Contains(sig.Caught, "SIGHUP") || !slices.Contains(sig.Caught, "SIGQUIT") || slices.Contains(sig.Caught, "SIGKILL") {
		t.Errorf("caught = %v", sig.Caught)
	}
	if !slices.Equal(sig.Ignored, []string{"SIGPIPE"}) || !slices.Equal(sig.Pending, []string{"SIGUSR1"}) {
		t.Errorf("ignored %v pending %v", sig.Ignored, sig.Pending)
	}

	own := map[string]bool{}
	for _, ns := range got.Namespaces {
		own[ns.Kind] = ns.Own
	}
	if !own["net"] || own["pid"] || own["mnt"] {
		t.Errorf("namespaces = %+v, want only its network its own", got.Namespaces)
	}
	if got.Cgroup.Path != "/system.slice/nginx.service" {
		t.Errorf("cgroup = %+v", got.Cgroup)
	}

	if len(got.Limits) != 3 || got.Limits[1] != (InspectLimit{"Max open files", "1024", "524288", "files"}) {
		t.Errorf("limits = %+v", got.Limits)
	}

	f := got.Files
	if !f.Ready || f.Total != 6 || f.Files != 2 || f.Sockets != 1 || f.Pipes != 1 || f.Devices != 1 || f.Other != 1 || f.Deleted != 1 {
		t.Errorf("files = %+v", f)
	}
	if f.Entries[0].FD != 0 || f.Entries[4].Target != "/var/log/nginx/old.log" || !f.Entries[4].Deleted {
		t.Errorf("entries = %+v", f.Entries)
	}

	if len(got.Libraries) != 2 || !got.Libraries[0].Deleted || !strings.HasSuffix(got.Libraries[0].Path, "libpcre2-8.so.0.11.2") {
		t.Errorf("libraries = %+v, want each once and the replaced one first", got.Libraries)
	}
	if !got.EnvironmentReadable || !slices.Equal(got.Environment, []string{"DB_PASSWORD=hunter2", "PATH=/usr/bin"}) {
		t.Errorf("environment = %v", got.Environment)
	}

	if got.ThreadsTotal != 2 || got.Threads[0].Name != "nginx worker" || got.Threads[0].State != "running" || got.Threads[0].CPUSeconds != 10 {
		t.Errorf("threads = %+v, want the busiest first", got.Threads)
	}
}

// A process this account cannot read is not an error: every block says it
// is not ready, and every list is a list rather than null.
func TestInspectOfAnUnreadableProcessIsEmptyNotNull(t *testing.T) {
	got := InspectAt(t.TempDir(), 4242, func(uint32) string { return "" }, func(uint32) string { return "" })
	if got.Identity.Ready || got.Memory.Ready || got.Files.Ready || got.Signals.Ready || got.EnvironmentReadable {
		t.Fatalf("unreadable process reads as ready: %+v", got)
	}
	if got.Argv == nil || got.Limits == nil || got.Files.Entries == nil || got.Threads == nil ||
		got.Environment == nil || got.Signals.Caught == nil || got.Security.Capabilities == nil {
		t.Fatalf("a list came back null: %+v", got)
	}
}

func TestTerminalNamesDecodeTtyNr(t *testing.T) {
	for nr, want := range map[int64]string{0: "", 34816: "pts/0", 34823: "pts/7", 1025: "tty1", 1088: "ttyS0"} {
		if got := terminalName(nr); got != want {
			t.Errorf("terminalName(%d) = %q, want %q", nr, got, want)
		}
	}
}

func TestSignalMasksDecodeToNames(t *testing.T) {
	if got := signalNames("0000000000004001"); !slices.Equal(got, []string{"SIGHUP", "SIGTERM"}) {
		t.Errorf("signals = %v", got)
	}
	if got := signalNames("0000000400000000"); !slices.Equal(got, []string{"SIGRTMIN+1"}) {
		t.Errorf("realtime = %v", got)
	}
	if names, all := capabilityNames("000001ffffffffff"); !all || len(names) != len(capabilityList) {
		t.Errorf("root's set = %d names, all %v", len(names), all)
	}
	if got := cpuListCount("0-7,12,14-15"); got != 11 {
		t.Errorf("cpu count = %d", got)
	}
}

func TestDescendantsWalkTheWholeTreeOnce(t *testing.T) {
	children := map[int32][]int32{10: {11, 12}, 11: {13}, 13: {10}, 12: {}}
	if got := descendantsOf(children, 10); !slices.Equal(got, []int32{11, 12, 13}) {
		t.Fatalf("descendants = %v, want nearest first and the cycle cut", got)
	}
}
