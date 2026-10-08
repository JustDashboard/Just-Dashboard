package metrics

import (
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

// Packet loss is judged over a window between two health checks rather than
// on the kernel's since-boot counters, which hold every drop the interface
// has ever had: an outage last month and a link failing now read the same.
const (
	// A window closes once its baseline is this old. Health is polled about
	// once a minute, so a window is usually one or two polls wide.
	linkWindow = time.Minute
	// A baseline older than this describes a stretch nobody was watching, and
	// reporting it would put a past incident on screen as a present one.
	linkStale = 10 * time.Minute

	// A physical link is judged on both a count and a share, so a quiet link
	// losing three packets of two hundred is not a finding and a busy one
	// losing a hundred of ten million is not either.
	physicalDropFloor  = 100
	physicalDropShare  = 1.0
	physicalErrorFloor = 10
	// A tunnel drops whatever it cannot deliver to an absent peer, which is
	// routine, so only heavy loss is worth a notice.
	tunnelDropFloor = 500
	tunnelDropShare = 5.0
)

// linkCounters is one interface's cumulative counters at one check.
type linkCounters struct {
	drops, errors, packets uint64
	at                     time.Time
}

// linkDelta is the counters' growth over one completed window.
type linkDelta struct {
	drops, errors, packets uint64
	span                   time.Duration
}

// lossPercent is the share of packets in the window that were dropped. Drops
// are not counted among an interface's packets, so they are added back to
// get the whole of what passed.
func (d linkDelta) lossPercent() float64 {
	total := d.packets + d.drops
	if total == 0 {
		return 0
	}
	return float64(d.drops) / float64(total) * 100
}

// linkWatch keeps the baselines between checks. It lives on the Recorder,
// which outlives every request; a restart simply begins a new window.
type linkWatch struct {
	mu   sync.Mutex
	base map[string]linkCounters
	last map[string]linkDelta
}

// linkWindows is what one check saw: the latest completed window of every
// judged interface that has one, and how many interfaces were judged at all.
type linkWindows struct {
	windows map[string]linkDelta
	judged  int
}

// kind is the interface's class, read from the snapshot where the collector
// already classified it.
func (linkWindows) kind(n sysinfo.NetStats) string {
	if n.Kind != "" {
		return n.Kind
	}
	return netsec.ClassifyInterface(n.Interface)
}

// observe advances every judged interface's window and returns the latest
// completed ones. Between completions the last window stands, so a check a
// few seconds after another reports what that one did rather than nothing.
func (w *linkWatch) observe(nets []sysinfo.NetStats, now time.Time) linkWindows {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.base == nil {
		w.base = map[string]linkCounters{}
		w.last = map[string]linkDelta{}
	}
	out := linkWindows{windows: map[string]linkDelta{}}
	seen := map[string]bool{}
	for _, n := range nets {
		// Container plumbing and bridges carry traffic the host already
		// counts on its own links, and drop by design when a container
		// is gone; loopback cannot lose anything worth knowing about.
		switch out.kind(n) {
		case "physical", "tunnel":
		default:
			continue
		}
		out.judged++
		seen[n.Interface] = true
		current := linkCounters{
			drops:   n.DropIn + n.DropOut,
			errors:  n.ErrIn + n.ErrOut,
			packets: n.PacketsRecv + n.PacketsSent,
			at:      now,
		}
		base, ok := w.base[n.Interface]
		switch {
		case !ok:
			w.base[n.Interface] = current
		case current.drops < base.drops || current.errors < base.errors || current.packets < base.packets ||
			now.Sub(base.at) > linkStale:
			// Counters that went backwards mean the interface was recreated;
			// a baseline nobody advanced for a long stretch describes a past
			// nobody watched. Either way the next window starts here.
			w.base[n.Interface] = current
			delete(w.last, n.Interface)
		case now.Sub(base.at) >= linkWindow:
			w.last[n.Interface] = linkDelta{
				drops:   current.drops - base.drops,
				errors:  current.errors - base.errors,
				packets: current.packets - base.packets,
				span:    now.Sub(base.at),
			}
			w.base[n.Interface] = current
		}
		if d, ok := w.last[n.Interface]; ok {
			out.windows[n.Interface] = d
		}
	}
	for name := range w.base {
		if !seen[name] {
			delete(w.base, name)
			delete(w.last, name)
		}
	}
	return out
}
