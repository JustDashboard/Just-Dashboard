package netsec

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const cymruAnswer = `Bulk mode; whois.cymru.com [2026-10-09 12:00:00 +0000]
AS      | IP               | BGP Prefix          | CC | Registry | Allocated  | AS Name
15169   | 8.8.8.8          | 8.8.8.0/24          | US | arin     | 2023-12-28 | GOOGLE, US`

func TestASNLookupLabelsSourceAndGeographyUncertainty(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "whois" || !reflect.DeepEqual(args, []string{"-h", "whois.cymru.com", "-v", "8.8.8.8"}) {
			t.Fatalf("argv = %s %v", cmd, args)
		}
		return cymruAnswer, "1s", nil
	}
	res, err := New().ASNLookup(t.Context(), "8.8.8.8")
	if err != nil || !res.OK || res.Verdict != ProbeOK || !reflect.DeepEqual(res.Records, []string{"AS15169 GOOGLE, US", "8.8.8.0/24"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(factValue(res, "Source"), "Team Cymru") || factValue(res, "Registration country") != "US (registry record, not a location)" {
		t.Fatalf("facts = %+v", res.Facts)
	}
	if !strings.Contains(strings.Join(res.Limitations, " "), "not where the server is") {
		t.Fatal("no geography caveat")
	}
	diagnosticRun = func(context.Context, time.Duration, string, ...string) (string, string, error) {
		return cymruAnswer + "\n13335   | 8.8.8.8          | 8.8.8.0/24          | US | arin     | 2023-12-28 | CLOUDFLARENET, US", "1s", nil
	}
	res, _ = New().ASNLookup(t.Context(), "8.8.8.8")
	if res.Verdict != ProbeFindings || !hasFinding(res, "multiple-origins") {
		t.Fatalf("MOAS = %+v", res)
	}
	diagnosticRun = func(context.Context, time.Duration, string, ...string) (string, string, error) {
		return "AS      | IP | BGP Prefix | CC | Registry | Allocated | AS Name\nNA      | 10.0.0.1 | NA | | other | | NA", "1s", nil
	}
	res, _ = New().ASNLookup(t.Context(), "10.0.0.1")
	if res.Verdict != ProbeUnknown || !strings.Contains(res.Summary, "No BGP announcement") {
		t.Fatalf("unannounced = %+v", res)
	}
	diagnosticRun = func(context.Context, time.Duration, string, ...string) (string, string, error) {
		return "", "20s", errors.New("signal: killed")
	}
	res, _ = New().ASNLookup(t.Context(), "8.8.8.8")
	if res.OK || res.Verdict != ProbeFailed {
		t.Fatalf("failed lookup = %+v", res)
	}
}

const whoisDomain = `   Domain Name: EXAMPLE.TEST
   Registry Domain ID: 123
   Registrar WHOIS Server: whois.registrar.test
   Updated Date: 2026-08-14T07:01:31Z
   Creation Date: 1995-08-14T04:00:00Z
   Registry Expiry Date: 2099-08-13T04:00:00Z
   Registrar: Example Registrar, Inc.
   Domain Status: clientDeleteProhibited https://icann.org/epp#clientDeleteProhibited
   Domain Status: clientTransferProhibited https://icann.org/epp#clientTransferProhibited
   Name Server: A.IANA-SERVERS.NET
   Name Server: B.IANA-SERVERS.NET
   DNSSEC: signedDelegation
Registrant Organization: REDACTED FOR PRIVACY
Registrant Country: REDACTED FOR PRIVACY
>>> Last update of whois database: 2026-10-09T12:00:00Z <<<`

const whoisNetwork = `# ARIN WHOIS data and services are subject to the Terms of Use
NetRange:       8.8.8.0 - 8.8.8.255
CIDR:           8.8.8.0/24
NetName:        GOGL
OrgName:        Google LLC
Country:        US
OrgAbuseEmail:  network-abuse@google.com`

func TestWhoisNormalisesAndSeparatesRedactedAbsentAndFailed(t *testing.T) {
	stubLANDiagnostics(t)
	answer := whoisDomain
	var answerErr error
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "whois" || len(args) != 1 {
			t.Fatalf("argv = %s %v", cmd, args)
		}
		return answer, "1s", answerErr
	}
	res, err := New().Whois(t.Context(), "example.test")
	if err != nil || !res.OK || res.Verdict != ProbeOK {
		t.Fatalf("%+v %v", res, err)
	}
	fields := map[string][]string{}
	for _, row := range tableByID(res, "fields").Rows {
		fields[row[0]] = row[1:]
	}
	if fields["Registrar"][1] != "present" || fields["Registrant organisation"][1] != "redacted" || fields["Abuse contact"][1] != "absent" ||
		fields["Name servers"][0] != "a.iana-servers.net, b.iana-servers.net" || !strings.Contains(fields["Status"][0], "clientTransferProhibited") {
		t.Fatalf("fields = %+v", fields)
	}
	if factValue(res, "Answered by") != "registrar server whois.registrar.test" || !strings.Contains(factValue(res, "Lookup outcome"), "2 redacted") {
		t.Fatalf("facts = %+v", res.Facts)
	}
	if _, ok := fields["Address range"]; ok {
		t.Fatal("a domain answer listed address fields")
	}

	answer = whoisNetwork
	res, _ = New().Whois(t.Context(), "8.8.8.8")
	fields = map[string][]string{}
	for _, row := range tableByID(res, "fields").Rows {
		fields[row[0]] = row[1:]
	}
	if fields["CIDR"][0] != "8.8.8.0/24" || fields["Organisation"][0] != "Google LLC" || factValue(res, "Answered by") != "ARIN" {
		t.Fatalf("network = %+v %+v", fields, res.Facts)
	}

	answer = "No match for \"UNREGISTERED-NAME.TEST\".\n>>> Last update of whois database <<<"
	res, _ = New().Whois(t.Context(), "unregistered-name.test")
	if !res.OK || !strings.Contains(factValue(res, "Lookup outcome"), "no matching registration") {
		t.Fatalf("no match = %+v", res)
	}

	answer, answerErr = "connect: Connection refused", errors.New("exit status 1")
	res, _ = New().Whois(t.Context(), "example.test")
	if res.OK || res.Verdict != ProbeFailed || !strings.Contains(res.Summary, "unknown, not absent") {
		t.Fatalf("failure read as absence: %+v", res)
	}
}
