package netsec

import "testing"

func TestProbeRequestClosedVocabularyAndNormalisation(t *testing.T) {
	for _, tc := range []struct {
		request ProbeRequest
		want    ProbeRequest
	}{
		{ProbeRequest{Tool: "dns", Target: " example.test. ", Record: "aaaa", Port: 5, Option: "ignored"}, ProbeRequest{Tool: "dns", Target: "example.test.", Record: "AAAA"}},
		{ProbeRequest{Tool: "dns", Target: "localhost"}, ProbeRequest{Tool: "dns", Target: "localhost", Record: "A"}},
		{ProbeRequest{Tool: "starttls", Target: "mail.test", Option: " IMAP "}, ProbeRequest{Tool: "starttls", Target: "mail.test", Option: "imap", Port: 143}},
		{ProbeRequest{Tool: "ssh", Target: "2001:db8::1"}, ProbeRequest{Tool: "ssh", Target: "2001:db8::1", Port: 22}},
		{ProbeRequest{Tool: "http", Target: "example.test"}, ProbeRequest{Tool: "http", Target: "example.test", Port: 443}},
		{ProbeRequest{Tool: "route", Target: "2001:0db8:0:0::1"}, ProbeRequest{Tool: "route", Target: "2001:db8::1"}},
		{ProbeRequest{Tool: "route", Target: "::ffff:192.0.2.1"}, ProbeRequest{Tool: "route", Target: "192.0.2.1"}},
		{ProbeRequest{Tool: "listeners", Target: "ignored", Port: 5, Record: "ignored", Option: "ignored"}, ProbeRequest{Tool: "listeners"}},
	} {
		got, err := ValidateProbeRequest(tc.request)
		if err != nil || got != tc.want {
			t.Errorf("%+v => %+v, %v; want %+v", tc.request, got, err, tc.want)
		}
	}
	for _, req := range []ProbeRequest{
		{Tool: "bash", Target: "id"}, {Tool: "ping", Target: "--help"}, {Tool: "ping", Target: "x;id"}, {Tool: "http", Target: "https://example.test"},
		{Tool: "dns", Target: "example.test", Record: "AXFR"}, {Tool: "dns", Target: "example.test", Record: "PTR"},
		{Tool: "port", Target: "127.0.0.1"}, {Tool: "port", Target: "127.0.0.1", Port: -1}, {Tool: "port", Target: "127.0.0.1", Port: 65536},
		{Tool: "starttls", Target: "mail.test", Option: "smtp -o shell"}, {Tool: "starttls", Target: "mail.test", Port: 465},
		{Tool: "mx", Target: "2001:db8::1"}, {Tool: "asn", Target: "example.test"}, {Tool: "dnsbl", Target: "example.test"},
		{Tool: "route", Target: "example.test"}, {Tool: "route", Target: "fe80::1%eth0"},
		{Tool: "capture", Target: "lo", Option: "tcp or host example.test"}, {Tool: "capture", Target: "../etc"}, {Tool: "wol", Target: "ff:ff:ff:ff:ff:ff", Option: "lo"},
		{Tool: "wol", Target: "02:00:00:00:00:01", Option: "lo"},
	} {
		if got, err := ValidateProbeRequest(req); err == nil {
			t.Errorf("accepted %+v as %+v", req, got)
		}
	}
	for _, tool := range []string{"ping", "traceroute", "scan", "whois", "dnsauth", "mtu", "tls", "tlssurvey", "httpsec", "siteaudit", "banner", "egress", "neigh", "capabilities"} {
		if _, err := ValidateProbeRequest(ProbeRequest{Tool: tool, Target: "127.0.0.1", Port: 443}); err != nil {
			t.Errorf("closed tool %s rejected: %v", tool, err)
		}
	}
}
