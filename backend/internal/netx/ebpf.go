package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The eBPF programs loaded into this kernel, and the ones attached to a
// network device. Read-only: bpftool can detach a program, and nothing on the
// dashboard offers to, because the programs that matter here belong to
// systemd, Cilium, a firewall or a security agent that put them there.

// bpfStatsPath is the switch that makes the kernel time each program. It is
// off by default because it costs a little on every program run, which is why
// run time and run count are absent from most listings.
var bpfStatsPath = "/proc/sys/kernel/bpf_stats_enabled"

// maxBPFPrograms bounds what a host with thousands of loaded programs (a
// service mesh) sends to the page.
const maxBPFPrograms = 1000

// EBPFProgram is one loaded program.
type EBPFProgram struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Tag  string `json:"tag,omitempty"`
	// LoadedAt is when it was loaded, absent where bpftool does not say.
	LoadedAt    *time.Time `json:"loadedAt,omitempty"`
	UID         int        `json:"uid"`
	BytesXlated int64      `json:"bytesXlated"`
	BytesJited  int64      `json:"bytesJited"`
	Memlock     int64      `json:"memlock"`
	MapIDs      []int      `json:"mapIds"`
	// RunTimeNs and RunCount are present only with kernel.bpf_stats_enabled.
	RunTimeNs *int64   `json:"runTimeNs,omitempty"`
	RunCount  *int64   `json:"runCount,omitempty"`
	Pinned    []string `json:"pinned,omitempty"`
	// Owners are the processes holding it, where bpftool names them.
	Owners []string `json:"owners,omitempty"`
}

// EBPFTypeCount is how many programs there are of a type.
type EBPFTypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// EBPFAttachment is a program attached to a device.
type EBPFAttachment struct {
	Device  string `json:"device"`
	Ifindex int    `json:"ifindex"`
	// Kind is xdp, tc ingress or tc egress.
	Kind      string `json:"kind"`
	ProgramID int    `json:"programId"`
	Name      string `json:"name,omitempty"`
	// Mode is how an XDP program is attached: driver, generic or offload.
	Mode string `json:"mode,omitempty"`
}

// EBPFView is the eBPF programs and where they are attached.
type EBPFView struct {
	Installed bool `json:"installed"`
	// Package is what provides bpftool, for the install hand-off.
	Package         string           `json:"package,omitempty"`
	BPFStatsEnabled bool             `json:"bpfStatsEnabled"`
	Programs        []EBPFProgram    `json:"programs"`
	Total           int              `json:"total"`
	ByType          []EBPFTypeCount  `json:"byType"`
	Attachments     []EBPFAttachment `json:"attachments"`
	Error           string           `json:"error,omitempty"`
	// Platform is what the kernel offers eBPF, read without bpftool.
	Platform EBPFPlatform `json:"platform"`
	// CgroupAttachments counts programs attached to cgroups — systemd's
	// per-service firewalls and device filters, a container runtime's, the
	// dashboard's own observer — from bpftool's cgroup tree; nil where it
	// could not be read.
	CgroupAttachments *int `json:"cgroupAttachments,omitempty"`
	// ObserverProgramIDs are the programs this dashboard's kernel observer
	// holds while attached, so the inventory tells them from the host's own.
	ObserverProgramIDs []uint32 `json:"observerProgramIds"`
}

// EBPFPlatform is the kernel's standing for eBPF: its release, whether
// programs are compiled to native code, whether unprivileged users may load
// them, and whether type information (BTF) and the bpf filesystem programs
// pin into are present.
type EBPFPlatform struct {
	Kernel string `json:"kernel,omitempty"`
	// JIT is net.core.bpf_jit_enable (0 off, 1 on, 2 on with debug output);
	// JITHarden is net.core.bpf_jit_harden.
	JIT       string `json:"jit,omitempty"`
	JITHarden string `json:"jitHarden,omitempty"`
	// UnprivilegedDisabled is kernel.unprivileged_bpf_disabled: 0 allows
	// unprivileged loading, 1 and 2 refuse it.
	UnprivilegedDisabled string `json:"unprivilegedDisabled,omitempty"`
	BTF                  bool   `json:"btf"`
	BPFFS                bool   `json:"bpffs"`
	StatsEnabled         bool   `json:"statsEnabled"`
}

// bpfRoot is where the platform is read from; tests point it elsewhere.
var bpfRoot = "/"

func readBPFPlatform() EBPFPlatform {
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(bpfRoot, path))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	p := EBPFPlatform{
		Kernel: read("proc/sys/kernel/osrelease"), JIT: read("proc/sys/net/core/bpf_jit_enable"),
		JITHarden: read("proc/sys/net/core/bpf_jit_harden"), UnprivilegedDisabled: read("proc/sys/kernel/unprivileged_bpf_disabled"),
		StatsEnabled: read("proc/sys/kernel/bpf_stats_enabled") == "1",
	}
	_, err := os.Stat(filepath.Join(bpfRoot, "sys/kernel/btf/vmlinux"))
	p.BTF = err == nil
	for _, line := range strings.Split(read("proc/mounts"), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[2] == "bpf" {
			p.BPFFS = true
		}
	}
	return p
}

// flexInt reads a JSON number or a numeric string: bpftool has printed
// loaded_at both ways across versions.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	*f = flexInt(n)
	return nil
}

type bpfProg struct {
	ID          int      `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Tag         string   `json:"tag"`
	LoadedAt    flexInt  `json:"loaded_at"`
	UID         int      `json:"uid"`
	BytesXlated int64    `json:"bytes_xlated"`
	BytesJited  int64    `json:"bytes_jited"`
	Memlock     int64    `json:"bytes_memlock"`
	MemlockOld  int64    `json:"memlock"`
	MapIDs      []int    `json:"map_ids"`
	RunTimeNs   *int64   `json:"run_time_ns"`
	RunCnt      *int64   `json:"run_cnt"`
	Pinned      []string `json:"pinned"`
	PIDs        []struct {
		PID  int    `json:"pid"`
		Comm string `json:"comm"`
	} `json:"pids"`
}

// EBPF reads the loaded programs and the device attachments.
func (s *Service) EBPF(ctx context.Context) (*EBPFView, error) {
	v := &EBPFView{Programs: []EBPFProgram{}, ByType: []EBPFTypeCount{}, Attachments: []EBPFAttachment{},
		Platform: readBPFPlatform(), ObserverProgramIDs: []uint32{}}
	if !has("bpftool") {
		v.Package = "bpftool"
		return v, nil
	}
	v.Installed = true
	if b, err := os.ReadFile(bpfStatsPath); err == nil {
		v.BPFStatsEnabled = strings.TrimSpace(string(b)) == "1"
	}
	out, err := run(ctx, "bpftool", "-j", "prog", "show")
	if err != nil {
		// Usually the privilege to list programs; the page says so rather
		// than showing a kernel that appears to have none.
		v.Error = bpfMessage(err)
		return v, nil
	}
	progs, err := parseBPFProgs(out)
	if err != nil {
		v.Error = fmt.Sprintf("bpftool printed something this page could not read: %v", err)
		return v, nil
	}
	v.Total = len(progs)
	counts := map[string]int{}
	for _, p := range progs {
		counts[p.Type]++
	}
	for t, n := range counts {
		v.ByType = append(v.ByType, EBPFTypeCount{Type: t, Count: n})
	}
	sort.Slice(v.ByType, func(i, j int) bool {
		if v.ByType[i].Count != v.ByType[j].Count {
			return v.ByType[i].Count > v.ByType[j].Count
		}
		return v.ByType[i].Type < v.ByType[j].Type
	})
	names := map[int]string{}
	for _, p := range progs {
		names[p.ID] = p.Name
	}
	if len(progs) > maxBPFPrograms {
		progs = progs[:maxBPFPrograms]
	}
	v.Programs = progs

	// The attachments are a second command; its failure leaves the programs.
	net, err := run(ctx, "bpftool", "-j", "net", "show")
	if err != nil {
		v.Error = "the device attachments could not be read: " + bpfMessage(err)
		return v, nil
	}
	if v.Attachments, err = parseBPFNet(net, names); err != nil {
		v.Error = fmt.Sprintf("bpftool net printed something this page could not read: %v", err)
		v.Attachments = []EBPFAttachment{}
	}
	// The cgroup tree is a count here and a list in a program's detail; its
	// failure costs the count.
	if tree, err := run(ctx, "bpftool", "-j", "cgroup", "tree"); err == nil {
		if cgroups, err := parseBPFCgroups(tree); err == nil {
			n := len(cgroups)
			v.CgroupAttachments = &n
		}
	}
	return v, nil
}

// EBPFCgroupAttachment is a program attached to a cgroup.
type EBPFCgroupAttachment struct {
	Cgroup     string `json:"cgroup"`
	ProgramID  int    `json:"programId"`
	AttachType string `json:"attachType"`
	Flags      string `json:"flags,omitempty"`
	Name       string `json:"name,omitempty"`
}

// parseBPFCgroups reads `bpftool -j cgroup tree`: one object per cgroup with
// the programs attached to it.
func parseBPFCgroups(out string) ([]EBPFCgroupAttachment, error) {
	out = strings.TrimSpace(out)
	atts := []EBPFCgroupAttachment{}
	if out == "" || out == "null" {
		return atts, nil
	}
	var raw []struct {
		Cgroup   string `json:"cgroup"`
		Programs []struct {
			ID         int    `json:"id"`
			AttachType string `json:"attach_type"`
			Flags      string `json:"attach_flags"`
			Name       string `json:"name"`
		} `json:"programs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	for _, c := range raw {
		for _, p := range c.Programs {
			atts = append(atts, EBPFCgroupAttachment{Cgroup: c.Cgroup, ProgramID: p.ID, AttachType: p.AttachType, Flags: p.Flags, Name: p.Name})
		}
	}
	return atts, nil
}

// EBPFMap is one map a program holds.
type EBPFMap struct {
	ID         int      `json:"id"`
	Type       string   `json:"type"`
	Name       string   `json:"name,omitempty"`
	KeyBytes   int64    `json:"keyBytes"`
	ValueBytes int64    `json:"valueBytes"`
	MaxEntries int64    `json:"maxEntries"`
	Memlock    int64    `json:"memlock"`
	Frozen     bool     `json:"frozen"`
	Pinned     []string `json:"pinned,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// EBPFLink is a bpf link holding a program: the modern attachment, which
// tracing, tcx, netkit and cgroup programs use.
type EBPFLink struct {
	ID         int    `json:"id"`
	Type       string `json:"type"`
	AttachType string `json:"attachType,omitempty"`
	CgroupID   int64  `json:"cgroupId,omitempty"`
	Ifindex    int    `json:"ifindex,omitempty"`
	NetnsIno   int64  `json:"netnsIno,omitempty"`
}

// EBPFProgramDetail is one program in full: what bpftool says of it, the maps
// it holds, every place it is attached, and its average cost per run where
// the kernel keeps run statistics.
type EBPFProgramDetail struct {
	EBPFProgram
	GPLCompatible bool  `json:"gplCompatible"`
	Jited         bool  `json:"jited"`
	BTFID         int   `json:"btfId,omitempty"`
	VerifiedInsns int64 `json:"verifiedInsns,omitempty"`
	// AvgRunNs is run time over run count, present only when both are.
	AvgRunNs *float64 `json:"avgRunNs,omitempty"`
	// Observer is true when this program belongs to the dashboard's own
	// kernel observer (set by the api).
	Observer bool                   `json:"observer"`
	Maps     []EBPFMap              `json:"maps"`
	Devices  []EBPFAttachment       `json:"devices"`
	Cgroups  []EBPFCgroupAttachment `json:"cgroups"`
	Links    []EBPFLink             `json:"links"`
	// Errors are the reads that failed; each costs only its own part.
	Errors []string `json:"errors"`
}

// maxDetailMaps bounds the per-map reads one detail makes.
const maxDetailMaps = 16

// EBPFProgram reads one program's detail. It is read-only like the
// inventory: nothing here detaches or unloads anything.
func (s *Service) EBPFProgram(ctx context.Context, id int) (*EBPFProgramDetail, error) {
	if id <= 0 {
		return nil, errors.New("a program id is a positive number")
	}
	if !has("bpftool") {
		return nil, &UnavailableError{Tool: "bpftool", Package: "bpftool"}
	}
	out, err := run(ctx, "bpftool", "-j", "prog", "show", "id", strconv.Itoa(id))
	if err != nil {
		if strings.Contains(err.Error(), "No such file") || strings.Contains(err.Error(), "get by id") {
			return nil, fmt.Errorf("program %d: %w", id, ErrNotFound)
		}
		return nil, errors.New(bpfMessage(err))
	}
	var raw struct {
		bpfProg
		GPL      bool  `json:"gpl_compatible"`
		Jited    bool  `json:"jited"`
		BTFID    int   `json:"btf_id"`
		Verified int64 `json:"verified_insns"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return nil, fmt.Errorf("bpftool printed something this page could not read: %w", err)
	}
	progs, err := parseBPFProgs("[" + strings.TrimSpace(out) + "]")
	if err != nil || len(progs) != 1 {
		return nil, errors.New("bpftool printed something this page could not read")
	}
	d := &EBPFProgramDetail{EBPFProgram: progs[0], GPLCompatible: raw.GPL, Jited: raw.Jited, BTFID: raw.BTFID,
		VerifiedInsns: raw.Verified, Maps: []EBPFMap{}, Devices: []EBPFAttachment{}, Cgroups: []EBPFCgroupAttachment{},
		Links: []EBPFLink{}, Errors: []string{}}
	if d.RunTimeNs != nil && d.RunCount != nil && *d.RunCount > 0 {
		avg := math.Round(float64(*d.RunTimeNs)/float64(*d.RunCount)*10) / 10
		d.AvgRunNs = &avg
	}
	for i, mid := range d.MapIDs {
		if i >= maxDetailMaps {
			d.Errors = append(d.Errors, fmt.Sprintf("%d more maps are not read", len(d.MapIDs)-maxDetailMaps))
			break
		}
		d.Maps = append(d.Maps, readBPFMap(ctx, mid))
	}
	if net, err := run(ctx, "bpftool", "-j", "net", "show"); err != nil {
		d.Errors = append(d.Errors, "device attachments: "+bpfMessage(err))
	} else if atts, err := parseBPFNet(net, map[int]string{id: d.Name}); err == nil {
		for _, a := range atts {
			if a.ProgramID == id {
				d.Devices = append(d.Devices, a)
			}
		}
	}
	if tree, err := run(ctx, "bpftool", "-j", "cgroup", "tree"); err != nil {
		d.Errors = append(d.Errors, "cgroup attachments: "+bpfMessage(err))
	} else if cgroups, err := parseBPFCgroups(tree); err == nil {
		for _, c := range cgroups {
			if c.ProgramID == id {
				d.Cgroups = append(d.Cgroups, c)
			}
		}
	}
	if links, err := run(ctx, "bpftool", "-j", "link", "show"); err != nil {
		d.Errors = append(d.Errors, "links: "+bpfMessage(err))
	} else {
		var raw []struct {
			ID         int    `json:"id"`
			Type       string `json:"type"`
			ProgID     int    `json:"prog_id"`
			AttachType string `json:"attach_type"`
			CgroupID   int64  `json:"cgroup_id"`
			Ifindex    int    `json:"ifindex"`
			NetnsIno   int64  `json:"netns_ino"`
		}
		if trimmed := strings.TrimSpace(links); trimmed != "" && trimmed != "null" && json.Unmarshal([]byte(trimmed), &raw) == nil {
			for _, l := range raw {
				if l.ProgID == id {
					d.Links = append(d.Links, EBPFLink{ID: l.ID, Type: l.Type, AttachType: l.AttachType, CgroupID: l.CgroupID, Ifindex: l.Ifindex, NetnsIno: l.NetnsIno})
				}
			}
		}
	}
	return d, nil
}

func readBPFMap(ctx context.Context, id int) EBPFMap {
	m := EBPFMap{ID: id}
	out, err := run(ctx, "bpftool", "-j", "map", "show", "id", strconv.Itoa(id))
	if err != nil {
		m.Error = bpfMessage(err)
		return m
	}
	var raw struct {
		Type       string   `json:"type"`
		Name       string   `json:"name"`
		KeyBytes   int64    `json:"bytes_key"`
		ValueBytes int64    `json:"bytes_value"`
		MaxEntries int64    `json:"max_entries"`
		Memlock    int64    `json:"bytes_memlock"`
		Frozen     flexInt  `json:"frozen"`
		Pinned     []string `json:"pinned"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &raw) != nil {
		m.Error = "bpftool printed something this page could not read"
		return m
	}
	m.Type, m.Name, m.KeyBytes, m.ValueBytes = raw.Type, raw.Name, raw.KeyBytes, raw.ValueBytes
	m.MaxEntries, m.Memlock, m.Frozen, m.Pinned = raw.MaxEntries, raw.Memlock, raw.Frozen != 0, raw.Pinned
	return m
}

// bpfErrorRe finds the message bpftool -j prints for a failure: it keeps the
// JSON shape, so an error arrives as [{"error":"…"}].
var bpfErrorRe = regexp.MustCompile(`"error":\s*"([^"]*)"`)

func bpfMessage(err error) string {
	if m := bpfErrorRe.FindStringSubmatch(err.Error()); m != nil {
		return "bpftool: " + m[1]
	}
	return err.Error()
}

// parseBPFProgs reads `bpftool -j prog show`: a JSON array of programs, or
// null where there are none.
func parseBPFProgs(out string) ([]EBPFProgram, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return []EBPFProgram{}, nil
	}
	var raw []bpfProg
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	progs := make([]EBPFProgram, 0, len(raw))
	for _, r := range raw {
		p := EBPFProgram{
			ID: r.ID, Type: r.Type, Name: r.Name, Tag: r.Tag, UID: r.UID,
			BytesXlated: r.BytesXlated, BytesJited: r.BytesJited, Memlock: r.Memlock,
			MapIDs: r.MapIDs, RunTimeNs: r.RunTimeNs, RunCount: r.RunCnt, Pinned: r.Pinned,
		}
		if p.Memlock == 0 {
			p.Memlock = r.MemlockOld
		}
		if p.MapIDs == nil {
			p.MapIDs = []int{}
		}
		if r.LoadedAt > 0 {
			t := time.Unix(int64(r.LoadedAt), 0).UTC()
			p.LoadedAt = &t
		}
		seen := map[string]bool{}
		for _, o := range r.PIDs {
			label := o.Comm + " (" + strconv.Itoa(o.PID) + ")"
			if !seen[label] {
				seen[label] = true
				p.Owners = append(p.Owners, label)
			}
		}
		progs = append(progs, p)
	}
	sort.Slice(progs, func(i, j int) bool { return progs[i].ID < progs[j].ID })
	return progs, nil
}

// bpfNet is `bpftool -j net show`: one object with an array per attachment
// point, wrapped in an array. The XDP entries carry either one program or a
// list of them with a mode each.
type bpfNet struct {
	XDP []struct {
		Devname string `json:"devname"`
		Ifindex int    `json:"ifindex"`
		ID      int    `json:"id"`
		Mode    string `json:"mode"`
		Multi   []struct {
			Mode string `json:"mode"`
			ID   int    `json:"id"`
		} `json:"multi_attachments"`
	} `json:"xdp"`
	TC []struct {
		Devname string `json:"devname"`
		Ifindex int    `json:"ifindex"`
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		ProgID  int    `json:"prog_id"`
	} `json:"tc"`
}

func parseBPFNet(out string, names map[int]string) ([]EBPFAttachment, error) {
	out = strings.TrimSpace(out)
	atts := []EBPFAttachment{}
	if out == "" || out == "null" {
		return atts, nil
	}
	var docs []bpfNet
	if strings.HasPrefix(out, "[") {
		if err := json.Unmarshal([]byte(out), &docs); err != nil {
			return nil, err
		}
	} else {
		var one bpfNet
		if err := json.Unmarshal([]byte(out), &one); err != nil {
			return nil, err
		}
		docs = []bpfNet{one}
	}
	for _, d := range docs {
		for _, x := range d.XDP {
			if len(x.Multi) > 0 {
				for _, m := range x.Multi {
					atts = append(atts, EBPFAttachment{Device: x.Devname, Ifindex: x.Ifindex, Kind: "xdp", ProgramID: m.ID, Name: names[m.ID], Mode: m.Mode})
				}
				continue
			}
			atts = append(atts, EBPFAttachment{Device: x.Devname, Ifindex: x.Ifindex, Kind: "xdp", ProgramID: x.ID, Name: names[x.ID], Mode: x.Mode})
		}
		for _, t := range d.TC {
			// "clsact/ingress" and "clsact/egress": the part after the slash
			// is the direction.
			kind := "tc"
			if _, dir, ok := strings.Cut(t.Kind, "/"); ok {
				kind = "tc " + dir
			}
			name := t.Name
			if name == "" {
				name = names[t.ProgID]
			}
			atts = append(atts, EBPFAttachment{Device: t.Devname, Ifindex: t.Ifindex, Kind: kind, ProgramID: t.ProgID, Name: name})
		}
	}
	sort.SliceStable(atts, func(i, j int) bool {
		if atts[i].Device != atts[j].Device {
			return atts[i].Device < atts[j].Device
		}
		return atts[i].Kind < atts[j].Kind
	})
	return atts, nil
}
