package netx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// SQMProfile contains only operator intent. Device names, ownership cookies,
// and restoration identities never come from the request.
type SQMProfile struct {
	Diffserv     string `json:"diffserv"`
	FlowMode     string `json:"flowMode"`
	NAT          bool   `json:"nat"`
	PreserveDSCP bool   `json:"preserveDscp"`
	Overhead     int    `json:"overhead"`
	MPU          int    `json:"mpu"`
	LinkLayer    string `json:"linkLayer"`
	RTTMillis    int    `json:"rttMillis"`
}

type SQMSpec struct {
	SQMProfile
	IFB         string `json:"ifb"`
	Token       string `json:"token"`
	SourceMAC   string `json:"sourceMac"`
	SourceKind  string `json:"sourceKind"`
	MTU         int    `json:"mtu"`
	Hook        string `json:"hook"`
	CreatedHook bool   `json:"createdHook"`
}

const sqmPreference = 49152
const sqmHandle = 51966
const sqmAliasPrefix = "jd-sqm:"

func normSQMProfile(p SQMProfile) (SQMProfile, error) {
	if p.Diffserv == "" {
		p.Diffserv = "besteffort"
	}
	if p.FlowMode == "" {
		p.FlowMode = "dual-dsthost"
	}
	if p.LinkLayer == "" {
		p.LinkLayer = "noatm"
	}
	if p.RTTMillis == 0 {
		p.RTTMillis = 100
	}
	if !sqmOneOf(p.Diffserv, "besteffort", "diffserv3", "diffserv4") {
		return p, errors.New("CAKE classes are besteffort, diffserv3 or diffserv4")
	}
	if !sqmOneOf(p.FlowMode, "dual-dsthost", "triple-isolate", "flows") {
		return p, errors.New("download fairness is dual-dsthost, triple-isolate or flows")
	}
	if !sqmOneOf(p.LinkLayer, "noatm", "atm", "ptm") || p.Overhead < -64 || p.Overhead > 256 || p.MPU < 0 || p.MPU > 256 || p.RTTMillis < 10 || p.RTTMillis > 1000 {
		return p, errors.New("CAKE needs noatm, atm or ptm; overhead -64…256 bytes, minimum packet 0…256 bytes, and RTT 10…1000 ms")
	}
	return p, nil
}

func sqmOneOf(v string, values ...string) bool {
	for _, allowed := range values {
		if v == allowed {
			return true
		}
	}
	return false
}

func validSQMIdentity(s *SQMSpec) error {
	if s == nil {
		return errors.New("missing SQM ownership")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(s.Token) || s.IFB != "jds"+s.Token[:12] {
		return errors.New("invalid server-derived SQM identity")
	}
	if !sqmOneOf(s.Hook, "ingress", "clsact") || s.CreatedHook && s.Hook != "ingress" || s.MTU < 68 || s.MTU > 65535 {
		return errors.New("invalid saved SQM hook or MTU")
	}
	if !sqmOneOf(s.SourceKind, "physical", "dummy", "bridge", "vlan", "bond", "tun", "wireguard", "gre", "gretap", "vxlan", "macvlan") {
		return errors.New("unsupported SQM source identity")
	}
	if s.SourceMAC != "" {
		mac, err := net.ParseMAC(s.SourceMAC)
		if err != nil || len(mac) != 6 || mac.String() != s.SourceMAC {
			return errors.New("invalid SQM source hardware address")
		}
	}
	profile, err := normSQMProfile(s.SQMProfile)
	if err != nil {
		return err
	}
	if profile != s.SQMProfile {
		return errors.New("saved SQM profile is not canonical")
	}
	return nil
}

type sqmLink struct {
	Name   string   `json:"ifname"`
	MAC    string   `json:"address"`
	Alias  string   `json:"ifalias"`
	MTU    int      `json:"mtu"`
	Master string   `json:"master"`
	Flags  []string `json:"flags"`
	Info   struct {
		Kind string `json:"info_kind"`
	} `json:"linkinfo"`
}

// The inventory distinguishes absence from a failed per-device read. A failed
// read can never authorize recreating or deleting a link.
func readSQMLink(ctx context.Context, name string) (*sqmLink, error) {
	out, err := executeRecovery(ctx, nil, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return nil, fmt.Errorf("reading SQM link ownership: %w", err)
	}
	var links []sqmLink
	if !strings.HasPrefix(strings.TrimSpace(out), "[") || json.Unmarshal([]byte(out), &links) != nil {
		return nil, errors.New("unreadable SQM link inventory")
	}
	for i := range links {
		if links[i].Name == name {
			return &links[i], nil
		}
	}
	return nil, nil
}

func sqmLinkKind(l *sqmLink) string {
	if l.Info.Kind == "" {
		return "physical"
	}
	return l.Info.Kind
}

func sqmIFBMAC(token string) string {
	// A separately chosen part of the nonce supplies an atomic creation
	// identity even on kernels that ignore IFLA_IFALIAS on RTM_NEWLINK.
	return "02:" + token[22:24] + ":" + token[24:26] + ":" + token[26:28] + ":" + token[28:30] + ":" + token[30:32]
}

func sqmSource(ctx context.Context, sh ShapeSpec) error {
	l, err := readSQMLink(ctx, sh.Device)
	if err != nil {
		return err
	}
	if l == nil || sqmLinkKind(l) != sh.SQM.SourceKind || l.MAC != sh.SQM.SourceMAC || l.MTU != sh.SQM.MTU {
		return shapingDrift("%s source identity or MTU changed; review it through its native owner before retrying SQM", sh.Device)
	}
	return nil
}

func sqmCakeArgs(sh ShapeSpec) []string {
	p := sh.SQM
	nat, wash := "nonat", "wash"
	if p.NAT {
		nat = "nat"
	}
	if p.PreserveDSCP {
		wash = "nowash"
	}
	return []string{"qdisc", "replace", "dev", p.IFB, "root", "handle", "ca11:", "cake", "bandwidth", strconv.Itoa(sh.IngressKbit) + "kbit", p.Diffserv, p.FlowMode, nat, wash, "split-gso", "no-ack-filter", "ingress", p.LinkLayer, "overhead", strconv.Itoa(p.Overhead), "mpu", strconv.Itoa(p.MPU), "rtt", strconv.Itoa(p.RTTMillis) + "ms"}
}

func sqmRedirectArgs(sh ShapeSpec, verb string) []string {
	args := []string{"filter", verb, "dev", sh.Device, "ingress", "protocol", "all", "pref", strconv.Itoa(sqmPreference), "handle", "0xcafe", "matchall"}
	if verb != "del" {
		args = append(args, "skip_hw", "action", "mirred", "egress", "redirect", "dev", sh.SQM.IFB, "cookie", sh.SQM.Token)
	}
	return args
}

type sqmFilter struct {
	Protocol string `json:"protocol"`
	Pref     int    `json:"pref"`
	Kind     string `json:"kind"`
	Chain    int    `json:"chain"`
	Options  struct {
		Handle  int  `json:"handle"`
		SkipHW  bool `json:"skip_hw"`
		Actions []struct {
			Kind      string `json:"kind"`
			Action    string `json:"mirred_action"`
			Direction string `json:"direction"`
			Device    string `json:"to_dev"`
			Cookie    string `json:"cookie"`
			Control   struct {
				Type string `json:"type"`
			} `json:"control_action"`
		} `json:"actions"`
	} `json:"options"`
}

func sqmReadFilters(ctx context.Context, device string, hook string) ([]sqmFilter, error) {
	out, err := executeRecovery(ctx, nil, "tc", "-j", "filter", "show", "dev", device, hook)
	if err != nil {
		return nil, fmt.Errorf("reading %s %s filters: %w", device, hook, err)
	}
	var filters []sqmFilter
	if !strings.HasPrefix(strings.TrimSpace(out), "[") || json.Unmarshal([]byte(out), &filters) != nil {
		return nil, errors.New("unreadable SQM filter ownership")
	}
	return filters, nil
}

func checkSQMFilters(filters []sqmFilter, sh ShapeSpec, allowMissing bool) (bool, error) {
	found := 0
	for _, f := range filters {
		if f.Pref != sqmPreference || f.Protocol != "all" || f.Kind != "matchall" || f.Chain != 0 {
			return false, shapingDrift("%s has foreign ingress filters; SQM will not overwrite them", sh.Device)
		}
		if len(f.Options.Actions) == 0 {
			continue
		}
		if f.Options.Handle != sqmHandle || !f.Options.SkipHW || len(f.Options.Actions) != 1 {
			return false, shapingDrift("%s SQM redirect ownership differs", sh.Device)
		}
		a := f.Options.Actions[0]
		if a.Kind != "mirred" || a.Action != "redirect" || a.Direction != "egress" || a.Device != sh.SQM.IFB || a.Cookie != sh.SQM.Token || a.Control.Type != "stolen" {
			return false, shapingDrift("%s SQM redirect identity differs", sh.Device)
		}
		found++
	}
	if found > 1 || found == 0 && !allowMissing {
		return false, shapingDrift("%s SQM redirect is missing or duplicated", sh.Device)
	}
	return found == 1, nil
}

func sqmQdiscs(ctx context.Context, device string) ([]tcQdisc, error) {
	out, err := executeRecovery(ctx, nil, "tc", "-j", "qdisc", "show", "dev", device)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		return nil, errors.New("unreadable SQM queue inventory")
	}
	return parseQdiscs(out)
}

func checkSQMCake(qs []tcQdisc, sh ShapeSpec, allowDefault bool) error {
	if allowDefault && (len(qs) == 0 || len(qs) == 1 && qs[0].Root && sqmOneOf(qs[0].Kind, "noqueue", "noop")) {
		return nil
	}
	if len(qs) != 1 || !qs[0].Root || qs[0].Kind != "cake" || qs[0].Handle != "ca11:" {
		return shapingDrift("%s has a foreign or missing IFB queue", sh.Device)
	}
	o := qs[0].Options
	stringsExpected := map[string]string{"diffserv": sh.SQM.Diffserv, "flowmode": sh.SQM.FlowMode, "ack-filter": "disabled", "atm": sh.SQM.LinkLayer}
	for key, want := range stringsExpected {
		var value string
		if json.Unmarshal(o[key], &value) != nil || value != want {
			return shapingDrift("%s CAKE %s differs from the saved profile", sh.SQM.IFB, key)
		}
	}
	intsExpected := map[string]int64{"bandwidth": int64(shapeBytes(sh.IngressKbit)), "overhead": int64(sh.SQM.Overhead), "mpu": int64(sh.SQM.MPU), "rtt": int64(sh.SQM.RTTMillis) * 1000}
	for key, want := range intsExpected {
		var value int64
		// iproute2 deliberately omits MPU when it is zero, even when
		// the kernel returned the zero-valued attribute.
		if key == "mpu" && want == 0 && o[key] == nil {
			continue
		}
		if json.Unmarshal(o[key], &value) != nil || value != want {
			return shapingDrift("%s CAKE %s differs from the saved profile", sh.SQM.IFB, key)
		}
	}
	for key, want := range map[string]bool{"nat": sh.SQM.NAT, "wash": !sh.SQM.PreserveDSCP, "ingress": true, "split_gso": true, "raw": false} {
		var value bool
		if json.Unmarshal(o[key], &value) != nil || value != want {
			return shapingDrift("%s CAKE %s differs from the saved profile", sh.SQM.IFB, key)
		}
	}
	var mark string
	if json.Unmarshal(o["fwmark"], &mark) != nil || mark != "0" {
		return shapingDrift("%s has a foreign CAKE fwmark override", sh.SQM.IFB)
	}
	return nil
}

func sqmIFBOwned(ctx context.Context, sh ShapeSpec, allowAbsent, allowDefault bool) (bool, error) {
	l, err := readSQMLink(ctx, sh.SQM.IFB)
	if err != nil {
		return false, err
	}
	if l == nil {
		if allowAbsent {
			return false, nil
		}
		return false, shapingDrift("%s's owned IFB is missing", sh.Device)
	}
	if l.Info.Kind != "ifb" || l.MAC != sqmIFBMAC(sh.SQM.Token) || (l.Alias != sqmAliasPrefix+sh.SQM.Token && !(allowDefault && l.Alias == "")) || l.MTU != sh.SQM.MTU || l.Master != "" {
		return false, shapingDrift("%s IFB identity changed; review through its native owner before retrying", sh.Device)
	}
	qs, err := sqmQdiscs(ctx, sh.SQM.IFB)
	if err != nil {
		return false, err
	}
	if err := checkSQMCake(qs, sh, allowDefault); err != nil {
		return false, err
	}
	for _, kind := range []string{"filter", "class"} {
		out, err := executeRecovery(ctx, nil, "tc", "-j", kind, "show", "dev", sh.SQM.IFB)
		if err != nil {
			return false, err
		}
		var entries []json.RawMessage
		if !strings.HasPrefix(strings.TrimSpace(out), "[") || json.Unmarshal([]byte(out), &entries) != nil {
			return false, shapingDrift("%s IFB has foreign %ss", sh.Device, kind)
		}
		if kind == "filter" && len(entries) != 0 {
			return false, shapingDrift("%s IFB has foreign filters", sh.Device)
		}
		// CAKE exposes virtual flow/tin classes after the link comes up.
		// They cannot be configured as an independent hierarchy.
		if kind == "class" {
			for _, entry := range entries {
				var class tcClass
				if json.Unmarshal(entry, &class) != nil || class.Kind != "cake" || class.Parent != "ca11:" || !regexp.MustCompile(`^ca11:[0-9a-f]+$`).MatchString(class.Handle) {
					return false, shapingDrift("%s IFB has foreign classes", sh.Device)
				}
			}
		}
	}
	return true, nil
}

func verifySQM(ctx context.Context, sh ShapeSpec) error {
	if err := validSQMIdentity(sh.SQM); err != nil {
		return err
	}
	if err := sqmSource(ctx, sh); err != nil {
		return err
	}
	if _, err := sqmIFBOwned(ctx, sh, false, false); err != nil {
		return err
	}
	l, err := readSQMLink(ctx, sh.SQM.IFB)
	if err != nil {
		return err
	}
	if !sqmOneOf("UP", l.Flags...) {
		return shapingDrift("%s IFB is down", sh.Device)
	}
	qs, err := sqmQdiscs(ctx, sh.Device)
	if err != nil {
		return err
	}
	if ingressKind(qs) != sh.SQM.Hook {
		return shapingDrift("%s ingress hook ownership changed", sh.Device)
	}
	filters, err := sqmReadFilters(ctx, sh.Device, "ingress")
	if err != nil {
		return err
	}
	_, err = checkSQMFilters(filters, sh, false)
	return err
}

func prepareSQM(ctx context.Context, sh *ShapeSpec, prev *ShapeSpec) error {
	if prev != nil && prev.SQM != nil {
		if err := verifySQM(ctx, *prev); err != nil {
			return err
		}
		if sh.SQM != nil {
			profile := sh.SQM.SQMProfile
			copy := *prev.SQM
			copy.SQMProfile = profile
			sh.SQM = &copy
		}
		return nil
	}
	if sh.SQM == nil {
		return nil
	}
	if prev != nil && prev.IngressKbit > 0 {
		if err := verifyShaping(ctx, *prev); err != nil {
			return fmt.Errorf("review the saved policer through its native owner before enabling SQM: %w", err)
		}
	}
	l, err := readSQMLink(ctx, sh.Device)
	if err != nil {
		return err
	}
	if l == nil {
		return errors.New("SQM source no longer exists")
	}
	qs, err := sqmQdiscs(ctx, sh.Device)
	if err != nil {
		return err
	}
	hook := ingressKind(qs)
	created := hook == "" || prev != nil && prev.IngressKbit > 0
	if hook != "" && (prev == nil || prev.IngressKbit == 0) {
		filters, err := sqmReadFilters(ctx, sh.Device, "ingress")
		if err != nil {
			return err
		}
		if len(filters) != 0 {
			return fmt.Errorf("%s has foreign ingress filters; review them through their native owner before enabling SQM", sh.Device)
		}
	}
	if created {
		hook = "ingress"
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	text := hex.EncodeToString(token[:])
	sh.SQM = &SQMSpec{SQMProfile: sh.SQM.SQMProfile, IFB: "jds" + text[:12], Token: text, SourceMAC: l.MAC, SourceKind: sqmLinkKind(l), MTU: l.MTU, Hook: hook, CreatedHook: created}
	if err := validSQMIdentity(sh.SQM); err != nil {
		return err
	}
	existing, err := readSQMLink(ctx, sh.SQM.IFB)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("SQM IFB name is already present; no existing interface was adopted")
	}
	return nil
}

func sqmPrevious(sh *ShapeSpec) *ShapeSpec {
	if sh != nil && sh.SQM != nil {
		return sh
	}
	return nil
}

func applySQM(ctx context.Context, sh ShapeSpec, previous *ShapeSpec) error {
	existing := previous != nil
	if err := sqmSource(ctx, sh); err != nil {
		return err
	}
	qs, err := sqmQdiscs(ctx, sh.Device)
	if err != nil {
		return err
	}
	hook := ingressKind(qs)
	if hook != sh.SQM.Hook && !(hook == "" && sh.SQM.CreatedHook) {
		return shapingDrift("%s ingress hook changed before SQM apply", sh.Device)
	}
	if hook != "" {
		filters, err := sqmReadFilters(ctx, sh.Device, "ingress")
		if err != nil {
			return err
		}
		if existing {
			if _, err := checkSQMFilters(filters, sh, false); err != nil {
				return err
			}
		} else if len(filters) != 0 {
			return shapingDrift("%s acquired foreign ingress filters before SQM apply", sh.Device)
		}
	}
	if !existing {
		if link, err := readSQMLink(ctx, sh.SQM.IFB); err != nil {
			return err
		} else if link != nil {
			return errors.New("SQM IFB collision; no existing interface was adopted")
		}
		if _, err := executeRecovery(ctx, nil, "ip", "link", "add", "name", sh.SQM.IFB, "address", sqmIFBMAC(sh.SQM.Token), "mtu", strconv.Itoa(sh.SQM.MTU), "type", "ifb"); err != nil {
			return err
		}
		if _, err := sqmIFBOwned(ctx, sh, false, true); err != nil {
			return err
		}
		if _, err := executeRecovery(ctx, nil, "ip", "link", "set", "dev", sh.SQM.IFB, "alias", sqmAliasPrefix+sh.SQM.Token); err != nil {
			return err
		}
		if _, err := sqmIFBOwned(ctx, sh, false, true); err != nil {
			return err
		}
		link, err := readSQMLink(ctx, sh.SQM.IFB)
		if err != nil {
			return err
		}
		if link == nil || link.Alias != sqmAliasPrefix+sh.SQM.Token {
			return shapingDrift("%s IFB ownership changed before CAKE setup", sh.Device)
		}
	} else {
		if _, err := sqmIFBOwned(ctx, *previous, false, false); err != nil {
			return err
		}
		// CAKE cannot replace itself on all kernels. Hold the owned IFB down
		// while recreating its queue rather than forwarding through a default
		// queue between the two commands.
		if _, err := executeRecovery(ctx, nil, "ip", "link", "set", "dev", sh.SQM.IFB, "down"); err != nil {
			return err
		}
		if _, err := executeRecovery(ctx, nil, "tc", "qdisc", "del", "dev", sh.SQM.IFB, "root"); err != nil {
			return err
		}
	}
	if _, err := executeRecovery(ctx, nil, "tc", sqmCakeArgs(sh)...); err != nil {
		return err
	}
	if _, err := executeRecovery(ctx, nil, "ip", "link", "set", "dev", sh.SQM.IFB, "up"); err != nil {
		return err
	}
	qs, err = sqmQdiscs(ctx, sh.Device)
	if err != nil {
		return err
	}
	if ingressKind(qs) == "" && sh.SQM.CreatedHook {
		if _, err := executeRecovery(ctx, nil, "tc", "qdisc", "add", "dev", sh.Device, "handle", "ffff:", "ingress"); err != nil {
			return err
		}
	} else if ingressKind(qs) != sh.SQM.Hook {
		return shapingDrift("%s ingress hook changed before SQM apply", sh.Device)
	}
	if !existing {
		if _, err := executeRecovery(ctx, nil, "tc", sqmRedirectArgs(sh, "add")...); err != nil {
			return err
		}
	}
	return verifySQM(ctx, sh)
}

// removeSQM removes the redirect before its destination, and only removes a
// hook we created when it is still empty. Existing clsact and egress survive.
func removeSQM(ctx context.Context, sh ShapeSpec, partial bool, alternatives ...ShapeSpec) error {
	if err := sqmSource(ctx, sh); err != nil {
		return err
	}
	qs, err := sqmQdiscs(ctx, sh.Device)
	if err != nil {
		return err
	}
	hook := ingressKind(qs)
	if hook != "" && hook != sh.SQM.Hook {
		return shapingDrift("%s ingress hook changed during SQM recovery", sh.Device)
	}
	if hook != "" {
		filters, err := sqmReadFilters(ctx, sh.Device, "ingress")
		if err != nil {
			return err
		}
		found, err := checkSQMFilters(filters, sh, partial)
		if err != nil {
			return err
		}
		if found {
			if _, err := executeRecovery(ctx, nil, "tc", sqmRedirectArgs(sh, "del")...); err != nil {
				return err
			}
		}
	}
	owned, err := sqmIFBOwned(ctx, sh, partial, partial)
	if err != nil && partial {
		for _, previous := range alternatives {
			if previous.SQM != nil && previous.SQM.Token == sh.SQM.Token {
				owned, err = sqmIFBOwned(ctx, previous, true, true)
				if err == nil {
					break
				}
			}
		}
	}
	if err != nil {
		return err
	}
	if owned {
		if _, err := executeRecovery(ctx, nil, "ip", "link", "del", "dev", sh.SQM.IFB); err != nil {
			return err
		}
	}
	if hook != "" && sh.SQM.CreatedHook {
		filters, err := sqmReadFilters(ctx, sh.Device, "ingress")
		if err != nil {
			return err
		}
		if len(filters) != 0 {
			return shapingDrift("%s ingress acquired foreign filters during recovery", sh.Device)
		}
		if _, err := executeRecovery(ctx, nil, "tc", "qdisc", "del", "dev", sh.Device, "ingress"); err != nil {
			return err
		}
	}
	return nil
}
