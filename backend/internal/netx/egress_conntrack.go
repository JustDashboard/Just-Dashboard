package netx

import (
	"context"
	"net/netip"
	"strconv"

	"golang.org/x/sys/unix"
)

// A switch changes the route of new packets; it does not move what the
// connection table already holds. Each switch reports, before and after, the
// tracked connections attributed to the members it moves away from — by the
// pinning mark of a sticky group, otherwise by the member's local address
// (the original source of a connection this host opened, the translated
// address of one it masqueraded) — so a connection that breaks is counted
// rather than silently lost. Connections nothing attributes, such as
// forwarded flows without translation, are counted as such.

const egressConntrackLimit = 65536

// EgressConnections is the connection-tracking evidence of one switch.
type EgressConnections struct {
	Read      int    `json:"read"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
	// ByMember counts the connections attributed to each member, by id.
	ByMember map[string]int `json:"byMember"`
	// Moved are attributed to members leaving the decided set: their next
	// packets take the new route, and one translated to the old member's
	// address will not survive it.
	Moved int `json:"moved"`
	// Pinned stay on a member that remains up, because the group is sticky.
	Pinned int `json:"pinned"`
	// Flushed were deleted because their member is down and the group
	// flushes; FlushFailed could not be.
	Flushed     int `json:"flushed"`
	FlushFailed int `json:"flushFailed,omitempty"`
	// Spared were attributed to a flushed member but involve a protected
	// operator address, and were kept.
	Spared int `json:"spared,omitempty"`
	// Basis says how connections were attributed.
	Basis string `json:"basis"`
}

// egressAttribution maps a tracked connection to a member of a group.
type egressAttribution struct {
	g       EgressGroupSpec
	sources map[netip.Addr]int
	shared  map[netip.Addr]bool
}

func newEgressAttribution(g EgressGroupSpec, sources map[int]netip.Addr) egressAttribution {
	a := egressAttribution{g: g, sources: map[netip.Addr]int{}, shared: map[netip.Addr]bool{}}
	for id, src := range sources {
		if !src.IsValid() {
			continue
		}
		if _, dup := a.sources[src]; dup {
			a.shared[src] = true
		}
		a.sources[src] = id
	}
	return a
}

// member is the member a connection belongs to, and whether that came from
// a pinning mark.
func (a egressAttribution) member(e ctEntry) (int, bool) {
	if a.g.Family == "inet" && e.Family != unix.AF_INET || a.g.Family == "inet6" && e.Family != unix.AF_INET6 {
		return 0, false
	}
	if v := e.Mark & egressMarkMask; v != 0 {
		n := int(v>>8) - a.g.Slot*egressMembersMax
		if n >= 1 && n <= egressMembersMax && a.g.member(n) != nil {
			return n, true
		}
	}
	local := e.Src
	if e.Status&ctStatusSrcNAT != 0 && e.ReplyDst.IsValid() {
		local = e.ReplyDst
	}
	local = local.Unmap()
	if a.shared[local] {
		return 0, false
	}
	if id, ok := a.sources[local]; ok {
		return id, false
	}
	return 0, false
}

func egressBasis(g EgressGroupSpec) string {
	if g.Sticky {
		return "Pinned connections by their mark; others by the member's local address (its own source, or the address it translated to)."
	}
	return "By the member's local address: the source of a connection this host opened, or the address a masqueraded connection was translated to. Members sharing an address are not told apart."
}

// countEgressConnections reads the table once and attributes it.
func (s *Service) countEgressConnections(ctx context.Context, g EgressGroupSpec, sources map[int]netip.Addr, leaving map[int]bool, staying map[int]bool) *EgressConnections {
	out := &EgressConnections{ByMember: map[string]int{}, Basis: egressBasis(g)}
	attr := newEgressAttribution(g, sources)
	err := inNetns(s.egressNetns, func() error {
		read, truncated, err := conntrackDump(ctx, egressConntrackLimit, func(e ctEntry) bool {
			id, pinned := attr.member(e)
			if id == 0 {
				return true
			}
			out.ByMember[strconv.Itoa(id)]++
			switch {
			case pinned && staying[id]:
				out.Pinned++
			case leaving[id]:
				out.Moved++
			}
			return true
		})
		out.Read, out.Truncated = read, truncated
		return err
	})
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

// flushEgressConnections deletes the tracked connections of members declared
// down, never one that involves a protected operator address or the
// requester's own.
func (s *Service) flushEgressConnections(ctx context.Context, g EgressGroupSpec, sources map[int]netip.Addr, down map[int]bool, spare []netip.Prefix, out *EgressConnections) {
	if len(down) == 0 {
		return
	}
	attr := newEgressAttribution(g, sources)
	var victims []ctEntry
	err := inNetns(s.egressNetns, func() error {
		_, _, err := conntrackDump(ctx, egressConntrackLimit, func(e ctEntry) bool {
			id, _ := attr.member(e)
			if id == 0 || !down[id] {
				return true
			}
			for _, p := range spare {
				if p.Contains(e.Src.Unmap()) || p.Contains(e.Dst.Unmap()) {
					out.Spared++
					return true
				}
			}
			victims = append(victims, e)
			return true
		})
		if err != nil {
			return err
		}
		for _, e := range victims {
			if err := conntrackDelete(ctx, e); err != nil {
				out.FlushFailed++
				continue
			}
			out.Flushed++
		}
		return nil
	})
	if err != nil && out.Error == "" {
		out.Error = err.Error()
	}
}
