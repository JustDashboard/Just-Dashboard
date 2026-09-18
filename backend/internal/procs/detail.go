package procs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessLink is one process as seen from another's detail view: enough to
// recognise it and to open it, without the counters a full row carries.
type ProcessLink struct {
	PID        int32     `json:"pid"`
	Name       string    `json:"name"`
	Cmdline    string    `json:"cmdline"`
	Username   string    `json:"username"`
	State      string    `json:"state"`
	CPUPercent float64   `json:"cpuPercent"`
	RSS        uint64    `json:"rss"`
	CreateTime time.Time `json:"createTime"`
}

// ProcessTree is where a process sits: the chain above it up to init, and the
// processes it has started. The chain matters because the remedy for a
// runaway child is usually its supervisor — killing a worker that gunicorn or
// PM2 immediately respawns is the loop this view exists to break.
type ProcessTree struct {
	Ancestors []ProcessLink `json:"ancestors"`
	Children  []ProcessLink `json:"children"`
}

// ListeningPort is one socket a process accepts connections on.
type ListeningPort struct {
	Proto   string `json:"proto"`
	Address string `json:"address"`
	Port    uint32 `json:"port"`
}

// Tree walks the parent chain and collects direct children from one pass over
// the table. Children are found by scanning rather than through gopsutil's
// helper, which reads every process once per call anyway and returns handles
// that would each have to be read again.
func (t *Table) Tree(ctx context.Context, pid int32) (*ProcessTree, error) {
	if _, err := process.NewProcessWithContext(ctx, pid); err != nil {
		return nil, fmt.Errorf("no process with pid %d", pid)
	}
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	byPID := make(map[int32]*process.Process, len(all))
	children := []ProcessLink{}
	for _, p := range all {
		byPID[p.Pid] = p
		if ppid, err := p.PpidWithContext(ctx); err == nil && ppid == pid {
			if link, ok := linkFor(ctx, p); ok {
				children = append(children, link)
			}
		}
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].CPUPercent != children[j].CPUPercent {
			return children[i].CPUPercent > children[j].CPUPercent
		}
		return children[i].PID < children[j].PID
	})

	ancestors := []ProcessLink{}
	seen := map[int32]bool{pid: true}
	current := byPID[pid]
	for current != nil {
		ppid, err := current.PpidWithContext(ctx)
		if err != nil || ppid <= 0 || seen[ppid] {
			break
		}
		seen[ppid] = true
		parent := byPID[ppid]
		if parent == nil {
			break
		}
		if link, ok := linkFor(ctx, parent); ok {
			ancestors = append(ancestors, link)
		}
		current = parent
	}
	// Outermost first, so the chain reads top-down like the process tree it is.
	for i, j := 0, len(ancestors)-1; i < j; i, j = i+1, j-1 {
		ancestors[i], ancestors[j] = ancestors[j], ancestors[i]
	}
	return &ProcessTree{Ancestors: ancestors, Children: children}, nil
}

func linkFor(ctx context.Context, p *process.Process) (ProcessLink, bool) {
	link := ProcessLink{PID: p.Pid}
	link.Name, _ = p.NameWithContext(ctx)
	if link.Name == "" {
		return link, false
	}
	link.Cmdline, _ = p.CmdlineWithContext(ctx)
	link.Username, _ = p.UsernameWithContext(ctx)
	if st, err := p.StatusWithContext(ctx); err == nil {
		link.State = processState(strings.Join(st, ","))
	}
	link.CPUPercent, _ = p.CPUPercentWithContext(ctx)
	link.CPUPercent = round2(link.CPUPercent)
	if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
		link.RSS = mi.RSS
	}
	if ct, err := p.CreateTimeWithContext(ctx); err == nil {
		link.CreateTime = time.UnixMilli(ct).UTC()
	}
	return link, true
}

// sockets reads what a process listens on and how many connections it holds.
// "What is on port 3000" is the question the process table is opened for
// most often after "what is eating the CPU", and until now the answer lived
// on the security page's connection list, one hop away from the process.
func sockets(ctx context.Context, p *process.Process) ([]ListeningPort, int) {
	conns, err := p.ConnectionsWithContext(ctx)
	if err != nil {
		return nil, 0
	}
	listening := []ListeningPort{}
	established := 0
	seen := map[string]bool{}
	for _, c := range conns {
		proto := "tcp"
		if c.Type == syscall.SOCK_DGRAM {
			proto = "udp"
		}
		if c.Family == syscall.AF_INET6 {
			proto += "6"
		}
		switch {
		case c.Status == "LISTEN", proto[:3] == "udp" && c.Raddr.Port == 0 && c.Laddr.Port > 0:
			key := fmt.Sprintf("%s/%s/%d", proto, c.Laddr.IP, c.Laddr.Port)
			if seen[key] {
				continue
			}
			seen[key] = true
			listening = append(listening, ListeningPort{Proto: proto, Address: c.Laddr.IP, Port: c.Laddr.Port})
		case c.Status == "ESTABLISHED":
			established++
		}
	}
	sort.Slice(listening, func(i, j int) bool {
		if listening[i].Port != listening[j].Port {
			return listening[i].Port < listening[j].Port
		}
		return listening[i].Proto < listening[j].Proto
	})
	return listening, established
}

// openFilesLimit reads the soft NOFILE limit, which beside the descriptor
// count is what turns "1,020 open files" from a number into a warning.
func openFilesLimit(ctx context.Context, p *process.Process) uint64 {
	limits, err := p.RlimitUsageWithContext(ctx, false)
	if err != nil {
		return 0
	}
	for _, l := range limits {
		if l.Resource == process.RLIMIT_NOFILE {
			return l.Soft
		}
	}
	return 0
}
