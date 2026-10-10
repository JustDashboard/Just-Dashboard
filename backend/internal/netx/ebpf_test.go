package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withBPFStats(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bpf_stats_enabled")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prev := bpfStatsPath
	bpfStatsPath = path
	t.Cleanup(func() { bpfStatsPath = prev })
}

// The programs systemd loads on any recent host, as bpftool prints them.
func TestEBPFReadsTheRealListing(t *testing.T) {
	withBPFStats(t, "0\n")
	rec := record(t)
	rec.on("bpftool -j prog show", fixture(t, "ebpf-prog.json")).
		on("bpftool -j net show", fixture(t, "ebpf-net.json")).
		on("bpftool -j cgroup tree", fixture(t, "ebpf-cgroup-tree.json"))
	v, err := New(Options{}).EBPF(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.Installed || v.BPFStatsEnabled || v.Error != "" || v.Package != "" {
		t.Fatalf("view = %+v", v)
	}
	if v.Total != 12 || len(v.Programs) != 12 {
		t.Fatalf("%d programs", v.Total)
	}
	for i := 1; i < len(v.Programs); i++ {
		if v.Programs[i].ID <= v.Programs[i-1].ID {
			t.Fatalf("not ordered by id: %v", v.Programs)
		}
	}
	if len(v.ByType) != 3 || v.ByType[0].Type != "cgroup_skb" || v.ByType[0].Count != 8 || v.ByType[1].Count != 3 {
		t.Fatalf("by type = %+v", v.ByType)
	}
	var sysctl *EBPFProgram
	for i, p := range v.Programs {
		if p.Name == "sysctl_monitor" {
			sysctl = &v.Programs[i]
		}
	}
	if sysctl == nil || sysctl.Type != "cgroup_sysctl" || sysctl.Tag != "78dcac0637f56d1c" || sysctl.BytesXlated != 1232 ||
		sysctl.BytesJited != 781 || sysctl.Memlock != 4096 || len(sysctl.MapIDs) != 3 || sysctl.LoadedAt == nil ||
		sysctl.RunTimeNs != nil || sysctl.RunCount != nil {
		t.Fatalf("sysctl_monitor = %+v", sysctl)
	}
	if len(v.Attachments) != 0 || v.Attachments == nil {
		t.Fatalf("attachments = %v", v.Attachments)
	}
}

// With kernel.bpf_stats_enabled the programs carry run time and count, and
// the attachments name what is on each device.
func TestEBPFStatsAndAttachments(t *testing.T) {
	withBPFStats(t, "1\n")
	rec := record(t)
	rec.on("bpftool -j prog show", fixture(t, "ebpf-prog-stats.json")).
		on("bpftool -j net show", fixture(t, "ebpf-net-attached.json")).
		fail("bpftool -j cgroup tree", "Error: nope")
	v, err := New(Options{}).EBPF(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.BPFStatsEnabled || len(v.Programs) != 3 {
		t.Fatalf("view = %+v", v)
	}
	xdp := v.Programs[0]
	if xdp.Name != "xdp_drop_icmp" || xdp.RunTimeNs == nil || *xdp.RunTimeNs != 912345678 || *xdp.RunCount != 4410021 ||
		len(xdp.Pinned) != 1 || len(xdp.Owners) != 1 || xdp.Owners[0] != "edge-agent (2201)" {
		t.Fatalf("xdp = %+v", xdp)
	}
	// loaded_at as a string and memlock under its older key.
	tcIn := v.Programs[1]
	if tcIn.LoadedAt == nil || tcIn.LoadedAt.Unix() != 1790000100 || tcIn.Memlock != 4096 || tcIn.RunCount == nil || *tcIn.RunCount != 0 {
		t.Fatalf("tc ingress program = %+v", tcIn)
	}
	if v.Programs[2].MapIDs[0] != 7 {
		t.Fatalf("maps = %v", v.Programs[2].MapIDs)
	}

	want := []EBPFAttachment{
		{Device: "eth0", Ifindex: 2, Kind: "tc egress", ProgramID: 73, Name: "tc_egress_count"},
		{Device: "eth0", Ifindex: 2, Kind: "tc ingress", ProgramID: 72, Name: "tc_ingress_count"},
		{Device: "eth0", Ifindex: 2, Kind: "xdp", ProgramID: 71, Name: "xdp_drop_icmp", Mode: "driver"},
		{Device: "wg0", Ifindex: 9, Kind: "xdp", ProgramID: 71, Name: "xdp_drop_icmp", Mode: "generic"},
	}
	if len(v.Attachments) != len(want) {
		t.Fatalf("attachments = %+v", v.Attachments)
	}
	for i, w := range want {
		if v.Attachments[i] != w {
			t.Errorf("attachment %d = %+v, want %+v", i, v.Attachments[i], w)
		}
	}
}

func TestEBPFWithoutBpftool(t *testing.T) {
	rec := record(t, "bpftool")
	v, err := New(Options{}).EBPF(context.Background())
	if err != nil || v.Installed || v.Package != "bpftool" || v.Programs == nil || v.Attachments == nil {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if len(rec.commands()) != 0 {
		t.Fatalf("ran %v without the tool", rec.commands())
	}
}

func TestEBPFReportsWhatItCouldNotRead(t *testing.T) {
	withBPFStats(t, "")
	record(t).fail("bpftool -j prog show", `[{"error":"can't get next program: Operation not permitted"}]`)
	v, err := New(Options{}).EBPF(context.Background())
	if err != nil || !v.Installed || v.Error != "bpftool: can't get next program: Operation not permitted" || len(v.Programs) != 0 {
		t.Fatalf("view = %+v, %v", v, err)
	}

	// Programs read, attachments not: the programs stay.
	rec := record(t)
	rec.on("bpftool -j prog show", fixture(t, "ebpf-prog.json")).fail("bpftool -j net show", "Error: nope")
	v, _ = New(Options{}).EBPF(context.Background())
	if len(v.Programs) == 0 || v.Error == "" {
		t.Fatalf("view = %+v", v)
	}

	record(t).on("bpftool -j prog show", "null\n").on("bpftool -j net show", "[]\n").on("bpftool -j cgroup tree", "null\n")
	v, _ = New(Options{}).EBPF(context.Background())
	if v.Error != "" || v.Total != 0 || v.Programs == nil {
		t.Fatalf("no programs = %+v", v)
	}

	record(t).on("bpftool -j prog show", "not json")
	v, _ = New(Options{}).EBPF(context.Background())
	if v.Error == "" {
		t.Fatal("unreadable output reported no error")
	}
}

func withBPFRoot(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prev := bpfRoot
	bpfRoot = root
	t.Cleanup(func() { bpfRoot = prev })
}

// The platform is read from the kernel's own files, so a host without
// bpftool still says what it offers eBPF.
func TestEBPFPlatformAndCgroupAttachments(t *testing.T) {
	withBPFStats(t, "0\n")
	withBPFRoot(t, map[string]string{
		"proc/sys/kernel/osrelease":                 "6.14.0-37-generic\n",
		"proc/sys/net/core/bpf_jit_enable":          "1\n",
		"proc/sys/net/core/bpf_jit_harden":          "0\n",
		"proc/sys/kernel/unprivileged_bpf_disabled": "2\n",
		"proc/sys/kernel/bpf_stats_enabled":         "0\n",
		"sys/kernel/btf/vmlinux":                    "btf",
		"proc/mounts":                               "proc /proc proc rw 0 0\nbpf /sys/fs/bpf bpf rw,nosuid 0 0\n",
	})
	rec := record(t)
	rec.on("bpftool -j prog show", fixture(t, "ebpf-prog.json")).
		on("bpftool -j net show", fixture(t, "ebpf-net.json")).
		on("bpftool -j cgroup tree", fixture(t, "ebpf-cgroup-tree.json"))
	v, err := New(Options{}).EBPF(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := EBPFPlatform{Kernel: "6.14.0-37-generic", JIT: "1", JITHarden: "0", UnprivilegedDisabled: "2", BTF: true, BPFFS: true}
	if v.Platform != want {
		t.Fatalf("platform = %+v, want %+v", v.Platform, want)
	}
	if v.CgroupAttachments == nil || *v.CgroupAttachments != 4 || v.ObserverProgramIDs == nil {
		t.Fatalf("cgroup attachments = %v, observer = %v", v.CgroupAttachments, v.ObserverProgramIDs)
	}

	// Without bpftool the platform is still there, and nothing ran.
	withBPFRoot(t, map[string]string{"proc/sys/kernel/osrelease": "6.1.0\n"})
	rec = record(t, "bpftool")
	v, _ = New(Options{}).EBPF(context.Background())
	if v.Installed || v.Platform.Kernel != "6.1.0" || v.Platform.BTF || v.Platform.BPFFS || len(rec.commands()) != 0 {
		t.Fatalf("view = %+v, ran %v", v, rec.commands())
	}
}

// A program's detail joins its maps, its cgroup attachments and its links,
// and an average cost only where the kernel counted runs.
func TestEBPFProgramDetail(t *testing.T) {
	rec := record(t)
	rec.on("bpftool -j prog show id 30", fixture(t, "ebpf-prog-detail.json")).
		on("bpftool -j map show id 9", `{"id":9,"type":"cgroup_array","name":"cgroup_map","flags":0,"bytes_key":4,"bytes_value":4,"max_entries":1,"bytes_memlock":272,"frozen":0}`).
		on("bpftool -j map show id 12", `{"id":12,"type":"hash","name":"seen","bytes_key":8,"bytes_value":16,"max_entries":1024,"bytes_memlock":90112,"frozen":1,"pinned":["/sys/fs/bpf/seen"]}`).
		fail("bpftool -j map show id 10", `[{"error":"can't get map by id (10): No such file or directory"}]`).
		on("bpftool -j net show", fixture(t, "ebpf-net.json")).
		on("bpftool -j cgroup tree", fixture(t, "ebpf-cgroup-tree.json")).
		on("bpftool -j link show", fixture(t, "ebpf-link.json"))
	d, err := New(Options{}).EBPFProgram(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "sysctl_monitor" || !d.GPLCompatible || !d.Jited || d.BTFID != 98 || d.VerifiedInsns != 154 {
		t.Fatalf("detail = %+v", d)
	}
	if d.AvgRunNs == nil || *d.AvgRunNs != 2000 {
		t.Fatalf("average run = %v", d.AvgRunNs)
	}
	if len(d.Maps) != 3 || d.Maps[0].Type != "cgroup_array" || d.Maps[1].MaxEntries != 1024 || !d.Maps[1].Frozen ||
		len(d.Maps[1].Pinned) != 1 || d.Maps[2].Error == "" {
		t.Fatalf("maps = %+v", d.Maps)
	}
	if len(d.Cgroups) != 1 || d.Cgroups[0].Cgroup != "/sys/fs/cgroup" || d.Cgroups[0].AttachType != "cgroup_sysctl" {
		t.Fatalf("cgroups = %+v", d.Cgroups)
	}
	if len(d.Links) != 1 || d.Links[0].Type != "cgroup" || d.Links[0].CgroupID != 1 || len(d.Devices) != 0 || len(d.Errors) != 0 {
		t.Fatalf("links = %+v devices = %+v errors = %v", d.Links, d.Devices, d.Errors)
	}
	for _, c := range rec.commands() {
		if strings.Contains(c, "detach") || strings.Contains(c, "unload") || strings.Contains(c, "pin ") {
			t.Fatalf("the detail changed something: %s", c)
		}
	}

	record(t).fail("bpftool -j prog show id 4", `[{"error":"get by id (4): No such file or directory"}]`)
	if _, err := New(Options{}).EBPFProgram(context.Background(), 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a gone program = %v", err)
	}
	if _, err := New(Options{}).EBPFProgram(context.Background(), 0); err == nil {
		t.Fatal("id 0 was read")
	}
	record(t, "bpftool")
	var missing *UnavailableError
	if _, err := New(Options{}).EBPFProgram(context.Background(), 30); !errors.As(err, &missing) {
		t.Fatalf("without bpftool = %v", err)
	}
}
