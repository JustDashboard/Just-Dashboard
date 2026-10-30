package deploy

import (
	"context"
	"strings"
	"time"
)

// A domain's DNS record, before a release (dnsrecords, api's
// handlers_deploy_dns_record.go). When the public lookup says a planned
// name does not reach this server — or, for a name served through
// Cloudflare Tunnel, does not reach Cloudflare — preflight asks what record
// the name needs and whether the DNS credentials saved under Certificates
// can write it, and says so: dns_record_writable offers the Domains
// settings' Create the record, dns_record_missing names the record to make
// by hand. Either is a warning, as the lookup's own finding is: the release
// may go ahead and serve once the record exists. Nothing is written here.

// DNSRecordObservation is what a name needs and who could write it.
type DNSRecordObservation struct {
	// Records are the sets to write, as lines: "A app.example.com →
	// 203.0.113.7".
	Records []string `json:"records"`
	// Missing says the name holds no record of its own yet; otherwise
	// what it holds names another server.
	Missing bool `json:"missing"`
	// Writable says saved credentials reach the zone and nothing stands in
	// the way; Provider and Zone say whose and which.
	Writable bool   `json:"writable"`
	Provider string `json:"provider,omitempty"`
	Zone     string `json:"zone,omitempty"`
	// Present says the zone already holds exactly the records: resolvers
	// have yet to see them.
	Present bool `json:"present,omitempty"`
	// Detail is why it cannot be written, or what the write would be.
	Detail string `json:"detail,omitempty"`
}

// DNSRecordObserver answers for one planned domain; false when there is
// nothing to say (no public address to name, say).
type DNSRecordObserver func(ctx context.Context, domain PlannedDomain) (DNSRecordObservation, bool)

// WithDNSRecords attaches the DNS record observer to preflight.
func (o *HostPreflightObserver) WithDNSRecords(observer DNSRecordObserver) *HostPreflightObserver {
	o.dnsRecords = observer
	return o
}

// observeDNSRecord asks about a name whose lookup did not reach where it
// should: this server, or for a tunnel's name Cloudflare's edge.
func (o *HostPreflightObserver) observeDNSRecord(ctx context.Context, planned PlannedDomain, pointsHere, behindProxy bool) *DNSRecordObservation {
	if o.dnsRecords == nil || behindProxy || (pointsHere && !planned.Tunnel) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	observed, ok := o.dnsRecords(ctx, planned)
	if !ok {
		return nil
	}
	return &observed
}

// dnsRecordFixKind opens the Domains settings on a name's record.
const dnsRecordFixKind = "dns_record"

// dnsRecordFinding is preflight's word on a name's record.
func dnsRecordFinding(planned PlannedDomain, record *DNSRecordObservation, field string) (PreflightFinding, bool) {
	if record == nil || record.Present || len(record.Records) == 0 {
		return PreflightFinding{}, false
	}
	measured := strings.Join(record.Records, "; ")
	if record.Writable {
		title := "DNS record can be created from here"
		if !record.Missing {
			title = "DNS record can be corrected from here"
		}
		item := finding("dns_record_writable", PreflightWarning, title, measured,
			"The "+record.Provider+" credentials saved under Certificates reach the zone "+record.Zone+
				". Nothing is written until you press Create the record on the domain's settings.",
			"Create the record from Domains settings, then retry preflight.", "proxy", field)
		item.Fix = &CauseFix{Kind: dnsRecordFixKind, Field: field, Value: planned.Hostname}
		return item, true
	}
	title := "DNS record is missing"
	if !record.Missing {
		title = "DNS record names another server"
	}
	item := finding("dns_record_missing", PreflightWarning, title, measured, record.Detail,
		"Create the record where the zone is kept, or save that provider's API token under Certificates so it can be created from here; then retry preflight.",
		"proxy", field)
	item.DeepLink = "/proxy/certificates"
	return item, true
}
