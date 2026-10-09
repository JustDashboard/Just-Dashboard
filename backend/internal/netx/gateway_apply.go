package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ForwardingRequiredError is a forward or NAT entry that was refused because the
// kernel is not forwarding. It is its own type because the handler answers it
// with a code the page turns into a link to the Routing page, not a sentence.
type ForwardingRequiredError struct {
	// Family is "4" or "6".
	Family string
}

func (e *ForwardingRequiredError) Error() string {
	return fmt.Sprintf("IPv%s forwarding is off, so nothing could pass through this server to the address. Turn it on on the Routing page first.", e.Family)
}

// gatewayForwardingOn reads the kernel's forwarding switch for a family.
//
// The switch belongs to the Routing page (SetForwarding), which checks what
// turning it on would do to this server's own IPv6 addresses; an entry that
// needs forwarding is refused while it is off rather than flipping it here,
// so there is one place that owns the setting and its guard.
func gatewayForwardingOn(family string) bool {
	path := "net/ipv4/ip_forward"
	if family == "6" {
		path = "net/ipv6/conf/all/forwarding"
	}
	b, err := os.ReadFile(filepath.Join(gatewaySysRoot, path))
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

// applyFile is where a candidate ruleset is written for nft to read; removed
// whether or not it loaded.
const gatewayApplyFile = ".gateway.apply.nft"

// loadGateway loads a spec's gateway ruleset into the kernel. The same
// bytes the boot unit reads, so what runs now is what comes back after a
// reboot.
func (s *Service) loadGateway(ctx context.Context, sp *Spec) error {
	ruleset, err := renderGateway(sp, s.trustedFor(sp))
	if err != nil {
		return err
	}
	return s.loadGatewayRules(ctx, ruleset)
}

func (s *Service) loadGatewayRules(ctx context.Context, ruleset string) error {
	if err := os.MkdirAll(s.paths.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.paths.Dir, gatewayApplyFile)
	if err := os.WriteFile(path, []byte(ruleset), 0o600); err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := run(ctx, "nft", "-f", path); err != nil {
		return fmt.Errorf("loading the gateway ruleset: %w", err)
	}
	return nil
}

// gatewayEmpty reports whether a spec would render a table with nothing in
// it but the trusted sets.
func gatewayEmpty(sp *Spec) bool {
	for _, f := range sp.Forwards {
		if f.Enabled {
			return false
		}
	}
	for _, n := range sp.NAT {
		if n.Enabled {
			return false
		}
	}
	for _, l := range sp.Limits {
		if l.Enabled {
			return false
		}
	}
	for _, b := range sp.Blocklists {
		if b.Enabled {
			return false
		}
	}
	return true
}

// restoreGateway puts the kernel's gateway table back to what a spec
// describes. Best effort, because it runs on the way out of a failure.
func (s *Service) restoreGateway(ctx context.Context, sp *Spec) {
	if gatewayEmpty(sp) {
		_, err := run(ctx, "nft", "delete", "table", "inet", gatewayTable)
		if err != nil && !isGone(err) {
			recordRecoveryError(ctx, err)
		}
		return
	}
	if err := s.loadGateway(ctx, sp); err != nil {
		recordRecoveryError(ctx, err)
		s.log.Error("restoring the gateway ruleset after a failed change", "err", err)
	}
}

// admissionDeleteCap bounds how many copies of the rule one removal takes out.
const admissionDeleteCap = 8

// syncAdmission makes the iptables admission rules what target needs: present
// and first when anything translates, absent when nothing does. Idempotent —
// the commands delete before they insert — and run on every change while
// something translates rather than only when the need flips, because a `ufw
// reload` or a Docker restart rebuilds the chains and takes the rule with
// them; the next change here puts it back. When neither the spec before nor
// after has anything to admit there is nothing to put in or take out.
func (s *Service) syncAdmission(ctx context.Context, target, prior *Spec) error {
	insert := needsAdmission(target)
	if !insert && !needsAdmission(prior) {
		return nil
	}
	affected := admissionFamilies(prior)
	for family := range admissionFamilies(target) {
		affected[family] = true
	}
	for _, cmd := range admissionCommands(insert) {
		family := "inet"
		if cmd[0] == "ip6tables" {
			family = "inet6"
		}
		if !affected[family] {
			continue
		}
		if cmd[1] == "-I" {
			if !admissionFamilies(target)[family] {
				continue
			}
			ch := inspectAdmissionChain(ctx, cmd[0], cmd[2], family, true)
			if ch.Status == "unsupported" {
				if ch.Needed {
					return fmt.Errorf("admission is required in %s/%s but unavailable: %s", family, cmd[2], ch.Reason)
				}
				continue
			}
			if ch.Status == "unreadable" {
				return fmt.Errorf("reading admission chain %s/%s: %s", family, cmd[2], ch.Reason)
			}
		}
		if cmd[1] == "-D" {
			// One -D removes one copy of the rule. Duplicates arise — an
			// earlier run that died between the delete and the insert, a
			// boot unit started twice — and an inserted rule that leaves
			// another beneath it is not removed when the translations are.
			// Delete until the kernel says there is none left, a few times
			// at most. The failure that ends it is the common case.
			for i := 0; i < admissionDeleteCap; i++ {
				if out, err := run(ctx, cmd[0], cmd[1:]...); err != nil {
					if recoveryExpectedAbsence(cmd[0], out, err) {
						break
					}
					var unavailable *UnavailableError
					if errors.As(err, &unavailable) {
						state := inspectAdmissionChain(ctx, cmd[0], cmd[2], family, true)
						if state.Status == "unsupported" && !state.Needed {
							break
						}
					}
					return fmt.Errorf("removing owned admission from %s/%s: %w", family, cmd[2], err)
				}
			}
			continue
		}
		_, err := run(ctx, cmd[0], cmd[1:]...)
		if err == nil {
			continue
		}
		// Optional absence was checked before insertion. A present chain
		// that fails to accept its rule is a partial apply, even on IPv6.
		return fmt.Errorf("admitting the translated connections through %s: %w", cmd[2], err)
	}
	return nil
}

// gatewayStep is the runtime half of a gateway change: load the new table,
// put the admission rules where they belong, and take both back if anything
// fails. commit does not call undo when apply itself fails, so apply cleans up
// after itself.
func (s *Service) gatewayStep(old, next *Spec) step {
	return s.gatewayStepWithRules(old, next, "")
}

func (s *Service) gatewayStepWithRules(old, next *Spec, previous string, candidate ...string) step {
	restore := func(ctx context.Context) {
		if previous == "" || gatewayEmpty(old) {
			s.restoreGateway(ctx, old)
			return
		}
		if err := s.loadGatewayRules(ctx, previous); err != nil {
			recordRecoveryError(ctx, err)
			s.log.Error("restoring the gateway ruleset after a failed change", "err", err)
		}
	}
	return step{
		apply: func(ctx context.Context) error {
			// The replacement starts every counter at zero; what the old
			// table counted is read into the totals first.
			if !gatewayEmpty(old) {
				s.telemetry.observeCounters(ctx)
			}
			load := func() error { return s.loadGateway(ctx, next) }
			if len(candidate) > 0 {
				load = func() error { return s.loadGatewayRules(ctx, candidate[0]) }
			}
			if err := load(); err != nil {
				return err
			}
			if err := s.syncAdmission(ctx, next, old); err != nil {
				rollback(ctx, func(recovery context.Context) {
					restore(recovery)
					recordRecoveryError(recovery, s.syncAdmission(recovery, old, next))
				})
				return err
			}
			return nil
		},
		undo: func(ctx context.Context) {
			restore(ctx)
			recordRecoveryError(ctx, s.syncAdmission(ctx, old, next))
		},
		verify: func(ctx context.Context) error {
			if _, err := run(ctx, "nft", "list", "set", "inet", gatewayTable, "trusted4"); err != nil {
				return fmt.Errorf("the gateway table is not loaded after applying it: %w", err)
			}
			if a := s.admissionState(ctx, next); a.Needed && !a.Present {
				return fmt.Errorf("translated connections were not admitted in every required chain: %s", admissionFailure(a))
			}
			return nil
		},
	}
}

// mutateGateway is the shape every gateway change has: take the lock, copy
// the spec, edit and judge the copy, refuse where the host cannot honour it,
// and commit. edit returns whether the result puts a translation into force:
// only a forward or a NAT entry needs the firewall's cooperation and the
// kernel's forwarding, because only a translation has to be admitted past the
// host's other filters. Removing and disabling never need the host's
// permission, so an operator can always clear out what they made, even after
// the host switched firewalls.
func (s *Service) mutateGateway(ctx context.Context, edit func(old, next *Spec) (translating bool, err error)) error {
	return s.mutateGatewayWithRollback(ctx, edit, nil)
}

// Cache edits share the gateway lock and rollback. Capture the old rendered
// rules before an edit can replace a feed file, so restoring the old spec
// cannot accidentally reload the failed candidate's networks.
func (s *Service) mutateGatewayWithRollback(ctx context.Context, edit func(old, next *Spec) (bool, error), undoEdit func()) (err error) {
	return s.mutateGatewayCandidate(ctx, edit, undoEdit, nil)
}

type gatewayCacheChange struct {
	before recoverySnapshot
	nets   []netip.Prefix
}

func (s *Service) mutateGatewayWithCache(ctx context.Context, edit func(old, next *Spec, stage func(int, []netip.Prefix) error) (bool, error)) error {
	var changes []gatewayCacheChange
	return s.mutateGatewayCandidate(ctx, func(old, next *Spec) (bool, error) {
		return edit(old, next, func(id int, nets []netip.Prefix) error {
			path := blocklistFile(filepath.Join(s.paths.Dir, "lists"), id)
			if err := validateRecoveryBlocklistPath(s.paths.Dir, path); err != nil {
				return err
			}
			previous, err := saveNetworkFile(path)
			if err != nil {
				return fmt.Errorf("reading the blocklist cache before replacement: %w", err)
			}
			changes = append(changes, gatewayCacheChange{before: recoverySnapshot{Path: path, Data: previous.data, Mode: previous.perm, Exists: previous.exists}, nets: nets})
			return nil
		})
	}, nil, &changes)
}

func (s *Service) mutateGatewayCandidate(ctx context.Context, edit func(old, next *Spec) (bool, error), undoEdit func(), cacheChanges *[]gatewayCacheChange) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil && undoEdit != nil {
			var saved *persistenceError
			if !errors.As(err, &saved) {
				undoEdit()
			}
		}
	}()
	old, err := s.loadSpec()
	if err != nil {
		return err
	}
	previous, renderErr := renderGateway(old, s.trustedFor(old))
	if renderErr != nil {
		// Cache loss must not prevent disabling, removing or refreshing the
		// broken list. The last committed render retains its previous data
		// and is the rollback source until the candidate is validated.
		b, readErr := os.ReadFile(filepath.Join(s.paths.Dir, gatewayFile))
		if readErr != nil {
			return fmt.Errorf("reading the last applied gateway policy after cache failure: %v (%w)", readErr, renderErr)
		}
		previous = string(b)
	}
	next := old.clone()
	translating, err := edit(old, next)
	if err != nil {
		return err
	}
	if translating {
		if _, err := s.requireWritable(ctx, next); err != nil {
			return err
		}
		if err := checkNewForwarding(old, next); err != nil {
			return err
		}
	}
	if err := s.resolveAutoNAT(ctx, next); err != nil {
		return err
	}
	if cacheChanges == nil || len(*cacheChanges) == 0 {
		return s.commit(ctx, next, s.gatewayStepWithRules(old, next, previous))
	}
	candidates := map[int][]netip.Prefix{}
	for _, change := range *cacheChanges {
		id, parseErr := strconv.Atoi(strings.TrimSuffix(filepath.Base(change.before.Path), ".txt"))
		if parseErr != nil {
			return parseErr
		}
		candidates[id] = change.nets
	}
	rules, err := renderGatewayCandidate(next, s.trustedFor(next), candidates)
	if err != nil {
		return err
	}
	st := s.gatewayStepWithRules(old, next, previous, rules)
	st.gatewayRules = &rules
	for _, change := range *cacheChanges {
		st.recoveryFiles = append(st.recoveryFiles, change.before)
	}
	apply, undo := st.apply, st.undo
	var wrote []recoverySnapshot
	restoreCaches := func(ctx context.Context) {
		for i := len(wrote) - 1; i >= 0; i-- {
			f := wrote[i]
			recordRecoveryError(ctx, (savedNetworkFile{data: f.Data, perm: f.Mode, exists: f.Exists}).restore(f.Path))
		}
	}
	st.apply = func(ctx context.Context) error {
		for _, change := range *cacheChanges {
			// Rename may have succeeded even when the writer reports a later
			// fsync error. Every attempted replacement needs its snapshot.
			wrote = append(wrote, change.before)
			if err := writeNetworkFile(change.before.Path, blocklistCacheBytes(change.nets), 0o644); err != nil {
				rollback(ctx, restoreCaches)
				return fmt.Errorf("saving the fetched list: %w", err)
			}
		}
		if err := apply(ctx); err != nil {
			rollback(ctx, restoreCaches)
			return err
		}
		return nil
	}
	st.undo = func(ctx context.Context) { undo(ctx); restoreCaches(ctx) }
	return s.commit(ctx, next, st)
}

// checkNewForwarding refuses an entry that starts carrying traffic through
// the host while the kernel is not forwarding. Only entries this change made
// or enabled are checked: turning forwarding off later is the Routing page's
// guarded decision, and an edit to a working entry is not the moment to
// complain about it.
func checkNewForwarding(old, next *Spec) error {
	was := map[string]bool{}
	for _, f := range old.Forwards {
		was["f"+strconv.Itoa(f.ID)] = f.Enabled
	}
	for _, n := range old.NAT {
		was["n"+strconv.Itoa(n.ID)] = n.Enabled
	}
	need := map[string]bool{}
	for _, f := range next.Forwards {
		if f.Enabled && !was["f"+strconv.Itoa(f.ID)] {
			if t, err := ParseAddr(f.Target); err == nil {
				need[familyDigit(t)] = true
			}
		}
	}
	for _, n := range next.NAT {
		if n.Enabled && !was["n"+strconv.Itoa(n.ID)] {
			if p, err := ParsePrefix(n.Source); err == nil {
				need[familyDigit(p.Addr())] = true
			}
		}
	}
	for _, fam := range []string{"4", "6"} {
		if need[fam] && !gatewayForwardingOn(fam) {
			return &ForwardingRequiredError{Family: fam}
		}
	}
	return nil
}

func familyDigit(a netip.Addr) string {
	if a.Is4() {
		return "4"
	}
	return "6"
}

// resolveAutoNAT decides, for every forward whose source translation is
// "auto", whether its flow is masqueraded, and stores the answer in the spec
// (see storeNAT for why it is stored). Masquerading is what makes the reply
// come back through this host; it is unnecessary — and costs the target the
// visitor's real address — when the target is on a network this host is the
// router of, because the reply then has no other way home.
//
// A network the uplink is on does not count as one this host routes: a target
// on the same LAN as the public interface answers the visitor through the
// LAN's own router, bypassing the translation, and the visitor sees a reply
// from an address it never contacted.
func (s *Service) resolveAutoNAT(ctx context.Context, sp *Spec) error {
	auto := false
	for _, f := range sp.Forwards {
		if c, _, _ := splitNAT(f.SourceNAT); c == natAuto {
			auto = true
		}
	}
	if !auto {
		return nil
	}
	out, err := run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return fmt.Errorf("the host's addresses could not be read to decide whether a forward needs its source translated: %w", err)
	}
	var addrs []ipAddr
	if err := json.Unmarshal([]byte(out), &addrs); err != nil {
		return fmt.Errorf("ip addr printed something unreadable: %w", err)
	}
	uplinks := readUplinks(ctx)
	for i := range sp.Forwards {
		f := &sp.Forwards[i]
		if c, _, _ := splitNAT(f.SourceNAT); c != natAuto {
			continue
		}
		target, err := ParseAddr(f.Target)
		if err != nil {
			continue
		}
		f.SourceNAT = storeNAT(natAuto, !routedHere(addrs, uplinks, target))
	}
	return nil
}

// routedHere reports whether a target is this host's own address or on a
// network of a device that is not the uplink.
func routedHere(addrs []ipAddr, uplinks map[string]bool, target netip.Addr) bool {
	here, _ := autoNATReason(addrs, uplinks, target)
	return here
}

// autoNATReason is routedHere with the sentence that explains it.
func autoNATReason(addrs []ipAddr, uplinks map[string]bool, target netip.Addr) (bool, string) {
	onUplink := ""
	for _, a := range addrs {
		if a.IfName == "lo" {
			continue
		}
		for _, info := range a.AddrInfo {
			local, err := netip.ParseAddr(info.Local)
			if err != nil {
				continue
			}
			if local == target {
				return true, fmt.Sprintf("%s is this server's own address on %s, so replies come back through it.", target, a.IfName)
			}
			network := netip.PrefixFrom(local, info.PrefixLen).Masked()
			if !network.Contains(target) {
				continue
			}
			if uplinks[a.IfName] {
				onUplink = a.IfName
				continue
			}
			return true, fmt.Sprintf("%s is on %s (%s), a network this server routes, so the visitor's address can be kept.", target, network, a.IfName)
		}
	}
	if onUplink != "" {
		return false, fmt.Sprintf("%s shares the uplink's network on %s; its replies would bypass this server unless they are masqueraded.", target, onUplink)
	}
	return false, fmt.Sprintf("%s is not on a network this server routes; masquerading makes its replies come back through this server.", target)
}

// ifaceAddresses reads a device's addresses, and so also whether it exists.
func ifaceAddresses(ctx context.Context, name string) ([]netip.Addr, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	out, err := run(ctx, "ip", "-j", "addr", "show", "dev", name)
	if err != nil {
		var missing *UnavailableError
		if errors.As(err, &missing) {
			return nil, err
		}
		return nil, fmt.Errorf("there is no interface called %s on this host", name)
	}
	var devs []ipAddr
	if err := json.Unmarshal([]byte(out), &devs); err != nil {
		return nil, fmt.Errorf("ip addr printed something unreadable: %w", err)
	}
	var addrs []netip.Addr
	for _, d := range devs {
		for _, info := range d.AddrInfo {
			if a, err := netip.ParseAddr(info.Local); err == nil {
				addrs = append(addrs, a)
			}
		}
	}
	return addrs, nil
}

// ----------------------------------------------------------------------------
// Port forwards

// ForwardRequest is the body of a forward's create and update. An update
// replaces every field but Enabled, which keeps its value when omitted.
type ForwardRequest struct {
	Name       string   `json:"name"`
	Protocol   string   `json:"protocol"`
	Interface  string   `json:"interface"`
	Ports      string   `json:"ports"`
	Target     string   `json:"target"`
	TargetPort string   `json:"targetPort"`
	SourceNAT  string   `json:"sourceNat"`
	Sources    []string `json:"sources"`
	Enabled    *bool    `json:"enabled"`
}

// AddForward creates a port forward. protected are ports this server is
// itself reached on (SSH, the dashboard's); a forward that would take one
// over from the reader is refused.
func (s *Service) AddForward(ctx context.Context, req ForwardRequest, client, actor string, protected []int) (ForwardView, error) {
	return s.saveForward(ctx, 0, req, client, actor, protected)
}

// UpdateForward replaces a forward's settings, and enables or disables it.
func (s *Service) UpdateForward(ctx context.Context, id int, req ForwardRequest, client, actor string, protected []int) (ForwardView, error) {
	return s.saveForward(ctx, id, req, client, actor, protected)
}

func (s *Service) saveForward(ctx context.Context, id int, req ForwardRequest, client, actor string, protected []int) (ForwardView, error) {
	var saved ForwardSpec
	err := s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		idx := -1
		for i, f := range next.Forwards {
			if f.ID == id {
				idx = i
			}
		}
		if id != 0 && idx < 0 {
			return false, fmt.Errorf("forward %d: %w", id, ErrNotFound)
		}
		f := ForwardSpec{
			Name: req.Name, Protocol: req.Protocol, Interface: req.Interface, Ports: req.Ports,
			Target: req.Target, TargetPort: req.TargetPort, Sources: req.Sources,
			SourceNAT: strings.TrimSpace(req.SourceNAT), Enabled: true,
		}
		if f.SourceNAT == "" {
			f.SourceNAT = natAuto
		}
		if _, _, ok := splitNAT(f.SourceNAT); !ok || strings.Contains(f.SourceNAT, ":") {
			return false, fmt.Errorf("source translation is auto, always or never")
		}
		if idx >= 0 {
			f.ID, f.Made, f.Enabled = next.Forwards[idx].ID, next.Forwards[idx].Made, next.Forwards[idx].Enabled
		}
		if req.Enabled != nil {
			f.Enabled = *req.Enabled
		}
		f, err := normForward(f)
		if err != nil {
			return false, err
		}
		if f.Interface != "" {
			if _, err := ifaceAddresses(ctx, f.Interface); err != nil {
				return false, err
			}
		}
		for _, other := range next.Forwards {
			if other.ID != f.ID && forwardsCollide(other, f) {
				return false, fmt.Errorf("%w: %q already forwards %s on %s", ErrExists, other.Name, other.Ports, other.Protocol)
			}
		}
		if f.Enabled {
			if err := s.guardForward(ctx, f, client, protected); err != nil {
				return false, err
			}
		}
		f.ChangedAt = gatewayNow().UTC()
		if idx < 0 {
			f.ID, f.Made = next.takeID(), gwStamp(actor)
			next.Forwards = append(next.Forwards, f)
		} else {
			next.Forwards[idx] = f
		}
		saved = f
		return f.Enabled, nil
	})
	if err != nil {
		return ForwardView{}, err
	}
	// The stored entry, not the edited copy: auto source translation was
	// decided between the two.
	sp, err := s.loadSpec()
	if err != nil {
		return ForwardView{}, err
	}
	for _, f := range sp.Forwards {
		if f.ID == saved.ID {
			saved = f
		}
	}
	return forwardView(saved, nil), nil
}

// forwardsCollide reports whether two enabled forwards would both claim the
// same traffic: the same protocol and an overlapping port on the same device
// from the same sources. The first rule would win silently and the second
// would look as if it worked.
func forwardsCollide(a, b ForwardSpec) bool {
	if !a.Enabled || !b.Enabled || a.Interface != b.Interface {
		return false
	}
	if a.Protocol != b.Protocol && a.Protocol != "both" && b.Protocol != "both" {
		return false
	}
	if len(a.Sources) != len(b.Sources) {
		return false
	}
	for i := range a.Sources {
		if a.Sources[i] != b.Sources[i] {
			return false
		}
	}
	alo, ahi := portBounds(a.Ports)
	blo, bhi := portBounds(b.Ports)
	return alo <= bhi && blo <= ahi
}

func portBounds(ports string) (lo, hi int) {
	l, h, ok := strings.Cut(ports, "-")
	lo, _ = strconv.Atoi(l)
	if !ok {
		return lo, lo
	}
	hi, _ = strconv.Atoi(h)
	return lo, hi
}

// guardForward refuses a forward that would take over a port this server is
// reached on from the reader. A forward on port 22 or 443 captures every new
// connection to it on that device before the host's own service sees it, the
// reader's included.
func (s *Service) guardForward(ctx context.Context, f ForwardSpec, client string, protected []int) error {
	if f.Protocol == "udp" || len(protected) == 0 {
		return nil
	}
	lo, hi := portBounds(f.Ports)
	var hit []string
	for _, p := range protected {
		if p >= lo && p <= hi {
			hit = append(hit, strconv.Itoa(p))
		}
	}
	if len(hit) == 0 {
		return nil
	}
	// A forward limited to other networks leaves the reader alone.
	if addr, err := ParseAddr(client); err == nil && len(f.Sources) > 0 {
		covered := false
		for _, src := range f.Sources {
			if p, err := ParsePrefix(src); err == nil && p.Contains(addr) {
				covered = true
			}
		}
		if !covered {
			return nil
		}
	}
	if f.Interface != "" {
		path, err := clientPath(ctx, client)
		if err != nil {
			return err
		}
		if path.Local || path.Device != f.Interface {
			return nil
		}
	}
	return guarded("port %s is how this server answers you (SSH or the dashboard), and a forward on it would send your own connection to %s instead. Limit the forward to other source networks or another interface, or forward a different port.",
		strings.Join(hit, ", "), f.Target)
}

// DeleteForward removes a forward.
func (s *Service) DeleteForward(ctx context.Context, id int) error {
	return s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i, f := range next.Forwards {
			if f.ID == id {
				next.Forwards = append(next.Forwards[:i], next.Forwards[i+1:]...)
				return false, nil
			}
		}
		return false, fmt.Errorf("forward %d: %w", id, ErrNotFound)
	})
}

// ----------------------------------------------------------------------------
// NAT

// NATRequest is the body of a NAT entry's create and update.
type NATRequest struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Interface string `json:"interface"`
	ToAddress string `json:"toAddress"`
	// Mode is masquerade or snat (both stored empty), one-to-one or nptv6.
	Mode         string   `json:"mode"`
	Translated   string   `json:"translated"`
	Destinations []string `json:"destinations"`
	Enabled      *bool    `json:"enabled"`
}

// AddNAT creates a NAT entry for a network.
func (s *Service) AddNAT(ctx context.Context, req NATRequest, client, actor string) (NATView, error) {
	return s.saveNAT(ctx, 0, req, client, actor)
}

// UpdateNAT replaces a NAT entry's settings, and enables or disables it.
func (s *Service) UpdateNAT(ctx context.Context, id int, req NATRequest, client, actor string) (NATView, error) {
	return s.saveNAT(ctx, id, req, client, actor)
}

// natFromRequest is the entry a request describes, before normalization.
func natFromRequest(req NATRequest) NATSpec {
	return NATSpec{
		Name: req.Name, Source: req.Source, Interface: req.Interface, ToAddress: req.ToAddress,
		Mode: strings.TrimSpace(req.Mode), Translated: req.Translated, Destinations: req.Destinations, Enabled: true,
	}
}

func (s *Service) saveNAT(ctx context.Context, id int, req NATRequest, client, actor string) (NATView, error) {
	var saved NATSpec
	err := s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		idx := -1
		for i, n := range next.NAT {
			if n.ID == id {
				idx = i
			}
		}
		if id != 0 && idx < 0 {
			return false, fmt.Errorf("NAT entry %d: %w", id, ErrNotFound)
		}
		n := natFromRequest(req)
		if idx >= 0 {
			if owner := next.NAT[idx].Owner; owner != "" {
				return false, fmt.Errorf("%w: it belongs to %s, which keeps it in step with itself; change it there", ErrNotManaged, owner)
			}
			n.ID, n.Made, n.Enabled = next.NAT[idx].ID, next.NAT[idx].Made, next.NAT[idx].Enabled
		}
		if req.Enabled != nil {
			n.Enabled = *req.Enabled
		}
		n, err := normNAT(n)
		if err != nil {
			return false, err
		}
		if err := s.checkNATHost(ctx, n, client); err != nil {
			return false, err
		}
		for _, other := range next.NAT {
			if other.ID != n.ID && natsCollide(other, n) {
				return false, fmt.Errorf("%w: %q already translates %s out of %s", ErrExists, other.Name, other.Source, other.Interface)
			}
		}
		if idx < 0 {
			n.ID, n.Made = next.takeID(), gwStamp(actor)
			next.NAT = append(next.NAT, n)
		} else {
			next.NAT[idx] = n
		}
		saved = n
		return n.Enabled, nil
	})
	if err != nil {
		return NATView{}, err
	}
	return natView(saved, nil), nil
}

// checkNATHost asks the host what a NAT entry depends on: the device exists,
// a fixed or mapped single address is one of its own, and a mapped public
// side does not take the address the requester's own connection arrives on.
func (s *Service) checkNATHost(ctx context.Context, n NATSpec, client string) error {
	addrs, err := ifaceAddresses(ctx, n.Interface)
	if err != nil {
		return err
	}
	owns := func(a netip.Addr) bool {
		for _, have := range addrs {
			if have == a {
				return true
			}
		}
		return false
	}
	if n.ToAddress != "" {
		to, _ := ParseAddr(n.ToAddress)
		if !owns(to) {
			return fmt.Errorf("%s is not an address of %s, so it cannot be the address traffic leaves from", to, n.Interface)
		}
	}
	if !natMapped(n) || !n.Enabled {
		return nil
	}
	to, _ := ParsePrefix(n.Translated)
	if to.IsSingleIP() && !owns(to.Addr()) {
		return fmt.Errorf("%s is not an address of %s; a one-to-one address must be on the device it maps through", to.Addr(), n.Interface)
	}
	if addr, err := ParseAddr(client); err == nil && !addr.IsLoopback() {
		path, err := clientPath(ctx, client)
		if err != nil {
			return err
		}
		if src, err := ParseAddr(path.Source); err == nil && to.Contains(src) {
			return guarded("%s is the address your own connection to this server uses, and mapping %s would send every connection to it, yours included, to %s. Map an address you do not reach the dashboard through.", src, to, n.Source)
		}
	}
	return nil
}

// natsCollide reports two enabled entries that would translate the same
// traffic: overlapping sources out of one device, where only the first rule
// would ever apply, or two mappings claiming overlapping public sides.
func natsCollide(a, b NATSpec) bool {
	if !a.Enabled || !b.Enabled {
		return false
	}
	pa, ea := ParsePrefix(a.Source)
	pb, eb := ParsePrefix(b.Source)
	if ea != nil || eb != nil {
		return false
	}
	if a.Interface == b.Interface && pa.Masked().Overlaps(pb.Masked()) && destinationsOverlap(a.Destinations, b.Destinations) {
		return true
	}
	if natMapped(a) && natMapped(b) {
		ta, e1 := ParsePrefix(a.Translated)
		tb, e2 := ParsePrefix(b.Translated)
		return e1 == nil && e2 == nil && ta.Masked().Overlaps(tb.Masked())
	}
	return false
}

// destinationsOverlap is true when two destination lists can match the same
// packet; an empty list matches everything.
func destinationsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		px, err := ParsePrefix(x)
		if err != nil {
			continue
		}
		for _, y := range b {
			if py, err := ParsePrefix(y); err == nil && px.Masked().Overlaps(py.Masked()) {
				return true
			}
		}
	}
	return false
}

// DeleteNAT removes a NAT entry the operator made. An entry a VPN made for
// itself goes with the VPN.
func (s *Service) DeleteNAT(ctx context.Context, id int) error {
	return s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i, n := range next.NAT {
			if n.ID != id {
				continue
			}
			if n.Owner != "" {
				return false, fmt.Errorf("%w: it belongs to %s and is removed with it", ErrNotManaged, n.Owner)
			}
			next.NAT = append(next.NAT[:i], next.NAT[i+1:]...)
			return false, nil
		}
		return false, fmt.Errorf("NAT entry %d: %w", id, ErrNotFound)
	})
}

// upsertOwnedNAT adds, or updates in place, the NAT entry an owner keeps for
// a source network — a WireGuard exit's masquerade for its clients' subnet.
// It is a pure edit of a spec the caller is about to commit in its own
// change, so the entry and the tunnel appear and disappear together. The
// renderer checks the values; nothing here touches the host.
func upsertOwnedNAT(next *Spec, owner, name, source, iface, actor string) {
	for i := range next.NAT {
		n := &next.NAT[i]
		if n.Owner == owner && n.Source == source {
			n.Name, n.Interface, n.Enabled = name, iface, true
			return
		}
	}
	next.NAT = append(next.NAT, NATSpec{
		ID: next.takeID(), Name: name, Source: source, Interface: iface,
		Owner: owner, Enabled: true, Made: gwStamp(actor),
	})
}

// removeOwnedNAT removes every NAT entry an owner kept.
func removeOwnedNAT(next *Spec, owner string) {
	kept := next.NAT[:0]
	for _, n := range next.NAT {
		if n.Owner != owner {
			kept = append(kept, n)
		}
	}
	next.NAT = kept
}

// ----------------------------------------------------------------------------
// Limits

// LimitRequest is the body of a limit's create and update.
type LimitRequest struct {
	Name              string `json:"name"`
	Protocol          string `json:"protocol"`
	Ports             string `json:"ports"`
	Rate              int    `json:"rate"`
	Per               string `json:"per"`
	Burst             int    `json:"burst"`
	PerSource         bool   `json:"perSource"`
	MaxConnections    int    `json:"maxConnections"`
	GlobalConnections int    `json:"globalConnections"`
	Profile           string `json:"profile"`
	Action            string `json:"action"`
	Enabled           *bool  `json:"enabled"`
}

// AddLimit creates a rate or connection limit on a port.
func (s *Service) AddLimit(ctx context.Context, req LimitRequest, client, actor string) (LimitView, error) {
	return s.saveLimit(ctx, 0, req, client, actor)
}

// UpdateLimit replaces a limit's settings, and enables or disables it.
func (s *Service) UpdateLimit(ctx context.Context, id int, req LimitRequest, client, actor string) (LimitView, error) {
	return s.saveLimit(ctx, id, req, client, actor)
}

func (s *Service) saveLimit(ctx context.Context, id int, req LimitRequest, client, actor string) (LimitView, error) {
	var saved LimitSpec
	err := s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		idx := -1
		for i, l := range next.Limits {
			if l.ID == id {
				idx = i
			}
		}
		if id != 0 && idx < 0 {
			return false, fmt.Errorf("limit %d: %w", id, ErrNotFound)
		}
		l := LimitSpec{
			Name: req.Name, Protocol: req.Protocol, Ports: req.Ports, Rate: req.Rate, Per: req.Per,
			Burst: req.Burst, PerSource: req.PerSource, MaxConnections: req.MaxConnections,
			GlobalConnections: req.GlobalConnections, Profile: strings.TrimSpace(req.Profile),
			Action: req.Action, Enabled: true,
		}
		if l.Action == "" {
			l.Action = "drop"
		}
		if idx >= 0 {
			l.ID, l.Made, l.Enabled = next.Limits[idx].ID, next.Limits[idx].Made, next.Limits[idx].Enabled
		}
		if req.Enabled != nil {
			l.Enabled = *req.Enabled
		}
		l, err := normLimit(l)
		if err != nil {
			return false, err
		}
		// Before anything is added that drops: the reader's own address is
		// kept out of its reach (path.go).
		s.trustClientBy(next, client, actor, "")
		if idx < 0 {
			l.ID, l.Made = next.takeID(), gwStamp(actor)
			next.Limits = append(next.Limits, l)
		} else {
			next.Limits[idx] = l
		}
		saved = l
		// A limit only drops, and a drop at the raw and filter hooks works
		// whatever else filters this host, so it does not wait on the
		// capability that port forwards and NAT do (gateway_capability.go).
		return false, nil
	})
	if err != nil {
		return LimitView{}, err
	}
	return limitView(saved, nil), nil
}

// DeleteLimit removes a limit.
func (s *Service) DeleteLimit(ctx context.Context, id int) error {
	return s.mutateGateway(ctx, func(old, next *Spec) (bool, error) {
		for i, l := range next.Limits {
			if l.ID == id {
				next.Limits = append(next.Limits[:i], next.Limits[i+1:]...)
				return false, nil
			}
		}
		return false, fmt.Errorf("limit %d: %w", id, ErrNotFound)
	})
}

// ----------------------------------------------------------------------------
// Reading

// RuleCounter is what a rule of the gateway table has matched.
type RuleCounter struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// nftRules is `nft -t -j list table`, read for each rule's comment and counter.
type nftRules struct {
	Nftables []struct {
		Rule *struct {
			Comment string `json:"comment"`
			Expr    []struct {
				Counter *struct {
					Packets uint64 `json:"packets"`
					Bytes   uint64 `json:"bytes"`
				} `json:"counter"`
			} `json:"expr"`
		} `json:"rule"`
	} `json:"nftables"`
}

// parseGatewayCounters sums the counters of rules by their comment: a
// protocol "both" is two rules with one comment, and a limit is one rule per
// chain.
func parseGatewayCounters(out string) (map[string]RuleCounter, error) {
	var listing nftRules
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		return nil, fmt.Errorf("nft printed the gateway table in a form this dashboard could not read: %w", err)
	}
	counters := map[string]RuleCounter{}
	for _, o := range listing.Nftables {
		if o.Rule == nil || o.Rule.Comment == "" {
			continue
		}
		for _, e := range o.Rule.Expr {
			if e.Counter == nil {
				continue
			}
			c := counters[o.Rule.Comment]
			c.Packets += e.Counter.Packets
			c.Bytes += e.Counter.Bytes
			counters[o.Rule.Comment] = c
		}
	}
	return counters, nil
}

// gatewayCounters reads the live counters. An absent table — the dashboard
// has made nothing, or the host was just booted without the unit — is zero
// counters and loaded false, not an error: the page then says the entries
// are not in force.
func gatewayCounters(ctx context.Context) (counters map[string]RuleCounter, loaded bool) {
	out, err := run(ctx, "nft", "-t", "-j", "list", "table", "inet", gatewayTable)
	if err != nil {
		return map[string]RuleCounter{}, false
	}
	c, err := parseGatewayCounters(out)
	if err != nil {
		return map[string]RuleCounter{}, false
	}
	return c, true
}

// GatewayForwarding is the kernel's forwarding switches.
type GatewayForwarding struct {
	IPv4 bool `json:"ipv4"`
	IPv6 bool `json:"ipv6"`
}

// AdmissionState is whether the iptables rule that admits translated
// connections is in place, against whether anything needs it.
type AdmissionState struct {
	Needed    bool                  `json:"needed"`
	Present   bool                  `json:"present"`
	CheckedAt time.Time             `json:"checkedAt"`
	Chains    []AdmissionChainState `json:"chains"`
}

type AdmissionChainState struct {
	Family string `json:"family"`
	Tool   string `json:"tool"`
	Chain  string `json:"chain"`
	Needed bool   `json:"needed"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// ForwardView is a port forward as the Gateway page shows it.
type ForwardView struct {
	ID         int      `json:"id"`
	Name       string   `json:"name"`
	Protocol   string   `json:"protocol"`
	Interface  string   `json:"interface"`
	Ports      string   `json:"ports"`
	Target     string   `json:"target"`
	TargetPort string   `json:"targetPort"`
	Sources    []string `json:"sources"`
	// SourceNat is the choice made (auto, always or never); Masquerade is
	// what it came to for this target.
	SourceNat  string     `json:"sourceNat"`
	Masquerade bool       `json:"masquerade"`
	Enabled    bool       `json:"enabled"`
	ChangedAt  *time.Time `json:"changedAt"`
	Made
	// Packets and Bytes are what the forward has carried since the table
	// was loaded; Total carries across table replacements.
	Packets   uint64          `json:"packets"`
	Bytes     uint64          `json:"bytes"`
	Total     CounterTotal    `json:"total"`
	Readiness *EntryReadiness `json:"readiness,omitempty"`
	// Decision is auto source translation re-checked against the host now.
	Decision *NATDecision `json:"decision,omitempty"`
	// Check is the last target check from this server.
	Check *ForwardCheck `json:"check,omitempty"`
	// External is the latest measurement of the public port by an enrolled
	// external source, attached for administrators.
	External *ExternalEvidence `json:"external,omitempty"`
}

func forwardView(f ForwardSpec, counters map[string]RuleCounter) ForwardView {
	choice, masq, _ := splitNAT(f.SourceNAT)
	v := ForwardView{
		ID: f.ID, Name: f.Name, Protocol: f.Protocol, Interface: f.Interface, Ports: f.Ports,
		Target: f.Target, TargetPort: f.TargetPort, Sources: f.Sources, SourceNat: choice,
		Masquerade: masq, Enabled: f.Enabled, Made: f.Made,
	}
	if v.Sources == nil {
		v.Sources = []string{}
	}
	if !f.ChangedAt.IsZero() {
		t := f.ChangedAt
		v.ChangedAt = &t
	}
	c := counters["forward:"+strconv.Itoa(f.ID)]
	v.Packets, v.Bytes = c.Packets, c.Bytes
	return v
}

// NATView is a NAT entry as the Gateway page shows it.
type NATView struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Interface string `json:"interface"`
	ToAddress string `json:"toAddress"`
	// Mode is masquerade, snat, one-to-one or nptv6; Translated is a
	// mapping's public side.
	Mode         string   `json:"mode"`
	Translated   string   `json:"translated"`
	Destinations []string `json:"destinations"`
	// Owner is what keeps the entry, when that is not a person: removing it
	// is done there.
	Owner   string `json:"owner"`
	Enabled bool   `json:"enabled"`
	Made
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
	// InPackets and InBytes are a mapping's inbound half since load.
	InPackets uint64          `json:"inPackets"`
	InBytes   uint64          `json:"inBytes"`
	Total     CounterTotal    `json:"total"`
	InTotal   CounterTotal    `json:"inTotal"`
	Readiness *EntryReadiness `json:"readiness,omitempty"`
}

func natView(n NATSpec, counters map[string]RuleCounter) NATView {
	c := counters["nat:"+strconv.Itoa(n.ID)]
	in := counters["nat-in:"+strconv.Itoa(n.ID)]
	mode := n.Mode
	if mode == "" {
		mode = "masquerade"
		if n.ToAddress != "" {
			mode = "snat"
		}
	}
	dests := n.Destinations
	if dests == nil {
		dests = []string{}
	}
	return NATView{
		ID: n.ID, Name: n.Name, Source: n.Source, Interface: n.Interface, ToAddress: n.ToAddress,
		Mode: mode, Translated: n.Translated, Destinations: dests,
		Owner: n.Owner, Enabled: n.Enabled, Made: n.Made, Packets: c.Packets, Bytes: c.Bytes,
		InPackets: in.Packets, InBytes: in.Bytes,
	}
}

// GatewayView is everything the Gateway page draws.
type GatewayView struct {
	Capability Capability `json:"capability"`
	// Loaded is whether the gateway table is in the kernel. False with
	// entries in the spec means they are not in force: the host was booted
	// without the unit, or something deleted the table.
	Loaded     bool              `json:"loaded"`
	Forwarding GatewayForwarding `json:"forwarding"`
	Admission  AdmissionState    `json:"admission"`
	Forwards   []ForwardView     `json:"forwards"`
	NAT        []NATView         `json:"nat"`
	// Counters labels the live figures and the totals.
	Counters CounterEvidence `json:"counters"`
	// Flows are each enabled entry's modeled verdicts at the checked layers.
	Flows []EntryFlow `json:"flows"`

	hostAddrs []hostAddr
}

// Gateway reads the port forwards and NAT entries with their live counters,
// totals, readiness and the per-flow verdicts of the host's other chains.
func (s *Service) Gateway(ctx context.Context) (*GatewayView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	live := readLiveCounters(ctx)
	capability := s.GatewayCapability(ctx)
	admission := s.admissionState(ctx, sp)
	addrs := hostAddresses(ctx)
	v := &GatewayView{
		Capability: capability,
		Loaded:     live.Loaded,
		Forwarding: GatewayForwarding{IPv4: gatewayForwardingOn("4"), IPv6: gatewayForwardingOn("6")},
		Admission:  admission,
		Forwards:   make([]ForwardView, 0, len(sp.Forwards)),
		NAT:        make([]NATView, 0, len(sp.NAT)),
		Counters:   s.telemetry.evidence(live),
		Flows:      evaluateGatewayFlows(capability.listing, modelGatewayFlows(sp, addrs)),
		hostAddrs:  addrs,
	}
	if v.Flows == nil {
		v.Flows = []EntryFlow{}
	}
	var keys []string
	for _, f := range sp.Forwards {
		keys = append(keys, "forward:"+strconv.Itoa(f.ID))
	}
	for _, n := range sp.NAT {
		keys = append(keys, "nat:"+strconv.Itoa(n.ID), "nat-in:"+strconv.Itoa(n.ID))
	}
	totals := s.telemetry.totals(ctx, live, keys)
	decisions := autoNATDecisions(ctx, sp.Forwards)
	for _, f := range sp.Forwards {
		fv := forwardView(f, live.Counters)
		fv.Total = totals["forward:"+strconv.Itoa(f.ID)]
		target, _ := ParseAddr(f.Target)
		r := readinessOf(f.Enabled, live, forwardRuleComments(f), familyOf(target), admission)
		fv.Readiness = &r
		if d, ok := decisions[f.ID]; ok {
			fv.Decision = &d
		}
		fv.Check = s.lastForwardCheck(f)
		v.Forwards = append(v.Forwards, fv)
	}
	for _, n := range sp.NAT {
		nv := natView(n, live.Counters)
		nv.Total, nv.InTotal = totals["nat:"+strconv.Itoa(n.ID)], totals["nat-in:"+strconv.Itoa(n.ID)]
		family := "inet"
		if p, err := ParsePrefix(n.Source); err == nil {
			family = familyOf(p.Addr())
		}
		r := readinessOf(n.Enabled, live, natRuleComments(n), family, admission)
		nv.Readiness = &r
		v.NAT = append(v.NAT, nv)
	}
	return v, nil
}

// Each filtering family has independent chains. A reload can remove any
// single owned rule, so aggregate health must never infer the others from it.
func (s *Service) admissionState(ctx context.Context, sp *Spec) AdmissionState {
	a := AdmissionState{Needed: needsAdmission(sp), CheckedAt: time.Now().UTC(), Chains: []AdmissionChainState{}}
	families := admissionFamilies(sp)
	a.Present = a.Needed
	required := false
	for _, tool := range []string{"iptables", "ip6tables"} {
		family := "inet"
		if tool == "ip6tables" {
			family = "inet6"
		}
		if !families[family] {
			continue
		}
		for _, chain := range admissionChains {
			ch := inspectAdmissionChain(ctx, tool, chain, family, true)
			a.Chains = append(a.Chains, ch)
			required = required || ch.Needed
			if ch.Needed && ch.Status != "present" {
				a.Present = false
			}
		}
	}
	a.Needed = a.Needed && required
	if !a.Needed {
		a.Present = false
	}
	return a
}

func admissionFamilies(sp *Spec) map[string]bool {
	out := map[string]bool{}
	for _, f := range sp.Forwards {
		if a, err := ParseAddr(f.Target); f.Enabled && err == nil {
			out[familyOf(a)] = true
		}
	}
	for _, n := range sp.NAT {
		if p, err := ParsePrefix(n.Source); n.Enabled && err == nil {
			out[familyOf(p.Addr())] = true
		}
	}
	return out
}

func inspectAdmissionChain(ctx context.Context, tool, chain, family string, needed bool) AdmissionChainState {
	v := AdmissionChainState{Family: family, Tool: tool, Chain: chain, Needed: needed}
	out, err := run(ctx, tool, "-S", chain)
	if err != nil {
		var missing *UnavailableError
		switch {
		case errors.As(err, &missing), strings.Contains(out+err.Error(), "No chain/target/match"), strings.Contains(out+err.Error(), "does not exist"), strings.Contains(out+err.Error(), "Table does not exist"):
			v.Status, v.Reason = "unsupported", "This tool or filtering chain is absent; no admission rule can be installed here."
			if chain == "DOCKER-USER" {
				v.Needed = false
			} else {
				// An absent tool is optional only after checking that no
				// compatible filtering chain still requires its admission.
				listing, readErr := run(ctx, "nft", "-t", "-j", "list", "ruleset")
				var rules nftListing
				if readErr != nil || json.Unmarshal([]byte(listing), &rules) != nil || rules.Nftables == nil {
					v.Status, v.Reason = "unreadable", "Cannot establish whether the unavailable admission tool leaves a filtering chain unadmitted."
				} else {
					v.Needed = false
					nftFamily := "ip"
					if family == "inet6" {
						nftFamily = "ip6"
					}
					for _, o := range rules.Nftables {
						if o.Chain != nil && o.Chain.Family == nftFamily && o.Chain.Table == "filter" && o.Chain.Name == chain {
							v.Needed = true
							break
						}
					}
				}
			}
		default:
			v.Status, v.Reason = "unreadable", err.Error()
		}
		return v
	}
	chainRules := out
	out, err = run(ctx, tool, append([]string{"-C", chain}, admissionRule()...)...)
	if err == nil {
		for _, line := range strings.Split(chainRules, "\n") {
			if !strings.HasPrefix(line, "-A "+chain+" ") {
				continue
			}
			if strings.ReplaceAll(line, "\"", "") != strings.Join(append([]string{"-A", chain}, admissionRule()...), " ") {
				v.Status, v.Reason = "absent", "The owned admission rule exists but is not first in the chain; earlier rules may block it."
				return v
			}
			break
		}
		v.Status = "present"
		return v
	}
	if strings.Contains(out+err.Error(), "Bad rule") || strings.Contains(out+err.Error(), "matching rule") {
		v.Status, v.Reason = "absent", "The dashboard's translation admission rule is missing from this chain."
	} else {
		v.Status, v.Reason = "unreadable", err.Error()
	}
	return v
}

func admissionFailure(a AdmissionState) string {
	var failed []string
	for _, ch := range a.Chains {
		if ch.Needed && ch.Status != "present" {
			failed = append(failed, ch.Family+"/"+ch.Chain+": "+ch.Status)
		}
	}
	return strings.Join(failed, ", ")
}

// RepairGatewayAdmission changes only rules owned by their fixed mark and
// comment. It never edits a foreign chain's rules, policy or ownership.
func (s *Service) RepairGatewayAdmission(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return err
	}
	defer unlockChange(lock)
	if prior, err := readChange(s.paths.Dir); err == nil {
		if !changeTerminal(prior.Phase) {
			return &ReadOnlyError{Reason: "An earlier network change needs confirmation or recovery before admission can be repaired."}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	if needsAdmission(sp) {
		if _, err := s.requireWritable(ctx, sp); err != nil {
			return err
		}
	}
	if err := s.syncAdmission(ctx, sp, sp); err != nil {
		return err
	}
	if a := s.admissionState(ctx, sp); a.Needed && !a.Present {
		return fmt.Errorf("admission repair is incomplete: %s", admissionFailure(a))
	}
	return nil
}
