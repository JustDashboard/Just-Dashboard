package netx

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"
)

// A record in the block can repeat itself, contradict another of its own
// lines, or contradict a line of the file outside the block that a reader
// reaches first. None of that was visible before saving, and saving said
// nothing about whether the host then resolved the name to the address. The
// preview names those overlaps against the file as it is now; the resolution
// check asks the host's own NSS — getent, through nsswitch.conf, in the host's
// namespace — what each managed name resolves to.

// HostRecordIssue is one overlap a set of records has.
type HostRecordIssue struct {
	// Kind is duplicate (the same name and address twice in the block),
	// conflict (one name, two addresses of a family in the block), shadowed (a
	// line outside the block, read first, gives the name another address),
	// overrides (the block is read before a line outside it that disagrees) or
	// repeated (a line outside the block already says the same).
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Address string `json:"address"`
	// Record is the 0-based index of the record in the request; Other the
	// address it overlaps with, and Line that address's line outside the block.
	Record int    `json:"record"`
	Other  string `json:"other,omitempty"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail"`
}

// HostRecordsPreview is what saving records would write and what overlaps.
type HostRecordsPreview struct {
	Records []HostRecord      `json:"records"`
	Block   []string          `json:"block"`
	Added   []HostRecord      `json:"added"`
	Removed []HostRecord      `json:"removed"`
	Issues  []HostRecordIssue `json:"issues"`
}

func hostFamily(address string) string {
	if a, err := netip.ParseAddr(address); err == nil && a.Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// PreviewHostRecords validates records as SetHostRecords would and compares
// them with the file as it is, changing nothing.
func (s *Service) PreviewHostRecords(ctx context.Context, records []HostRecord) (*HostRecordsPreview, error) {
	clean, err := cleanHostRecords(records)
	if err != nil {
		return nil, err
	}
	current, err := s.HostRecords(ctx)
	if err != nil {
		return nil, err
	}
	if current.Problem != "" {
		return nil, fmt.Errorf("%s", current.Problem)
	}
	out := &HostRecordsPreview{Records: clean, Block: []string{}, Added: []HostRecord{}, Removed: []HostRecord{}, Issues: []HostRecordIssue{}}
	if len(clean) > 0 {
		out.Block = append(out.Block, hostsBegin)
		for _, r := range clean {
			out.Block = append(out.Block, r.Address+" "+strings.Join(r.Names, " "))
		}
		out.Block = append(out.Block, hostsEnd)
	}
	key := func(r HostRecord) string { return r.Address + " " + strings.Join(r.Names, " ") }
	before, after := map[string]bool{}, map[string]bool{}
	for _, r := range current.Managed {
		before[key(r)] = true
	}
	for _, r := range clean {
		after[key(r)] = true
		if !before[key(r)] {
			out.Added = append(out.Added, r)
		}
	}
	for _, r := range current.Managed {
		if !after[key(r)] {
			out.Removed = append(out.Removed, r)
		}
	}

	// Where the block sits decides which line a reader meets first. A file with
	// no block gets one at its end, after every foreign line.
	blockLine := 1 << 30
	if data, err := os.ReadFile(hostFilePath(s.paths.Hosts)); err == nil {
		if span, err := findHostsBlock(s.paths.Hosts, splitHostsLines(data)); err == nil && span.begin >= 0 {
			blockLine = span.begin + 1
		}
	}
	type seen struct {
		record  int
		address string
	}
	inBlock := map[string][]seen{}
	for i, r := range clean {
		for _, name := range r.Names {
			for _, prev := range inBlock[name] {
				switch {
				case prev.address == r.Address:
					out.Issues = append(out.Issues, HostRecordIssue{Kind: "duplicate", Name: name, Address: r.Address, Record: i, Other: prev.address,
						Detail: fmt.Sprintf("%s is already given %s by record %d", name, r.Address, prev.record+1)})
				case hostFamily(prev.address) == hostFamily(r.Address):
					out.Issues = append(out.Issues, HostRecordIssue{Kind: "conflict", Name: name, Address: r.Address, Record: i, Other: prev.address,
						Detail: fmt.Sprintf("%s also has the %s address %s in record %d: resolvers return both, in file order, and a program may use either", name, hostFamily(r.Address), prev.address, prev.record+1)})
				}
			}
			inBlock[name] = append(inBlock[name], seen{i, r.Address})
		}
	}
	for i, r := range clean {
		for _, name := range r.Names {
			for _, line := range current.Other {
				if !containsFold(line.Names, name) {
					continue
				}
				switch {
				case line.Address == r.Address:
					out.Issues = append(out.Issues, HostRecordIssue{Kind: "repeated", Name: name, Address: r.Address, Record: i, Other: line.Address, Line: line.Line,
						Detail: fmt.Sprintf("line %d outside the block already gives %s this address", line.Line, name)})
				case hostFamily(line.Address) != hostFamily(r.Address):
				case line.Line < blockLine:
					out.Issues = append(out.Issues, HostRecordIssue{Kind: "shadowed", Name: name, Address: r.Address, Record: i, Other: line.Address, Line: line.Line,
						Detail: fmt.Sprintf("line %d, before the block, gives %s the address %s: it is returned first, and programs that take the first answer use it", line.Line, name, line.Address)})
				default:
					out.Issues = append(out.Issues, HostRecordIssue{Kind: "overrides", Name: name, Address: r.Address, Record: i, Other: line.Address, Line: line.Line,
						Detail: fmt.Sprintf("line %d, after the block, gives %s the address %s: the block's address is returned first", line.Line, name, line.Address)})
				}
			}
		}
	}
	return out, nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// HostNameResolution is what the host's NSS answers for one managed name.
type HostNameResolution struct {
	Name       string   `json:"name"`
	Configured []string `json:"configured"`
	IPv4       []string `json:"ipv4"`
	IPv6       []string `json:"ipv6"`
	// State is matches (each configured address is the first answer of its
	// family), includes (configured addresses are answered, not first),
	// differs (the answers miss a configured address), unresolved (NSS has no
	// answer) or unknown (getent could not be run).
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// HostResolutionEvidence is the host's NSS answering the managed names.
type HostResolutionEvidence struct {
	CheckedAt time.Time            `json:"checkedAt"`
	Hosts     string               `json:"hosts"`
	Names     []HostNameResolution `json:"names"`
	// Omitted counts managed names past the bound that were not asked.
	Omitted     int      `json:"omitted"`
	Limitations []string `json:"limitations"`
}

// maxHostResolutionNames bounds the getent calls one check makes: two each.
const maxHostResolutionNames = 16

// dnsHostsExecutor runs getent in the host's namespace. A variable so tests
// answer for it.
var dnsHostsExecutor TrafficExecutor = runDNSNative

// HostResolution asks the host's NSS for every managed name, as programs on
// the host would resolve it.
func (s *Service) HostResolution(ctx context.Context) (*HostResolutionEvidence, error) {
	records, err := s.HostRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := &HostResolutionEvidence{CheckedAt: time.Now().UTC(), Names: []HostNameResolution{}, Limitations: []string{
		"getent asks the host's NSS in the order nsswitch.conf gives: a name missing from the files source goes on to DNS like any program's lookup would.",
		"Programs that resolve without NSS — static binaries, browsers with their own DNS, containers with their own /etc/hosts — can answer differently.",
	}}
	out.Hosts = nsswitchHosts()
	configured := map[string][]string{}
	order := []string{}
	for _, r := range records.Managed {
		for _, name := range r.Names {
			name = strings.ToLower(name)
			if _, ok := configured[name]; !ok {
				order = append(order, name)
			}
			configured[name] = append(configured[name], r.Address)
		}
	}
	if len(order) > maxHostResolutionNames {
		out.Omitted = len(order) - maxHostResolutionNames
		order = order[:maxHostResolutionNames]
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	available := has("getent")
	for _, name := range order {
		if !available {
			out.Names = append(out.Names, HostNameResolution{Name: name, Configured: configured[name], IPv4: []string{}, IPv6: []string{}, State: "unknown", Detail: "getent is not installed on the host."})
			continue
		}
		out.Names = append(out.Names, resolveHostName(ctx, name, configured[name]))
	}
	return out, nil
}

// nsswitchHosts is the hosts line of the host's nsswitch.conf, or why not.
func nsswitchHosts() string {
	data, err := readUnder(dnsOwnerRoot, "/etc/nsswitch.conf")
	if err != nil {
		return "nsswitch.conf could not be read"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "hosts:"); ok {
			return strings.Join(strings.Fields(value), " ")
		}
	}
	return "nsswitch.conf has no hosts line"
}

func resolveHostName(ctx context.Context, name string, configured []string) HostNameResolution {
	r := HostNameResolution{Name: name, Configured: configured, IPv4: []string{}, IPv6: []string{}}
	unknown := false
	for _, family := range []string{"ahostsv4", "ahostsv6"} {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		out, err := dnsHostsExecutor(callCtx, "getent", family, name)
		timedOut := callCtx.Err() != nil
		cancel()
		if err != nil {
			// getent exits 2, printing nothing, when the name is not found; a
			// failure with output, or no answer in time, is the lookup failing.
			if strings.TrimSpace(out) != "" || timedOut {
				unknown = true
			}
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			a, err := netip.ParseAddr(fields[0])
			if err != nil {
				continue
			}
			value := a.Unmap().String()
			list := &r.IPv6
			if family == "ahostsv4" {
				if !a.Unmap().Is4() {
					continue
				}
				list = &r.IPv4
			} else if a.Unmap().Is4() {
				// ahostsv6 maps IPv4 answers into ::ffff: when no AAAA exists.
				continue
			}
			if !containsString(*list, value) {
				*list = append(*list, value)
			}
		}
	}
	switch {
	case unknown:
		r.State, r.Detail = "unknown", "getent could not be run on the host"
	case len(r.IPv4) == 0 && len(r.IPv6) == 0:
		r.State, r.Detail = "unresolved", "the host's NSS returns no address: nsswitch.conf may not consult files, or a source before it answered not-found"
	default:
		first, includes := true, true
		for _, address := range configured {
			list := r.IPv6
			if hostFamily(address) == "IPv4" {
				list = r.IPv4
			}
			at := -1
			for i, got := range list {
				if got == address {
					at = i
				}
			}
			first = first && at == 0
			includes = includes && at >= 0
		}
		switch {
		case first:
			r.State, r.Detail = "matches", "each configured address is the first the host returns for its family"
		case includes:
			r.State, r.Detail = "includes", "the configured addresses are returned, but another address of the same family comes first"
		default:
			r.State, r.Detail = "differs", "the host returns other addresses: a line read earlier or another NSS source answers for this name"
		}
	}
	r.Detail += "."
	return r
}
