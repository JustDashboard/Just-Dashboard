package netx

import (
	"context"
	"os"
	"path/filepath"
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
		on("bpftool -j net show", fixture(t, "ebpf-net.json"))
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
		on("bpftool -j net show", fixture(t, "ebpf-net-attached.json"))
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

	record(t).on("bpftool -j prog show", "null\n").on("bpftool -j net show", "[]\n")
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
