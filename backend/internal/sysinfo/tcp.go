package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// The interface counters say whether packets were lost on a link; they say
// nothing about whether connections are struggling. TCP's own counters do:
// segments sent again because no acknowledgement came back, connection
// attempts that failed, connections reset, and connections refused at a full
// accept queue. They are the host's (TCP's MIB is shared by both families),
// cumulative since boot, and read here as rates over the collector's interval
// beside the cumulative values the health windows difference themselves.

// TCPCounters is one reading of the TCP MIB.
type TCPCounters struct {
	ActiveOpens     uint64 `json:"activeOpens"`
	PassiveOpens    uint64 `json:"passiveOpens"`
	AttemptFails    uint64 `json:"attemptFails"`
	EstabResets     uint64 `json:"estabResets"`
	OutSegs         uint64 `json:"outSegs"`
	RetransSegs     uint64 `json:"retransSegs"`
	InErrs          uint64 `json:"inErrs"`
	ListenOverflows uint64 `json:"listenOverflows"`
	ListenDrops     uint64 `json:"listenDrops"`
	Timeouts        uint64 `json:"timeouts"`
}

// TCPStats is the TCP reading on a snapshot.
type TCPStats struct {
	// Supported is false where /proc/net/snmp could not be read.
	Supported bool        `json:"supported"`
	Counters  TCPCounters `json:"counters"`
	// Per-second rates over the interval since the previous collection;
	// zero on the first.
	OutSegsRate      float64 `json:"outSegsRate"`
	RetransRate      float64 `json:"retransRate"`
	AttemptFailsRate float64 `json:"attemptFailsRate"`
	EstabResetsRate  float64 `json:"estabResetsRate"`
	ListenDropsRate  float64 `json:"listenDropsRate"`
	// RetransPercent is the share of segments sent in the interval that were
	// retransmissions.
	RetransPercent float64 `json:"retransPercent"`
	// Latency is the kernel's smoothed round-trip time of established
	// connections to peers off this host's networks.
	Latency TCPLatency `json:"latency"`
}

// TCPLatency summarises tcp_info's smoothed RTT across connections.
type TCPLatency struct {
	Supported bool `json:"supported"`
	// Sockets is how many established connections to external peers were
	// measured; with none there is no latency to report.
	Sockets  int     `json:"sockets"`
	MedianMs float64 `json:"medianMs"`
	P90Ms    float64 `json:"p90Ms"`
}

var (
	snmpPath    = "/proc/net/snmp"
	netstatPath = "/proc/net/netstat"
)

// ReadTCPCounters reads the TCP MIB and the extension counters.
func ReadTCPCounters() (TCPCounters, bool) {
	var c TCPCounters
	snmp, ok := readMIB(snmpPath, "Tcp:")
	if !ok {
		return c, false
	}
	c.ActiveOpens = snmp["ActiveOpens"]
	c.PassiveOpens = snmp["PassiveOpens"]
	c.AttemptFails = snmp["AttemptFails"]
	c.EstabResets = snmp["EstabResets"]
	c.OutSegs = snmp["OutSegs"]
	c.RetransSegs = snmp["RetransSegs"]
	c.InErrs = snmp["InErrs"]
	if ext, ok := readMIB(netstatPath, "TcpExt:"); ok {
		c.ListenOverflows = ext["ListenOverflows"]
		c.ListenDrops = ext["ListenDrops"]
		c.Timeouts = ext["TCPTimeouts"]
	}
	return c, true
}

// readMIB reads one protocol's pair of lines — a header naming each column
// and a line of values — from a /proc/net MIB file.
func readMIB(path, prefix string) (map[string]uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var header []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || fields[0] != prefix {
			continue
		}
		if header == nil {
			header = fields[1:]
			continue
		}
		values := map[string]uint64{}
		for i, name := range header {
			if i+1 >= len(fields) {
				break
			}
			// MaxConn is -1 and is the one signed column; it is not read.
			if n, err := strconv.ParseUint(fields[i+1], 10, 64); err == nil {
				values[name] = n
			}
		}
		return values, true
	}
	return nil, false
}

// tcpRates turns two readings into per-second rates. A counter that went
// backwards (a namespace recreated, a counter wrapped) yields no rate.
func tcpRates(prev, cur TCPCounters, elapsed float64) TCPStats {
	s := TCPStats{Supported: true, Counters: cur}
	if elapsed <= 0 {
		return s
	}
	rate := func(a, b uint64) float64 {
		if b < a {
			return 0
		}
		return float64(b-a) / elapsed
	}
	s.OutSegsRate = rate(prev.OutSegs, cur.OutSegs)
	s.RetransRate = rate(prev.RetransSegs, cur.RetransSegs)
	s.AttemptFailsRate = rate(prev.AttemptFails, cur.AttemptFails)
	s.EstabResetsRate = rate(prev.EstabResets, cur.EstabResets)
	s.ListenDropsRate = rate(prev.ListenDrops, cur.ListenDrops)
	if s.OutSegsRate > 0 {
		s.RetransPercent = s.RetransRate / s.OutSegsRate * 100
	}
	return s
}
