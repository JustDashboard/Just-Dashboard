package netx

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A DNSSEC setting and resolved's counters say validation happens; they do not
// show the chain of trust a particular name rests on. The chain diagnostic
// walks from the name to the root through the identified native resolver,
// asking each candidate zone for its DNSKEY set and each zone apex for its DS
// set with fresh-network flags, and records resolved's own authentication of
// each set. It then recomputes, itself, every DS digest from the child's
// DNSKEY records and compares the root's keys with the IANA root anchors, so
// each delegation link is checked independently of resolved. resolved refuses
// explicit RRSIG questions, so signatures themselves remain the native
// validator's word, and the report says so.
//
// Ancestors are asked only while they stay in the name's own native policy
// scope: a name under a private routing domain never has its parents asked of
// a different (often public) scope.

// rootAnchors are the IANA root KSK digests (KSK-2017 and KSK-2024), the same
// pair systemd-resolved 257 carries built in.
var rootAnchors = []DNSSECDelegation{
	{KeyTag: 20326, Algorithm: 8, DigestType: 2, Digest: "e06d44b80b8f1d39a95c0b0d7c65d08458e880409bbc683457104237c7f8ec8d"},
	{KeyTag: 38696, Algorithm: 8, DigestType: 2, Digest: "683d2d0acb8c9b712a1948b27f741219298d0a450d612c483af444a4c0fb2b16"},
}

const (
	dnsTypeDS     = 43
	dnsTypeDNSKEY = 48
	// maxDNSSECLevels and maxDNSSECQuestions bound one diagnostic.
	maxDNSSECLevels    = 12
	maxDNSSECQuestions = 24
)

// DNSSECKey is one DNSKEY record.
type DNSSECKey struct {
	KeyTag    uint16 `json:"keyTag"`
	Algorithm uint8  `json:"algorithm"`
	Flags     uint16 `json:"flags"`
	// SEP marks a key-signing key, the one a parent's DS normally points at.
	SEP bool `json:"sep"`
}

// DNSSECDelegation is one DS record, or a root anchor, and whether a key of the
// zone hashes to it.
type DNSSECDelegation struct {
	KeyTag     uint16  `json:"keyTag"`
	Algorithm  uint8   `json:"algorithm"`
	DigestType uint8   `json:"digestType"`
	Digest     string  `json:"digest"`
	Matched    bool    `json:"matched"`
	MatchedKey *uint16 `json:"matchedKey,omitempty"`
	// Supported is false for a digest type this check cannot compute.
	Supported bool `json:"supported"`
}

// DNSSECSet is what the native resolver answered for one record set.
type DNSSECSet struct {
	// State is authenticated, unauthenticated, absent (no record of the type),
	// not_found (the name does not exist), validation_failed, error or
	// not_asked.
	State   string `json:"state"`
	Records int    `json:"records"`
	Detail  string `json:"detail,omitempty"`
}

// DNSSECLevel is one candidate zone on the way from the name to the root.
type DNSSECLevel struct {
	Zone string `json:"zone"`
	// Role is apex (the name has DNSKEY records), not_apex, not_queried
	// (outside the name's policy scope or past the bound) or unavailable.
	Role   string             `json:"role"`
	Scope  string             `json:"scope,omitempty"`
	DNSKEY DNSSECSet          `json:"dnskey"`
	DS     DNSSECSet          `json:"ds"`
	Keys   []DNSSECKey        `json:"keys"`
	Links  []DNSSECDelegation `json:"links"`
	// Link is digest_match (a DS digest recomputed here matches a key),
	// digest_mismatch (DS records exist and none matches), no_ds, root_anchor,
	// root_anchor_mismatch or not_checked.
	Link   string `json:"link"`
	Detail string `json:"detail"`
}

// DNSSECChain is one diagnostic.
type DNSSECChain struct {
	Name         string        `json:"name"`
	StartedAt    time.Time     `json:"startedAt"`
	EndedAt      time.Time     `json:"endedAt"`
	Owner        string        `json:"owner"`
	OwnerVersion string        `json:"ownerVersion,omitempty"`
	PolicyMatch  string        `json:"policyMatch,omitempty"`
	PolicyStable bool          `json:"policyStable"`
	Levels       []DNSSECLevel `json:"levels"`
	// Verdict is secure (every link from the name's zone to the root anchor
	// was recomputed and every set authenticated), anchored (secure up to a
	// zone whose parent could not be asked or that resolved trusts locally),
	// insecure (a delegation has no DS), broken (a DS digest or the root anchor
	// does not match, or resolved rejected a set) or unknown.
	Verdict     string   `json:"verdict"`
	Summary     string   `json:"summary"`
	Questions   int      `json:"questions"`
	Error       string   `json:"error,omitempty"`
	Limitations []string `json:"limitations"`
}

// InvestigateDNSSEC walks the chain of trust for name. execute is the trusted
// native adapter, never request input.
func (s *Service) InvestigateDNSSEC(ctx context.Context, name string, execute TrafficExecutor) (*DNSSECChain, error) {
	clean, err := cleanLookupName(name, "A")
	if err != nil {
		return nil, err
	}
	name = strings.ToLower(clean)
	if execute == nil {
		execute = dnsNativeExecutor
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := &DNSSECChain{Name: name, StartedAt: time.Now().UTC(), Owner: "unknown", Levels: []DNSSECLevel{}, Verdict: "unknown", Limitations: []string{
		"RRSIG signatures are verified only by systemd-resolved: it refuses explicit RRSIG questions, so this check recomputes DS digests and compares root keys with the IANA anchors, but does not verify signatures itself.",
		"An absent DS set is reported as resolved returned it; its API does not say whether the absence itself was proven by NSEC/NSEC3.",
		"Ancestors outside the name's own policy scope are not asked, so a private zone's chain may end at its local trust anchor or at the edge of its scope.",
		"Answers are fresh network data from the identified resolver; local trust anchors, negative trust anchors and the resolver's own configuration files are not read.",
	}}
	defer func() { c.EndedAt = time.Now().UTC() }()
	owner, version, failure := dnsNativeIdentify(ctx, execute)
	c.OwnerVersion = version
	if failure != "" {
		c.Error = failure
		return c, nil
	}
	c.Owner = "systemd-resolved"
	all, before, err := dnsNativeSnapshot(ctx, execute, owner)
	if err != nil {
		c.Error = err.Error() + "; no question was sent."
		return c, nil
	}
	policy, match := dnsBestPolicy(name, all)
	c.PolicyMatch = match
	if err := dnsCheckPolicy(policy); err != nil {
		c.Error = err.Error() + "; no question or default-scope fallback was used."
		return c, nil
	}
	scopeKey := dnsScopeKey(policy)
	labels := strings.Split(name, ".")
	zones := []string{}
	for i := range labels {
		zones = append(zones, strings.Join(labels[i:], "."))
	}
	zones = append(zones, ".")
	outside := false
	for i, zone := range zones {
		level := DNSSECLevel{Zone: zone, Keys: []DNSSECKey{}, Links: []DNSSECDelegation{}, Link: "not_checked", DNSKEY: DNSSECSet{State: "not_asked"}, DS: DNSSECSet{State: "not_asked"}}
		question := zone
		if zone == "." {
			question = ""
		}
		zonePolicy, _ := dnsBestPolicy(question, all)
		level.Scope = dnsScopeLabel(zonePolicy)
		switch {
		case outside || dnsScopeKey(zonePolicy) != scopeKey:
			outside = true
			level.Role = "not_queried"
			level.Detail = "Its native policy scope differs from the name's, so it was not asked: the name's parents stay inside the name's own scope."
			c.Levels = append(c.Levels, level)
			continue
		case i >= maxDNSSECLevels || c.Questions+2 > maxDNSSECQuestions:
			level.Role = "not_queried"
			level.Detail = "The diagnostic's question bound was reached."
			c.Levels = append(c.Levels, level)
			continue
		}
		keys, set, err := dnsNativeSet(ctx, execute, owner, zone, dnsTypeDNSKEY)
		c.Questions++
		if err != nil {
			c.Error = err.Error()
			break
		}
		level.DNSKEY = set
		if set.State == "absent" || set.State == "not_found" {
			level.Role = "not_apex"
			level.Detail = "No DNSKEY records: not a zone apex."
			c.Levels = append(c.Levels, level)
			continue
		}
		if set.State == "validation_failed" || set.State == "error" {
			level.Role = "unavailable"
			level.Detail = set.Detail
			c.Levels = append(c.Levels, level)
			continue
		}
		level.Role = "apex"
		for _, rdata := range keys {
			if key, ok := parseDNSKEY(rdata); ok {
				level.Keys = append(level.Keys, key)
			}
		}
		if zone == "." {
			level.Links, level.Link = matchDelegations(zone, keys, rootAnchors)
			if level.Link == "digest_match" {
				level.Link = "root_anchor"
				level.Detail = "A root key-signing key hashes to an IANA root anchor."
			} else {
				level.Link = "root_anchor_mismatch"
				level.Detail = "No root key hashes to an IANA root anchor."
			}
			c.Levels = append(c.Levels, level)
			continue
		}
		dsRecords, ds, err := dnsNativeSet(ctx, execute, owner, zone, dnsTypeDS)
		c.Questions++
		if err != nil {
			c.Error = err.Error()
			break
		}
		level.DS = ds
		switch ds.State {
		case "authenticated", "unauthenticated":
			parsed := []DNSSECDelegation{}
			for _, rdata := range dsRecords {
				if d, ok := parseDS(rdata); ok {
					parsed = append(parsed, d)
				}
			}
			level.Links, level.Link = matchDelegations(zone, keys, parsed)
			if level.Link == "digest_match" {
				level.Detail = "A DS digest recomputed here from this zone's DNSKEY matches the parent's DS record."
			} else {
				level.Detail = "The parent's DS records match none of this zone's keys: the delegation is broken."
			}
		case "absent", "not_found":
			level.Link = "no_ds"
			level.Detail = "The parent returned no DS record for this zone: an insecure delegation, or a zone resolved trusts through a local anchor."
		default:
			level.Detail = "The DS set could not be read: " + ds.Detail
		}
		c.Levels = append(c.Levels, level)
	}
	if _, digest, err := dnsNativeSnapshot(ctx, execute, owner); err == nil {
		c.PolicyStable = digest == before
	}
	if !c.PolicyStable {
		c.Limitations = append(c.Limitations, "The native policy changed or could not be reread during the walk, so the levels may describe different policy.")
	}
	chainVerdict(c)
	return c, nil
}

// dnsScopeKey identifies a set of native policy scopes by their indexes.
func dnsScopeKey(policy []DNSPolicyScope) string {
	parts := []string{}
	for _, p := range policy {
		parts = append(parts, strconv.Itoa(p.Index))
	}
	return strings.Join(parts, ",")
}

func dnsScopeLabel(policy []DNSPolicyScope) string {
	parts := []string{}
	for _, p := range policy {
		parts = append(parts, p.Interface)
	}
	if len(parts) == 0 {
		return "no scope"
	}
	return strings.Join(parts, ", ")
}

// dnsNativeSet asks the native resolver one record set with fresh-network
// flags and returns the records' RDATA and how the set was answered. A
// returned error is a contract failure that stops the walk.
func dnsNativeSet(ctx context.Context, execute TrafficExecutor, owner, zone string, qtype uint16) ([][]byte, DNSSECSet, error) {
	if current, err := dnsBusOwner(ctx, execute); err != nil || current != owner {
		return nil, DNSSECSet{}, fmt.Errorf("native resolver identity changed during the walk; no further question was sent")
	}
	question := zone + "."
	if zone == "." {
		question = "."
	}
	out, err := dnsBusCall(ctx, execute, owner, dnsNativePath, dnsNativeManager, "ResolveRecord", "isqqt", "0", question, "1", strconv.Itoa(int(qtype)), strconv.FormatUint(dnsFreshFlags, 10))
	if err != nil {
		line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
		switch {
		case dnsNativeError(out, "DNSSEC validation failed:"):
			return nil, DNSSECSet{State: "validation_failed", Detail: dnsBoundedError(line)}, nil
		case strings.Contains(line, "does not have any RR of the requested type"):
			return nil, DNSSECSet{State: "absent"}, nil
		case strings.HasPrefix(line, "Call failed: ") && strings.Contains(line, "not found"):
			return nil, DNSSECSet{State: "not_found"}, nil
		}
		return nil, DNSSECSet{State: "error", Detail: dnsBoundedError(line)}, nil
	}
	var reply dnsBusReply
	var rows [][]json.RawMessage
	var flags uint64
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Type != "a(iqqay)t" || len(reply.Data) != 2 || json.Unmarshal(reply.Data[0], &rows) != nil || len(rows) == 0 || len(rows) > 64 || json.Unmarshal(reply.Data[1], &flags) != nil {
		return nil, DNSSECSet{}, fmt.Errorf("native record envelope for %s is malformed", zone)
	}
	want := strings.TrimSuffix(zone, ".")
	records := [][]byte{}
	for _, row := range rows {
		var index int
		var class, kind uint16
		var wire []byte
		if len(row) != 4 || json.Unmarshal(row[0], &index) != nil || index < 0 || json.Unmarshal(row[1], &class) != nil || class != 1 || json.Unmarshal(row[2], &kind) != nil || json.Unmarshal(row[3], &wire) != nil || len(wire) > 65535 {
			return nil, DNSSECSet{}, fmt.Errorf("native record tuple for %s is malformed", zone)
		}
		ownerName, offset, err := dnsExpandedOwner(wire)
		if err != nil || !strings.EqualFold(ownerName, want) || kind != qtype || len(wire) < offset+10 || binary.BigEndian.Uint16(wire[offset:]) != kind ||
			len(wire) != offset+10+int(binary.BigEndian.Uint16(wire[offset+8:])) {
			return nil, DNSSECSet{}, fmt.Errorf("native record for %s does not match the exact owner and type", zone)
		}
		records = append(records, wire[offset+10:])
	}
	set := DNSSECSet{State: "unauthenticated", Records: len(records)}
	localOrigin := dnsFlagSynthetic | dnsFlagCache | dnsFlagZone | dnsFlagTrustAnchor
	fresh := flags&dnsFlagDNS != 0 && flags&dnsFlagNetwork != 0 && flags&(localOrigin|dnsFlagOtherProtocol) == 0
	switch {
	case !fresh:
		set.Detail = "The reply did not establish fresh network origin, so its authentication flag is not evidence."
	case flags&dnsFlagAuthenticated != 0:
		set.State = "authenticated"
	default:
		set.Detail = "resolved did not authenticate this set: validation is off or downgraded for its scope, or the zone is unsigned above."
	}
	return records, set, nil
}

func parseDNSKEY(rdata []byte) (DNSSECKey, bool) {
	if len(rdata) < 4 || rdata[2] != 3 {
		return DNSSECKey{}, false
	}
	flags := binary.BigEndian.Uint16(rdata)
	return DNSSECKey{KeyTag: dnsKeyTag(rdata), Algorithm: rdata[3], Flags: flags, SEP: flags&1 != 0}, true
}

func parseDS(rdata []byte) (DNSSECDelegation, bool) {
	if len(rdata) < 5 {
		return DNSSECDelegation{}, false
	}
	return DNSSECDelegation{KeyTag: binary.BigEndian.Uint16(rdata), Algorithm: rdata[2], DigestType: rdata[3], Digest: hex.EncodeToString(rdata[4:])}, true
}

// dnsKeyTag is RFC 4034 Appendix B's checksum over a DNSKEY's RDATA.
func dnsKeyTag(rdata []byte) uint16 {
	var sum uint32
	for i, b := range rdata {
		if i&1 == 0 {
			sum += uint32(b) << 8
		} else {
			sum += uint32(b)
		}
	}
	sum += sum >> 16 & 0xffff
	return uint16(sum)
}

// dnsCanonicalName is the owner name in canonical wire form (RFC 4034 6.2).
func dnsCanonicalName(zone string) []byte {
	var out []byte
	if zone != "." {
		for _, label := range strings.Split(strings.ToLower(strings.TrimSuffix(zone, ".")), ".") {
			out = append(out, byte(len(label)))
			out = append(out, label...)
		}
	}
	return append(out, 0)
}

// dnsDSDigest recomputes a DS digest (RFC 4034 5.1.4, RFC 4509, RFC 6605).
func dnsDSDigest(zone string, rdata []byte, digestType uint8) ([]byte, bool) {
	input := append(dnsCanonicalName(zone), rdata...)
	switch digestType {
	case 1:
		sum := sha1.Sum(input)
		return sum[:], true
	case 2:
		sum := sha256.Sum256(input)
		return sum[:], true
	case 4:
		sum := sha512.Sum384(input)
		return sum[:], true
	}
	return nil, false
}

// matchDelegations recomputes each delegation's digest from the zone's keys.
func matchDelegations(zone string, keys [][]byte, delegations []DNSSECDelegation) ([]DNSSECDelegation, string) {
	out := make([]DNSSECDelegation, len(delegations))
	matched := false
	for i, d := range delegations {
		d.Supported = d.DigestType == 1 || d.DigestType == 2 || d.DigestType == 4
		want, err := hex.DecodeString(d.Digest)
		for _, rdata := range keys {
			key, ok := parseDNSKEY(rdata)
			if !ok || err != nil || key.KeyTag != d.KeyTag || key.Algorithm != d.Algorithm {
				continue
			}
			if got, ok := dnsDSDigest(zone, rdata, d.DigestType); ok && bytes.Equal(got, want) {
				tag := key.KeyTag
				d.Matched, d.MatchedKey = true, &tag
				matched = true
			}
		}
		out[i] = d
	}
	if matched {
		return out, "digest_match"
	}
	return out, "digest_mismatch"
}

// chainVerdict reads the levels from the name's zone upwards.
func chainVerdict(c *DNSSECChain) {
	if c.Error != "" {
		c.Verdict, c.Summary = "unknown", c.Error
		return
	}
	apexes := []DNSSECLevel{}
	for _, l := range c.Levels {
		if l.Role == "apex" || l.Role == "unavailable" {
			apexes = append(apexes, l)
		}
	}
	if len(apexes) == 0 {
		c.Verdict, c.Summary = "unknown", "No zone apex answered inside the name's policy scope."
		return
	}
	authenticated := true
	for _, l := range apexes {
		if l.Role == "unavailable" {
			c.Verdict, c.Summary = "broken", l.Zone+": "+l.Detail
			if l.DNSKEY.State != "validation_failed" {
				c.Verdict = "unknown"
			}
			return
		}
		switch l.Link {
		case "digest_mismatch":
			c.Verdict, c.Summary = "broken", "The DS records for "+l.Zone+" match none of its keys."
			return
		case "root_anchor_mismatch":
			c.Verdict, c.Summary = "broken", "No root key matches an IANA root anchor."
			return
		}
		if l.DS.State == "validation_failed" {
			c.Verdict, c.Summary = "broken", "resolved rejected the DS set for "+l.Zone+": "+l.DS.Detail
			return
		}
		authenticated = authenticated && l.DNSKEY.State == "authenticated" && (l.Link == "root_anchor" || l.DS.State == "authenticated" || l.Link == "no_ds")
	}
	top := apexes[len(apexes)-1]
	for _, l := range apexes {
		if l.Link == "no_ds" && l.Zone != top.Zone {
			c.Verdict, c.Summary = "insecure", "The delegation to "+l.Zone+" has no DS record, so nothing above it vouches for its keys."
			return
		}
	}
	switch {
	case top.Link == "root_anchor" && authenticated:
		c.Verdict, c.Summary = "secure", fmt.Sprintf("Every delegation from %s to the root was recomputed here and matches, the root keys match an IANA anchor, and resolved authenticated every set.", apexes[0].Zone)
	case top.Link == "root_anchor":
		c.Verdict, c.Summary = "insecure", "The digests link to the root anchor, but resolved did not authenticate every set: validation is off or downgraded for this scope."
	case authenticated:
		c.Verdict, c.Summary = "anchored", fmt.Sprintf("Every delegation up to %s matches and resolved authenticated each set; above %s the chain was not asked, or %s is trusted through a local anchor.", top.Zone, top.Zone, top.Zone)
	default:
		c.Verdict, c.Summary = "insecure", "resolved did not authenticate the keys of "+top.Zone+": validation is off or downgraded, or the zone is not signed from a trusted anchor."
	}
}
