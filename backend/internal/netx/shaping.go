package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The queue disciplines the page offers. fq_codel is the kernel's good
// default, cake adds per-flow fairness and its own shaper, and fq is what BBR
// wants beneath it.
var shapeQdiscs = []string{"fq_codel", "cake", "fq"}

// The speed limits: a floor below which a limit is almost certainly a typo
// for a unit, and a ceiling nobody's link reaches.
const (
	minGuardedKbit = 1000
	maxShapeKbit   = 100_000_000
)

// shapeLines are the tc commands that give one device its shaping, without
// the leading "tc".
//
// Egress is htb with one class at the limit and fq_codel under it, so a
// saturated upload stays fair between flows instead of queueing behind
// whichever sent first; cake does both jobs itself and takes a bandwidth.
// Ingress cannot be queued, only policed: packets over the rate are dropped
// as they arrive, which is what makes TCP slow down.
//
// Each half starts by clearing what the device had, so the unit can run over a
// device that is already shaped: htb refuses to be replaced by itself and
// matchall by itself, so the root queue is deleted and the policer's one
// filter is. The deletes fail harmlessly when there is nothing. The ingress
// queue is never deleted here: tc-BPF programs hang their filters on a
// clsact queue, and deleting "ingress" there takes the whole of it, and them,
// away. Replacing the plain ingress queue over itself is a no-op, and over a
// clsact one an error the batch's -force carries on past.
func shapeLines(sh ShapeSpec) []string {
	d := sh.Device
	var out []string
	switch {
	case sh.EgressKbit > 0 && sh.Qdisc == "cake":
		out = append(out,
			fmt.Sprintf("qdisc del dev %s root", d),
			fmt.Sprintf("qdisc replace dev %s root cake bandwidth %dkbit", d, sh.EgressKbit))
	case sh.EgressKbit > 0:
		leaf := sh.Qdisc
		if leaf == "" {
			leaf = "fq_codel"
		}
		out = append(out,
			fmt.Sprintf("qdisc del dev %s root", d),
			fmt.Sprintf("qdisc replace dev %s root handle 1: htb default 10", d),
			fmt.Sprintf("class replace dev %s parent 1: classid 1:10 htb rate %dkbit ceil %dkbit", d, sh.EgressKbit, sh.EgressKbit),
			fmt.Sprintf("qdisc replace dev %s parent 1:10 handle 10: %s", d, leaf))
	case sh.Qdisc != "":
		out = append(out,
			fmt.Sprintf("qdisc del dev %s root", d),
			fmt.Sprintf("qdisc replace dev %s root %s", d, sh.Qdisc))
	}
	if sh.IngressKbit > 0 {
		out = append(out,
			fmt.Sprintf("qdisc replace dev %s handle ffff: ingress", d),
			fmt.Sprintf("filter del dev %s parent ffff: prio 1", d),
			fmt.Sprintf("filter replace dev %s parent ffff: protocol all prio 1 matchall action police rate %dkbit burst %d drop",
				d, sh.IngressKbit, policeBurst(sh.IngressKbit)))
	}
	return out
}

// policeBurst is the burst a policer allows, in bytes: a tenth of a second at
// the rate, and never under 16 KB. A policer with less than that drops the
// first packets of every burst even when the average is far under the limit,
// which looks like a limit set lower than it is.
func policeBurst(kbit int) int {
	return max(kbit*25/2, 16*1024)
}

// normShape validates a device's shaping and returns it canonical.
func normShape(sh ShapeSpec) (ShapeSpec, error) {
	if err := ValidIfName(sh.Device); err != nil {
		return sh, err
	}
	switch sh.Qdisc {
	case "":
	case "fq_codel", "cake", "fq":
	default:
		return sh, fmt.Errorf("the queue discipline is one of %s", strings.Join(shapeQdiscs, ", "))
	}
	for _, k := range []int{sh.EgressKbit, sh.IngressKbit} {
		if k < 0 || k > maxShapeKbit {
			return sh, fmt.Errorf("a speed limit is between 1 kbit/s and %d kbit/s; 0 is no limit", maxShapeKbit)
		}
	}
	if sh.Qdisc == "" && sh.EgressKbit == 0 && sh.IngressKbit == 0 {
		return sh, errors.New("set a queue discipline, a speed limit, or both")
	}
	return sh, nil
}

// renderShaping renders the tc batch file the boot unit restores the speed
// limits and queue disciplines from. An entry that does not validate is left
// out rather than written: the file is read by tc at boot, and one bad line
// must not be able to stop the rest.
func renderShaping(sp *Spec) string {
	var b strings.Builder
	b.WriteString(generatedHeader)
	for _, sh := range sp.Shaping {
		sh, err := normShape(sh)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "# %s\n", sh.Device)
		for _, l := range shapeLines(sh) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// runShapeLines runs a device's tc commands one by one, as explicit
// arguments. The deletes are allowed to fail: they clear what may not be
// there.
func runShapeLines(ctx context.Context, lines []string) error {
	for _, l := range lines {
		args := strings.Fields(l)
		_, err := run(ctx, "tc", args...)
		if err != nil && !strings.HasPrefix(l, "qdisc del ") && !strings.HasPrefix(l, "filter del ") {
			return fmt.Errorf("tc %s: %w", l, err)
		}
	}
	return nil
}

// hasRoot is whether the entry sets the device's root queue.
func (sh ShapeSpec) hasRoot() bool { return sh.EgressKbit > 0 || sh.Qdisc != "" }

// deviceQdiscs reads the queues a device has now.
func deviceQdiscs(ctx context.Context, device string) ([]tcQdisc, error) {
	out, err := run(ctx, "tc", "-j", "qdisc", "show", "dev", device)
	if err != nil {
		return nil, fmt.Errorf("reading %s's queues: %w", device, err)
	}
	return parseQdiscs(out)
}

// ingressKind is what holds a device's ingress hook: "ingress" for the plain
// queue the dashboard makes, "clsact" for the one tc-BPF programs use, empty
// for neither.
func ingressKind(qs []tcQdisc) string {
	kind := ""
	for _, q := range qs {
		switch q.Kind {
		case "clsact":
			return "clsact"
		case "ingress":
			kind = "ingress"
		}
	}
	return kind
}

// removeRoot deletes a device's root queue, which the kernel then replaces
// with its default.
func removeRoot(ctx context.Context, device string) {
	_, _ = run(ctx, "tc", "qdisc", "del", "dev", device, "root") // nothing to delete is the common case
}

// removeIngress deletes a device's ingress queue only when it is the plain
// one. A clsact queue is somebody else's — a BPF program's — and goes with
// everything attached to it if it is deleted.
func removeIngress(ctx context.Context, device string) {
	qs, err := deviceQdiscs(ctx, device)
	if err != nil || ingressKind(qs) != "ingress" {
		return
	}
	_, _ = run(ctx, "tc", "qdisc", "del", "dev", device, "ingress") // the plain queue, removed after the policer's filter
}

// undoShaping takes off what an entry set and puts back what was there before.
func undoShaping(ctx context.Context, set ShapeSpec, prev *ShapeSpec) error {
	if set.hasRoot() {
		removeRoot(ctx, set.Device)
	}
	if set.IngressKbit > 0 {
		removeIngress(ctx, set.Device)
	}
	if prev == nil {
		return nil
	}
	return runShapeLines(ctx, shapeLines(*prev))
}

// ShapeRequest is the body of a device's shaping.
type ShapeRequest struct {
	// Qdisc is fq_codel, cake or fq; empty leaves the kernel's default.
	Qdisc string `json:"qdisc"`
	// EgressKbit and IngressKbit are the upload and download limits in
	// kilobits a second; zero is no limit.
	EgressKbit  int `json:"egressKbit"`
	IngressKbit int `json:"ingressKbit"`
}

// linkKind reads a device's kernel kind, and so also whether it exists.
func linkKind(ctx context.Context, device string) (string, error) {
	out, err := run(ctx, "ip", "-j", "-d", "link", "show", "dev", device)
	if err != nil {
		var missing *UnavailableError
		if errors.As(err, &missing) {
			return "", err
		}
		return "", fmt.Errorf("there is no interface called %s on this host", device)
	}
	var links []ipLink
	if err := json.Unmarshal([]byte(out), &links); err != nil || len(links) == 0 {
		return "", errors.New("ip link printed something unreadable")
	}
	switch l := links[0]; {
	case l.LinkType == "loopback":
		return "loopback", nil
	case l.LinkInfo.InfoKind == "":
		return "physical", nil
	default:
		return l.LinkInfo.InfoKind, nil
	}
}

// SetShaping gives a device a queue discipline and speed limits.
func (s *Service) SetShaping(ctx context.Context, device string, req ShapeRequest, client, actor string) error {
	sh, err := normShape(ShapeSpec{Device: device, Qdisc: req.Qdisc, EgressKbit: req.EgressKbit, IngressKbit: req.IngressKbit})
	if err != nil {
		return err
	}
	kind, err := linkKind(ctx, sh.Device)
	if err != nil {
		return err
	}
	if kind == "veth" || kind == "loopback" {
		return fmt.Errorf("%s is a %s device: shape the interface it leads to, not one end of a pair", sh.Device, kind)
	}
	if (sh.EgressKbit > 0 && sh.EgressKbit < minGuardedKbit) || (sh.IngressKbit > 0 && sh.IngressKbit < minGuardedKbit) {
		path, err := clientPath(ctx, client)
		if err != nil {
			return err
		}
		switch {
		case readUplinks(ctx)[sh.Device]:
			return guarded("%s carries this server's default route; a limit under %d kbit/s on it would make the dashboard, and everything else, crawl.", sh.Device, minGuardedKbit)
		case !path.Local && path.Device == sh.Device:
			return guarded("Your browser is reached through %s; a limit under %d kbit/s on it would make this page crawl.", sh.Device, minGuardedKbit)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := old.clone()
	var prev *ShapeSpec
	idx := -1
	for i := range next.Shaping {
		if next.Shaping[i].Device == sh.Device {
			idx = i
			p := next.Shaping[i]
			prev = &p
		}
	}
	// A saved spec is ownership evidence only for the half it actually manages.
	// Capture supported unowned queues before the first destructive command.
	baseline, err := shapingBaseline(ctx, sh, prev)
	if err != nil {
		return err
	}

	if idx < 0 {
		sh.Made = gwStamp(actor)
		next.Shaping = append(next.Shaping, sh)
	} else {
		sh.Made = next.Shaping[idx].Made
		next.Shaping[idx] = sh
	}
	restore := func(ctx context.Context) {
		if err := undoShaping(ctx, sh, prev); err != nil {
			recordRecoveryError(ctx, err)
			s.log.Error("restoring a device's shaping after a failed change", "device", sh.Device, "err", err)
		}
		if prev == nil || !prev.hasRoot() {
			if err := runShapeLines(ctx, baseline); err != nil {
				recordRecoveryError(ctx, err)
				s.log.Error("restoring the original queue", "device", sh.Device, "err", err)
			}
		}

	}
	var recovery []recoveryCommand
	for _, line := range baseline {
		recovery = append(recovery, recoveryCommand{Tool: "tc", Args: strings.Fields(line)})
	}
	return s.commit(ctx, next, step{
		recovery: recovery,
		apply: func(ctx context.Context) error {
			// A half the new entry no longer sets is cleared; a half it sets is
			// replaced by its own lines.
			if prev != nil && prev.hasRoot() && !sh.hasRoot() {
				removeRoot(ctx, sh.Device)
			}
			if prev != nil && prev.IngressKbit > 0 && sh.IngressKbit == 0 {
				removeIngress(ctx, sh.Device)
			}
			if err := runShapeLines(ctx, shapeLines(sh)); err != nil {
				rollback(ctx, restore)
				return err
			}
			return nil
		},
		undo:   restore,
		verify: func(ctx context.Context) error { return verifyShaping(ctx, sh) },
	})
}

// ClearShaping removes a device's shaping.
func (s *Service) ClearShaping(ctx context.Context, device string) error {
	if err := ValidIfName(device); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := old.clone()
	var prev *ShapeSpec
	for i, sh := range next.Shaping {
		if sh.Device == device {
			p := sh
			prev = &p
			next.Shaping = append(next.Shaping[:i], next.Shaping[i+1:]...)
			break
		}
	}
	if prev == nil {
		return fmt.Errorf("shaping of %s: %w", device, ErrNotFound)
	}
	if _, err := shapingBaseline(ctx, ShapeSpec{Device: device}, prev); err != nil {
		return err
	}

	return s.commit(ctx, next, step{
		apply: func(ctx context.Context) error {
			if prev.hasRoot() {
				removeRoot(ctx, device)
			}
			if prev.IngressKbit > 0 {
				removeIngress(ctx, device)
			}
			return nil
		},
		undo: func(ctx context.Context) {
			if err := runShapeLines(ctx, shapeLines(*prev)); err != nil {
				recordRecoveryError(ctx, err)
				s.log.Error("restoring a device's shaping after a failed change", "device", device, "err", err)
			}
		},
	})
}

// tcQdisc is one entry of `tc -j -s qdisc show`.
type tcQdisc struct {
	Kind       string                     `json:"kind"`
	Dev        string                     `json:"dev"`
	Handle     string                     `json:"handle"`
	Parent     string                     `json:"parent"`
	Root       bool                       `json:"root"`
	Options    map[string]json.RawMessage `json:"options"`
	Bytes      uint64                     `json:"bytes"`
	Packets    uint64                     `json:"packets"`
	Drops      uint64                     `json:"drops"`
	Overlimits uint64                     `json:"overlimits"`
	Requeues   uint64                     `json:"requeues"`
	Backlog    uint64                     `json:"backlog"`
}

func parseQdiscs(out string) ([]tcQdisc, error) {
	var qs []tcQdisc
	if err := json.Unmarshal([]byte(out), &qs); err != nil {
		return nil, fmt.Errorf("tc printed something unreadable: %w", err)
	}
	return qs, nil
}

// shapingBaseline refuses structures whose recovery the dashboard cannot reproduce.
// A classless fq_codel queue is recoverable; an arbitrary hierarchy or attached
// filter is not. Existing ingress belongs to its creator, even when empty.
func shapingBaseline(ctx context.Context, sh ShapeSpec, prev *ShapeSpec) ([]string, error) {
	qs, err := deviceQdiscs(ctx, sh.Device)
	if err != nil {
		return nil, err
	}
	if sh.IngressKbit > 0 {
		if ingressKind(qs) == "clsact" {
			return nil, fmt.Errorf("%s has a clsact queue; its tc-BPF filters must be preserved. Limit upload only", sh.Device)
		}
		if ingressKind(qs) != "" && (prev == nil || prev.IngressKbit == 0) {
			return nil, fmt.Errorf("%s has an unmanaged ingress queue; remove it through its owner before adding a download limit", sh.Device)
		}
	}
	if prev != nil && prev.hasRoot() {
		expected := prev.Qdisc
		if prev.EgressKbit > 0 && prev.Qdisc != "cake" {
			expected = "htb"
		}
		rootOK := false
		for _, q := range qs {
			if q.Root {
				rootOK = q.Kind == expected && (expected != "htb" || q.Handle == "1:")
			} else if q.Kind != "ingress" && q.Kind != "clsact" {
				leaf := prev.Qdisc
				if leaf == "" {
					leaf = "fq_codel"
				}
				if expected != "htb" || q.Parent != "1:10" || q.Handle != "10:" || q.Kind != leaf {
					return nil, fmt.Errorf("%s has a foreign queue beneath its managed root; refusing to replace it", sh.Device)
				}
			}
		}
		if !rootOK {
			return nil, fmt.Errorf("%s's managed root has been replaced by another owner; refusing to overwrite it", sh.Device)
		}
	}
	if prev != nil && prev.hasRoot() {
		out, err := run(ctx, "tc", "-j", "filter", "show", "dev", sh.Device, "root")
		if err != nil {
			return nil, fmt.Errorf("reading managed root filters: %w", err)
		}
		if !rootFiltersClear(out) {
			return nil, fmt.Errorf("%s has foreign or unreadable root filters; refusing to remove them", sh.Device)
		}
	}

	if prev != nil && prev.IngressKbit > 0 && ingressKind(qs) == "ingress" {
		out, err := run(ctx, "tc", "-j", "filter", "show", "dev", sh.Device, "parent", "ffff:")
		if err != nil {
			return nil, fmt.Errorf("reading ingress ownership: %w", err)
		}
		var filters []tcFilter
		if json.Unmarshal([]byte(out), &filters) != nil {
			return nil, errors.New("tc printed unreadable ingress ownership")
		}
		for _, f := range filters {
			if f.Pref != 1 || f.Kind != "matchall" || f.Protocol != "all" || f.Chain != 0 || (len(f.Options.Actions) != 0 && (len(f.Options.Actions) != 1 || f.Options.Actions[0].Kind != "police")) {
				return nil, fmt.Errorf("%s has foreign ingress filters; refusing to remove or replace them", sh.Device)
			}
		}
	}
	if !sh.hasRoot() || (prev != nil && prev.hasRoot()) {
		return nil, nil
	}
	var root *tcQdisc
	for i := range qs {
		q := &qs[i]
		if q.Root {
			if root != nil {
				return nil, fmt.Errorf("%s has multiple root queues", sh.Device)
			}
			root = q
		} else if q.Kind != "ingress" && q.Kind != "clsact" {
			return nil, fmt.Errorf("%s has an unmanaged queue hierarchy; its owner must remove it before shaping", sh.Device)
		}
	}
	if root == nil || root.Kind == "noqueue" {
		return nil, nil
	}
	refuse := func() ([]string, error) {
		return nil, fmt.Errorf("%s has an unmanaged %s queue whose recovery is unsupported; change it through its owner before shaping", sh.Device, root.Kind)
	}
	if root.Kind != "fq_codel" || !regexp.MustCompile(`^[0-9a-fA-F]+:$`).MatchString(root.Handle) {
		return refuse()
	}
	out, err := run(ctx, "tc", "-j", "filter", "show", "dev", sh.Device, "root")
	if err != nil {
		return nil, fmt.Errorf("reading existing queue filters: %w", err)
	}
	if !rootFiltersClear(out) {
		return refuse()
	}
	if len(root.Options) == 0 {
		return refuse()
	}
	line := fmt.Sprintf("qdisc replace dev %s root handle %s fq_codel", sh.Device, root.Handle)
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
	return []string{line}, nil
}

// tc's root listing also includes ingress/clsact filters on some releases.
// They survive root removal, so only filters attached to the root tree block it.
func rootFiltersClear(out string) bool {
	var filters []struct {
		Parent string `json:"parent"`
	}
	if json.Unmarshal([]byte(out), &filters) != nil {
		return false
	}
	for _, filter := range filters {
		if filter.Parent != "ffff:" && filter.Parent != "ffff:fff2" && filter.Parent != "ffff:fff3" {
			return false
		}
	}
	return true
}

type tcClass struct {
	Kind   string `json:"class"`
	Handle string `json:"handle"`
	Root   bool   `json:"root"`
	Parent string `json:"parent"`
	Rate   uint64 `json:"rate"`
	Ceil   uint64 `json:"ceil"`
}

type tcFilter struct {
	Protocol string `json:"protocol"`
	Pref     int    `json:"pref"`
	Kind     string `json:"kind"`
	Chain    int    `json:"chain"`
	Options  struct {
		Actions []struct {
			Kind    string `json:"kind"`
			Control struct {
				Type string `json:"type"`
			} `json:"control_action"`
		} `json:"actions"`
	} `json:"options"`
}

// tc expresses rates in bytes/second in JSON; requests use decimal kbit/second.
func shapeBytes(kbit int) uint64 { return uint64(kbit) * 125 }

func verifyShaping(ctx context.Context, sh ShapeSpec) error {
	qs, err := deviceQdiscs(ctx, sh.Device)
	if err != nil {
		return err
	}
	rootKind := sh.Qdisc
	if sh.EgressKbit > 0 && sh.Qdisc != "cake" {
		rootKind = "htb"
	}
	var root *tcQdisc
	ingress := false
	leaf := false
	for i := range qs {
		q := &qs[i]
		if q.Root {
			root = q
		}
		if q.Kind == "ingress" && q.Handle == "ffff:" {
			ingress = true
		}
		kind := sh.Qdisc
		if kind == "" {
			kind = "fq_codel"
		}
		if q.Parent == "1:10" && q.Handle == "10:" && q.Kind == kind {
			leaf = true
		}
	}
	gotRoot := ""
	if root != nil {
		gotRoot = root.Kind
	}
	if rootKind != "" && gotRoot != rootKind {
		return shapingDrift("%s has %q as its queue after setting %q", sh.Device, gotRoot, rootKind)
	}
	if sh.EgressKbit > 0 {
		if sh.Qdisc == "cake" {
			var bandwidth uint64
			if json.Unmarshal(root.Options["bandwidth"], &bandwidth) != nil || bandwidth != shapeBytes(sh.EgressKbit) {
				return shapingDrift("%s CAKE bandwidth does not match %d kbit/s", sh.Device, sh.EgressKbit)
			}
		} else {
			var def string
			if root.Handle != "1:" || json.Unmarshal(root.Options["default"], &def) != nil || def != "0x10" || !leaf {
				return shapingDrift("%s HTB root/default class or leaf queue does not match the requested shaping", sh.Device)
			}
			out, err := run(ctx, "tc", "-j", "class", "show", "dev", sh.Device)
			if err != nil {
				return fmt.Errorf("reading shaping classes: %w", err)
			}
			var classes []tcClass
			if json.Unmarshal([]byte(out), &classes) != nil {
				return errors.New("tc printed unreadable shaping classes")
			}
			if len(classes) != 1 || classes[0].Kind != "htb" || classes[0].Handle != "1:10" || (!classes[0].Root && classes[0].Parent != "1:") || classes[0].Rate != shapeBytes(sh.EgressKbit) || classes[0].Ceil != shapeBytes(sh.EgressKbit) {
				return shapingDrift("%s HTB class rate/ceil does not match %d kbit/s", sh.Device, sh.EgressKbit)
			}
		}
	}
	if sh.IngressKbit > 0 {
		if !ingress {
			return shapingDrift("%s has no ingress policer after setting one", sh.Device)
		}
		out, err := run(ctx, "tc", "-j", "filter", "show", "dev", sh.Device, "parent", "ffff:")
		if err != nil {
			return fmt.Errorf("reading ingress filters: %w", err)
		}
		var filters []tcFilter
		if json.Unmarshal([]byte(out), &filters) != nil {
			return errors.New("tc printed unreadable ingress filters")
		}
		found := 0
		for _, f := range filters {
			if f.Pref != 1 || f.Protocol != "all" || f.Kind != "matchall" || f.Chain != 0 {
				return shapingDrift("%s ingress contains filters beyond the requested policer", sh.Device)
			}
			// iproute2 can emit a descriptor and a detailed record for one
			// filter. Count actual actions rather than treating both as rules.
			if len(f.Options.Actions) == 0 {
				continue
			}
			if len(f.Options.Actions) == 1 && f.Options.Actions[0].Kind == "police" && f.Options.Actions[0].Control.Type == "drop" {
				found++
			} else {
				return shapingDrift("%s ingress filter actions differ from the requested policer", sh.Device)
			}
		}
		if found != 1 {
			return shapingDrift("%s ingress matchall/drop policer is missing", sh.Device)
		}
		// iproute2 releases omit policer rate/burst from JSON, including 6.14.
		// Detailed output is required as evidence rather than assuming the write worked.
		out, err = run(ctx, "tc", "-r", "-d", "filter", "show", "dev", sh.Device, "parent", "ffff:", "pref", "1")
		if err != nil {
			return fmt.Errorf("reading ingress policer parameters: %w", err)
		}
		if err := verifyPolicer(out, sh.IngressKbit); err != nil {
			return fmt.Errorf("%s: %w", sh.Device, err)
		}
	}
	return nil
}

var policeParams = regexp.MustCompile(`police\s+\S+\s+rate\s+([0-9.]+)([KMG]?bit)\s+burst\s+([0-9.]+)([KMG]?b)\s+mtu\s+\S+(?:\s+\[[0-9a-fA-F]+\])?\s+action\s+drop`)

func verifyPolicer(out string, kbit int) error {
	matches := policeParams.FindAllStringSubmatch(out, -1)
	if len(matches) != 1 {
		return errors.New("ingress policer rate/burst/drop parameters are unreadable")
	}
	m := matches[0]
	units := map[string]float64{"bit": 1, "Kbit": 1e3, "Mbit": 1e6, "Gbit": 1e9, "b": 1, "Kb": 1024, "Mb": 1024 * 1024, "Gb": 1024 * 1024 * 1024}
	rate, _ := strconv.ParseFloat(m[1], 64)
	rate *= units[m[2]]
	burst, _ := strconv.ParseFloat(m[3], 64)
	burst *= units[m[4]]
	// The kernel converts bucket bytes to clock ticks and back. Permit only
	// four microseconds of quantization, not an operationally different burst.
	tolerance := math.Max(16, float64(shapeBytes(kbit))/250000)
	if rate != float64(kbit)*1000 || math.Abs(burst-float64(policeBurst(kbit))) > tolerance {
		return shapingDrift("ingress policer rate/burst does not match %d kbit/s and %d bytes", kbit, policeBurst(kbit))
	}
	return nil
}

// QdiscStat is a queue discipline's counters.
type QdiscStat struct {
	Kind       string `json:"kind"`
	Bytes      uint64 `json:"bytes"`
	Packets    uint64 `json:"packets"`
	Drops      uint64 `json:"drops"`
	Overlimits uint64 `json:"overlimits"`
	Requeues   uint64 `json:"requeues"`
	Backlog    uint64 `json:"backlog"`
}

func qdiscStat(q tcQdisc) *QdiscStat {
	return &QdiscStat{Kind: q.Kind, Bytes: q.Bytes, Packets: q.Packets, Drops: q.Drops,
		Overlimits: q.Overlimits, Requeues: q.Requeues, Backlog: q.Backlog}
}

// ShapeDevice is one device's queue as the Traffic page shows it.
type ShapeDevice struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Root is the device's root queue; Leaf the queue beneath a shaper, where
	// the drops are counted.
	Root *QdiscStat `json:"root"`
	Leaf *QdiscStat `json:"leaf"`
	// Ingress is whether the device has an ingress queue, which is where a
	// download limit lives.
	Ingress bool `json:"ingress"`
	// Managed is whether the dashboard shaped it, and the rest what it set.
	Managed     bool   `json:"managed"`
	Qdisc       string `json:"qdisc"`
	EgressKbit  int    `json:"egressKbit"`
	IngressKbit int    `json:"ingressKbit"`
	Uplink      bool   `json:"uplink"`
	ClientPath  bool   `json:"clientPath"`
	// Shapeable is false for loopback and for container veths; Guard says why.
	Shapeable    bool               `json:"shapeable"`
	Guard        string             `json:"guard"`
	Verification *ShapeVerification `json:"verification,omitempty"`
}

// BBRState is the kernel's congestion control and default queue.
type BBRState struct {
	// Available is whether bbr is among the algorithms the kernel offers;
	// when it is not, the module has to be loaded on the host first.
	Available    bool     `json:"available"`
	Active       bool     `json:"active"`
	Congestion   string   `json:"congestion"`
	Algorithms   []string `json:"algorithms"`
	DefaultQdisc string   `json:"defaultQdisc"`
	// Managed is whether the dashboard set it, so the boot unit restores it.
	Managed bool `json:"managed"`
}

// ShapingView is everything the shaping section draws.
type ShapingView struct {
	Devices []ShapeDevice `json:"devices"`
	BBR     BBRState      `json:"bbr"`
	// Qdiscs are the disciplines the page offers.
	Qdiscs []string `json:"qdiscs"`
}

// Shaping reads every device's queue with what the dashboard set on it.
func (s *Service) Shaping(ctx context.Context, client string) (*ShapingView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	linkOut, err := run(ctx, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return nil, err
	}
	var links []ipLink
	if err := json.Unmarshal([]byte(linkOut), &links); err != nil {
		return nil, fmt.Errorf("ip link printed something unreadable: %w", err)
	}
	tcOut, err := run(ctx, "tc", "-j", "-s", "qdisc", "show")
	if err != nil {
		return nil, err
	}
	qs, err := parseQdiscs(tcOut)
	if err != nil {
		return nil, err
	}
	uplinks := readUplinks(ctx)
	path, _ := clientPath(ctx, client)

	byDev := map[string][]tcQdisc{}
	for _, q := range qs {
		byDev[q.Dev] = append(byDev[q.Dev], q)
	}
	spec := map[string]ShapeSpec{}
	for _, sh := range sp.Shaping {
		spec[sh.Device] = sh
	}
	v := &ShapingView{Devices: make([]ShapeDevice, 0, len(links)), Qdiscs: shapeQdiscs}
	for _, l := range links {
		d := ShapeDevice{
			Name: l.IfName, Uplink: uplinks[l.IfName],
			ClientPath: !path.Local && path.Device != "" && path.Device == l.IfName,
			Shapeable:  true,
		}
		switch {
		case l.LinkType == "loopback":
			d.Kind = "loopback"
		case l.LinkInfo.InfoKind == "":
			d.Kind = "physical"
		default:
			d.Kind = l.LinkInfo.InfoKind
		}
		if d.Kind == "loopback" || d.Kind == "veth" {
			d.Shapeable = false
			d.Guard = "Shape the interface this one leads to; a loopback or one end of a veth pair carries no traffic of its own to limit."
		}
		for _, q := range byDev[l.IfName] {
			switch {
			case q.Kind == "ingress":
				d.Ingress = true
			case q.Root:
				d.Root = qdiscStat(q)
			case d.Leaf == nil:
				d.Leaf = qdiscStat(q)
			}
		}
		if sh, ok := spec[l.IfName]; ok {
			d.Managed, d.Qdisc, d.EgressKbit, d.IngressKbit = true, sh.Qdisc, sh.EgressKbit, sh.IngressKbit
			verifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			d.Verification = readShapeVerification(verifyCtx, sh)
			cancel()
		}
		v.Devices = append(v.Devices, d)
	}
	sort.SliceStable(v.Devices, func(i, j int) bool {
		a, b := v.Devices[i], v.Devices[j]
		if a.Uplink != b.Uplink {
			return a.Uplink
		}
		return a.Name < b.Name
	})
	v.BBR = readBBR(sp)
	return v, nil
}

func readBBR(sp *Spec) BBRState {
	st := BBRState{Algorithms: []string{}}
	st.Congestion, _ = gatewayReadSysctl("net.ipv4.tcp_congestion_control")
	st.DefaultQdisc, _ = gatewayReadSysctl("net.core.default_qdisc")
	if avail, ok := gatewayReadSysctl("net.ipv4.tcp_available_congestion_control"); ok {
		st.Algorithms = strings.Fields(avail)
	}
	for _, a := range st.Algorithms {
		if a == "bbr" {
			st.Available = true
		}
	}
	st.Active = st.Congestion == "bbr"
	_, c := sp.Sysctls[bbrCongestionKey]
	_, q := sp.Sysctls[bbrQdiscKey]
	st.Managed = c && q
	return st
}

const (
	bbrCongestionKey = "net.ipv4.tcp_congestion_control"
	bbrQdiscKey      = "net.core.default_qdisc"
)

// SetBBR turns BBR congestion control on, with the fq queue it is meant to
// run over, or puts the kernel's usual cubic and fq_codel back. The module is
// not loaded from here: modprobe is a change to the running kernel this
// dashboard does not make, so a host without bbr is told to load it.
func (s *Service) SetBBR(ctx context.Context, on bool, actor string) error {
	want := map[string]string{bbrCongestionKey: "cubic", bbrQdiscKey: "fq_codel"}
	if on {
		want = map[string]string{bbrCongestionKey: "bbr", bbrQdiscKey: "fq"}
		avail, _ := gatewayReadSysctl("net.ipv4.tcp_available_congestion_control")
		if !strings.Contains(" "+avail+" ", " bbr ") {
			return errors.New("this kernel does not have BBR loaded (the available algorithms are " + strings.TrimSpace(avail) + "). Load it on the host with `modprobe tcp_bbr`, then try again")
		}
	}
	keys := []string{bbrCongestionKey, bbrQdiscKey}

	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := old.clone()
	prev := map[string]string{}
	for _, k := range keys {
		prev[k], _ = gatewayReadSysctl(k)
		if on {
			next.Sysctls[k] = want[k]
		} else {
			delete(next.Sysctls, k)
		}
	}
	return s.commit(ctx, next, step{
		apply:  func(ctx context.Context) error { return writeSysctls(ctx, keys, want, prev) },
		undo:   func(ctx context.Context) { restoreSysctls(ctx, keys, prev) },
		verify: func(ctx context.Context) error { return verifySysctls(ctx, keys, want) },
	})
}
