package netx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// BlocklistCacheHealth describes actual data rather than the last fetch's
// saved metadata. A cache failure never authorizes an empty replacement set.
type BlocklistCacheHealth struct {
	Status     string `json:"status"`
	Count      int    `json:"count"`
	Generation string `json:"generation"`
	Error      string `json:"error,omitempty"`
}

type BlocklistRuntimeHealth struct {
	Status     string    `json:"status"`
	Count      *int      `json:"count"`
	Generation string    `json:"generation"`
	CheckedAt  time.Time `json:"checkedAt"`
	Error      string    `json:"error,omitempty"`
}

func readBlocklistChecked(dir string, id int) ([]netip.Prefix, error) {
	if dir == "" {
		return nil, errors.New("the blocklist cache directory is unavailable")
	}
	f, err := os.Open(blocklistFile(dir, id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFeedBytes {
		return nil, errors.New("the cached list exceeds the size limit")
	}
	var nets []netip.Prefix
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p, err := ParsePrefix(line)
		if err != nil || p.Bits() == 0 {
			return nil, fmt.Errorf("cached list line %d is not a valid bounded network", n+1)
		}
		for _, reserved := range neverBlock {
			if overlaps(p, reserved) {
				return nil, fmt.Errorf("cached list line %d includes a private or reserved network", n+1)
			}
		}
		nets = append(nets, p.Masked())
		if len(nets) > maxListEntries {
			return nil, errors.New("the cached list exceeds the entry limit")
		}
	}
	if len(nets) == 0 {
		return nil, errors.New("the cached list is empty")
	}
	return mergePrefixes(nets), nil
}

func checkedBlocklist(dir string, id int) ([]netip.Prefix, BlocklistCacheHealth) {
	health := BlocklistCacheHealth{Status: "unreadable"}
	path := blocklistFile(dir, id)
	fi, err := os.Stat(path)
	if dir == "" {
		err = errors.New("the blocklist cache directory is unavailable")
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			health.Status = "missing"
		}
		health.Error = err.Error()
		return nil, health
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o444 == 0 {
		health.Error = "the cached list is not a readable regular file"
		return nil, health
	}
	blocklistMemo.Lock()
	m, found := blocklistMemo.byPath[path]
	blocklistMemo.Unlock()
	ctime := int64(0)
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ctime = st.Ctim.Sec*1e9 + st.Ctim.Nsec
	}
	if found && m.mtime == fi.ModTime().UnixNano() && m.size == fi.Size() && m.mode == fi.Mode() && m.ctime == ctime && os.SameFile(m.info, fi) {
		return m.nets, BlocklistCacheHealth{Status: "ready", Count: len(m.nets), Generation: m.generation}
	}
	nets, err := readBlocklistChecked(dir, id)
	blocklistParses.Add(1)
	if err != nil {
		health.Status, health.Error = "invalid", err.Error()
		if errors.Is(err, os.ErrPermission) {
			health.Status = "unreadable"
		}
		return nil, health
	}
	generation := blocklistGeneration(prefixIntervals(nets))
	blocklistMemo.Lock()
	if len(blocklistMemo.byPath) > 64 {
		clear(blocklistMemo.byPath)
	}
	blocklistMemo.byPath[path] = memoizedList{mtime: fi.ModTime().UnixNano(), size: fi.Size(), mode: fi.Mode(), ctime: ctime, info: fi, nets: nets, generation: generation}
	blocklistMemo.Unlock()
	return nets, BlocklistCacheHealth{Status: "ready", Count: len(nets), Generation: generation}
}

func blocklistData(dir string, bl BlocklistSpec) ([]netip.Prefix, BlocklistCacheHealth) {
	if bl.Kind != "manual" {
		return checkedBlocklist(dir, bl.ID)
	}
	var nets []netip.Prefix
	for _, raw := range bl.Entries {
		p, err := ParsePrefix(raw)
		if err != nil || p.Bits() == 0 {
			return nil, BlocklistCacheHealth{Status: "invalid", Error: "the saved manual list contains an invalid network"}
		}
		nets = append(nets, p.Masked())
	}
	if len(nets) == 0 {
		return nil, BlocklistCacheHealth{Status: "invalid", Error: "the saved manual list is empty"}
	}
	nets = mergePrefixes(nets)
	return nets, BlocklistCacheHealth{Status: "ready", Count: len(nets), Generation: blocklistGeneration(prefixIntervals(nets))}
}

type addressInterval struct{ first, last netip.Addr }

func prefixLast(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As16()
	bits := p.Bits()
	if p.Addr().Is4() {
		bits += 96
	}
	for bit := bits; bit < 128; bit++ {
		b[bit/8] |= 1 << (7 - bit%8)
	}
	a := netip.AddrFrom16(b)
	if p.Addr().Is4() {
		return a.Unmap()
	}
	return a
}

func prefixIntervals(nets []netip.Prefix) []addressInterval {
	var intervals []addressInterval
	for _, p := range mergePrefixes(nets) {
		intervals = append(intervals, addressInterval{p.Addr(), prefixLast(p)})
	}
	return intervals
}

// nft auto-merge may combine neighboring prefixes into a range. Hashing the
// same address union avoids reporting that representation change as drift.
func blocklistGeneration(intervals []addressInterval) string {
	intervals = slices.Clone(intervals)
	slices.SortFunc(intervals, func(a, b addressInterval) int { return a.first.Compare(b.first) })
	var normalized []addressInterval
	for _, r := range intervals {
		if n := len(normalized); n > 0 {
			last := &normalized[n-1]
			if last.first.Is4() == r.first.Is4() && (r.first.Compare(last.last) <= 0 || last.last.Next() == r.first) {
				if r.last.Compare(last.last) > 0 {
					last.last = r.last
				}
				continue
			}
		}
		normalized = append(normalized, r)
	}
	h := sha256.New()
	for _, r := range normalized {
		fmt.Fprintf(h, "%s-%s\n", r.first, r.last)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func renderedBlocklistGeneration(dir string, id int) string {
	b, err := os.ReadFile(filepath.Join(dir, gatewayFile))
	if err != nil {
		return ""
	}
	var intervals []addressInterval
	found := 0
	for _, fam := range []string{"4", "6"} {
		start := strings.Index(string(b), fmt.Sprintf("\tset bl_%d_%s {\n", id, fam))
		if start < 0 {
			continue
		}
		body := string(b)[start:]
		end := strings.Index(body, "\t}\n")
		if end < 0 {
			return ""
		}
		body = body[:end]
		found++
		if at := strings.Index(body, "elements = { "); at >= 0 {
			elements := strings.SplitN(body[at+len("elements = { "):], " }", 2)[0]
			for _, raw := range strings.Split(elements, ", ") {
				p, err := ParsePrefix(raw)
				if err != nil {
					return ""
				}
				intervals = append(intervals, addressInterval{p.Masked().Addr(), prefixLast(p)})
			}
		}
	}
	if found != 2 {
		return ""
	}
	return blocklistGeneration(intervals)
}

func readBlocklistRuntime(ctx context.Context, id int) BlocklistRuntimeHealth {
	v := BlocklistRuntimeHealth{Status: "present", CheckedAt: time.Now().UTC()}
	var intervals []addressInterval
	count := 0
	for _, family := range []string{"4", "6"} {
		name := fmt.Sprintf("bl_%d_%s", id, family)
		out, err := run(ctx, "nft", "-j", "list", "set", "inet", gatewayTable, name)
		if err != nil {
			v.Status, v.Error = "unreadable", err.Error()
			if strings.Contains(out+err.Error(), "No such file") || strings.Contains(out+err.Error(), "no such") {
				v.Status = "absent"
			}
			return v
		}
		parsed, err := parseBlocklistSet(out, name)
		if err != nil {
			v.Status, v.Error = "unreadable", err.Error()
			return v
		}
		intervals = append(intervals, parsed...)
		count += len(parsed)
	}
	v.Count, v.Generation = &count, blocklistGeneration(intervals)
	return v
}

func parseBlocklistSet(out, name string) ([]addressInterval, error) {
	var listing struct {
		Nftables []struct {
			Set *struct {
				Name string            `json:"name"`
				Elem []json.RawMessage `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &listing) != nil {
		return nil, errors.New("nft returned an unreadable blocklist set")
	}
	for _, o := range listing.Nftables {
		if o.Set == nil || o.Set.Name != name {
			continue
		}
		var intervals []addressInterval
		for _, raw := range o.Set.Elem {
			var addr string
			if json.Unmarshal(raw, &addr) == nil {
				p, err := ParsePrefix(addr)
				if err != nil {
					return nil, err
				}
				intervals = append(intervals, addressInterval{p.Masked().Addr(), prefixLast(p)})
				continue
			}
			var elem struct {
				Prefix *struct {
					Addr string `json:"addr"`
					Len  int    `json:"len"`
				} `json:"prefix"`
				Range []string `json:"range"`
				Elem  *struct {
					Val json.RawMessage `json:"val"`
				} `json:"elem"`
			}
			if json.Unmarshal(raw, &elem) != nil {
				return nil, errors.New("nft returned an unsupported set element")
			}
			if elem.Prefix != nil {
				p, err := ParsePrefix(fmt.Sprintf("%s/%d", elem.Prefix.Addr, elem.Prefix.Len))
				if err != nil {
					return nil, err
				}
				intervals = append(intervals, addressInterval{p.Masked().Addr(), prefixLast(p)})
			} else if len(elem.Range) == 2 {
				lo, e1 := ParseAddr(elem.Range[0])
				hi, e2 := ParseAddr(elem.Range[1])
				if e1 != nil || e2 != nil || lo.Is4() != hi.Is4() || lo.Compare(hi) > 0 {
					return nil, errors.New("nft returned an invalid set range")
				}
				intervals = append(intervals, addressInterval{lo, hi})
			} else {
				return nil, errors.New("nft returned an unsupported set element")
			}
		}
		return intervals, nil
	}
	return nil, fmt.Errorf("nft did not return the requested set %s", name)
}

func (s *Service) blocklistHealth(ctx context.Context, bl BlocklistSpec, v *BlocklistView) {
	v.RenderedGeneration = renderedBlocklistGeneration(s.paths.Dir, bl.ID)
	if !bl.Enabled {
		v.Enforcement, v.Runtime.Status = "disabled", "not_required"
		return
	}
	v.Runtime = readBlocklistRuntime(ctx, bl.ID)
	v.Enforcement = "unknown"
	if v.Cache.Status != "ready" || v.Runtime.Status == "absent" {
		v.Enforcement = "degraded"
		return
	}
	if v.Runtime.Status != "present" {
		return
	}
	if v.Cache.Generation == v.Runtime.Generation && v.RenderedGeneration == v.Cache.Generation {
		v.Enforcement = "verified"
	} else {
		v.Enforcement = "degraded"
	}
}
