package netx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What a device's queues are before and after the dashboard touches them.
//
// A root queue is one of four things to a change: the dashboard's own; the
// kernel's default, which can be replaced because deleting the replacement
// brings it back; a classless fq_codel whose parameters are captured and put
// back; or a structure somebody else built, which is refused rather than
// overwritten. The page says which before anyone presses Apply, from the same
// predicates the change itself runs.
//
// Verification that only compares a root's kind passes a queue whose
// parameters were changed in place with `tc qdisc change`. So after a change
// is applied the queue that carries its parameters is read back and kept;
// later reads compare against it and name what moved.

// CakeTin is one of CAKE's traffic classes with what it measured: the delay
// packets spent queued in it, and its flows. It is the one queue here that
// times its own packets, which makes it the latency evidence a shaper has.
type CakeTin struct {
	Name        string `json:"name"`
	ThresholdBs uint64 `json:"thresholdBytesPerSecond"`
	PeakDelayUs uint64 `json:"peakDelayUs"`
	AvgDelayUs  uint64 `json:"avgDelayUs"`
	BaseDelayUs uint64 `json:"baseDelayUs"`
	SentPackets uint64 `json:"sentPackets"`
	SentBytes   uint64 `json:"sentBytes"`
	Drops       uint64 `json:"drops"`
	ECNMarks    uint64 `json:"ecnMarks"`
	SparseFlows uint64 `json:"sparseFlows"`
	BulkFlows   uint64 `json:"bulkFlows"`
}

// cakeTinJSON is one entry of a CAKE queue's "tins" in `tc -s -j`.
type cakeTinJSON struct {
	ThresholdRate uint64 `json:"threshold_rate"`
	SentBytes     uint64 `json:"sent_bytes"`
	PeakDelay     uint64 `json:"peak_delay_us"`
	AvgDelay      uint64 `json:"avg_delay_us"`
	BaseDelay     uint64 `json:"base_delay_us"`
	SentPackets   uint64 `json:"sent_packets"`
	Drops         uint64 `json:"drops"`
	ECNMark       uint64 `json:"ecn_mark"`
	SparseFlows   uint64 `json:"sparse_flows"`
	BulkFlows     uint64 `json:"bulk_flows"`
}

// cakeTinNames are the classes each diffserv mode has, in the order tc lists
// their tins.
var cakeTinNames = map[string][]string{
	"besteffort": {"Best effort"},
	"diffserv3":  {"Bulk", "Best effort", "Voice"},
	"diffserv4":  {"Bulk", "Best effort", "Video", "Voice"},
	"diffserv8":  {"Tin 0", "Tin 1", "Tin 2", "Tin 3", "Tin 4", "Tin 5", "Tin 6", "Tin 7"},
	"precedence": {"Tin 0", "Tin 1", "Tin 2", "Tin 3", "Tin 4", "Tin 5", "Tin 6", "Tin 7"},
}

func cakeTins(q tcQdisc) []CakeTin {
	if q.Kind != "cake" || len(q.Tins) == 0 {
		return nil
	}
	var mode string
	_ = json.Unmarshal(q.Options["diffserv"], &mode)
	names := cakeTinNames[mode]
	out := make([]CakeTin, 0, len(q.Tins))
	for i, t := range q.Tins {
		name := fmt.Sprintf("Tin %d", i)
		if i < len(names) {
			name = names[i]
		}
		out = append(out, CakeTin{Name: name, ThresholdBs: t.ThresholdRate, PeakDelayUs: t.PeakDelay,
			AvgDelayUs: t.AvgDelay, BaseDelayUs: t.BaseDelay, SentPackets: t.SentPackets, SentBytes: t.SentBytes,
			Drops: t.Drops, ECNMarks: t.ECNMark, SparseFlows: t.SparseFlows, BulkFlows: t.BulkFlows})
	}
	return out
}

// QdiscNode is one queue in a device's tree.
type QdiscNode struct {
	Kind   string `json:"kind"`
	Handle string `json:"handle"`
	Parent string `json:"parent,omitempty"`
	Root   bool   `json:"root"`
}

// ShapeOwnership is what the next change would do with a device's queues.
type ShapeOwnership struct {
	// Verdict is managed, kernel, preserved or refused.
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

func queueTree(qs []tcQdisc) []QdiscNode {
	out := make([]QdiscNode, 0, len(qs))
	for _, q := range qs {
		out = append(out, QdiscNode{Kind: q.Kind, Handle: q.Handle, Parent: q.Parent, Root: q.Root})
	}
	return out
}

// kernelHandle is the handle the kernel gives the queues it attaches itself
// when a device comes up. A queue anybody replaced has an allocated one.
const kernelHandle = "0:"

var classlessHandle = regexp.MustCompile(`^[0-9a-fA-F]+:$`)

// unmanagedRoot judges a device's queues before the dashboard first replaces
// its root. It returns the tc lines that put the original back after a
// failed change or a later clear (none where deleting the replacement is
// enough), with the verdict, or why it refuses. filtersClear is asked only
// when the answer depends on the root's filters; defaultQdisc is the
// kernel's net.core.default_qdisc now.
func unmanagedRoot(device string, qs []tcQdisc, filtersClear func() (bool, error), defaultQdisc string) ([]string, string, error) {
	var root *tcQdisc
	var children []tcQdisc
	for i := range qs {
		q := &qs[i]
		switch {
		case q.Root:
			if root != nil {
				return nil, "", fmt.Errorf("%s has multiple root queues", device)
			}
			root = q
		case q.Kind == "ingress" || q.Kind == "clsact":
		default:
			children = append(children, *q)
		}
	}
	if root == nil || root.Kind == "noqueue" {
		if len(children) > 0 {
			return nil, "", fmt.Errorf("%s has an unmanaged queue hierarchy; its owner must remove it before shaping", device)
		}
		return nil, "kernel", nil
	}
	// A multiqueue card's default is mq with one queue per transmit ring,
	// all of the kernel's default kind. Deleting a replacement makes the
	// kernel attach exactly that again, provided the default is still the
	// kind they are now.
	if root.Kind == "mq" && root.Handle == kernelHandle {
		for _, c := range children {
			if c.Handle != kernelHandle || c.Kind != defaultQdisc {
				return nil, "", fmt.Errorf("%s's multiqueue tree has a %s queue where the kernel's default is %s; clearing a limit could not bring it back, so change it through its owner first", device, c.Kind, defaultQdisc)
			}
		}
		return nil, "kernel", nil
	}
	if len(children) > 0 {
		return nil, "", fmt.Errorf("%s has an unmanaged queue hierarchy; its owner must remove it before shaping", device)
	}
	refuse := func() ([]string, string, error) {
		return nil, "", fmt.Errorf("%s has an unmanaged %s queue whose recovery is unsupported; change it through its owner before shaping", device, root.Kind)
	}
	if root.Kind != "fq_codel" || !classlessHandle.MatchString(root.Handle) {
		// The kernel's own single-queue default, of whatever kind it is, comes
		// back by itself when a replacement is deleted.
		if root.Handle == kernelHandle && root.Kind == defaultQdisc {
			return nil, "kernel", nil
		}
		return refuse()
	}
	clear, err := filtersClear()
	if err != nil {
		return nil, "", fmt.Errorf("reading existing queue filters: %w", err)
	}
	if !clear || len(root.Options) == 0 {
		return refuse()
	}
	line := fmt.Sprintf("qdisc replace dev %s root handle %s fq_codel", device, root.Handle)
	for _, key := range []string{"limit", "flows", "quantum", "target", "interval", "memory_limit", "drop_batch"} {
		raw, exists := root.Options[key]
		if !exists {
			return refuse()
		}
		var value uint64
		if json.Unmarshal(raw, &value) != nil || value == 0 {
			return refuse()
		}
		suffix := ""
		if key == "target" || key == "interval" {
			// fq_codel keeps times in 1024 ns units and prints them truncated
			// to microseconds; writing the printed figure back truncates
			// again and loses a unit on every restore. One microsecond more
			// lands in exactly the unit that was read.
			value++
			suffix = "us"
		}
		line += fmt.Sprintf(" %s %d%s", key, value, suffix)
	}
	var ecn bool
	ecnRaw, hasECN := root.Options["ecn"]
	if (hasECN && json.Unmarshal(ecnRaw, &ecn) != nil) || (len(root.Options) != 7 && !hasECN) || (len(root.Options) != 8 && hasECN) {
		return refuse()
	}
	if ecn {
		line += " ecn"
	} else {
		line += " noecn"
	}
	return []string{line}, "preserved", nil
}

// rootFiltersOf reads whether only ingress-side filters hang off a device.
func rootFiltersOf(ctx context.Context, device string) func() (bool, error) {
	return func() (bool, error) {
		out, err := run(ctx, "tc", "-j", "filter", "show", "dev", device, "root")
		if err != nil {
			return false, err
		}
		return rootFiltersClear(out), nil
	}
}

func currentDefaultQdisc() string {
	v, _ := gatewayReadSysctl("net.core.default_qdisc")
	return v
}

// ownershipOf is the view's verdict for an unmanaged device, from the same
// predicate the first change runs.
func ownershipOf(ctx context.Context, device string, qs []tcQdisc) ShapeOwnership {
	baseline, verdict, err := unmanagedRoot(device, qs, rootFiltersOf(ctx, device), currentDefaultQdisc())
	switch {
	case err != nil:
		return ShapeOwnership{Verdict: "refused", Reason: err.Error()}
	case verdict == "preserved":
		return ShapeOwnership{Verdict: verdict, Reason: fmt.Sprintf("The existing fq_codel's parameters are captured before the first change and put back when it is cleared or fails (%s).", strings.TrimPrefix(baseline[0], "qdisc replace dev "+device+" "))}
	default:
		return ShapeOwnership{Verdict: verdict, Reason: "The kernel's own default queue; it is replaced by the change and the kernel attaches it again when the change is cleared."}
	}
}

// volatileOptions are option keys tc prints that count rather than configure.
var volatileOptions = map[string]bool{"direct_packets_stat": true}

// effectiveOptions are a queue's configured parameters as strings, for the
// page and for comparison with what was applied.
func effectiveOptions(q tcQdisc) map[string]string {
	out := map[string]string{}
	for k, raw := range q.Options {
		if volatileOptions[k] {
			continue
		}
		s := strings.TrimSpace(string(raw))
		if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
			continue
		}
		out[k] = strings.Trim(s, `"`)
	}
	return out
}

// parameterQueue is the queue that carries a managed entry's parameters: the
// leaf under an HTB shaper, otherwise the root.
func parameterQueue(sh ShapeSpec, qs []tcQdisc) *tcQdisc {
	htb := sh.EgressKbit > 0 && sh.Qdisc != "cake"
	for i := range qs {
		q := &qs[i]
		if htb && q.Parent == "1:10" && q.Handle == "10:" {
			return q
		}
		if !htb && q.Root {
			return q
		}
	}
	return nil
}

// AppliedQueue is the parameter queue as it was read right after the
// dashboard applied a device's entry.
type AppliedQueue struct {
	Kind      string            `json:"kind"`
	Handle    string            `json:"handle"`
	Options   map[string]string `json:"options"`
	AppliedAt time.Time         `json:"appliedAt"`
}

// entryKey is a shaping entry without its provenance, so a kept queue is
// only compared with the entry it was read for: a temporary change that its
// recovery took back leaves a queue that belongs to nothing saved.
func entryKey(sh ShapeSpec) string {
	sh.Made = Made{}
	b, _ := json.Marshal(sh)
	return string(b)
}

// recordApplied keeps the parameter queue just applied. Failing to keep it
// costs the later comparison, not the change, so the error is logged.
func (s *Service) recordApplied(ctx context.Context, sh ShapeSpec) {
	if s.db == nil || !sh.hasRoot() {
		return
	}
	qs, err := deviceQdiscs(ctx, sh.Device)
	if err != nil {
		s.log.Warn("reading the applied queue", "device", sh.Device, "err", err)
		return
	}
	q := parameterQueue(sh, qs)
	if q == nil {
		return
	}
	options, _ := json.Marshal(effectiveOptions(*q))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO network_shaping_applied (device, entry, kind, handle, options, applied_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(device) DO UPDATE SET entry = excluded.entry, kind = excluded.kind,
		handle = excluded.handle, options = excluded.options, applied_at = excluded.applied_at`,
		sh.Device, entryKey(sh), q.Kind, q.Handle, string(options), time.Now().Unix()); err != nil {
		s.log.Warn("keeping the applied queue", "device", sh.Device, "err", err)
	}
}

func (s *Service) forgetApplied(ctx context.Context, device string) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM network_shaping_applied WHERE device = ?`, device); err != nil {
		s.log.Warn("forgetting the applied queue", "device", device, "err", err)
	}
}

// appliedQueue is the queue kept for exactly this entry, or nil.
func (s *Service) appliedQueue(ctx context.Context, sh ShapeSpec) (*AppliedQueue, error) {
	if s.db == nil {
		return nil, nil
	}
	var a AppliedQueue
	var entry, options string
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT entry, kind, handle, options, applied_at FROM network_shaping_applied WHERE device = ?`, sh.Device).
		Scan(&entry, &a.Kind, &a.Handle, &options, &at)
	if errors.Is(err, sql.ErrNoRows) || err == nil && entry != entryKey(sh) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	device := sh.Device
	if err := json.Unmarshal([]byte(options), &a.Options); err != nil {
		return nil, fmt.Errorf("the applied queue of %s is unreadable", device)
	}
	a.AppliedAt = time.Unix(at, 0).UTC()
	return &a, nil
}

// compareApplied names what moved between the applied parameter queue and
// the one the kernel has now. Empty when nothing did.
func compareApplied(applied *AppliedQueue, now *tcQdisc) []string {
	if applied == nil {
		return nil
	}
	if now == nil {
		return []string{fmt.Sprintf("the %s queue applied here is gone", applied.Kind)}
	}
	if now.Kind != applied.Kind || now.Handle != applied.Handle {
		return []string{fmt.Sprintf("the %s queue %s applied here was replaced by %s %s", applied.Kind, applied.Handle, now.Kind, now.Handle)}
	}
	current := effectiveOptions(*now)
	var moved []string
	for k, was := range applied.Options {
		if is, ok := current[k]; !ok {
			moved = append(moved, k+" is no longer set")
		} else if is != was {
			moved = append(moved, fmt.Sprintf("%s %s → %s", k, was, is))
		}
	}
	for k := range current {
		if _, ok := applied.Options[k]; !ok {
			moved = append(moved, k+" was added")
		}
	}
	sort.Strings(moved)
	return moved
}

// Offload is what the network card does to packets before a download queue
// sees them. Generic receive offload hands the IFB packets merged into one
// large buffer, which CAKE splits again (split-gso); large receive offload
// merges in hardware and cannot be split back exactly, so a shaper behind it
// sees fewer, larger packets than the wire carried.
type Offload struct {
	Checked bool   `json:"checked"`
	GRO     string `json:"gro,omitempty"`
	LRO     string `json:"lro,omitempty"`
	GSO     string `json:"gso,omitempty"`
	TSO     string `json:"tso,omitempty"`
	Error   string `json:"error,omitempty"`
}

var ethtoolFeature = regexp.MustCompile(`^(generic-receive-offload|large-receive-offload|generic-segmentation-offload|tcp-segmentation-offload):\s+(on|off)(\s+\[fixed\])?`)

// parseOffload reads `ethtool -k DEV`.
func parseOffload(out string) Offload {
	o := Offload{Checked: true}
	for _, line := range strings.Split(out, "\n") {
		m := ethtoolFeature.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		v := m[2]
		if m[3] != "" {
			v += " (fixed)"
		}
		switch m[1] {
		case "generic-receive-offload":
			o.GRO = v
		case "large-receive-offload":
			o.LRO = v
		case "generic-segmentation-offload":
			o.GSO = v
		case "tcp-segmentation-offload":
			o.TSO = v
		}
	}
	return o
}

func readOffload(ctx context.Context, device string) *Offload {
	if !has("ethtool") {
		return &Offload{Error: "ethtool is not installed; offload settings are unknown"}
	}
	out, err := run(ctx, "ethtool", "-k", device)
	if err != nil {
		return &Offload{Error: "ethtool could not read " + device + ": " + err.Error()}
	}
	o := parseOffload(out)
	return &o
}

// UploadProfile is CAKE's egress profile beyond a bare bandwidth: which
// traffic classes it keeps apart by their DSCP marks, how flows share the
// link, and the link layer's framing, so the shaper counts the bytes the
// modem actually sends.
type UploadProfile struct {
	// Diffserv is besteffort (one class), diffserv3 or diffserv4.
	Diffserv string `json:"diffserv"`
	// FlowMode is dual-srchost (fair between this host's own sources first),
	// triple-isolate or flows.
	FlowMode string `json:"flowMode"`
	NAT      bool   `json:"nat"`
	// Wash clears DSCP marks after classification, for a provider that
	// mistreats them; off keeps them for the next hop.
	Wash      bool `json:"wash"`
	AckFilter bool `json:"ackFilter"`
	// Overhead and MPU are per-packet framing bytes and the smallest frame;
	// LinkLayer is noatm, atm (ADSL cells) or ptm (VDSL).
	Overhead  int    `json:"overhead"`
	MPU       int    `json:"mpu"`
	LinkLayer string `json:"linkLayer"`
	RTTMillis int    `json:"rttMillis"`
}

func normUploadProfile(p UploadProfile) (UploadProfile, error) {
	if p.Diffserv == "" {
		p.Diffserv = "besteffort"
	}
	if p.FlowMode == "" {
		p.FlowMode = "dual-srchost"
	}
	if p.LinkLayer == "" {
		p.LinkLayer = "noatm"
	}
	if p.RTTMillis == 0 {
		p.RTTMillis = 100
	}
	if !sqmOneOf(p.Diffserv, "besteffort", "diffserv3", "diffserv4") {
		return p, errors.New("upload classes are besteffort, diffserv3 or diffserv4")
	}
	if !sqmOneOf(p.FlowMode, "dual-srchost", "triple-isolate", "flows") {
		return p, errors.New("upload fairness is dual-srchost, triple-isolate or flows")
	}
	if !sqmOneOf(p.LinkLayer, "noatm", "atm", "ptm") || p.Overhead < -64 || p.Overhead > 256 || p.MPU < 0 || p.MPU > 256 || p.RTTMillis < 10 || p.RTTMillis > 1000 {
		return p, errors.New("CAKE needs noatm, atm or ptm; overhead -64…256 bytes, minimum packet 0…256 bytes, and RTT 10…1000 ms")
	}
	return p, nil
}

// uploadCakeArgs are the words after "cake bandwidth N" for a profile.
func uploadCakeArgs(p UploadProfile) []string {
	nat, wash, ack := "nonat", "nowash", "no-ack-filter"
	if p.NAT {
		nat = "nat"
	}
	if p.Wash {
		wash = "wash"
	}
	if p.AckFilter {
		ack = "ack-filter"
	}
	return []string{p.Diffserv, p.FlowMode, nat, wash, "split-gso", ack, p.LinkLayer,
		"overhead", strconv.Itoa(p.Overhead), "mpu", strconv.Itoa(p.MPU), "rtt", strconv.Itoa(p.RTTMillis) + "ms"}
}

// checkUploadCake compares a root CAKE queue's options with a profile.
func checkUploadCake(device string, o map[string]json.RawMessage, kbit int, p UploadProfile) error {
	ack := "disabled"
	if p.AckFilter {
		ack = "enabled"
	}
	for key, want := range map[string]string{"diffserv": p.Diffserv, "flowmode": p.FlowMode, "ack-filter": ack, "atm": p.LinkLayer} {
		var value string
		if json.Unmarshal(o[key], &value) != nil || value != want {
			return shapingDrift("%s CAKE %s differs from the saved upload profile", device, key)
		}
	}
	for key, want := range map[string]int64{"bandwidth": int64(shapeBytes(kbit)), "overhead": int64(p.Overhead), "mpu": int64(p.MPU), "rtt": int64(p.RTTMillis) * 1000} {
		var value int64
		if key == "mpu" && want == 0 && o[key] == nil {
			continue
		}
		if json.Unmarshal(o[key], &value) != nil || value != want {
			return shapingDrift("%s CAKE %s differs from the saved upload profile", device, key)
		}
	}
	for key, want := range map[string]bool{"nat": p.NAT, "wash": p.Wash, "ingress": false, "split_gso": true, "raw": false} {
		var value bool
		if json.Unmarshal(o[key], &value) != nil || value != want {
			return shapingDrift("%s CAKE %s differs from the saved upload profile", device, key)
		}
	}
	return nil
}
