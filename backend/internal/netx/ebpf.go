package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	v := &EBPFView{Programs: []EBPFProgram{}, ByType: []EBPFTypeCount{}, Attachments: []EBPFAttachment{}}
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
	return v, nil
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
