package logsx

import (
	"strconv"
	"strings"
	"time"
)

// The kernel lens names what the kernel ring says about the host itself: a
// process killed for memory, a segfault, a disk returning I/O errors, a
// filesystem gone read-only, a link dropping. Everything else in kern.log —
// on a Docker host, four lines in five are veth ports entering and leaving
// states — keeps no event and stays readable as the text it is. Packet lines
// (UFW, firewalld) are the firewall lens's, and are handed to it.
func init() {
	register(&Lens{
		ID: "kernel",
		Events: []string{"oom_kill", "oom", "segfault", "io_error", "fs_error", "readonly_fs",
			"link_down", "link_up", "apparmor_denied", "hung_task", "mce"},
		Attrs: []string{"pid", "program", "memory", "dev", "unit", "container"},
		New:   func() Reader { return &kernelReader{} },
	})
}

// An OOM report is one record over a hundred lines: "node invoked
// oom-killer", the call trace, Mem-Info, the task table, the oom-kill summary,
// and only then the "Killed process" line that says who died. The trace and
// tables fold under the invocation as continuations; the kill is a record of
// its own, because it is the line the operator is looking for. The dump comes
// out in one burst, so a record left open past kernelDumpWindow, or longer
// than kernelDumpLines, is a report that never finished, not one still going.
const (
	kernelDumpWindow = 2 * time.Second
	kernelDumpLines  = 4096
)

type kernelReader struct {
	// head is the event whose record later lines may continue: "oom" takes
	// the dump, "oom_kill" the reaper's line, "segfault" the Code: line.
	head  string
	level string
	at    *time.Time
	lines int
	// owner is the unit the oom-kill summary names; it is carried onto the
	// Killed process line that follows it, which does not say it.
	owner string
}

func (r *kernelReader) Read(l *Line) {
	msg := sysStripUptime(sysEnvelope(l).text)
	if firewallRead(l, msg) {
		l.Lens = "firewall"
		r.head, r.owner = "", ""
		return
	}
	if event := kernelEvent(l, msg, r.owner); event != "" {
		r.head, r.level, r.at, r.lines, r.owner = event, l.Level, l.Timestamp, 0, ""
		return
	}
	r.owner = ""
	if strings.HasPrefix(msg, "oom-kill:") {
		r.owner = kernelOwner(msg)
	}
	if r.continues(l, msg) {
		l.Cont = true
		l.SetLevel(r.level)
		return
	}
	r.head = ""
}

// continues says whether a line with no event of its own belongs to the
// record opened above it.
func (r *kernelReader) continues(l *Line, msg string) bool {
	switch r.head {
	case "oom":
		r.lines++
		if r.lines > kernelDumpLines {
			return false
		}
		if r.at != nil && l.Timestamp != nil && l.Timestamp.Sub(*r.at) > kernelDumpWindow {
			return false
		}
		return true
	case "oom_kill":
		return strings.HasPrefix(msg, "oom_reaper: ")
	case "segfault":
		return strings.HasPrefix(msg, "Code: ")
	}
	return false
}

// kernelEvent names a kernel message, reading the values that go with it.
// Each rule is gated on a literal the kernel prints, so a veth line costs a
// handful of prefix tests.
func kernelEvent(l *Line, msg, owner string) string {
	switch {
	case strings.Contains(msg, "Killed process "):
		return kernelOOMKill(l, msg, owner)
	case strings.Contains(msg, " invoked oom-killer: "):
		l.Event = "oom"
		l.SetAttr("program", msg[:strings.Index(msg, " invoked oom-killer: ")])
		l.SetLevel("error")
		return l.Event
	case strings.Contains(msg, "]: segfault at "):
		program, pid := kernelTask(msg[:strings.Index(msg, ": segfault at ")])
		return kernelCrash(l, program, pid)
	case strings.HasPrefix(msg, "traps: ") && strings.Contains(msg, "] general protection fault "):
		// A general protection fault is a SIGSEGV by another route. The int3
		// and invalid-opcode traps are how Chrome and Go abort on purpose,
		// hundreds a day from a headless browser, and are left as text.
		rest := msg[len("traps: "):]
		program, pid := kernelTask(rest[:strings.IndexByte(rest, ' ')])
		return kernelCrash(l, program, pid)
	case strings.HasPrefix(msg, "INFO: task ") && strings.Contains(msg, " blocked for more than "):
		// "INFO:" is the prefix, not the severity: the kernel prints this at
		// KERN_ERR, and a task stuck in the kernel for minutes is a stalled
		// disk far more often than anything informational.
		task := msg[len("INFO: task "):strings.Index(msg, " blocked for more than ")]
		if colon := strings.LastIndexByte(task, ':'); colon > 0 {
			l.SetAttr("program", task[:colon])
			l.SetAttr("pid", task[colon+1:])
		}
		l.Event = "hung_task"
		l.SetLevel("error")
		return l.Event
	case strings.Contains(msg, " error, dev "):
		// blk_status_to_str's words — "I/O", "critical medium", "timeout" —
		// all end in " error, dev sda, sector …".
		dev := msg[strings.Index(msg, " error, dev ")+len(" error, dev "):]
		return kernelDev(l, "io_error", "error", sysUntil(dev, ','))
	case strings.HasPrefix(msg, "Buffer I/O error on dev "):
		dev := msg[len("Buffer I/O error on dev "):]
		return kernelDev(l, "io_error", "error", sysUntil(dev, ','))
	case strings.Contains(msg, "Remounting filesystem read-only"), strings.Contains(msg, "forced readonly"):
		return kernelDev(l, "readonly_fs", "critical", kernelParenDev(msg))
	case strings.HasPrefix(msg, "EXT4-fs error (device "), strings.HasPrefix(msg, "EXT3-fs error (device "),
		strings.HasPrefix(msg, "EXT2-fs error (device "), strings.HasPrefix(msg, "BTRFS error (device "),
		strings.HasPrefix(msg, "BTRFS critical (device "):
		return kernelDev(l, "fs_error", "error", kernelParenDev(msg))
	case strings.Contains(msg, "Link is Down"):
		return kernelLink(l, msg, strings.Index(msg, "Link is Down"), "link_down")
	case strings.Contains(msg, "Link is Up"):
		return kernelLink(l, msg, strings.Index(msg, "Link is Up"), "link_up")
	case strings.Contains(msg, `apparmor="DENIED"`):
		l.Event = "apparmor_denied"
		if i := strings.Index(msg, " pid="); i >= 0 {
			l.SetAttr("pid", sysDigits(msg[i+len(" pid="):]))
		}
		if i := strings.Index(msg, ` comm="`); i >= 0 {
			comm := msg[i+len(` comm="`):]
			if end := strings.IndexByte(comm, '"'); end > 0 {
				l.SetAttr("program", comm[:end])
			}
		}
		return l.Event
	case strings.HasPrefix(msg, "mce: [Hardware Error]: "), strings.Contains(msg, "Machine check events logged"):
		l.Event = "mce"
		l.SetLevel("critical")
		return l.Event
	}
	return ""
}

// kernelOOMKill reads "Out of memory: Killed process 48211 (node)
// total-vm:…kB, anon-rss:…kB, file-rss:…kB, shmem-rss:…kB, …" and its memory
// cgroup variant. The name is matched up to ") total-vm:" rather than the
// first ")", because a comm is cut at fifteen bytes and "next-build (v16" is
// a real one. memory is what the victim held resident, in bytes.
func kernelOOMKill(l *Line, msg, owner string) string {
	i := strings.Index(msg, "Killed process ")
	head := msg[:i]
	if !strings.HasSuffix(head, "out of memory: ") && !strings.HasSuffix(head, "Out of memory: ") &&
		!strings.HasSuffix(head, "(oom_kill_allocating_task): ") {
		return ""
	}
	rest := msg[i+len("Killed process "):]
	l.Event = "oom_kill"
	l.SetAttr("pid", sysDigits(rest))
	if open, shut := strings.IndexByte(rest, '('), strings.Index(rest, ") total-vm:"); open > 0 && shut > open {
		l.SetAttr("program", rest[open+1:shut])
	}
	var kb float64
	for _, key := range []string{"anon-rss:", "file-rss:", "shmem-rss:"} {
		if at := strings.Index(rest, key); at >= 0 {
			n, _ := strconv.ParseFloat(sysDigits(rest[at+len(key):]), 64)
			kb += n
		}
	}
	if kb > 0 {
		l.SetAttrNumber("memory", kb*1024)
	}
	if owner != "" {
		if strings.HasPrefix(owner, "docker-") && strings.HasSuffix(owner, ".scope") && len(owner) >= len("docker-")+12 {
			l.SetAttr("container", owner[len("docker-"):len("docker-")+12])
		} else {
			l.SetAttr("unit", owner)
		}
	}
	l.SetLevel("error")
	return l.Event
}

// kernelOwner reads the unit that held the killed task out of the summary
// line: task_memcg is the cgroup path, and its last element is a unit —
// docker-<id>.scope for a container, foo.service for a service.
func kernelOwner(msg string) string {
	i := strings.Index(msg, "task_memcg=")
	if i < 0 {
		return ""
	}
	path := msg[i+len("task_memcg="):]
	path = sysUntil(path, ',')
	unit := path[strings.LastIndexByte(path, '/')+1:]
	if strings.IndexByte(unit, '.') < 0 {
		return ""
	}
	return unit
}

func kernelCrash(l *Line, program, pid string) string {
	l.Event = "segfault"
	l.SetAttr("program", program)
	l.SetAttr("pid", pid)
	l.SetLevel("error")
	return l.Event
}

// kernelTask splits the kernel's "comm[pid]".
func kernelTask(s string) (program, pid string) {
	open := strings.LastIndexByte(s, '[')
	if open <= 0 || !strings.HasSuffix(s, "]") {
		return s, ""
	}
	return s[:open], s[open+1 : len(s)-1]
}

func kernelDev(l *Line, event, level, dev string) string {
	l.Event = event
	l.SetAttr("dev", dev)
	l.SetLevel(level)
	return event
}

// kernelParenDev reads the device out of "EXT4-fs (sda1): …" or "EXT4-fs
// error (device sda1): …".
func kernelParenDev(msg string) string {
	open := strings.IndexByte(msg, '(')
	if open < 0 {
		return ""
	}
	dev := strings.TrimPrefix(msg[open+1:], "device ")
	return sysUntil(dev, ')')
}

// kernelLink reads the interface out of "ens3: Link is Down" or a driver's
// "e1000e 0000:00:19.0 eth0: NIC Link is Up 1000 Mbps Full Duplex": it is the
// last word before the colon ahead of the state.
func kernelLink(l *Line, msg string, at int, event string) string {
	head := strings.TrimSpace(msg[:at])
	head = strings.TrimSpace(strings.TrimSuffix(head, "NIC"))
	head = strings.TrimSuffix(head, ":")
	l.Event = event
	l.SetAttr("dev", head[strings.LastIndexByte(head, ' ')+1:])
	if event == "link_down" {
		l.SetLevel("warn")
	}
	return event
}
