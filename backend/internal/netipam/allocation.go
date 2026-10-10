package netipam

import (
	"fmt"
	"math/big"
	"net/netip"
	"sort"
)

type interval struct{ lo, hi *big.Int }

func prefixInterval(p netip.Prefix) interval {
	var raw []byte
	if p.Addr().Is4() {
		b := p.Masked().Addr().As4()
		raw = b[:]
	} else {
		b := p.Masked().Addr().As16()
		raw = b[:]
	}
	lo := new(big.Int).SetBytes(raw)
	size := new(big.Int).Lsh(big.NewInt(1), uint(p.Addr().BitLen()-p.Bits()))
	hi := new(big.Int).Sub(new(big.Int).Add(lo, size), big.NewInt(1))
	return interval{lo, hi}
}
func intPrefix(value *big.Int, bits, width int) netip.Prefix {
	raw := value.FillBytes(make([]byte, width/8))
	if width == 32 {
		var a [4]byte
		copy(a[:], raw)
		return netip.PrefixFrom(netip.AddrFrom4(a), bits)
	}
	var a [16]byte
	copy(a[:], raw)
	return netip.PrefixFrom(netip.AddrFrom16(a), bits)
}
func mergeIntervals(values []interval) []interval {
	sort.Slice(values, func(i, j int) bool { return values[i].lo.Cmp(values[j].lo) < 0 })
	out := []interval{}
	for _, v := range values {
		if len(out) == 0 || v.lo.Cmp(new(big.Int).Add(out[len(out)-1].hi, big.NewInt(1))) > 0 {
			out = append(out, interval{new(big.Int).Set(v.lo), new(big.Int).Set(v.hi)})
			continue
		}
		if v.hi.Cmp(out[len(out)-1].hi) > 0 {
			out[len(out)-1].hi.Set(v.hi)
		}
	}
	return out
}
func blockers(pool netip.Prefix, reservations []Reservation, snapshot Snapshot, ignore map[string]bool) ([]interval, []interval) {
	reserved, observed := []interval{}, []interval{}
	for _, v := range reservations {
		if v.State == "released" || ignore[v.ID] {
			continue
		}
		p, e := Canonical(v.Prefix)
		if e == nil && p.Overlaps(pool) {
			reserved = append(reserved, prefixInterval(p))
		}
	}
	for _, v := range snapshot.Observations {
		p, e := Canonical(v.Prefix)
		if e == nil && p.Overlaps(pool) {
			observed = append(observed, prefixInterval(p))
		}
	}
	return reserved, observed
}

// FirstFit jumps past the interval that blocks a candidate. Its work is
// bounded by observed/reserved rows, including a /8 IPv6 pool of /128s.
func FirstFit(pool netip.Prefix, bits int, occupied []interval) (netip.Prefix, error) {
	if bits < pool.Bits() || bits > pool.Addr().BitLen() {
		return netip.Prefix{}, fmt.Errorf("allocation prefix does not fit the pool")
	}
	boundary := prefixInterval(pool)
	size := new(big.Int).Lsh(big.NewInt(1), uint(pool.Addr().BitLen()-bits))
	candidate := new(big.Int).Set(boundary.lo)
	for _, v := range mergeIntervals(occupied) {
		end := new(big.Int).Sub(new(big.Int).Add(candidate, size), big.NewInt(1))
		if end.Cmp(v.lo) < 0 {
			break
		}
		if candidate.Cmp(v.hi) > 0 {
			continue
		}
		next := new(big.Int).Add(v.hi, big.NewInt(1))
		next.Add(next, new(big.Int).Sub(size, big.NewInt(1)))
		next.Div(next, size)
		candidate.Mul(next, size)
	}
	end := new(big.Int).Sub(new(big.Int).Add(candidate, size), big.NewInt(1))
	if end.Cmp(boundary.hi) > 0 {
		return netip.Prefix{}, fmt.Errorf("no candidate block remains outside known ranges and active reservations")
	}
	return intPrefix(candidate, bits, pool.Addr().BitLen()), nil
}
func PreviewPrefix(value string, reservations []Reservation, snapshot Snapshot, ignore map[string]bool) (Preview, error) {
	p, e := Canonical(value)
	if e != nil {
		return Preview{}, e
	}
	out := Preview{Prefix: p.String(), Status: "no_known_overlap", Conflicts: []Conflict{}, Coverage: snapshot.Coverage, CheckedAt: snapshot.CheckedAt, Limitations: append([]string(nil), limitations...)}
	if unknown(snapshot) {
		out.Status = "unknown_coverage"
	}
	for _, v := range reservations {
		if v.State == "released" || ignore[v.ID] {
			continue
		}
		q, e := Canonical(v.Prefix)
		if e == nil && q.Overlaps(p) {
			out.Conflicts = append(out.Conflicts, Conflict{Prefix: v.Prefix, Owner: v.Owner, Resource: v.Resource, Basis: "reserved_plan", ReservationID: v.ID})
		}
	}
	for _, v := range snapshot.Observations {
		q, e := Canonical(v.Prefix)
		if e == nil && q.Overlaps(p) {
			out.Conflicts = append(out.Conflicts, Conflict{Prefix: v.Prefix, Owner: v.Owner, Resource: v.Resource, Basis: v.Basis, Domain: v.Domain})
		}
	}
	if len(out.Conflicts) > 0 {
		out.Status = "known_overlap"
	}
	return out, nil
}
func blockCount(pool netip.Prefix, bits int, values []interval) *big.Int {
	bounds := prefixInterval(pool)
	size := new(big.Int).Lsh(big.NewInt(1), uint(pool.Addr().BitLen()-bits))
	blocks := []interval{}
	for _, v := range values {
		lo, hi := new(big.Int).Set(v.lo), new(big.Int).Set(v.hi)
		if lo.Cmp(bounds.lo) < 0 {
			lo.Set(bounds.lo)
		}
		if hi.Cmp(bounds.hi) > 0 {
			hi.Set(bounds.hi)
		}
		if hi.Cmp(lo) < 0 {
			continue
		}
		lo.Sub(lo, bounds.lo)
		lo.Div(lo, size)
		hi.Sub(hi, bounds.lo)
		hi.Div(hi, size)
		blocks = append(blocks, interval{lo, hi})
	}
	count := new(big.Int)
	for _, v := range mergeIntervals(blocks) {
		count.Add(count, new(big.Int).Add(new(big.Int).Sub(v.hi, v.lo), big.NewInt(1)))
	}
	return count
}
func UtilizationFor(pool Pool, reservations []Reservation, snapshot Snapshot) Utilization {
	p, _ := Canonical(pool.Prefix)
	reserved, observed := blockers(p, reservations, snapshot, nil)
	total := new(big.Int).Lsh(big.NewInt(1), uint(pool.AllocationBits-p.Bits()))
	unavailable := blockCount(p, pool.AllocationBits, append(append([]interval{}, reserved...), observed...))
	coverage := "observed"
	if unknown(snapshot) {
		coverage = "unknown"
	}
	return Utilization{PoolID: pool.ID, TotalAddresses: new(big.Int).Lsh(big.NewInt(1), uint(p.Addr().BitLen()-p.Bits())).String(), TotalBlocks: total.String(), ReservedBlocks: blockCount(p, pool.AllocationBits, reserved).String(), ObservedBlocks: blockCount(p, pool.AllocationBits, observed).String(), UnavailableBlocks: unavailable.String(), CandidateBlocks: new(big.Int).Sub(total, unavailable).String(), Coverage: coverage}
}
