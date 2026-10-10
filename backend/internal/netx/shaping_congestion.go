package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Whether BBR helped, from this host's own traffic.
//
// Switching the congestion control is one sysctl, and it proves nothing: a
// socket keeps the algorithm it was opened with, so right after the switch
// the host runs both, and a figure averaged over all of them describes
// neither. ss names each socket's algorithm beside TCP's own round-trip
// estimate, its retransmissions and the rate it last delivered at, so the
// live sockets fold into one group per algorithm and the groups compare on
// the same traffic at the same moment. When the switch is made, the groups
// as they were are kept, so "before" is a reading rather than a memory.
//
// It is a comparison of whatever this host happened to be doing, not a
// controlled experiment: different peers, paths and workloads sit in each
// group, and the page says so.

// CongestionGroup is the established TCP sockets running one algorithm.
type CongestionGroup struct {
	Algorithm string  `json:"algorithm"`
	Sockets   int     `json:"sockets"`
	MedianRTT float64 `json:"medianRttMs"`
	P90RTT    float64 `json:"p90RttMs"`
	// RetransmitShare is resent segments over sent segments, summed over the
	// group's live sockets since each opened.
	RetransmitShare float64 `json:"retransmitShare"`
	// MedianDeliveryMbit is the median of the rate each socket last
	// delivered at, where ss reports one.
	MedianDeliveryMbit float64 `json:"medianDeliveryMbit"`
	SegmentsOut        uint64  `json:"segmentsOut"`
	BytesSent          uint64  `json:"bytesSent"`
}

// CongestionComparison is every algorithm's group at one read.
type CongestionComparison struct {
	At time.Time `json:"at"`
	// Default is what new sockets get now.
	Default   string            `json:"default"`
	Groups    []CongestionGroup `json:"groups"`
	Loopback  int               `json:"loopback"`
	Truncated bool              `json:"truncated"`
	Error     string            `json:"error,omitempty"`
}

// CongestionSnapshot is a comparison kept when the switch was made.
type CongestionSnapshot struct {
	ID         int64                `json:"id"`
	At         time.Time            `json:"at"`
	Before     string               `json:"before"`
	After      string               `json:"after"`
	Actor      string               `json:"actor,omitempty"`
	Comparison CongestionComparison `json:"comparison"`
}

// CongestionView is the comparison now with the ones kept at each switch.
type CongestionView struct {
	Now       CongestionComparison `json:"now"`
	Snapshots []CongestionSnapshot `json:"snapshots"`
	Note      string               `json:"note"`
}

const congestionNote = "Each group is whatever this host's sockets were doing at the read: different peers, paths and workloads, not a controlled test. A socket keeps the algorithm it opened with."

// congestionKept bounds the snapshots kept.
const congestionKept = 20

type congestionReader struct {
	mu   sync.Mutex
	last *CongestionComparison
}

// groupCongestion folds sockets by algorithm, loopback left out.
func groupCongestion(socks []flowSocket, truncated bool, now time.Time, def string) CongestionComparison {
	c := CongestionComparison{At: now.UTC(), Default: def, Truncated: truncated, Groups: []CongestionGroup{}}
	type acc struct {
		g          CongestionGroup
		rtts, rate []float64
		retrans    uint64
	}
	groups := map[string]*acc{}
	for _, sk := range socks {
		if loopbackPeer(sk.peerAddr) {
			c.Loopback++
			continue
		}
		name := sk.congestion
		if name == "" {
			name = "unknown"
		}
		a := groups[name]
		if a == nil {
			a = &acc{g: CongestionGroup{Algorithm: name}}
			groups[name] = a
		}
		a.g.Sockets++
		a.g.SegmentsOut += sk.segsOut
		a.g.BytesSent += sk.tx
		a.retrans += sk.retransTotal
		if sk.hasRTT {
			a.rtts = append(a.rtts, sk.rttMs)
		}
		if sk.hasDelivery {
			a.rate = append(a.rate, sk.deliveryBps/1e6)
		}
	}
	for _, a := range groups {
		sort.Float64s(a.rtts)
		sort.Float64s(a.rate)
		a.g.MedianRTT = round3(quantile(a.rtts, 0.5))
		a.g.P90RTT = round3(quantile(a.rtts, 0.9))
		a.g.MedianDeliveryMbit = math.Round(quantile(a.rate, 0.5)*100) / 100
		if a.g.SegmentsOut > 0 {
			a.g.RetransmitShare = math.Round(float64(a.retrans)/float64(a.g.SegmentsOut)*1e5) / 1e5
		}
		c.Groups = append(c.Groups, a.g)
	}
	sort.Slice(c.Groups, func(i, j int) bool {
		if c.Groups[i].Sockets != c.Groups[j].Sockets {
			return c.Groups[i].Sockets > c.Groups[j].Sockets
		}
		return c.Groups[i].Algorithm < c.Groups[j].Algorithm
	})
	return c
}

// readCongestion reads the live sockets, at most every ten seconds.
func (s *Service) readCongestion(ctx context.Context, fresh bool) CongestionComparison {
	s.congestion.mu.Lock()
	defer s.congestion.mu.Unlock()
	now := time.Now()
	if l := s.congestion.last; l != nil && !fresh && now.Sub(l.At) < latencyEvery {
		return *l
	}
	def, _ := gatewayReadSysctl(bbrCongestionKey)
	c := CongestionComparison{At: now.UTC(), Default: def, Groups: []CongestionGroup{}}
	if !has("ss") {
		c.Error = "ss is not installed (iproute2)"
	} else if out, err := run(ctx, "ss", "-tinH", "state", "established"); err != nil {
		c.Error = fmt.Sprintf("reading the TCP sockets: %v", err)
	} else {
		socks, truncated := parseSS(out)
		c = groupCongestion(socks, truncated, now, def)
	}
	s.congestion.last = &c
	return c
}

// Congestion is the comparison now and the snapshots kept at each switch.
func (s *Service) Congestion(ctx context.Context) (*CongestionView, error) {
	v := &CongestionView{Now: s.readCongestion(ctx, false), Snapshots: []CongestionSnapshot{}, Note: congestionNote}
	if s.db == nil {
		return v, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, before_algorithm, after_algorithm, actor, payload
		  FROM network_congestion_snapshots ORDER BY at DESC, id DESC LIMIT ?`, congestionKept)
	if err != nil {
		return nil, fmt.Errorf("reading the congestion snapshots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var snap CongestionSnapshot
		var at int64
		var payload string
		if err := rows.Scan(&snap.ID, &at, &snap.Before, &snap.After, &snap.Actor, &payload); err != nil {
			return nil, err
		}
		snap.At = time.Unix(at, 0).UTC()
		if json.Unmarshal([]byte(payload), &snap.Comparison) != nil {
			snap.Comparison = CongestionComparison{Error: "this snapshot could not be read", Groups: []CongestionGroup{}}
		}
		v.Snapshots = append(v.Snapshots, snap)
	}
	return v, rows.Err()
}

// keepCongestion stores the comparison taken just before a switch, and
// prunes to the newest few. A failure is logged: the switch has happened.
func (s *Service) keepCongestion(ctx context.Context, before CongestionComparison, from, to, actor string) {
	if s.db == nil {
		return
	}
	payload, _ := json.Marshal(before)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO network_congestion_snapshots (at, before_algorithm, after_algorithm, actor, payload)
		VALUES (?, ?, ?, ?, ?)`, before.At.Unix(), from, to, actor, string(payload)); err != nil {
		s.log.Warn("keeping the congestion comparison", "err", err)
		return
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM network_congestion_snapshots WHERE id NOT IN
		(SELECT id FROM network_congestion_snapshots ORDER BY at DESC, id DESC LIMIT ?)`, congestionKept); err != nil {
		s.log.Warn("pruning the congestion comparisons", "err", err)
	}
}
