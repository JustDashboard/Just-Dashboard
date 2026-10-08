package netx

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// renderSysctl renders the kernel settings drop-in systemd-sysctl reads at
// boot.
func renderSysctl(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	keys := make([]string, 0, len(sp.Sysctls))
	for k := range sp.Sysctls {
		if k != sysctlForwardV4 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	// Changing ip_forward resets accept_redirects. Restore forwarding before
	// protections so a boot cannot undo the operator's redirect policy.
	if _, set := sp.Sysctls[sysctlForwardV4]; set {
		keys = append([]string{sysctlForwardV4}, keys...)
	}
	for _, k := range keys {
		b.WriteString(k + " = " + sp.Sysctls[k] + "\n")
	}
	return b.String()
}

// protectionDef is one kernel setting the Protection page may change. The
// list is closed, as sshd's directive list is: a setting is here because it
// has a value that is the right one for a server on the internet, a reason a
// sentence long, and a range in which no value locks the machine up. A key
// that is not in it is refused, whatever the request says.
type protectionDef struct {
	Key         string
	Label       string
	Why         string
	Recommended string
	// Choice values, or Min and Max for a number.
	Allowed  []string
	Min, Max int
	// lowerIsWeaker orients a number: whether a value below the
	// recommendation is the weaker one.
	lowerIsWeaker bool
}

var protectionDefs = []protectionDef{
	{
		Key: "net.ipv4.tcp_syncookies", Label: "SYN cookies", Recommended: "1", Allowed: []string{"0", "1"},
		Why: "Answers a flood of half-open connections without keeping state for each, so a SYN flood cannot fill the connection queue and refuse real visitors.",
	},
	{
		Key: "net.ipv4.conf.all.rp_filter", Label: "Reverse-path filter (all interfaces)", Recommended: "2", Allowed: []string{"0", "2"},
		Why: "Drops packets whose source address could not be reached back through the interface they came in on. Loose (2) only requires some route to exist, which stops spoofed private sources and keeps asymmetric routing, Tailscale and WireGuard policy routing working. Strict (1) is not offered: it breaks all three, and can cut off the connection this page is on.",
	},
	{
		Key: "net.ipv4.conf.default.rp_filter", Label: "Reverse-path filter (new interfaces)", Recommended: "2", Allowed: []string{"0", "2"},
		Why: "The same filter for interfaces created after boot, such as a container's or a tunnel's. Loose for the reason the setting above is.",
	},
	{
		Key: "net.ipv4.conf.all.accept_redirects", Label: "Accept ICMP redirects (IPv4)", Recommended: "0", Allowed: []string{"0", "1"},
		Why: "A redirect tells this server to send traffic through another router. A host that is not a client of a LAN should never take that advice: it is how traffic is steered through an attacker.",
	},
	{
		Key: "net.ipv6.conf.all.accept_redirects", Label: "Accept ICMP redirects (IPv6)", Recommended: "0", Allowed: []string{"0", "1"},
		Why: "The IPv6 form of the same redirect, which neighbours on the same link can send.",
	},
	{
		Key: "net.ipv4.conf.all.send_redirects", Label: "Send ICMP redirects", Recommended: "0", Allowed: []string{"0", "1"},
		Why: "Only a router has any business telling a neighbour to use another gateway; a server that forwards for containers or a VPN should not be volunteering routes.",
	},
	{
		Key: "net.ipv4.conf.all.accept_source_route", Label: "Accept source-routed packets (IPv4)", Recommended: "0", Allowed: []string{"0", "1"},
		Why: "A source-routed packet carries the path it wants to take, which lets a sender bypass the routing and filtering the network was built around.",
	},
	{
		Key: "net.ipv6.conf.all.accept_source_route", Label: "Accept source-routed packets (IPv6)", Recommended: "0", Allowed: []string{"0", "1"},
		Why: "The IPv6 form of the same, with the same risk.",
	},
	{
		Key: "net.ipv4.icmp_echo_ignore_broadcasts", Label: "Ignore broadcast pings", Recommended: "1", Allowed: []string{"0", "1"},
		Why: "A ping to a broadcast address makes every host on the network answer; spoofed with a victim's address it turns this server into an amplifier for a smurf attack.",
	},
	{
		Key: "net.ipv4.icmp_ignore_bogus_error_responses", Label: "Ignore bogus ICMP errors", Recommended: "1", Allowed: []string{"0", "1"},
		Why: "Some routers answer a broadcast with malformed error replies; ignoring them keeps the kernel's log from filling with the noise of somebody else's bug.",
	},
	{
		Key: "net.ipv4.tcp_rfc1337", Label: "Protect against TIME-WAIT assassination", Recommended: "1", Allowed: []string{"0", "1"},
		Why: "Stops a stray reset from tearing down a connection that is already closing, which a spoofed packet can otherwise use to kill or hijack a session.",
	},
	{
		Key: "net.ipv4.tcp_max_syn_backlog", Label: "SYN backlog", Recommended: "4096", Min: 128, Max: 262144, lowerIsWeaker: true,
		Why: "How many half-open connections the kernel remembers per listener. The default is sized for a workstation; a busy server wants room to absorb a burst before SYN cookies have to take over.",
	},
	{
		Key: "net.ipv4.tcp_synack_retries", Label: "SYN-ACK retries", Recommended: "2", Min: 1, Max: 5,
		Why: "How many times the server repeats its reply to a connection that never completes. Each retry keeps a half-open entry alive for longer; two gives a slow client time and a flood less of it.",
	},
	{
		Key: "net.ipv4.conf.all.log_martians", Label: "Log impossible source addresses", Recommended: "0", Allowed: []string{"0", "1"},
		Why: "Writes a kernel log line for each packet with a source address that cannot be real. Useful for a day while debugging a routing problem, but on an internet-facing host it is a steady trickle of noise that fills the log; off is the recommendation, and on is the right temporary choice.",
	},
	{
		Key: "net.netfilter.nf_conntrack_max", Label: "Connection tracking table size", Min: 65536, Max: 4194304, lowerIsWeaker: true,
		Why: "How many connections the kernel can track at once. When the table is full new connections are dropped, which a flood of them causes on purpose; raise it before the meter reaches the top. Each entry costs a few hundred bytes of memory.",
	},
}

func protectionDefFor(key string) (protectionDef, bool) {
	for _, d := range protectionDefs {
		if d.Key == key {
			return d, true
		}
	}
	return protectionDef{}, false
}

func (d protectionDef) kind() string {
	if len(d.Allowed) > 0 {
		return "choice"
	}
	return "number"
}

// check validates a value against a setting's own range, in the canonical
// form it is written in.
func (d protectionDef) check(v string) (string, error) {
	v = strings.TrimSpace(v)
	if d.kind() == "choice" {
		for _, a := range d.Allowed {
			if v == a {
				return v, nil
			}
		}
		return "", fmt.Errorf("%s is one of %s", d.Label, strings.Join(d.Allowed, ", "))
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < d.Min || n > d.Max {
		return "", fmt.Errorf("%s is a number from %d to %d", d.Label, d.Min, d.Max)
	}
	return strconv.Itoa(n), nil
}

// recommended is the value to aim for. For the connection table there is no
// fixed answer: it is never below what the host is already running.
func (d protectionDef) recommended(current string) string {
	if d.Recommended != "" {
		return d.Recommended
	}
	n, _ := strconv.Atoi(current)
	return strconv.Itoa(max(n, 262144))
}

// weaker reports whether moving to v from the current value gives away
// protection.
func (d protectionDef) weaker(v, current string) bool {
	if d.kind() == "choice" {
		return v != d.Recommended
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return true
	}
	ref, err := strconv.Atoi(d.recommended(current))
	if err != nil {
		return true
	}
	if d.Key == "net.netfilter.nf_conntrack_max" {
		cur, err := strconv.Atoi(current)
		return err != nil || n < cur
	}
	if d.lowerIsWeaker {
		return n < ref
	}
	return n > ref
}

// readSysctl reads a kernel setting from /proc/sys. Absent means the host
// does not have it: a conntrack module that is not loaded, or IPv6 turned
// off.
func gatewayReadSysctl(key string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(gatewaySysRoot, strings.ReplaceAll(key, ".", "/")))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// ProtectionSetting is one kernel setting as the page shows it.
type ProtectionSetting struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Why         string `json:"why"`
	Recommended string `json:"recommended"`
	// Kind is choice (Allowed lists the values) or number (Min to Max).
	Kind    string   `json:"kind"`
	Allowed []string `json:"allowed"`
	Min     int      `json:"min"`
	Max     int      `json:"max"`
	// Current is what the kernel is running; Available is false when this
	// host does not have the setting.
	Current   string `json:"current"`
	Available bool   `json:"available"`
	// SetHere is whether the dashboard keeps a value for it, and Value is that
	// value: restored at boot.
	SetHere       bool   `json:"setHere"`
	Value         string `json:"value"`
	AtRecommended bool   `json:"atRecommended"`
}

func protectionSettings(sp *Spec) []ProtectionSetting {
	out := make([]ProtectionSetting, 0, len(protectionDefs))
	for _, d := range protectionDefs {
		cur, ok := gatewayReadSysctl(d.Key)
		v, set := sp.Sysctls[d.Key]
		rec := d.recommended(cur)
		s := ProtectionSetting{
			Key: d.Key, Label: d.Label, Why: d.Why, Recommended: rec, Kind: d.kind(),
			Allowed: d.Allowed, Min: d.Min, Max: d.Max,
			Current: cur, Available: ok, SetHere: set, Value: v,
			AtRecommended: ok && cur == rec,
		}
		if s.Allowed == nil {
			s.Allowed = []string{}
		}
		out = append(out, s)
	}
	return out
}

// resetNote is shown with the reset control: removing a setting from the
// dashboard stops it being restored at boot, and the kernel keeps running
// with what it has until then.
const resetNote = "Resetting removes the setting from what the dashboard restores at boot. The running kernel keeps its current value until the next reboot."

// SetProtections sets kernel settings from the closed list. Each is applied
// with sysctl -w and recorded in the spec, so the boot unit's drop-in restores
// it; a setting that fails to take puts back the ones already changed.
func (s *Service) SetProtections(ctx context.Context, values map[string]string, actor string) error {
	if len(values) == 0 {
		return fmt.Errorf("no settings were given")
	}
	keys := make([]string, 0, len(values))
	clean := map[string]string{}
	for k, raw := range values {
		d, ok := protectionDefFor(k)
		if !ok {
			return fmt.Errorf("%q is not a setting the dashboard manages", k)
		}
		v, err := d.check(raw)
		if err != nil {
			return err
		}
		if _, ok := gatewayReadSysctl(k); !ok {
			return fmt.Errorf("%s is not available on this host", d.Label)
		}
		clean[k] = v
		keys = append(keys, k)
	}
	sort.Strings(keys)

	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := old.clone()
	var changed []string
	prev := map[string]string{}
	for _, k := range keys {
		next.Sysctls[k] = clean[k]
		if cur, _ := gatewayReadSysctl(k); cur != clean[k] {
			changed = append(changed, k)
			prev[k] = cur
		}
	}
	return s.commit(ctx, next, step{
		apply:  func(ctx context.Context) error { return writeSysctls(ctx, changed, clean, prev) },
		undo:   func(ctx context.Context) { restoreSysctls(ctx, changed, prev) },
		verify: func(ctx context.Context) error { return verifySysctls(ctx, changed, clean) },
	})
}

// writeSysctls applies each value, putting back the ones already applied if
// one is refused.
func writeSysctls(ctx context.Context, keys []string, want, prev map[string]string) error {
	for i, k := range keys {
		if _, err := run(ctx, "sysctl", "-w", k+"="+want[k]); err != nil {
			rollback(ctx, func(recovery context.Context) { restoreSysctls(recovery, keys[:i], prev) })
			return fmt.Errorf("setting %s: %w", k, err)
		}
	}
	return nil
}

func restoreSysctls(ctx context.Context, keys []string, prev map[string]string) {
	for _, k := range keys {
		_, _ = run(ctx, "sysctl", "-w", k+"="+prev[k]) // returning to the earlier value on the way out of a failure
	}
}

// verifySysctls asks the kernel what it holds now: a write the kernel clamps
// or ignores is not a setting that took.
func verifySysctls(ctx context.Context, keys []string, want map[string]string) error {
	for _, k := range keys {
		out, err := run(ctx, "sysctl", "-n", k)
		if err != nil {
			return fmt.Errorf("reading %s back: %w", k, err)
		}
		if got := strings.TrimSpace(out); got != want[k] {
			return fmt.Errorf("%s is %s after setting it to %s; the kernel did not accept the value", k, got, want[k])
		}
	}
	return nil
}

// WeakensProtection reports whether any of the values gives protection away
// from where the kernel is now. The handler uses it to decide whether a
// request is the destructive kind; anything it cannot read is, since a
// request that cannot be classified is judged by the worst it could be.
func (s *Service) WeakensProtection(values map[string]string) bool {
	for k, v := range values {
		d, ok := protectionDefFor(k)
		if !ok {
			return true
		}
		clean, err := d.check(v)
		if err != nil {
			return true
		}
		cur, _ := gatewayReadSysctl(k)
		if d.weaker(clean, cur) {
			return true
		}
	}
	return false
}

// ResetProtection stops the dashboard restoring one setting at boot. The
// running kernel keeps its value until then (resetNote).
func (s *Service) ResetProtection(ctx context.Context, key string) error {
	if _, ok := protectionDefFor(key); !ok {
		return fmt.Errorf("%q is not a setting the dashboard manages", key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	if _, ok := sp.Sysctls[key]; !ok {
		return fmt.Errorf("%s: %w", key, ErrNotFound)
	}
	next := sp.clone()
	delete(next.Sysctls, key)
	return s.commit(ctx, next, step{})
}

// ----------------------------------------------------------------------------
// Reading

// LimitView is a limit with what it has dropped. Spelled out rather than
// embedding the spec's entry so every field is always present: the spec omits
// what is zero, and a page reading a missing rate as undefined is a bug that
// only shows on a limit that has none.
type LimitView struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Ports    string `json:"ports"`
	// Rate new connections per Per (second, minute or hour), allowing Burst
	// more; Rate zero is no rate limit, and Per and Burst are then empty and
	// zero. A Burst of zero under a rate is nft's own default of five.
	Rate           int    `json:"rate"`
	Per            string `json:"per"`
	Burst          int    `json:"burst"`
	PerSource      bool   `json:"perSource"`
	MaxConnections int    `json:"maxConnections"`
	Action         string `json:"action"`
	Enabled        bool   `json:"enabled"`
	Made
	// Packets and Bytes are what the limit has refused since the table was
	// loaded.
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

func limitView(l LimitSpec, counters map[string]RuleCounter) LimitView {
	c := counters["limit:"+strconv.Itoa(l.ID)]
	return LimitView{
		ID: l.ID, Name: l.Name, Protocol: l.Protocol, Ports: l.Ports, Rate: l.Rate, Per: l.Per, Burst: l.Burst,
		PerSource: l.PerSource, MaxConnections: l.MaxConnections, Action: l.Action, Enabled: l.Enabled,
		Made: l.Made, Packets: c.Packets, Bytes: c.Bytes,
	}
}

// BlocklistView is a list with its size, freshness and what it has dropped.
type BlocklistView struct {
	ID                 int                    `json:"id"`
	Name               string                 `json:"name"`
	Kind               string                 `json:"kind"`
	Countries          []string               `json:"countries"`
	URL                string                 `json:"url"`
	Entries            []string               `json:"entries"`
	Enabled            bool                   `json:"enabled"`
	Refreshed          *time.Time             `json:"refreshed"`
	Count              int                    `json:"count"`
	Error              string                 `json:"error"`
	SavedCount         int                    `json:"savedCount"`
	Cache              BlocklistCacheHealth   `json:"cache"`
	RenderedGeneration string                 `json:"renderedGeneration"`
	Runtime            BlocklistRuntimeHealth `json:"runtime"`
	Enforcement        string                 `json:"enforcement"`
	Made
	// ContainsYou says the list holds the reader's own address. The trusted
	// set keeps it from being dropped, but the page says so rather than
	// leaving it to be found out.
	ContainsYou bool   `json:"containsYou"`
	Packets     uint64 `json:"packets"`
	Bytes       uint64 `json:"bytes"`
}

func blocklistView(dir string, bl BlocklistSpec, client netip.Addr, counters map[string]RuleCounter) BlocklistView {
	v := BlocklistView{
		ID: bl.ID, Name: bl.Name, Kind: bl.Kind, Countries: bl.Countries, URL: bl.URL, Entries: bl.Entries,
		Enabled: bl.Enabled, SavedCount: bl.Count, Error: bl.Error, Made: bl.Made,
	}
	nets, health := blocklistData(dir, bl)
	v.Cache, v.Count, v.Enforcement = health, health.Count, "unknown"
	if health.Status != "ready" {
		v.Enforcement = "degraded"
		if v.Error != "" {
			v.Error += "; "
		}
		v.Error += health.Error
	}
	if v.Countries == nil {
		v.Countries = []string{}
	}
	if v.Entries == nil {
		v.Entries = []string{}
	}
	if !bl.Refreshed.IsZero() {
		t := bl.Refreshed
		v.Refreshed = &t
	}
	if client.IsValid() {
		for _, p := range nets {
			if p.Contains(client) {
				v.ContainsYou = true
				break
			}
		}
	}
	c := counters["blocklist:"+strconv.Itoa(bl.ID)]
	v.Packets, v.Bytes = c.Packets, c.Bytes
	return v
}

func (s *Service) blocklistViewByID(ctx context.Context, id int, client string) (BlocklistView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return BlocklistView{}, err
	}
	addr, _ := ParseAddr(client)
	for _, bl := range sp.Blocklists {
		if bl.ID == id {
			v := blocklistView(filepath.Join(s.paths.Dir, "lists"), bl, addr, nil)
			s.blocklistHealth(ctx, bl, &v)
			return v, nil
		}
	}
	return BlocklistView{}, fmt.Errorf("blocklist %d: %w", id, ErrNotFound)
}

// TrustedEntry is an address or network no drop in the gateway table matches,
// with what put it there.
type TrustedEntry struct {
	Address string `json:"address"`
	// Origin is loopback, allowlist (the dashboard's own allowlist), you (the
	// address of whoever made the first protection entry, kept for them) or
	// kept (another address kept earlier).
	Origin string `json:"origin"`
	// Removable is true for the entries the dashboard keeps itself.
	Removable bool `json:"removable"`
}

// ProtectionView is everything the Protection page draws.
type ProtectionView struct {
	// Loaded is whether the gateway table is in the kernel; false with entries
	// in the spec means they are not in force.
	Loaded     bool            `json:"loaded"`
	Limits     []LimitView     `json:"limits"`
	Blocklists []BlocklistView `json:"blocklists"`
	Presets    []FeedPreset    `json:"presets"`
	Trusted    []TrustedEntry  `json:"trusted"`
	// Client is the reader's address, and ClientTrusted whether the trusted
	// set covers it.
	Client        string              `json:"client"`
	ClientTrusted bool                `json:"clientTrusted"`
	Settings      []ProtectionSetting `json:"settings"`
	ResetNote     string              `json:"resetNote"`
	Conntrack     Conntrack           `json:"conntrack"`
}

// Protection reads the limits, blocklists, trusted addresses, kernel settings
// and connection table.
func (s *Service) Protection(ctx context.Context, client string) (*ProtectionView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	counters, loaded := gatewayCounters(ctx)
	addr, _ := ParseAddr(client)
	dir := filepath.Join(s.paths.Dir, "lists")
	v := &ProtectionView{
		Loaded:     loaded,
		Limits:     make([]LimitView, 0, len(sp.Limits)),
		Blocklists: make([]BlocklistView, 0, len(sp.Blocklists)),
		Presets:    feedPresets,
		Trusted:    s.trustedView(sp, addr),
		Client:     client,
		Settings:   protectionSettings(sp),
		ResetNote:  resetNote,
		Conntrack:  readConntrack(),
	}
	if addr.IsValid() {
		v.ClientTrusted = s.isTrusted(sp, addr)
	}
	for _, l := range sp.Limits {
		v.Limits = append(v.Limits, limitView(l, counters))
	}
	for _, bl := range sp.Blocklists {
		view := blocklistView(dir, bl, addr, counters)
		s.blocklistHealth(ctx, bl, &view)
		v.Blocklists = append(v.Blocklists, view)
	}
	return v, nil
}

func (s *Service) trustedView(sp *Spec, client netip.Addr) []TrustedEntry {
	out := []TrustedEntry{}
	for _, p := range loopbackRanges {
		out = append(out, TrustedEntry{Address: p.String(), Origin: "loopback"})
	}
	for _, p := range s.trustedRanges {
		out = append(out, TrustedEntry{Address: p.String(), Origin: "allowlist"})
	}
	for _, raw := range sp.Trusted {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		origin := "kept"
		if client.IsValid() && p.Contains(client) {
			origin = "you"
		}
		out = append(out, TrustedEntry{Address: p.Masked().String(), Origin: origin, Removable: true})
	}
	return out
}

// RemoveTrusted takes an address the dashboard kept out of its own drops off
// the list. The reader's own address cannot be taken off while nothing else
// trusts it: the next blocklist or limit could then refuse them.
func (s *Service) RemoveTrusted(ctx context.Context, address, client string) error {
	want, err := ParsePrefix(address)
	if err != nil {
		return err
	}
	want = want.Masked()
	err = s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i, raw := range next.Trusted {
			p, perr := ParsePrefix(raw)
			if perr != nil || p.Masked() != want {
				continue
			}
			next.Trusted = append(next.Trusted[:i], next.Trusted[i+1:]...)
			if addr, aerr := ParseAddr(client); aerr == nil && s.isTrusted(old, addr) && !s.isTrusted(next, addr) {
				return false, guarded("%s is the address you are using, and nothing else keeps it out of the gateway's drops. Remove it from another address, or keep it.", want)
			}
			return false, nil
		}
		for _, p := range append(append([]netip.Prefix{}, loopbackRanges...), s.trustedRanges...) {
			if p == want {
				return false, fmt.Errorf("%w: %s comes from loopback or the dashboard's allowlist, not from here", ErrNotManaged, want)
			}
		}
		return false, fmt.Errorf("%s: %w", want, ErrNotFound)
	})
	return err
}

// ProtectionSettings reads the kernel settings alone, for the response to a
// change to one.
func (s *Service) ProtectionSettings() ([]ProtectionSetting, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	return protectionSettings(sp), nil
}
