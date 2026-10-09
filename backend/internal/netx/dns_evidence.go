package netx

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/net/dns/dnsmessage"
)

const (
	dnsNativeService = "org.freedesktop.resolve1"
	dnsNativePath    = "/org/freedesktop/resolve1"
	dnsNativeManager = "org.freedesktop.resolve1.Manager"
	// The stable native flag contract is systemd v257 resolved-def.h.
	dnsFlagDNS           uint64 = 1 << 0
	dnsFlagOtherProtocol uint64 = 1<<1 | 1<<2 | 1<<3 | 1<<4
	dnsFlagNoCNAME       uint64 = 1 << 5
	dnsFlagAuthenticated uint64 = 1 << 9
	dnsFlagNoValidate    uint64 = 1 << 10
	dnsFlagNoSynthesize  uint64 = 1 << 11
	dnsFlagNoCache       uint64 = 1 << 12
	dnsFlagNoZone        uint64 = 1 << 13
	dnsFlagNoTrustAnchor uint64 = 1 << 14
	dnsFlagConfidential  uint64 = 1 << 18
	dnsFlagSynthetic     uint64 = 1 << 19
	dnsFlagCache         uint64 = 1 << 20
	dnsFlagZone          uint64 = 1 << 21
	dnsFlagTrustAnchor   uint64 = 1 << 22
	dnsFlagNetwork       uint64 = 1 << 23
	dnsFlagNoStale       uint64 = 1 << 24
	// Preserve the owner's DNSSEC/TLS policy; exclude cached and local answers
	// that can otherwise carry the same authentication/confidentiality flags.
	// ResolveRecord adds NO_SEARCH internally and refuses it in caller flags.
	dnsFreshFlags     uint64 = dnsFlagDNS | dnsFlagNoCNAME | dnsFlagNoSynthesize | dnsFlagNoCache | dnsFlagNoZone | dnsFlagNoTrustAnchor | dnsFlagNoStale
	maxDNSNativeBytes        = 256 << 10
)

type DNSInvestigationRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// ExpectedInterface constrains the native request to an existing best-match
	// policy link. It cannot redirect a private question to another link.
	ExpectedInterface string `json:"expectedInterface,omitempty"`
}

type DNSEvidenceReading struct {
	State   string `json:"state"`
	Basis   string `json:"basis"`
	Summary string `json:"summary"`
}

type DNSPolicyScope struct {
	Index                int      `json:"index"`
	Interface            string   `json:"interface"`
	Domains              []string `json:"domains"`
	Servers              []string `json:"servers"`
	CurrentServer        string   `json:"currentServer,omitempty"`
	ActiveDNS            bool     `json:"activeDNS"`
	DefaultRoute         bool     `json:"defaultRoute"`
	DNSSEC               string   `json:"dnssec"`
	DNSOverTLS           string   `json:"dnsOverTLS"`
	NegativeTrustAnchors []string `json:"negativeTrustAnchors"`
}

type DNSRecordEvidence struct {
	InterfaceIndex int    `json:"interfaceIndex"`
	Owner          string `json:"owner"`
	Type           uint16 `json:"type"`
	TTL            uint32 `json:"ttl"`
}

type DNSInvestigation struct {
	ID               string                  `json:"id,omitempty"`
	Version          int                     `json:"version"`
	Request          DNSInvestigationRequest `json:"request"`
	StartedAt        time.Time               `json:"startedAt"`
	EndedAt          time.Time               `json:"endedAt"`
	Owner            string                  `json:"owner"`
	OwnerIdentity    string                  `json:"ownerIdentity,omitempty"`
	OwnerVersion     string                  `json:"ownerVersion,omitempty"`
	Vantage          string                  `json:"vantage"`
	AnswerFamily     string                  `json:"answerFamily"`
	UpstreamFamily   string                  `json:"upstreamFamily"`
	PolicyMatch      string                  `json:"policyMatch"`
	Policy           []DNSPolicyScope        `json:"policy"`
	PolicyStable     bool                    `json:"policyStable"`
	AnswerInterfaces []int                   `json:"answerInterfaces"`
	Answers          []string                `json:"answers"`
	Records          []DNSRecordEvidence     `json:"records"`
	NativeFlags      string                  `json:"nativeFlags,omitempty"`
	Hops             []DNSQueryEvidence      `json:"hops,omitempty"`
	Route            DNSEvidenceReading      `json:"route"`
	Transport        DNSEvidenceReading      `json:"transport"`
	Trust            DNSEvidenceReading      `json:"trust"`
	DNSSEC           DNSEvidenceReading      `json:"dnssec"`
	NSS              DNSEvidenceReading      `json:"nss"`
	Error            string                  `json:"error,omitempty"`
	Limitations      []string                `json:"limitations"`
}

func ValidateDNSInvestigation(req DNSInvestigationRequest) (DNSInvestigationRequest, error) {
	req.Type = strings.ToUpper(strings.TrimSpace(req.Type))
	if _, ok := wireLookupTypes[req.Type]; !ok {
		return req, fmt.Errorf("select A, AAAA, CNAME, MX, TXT, NS, PTR or SRV")
	}
	name, err := cleanLookupName(req.Name, req.Type)
	if err != nil {
		return req, err
	}
	req.Name = strings.ToLower(name)
	if req.ExpectedInterface != "" && ValidIfName(req.ExpectedInterface) != nil {
		return req, fmt.Errorf("invalid expected interface")
	}
	return req, nil
}

// InvestigateDNS asks only the active native owner. It never tests a different
// server after failure. execute is a trusted source adapter, never HTTP input.
func (s *Service) InvestigateDNS(ctx context.Context, req DNSInvestigationRequest, execute TrafficExecutor) (*DNSInvestigation, error) {
	req, err := ValidateDNSInvestigation(req)
	if err != nil {
		return nil, err
	}
	if execute == nil {
		execute = dnsNativeExecutor
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r := &DNSInvestigation{Version: 1, Request: req, StartedAt: time.Now().UTC(), Owner: "unknown", Vantage: "host_native_resolver", AnswerFamily: "record_dependent", UpstreamFamily: "not_measured", Policy: []DNSPolicyScope{}, AnswerInterfaces: []int{}, Answers: []string{}, Records: []DNSRecordEvidence{}, Limitations: []string{
		"Policy snapshots and the query are sequential, not an atomic packet trace. Current server values are configuration observations, not per-query upstream identification.",
		"The requested A/AAAA family describes answer data. Resolver upstream family, source address and exact upstream endpoint are not measured.",
		"Native-reported authentication/encryption is evidence from the identified resolver, not independent packet capture, certificate inspection or cryptographic chain validation.",
		"Application NSS/search behavior, browser/application DoH, container clients, upstream forwarding and provider policy remain unmeasured.",
	}}
	defer func() { r.EndedAt = time.Now().UTC() }()
	r.Route = dnsUnknown("The native policy has not been read.")
	r.Transport = dnsUnknown("Encrypted transport has not been measured.")
	r.Trust = dnsUnknown("Resolver certificate trust has not been measured.")
	r.DNSSEC = dnsUnknown("DNSSEC validation has not been measured.")
	r.NSS = DNSEvidenceReading{"not_measured", "scope", "An absolute DNS record query excludes hosts, NSS, search expansion, LLMNR and mDNS. An application's answer can differ."}
	if req.Type == "A" {
		r.AnswerFamily = "inet"
	}
	if req.Type == "AAAA" {
		r.AnswerFamily = "inet6"
	}
	owner, version, failure := dnsNativeIdentify(ctx, execute)
	r.OwnerIdentity, r.OwnerVersion = owner, version
	if version != "" || failure == "" || strings.HasPrefix(failure, "Native resolver version") {
		r.Owner = "systemd-resolved"
	}
	if failure != "" {
		r.Error = failure
		return r, nil
	}
	qname := req.Name
	if req.Type == "PTR" {
		ip, _ := netip.ParseAddr(qname)
		qname = reverseDNSName(ip)
	}
	return r, dnsWalkNative(ctx, execute, owner, qname, req, r)
}

// dnsNativeIdentify pins the system-bus owner of resolve1, checks that its
// process is systemd-resolved 256 or newer, and returns its unique bus name and
// version line, or the sentence explaining why no question may be sent.
func dnsNativeIdentify(ctx context.Context, execute TrafficExecutor) (string, string, string) {
	owner, err := dnsBusOwner(ctx, execute)
	if err != nil {
		return "", "", "Native resolved ownership is unavailable; no query or alternate resolver was used."
	}
	credentials, err := dnsBusProperties(ctx, execute, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "GetConnectionCredentials", "s", owner)
	if err != nil {
		return owner, "", "Cannot identify the resolver process; no query was sent."
	}
	pid, err := dnsVariantInt(credentials["ProcessID"])
	if err != nil || pid <= 0 {
		return owner, "", "Cannot identify the resolver process; no query was sent."
	}
	exe, err := execute(ctx, "readlink", "/proc/"+strconv.Itoa(pid)+"/exe")
	exe = strings.TrimSpace(exe)
	if err != nil || !filepath.IsAbs(exe) || filepath.Base(exe) != "systemd-resolved" {
		return owner, "", "The bus owner is not a supported systemd-resolved process; no query was sent."
	}
	out, err := execute(ctx, exe, "--version")
	if err != nil {
		return owner, "", "Native resolver version is unreadable; no query was sent."
	}
	version := strings.Split(strings.TrimSpace(out), "\n")[0]
	fields := strings.Fields(version)
	major := 0
	if len(fields) > 1 {
		major, _ = strconv.Atoi(fields[1])
	}
	if major < 256 {
		return owner, version, "This adapter requires systemd-resolved 256 or newer for fresh network-origin evidence; no query was sent."
	}
	return owner, version, ""
}

func dnsUnknown(summary string) DNSEvidenceReading {
	return DNSEvidenceReading{"unknown", "unmeasured", summary}
}

func dnsInterpretNative(r *DNSInvestigation, flags uint64) {
	// The same flags also describe synthetic, cache and local-zone answers.
	// Accept a measurement only when the native service explicitly says DNS
	// from the network, with none of those local-origin bits.
	localOrigin := dnsFlagSynthetic | dnsFlagCache | dnsFlagZone | dnsFlagTrustAnchor
	knownOutput := dnsFlagDNS | dnsFlagOtherProtocol | dnsFlagAuthenticated | dnsFlagConfidential | localOrigin | dnsFlagNetwork
	// A future/unknown output bit cannot silently acquire an authentication or
	// transport meaning that this version of the adapter has not inspected.
	fresh := flags&dnsFlagDNS != 0 && flags&dnsFlagNetwork != 0 && flags&(localOrigin|dnsFlagOtherProtocol) == 0 && flags&^knownOutput == 0
	if !fresh {
		r.Limitations = append(r.Limitations, "The reply did not establish fresh network DNS origin. Authentication/confidentiality flags cannot prove DNSSEC or encrypted upstream transport.")
		return
	}
	r.Route = DNSEvidenceReading{"measured", "native_reply", "The native reply reports the listed answering interface indexes. Index zero is global/unspecified; the exact upstream server remains unmeasured."}
	if flags&dnsFlagAuthenticated != 0 {
		r.DNSSEC = DNSEvidenceReading{"validated", "native_reply", "The native resolver reports authenticated fresh DNS data under its own trust-anchor and validation policy."}
	} else {
		r.DNSSEC = DNSEvidenceReading{"not_authenticated", "native_reply", "The native resolver did not authenticate this answer. Configuration alone cannot distinguish unsigned data, disabled validation, downgrade or a negative trust anchor."}
	}
	if flags&dnsFlagConfidential == 0 {
		r.Transport = DNSEvidenceReading{"unencrypted", "native_reply", "The native resolver reports a fresh DNS answer without confidential transport."}
		return
	}
	r.Transport = DNSEvidenceReading{"encrypted", "native_reply", "The native resolver reports confidential transport for this fresh network DNS answer. This is not a packet trace or proof about upstream forwarding."}
	strict := r.PolicyStable && len(r.AnswerInterfaces) > 0
	// A global-scope reply carries the index of the interface it arrived on,
	// not scope zero. When the global scope is the only candidate, the question
	// went to its servers alone, so its policy covers that reply.
	globalOnly := len(r.Policy) == 1 && r.Policy[0].Index == 0
	for _, index := range r.AnswerInterfaces {
		found := false
		for _, p := range r.Policy {
			if p.Index == index {
				found = true
				strict = strict && p.DNSOverTLS == "yes"
			}
		}
		if !found && globalOnly {
			found = true
			strict = strict && r.Policy[0].DNSOverTLS == "yes"
		}
		strict = strict && found
	}
	if strict {
		r.Trust = DNSEvidenceReading{"native_policy_validated", "native_reply_and_configuration", "The answering scope requires strict DNS over TLS and the native resolver reports encrypted network data. Certificate validation follows its configured identity and system trust store; the certificate/chain was not independently inspected."}
	} else {
		r.Trust = dnsUnknown("Encryption was reported, but strict certificate-authentication policy could not be established for every answering scope. Opportunistic TLS does not prove certificate trust.")
	}
}

type dnsVariant struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}
type dnsBusReply struct {
	Type string            `json:"type"`
	Data []json.RawMessage `json:"data"`
}

func dnsBusCall(ctx context.Context, execute TrafficExecutor, destination, path, iface, method, signature string, args ...string) (string, error) {
	argv := []string{"--system", "--json=short", "--timeout=5s", "call", destination, path, iface, method}
	if signature != "" {
		argv = append(argv, signature)
		argv = append(argv, args...)
	}
	out, err := execute(ctx, "busctl", argv...)
	if len(out) > maxDNSNativeBytes {
		return "", fmt.Errorf("native DNS reply exceeds its bound")
	}
	return out, err
}

func dnsBusOwner(ctx context.Context, execute TrafficExecutor) (string, error) {
	out, err := dnsBusCall(ctx, execute, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "GetNameOwner", "s", dnsNativeService)
	if err != nil {
		return "", err
	}
	var reply dnsBusReply
	var owner string
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Type != "s" || len(reply.Data) != 1 || json.Unmarshal(reply.Data[0], &owner) != nil || !strings.HasPrefix(owner, ":") {
		return "", fmt.Errorf("native bus owner is unreadable")
	}
	return owner, nil
}

func dnsBusProperties(ctx context.Context, execute TrafficExecutor, destination, path, iface, method, signature string, args ...string) (map[string]dnsVariant, error) {
	out, err := dnsBusCall(ctx, execute, destination, path, iface, method, signature, args...)
	if err != nil {
		return nil, err
	}
	var reply dnsBusReply
	var props map[string]dnsVariant
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Type != "a{sv}" || len(reply.Data) != 1 || json.Unmarshal(reply.Data[0], &props) != nil || len(props) > 128 {
		return nil, fmt.Errorf("native properties are unreadable")
	}
	return props, nil
}
func dnsVariantInt(v dnsVariant) (int, error) {
	var n int
	err := json.Unmarshal(v.Data, &n)
	return n, err
}
func dnsVariantString(v dnsVariant) string {
	var s string
	if v.Type == "s" {
		_ = json.Unmarshal(v.Data, &s)
	}
	return s
}
func dnsVariantStrings(v dnsVariant) ([]string, error) {
	var s []string
	if v.Type != "as" || json.Unmarshal(v.Data, &s) != nil || len(s) > 256 {
		return nil, fmt.Errorf("native trust anchors are unreadable or exceed bounds")
	}
	for _, v := range s {
		if len(v) > 253 {
			return nil, fmt.Errorf("native trust anchor name exceeds bounds")
		}
	}
	if s == nil {
		s = []string{}
	}
	return s, nil
}

func dnsPolicySnapshot(ctx context.Context, execute TrafficExecutor, owner string, props map[string]dnsVariant) ([]DNSPolicyScope, error) {
	var servers, domains [][]json.RawMessage
	if props["DNSEx"].Type != "a(iiayqs)" || json.Unmarshal(props["DNSEx"].Data, &servers) != nil || len(servers) > 256 || props["Domains"].Type != "a(isb)" || json.Unmarshal(props["Domains"].Data, &domains) != nil || len(domains) > 2048 {
		return nil, fmt.Errorf("native routing/server policy is unreadable or exceeds bounds")
	}
	global := DNSPolicyScope{Index: 0, Interface: "global", Domains: []string{}, Servers: []string{}, DNSSEC: dnsVariantString(props["DNSSEC"]), DNSOverTLS: dnsVariantString(props["DNSOverTLS"])}
	if !dnsSecurityPolicyKnown(global.DNSSEC, global.DNSOverTLS) {
		return nil, fmt.Errorf("native DNS security policy is unreadable")
	}
	var err error
	global.NegativeTrustAnchors, err = dnsVariantStrings(props["DNSSECNegativeTrustAnchors"])
	if err != nil {
		return nil, err
	}
	// The manager flattens global and per-link servers. Its ifindex can be the
	// address scope (loopback is a real example), not the owning policy link.
	// Enumerate native link objects and subtract their endpoint multisets to
	// recover global entries, rather than attributing policy by that field.
	type scopedEndpoint struct {
		index    int
		endpoint string
	}
	remaining := []scopedEndpoint{}
	for _, row := range servers {
		scope, server, err := dnsNativeServer(row, true)
		if err != nil {
			return nil, err
		}
		remaining = append(remaining, scopedEndpoint{scope, server})
	}
	type scopedDomain struct {
		index   int
		name    string
		routing bool
	}
	remainingDomains := []scopedDomain{}
	for _, row := range domains {
		var index int
		var domain string
		var routing bool
		if len(row) != 3 || json.Unmarshal(row[0], &index) != nil || index < 0 || json.Unmarshal(row[1], &domain) != nil || json.Unmarshal(row[2], &routing) != nil || !dnsPolicyDomainKnown(domain) {
			return nil, fmt.Errorf("native domain policy is unreadable")
		}
		if index == 0 {
			if routing {
				domain = "~" + domain
			}
			global.Domains = append(global.Domains, domain)
		} else {
			remainingDomains = append(remainingDomains, scopedDomain{index, domain, routing})
		}
	}
	linksOut, err := execute(ctx, "ip", "-j", "link", "show")
	if err != nil {
		return nil, fmt.Errorf("native interface names are unreadable")
	}
	var links []struct {
		Index int    `json:"ifindex"`
		Name  string `json:"ifname"`
	}
	if json.Unmarshal([]byte(linksOut), &links) != nil || len(links) > 4096 {
		return nil, fmt.Errorf("native interface names exceed bounds")
	}
	names := map[int]string{}
	for _, link := range links {
		names[link.Index] = link.Name
	}
	tree, err := execute(ctx, "busctl", "--system", "--list", "--no-pager", "tree", owner)
	if err != nil || len(tree) > maxDNSNativeBytes {
		return nil, fmt.Errorf("native resolver link inventory is unreadable")
	}
	objects := []string{}
	for _, path := range strings.Fields(tree) {
		if strings.HasPrefix(path, "/org/freedesktop/resolve1/link/_") {
			objects = append(objects, path)
		}
	}
	if len(objects) > 128 {
		return nil, fmt.Errorf("native resolver inventory exceeds 128 links")
	}
	sort.Strings(objects)
	result := []DNSPolicyScope{}
	seenIndexes := map[int]bool{}
	for _, path := range objects {
		index, err := dnsNativeLinkIndex(path)
		if err != nil || index < 1 || seenIndexes[index] {
			return nil, fmt.Errorf("native link object identity is unreadable")
		}
		seenIndexes[index] = true
		lp, err := dnsBusProperties(ctx, execute, owner, path, "org.freedesktop.DBus.Properties", "GetAll", "s", "org.freedesktop.resolve1.Link")
		if err != nil {
			return nil, err
		}
		var localServers [][]json.RawMessage
		if lp["DNSEx"].Type != "a(iayqs)" || json.Unmarshal(lp["DNSEx"].Data, &localServers) != nil || len(localServers) > 256 {
			return nil, fmt.Errorf("native link servers are unreadable")
		}
		p := DNSPolicyScope{Index: index, Interface: names[index], Servers: []string{}, Domains: []string{}}
		var scopes uint64
		if lp["ScopesMask"].Type != "t" || json.Unmarshal(lp["ScopesMask"].Data, &scopes) != nil {
			return nil, fmt.Errorf("native link protocol scope is unreadable")
		}
		p.ActiveDNS = scopes&dnsFlagDNS != 0
		if ValidIfName(p.Interface) != nil {
			return nil, fmt.Errorf("native interface identity is unreadable")
		}
		for _, row := range localServers {
			_, server, err := dnsNativeServer(row, false)
			if err != nil {
				return nil, err
			}
			scoped, err := dnsEvidenceScopedServer(server, index, names)
			if err != nil {
				return nil, err
			}
			p.Servers = append(p.Servers, scoped)
			expectedScope := index
			parsed, _ := parseDNSServer(server)
			if parsed.addr.IsLoopback() {
				expectedScope = 1
			}
			found := false
			for i, value := range remaining {
				if value.endpoint == server && value.index == expectedScope {
					remaining = append(remaining[:i], remaining[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("native server inventories changed during the snapshot")
			}
		}
		var localDomains [][]json.RawMessage
		if lp["Domains"].Type != "a(sb)" || json.Unmarshal(lp["Domains"].Data, &localDomains) != nil || len(localDomains) > 256 {
			return nil, fmt.Errorf("native link routing domains are unreadable")
		}
		for _, row := range localDomains {
			var domain string
			var routing bool
			if len(row) != 2 || json.Unmarshal(row[0], &domain) != nil || !dnsPolicyDomainKnown(domain) || json.Unmarshal(row[1], &routing) != nil {
				return nil, fmt.Errorf("native link domain is unreadable")
			}
			found := false
			for i, declared := range remainingDomains {
				if declared.index == index && declared.name == domain && declared.routing == routing {
					remainingDomains = append(remainingDomains[:i], remainingDomains[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("native domain inventories changed during the snapshot")
			}
			if routing {
				domain = "~" + domain
			}
			p.Domains = append(p.Domains, domain)
		}
		if lp["DefaultRoute"].Type != "b" || json.Unmarshal(lp["DefaultRoute"].Data, &p.DefaultRoute) != nil {
			return nil, fmt.Errorf("native default-route policy is unreadable")
		}
		p.DNSSEC = dnsVariantString(lp["DNSSEC"])
		if lp["DNSSEC"].Type != "s" || lp["DNSOverTLS"].Type != "s" {
			return nil, fmt.Errorf("native link DNS security policy is unreadable")
		}
		if p.DNSSEC == "" {
			p.DNSSEC = global.DNSSEC
		}
		p.DNSOverTLS = dnsVariantString(lp["DNSOverTLS"])
		if p.DNSOverTLS == "" {
			p.DNSOverTLS = global.DNSOverTLS
		}
		if !dnsSecurityPolicyKnown(p.DNSSEC, p.DNSOverTLS) {
			return nil, fmt.Errorf("native link DNS security policy is unreadable")
		}
		p.NegativeTrustAnchors, err = dnsVariantStrings(lp["DNSSECNegativeTrustAnchors"])
		if err != nil {
			return nil, err
		}
		p.NegativeTrustAnchors = append(p.NegativeTrustAnchors, global.NegativeTrustAnchors...)
		sort.Strings(p.NegativeTrustAnchors)
		if value := lp["CurrentDNSServerEx"]; value.Type == "(iayqs)" {
			var row []json.RawMessage
			if json.Unmarshal(value.Data, &row) == nil {
				_, p.CurrentServer, _ = dnsNativeServer(row, false)
			}
		}
		if len(p.Servers) > 0 || len(p.Domains) > 0 {
			result = append(result, p)
		}
	}
	if len(remainingDomains) != 0 {
		return nil, fmt.Errorf("native declared domain policy has unreadable link ownership")
	}
	for _, entry := range remaining {
		scoped, err := dnsEvidenceScopedServer(entry.endpoint, entry.index, names)
		if err != nil {
			return nil, err
		}
		global.Servers = append(global.Servers, scoped)
	}
	global.ActiveDNS = len(global.Servers) > 0
	sort.Strings(global.NegativeTrustAnchors)
	if value := props["CurrentDNSServerEx"]; value.Type == "(iiayqs)" {
		var row []json.RawMessage
		if json.Unmarshal(value.Data, &row) == nil {
			_, global.CurrentServer, _ = dnsNativeServer(row, true)
		}
	}
	result = append([]DNSPolicyScope{global}, result...)
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

func dnsEvidenceScopedServer(server string, index int, names map[int]string) (string, error) {
	parsed, err := parseDNSServer(server)
	if err != nil {
		return "", err
	}
	if !parsed.addr.IsLinkLocalUnicast() {
		return server, nil
	}
	iface := names[index]
	if ValidIfName(iface) != nil {
		return "", fmt.Errorf("native link-local DNS address scope is unreadable")
	}
	addr := parsed.addr.WithZone(iface)
	value := addr.String()
	if parsed.port != "" {
		port, _ := strconv.Atoi(parsed.port)
		value = netip.AddrPortFrom(addr, uint16(port)).String()
	}
	if parsed.tlsName != "" {
		value += "#" + parsed.tlsName
	}
	return value, nil
}

func dnsNativeLinkIndex(path string) (int, error) {
	value := strings.TrimPrefix(path, "/org/freedesktop/resolve1/link/")
	var decoded strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '_' {
			if i+2 >= len(value) {
				return 0, fmt.Errorf("invalid native object escape")
			}
			raw, err := hex.DecodeString(value[i+1 : i+3])
			if err != nil {
				return 0, err
			}
			decoded.WriteByte(raw[0])
			i += 2
		} else {
			decoded.WriteByte(value[i])
		}
	}
	return strconv.Atoi(decoded.String())
}

func dnsNativeServer(row []json.RawMessage, withIndex bool) (int, string, error) {
	index := 0
	offset := 0
	var family, port int
	var raw []byte
	var name string
	if withIndex {
		if len(row) != 5 || json.Unmarshal(row[0], &index) != nil || index < 0 {
			return 0, "", fmt.Errorf("invalid native DNS interface")
		}
		offset = 1
	} else if len(row) != 4 {
		return 0, "", fmt.Errorf("invalid native server tuple")
	}
	if json.Unmarshal(row[offset], &family) != nil || json.Unmarshal(row[offset+1], &raw) != nil || json.Unmarshal(row[offset+2], &port) != nil || port < 0 || port > 65535 || json.Unmarshal(row[offset+3], &name) != nil {
		return 0, "", fmt.Errorf("invalid native DNS server")
	}
	ip, ok := netip.AddrFromSlice(raw)
	if !ok || family != 2 && family != 10 || (family == 2) != ip.Is4() || name != "" && validDNSName(name, false) != nil {
		return 0, "", fmt.Errorf("invalid native DNS server address or TLS identity")
	}
	address := ip.String()
	if port != 0 {
		address = netip.AddrPortFrom(ip, uint16(port)).String()
	}
	if name != "" {
		address += "#" + name
	}
	return index, address, nil
}

func dnsBestPolicy(name string, all []DNSPolicyScope) ([]DNSPolicyScope, string) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	best := -1
	chosen := []DNSPolicyScope{}
	match := "native default-route policy"
	for _, p := range all {
		score := -1
		for _, domain := range p.Domains {
			domain = strings.ToLower(strings.TrimPrefix(domain, "~"))
			if domain != "." {
				domain = strings.TrimSuffix(domain, ".")
			}
			if domain == "." {
				score = max(score, 0)
			} else if name == domain || strings.HasSuffix(name, "."+domain) {
				score = max(score, len(strings.Split(domain, ".")))
			}
		}
		if score > best {
			best = score
			chosen = []DNSPolicyScope{p}
		} else if score == best && score >= 0 {
			chosen = append(chosen, p)
		}
	}
	if best >= 0 {
		return chosen, fmt.Sprintf("longest suffix: %d labels", best)
	}
	for _, p := range all {
		if p.ActiveDNS && len(p.Servers) > 0 && (p.Index == 0 || p.DefaultRoute) {
			chosen = append(chosen, p)
		}
	}
	return chosen, match
}

func decodeDNSNativeRecords(out, name, rtype string, r *DNSInvestigation) (uint64, error) {
	flags, _, err := decodeDNSNativeRecordBatch(out, name, rtype, r)
	return flags, err
}

func decodeDNSNativeRecordBatch(out, name, rtype string, r *DNSInvestigation) (uint64, int, error) {
	var reply dnsBusReply
	var rows [][]json.RawMessage
	var flags uint64
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Type != "a(iqqay)t" || len(reply.Data) != 2 || json.Unmarshal(reply.Data[0], &rows) != nil || len(rows) == 0 || len(rows) > 64 || json.Unmarshal(reply.Data[1], &flags) != nil {
		return 0, 0, fmt.Errorf("invalid native record envelope")
	}
	fqdn, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return 0, 0, err
	}
	q := dnsmessage.Question{Name: fqdn, Type: wireLookupTypes[rtype], Class: dnsmessage.ClassINET}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 1, Response: true})
	_ = b.StartQuestions()
	_ = b.Question(q)
	packet, _ := b.Finish()
	binary.BigEndian.PutUint16(packet[6:8], uint16(len(rows)))
	records := []DNSRecordEvidence{}
	interfaces := []int{}
	wireBytes := 0
	for _, row := range rows {
		var index int
		var class, kind uint16
		var wire []byte
		if len(row) != 4 || json.Unmarshal(row[0], &index) != nil || index < 0 || json.Unmarshal(row[1], &class) != nil || class != 1 || json.Unmarshal(row[2], &kind) != nil || json.Unmarshal(row[3], &wire) != nil || len(wire) > 65535 {
			return 0, 0, fmt.Errorf("invalid native record tuple")
		}
		owner, offset, err := dnsExpandedOwner(wire)
		if err != nil {
			return 0, 0, err
		}
		if !strings.EqualFold(owner, strings.TrimSuffix(name, ".")) || kind != uint16(q.Type) {
			return 0, 0, fmt.Errorf("native record does not match the exact question owner and type")
		}
		if len(wire) < offset+10 || binary.BigEndian.Uint16(wire[offset:]) != kind || binary.BigEndian.Uint16(wire[offset+2:]) != class || len(wire) != offset+10+int(binary.BigEndian.Uint16(wire[offset+8:])) {
			return 0, 0, fmt.Errorf("native record metadata does not match its wire data")
		}
		records = append(records, DNSRecordEvidence{index, owner, kind, binary.BigEndian.Uint32(wire[offset+4:])})
		if !containsInt(interfaces, index) {
			interfaces = append(interfaces, index)
		}
		wireBytes += len(wire)
		packet = append(packet, wire...)
		if len(packet) > 65535 {
			return 0, 0, fmt.Errorf("native record aggregate exceeds bound")
		}
	}
	answers, _, err := parseDNSResponse(packet, 1, q)
	if err != nil {
		return 0, 0, err
	}
	if len(answers) == 0 {
		return 0, 0, fmt.Errorf("native reply contains no exact-owner records")
	}
	if rtype == "CNAME" {
		for _, answer := range answers[1:] {
			if !strings.EqualFold(answer, answers[0]) {
				return 0, 0, fmt.Errorf("native reply contains conflicting CNAME targets")
			}
		}
		answers = answers[:1]
	}
	r.Answers = answers
	r.Records, r.AnswerInterfaces = records, interfaces
	sort.Ints(r.AnswerInterfaces)
	return flags, wireBytes, nil
}

func containsInt(values []int, value int) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func dnsExpandedOwner(wire []byte) (string, int, error) {
	labels := []string{}
	offset := 0
	for offset < len(wire) {
		length := int(wire[offset])
		offset++
		if length == 0 {
			return strings.Join(labels, "."), offset, nil
		}
		if length > 63 || offset+length > len(wire) {
			break
		}
		labels = append(labels, string(wire[offset:offset+length]))
		offset += length
		if offset > 254 {
			break
		}
	}
	return "", 0, fmt.Errorf("native RR owner is not expanded DNS wire data")
}

type dnsBoundedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *dnsBoundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > maxDNSNativeBytes {
		b.exceeded = true
		p = p[:max(0, maxDNSNativeBytes-b.Len())]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

var _ io.Writer = (*dnsBoundedBuffer)(nil)

func runDNSNative(ctx context.Context, name string, args ...string) (string, error) {
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	var out dnsBoundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_, err := hostexec.RunGroup(ctx, cmd, 200*time.Millisecond)
	if out.exceeded {
		return "", errors.New("native DNS command output exceeds its bound")
	}
	if err != nil {
		return out.String(), fmt.Errorf("native DNS command failed: %s", firstLines(out.String(), 3))
	}
	return out.String(), nil
}
