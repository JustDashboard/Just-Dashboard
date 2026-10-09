package metrics

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// Link counters say a link is losing packets; TCP's own counters say
// connections are struggling whatever the link reports — a congested path
// past the uplink, a peer that stopped answering, an application whose accept
// queue is full. They are judged the way the links are: over a window
// between two health checks, never on the since-boot totals, which carry
// every incident the host has had.
const (
	// A window's retransmissions are judged on a count and a share, so a
	// quiet host resending three segments of a hundred is not a finding and
	// a busy one resending a thousand of ten million is not either.
	retransNoticeShare, retransNoticeFloor   = 2.0, 200
	retransWarningShare, retransWarningFloor = 5.0, 1000
	// Every listen drop is a connection a client made and the host refused
	// for want of room in an accept queue.
	listenDropWarnFloor = 10
	// Failed attempts include half-open scans against a public host, so they
	// are a notice, and only when they are a real share of the attempts.
	attemptFailFloor = 50
	attemptFailShare = 20.0
	// Latency is a notice when the median RTT of established connections is
	// both slow in itself and several times what the last hour recorded.
	latencySlowMs       = 150
	latencyBaselineX    = 3.0
	latencyBaselineSpan = 10
)

type tcpCounters struct {
	c  sysinfo.TCPCounters
	at time.Time
}

// tcpDelta is the counters' growth over one completed window.
type tcpDelta struct {
	retrans, outSegs, attemptFails, opens, listenDrops, resets uint64
	span                                                       time.Duration
}

func (d tcpDelta) retransPercent() float64 {
	if d.outSegs == 0 {
		return 0
	}
	return float64(d.retrans) / float64(d.outSegs) * 100
}

// tcpWatch keeps the baseline between checks, as linkWatch does for links.
type tcpWatch struct {
	mu   sync.Mutex
	base *tcpCounters
	last *tcpDelta
}

func (w *tcpWatch) observe(s sysinfo.TCPStats, now time.Time) *tcpDelta {
	if !s.Supported {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	cur := tcpCounters{c: s.Counters, at: now}
	switch {
	case w.base == nil:
		w.base = &cur
	case backwards(w.base.c, cur.c) || now.Sub(w.base.at) > linkStale:
		w.base, w.last = &cur, nil
	case now.Sub(w.base.at) >= linkWindow:
		b := w.base.c
		w.last = &tcpDelta{
			retrans: cur.c.RetransSegs - b.RetransSegs, outSegs: cur.c.OutSegs - b.OutSegs,
			attemptFails: cur.c.AttemptFails - b.AttemptFails,
			opens:        cur.c.ActiveOpens - b.ActiveOpens + cur.c.PassiveOpens - b.PassiveOpens,
			listenDrops:  cur.c.ListenDrops - b.ListenDrops, resets: cur.c.EstabResets - b.EstabResets,
			span: now.Sub(w.base.at),
		}
		w.base = &cur
	}
	return w.last
}

func backwards(a, b sysinfo.TCPCounters) bool {
	return b.RetransSegs < a.RetransSegs || b.OutSegs < a.OutSegs || b.AttemptFails < a.AttemptFails ||
		b.ActiveOpens < a.ActiveOpens || b.PassiveOpens < a.PassiveOpens || b.ListenDrops < a.ListenDrops ||
		b.EstabResets < a.EstabResets
}

// tcpFindings judges the window and the latency of established connections.
func tcpFindings(snap *sysinfo.Snapshot, w *tcpDelta, recent *Series) []Finding {
	var out []Finding
	if w != nil {
		share := w.retransPercent()
		evidence := []Fact{
			{Label: "Retransmitted", Value: thousands(w.retrans)},
			{Label: "Share of segments", Value: percent(share)},
			{Label: "Window", Value: spanShort(w.span)},
		}
		detail := fmt.Sprintf("%s of %s segments sent in the last %s were retransmissions (%s)",
			thousands(w.retrans), thousands(w.outSegs), spanWords(w.span), percent(share))
		advice := "TCP resends what was not acknowledged: loss or congestion somewhere on the path, or a peer that stopped answering. Check the links' drops first; with clean links the loss is past this host, and a saved ping or path investigation from here shows where."
		switch {
		case share >= retransWarningShare && w.retrans >= retransWarningFloor:
			out = append(out, Finding{ID: "tcp:retransmits", Level: "warning", Title: "Connections are resending a lot",
				Detail: detail, Advice: advice, Metric: "tcpRetrans", Value: share, Threshold: retransWarningShare,
				Area: AreaNetwork, Evidence: evidence})
		case share >= retransNoticeShare && w.retrans >= retransNoticeFloor:
			out = append(out, Finding{ID: "tcp:retransmits", Level: "notice", Title: "Connections are resending more than usual",
				Detail: detail, Advice: advice, Metric: "tcpRetrans", Value: share, Threshold: retransNoticeShare,
				Area: AreaNetwork, Evidence: evidence})
		}
		if w.listenDrops >= listenDropWarnFloor {
			out = append(out, Finding{ID: "tcp:listen-drops", Level: "warning", Title: "Connections are refused at a full accept queue",
				Detail: fmt.Sprintf("%s incoming connections were dropped in the last %s because a listening socket's queue was full", thousands(w.listenDrops), spanWords(w.span)),
				Advice: "A service is not accepting connections as fast as they arrive. Find the busy listener on the Ports page; raising its backlog only helps if the service then keeps up.",
				Metric: "tcpListenDrops", Value: float64(w.listenDrops), Threshold: listenDropWarnFloor, Area: AreaNetwork,
				Evidence: []Fact{{Label: "Dropped at accept", Value: thousands(w.listenDrops)}, {Label: "Window", Value: spanShort(w.span)}}})
		}
		if w.opens > 0 {
			failShare := float64(w.attemptFails) / float64(w.opens) * 100
			if w.attemptFails >= attemptFailFloor && failShare >= attemptFailShare {
				out = append(out, Finding{ID: "tcp:attempt-fails", Level: "notice", Title: "Many connection attempts are failing",
					Detail: fmt.Sprintf("%s of %s connection attempts in the last %s failed before they were established (%s)",
						thousands(w.attemptFails), thousands(w.opens), spanWords(w.span), percent(failShare)),
					Advice: "Outbound, a dependency is refusing or not answering; inbound, half-open scans count here too, so compare with the firewall log before chasing a service.",
					Metric: "tcpAttemptFails", Value: failShare, Threshold: attemptFailShare, Area: AreaNetwork,
					Evidence: []Fact{{Label: "Failed", Value: thousands(w.attemptFails)}, {Label: "Attempts", Value: thousands(w.opens)}, {Label: "Window", Value: spanShort(w.span)}}})
			}
		}
	}
	lat := snap.TCP.Latency
	if lat.Sockets > 0 && lat.MedianMs >= latencySlowMs {
		if base, n := baselineRTT(recent); n >= latencyBaselineSpan && base > 0 && lat.MedianMs >= base*latencyBaselineX {
			out = append(out, Finding{ID: "tcp:latency", Level: "notice", Title: "Connections are slower than this hour has been",
				Detail: fmt.Sprintf("Established connections' median round trip is %.0f ms against %.0f ms over the last hour", lat.MedianMs, base),
				Advice: "The kernel's own RTT of live connections rose; nothing here sent a probe. If the links are clean the change is on the path or at the peers — a saved ping or path investigation from this server shows which.",
				Metric: "tcpRtt", Value: lat.MedianMs, Threshold: base * latencyBaselineX, Area: AreaNetwork,
				Evidence: []Fact{{Label: "Median now", Value: fmt.Sprintf("%.0f ms", lat.MedianMs)}, {Label: "90th percentile", Value: fmt.Sprintf("%.0f ms", lat.P90Ms)},
					{Label: "Hour's mean", Value: fmt.Sprintf("%.0f ms", base)}, {Label: "Connections", Value: thousands(uint64(lat.Sockets))}}})
		}
	}
	return out
}

// baselineRTT is the mean of the recorded medians over the recent window and
// how many buckets had one.
func baselineRTT(recent *Series) (float64, int) {
	if recent == nil {
		return 0, 0
	}
	sum, n := 0.0, 0
	for _, p := range recent.Points {
		if p.RTT != nil {
			sum += *p.RTT
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// tcpSummary is the network area's TCP reading, appended to the links'.
func tcpSummary(snap *sysinfo.Snapshot, w *tcpDelta) string {
	parts := ""
	if w != nil && w.outSegs > 0 {
		parts = fmt.Sprintf("%s resent", percent(w.retransPercent()))
	}
	if snap.TCP.Latency.Sockets > 0 {
		if parts != "" {
			parts += " · "
		}
		parts += fmt.Sprintf("RTT %.0f ms", snap.TCP.Latency.MedianMs)
	}
	return parts
}

// ProbeEvidence is a saved diagnostic run, as the health verdict cites it.
type ProbeEvidence struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Tool    string    `json:"tool"`
	Target  string    `json:"target,omitempty"`
	Outcome string    `json:"outcome"`
	EndedAt time.Time `json:"endedAt"`
}

// probeWindow is how far back a probe is read as describing now.
const probeWindow = 30 * time.Minute

// probeFailed is an outcome that says the probe met trouble; a completed run
// and one the operator cancelled describe nothing about the network.
func probeFailed(outcome string) bool {
	switch outcome {
	case "completed", "cancelled", "interrupted", "unsupported", "permission_denied":
		return false
	}
	return true
}

// CorrelateProbes joins saved diagnostic runs from the last half hour to the
// network verdict. Network findings cite the probes that met trouble in the
// same stretch; with clean host evidence and failing probes, a notice says the
// trouble is past this host — the one conclusion neither half reaches alone.
func CorrelateProbes(h *Health, runs []ProbeEvidence, now time.Time) {
	var failed []ProbeEvidence
	for _, r := range runs {
		if !r.EndedAt.IsZero() && now.Sub(r.EndedAt) <= probeWindow && r.EndedAt.Before(now.Add(time.Minute)) && probeFailed(r.Outcome) {
			failed = append(failed, r)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].EndedAt.After(failed[j].EndedAt) })
	if len(failed) > 5 {
		failed = failed[:5]
	}
	networkFinding := false
	for i := range h.Findings {
		if h.Findings[i].Area != AreaNetwork {
			continue
		}
		networkFinding = true
		if len(failed) > 0 {
			h.Findings[i].Correlated = failed
			h.Findings[i].Evidence = append(h.Findings[i].Evidence, Fact{Label: "Probes in trouble", Value: fmt.Sprintf("%d in %s", len(failed), spanShort(probeWindow))})
		}
	}
	if !networkFinding && len(failed) >= 2 {
		h.Findings = append(h.Findings, Finding{ID: "probes:beyond-host", Level: "notice", Area: AreaNetwork,
			Title:  "Probes from this server are failing while its own network reads clean",
			Detail: fmt.Sprintf("%d saved diagnostic runs met trouble in the last %s; the links and TCP counters show nothing in the same stretch", len(failed), spanWords(probeWindow)),
			Advice: "The trouble is likely past this host: the path, the provider or the targets. Open the runs to see which stage failed.",
			Value:  float64(len(failed)), Threshold: 2, Correlated: failed,
			Evidence: []Fact{{Label: "Runs in trouble", Value: fmt.Sprintf("%d", len(failed))}, {Label: "Window", Value: spanShort(probeWindow)}}})
		h.Settle()
	}
}
