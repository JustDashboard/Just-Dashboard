package netsec

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Registry lookups with their source named and their uncertainty stated: a
// registration country is not a server's location, a redacted field is not an
// absent one, and an unanswered query is not "no registration".

type asnRow struct {
	AS, IP, Prefix, Country, Registry, Allocated, Name string
}

func parseCymru(out string) []asnRow {
	var rows []asnRow
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// The header row starts with "AS ", data rows with the number; the
		// Bulk-mode banner and separator lines are prose, not answers.
		if strings.HasPrefix(line, "AS ") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "Bulk") || strings.HasPrefix(line, "AS Name") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 7 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		rows = append(rows, asnRow{AS: fields[0], IP: fields[1], Prefix: fields[2], Country: fields[3], Registry: fields[4], Allocated: fields[5], Name: fields[6]})
	}
	return rows
}

// ASNLookup reports who announces an address — autonomous system, prefix,
// registry and registration country — from Team Cymru's whois service.
func (s *Service) ASNLookup(ctx context.Context, target string) (*ProbeResult, error) {
	if net.ParseIP(strings.TrimSpace(target)) == nil {
		return nil, fmt.Errorf("an ownership lookup takes an IP address, not a name")
	}
	res := &ProbeResult{Tool: "asn", Target: strings.TrimSpace(target)}
	if !diagnosticHas("whois") {
		res.Error = "whois is not installed on this host"
		res.Verdict = ProbeFailed
		return res, nil
	}
	out, elapsed, err := diagnosticRun(ctx, 20*time.Second, "whois", "-h", "whois.cymru.com", "-v", strings.TrimSpace(target))
	res.Duration = elapsed
	res.fact("Source", "Team Cymru IP-to-ASN service (whois.cymru.com), built from BGP announcements and regional registry allocation files", BasisRegistry)
	res.Limitations = append(res.Limitations,
		"Country is the one the registry recorded for the allocation. It is not where the server is: anycast, multinational networks and reassigned blocks make geography uncertain.",
		"The autonomous system and prefix are what the service saw announced when asked; announcements change. The address was sent to this third-party service.")
	rows := parseCymru(out)
	if err != nil && len(rows) == 0 {
		res.Error = err.Error()
		res.Output = strings.TrimSpace(out)
		res.Verdict, res.Summary = ProbeFailed, "The ownership lookup failed: "+shortDialError(err)
		return res, nil
	}
	if len(rows) == 0 {
		res.Error = "whois.cymru.com returned nothing usable"
		res.Output = strings.TrimSpace(out)
		if len(res.Output) > maxProbeOutput {
			res.Output = res.Output[:maxProbeOutput] + "\n… (truncated)"
		}
		res.Verdict, res.Summary = ProbeFailed, "The ownership service answered without a usable row."
		return res, nil
	}
	table := ProbeTable{ID: "origins", Title: "Announcements", Columns: []string{"AS", "AS name", "BGP prefix", "Registration country", "Registry", "Allocated"}}
	var b strings.Builder
	announced := false
	for _, r := range rows {
		table.Rows = append(table.Rows, []string{"AS" + r.AS, r.Name, r.Prefix, r.Country, r.Registry, r.Allocated})
		fmt.Fprintf(&b, "AS%-9s %s\n", r.AS, r.Name)
		fmt.Fprintf(&b, "Prefix:   %s\n", r.Prefix)
		fmt.Fprintf(&b, "Country:  %s   Registry: %s\n", r.Country, r.Registry)
		res.Records = append(res.Records, "AS"+r.AS+" "+r.Name, r.Prefix)
		announced = announced || (r.AS != "NA" && r.AS != "")
	}
	res.Tables = append(res.Tables, table)
	first := rows[0]
	res.fact("Registration country", first.Country+" (registry record, not a location)", BasisRegistry)
	if first.Allocated != "" {
		res.fact("Allocated", first.Allocated+" by "+strings.ToUpper(first.Registry), BasisRegistry)
	}
	if len(rows) > 1 {
		res.finding("multiple-origins", "notice", "More than one origin AS", "Several autonomous systems announce this address. This is normal for some anycast and DDoS-protection services and otherwise a sign of a routing problem.", "Network operator")
	}
	res.OK = true
	res.Output = strings.TrimSpace(b.String())
	if !announced {
		res.Verdict = ProbeUnknown
		res.Summary = "No BGP announcement was found for this address; it may be private, reserved or unrouted."
		return res, nil
	}
	res.Verdict = ProbeOK
	res.Summary = fmt.Sprintf("AS%s (%s) announces %s; registered in %s.", first.AS, first.Name, first.Prefix, first.Country)
	if len(res.Findings) > 0 {
		res.Verdict = ProbeFindings
	}
	return res, nil
}

type whoisField struct {
	label string
	keys  []string
	multi bool
	ip    bool
	both  bool
}

var whoisFields = []whoisField{
	{label: "Domain", keys: []string{"domain name", "domain"}},
	{label: "Registrar", keys: []string{"registrar", "registrar name", "sponsoring registrar"}},
	{label: "Created", keys: []string{"creation date", "created", "created on", "registered on", "registration time", "domain registration date"}, both: true},
	{label: "Updated", keys: []string{"updated date", "last updated", "last-modified", "changed", "last modified", "updated"}, both: true},
	{label: "Expires", keys: []string{"registry expiry date", "registrar registration expiration date", "expiry date", "expiration date", "paid-till", "expires on", "expire date"}},
	{label: "Status", keys: []string{"domain status", "status"}, multi: true, both: true},
	{label: "Name servers", keys: []string{"name server", "nserver", "nameservers", "name servers"}, multi: true},
	{label: "DNSSEC", keys: []string{"dnssec"}},
	{label: "Registrant organisation", keys: []string{"registrant organization", "registrant organisation", "registrant name"}},
	{label: "Registrant country", keys: []string{"registrant country"}},
	{label: "Address range", keys: []string{"netrange", "inetnum", "inet6num"}, ip: true},
	{label: "CIDR", keys: []string{"cidr", "route", "route6"}, ip: true},
	{label: "Network name", keys: []string{"netname"}, ip: true},
	{label: "Organisation", keys: []string{"orgname", "org-name", "organization", "owner"}, ip: true},
	{label: "Country", keys: []string{"country"}, ip: true},
	{label: "Abuse contact", keys: []string{"orgabuseemail", "abuse-mailbox", "registrar abuse contact email", "abuse contact email"}, both: true},
}

var (
	whoisRedacted = regexp.MustCompile(`(?i)redacted|data protected|not disclosed|withheld|gdpr|statutory masking|privacy service|contact privacy|whoisguard|domains by proxy|identity protect|private registration`)
	whoisNoMatch  = regexp.MustCompile(`(?im)^\s*(?:%+\s*)?(no match for|not found|no data found|no entries found|domain not found|no matching record|the queried object does not exist|status:\s*(?:free|available)|no object found)`)
	whoisFailure  = regexp.MustCompile(`(?i)connection refused|timed out|timeout|connection reset|no whois server is known|name or service not known|temporary failure in name resolution|limit exceeded|quota exceeded|too many requests|rate limit`)
)

func whoisSource(out string) string {
	lower := strings.ToLower(out)
	for _, r := range []struct{ match, name string }{
		{"arin whois data", "ARIN"}, {"ripe database", "RIPE NCC"}, {"apnic", "APNIC"}, {"lacnic", "LACNIC"},
		{"afrinic", "AFRINIC"}, {"verisign", "Verisign registry"}, {"public interest registry", "Public Interest Registry"},
	} {
		if strings.Contains(lower, r.match) {
			return r.name
		}
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "registrar whois server") && strings.TrimSpace(value) != "" {
			return "registrar server " + strings.TrimSpace(value)
		}
	}
	return ""
}

type whoisValue struct {
	Label, Value, State string
}

// normaliseWhois maps the many registries' key spellings onto one set of
// fields and says, for each, whether it was present, redacted or absent.
func normaliseWhois(out string, ip bool) []whoisValue {
	found := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "%") || strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(strings.TrimSpace(line), ">>>") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" || len(key) > 48 {
			continue
		}
		for _, field := range whoisFields {
			for _, k := range field.keys {
				if key == k {
					found[field.label] = append(found[field.label], value)
				}
			}
		}
	}
	var out2 []whoisValue
	for _, field := range whoisFields {
		if !field.both && field.ip != ip {
			continue
		}
		values := sortedUniqueFold(found[field.label])
		if !field.multi && len(values) > 1 {
			values = values[:1]
		}
		state := "present"
		value := strings.Join(values, ", ")
		switch {
		case len(values) == 0:
			state = "absent"
		case whoisRedacted.MatchString(value):
			state = "redacted"
		}
		if field.label == "Name servers" {
			value = strings.ToLower(value)
		}
		if len(value) > 300 {
			value = value[:300] + "…"
		}
		out2 = append(out2, whoisValue{Label: field.label, Value: value, State: state})
	}
	return out2
}

func sortedUniqueFold(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		k := strings.ToLower(v)
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}

// Whois looks a domain or address up in the registries and normalises the
// answer. It fails soft when whois is not installed.
func (s *Service) Whois(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	res := &ProbeResult{Tool: "whois", Target: target}
	if !diagnosticHas("whois") {
		res.Error = "whois is not installed on this host"
		res.Verdict = ProbeFailed
		return res, nil
	}
	out, elapsed, err := diagnosticRun(ctx, 20*time.Second, "whois", target)
	if len(out) > maxProbeOutput {
		out = out[:maxProbeOutput] + "\n… (truncated)"
	}
	res.Output, res.Duration = out, elapsed
	ip := net.ParseIP(strings.TrimSpace(target)) != nil
	res.Limitations = append(res.Limitations, "Registries publish different fields and many redact registrant contacts; an absent field is what this answer lacks, not proof the data does not exist.")
	if source := whoisSource(out); source != "" {
		res.fact("Answered by", source, BasisRegistry)
	}
	trimmed := strings.TrimSpace(out)
	values := normaliseWhois(out, ip)
	present := 0
	for _, v := range values {
		if v.State == "present" {
			present++
		}
	}
	switch {
	case whoisNoMatch.MatchString(out) && present <= 1:
		res.OK, res.Verdict = true, ProbeOK
		res.fact("Lookup outcome", "the registry answered: no matching registration", BasisRegistry)
		res.Summary = "The registry has no record of " + target + " (unregistered, or held by a registry this lookup did not reach)."
		res.Records = append(res.Records, "no registration")
		return res, nil
	case (trimmed == "" || present == 0) && (err != nil || whoisFailure.MatchString(out)):
		res.OK, res.Verdict = false, ProbeFailed
		reason := ""
		if m := whoisFailure.FindString(out); m != "" {
			reason = m
		} else if err != nil {
			reason = err.Error()
			res.Error = reason
		}
		res.fact("Lookup outcome", "lookup failed: "+nonEmptyOr(reason, "no answer"), BasisObserved)
		res.Summary = "The whois lookup failed (" + nonEmptyOr(reason, "no answer") + "); registration data is unknown, not absent."
		if res.Error == "" {
			res.Error = "whois lookup failed: " + nonEmptyOr(reason, "no answer")
		}
		return res, nil
	case present == 0:
		res.OK, res.Verdict = true, ProbeUnknown
		res.fact("Lookup outcome", "the server answered in a format with none of the known fields", BasisObserved)
		res.Summary = "An answer arrived, but none of its fields could be normalised; read the raw evidence."
		return res, nil
	}
	table := ProbeTable{ID: "fields", Title: "Registration", Columns: []string{"Field", "Value", "State"}}
	counts := map[string]int{}
	for _, v := range values {
		counts[v.State]++
		table.Rows = append(table.Rows, []string{v.Label, v.Value, v.State})
		if v.State == "present" && v.Label != "Status" && v.Label != "Updated" {
			res.Records = append(res.Records, v.Label+": "+v.Value)
		}
	}
	res.Tables = append(res.Tables, table)
	res.fact("Lookup outcome", fmt.Sprintf("%d fields present, %d redacted, %d absent from this answer", counts["present"], counts["redacted"], counts["absent"]), BasisRegistry)
	res.metric("present", "Fields present", float64(counts["present"]), "")
	res.metric("redacted", "Fields redacted", float64(counts["redacted"]), "")
	res.OK, res.Verdict = true, ProbeOK
	res.Error = ""
	res.Summary = "Registration found: " + strconv.Itoa(counts["present"]) + " fields present"
	if counts["redacted"] > 0 {
		res.Summary += ", " + strconv.Itoa(counts["redacted"]) + " redacted by the registry"
	}
	res.Summary += "."
	for _, v := range values {
		if v.Label == "Expires" && v.State == "present" {
			if when, ok := parseWhoisDate(v.Value); ok {
				days := int(time.Until(when).Hours() / 24)
				res.metric("days_to_expiry", "Days until the registration expires", float64(days), "days")
				if days < 30 {
					res.finding("registration-expiring", "warning", fmt.Sprintf("The registration expires in %d days", days), "Renew it with the registrar before it lapses.", "Domain registrant")
					res.Verdict = ProbeFindings
				}
			}
		}
	}
	return res, nil
}

func parseWhoisDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(strings.Split(value, ",")[0])
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05.0Z", "2006-01-02 15:04:05", "2006-01-02", "02-Jan-2006", "2006.01.02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
