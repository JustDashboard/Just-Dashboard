package netsec

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type mailFixture struct {
	mx      []*net.MX
	mxErr   error
	txt     map[string][]string
	txtErr  map[string]error
	ips     map[string][]string
	smtp    string
	dialErr error
}

func (f mailFixture) deps(t *testing.T) mailDeps {
	return mailDeps{
		lookupMX: func(context.Context, string) ([]*net.MX, error) { return f.mx, f.mxErr },
		lookupTXT: func(_ context.Context, name string) ([]string, error) {
			if err := f.txtErr[name]; err != nil {
				return nil, err
			}
			if v, ok := f.txt[name]; ok {
				return v, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
		},
		lookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			var out []netip.Addr
			for _, a := range f.ips[host] {
				out = append(out, netip.MustParseAddr(a))
			}
			if len(out) == 0 {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			return out, nil
		},
		dial: func(ctx context.Context, address string) (net.Conn, error) {
			if !strings.HasSuffix(address, ":25") {
				t.Errorf("SMTP dialled %s", address)
			}
			if f.dialErr != nil {
				return nil, f.dialErr
			}
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				fmt.Fprintf(server, "%s\r\n", f.smtp)
				_, _ = bufio.NewReader(server).ReadString('\n')
			}()
			return client, nil
		},
	}
}

func goodMail() mailFixture {
	return mailFixture{
		mx:   []*net.MX{{Host: "mx2.example.test.", Pref: 20}, {Host: "mx1.example.test.", Pref: 10}},
		txt:  map[string][]string{"example.test": {"v=spf1 mx -all"}, "_dmarc.example.test": {"v=DMARC1; p=reject; rua=mailto:d@example.test"}},
		ips:  map[string][]string{"mx1.example.test": {"192.0.2.25"}, "mx2.example.test": {"198.51.100.25", "2001:db8::25"}},
		smtp: "220 mx1.example.test ESMTP Postfix",
	}
}

func TestMailPathReportsEveryStageWithinTheEvidence(t *testing.T) {
	res := &ProbeResult{}
	checkMailPath(t.Context(), res, "example.test", goodMail().deps(t))
	for _, id := range []string{"mx", "addresses", "spf", "dmarc", "smtp_connect", "smtp_greeting"} {
		if stageStatus(res, id) != StagePassed {
			t.Fatalf("stage %s = %q: %+v", id, stageStatus(res, id), res.Stages)
		}
	}
	if res.Verdict != ProbeOK || !strings.Contains(res.Summary, "Delivery itself was not tested") || !strings.Contains(factValue(res, "Not checked"), "DKIM") {
		t.Fatalf("good path = %+v", res)
	}
	if table := tableByID(res, "exchangers"); table == nil || table.Rows[0][1] != "mx1.example.test" {
		t.Fatalf("preference order = %+v", table)
	}

	for name, tc := range map[string]struct {
		mutate  func(*mailFixture)
		stage   string
		status  string
		verdict string
		finding string
	}{
		"no SPF":           {func(f *mailFixture) { delete(f.txt, "example.test") }, "spf", StageWarning, ProbeFindings, "spf-none"},
		"two SPF":          {func(f *mailFixture) { f.txt["example.test"] = []string{"v=spf1 mx -all", "v=spf1 a ~all"} }, "spf", StageWarning, ProbeFindings, "spf-multiple"},
		"SPF lookup fails": {func(f *mailFixture) { f.txtErr = map[string]error{"example.test": errors.New("server misbehaving")} }, "spf", StageUnknown, ProbeUnknown, ""},
		"DMARC monitoring": {func(f *mailFixture) { f.txt["_dmarc.example.test"] = []string{"v=DMARC1; p=none"} }, "dmarc", StagePassed, ProbeFindings, "dmarc-monitor"},
		"port 25 blocked":  {func(f *mailFixture) { f.dialErr = &net.OpError{Op: "dial", Err: timeoutError{}} }, "smtp_connect", StageUnknown, ProbeUnknown, ""},
		"bad greeting":     {func(f *mailFixture) { f.smtp = "554 no service" }, "smtp_greeting", StageWarning, ProbeFindings, "smtp-greeting"},
		"no MX":            {func(f *mailFixture) { f.mx = nil; f.ips["example.test"] = []string{"192.0.2.80"} }, "mx", StageWarning, ProbeFindings, "no-mx"},
	} {
		f := goodMail()
		tc.mutate(&f)
		res := &ProbeResult{}
		checkMailPath(t.Context(), res, "example.test", f.deps(t))
		if stageStatus(res, tc.stage) != tc.status || res.Verdict != tc.verdict || (tc.finding != "" && !hasFinding(res, tc.finding)) {
			t.Errorf("%s: stage=%s verdict=%s findings=%+v", name, stageStatus(res, tc.stage), res.Verdict, res.Findings)
		}
	}

	f := goodMail()
	f.mxErr = errors.New("server misbehaving")
	res = &ProbeResult{}
	checkMailPath(t.Context(), res, "example.test", f.deps(t))
	if res.OK || res.Verdict != ProbeFailed || stageStatus(res, "mx") != StageFailed || !strings.Contains(res.Error, "MX lookup failed") {
		t.Fatalf("a failed lookup read as absent: %+v", res)
	}

	f = goodMail()
	f.mx = []*net.MX{{Host: ".", Pref: 0}}
	res = &ProbeResult{}
	checkMailPath(t.Context(), res, "example.test", f.deps(t))
	if !strings.Contains(res.Stages[0].Detail, "null MX") || stageStatus(res, "smtp_connect") != StageSkipped {
		t.Fatalf("null MX = %+v", res.Stages)
	}
}

// startSTARTTLS serves one protocol conversation; refuse makes the server
// decline the upgrade, hide drops the capability advertisement.
func startSTARTTLS(t *testing.T, protocol string, refuse, hide bool) int {
	t.Helper()
	cert := selfSignedCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		greet := map[string]string{"smtp": "220-fake.test ESMTP\r\n220 ready", "imap": "* OK fake IMAP ready", "pop3": "+OK fake POP3", "ftp": "220 fake FTP"}[protocol]
		fmt.Fprintf(c, "%s\r\n", greet)
		for {
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			upgrade := false
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				if hide {
					fmt.Fprintf(c, "250-fake.test\r\n250 SIZE 1000\r\n")
				} else {
					fmt.Fprintf(c, "250-fake.test\r\n250 STARTTLS\r\n")
				}
			case strings.HasPrefix(cmd, "A000 CAPABILITY"):
				fmt.Fprintf(c, "* CAPABILITY IMAP4rev1 STARTTLS\r\na000 OK done\r\n")
			case cmd == "CAPA":
				fmt.Fprintf(c, "+OK\r\nUSER\r\nSTLS\r\n.\r\n")
			case cmd == "FEAT":
				fmt.Fprintf(c, "211-Features:\r\n AUTH TLS\r\n211 End\r\n")
			case cmd == "STARTTLS" || cmd == "A001 STARTTLS" || cmd == "STLS" || cmd == "AUTH TLS":
				if refuse {
					fmt.Fprintf(c, "%s\r\n", map[string]string{"smtp": "454 TLS not available", "imap": "a001 NO no", "pop3": "-ERR no", "ftp": "500 no"}[protocol])
					continue
				}
				fmt.Fprintf(c, "%s\r\n", map[string]string{"smtp": "220 go ahead", "imap": "a001 OK begin", "pop3": "+OK begin", "ftp": "234 AUTH TLS ok"}[protocol])
				upgrade = true
			}
			if upgrade {
				tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
				_ = tc.Handshake()
				tc.Close()
				return
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSTARTTLSReportsProtocolSpecificStages(t *testing.T) {
	for _, protocol := range []string{"smtp", "imap", "pop3", "ftp"} {
		port := startSTARTTLS(t, protocol, false, false)
		res, err := New().STARTTLSCheck(t.Context(), "127.0.0.1", port, protocol)
		if err != nil || !res.OK {
			t.Fatalf("%s: %+v %v", protocol, res, err)
		}
		for _, id := range []string{"connect", "greeting", "capabilities", "upgrade", "handshake"} {
			if stageStatus(res, id) != StagePassed {
				t.Fatalf("%s stage %s = %q: %+v", protocol, id, stageStatus(res, id), res.Stages)
			}
		}
		if stageStatus(res, "certificate") != StageWarning || res.Verdict != ProbeFindings {
			t.Fatalf("%s: a self-signed certificate passed trust: %+v", protocol, res.Stages)
		}
	}
	port := startSTARTTLS(t, "smtp", true, false)
	res, _ := New().STARTTLSCheck(t.Context(), "127.0.0.1", port, "smtp")
	if res.OK || stageStatus(res, "upgrade") != StageFailed || stageStatus(res, "handshake") != StageSkipped || stageStatus(res, "certificate") != StageSkipped || !hasFinding(res, "plaintext") {
		t.Fatalf("refused upgrade = %+v", res)
	}
	port = startSTARTTLS(t, "smtp", false, true)
	res, _ = New().STARTTLSCheck(t.Context(), "127.0.0.1", port, "smtp")
	if !res.OK || stageStatus(res, "capabilities") != StageWarning || !hasFinding(res, "not-advertised") {
		t.Fatalf("hidden capability = %+v", res)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	res, _ = New().STARTTLSCheck(t.Context(), "127.0.0.1", closed, "imap")
	if res.OK || stageStatus(res, "connect") != StageFailed || stageStatus(res, "greeting") != StageSkipped || !strings.Contains(res.Summary, "tcp connect") {
		t.Fatalf("closed = %+v", res)
	}
}

func TestDNSBLSeparatesQueryFailuresFromNotListed(t *testing.T) {
	for _, tc := range []struct {
		zone   string
		addrs  []string
		err    error
		result string
	}{
		{"zen.spamhaus.org", nil, &net.DNSError{IsNotFound: true}, "not listed"},
		{"zen.spamhaus.org", []string{"127.0.0.2"}, nil, "listed"},
		{"zen.spamhaus.org", []string{"127.255.255.254"}, nil, "query refused"},
		{"bl.spamcop.net", []string{"192.0.2.1"}, nil, "unexpected answer"},
		{"bl.spamcop.net", nil, errors.New("i/o timeout"), "query failed"},
	} {
		var addrs []netip.Addr
		for _, a := range tc.addrs {
			addrs = append(addrs, netip.MustParseAddr(a))
		}
		if got, _, _ := dnsblAnswer(tc.zone, addrs, tc.err); got != tc.result {
			t.Errorf("%s %v %v => %s, want %s", tc.zone, tc.addrs, tc.err, got, tc.result)
		}
	}
	if _, _, detail := dnsblAnswer("zen.spamhaus.org", []netip.Addr{netip.MustParseAddr("127.255.255.254")}, nil); !strings.Contains(detail, "public or open resolvers") {
		t.Fatalf("refusal detail = %q", detail)
	}

	oldLookup, oldTXT, oldNow := dnsblLookup, dnsblTXT, dnsblNow
	t.Cleanup(func() { dnsblLookup, dnsblTXT, dnsblNow = oldLookup, oldTXT, oldNow })
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	dnsblNow = func() time.Time { return at }
	dnsblTXT = func(context.Context, string) ([]string, error) { return []string{"Listed by test"}, nil }
	dnsblLookup = func(_ context.Context, name string) ([]netip.Addr, error) {
		if !strings.HasPrefix(name, "9.2.0.192.") {
			t.Errorf("query %s", name)
		}
		switch {
		case strings.HasSuffix(name, "spamhaus.org"):
			return []netip.Addr{netip.MustParseAddr("127.255.255.254")}, nil
		case strings.HasSuffix(name, "spamcop.net"):
			return nil, errors.New("i/o timeout")
		}
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	res, err := New().DNSBLCheck(t.Context(), "192.0.2.9")
	if err != nil || res.Verdict != ProbeUnknown || len(res.Records) != 0 || !strings.Contains(res.Summary, "2 could not be checked") {
		t.Fatalf("unanswered lists read as clean: %+v %v", res, err)
	}
	table := tableByID(res, "lists")
	if table.Rows[0][1] != "query refused" || table.Rows[1][1] != "query failed" || table.Rows[0][4] != at.Format(time.RFC3339) {
		t.Fatalf("lists = %+v", table.Rows)
	}
	dnsblLookup = func(_ context.Context, name string) ([]netip.Addr, error) {
		if strings.HasSuffix(name, "spamcop.net") {
			return []netip.Addr{netip.MustParseAddr("127.0.0.2")}, nil
		}
		return nil, &net.DNSError{IsNotFound: true}
	}
	res, _ = New().DNSBLCheck(t.Context(), "192.0.2.9")
	if res.Verdict != ProbeFindings || len(res.Records) != 1 || !hasFinding(res, "listed-bl.spamcop.net") || !strings.Contains(tableByID(res, "lists").Rows[1][3], "Listed by test") {
		t.Fatalf("listing = %+v", res)
	}
}
