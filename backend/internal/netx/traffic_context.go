package netx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What the byte rates cannot say on their own.
//
// A device moving 40 MB/s is either healthy or retransmitting a third of what
// it sends, and its chart is the same line either way. The host's TCP counters
// say which, at the same two-second step, for the price of one file read: how
// many segments left, how many of them were sent again, how many connections
// opened and how many were reset. Latency is TCP's own smoothed round-trip
// estimate on the live sockets, read with ss at most every ten seconds and
// only while somebody is looking — it is what the kernel measured on real
// traffic, not a probe the dashboard sends.

// procNetSNMP is the host's protocol counters. A variable for tests.
var procNetSNMP = func() (io.ReadCloser, error) { return os.Open("/proc/net/snmp") }

// tcpCounters is the Tcp line of /proc/net/snmp, cumulative since boot.
type tcpCounters struct {
	activeOpens, passiveOpens, attemptFails, estabResets uint64
	currEstab, outSegs, retransSegs, outRsts             uint64
}

// parseTCPCounters reads the header and value lines that both start "Tcp:".
func parseTCPCounters(r io.Reader) (*tcpCounters, error) {
	sc := bufio.NewScanner(r)
	var header []string
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "Tcp:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Tcp:"))
		if header == nil {
			header = fields
			continue
		}
		if len(fields) != len(header) {
			return nil, errors.New("the Tcp counters do not match their header")
		}
		values := map[string]uint64{}
		for i, name := range header {
			// CurrEstab is a gauge and MaxConn is -1; neither is a counter
			// that can go backwards meaningfully, and both parse as signed.
			n, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil || n < 0 {
				continue
			}
			values[name] = uint64(n)
		}
		return &tcpCounters{
			activeOpens: values["ActiveOpens"], passiveOpens: values["PassiveOpens"],
			attemptFails: values["AttemptFails"], estabResets: values["EstabResets"],
			currEstab: values["CurrEstab"], outSegs: values["OutSegs"],
			retransSegs: values["RetransSegs"], outRsts: values["OutRsts"],
		}, nil
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("the kernel printed no Tcp counters")
}

func readTCPCounters() (*tcpCounters, error) {
	f, err := procNetSNMP()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseTCPCounters(f)
}

// TCPPoint is the host's TCP over one two-second step, every rate a second.
type TCPPoint struct {
	TS      int64   `json:"t"`
	OutSegs float64 `json:"outSegs"`
	// Retrans is segments sent again; over OutSegs it is the share of what
	// left that had to be repeated, which is what loss or a full queue costs.
	Retrans float64 `json:"retrans"`
	// Opens counts connections begun in both directions; Failed the attempts
	// that never completed; Resets the established ones torn down by a reset.
	Opens  float64 `json:"opens"`
	Failed float64 `json:"failed"`
	Resets float64 `json:"resets"`
	// Established is a gauge: the connections open at the end of the step.
	Established uint64 `json:"established"`
}

// observeTCP appends a step of TCP rates, keeping the same fifteen minutes as
// the devices. A failed read is remembered and reported rather than drawn as
// a quiet host.
func (s *Sampler) observeTCP(now time.Time, cur *tcpCounters, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.tcpError = err.Error()
		s.tcpLast = nil
		return
	}
	s.tcpError = ""
	if prev := s.tcpLast; prev != nil && !s.tcpAt.IsZero() {
		if elapsed := now.Sub(s.tcpAt).Seconds(); elapsed > 0 {
			p := TCPPoint{
				TS:          now.Unix(),
				OutSegs:     rate(prev.outSegs, cur.outSegs, elapsed),
				Retrans:     rate(prev.retransSegs, cur.retransSegs, elapsed),
				Opens:       rate(prev.activeOpens+prev.passiveOpens, cur.activeOpens+cur.passiveOpens, elapsed),
				Failed:      rate(prev.attemptFails, cur.attemptFails, elapsed),
				Resets:      rate(prev.estabResets, cur.estabResets, elapsed),
				Established: cur.currEstab,
			}
			s.tcpRing = append(s.tcpRing, p)
			if len(s.tcpRing) > liveKeep {
				s.tcpRing = s.tcpRing[len(s.tcpRing)-liveKeep:]
			}
		}
	}
	s.tcpLast, s.tcpAt = cur, now
}

// LiveTraffic is the two-second readings since a moment, with how old the
// newest one is. A page that keeps drawing the last answer it got cannot
// otherwise tell a quiet link from a sampler that stopped.
type LiveTraffic struct {
	Now int64 `json:"now"`
	// SampledAt is when the newest reading was taken, in unix seconds; zero
	// before the first.
	SampledAt   int64                  `json:"sampledAt"`
	StepSeconds int                    `json:"stepSeconds"`
	Series      map[string][]LivePoint `json:"series"`
	TCP         []TCPPoint             `json:"tcp"`
	TCPError    string                 `json:"tcpError,omitempty"`
	Latency     *TCPLatency            `json:"latency,omitempty"`
}

// LiveView is every device's ring and the TCP ring since a moment.
func (s *Sampler) LiveView(since int64, now time.Time) LiveTraffic {
	v := LiveTraffic{Now: now.Unix(), StepSeconds: int(liveStep.Seconds()), Series: s.Live(since), TCP: []TCPPoint{}}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.at.IsZero() {
		v.SampledAt = s.at.Unix()
	}
	i := sort.Search(len(s.tcpRing), func(i int) bool { return s.tcpRing[i].TS > since })
	v.TCP = append(v.TCP, s.tcpRing[i:]...)
	v.TCPError = s.tcpError
	return v
}

// TCPLatency is the round-trip time TCP measured on the host's established
// sockets, loopback left out: a machine talking to itself in microseconds
// would otherwise be most of the median on any host running a proxy.
type TCPLatency struct {
	At       time.Time `json:"at"`
	Sockets  int       `json:"sockets"`
	MedianMs float64   `json:"medianMs"`
	P90Ms    float64   `json:"p90Ms"`
	MaxMs    float64   `json:"maxMs"`
	// Retransmitting is how many sockets had segments outstanding for
	// retransmission at the read.
	Retransmitting int    `json:"retransmitting"`
	Loopback       int    `json:"loopback"`
	Truncated      bool   `json:"truncated"`
	Error          string `json:"error,omitempty"`
}

// latencyEvery is how stale a latency read may be before the next poll takes
// another. A busy proxy has thousands of sockets; reading them every two
// seconds for a figure that moves slowly would be most of what ss costs here.
const latencyEvery = 10 * time.Second

type latencySampler struct {
	mu   sync.Mutex
	last *TCPLatency
}

// TCPLatency answers the latest latency read, taking a new one when the last
// is older than ten seconds.
func (s *Service) TCPLatency(ctx context.Context) *TCPLatency {
	s.latency.mu.Lock()
	defer s.latency.mu.Unlock()
	now := time.Now()
	if l := s.latency.last; l != nil && now.Sub(l.At) < latencyEvery {
		cp := *l
		return &cp
	}
	l := &TCPLatency{At: now.UTC()}
	if !has("ss") {
		l.Error = "ss is not installed (iproute2)"
	} else if out, err := run(ctx, "ss", "-tinH", "state", "established"); err != nil {
		l.Error = fmt.Sprintf("reading the TCP sockets: %v", err)
	} else {
		socks, truncated := parseSS(out)
		*l = summariseLatency(socks, truncated, now)
	}
	s.latency.last = l
	cp := *l
	return &cp
}

// summariseLatency is TCPLatency's arithmetic, separate so tests feed it sockets.
func summariseLatency(socks []flowSocket, truncated bool, now time.Time) TCPLatency {
	l := TCPLatency{At: now.UTC(), Truncated: truncated}
	var rtts []float64
	for _, sk := range socks {
		if loopbackPeer(sk.peerAddr) {
			l.Loopback++
			continue
		}
		if sk.retransOut > 0 {
			l.Retransmitting++
		}
		if sk.hasRTT {
			rtts = append(rtts, sk.rttMs)
		}
	}
	l.Sockets = len(rtts)
	if len(rtts) == 0 {
		return l
	}
	sort.Float64s(rtts)
	l.MedianMs = round3(quantile(rtts, 0.5))
	l.P90Ms = round3(quantile(rtts, 0.9))
	l.MaxMs = round3(rtts[len(rtts)-1])
	return l
}

func loopbackPeer(addr string) bool {
	a, err := netip.ParseAddr(strings.SplitN(addr, "%", 2)[0])
	return err == nil && a.Unmap().IsLoopback()
}

// quantile is the nearest-rank quantile of an ascending slice.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// SocketCounters is one established TCP socket's counters as ss prints them,
// for joining onto a connection table read elsewhere.
type SocketCounters struct {
	LocalAddr     string   `json:"localAddress"`
	LocalPort     int      `json:"localPort"`
	PeerAddr      string   `json:"remoteAddress"`
	PeerPort      int      `json:"remotePort"`
	RxBytes       uint64   `json:"rxBytes"`
	TxBytes       uint64   `json:"txBytes"`
	RTTMs         *float64 `json:"rttMs,omitempty"`
	Retransmitted uint64   `json:"retransmitted"`
	Congestion    string   `json:"congestion,omitempty"`
}

// TCPSocketsTo reads the established TCP sockets to one address with their
// byte counters, round-trip estimate and retransmissions. The address is
// parsed before it becomes an argument; ss's own filter keeps the read small
// on a host with thousands of sockets.
func (s *Service) TCPSocketsTo(ctx context.Context, addr netip.Addr) ([]SocketCounters, bool, error) {
	if !has("ss") {
		return nil, false, &UnavailableError{Tool: "ss", Package: "iproute2"}
	}
	target := addr.Unmap()
	host := target.String()
	if target.Is6() {
		host = "[" + host + "]"
	}
	out, err := run(ctx, "ss", "-tinH", "state", "established", "dst", host)
	if err != nil {
		return nil, false, fmt.Errorf("reading the TCP sockets: %w", err)
	}
	socks, truncated := parseSS(out)
	list := make([]SocketCounters, 0, len(socks))
	for _, sk := range socks {
		la, lp := splitHostPort(sk.local)
		c := SocketCounters{LocalAddr: unmapAddr(la), LocalPort: lp, PeerAddr: unmapAddr(sk.peerAddr), PeerPort: sk.peerPort,
			RxBytes: sk.rx, TxBytes: sk.tx, Retransmitted: sk.retransTotal, Congestion: sk.congestion}
		if sk.hasRTT {
			rtt := sk.rttMs
			c.RTTMs = &rtt
		}
		list = append(list, c)
	}
	return list, truncated, nil
}

func unmapAddr(s string) string {
	if a, err := netip.ParseAddr(strings.SplitN(s, "%", 2)[0]); err == nil {
		return a.Unmap().String()
	}
	return s
}
