package netx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Traffic by program and by container.
//
// The kernel counts bytes per interface and per TCP socket, and nowhere in
// between: there is no per-process counter without an eBPF program attached,
// and the dashboard does not attach one. What a socket does carry is the
// bytes it has sent, had acknowledged and received (ss -i prints them), so a
// program's traffic is its sockets' counters, differenced between two reads
// and summed. UDP has no such counters; the note on every answer says so.

// socketCap bounds how many sockets one read keeps. A host proxying tens of
// thousands of connections would otherwise make every poll a parse of
// megabytes to draw a top-twenty list.
const socketCap = 4000

const (
	// youngWindow is how recent the previous read must be for a socket not in
	// it to count as born in the interval. A socket first seen after a long
	// gap may be hours old, and counting its lifetime bytes as one interval's
	// traffic would draw a spike that never happened.
	youngWindow = 5 * time.Second
	// minInterval is the shortest gap two reads are differenced across; a
	// second poll inside it is answered from the first. Rates over a few
	// hundred milliseconds are mostly the scheduler.
	minInterval = time.Second
	// staleAfter is how old the previous read can be before it is no
	// baseline at all: sockets have come and gone, and a rate averaged across
	// minutes of churn is not what is happening now.
	staleAfter = 2 * time.Minute
)

const trafficNote = "Rates are TCP's per-socket counters; UDP sockets are counted and named without bytes, which the kernel does not keep per socket"

// PeerTraffic is a remote end a program talks to.
type PeerTraffic struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	// Protocol is tcp or udp. A UDP peer has no byte counters: BytesKnown is
	// false and its zeros are not measurements.
	Protocol    string `json:"protocol"`
	BytesKnown  bool   `json:"bytesKnown"`
	Connections int    `json:"connections"`
	// RxBytes and TxBytes are what the live connections to it have carried.
	RxBytes uint64 `json:"rxBytes"`
	TxBytes uint64 `json:"txBytes"`
}

// ProgramTraffic is one program's sockets, summed.
type ProgramTraffic struct {
	Name        string `json:"name"`
	PIDs        []int  `json:"pids"`
	Connections int    `json:"connections"`
	// RxRate and TxRate are bytes a second over the interval.
	RxRate float64 `json:"rxRate"`
	TxRate float64 `json:"txRate"`
	// RxTotal and TxTotal are what the live sockets have carried since they
	// opened: a closed connection's bytes are not in them.
	RxTotal uint64        `json:"rxTotal"`
	TxTotal uint64        `json:"txTotal"`
	Peers   []PeerTraffic `json:"peers"`
	// UDPConnected are UDP sockets with a peer (QUIC clients, resolvers that
	// connect); UDPUnconnected are listeners and servers, HTTP/3 among them,
	// whose peers ss cannot name. Neither has byte counters.
	UDPConnected   int `json:"udpConnected"`
	UDPUnconnected int `json:"udpUnconnected"`
	// MedianRTTMs is the median of TCP's smoothed round-trip estimate over
	// the program's sockets; Retransmitted and SegmentsOut are what its live
	// sockets have resent and sent since they opened.
	MedianRTTMs   *float64 `json:"medianRttMs,omitempty"`
	Retransmitted uint64   `json:"retransmitted"`
	SegmentsOut   uint64   `json:"segmentsOut"`
}

// ProcessTraffic is every program's TCP traffic.
type ProcessTraffic struct {
	At              time.Time `json:"at"`
	IntervalSeconds float64   `json:"intervalSeconds"`
	// Warming is a first read, or one after a long gap: the totals are real
	// and the rates are zero because there is nothing to difference against.
	Warming  bool             `json:"warming"`
	Programs []ProgramTraffic `json:"programs"`
	// Truncated is whether the host had more sockets than were read.
	Truncated bool   `json:"truncated"`
	Note      string `json:"note"`
	// MissedOpens is how many TCP connections the kernel opened since the
	// previous read that this read did not see established — born and gone
	// between two reads, their bytes counted nowhere here. Absent while
	// warming, on a truncated read or where the kernel's counters could not
	// be read: unknown, not zero.
	MissedOpens *uint64 `json:"missedOpens,omitempty"`
	// Closed is how many sockets of the previous read are gone; what they
	// carried after it is not in any rate.
	Closed   int    `json:"closed"`
	UDPError string `json:"udpError,omitempty"`
	// History says whether the separate socket history is recording, which
	// is where traffic before this page opened can be read. The api fills it.
	History *RecordedHistory `json:"history,omitempty"`
}

// RecordedHistory is the standing of the opt-in socket history beside the
// live program reads.
type RecordedHistory struct {
	Recording      bool       `json:"recording"`
	KernelObserver bool       `json:"kernelObserver"`
	Since          *time.Time `json:"since,omitempty"`
	RetentionDays  int        `json:"retentionDays"`
}

// udpSocket is one line of `ss -uanpH`: UDP has a state column (ESTAB for a
// connected socket, UNCONN otherwise) and no counters.
type udpSocket struct {
	state, local, peer string
	peerAddr           string
	peerPort           int
	name               string
	pids               []int
}

// parseUDP reads `ss -uanpH`, stopping at socketCap.
func parseUDP(out string) (socks []udpSocket, truncated bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(socks) >= socketCap {
			return socks, true
		}
		f := ssSpace.Split(line, 6)
		if len(f) < 5 {
			continue
		}
		u := udpSocket{state: f[0], local: f[3], peer: f[4]}
		if u.state == "ESTAB" {
			u.peerAddr, u.peerPort = splitHostPort(f[4])
		}
		if len(f) == 6 {
			for _, m := range usersRe.FindAllStringSubmatch(f[5], -1) {
				pid, _ := strconv.Atoi(m[2])
				if u.name == "" {
					u.name = m[1]
				}
				u.pids = append(u.pids, pid)
			}
		}
		socks = append(socks, u)
	}
	return socks, false
}

// flowSocket is one established TCP socket.
type flowSocket struct {
	local, peer string
	name        string
	pids        []int
	tx, rx      uint64
	peerAddr    string
	peerPort    int
	hasName     bool
	// What ss -i prints about the connection itself: TCP's smoothed
	// round-trip estimate, segments sent and resent, the congestion control
	// the socket runs and the rate it last delivered at. Absent fields stay
	// zero with their has-flag false, never a measured zero.
	rttMs, minRTTMs float64
	hasRTT          bool
	retransOut      uint64
	retransTotal    uint64
	segsOut         uint64
	bytesRetrans    uint64
	congestion      string
	deliveryBps     float64
	hasDelivery     bool
}

type flowKey struct{ local, peer string }

// flowSampler differences each program's TCP byte counters between two reads.
type flowSampler struct {
	mu   sync.Mutex
	prev map[flowKey]flowSocket
	at   time.Time
	last *ProcessTraffic
	// opens is the kernel's count of TCP connections begun at the previous
	// read, and prevTruncated whether that read stopped at the cap.
	opens         *uint64
	prevTruncated bool
}

func newFlowSampler() *flowSampler { return &flowSampler{} }

var (
	usersRe = regexp.MustCompile(`\("(.*?)",pid=(\d+),fd=\d+\)`)
	// ssSpace splits the columns of a socket's first line.
	ssSpace = regexp.MustCompile(`\s+`)
)

// parseSS reads `ss -tinpH state established`. Each socket is two lines: the
// addresses and owners, then — indented by a tab — the counters. It stops
// reading at socketCap and says so.
func parseSS(out string) (socks []flowSocket, truncated bool) {
	var cur *flowSocket
	finish := func() {
		if cur != nil {
			socks = append(socks, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			finish()
			if len(socks) >= socketCap {
				return socks, true
			}
			// Recv-Q Send-Q Local Peer [users:(...)]. The process names have
			// spaces in them ("next-server (v1"), so only the first four
			// columns are split.
			f := ssSpace.Split(strings.TrimSpace(line), 5)
			if len(f) < 4 {
				continue
			}
			s := &flowSocket{local: f[2], peer: f[3]}
			s.peerAddr, s.peerPort = splitHostPort(f[3])
			if len(f) == 5 {
				for _, m := range usersRe.FindAllStringSubmatch(f[4], -1) {
					pid, _ := strconv.Atoi(m[2])
					if !s.hasName {
						s.name, s.hasName = m[1], true
					}
					s.pids = append(s.pids, pid)
				}
			}
			cur = s
			continue
		}
		if cur == nil {
			continue
		}
		var sent, acked uint64
		var haveSent bool
		tokens := strings.Fields(line)
		named := false
		for i, tok := range tokens {
			k, v, ok := strings.Cut(tok, ":")
			if !ok {
				switch {
				case tok == "delivery_rate" && i+1 < len(tokens):
					cur.deliveryBps, cur.hasDelivery = parseBps(tokens[i+1])
				case !named && ssCongestion(tok):
					// The algorithm is the first bare word after the
					// option flags and before the first key:value.
					cur.congestion = tok
				}
				continue
			}
			named = true
			switch k {
			case "bytes_sent":
				sent, haveSent = parseUint(v), true
			case "bytes_acked":
				acked = parseUint(v)
			case "bytes_received":
				cur.rx = parseUint(v)
			case "bytes_retrans":
				cur.bytesRetrans = parseUint(v)
			case "segs_out":
				cur.segsOut = parseUint(v)
			case "rtt":
				avg, _, _ := strings.Cut(v, "/")
				if ms, err := strconv.ParseFloat(avg, 64); err == nil {
					cur.rttMs, cur.hasRTT = ms, true
				}
			case "minrtt":
				cur.minRTTMs, _ = strconv.ParseFloat(v, 64)
			case "retrans":
				// retrans:outstanding/total
				out, total, _ := strings.Cut(v, "/")
				cur.retransOut, cur.retransTotal = parseUint(out), parseUint(total)
			}
		}
		// bytes_sent counts what was handed to the network; bytes_acked what
		// the peer confirmed. Sent is the traffic that left, where the kernel
		// prints it; older kernels print only acked.
		if haveSent {
			cur.tx = sent
		} else {
			cur.tx = acked
		}
	}
	finish()
	return socks, false
}

func parseUint(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

// ssFlags are the option words ss prints before the congestion algorithm.
var ssFlags = map[string]bool{"ts": true, "sack": true, "ecn": true, "ecnseen": true, "fastopen": true}

// ssCongestion is whether a bare word on the counters line names the
// socket's congestion control: lower-case letters and digits, not a flag.
func ssCongestion(tok string) bool {
	if ssFlags[tok] || tok == "" {
		return false
	}
	for _, r := range tok {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return tok[0] >= 'a' && tok[0] <= 'z'
}

// parseBps reads ss's "14551555552bps" (or with a K/M/G prefix on older
// releases) as bits a second.
func parseBps(s string) (float64, bool) {
	s = strings.TrimSuffix(s, "bps")
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "K"):
		mult, s = 1e3, strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		mult, s = 1e6, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "G"):
		mult, s = 1e9, strings.TrimSuffix(s, "G")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v * mult, true
}

// splitHostPort splits ss's address:port, where the host of an IPv6 address
// is bracketed and may carry a zone.
func splitHostPort(s string) (string, int) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, 0
	}
	port, _ := strconv.Atoi(s[i+1:])
	host := strings.Trim(s[:i], "[]")
	return host, port
}

// Processes reads the sockets and returns each program's traffic.
func (s *Service) Processes(ctx context.Context) (*ProcessTraffic, error) {
	if !has("ss") {
		return nil, &UnavailableError{Tool: "ss", Package: "iproute2"}
	}
	return s.flows.read(ctx)
}

func (f *flowSampler) read(ctx context.Context) (*ProcessTraffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	// Two polls inside a second share an answer rather than each moving the
	// baseline, which would make the rate depend on how many people have the
	// page open.
	if f.last != nil && now.Sub(f.at) < minInterval {
		cp := *f.last
		return &cp, nil
	}
	out, err := run(ctx, "ss", "-tinpH", "state", "established")
	if err != nil {
		return nil, fmt.Errorf("reading the TCP sockets: %w", err)
	}
	socks, truncated := parseSS(out)
	var opens *uint64
	if c, err := readTCPCounters(); err == nil {
		n := c.activeOpens + c.passiveOpens
		opens = &n
	}
	var udp []udpSocket
	udpOut, udpErr := run(ctx, "ss", "-uanpH")
	if udpErr == nil {
		var udpTruncated bool
		udp, udpTruncated = parseUDP(udpOut)
		truncated = truncated || udpTruncated
	}
	res := f.observeAll(now, socks, udp, opens, truncated)
	if udpErr != nil {
		res.UDPError = fmt.Sprintf("reading the UDP sockets: %v", udpErr)
	}
	f.last = res
	cp := *res
	return &cp, nil
}

// observeAll is observe with the UDP sockets and the kernel's open count
// beside the TCP ones.
func (f *flowSampler) observeAll(now time.Time, socks []flowSocket, udp []udpSocket, opens *uint64, truncated bool) *ProcessTraffic {
	prev, prevOpens, prevTruncated := f.prev, f.opens, f.prevTruncated
	res := f.observe(now, socks)
	res.Truncated = truncated
	if !res.Warming {
		seen := uint64(0)
		for _, sk := range socks {
			if _, ok := prev[flowKey{sk.local, sk.peer}]; !ok {
				seen++
			}
		}
		for key := range prev {
			if _, ok := f.prev[key]; !ok {
				res.Closed++
			}
		}
		if opens != nil && prevOpens != nil && *opens >= *prevOpens && !truncated && !prevTruncated {
			missed := uint64(0)
			if began := *opens - *prevOpens; began > seen {
				missed = began - seen
			}
			res.MissedOpens = &missed
		}
	}
	f.opens, f.prevTruncated = opens, truncated
	addUDP(res, udp)
	return res
}

// addUDP counts each program's UDP sockets and lists its connected peers
// beside the TCP ones, marked as having no byte counters.
func addUDP(res *ProcessTraffic, udp []udpSocket) {
	if len(udp) == 0 {
		return
	}
	index := map[string]int{}
	for i, p := range res.Programs {
		index[p.Name] = i
	}
	type peerKey struct {
		program, addr string
		port          int
	}
	peers := map[peerKey]int{}
	for _, u := range udp {
		name := u.name
		if name == "" {
			name = "unknown"
		}
		i, ok := index[name]
		if !ok {
			res.Programs = append(res.Programs, ProgramTraffic{Name: name, PIDs: []int{}, Peers: []PeerTraffic{}})
			i = len(res.Programs) - 1
			index[name] = i
		}
		p := &res.Programs[i]
		for _, pid := range u.pids {
			if !containsInt(p.PIDs, pid) {
				p.PIDs = append(p.PIDs, pid)
			}
		}
		sort.Ints(p.PIDs)
		if u.state != "ESTAB" || u.peerAddr == "" {
			p.UDPUnconnected++
			continue
		}
		p.UDPConnected++
		peers[peerKey{name, u.peerAddr, u.peerPort}]++
	}
	for key, n := range peers {
		p := &res.Programs[index[key.program]]
		if len(p.Peers) >= peersShown+udpPeersShown {
			continue
		}
		p.Peers = append(p.Peers, PeerTraffic{Address: key.addr, Port: key.port, Protocol: "udp", Connections: n})
	}
	for i := range res.Programs {
		sort.SliceStable(res.Programs[i].Peers, func(a, b int) bool {
			pa, pb := res.Programs[i].Peers[a], res.Programs[i].Peers[b]
			if pa.Protocol != pb.Protocol {
				return pa.Protocol == "tcp"
			}
			if pa.Protocol == "udp" && pa.Connections != pb.Connections {
				return pa.Connections > pb.Connections
			}
			return false
		})
	}
}

// udpPeersShown is how many UDP peers a program lists beside its TCP ones.
const udpPeersShown = 3

// observe differences socks against the previous read and aggregates by
// program. Split from read so the arithmetic is tested without a clock or a
// command.
func (f *flowSampler) observe(now time.Time, socks []flowSocket) *ProcessTraffic {
	elapsed := now.Sub(f.at)
	warming := f.prev == nil || elapsed > staleAfter || f.at.IsZero()
	young := !warming && elapsed < youngWindow

	type agg struct {
		p     *ProgramTraffic
		pids  map[int]bool
		peers map[string]*PeerTraffic
		rtts  []float64
	}
	progs := map[string]*agg{}
	next := make(map[flowKey]flowSocket, len(socks))

	for _, sk := range socks {
		key := flowKey{sk.local, sk.peer}
		next[key] = sk
		name := sk.name
		if name == "" {
			name = "unknown"
		}
		a := progs[name]
		if a == nil {
			a = &agg{p: &ProgramTraffic{Name: name, PIDs: []int{}, Peers: []PeerTraffic{}}, pids: map[int]bool{}, peers: map[string]*PeerTraffic{}}
			progs[name] = a
		}
		a.p.Connections++
		a.p.RxTotal += sk.rx
		a.p.TxTotal += sk.tx
		for _, pid := range sk.pids {
			a.pids[pid] = true
		}
		if sk.hasRTT {
			a.rtts = append(a.rtts, sk.rttMs)
		}
		a.p.Retransmitted += sk.retransTotal
		a.p.SegmentsOut += sk.segsOut
		pk := sk.peerAddr + ":" + strconv.Itoa(sk.peerPort)
		peer := a.peers[pk]
		if peer == nil {
			peer = &PeerTraffic{Address: sk.peerAddr, Port: sk.peerPort, Protocol: "tcp", BytesKnown: true}
			a.peers[pk] = peer
		}
		peer.Connections++
		peer.RxBytes += sk.rx
		peer.TxBytes += sk.tx

		if warming {
			continue
		}
		var drx, dtx uint64
		old, seen := f.prev[key]
		switch {
		case seen && sk.rx >= old.rx && sk.tx >= old.tx:
			drx, dtx = sk.rx-old.rx, sk.tx-old.tx
		case young:
			// Not in the last read, or its counters went backwards because
			// the same four-tuple was reopened. Either way a socket that
			// appeared within seconds of the last read was almost certainly
			// opened inside the interval, so its bytes are the interval's.
			drx, dtx = sk.rx, sk.tx
		default:
			// A socket first seen after a long gap may have been open for
			// hours. Its first interval is skipped rather than drawn as a
			// burst; the next read has a baseline for it.
		}
		a.p.RxRate += float64(drx)
		a.p.TxRate += float64(dtx)
	}

	res := &ProcessTraffic{At: now, Warming: warming, Note: trafficNote, Programs: make([]ProgramTraffic, 0, len(progs))}
	if !warming {
		res.IntervalSeconds = elapsed.Seconds()
	}
	for _, a := range progs {
		if !warming && elapsed > 0 {
			a.p.RxRate /= elapsed.Seconds()
			a.p.TxRate /= elapsed.Seconds()
		}
		for pid := range a.pids {
			a.p.PIDs = append(a.p.PIDs, pid)
		}
		sort.Ints(a.p.PIDs)
		if len(a.rtts) > 0 {
			sort.Float64s(a.rtts)
			median := round3(quantile(a.rtts, 0.5))
			a.p.MedianRTTMs = &median
		}
		for _, peer := range a.peers {
			a.p.Peers = append(a.p.Peers, *peer)
		}
		sort.Slice(a.p.Peers, func(i, j int) bool {
			bi, bj := a.p.Peers[i].RxBytes+a.p.Peers[i].TxBytes, a.p.Peers[j].RxBytes+a.p.Peers[j].TxBytes
			if bi != bj {
				return bi > bj
			}
			return a.p.Peers[i].Address < a.p.Peers[j].Address
		})
		if len(a.p.Peers) > peersShown {
			a.p.Peers = a.p.Peers[:peersShown]
		}
		res.Programs = append(res.Programs, *a.p)
	}
	sort.Slice(res.Programs, func(i, j int) bool {
		pi, pj := res.Programs[i], res.Programs[j]
		ri, rj := pi.RxRate+pi.TxRate, pj.RxRate+pj.TxRate
		if ri != rj {
			return ri > rj
		}
		ti, tj := pi.RxTotal+pi.TxTotal, pj.RxTotal+pj.TxTotal
		if ti != tj {
			return ti > tj
		}
		return pi.Name < pj.Name
	})
	f.prev, f.at = next, now
	return res
}

// peersShown is how many remote ends each program lists.
const peersShown = 5

// ContainerFlow is one container's recorded traffic over a window.
type ContainerFlow struct {
	Name string `json:"name"`
	// RxBytes and TxBytes are what crossed the container's interfaces in the
	// window, summing every interval so a restart (which zeroes the counters)
	// loses nothing.
	RxBytes uint64 `json:"rxBytes"`
	TxBytes uint64 `json:"txBytes"`
	// RxRate and TxRate are bytes a second between the last two samples.
	RxRate float64 `json:"rxRate"`
	TxRate float64 `json:"txRate"`
	// Series is the rate over the window in at most sixty steps, for a
	// sparkline.
	Series []RatePoint `json:"series"`
	// LastSeen is the unix time of the container's newest sample.
	LastSeen int64 `json:"lastSeen"`
}

// RatePoint is a rate at an instant.
type RatePoint struct {
	TS int64   `json:"t"`
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

// ContainerTraffic is every container's traffic over a window.
type ContainerTraffic struct {
	WindowSeconds int64 `json:"windowSeconds"`
	// Recording is whether the recorder has taken any container sample at
	// all; false on a fresh install, and the page says when it will have some.
	Recording  bool            `json:"recording"`
	Containers []ContainerFlow `json:"containers"`
}

// seriesPoints is the most steps a container's series has.
const seriesPoints = 60

// Containers reads the recorder's container samples. Their net_rx and net_tx
// are cumulative counters, so a window's traffic is the sum of the increases
// between samples and a rate is an increase over the time between two.
func (s *Service) Containers(ctx context.Context, window time.Duration) (*ContainerTraffic, error) {
	return s.containers(ctx, window, time.Now())
}

func (s *Service) containers(ctx context.Context, window time.Duration, now time.Time) (*ContainerTraffic, error) {
	return s.containerFlows(ctx, window, now, "", seriesPoints)
}

// detailPoints is how many steps one container's own chart has: twice the
// sparkline's, since it is drawn across a panel rather than in a row.
const detailPoints = 120

// ContainerDetail is one container's recorded traffic over a window, at a
// finer grain than the list's sparkline. A container with no samples in the
// window is ErrNotFound.
func (s *Service) ContainerDetail(ctx context.Context, name string, window time.Duration) (*ContainerFlow, error) {
	res, err := s.containerFlows(ctx, window, time.Now(), name, detailPoints)
	if err != nil {
		return nil, err
	}
	for i := range res.Containers {
		if res.Containers[i].Name == name {
			return &res.Containers[i], nil
		}
	}
	return nil, fmt.Errorf("traffic of %s: %w", name, ErrNotFound)
}

func (s *Service) containerFlows(ctx context.Context, window time.Duration, now time.Time, only string, points int64) (*ContainerTraffic, error) {
	if window <= 0 {
		window = time.Hour
	}
	if window > 31*24*time.Hour {
		window = 31 * 24 * time.Hour
	}
	res := &ContainerTraffic{WindowSeconds: int64(window.Seconds()), Containers: []ContainerFlow{}}
	if s.db == nil {
		return res, nil
	}
	var probe int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM metric_container_samples LIMIT 1`).Scan(&probe)
	switch {
	case err == sql.ErrNoRows:
		return res, nil
	case err != nil:
		return nil, fmt.Errorf("reading container samples: %w", err)
	}
	res.Recording = true

	from := now.Add(-window).Unix()
	// Both ends of the window are included; leave room for a partial bucket
	// at each end instead of sometimes returning a sixty-first point.
	step := (int64(window.Seconds()) + points - 2) / (points - 1)
	if step < 1 {
		step = 1
	}
	// Differenced and bucketed in SQL: a week of samples at the recorder's
	// grain is hundreds of thousands of rows per container, and a sparkline
	// of sixty points wants sixty. The increase between consecutive samples is
	// taken per row, where a counter that went backwards (the container
	// restarted) is read as what it has carried since, and only then summed
	// into buckets — differencing bucket maxima would lose a restart that
	// fell inside one.
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		WITH d AS (
		  SELECT name, ts, net_rx, net_tx,
		         LAG(net_rx) OVER w AS prx, LAG(net_tx) OVER w AS ptx
		    FROM metric_container_samples
		   WHERE ts >= ? AND ts <= ? AND (? = '' OR name = ?)
		     AND COALESCE(network_available, net_rx > 0 OR net_tx > 0)
		  WINDOW w AS (PARTITION BY name ORDER BY ts))
		SELECT name, MIN(ts), MAX(ts),
		       SUM(CASE WHEN prx IS NULL THEN 0 WHEN net_rx >= prx THEN net_rx - prx ELSE net_rx END),
		       SUM(CASE WHEN ptx IS NULL THEN 0 WHEN net_tx >= ptx THEN net_tx - ptx ELSE net_tx END)
		  FROM d GROUP BY name, ts / %d ORDER BY name, ts / %d`, step, step), from, now.Unix(), only, only)
	if err != nil {
		return nil, fmt.Errorf("reading container samples: %w", err)
	}
	defer rows.Close()
	byName := map[string]*ContainerFlow{}
	lastTS := map[string]int64{}
	var order []string
	for rows.Next() {
		var name string
		var first, last, rx, tx int64
		if err := rows.Scan(&name, &first, &last, &rx, &tx); err != nil {
			return nil, err
		}
		c := byName[name]
		if c == nil {
			c = &ContainerFlow{Name: name, Series: []RatePoint{}}
			byName[name] = c
			order = append(order, name)
		}
		c.RxBytes += uint64(rx)
		c.TxBytes += uint64(tx)
		// A bucket's bytes accrued since the previous bucket's last sample;
		// the first bucket's, since its own first.
		since := first
		if prev, ok := lastTS[name]; ok {
			since = prev
		}
		if dt := float64(last - since); dt > 0 {
			c.Series = append(c.Series, RatePoint{TS: last, Rx: float64(rx) / dt, Tx: float64(tx) / dt})
		}
		lastTS[name] = last
		c.LastSeen = last
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// The current rate wants the last two raw samples, which the buckets have
	// averaged away.
	last, err := s.db.QueryContext(ctx, `
		SELECT name, ts, net_rx, net_tx FROM (
		  SELECT name, ts, net_rx, net_tx,
		         ROW_NUMBER() OVER (PARTITION BY name ORDER BY ts DESC) AS n
		    FROM metric_container_samples
		   WHERE ts >= ? AND ts <= ? AND (? = '' OR name = ?)
		     AND COALESCE(network_available, net_rx > 0 OR net_tx > 0))
		 WHERE n <= 2 ORDER BY name, ts`, from, now.Unix(), only, only)
	if err != nil {
		return nil, fmt.Errorf("reading container samples: %w", err)
	}
	defer last.Close()
	type reading struct{ ts, rx, tx int64 }
	tail := map[string]reading{}
	freshFor := staleAfter
	if s.sampler != nil && 2*s.sampler.every > freshFor {
		freshFor = 2 * s.sampler.every
	}
	for last.Next() {
		var name string
		var r reading
		if err := last.Scan(&name, &r.ts, &r.rx, &r.tx); err != nil {
			return nil, err
		}
		if p, ok := tail[name]; ok && r.ts > p.ts && now.Sub(time.Unix(r.ts, 0)) <= freshFor {
			if c := byName[name]; c != nil {
				dt := float64(r.ts - p.ts)
				c.RxRate = float64(counterDelta(p.rx, r.rx)) / dt
				c.TxRate = float64(counterDelta(p.tx, r.tx)) / dt
			}
		}
		tail[name] = r
	}
	if err := last.Err(); err != nil {
		return nil, err
	}

	for _, name := range order {
		res.Containers = append(res.Containers, *byName[name])
	}
	sort.Slice(res.Containers, func(i, j int) bool {
		a, b := res.Containers[i], res.Containers[j]
		if at, bt := a.RxBytes+a.TxBytes, b.RxBytes+b.TxBytes; at != bt {
			return at > bt
		}
		return a.Name < b.Name
	})
	return res, nil
}

// counterDelta is the increase of a cumulative counter between two readings.
// A reading below the last one means the container restarted and the counter
// began again, so what it shows now is what it has carried since.
func counterDelta(prev, cur int64) int64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}
