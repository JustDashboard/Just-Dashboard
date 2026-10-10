package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Pressure is the kernel's own answer to "is this machine struggling", read
// from /proc/pressure.
//
// It is the number utilisation percentages cannot give. A host at 100% CPU
// with zero CPU pressure is a machine doing exactly as much work as it has
// capacity for, which is fine; a host at 40% with 30% CPU pressure is one
// where tasks are spending a third of their time waiting for a runqueue slot,
// which is not. Same for memory: "80% used" is meaningless on a box with a
// large page cache, whereas memory pressure above zero means something is
// actually stalling on reclaim.
//
// Some is the share of time at least one task was stalled; Full is the share
// where every task was — an idle-but-blocked machine, which for I/O is the
// signature of a disk that has become the bottleneck. avg10 is the window
// used: a dashboard sampling every 15 seconds wants the kernel's own
// short-window average, not a total counter it would have to difference.
type Pressure struct {
	// Supported is false on a kernel built without PSI (pre-4.20, or with
	// CONFIG_PSI_DEFAULT_DISABLED and no psi=1 on the command line), which is
	// worth distinguishing from "no pressure at all".
	Supported bool `json:"supported"`

	CPUSome float64 `json:"cpuSome"`
	MemSome float64 `json:"memSome"`
	MemFull float64 `json:"memFull"`
	IOSome  float64 `json:"ioSome"`
	IOFull  float64 `json:"ioFull"`
}

// pressureRoot is a variable so the tests can point it at a fixture directory
// rather than requiring the machine running them to have PSI enabled.
var pressureRoot = "/proc/pressure"

// ReadPressure reads all three PSI files. A missing file is not an error —
// the whole feature is optional in the kernel — so the caller gets a zero
// Pressure with Supported false and can say so on screen rather than showing
// three flat lines at zero that look like a healthy machine.
func ReadPressure() Pressure {
	var p Pressure
	if some, _, ok := readPSI(pressureRoot + "/cpu"); ok {
		p.Supported = true
		p.CPUSome = some
	}
	if some, full, ok := readPSI(pressureRoot + "/memory"); ok {
		p.Supported = true
		p.MemSome, p.MemFull = some, full
	}
	if some, full, ok := readPSI(pressureRoot + "/io"); ok {
		p.Supported = true
		p.IOSome, p.IOFull = some, full
	}
	return p
}

// readPSI parses one pressure file:
//
//	some avg10=0.00 avg60=0.00 avg300=0.00 total=0
//	full avg10=0.00 avg60=0.00 avg300=0.00 total=0
//
// Only avg10 is taken. The totals are cumulative microseconds and would need
// differencing against the previous read to mean anything; the kernel has
// already done that work for the averages.
func readPSI(path string) (some, full float64, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		var avg10 float64
		for _, field := range fields[1:] {
			key, value, found := strings.Cut(field, "=")
			if !found || key != "avg10" {
				continue
			}
			avg10, _ = strconv.ParseFloat(value, 64)
			break
		}
		switch fields[0] {
		case "some":
			some, ok = round2(avg10), true
		case "full":
			full, ok = round2(avg10), true
		}
	}
	return some, full, ok
}

// Sockets counts what the network stack is holding open.
//
// Read from /proc/net/sockstat rather than enumerating connections: walking
// every socket to count them means reading a line per connection and
// resolving each one, which on a busy host is thousands of lines every
// sample. The kernel already keeps the totals.
type Sockets struct {
	// TCPInUse is established plus listening plus everything in between —
	// the figure that climbs when an application leaks connections.
	TCPInUse int `json:"tcpInUse"`
	// TCPTimeWait is worth its own number: a host with tens of thousands of
	// them is one about to run out of ephemeral ports, and it looks like
	// nothing at all in a total.
	TCPTimeWait int `json:"tcpTimeWait"`
	TCPOrphan   int `json:"tcpOrphan"`
	UDPInUse    int `json:"udpInUse"`
	// Used is every socket of every family the kernel has allocated.
	Used int `json:"used"`
}

var sockstatPaths = []string{"/proc/net/sockstat", "/proc/net/sockstat6"}

// ReadSockets sums the IPv4 and IPv6 tables. They are separate files with the
// same shape, and reporting only the first would undercount a dual-stack host
// by however much of its traffic arrives over IPv6.
func ReadSockets() Sockets {
	var s Sockets
	for _, path := range sockstatPaths {
		readSockstat(path, &s)
	}
	return s
}

func readSockstat(path string, s *Sockets) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		// "sockets: used 1234" has no per-protocol key/value pairs, and only
		// appears in the IPv4 file.
		if fields[0] == "sockets:" {
			if len(fields) >= 3 && fields[1] == "used" {
				s.Used += atoi(fields[2])
			}
			continue
		}
		proto := strings.TrimSuffix(fields[0], ":")
		if proto != "TCP" && proto != "TCP6" && proto != "UDP" && proto != "UDP6" {
			continue
		}
		// The rest is "key value key value…", so the pairs are read two at a
		// time rather than split on a separator.
		for i := 1; i+1 < len(fields); i += 2 {
			n := atoi(fields[i+1])
			switch {
			case fields[i] == "inuse" && strings.HasPrefix(proto, "TCP"):
				s.TCPInUse += n
			case fields[i] == "inuse":
				s.UDPInUse += n
			case fields[i] == "tw":
				s.TCPTimeWait += n
			case fields[i] == "orphan":
				s.TCPOrphan += n
			}
		}
	}
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
