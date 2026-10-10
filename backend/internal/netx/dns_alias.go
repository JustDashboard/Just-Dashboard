package netx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Tests replace the native adapter without changing the ordinary host runner.
var dnsNativeExecutor TrafficExecutor = runDNSNative

// DNSQueryEvidence retains each question, including alias discovery failures.
// Snapshot digests cover the complete routing/security policy; the selected
// before/after scopes retain the human-readable policy used for that question.
type DNSQueryEvidence struct {
	Name             string              `json:"name"`
	Type             string              `json:"type"`
	OwnerIdentity    string              `json:"ownerIdentity"`
	PolicyMatch      string              `json:"policyMatch"`
	Policy           []DNSPolicyScope    `json:"policy"`
	PolicyAfter      []DNSPolicyScope    `json:"policyAfter,omitempty"`
	SnapshotBefore   string              `json:"snapshotBefore,omitempty"`
	SnapshotAfter    string              `json:"snapshotAfter,omitempty"`
	PolicyStable     bool                `json:"policyStable"`
	AnswerInterfaces []int               `json:"answerInterfaces"`
	Records          []DNSRecordEvidence `json:"records"`
	Answers          []string            `json:"answers"`
	NativeFlags      string              `json:"nativeFlags,omitempty"`
	Route            DNSEvidenceReading  `json:"route"`
	Transport        DNSEvidenceReading  `json:"transport"`
	Trust            DNSEvidenceReading  `json:"trust"`
	DNSSEC           DNSEvidenceReading  `json:"dnssec"`
	AliasTarget      string              `json:"aliasTarget,omitempty"`
	Error            string              `json:"error,omitempty"`
}

func dnsWalkNative(ctx context.Context, execute TrafficExecutor, owner, name string, req DNSInvestigationRequest, r *DNSInvestigation) error {
	seen := map[string]bool{}
	accepted := []*DNSInvestigation{}
	wireBytes := 0
	for redirects := 0; ; {
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if seen[name] {
			r.Error = "The explicit CNAME chain contains a loop; no repeated or alternate question was sent."
			return nil
		}
		seen[name] = true
		kind := req.Type
		for {
			hop := DNSQueryEvidence{Name: name, Type: kind, OwnerIdentity: owner, Policy: []DNSPolicyScope{}, AnswerInterfaces: []int{}, Records: []DNSRecordEvidence{}, Answers: []string{}, Route: dnsUnknown("Policy has not been read."), Transport: dnsUnknown("This question has no accepted native transport flags."), Trust: dnsUnknown("This question has no accepted native trust evidence."), DNSSEC: dnsUnknown("This question has no accepted native authentication flags.")}
			all, snapshot, err := dnsNativeSnapshot(ctx, execute, owner)
			if err == nil {
				hop.Policy, hop.PolicyMatch = dnsBestPolicy(name, all)
				hop.SnapshotBefore = snapshot
				err = dnsCheckPolicy(hop.Policy)
			}
			if len(r.Hops) == 0 {
				r.Policy, r.PolicyMatch = hop.Policy, hop.PolicyMatch
			}
			if err != nil {
				hop.Error = err.Error() + "; no query or default-scope fallback was used."
				r.Hops = append(r.Hops, hop)
				r.Error = hop.Error
				return nil
			}
			r.Route = DNSEvidenceReading{"modeled", "native_configuration", "Longest matching routing/search suffix selects the listed candidate scopes. Each alias target is checked separately before a native question."}
			hop.Route = r.Route
			ifindex := 0
			if req.ExpectedInterface != "" {
				for _, p := range hop.Policy {
					if p.Interface == req.ExpectedInterface {
						ifindex = p.Index
					}
				}
				if ifindex <= 0 {
					if len(r.Hops) == 0 {
						return fmt.Errorf("the expected interface is not a native best-match DNS policy scope")
					}
					hop.Error = "The CNAME target does not belong to the expected interface's best-match policy; no target query was sent."
					r.Hops = append(r.Hops, hop)
					r.Error = hop.Error
					return nil
				}
			}
			// Reserve room for the bounded record metadata before disclosing another
			// name. An oversized policy must not turn a query into an unsavable report.
			r.Hops = append(r.Hops, hop)
			if encoded, _ := json.Marshal(r); len(encoded) > 64<<10 {
				r.Hops = r.Hops[:len(r.Hops)-1]
				r.Error = "The retained policy chain exceeds its bound; no additional query was sent."
				return nil
			}
			h := &r.Hops[len(r.Hops)-1]
			before, ownerErr := dnsBusOwner(ctx, execute)
			if ownerErr != nil || before != owner {
				h.Error = "Native resolver identity changed before the query; no query was sent."
				r.Error = h.Error
				return nil
			}
			out, queryErr := dnsBusCall(ctx, execute, owner, dnsNativePath, dnsNativeManager, "ResolveRecord", "isqqt", strconv.Itoa(ifindex), name+".", "1", strconv.Itoa(int(wireLookupTypes[kind])), strconv.FormatUint(dnsFreshFlags, 10))
			after, ownerErr := dnsBusOwner(ctx, execute)
			if ownerErr != nil || after != owner {
				h.Error = "Native resolver identity changed during the query; its result cannot establish trust or transport."
				r.Error = h.Error
				return nil
			}
			post, digest, postErr := dnsNativeSnapshot(ctx, execute, owner)
			if postErr == nil {
				h.PolicyAfter, _ = dnsBestPolicy(name, post)
				h.SnapshotAfter = digest
				h.PolicyStable = digest == snapshot
			}
			if queryErr != nil {
				h.Error = dnsBoundedError(queryErr.Error())
				if dnsNativeError(out, "DNSSEC validation failed:") {
					r.DNSSEC = DNSEvidenceReading{"validation_failed", "native_error_text", "The native resolver rejected DNSSEC validation. No alternative upstream was tested."}
					h.DNSSEC = r.DNSSEC
				}
				if kind != "CNAME" && dnsNativeError(out, "CNAME loop detected, or CNAME resolving disabled on '") {
					// NO_CNAME makes resolved stop before redirecting. Discover the
					// original owner's CNAME explicitly; DNAME cannot satisfy this
					// strict question and remains an unsupported refusal.
					kind = "CNAME"
					continue
				}
				r.Error = h.Error
				if kind == "CNAME" && req.Type != "CNAME" {
					r.Error = "The native alias could not be read as an exact-owner CNAME; DNAME and unreadable aliases are unsupported. " + h.Error
				}
				return nil
			}
			part := &DNSInvestigation{Policy: h.Policy, PolicyStable: h.PolicyStable, AnswerInterfaces: []int{}, Records: []DNSRecordEvidence{}}
			flags, bytes, decodeErr := decodeDNSNativeRecordBatch(out, name, kind, part)
			if decodeErr != nil || wireBytes+bytes > 65535 || len(r.Records)+len(part.Records) > 64 {
				if decodeErr == nil {
					decodeErr = fmt.Errorf("the CNAME chain exceeds the aggregate record bound")
				}
				h.Error = "Native record evidence is malformed: " + decodeErr.Error()
				r.Error = h.Error
				return nil
			}
			wireBytes += bytes
			part.NativeFlags = strconv.FormatUint(flags, 10)
			part.Route, part.Transport, part.Trust, part.DNSSEC = r.Route, dnsUnknown("Transport is unmeasured."), dnsUnknown("Trust is unmeasured."), dnsUnknown("Validation is unmeasured.")
			dnsInterpretNative(part, flags)
			h.Records, h.AnswerInterfaces, h.NativeFlags = part.Records, part.AnswerInterfaces, part.NativeFlags
			h.Answers = part.Answers
			h.Route, h.Transport, h.Trust, h.DNSSEC = part.Route, part.Transport, part.Trust, part.DNSSEC
			r.Records = append(r.Records, part.Records...)
			for _, index := range part.AnswerInterfaces {
				if !containsInt(r.AnswerInterfaces, index) {
					r.AnswerInterfaces = append(r.AnswerInterfaces, index)
				}
			}
			accepted = append(accepted, part)
			if encoded, _ := json.Marshal(r); len(encoded) > maxDNSEvidenceArtifact-8192 {
				h.Answers = []string{}
				r.Error = "Native answer data exceeds the retained artifact bound; no alternate or additional query was sent."
				return nil
			}
			if kind != "CNAME" || req.Type == "CNAME" {
				r.Answers = part.Answers
				if encoded, _ := json.Marshal(r); len(encoded) > maxDNSEvidenceArtifact-4096 {
					r.Answers = []string{}
					r.Error = "Native answer data exceeds the retained artifact bound; no alternate or additional query was sent."
					return nil
				}
				dnsInterpretChain(r, accepted)
				return nil
			}
			if len(part.Answers) != 1 {
				r.Error = "The native CNAME has no single canonical target; no target query was sent."
				return nil
			}
			target := strings.ToLower(strings.TrimSuffix(part.Answers[0], "."))
			if validDNSName(target, req.Type == "SRV" || req.Type == "TXT") != nil {
				r.Error = "The native CNAME target is unsupported; no target query was sent."
				return nil
			}
			h.AliasTarget = target
			if redirects >= 8 {
				r.Error = "The CNAME chain exceeds eight redirects; no additional target query was sent."
				return nil
			}
			redirects++
			name = target
			break
		}
	}
}

func dnsNativeSnapshot(ctx context.Context, execute TrafficExecutor, owner string) ([]DNSPolicyScope, string, error) {
	identity, err := dnsBusOwner(ctx, execute)
	if err != nil || identity != owner {
		return nil, "", fmt.Errorf("native resolver identity changed or is unreadable")
	}
	props, err := dnsBusProperties(ctx, execute, owner, dnsNativePath, "org.freedesktop.DBus.Properties", "GetAll", "s", dnsNativeManager)
	if err != nil {
		return nil, "", fmt.Errorf("native DNS policy is unreadable")
	}
	if err = dnsCheckDelegation(ctx, execute, props); err != nil {
		return nil, "", err
	}
	all, err := dnsPolicySnapshot(ctx, execute, owner, props)
	if err != nil {
		return nil, "", err
	}
	identity, err = dnsBusOwner(ctx, execute)
	if err != nil || identity != owner {
		return nil, "", fmt.Errorf("native resolver identity changed during the policy read")
	}
	stable := append([]DNSPolicyScope{}, all...)
	for i := range stable {
		stable[i].CurrentServer = ""
	}
	raw, _ := json.Marshal(stable)
	if len(raw) > 64<<10 {
		return nil, "", fmt.Errorf("native policy snapshot exceeds its aggregate bound")
	}
	hash := sha256.Sum256(raw)
	return all, hex.EncodeToString(hash[:]), nil
}

func dnsCheckDelegation(ctx context.Context, execute TrafficExecutor, props map[string]dnsVariant) error {
	mode := dnsVariantString(props["ResolvConfMode"])
	if mode != "stub" && mode != "static" {
		return fmt.Errorf("the host resolver does not delegate to a supported resolved stub; native policy evidence is unsupported")
	}
	out, err := execute(ctx, "cat", resolvConfPath)
	if err != nil || len(out) > 64<<10 {
		return fmt.Errorf("the host configured resolver chain is unreadable")
	}
	servers := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.SplitN(strings.SplitN(line, "#", 2)[0], ";", 2)[0])
		if len(fields) == 0 || fields[0] != "nameserver" {
			continue
		}
		if len(fields) != 2 || fields[1] != resolvedStub {
			return fmt.Errorf("the host configured resolver chain includes another owner; no resolved question was sent")
		}
		servers++
	}
	if servers == 0 {
		return fmt.Errorf("the host configured resolver chain has no supported resolved stub")
	}
	return nil
}

func dnsCheckPolicy(policy []DNSPolicyScope) error {
	if len(policy) == 0 {
		return fmt.Errorf("no readable native DNS policy scope applies")
	}
	for _, p := range policy {
		if !p.ActiveDNS || len(p.Servers) == 0 {
			return fmt.Errorf("a declared best-match DNS scope is unavailable")
		}
	}
	return nil
}

func dnsSecurityPolicyKnown(dnssec, tls string) bool {
	return (dnssec == "yes" || dnssec == "no" || dnssec == "allow-downgrade") && (tls == "yes" || tls == "no" || tls == "opportunistic")
}

func dnsPolicyDomainKnown(domain string) bool {
	return domain == "." || len(domain) <= 253 && validDNSName(domain, true) == nil
}

func dnsNativeError(out, prefix string) bool {
	// Server-provided EDE text is appended to ordinary RCODE failures. Only
	// the first native diagnostic prefix identifies this reported failure.
	line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	return strings.HasPrefix(line, "Call failed: "+prefix)
}

func dnsBoundedError(message string) string {
	message = firstLines(message, 3)
	if len(message) > 2048 {
		message = message[:2048]
	}
	return message
}

func dnsInterpretChain(r *DNSInvestigation, parts []*DNSInvestigation) {
	flags := ^uint64(0)
	stable, measured, encrypted, authenticated, strict := true, true, true, true, true
	for _, p := range parts {
		f, _ := strconv.ParseUint(p.NativeFlags, 10, 64)
		flags &= f
		stable = stable && p.PolicyStable
		measured = measured && p.Route.State == "measured"
		encrypted = encrypted && p.Transport.State == "encrypted"
		authenticated = authenticated && p.DNSSEC.State == "validated"
		strict = strict && p.Trust.State == "native_policy_validated"
	}
	r.NativeFlags, r.PolicyStable = strconv.FormatUint(flags, 10), stable
	if !stable {
		r.Limitations = append(r.Limitations, "Native policy changed or could not be reread during at least one accepted question. Strict TLS trust cannot be established for the complete chain.")
	}
	r.Limitations = append(r.Limitations, "Chain transport and validation describe accepted CNAME and terminal records. Failed alias-discovery questions have no native result flags, so their transport remains unknown.")
	if !measured {
		r.Limitations = append(r.Limitations, "At least one accepted chain record did not establish fresh network DNS origin; complete-chain trust and transport remain unknown.")
		return
	}
	r.Route = DNSEvidenceReading{"measured", "native_reply", "Every accepted CNAME and terminal record reports native network DNS and its answering interface. The exact upstream endpoint remains unmeasured."}
	if authenticated {
		r.DNSSEC = DNSEvidenceReading{"validated", "native_reply", "The native resolver reports authentication for every accepted CNAME and terminal record under its own validation policy."}
	} else {
		r.DNSSEC = DNSEvidenceReading{"not_authenticated", "native_reply", "The native resolver did not authenticate every accepted record in the complete alias chain."}
	}
	if encrypted {
		r.Transport = DNSEvidenceReading{"encrypted", "native_reply", "The native resolver reports confidential transport for every accepted CNAME and terminal record. Failed discovery questions and upstream forwarding remain unmeasured."}
	} else {
		r.Transport = DNSEvidenceReading{"unencrypted", "native_reply", "At least one accepted CNAME or terminal record lacked native-reported confidential transport."}
	}
	if strict {
		r.Trust = DNSEvidenceReading{"native_policy_validated", "native_reply_and_configuration", "Every accepted chain record has native-reported encryption and a stable strict TLS policy on every answering scope. Certificate chains were not independently inspected."}
	}
}
