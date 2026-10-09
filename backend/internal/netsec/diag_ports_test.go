package netsec

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBannerIdentificationLabelsConfidence(t *testing.T) {
	mysql := append([]byte{0x4a, 0, 0, 0, 10}, []byte("10.11.6-MariaDB-0ubuntu0.24.04.1\x00rest")...)
	for _, tc := range []struct {
		port                                   int
		banner                                 []byte
		protocol, product, version, confidence string
	}{
		{22, []byte("SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5\r\n"), "SSH 2.0", "OpenSSH", "9.6p1", "high"},
		{2222, []byte("SSH-2.0-dropbear\r\n"), "SSH 2.0", "dropbear", "", "high"},
		{25, []byte("220 mail.example.test ESMTP Postfix (Ubuntu)\r\n"), "SMTP", "Postfix", "", "high"},
		{21, []byte("220 (vsFTPd 3.0.5)\r\n"), "FTP", "vsftpd", "3.0.5", "high"},
		{21, []byte("220 Welcome\r\n"), "FTP (by port)", "", "", "medium"},
		{2121, []byte("220 ProFTPD Server ready\r\n"), "FTP", "ProFTPD", "", "high"},
		{110, []byte("+OK Dovecot (Ubuntu) ready.\r\n"), "POP3", "Dovecot", "", "high"},
		{143, []byte("* OK [CAPABILITY IMAP4rev1 STARTTLS AUTH=PLAIN] Dovecot ready.\r\n"), "IMAP", "Dovecot", "", "high"},
		{3306, mysql, "MySQL protocol 10", "MariaDB", "10.11.6-MariaDB-0ubuntu0.24.04.1", "high"},
		{5900, []byte("RFB 003.008\n"), "VNC (RFB 3.8)", "", "", "high"},
		{2525, []byte("220 relay ready\r\n"), "SMTP (by port)", "", "", "medium"},
		{9999, []byte("220 something\r\n"), "SMTP or FTP", "", "", "low"},
		{6379, []byte("garbage"), "Redis (by port only)", "", "", "low"},
		{40000, []byte("garbage"), "unidentified", "", "", "none"},
	} {
		id := identifyBanner(tc.port, tc.banner)
		if id.Protocol != tc.protocol || id.Product != tc.product || id.Version != tc.version || id.Confidence != tc.confidence {
			t.Errorf("%d %q => %+v", tc.port, tc.banner, id)
		}
	}
	id := identifyBanner(143, []byte("* OK [CAPABILITY IMAP4rev1 STARTTLS AUTH=PLAIN] Dovecot ready.\r\n"))
	if len(id.Details) != 1 || id.Details[0].Value != "IMAP4rev1 STARTTLS AUTH=PLAIN" {
		t.Fatalf("capabilities = %+v", id.Details)
	}
}

func TestBannerGrabReportsSelfReportedSoftware(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			defer c.Close()
			fmt.Fprintf(c, "SSH-2.0-OpenSSH_9.6p1 Debian-4\r\n")
			time.Sleep(time.Second)
		}
	}()
	res, err := New().BannerGrab(t.Context(), "127.0.0.1", ln.Addr().(*net.TCPAddr).Port)
	if err != nil || !res.OK || res.Verdict != ProbeOK {
		t.Fatalf("%+v %v", res, err)
	}
	if factValue(res, "Software (self-reported)") != "OpenSSH 9.6p1" || !strings.HasPrefix(factValue(res, "Identification confidence"), "high") {
		t.Fatalf("facts = %+v", res.Facts)
	}
	if !strings.Contains(strings.Join(res.Limitations, " "), "forged") {
		t.Fatal("no self-reporting caveat")
	}
}

func TestPortScanPinsOneAddressAndReportsExactCoverage(t *testing.T) {
	ports := scanPorts()
	old := scanDial
	t.Cleanup(func() { scanDial = old })
	var mu = make(chan struct{}, 1)
	dialed := map[string]bool{}
	scanDial = func(_ context.Context, address string) (net.Conn, error) {
		mu <- struct{}{}
		dialed[address] = true
		<-mu
		host, port, _ := net.SplitHostPort(address)
		if host != "192.0.2.5" {
			t.Errorf("scan left the pinned address: %s", address)
		}
		switch port {
		case "22":
			a, b := net.Pipe()
			b.Close()
			return a, nil
		case "3306":
			a, b := net.Pipe()
			b.Close()
			return a, nil
		case "80":
			return nil, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
		}
		return nil, &net.OpError{Op: "dial", Err: timeoutError{}}
	}
	addr := netip.MustParseAddr("192.0.2.5")
	res := &ProbeResult{Tool: "scan"}
	interpretScan(res, addr, []netip.Addr{netip.MustParseAddr("2001:db8::5")}, ports, scanPortsAt(t.Context(), addr, ports))
	if len(dialed) != len(ports) {
		t.Fatalf("dialed %d of %d ports", len(dialed), len(ports))
	}
	table := tableByID(res, "ports")
	if table == nil || len(table.Rows) != len(ports) {
		t.Fatalf("every tried port is not listed: %+v", table)
	}
	if open, _ := metricValue(res, "open"); open != 2 {
		t.Fatalf("open = %v", open)
	}
	if closed, _ := metricValue(res, "closed"); closed != 1 {
		t.Fatalf("closed = %v", closed)
	}
	if !strings.Contains(factValue(res, "Coverage"), fmt.Sprintf("exactly %d TCP ports", len(ports))) || factValue(res, "Not covered") == "" || factValue(res, "Other addresses (not scanned)") != "2001:db8::5" {
		t.Fatalf("coverage = %+v", res.Facts)
	}
	if res.Verdict != ProbeFindings || !hasFinding(res, "open-3306") {
		t.Fatalf("an open database drew no finding: %+v", res.Findings)
	}
	if !reflect.DeepEqual(res.Records, []string{"22 SSH", "3306 MySQL / MariaDB"}) && !strings.HasPrefix(strings.Join(res.Records, ","), "22 ") {
		t.Fatalf("records = %v", res.Records)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

const fpA = "SHA256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const fpB = "SHA256:" + "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
const fpC = "SHA256:" + "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"

func TestSSHTrustValidationAndComparison(t *testing.T) {
	target, keys, err := ValidateSSHTrust("Host.Example.test.", 0, []SSHTrustedKey{{Type: "ssh-rsa", Fingerprint: fpB}, {Type: "ssh-ed25519", Fingerprint: fpA}})
	if err != nil || target != "host.example.test:22" || keys[0].Type != "ssh-ed25519" {
		t.Fatalf("%s %+v %v", target, keys, err)
	}
	for _, bad := range [][]SSHTrustedKey{nil, {{Type: "ssh-ed25519", Fingerprint: "MD5:aa:bb"}}, {{Type: "rot13", Fingerprint: fpA}}, {{Type: "ssh-rsa", Fingerprint: fpA}, {Type: "ssh-rsa", Fingerprint: fpB}}} {
		if _, _, err := ValidateSSHTrust("host.example.test", 22, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if got := SSHTrustTarget("2001:0db8::1", 2222); got != "[2001:db8::1]:2222" {
		t.Fatalf("ipv6 key = %s", got)
	}
	scan := func() *ProbeResult {
		return &ProbeResult{Tool: "ssh", Target: "host.example.test:22", OK: true, Records: []string{"ssh-ed25519 " + fpA, "ssh-rsa " + fpB}}
	}
	res := scan()
	CompareSSHKeys(res, nil)
	if res.Verdict != ProbeUnknown || !strings.Contains(res.Summary, "does not authenticate") {
		t.Fatalf("untrusted = %+v", res)
	}
	saved := &SSHTrust{Target: "host.example.test:22", Keys: []SSHTrustedKey{{Type: "ssh-ed25519", Fingerprint: fpA}, {Type: "ecdsa-sha2-nistp256", Fingerprint: fpC}}, Source: "entered", SavedAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), SavedBy: "operator"}
	res = scan()
	CompareSSHKeys(res, saved)
	table := tableByID(res, "trust")
	if res.Verdict != ProbeOK || table == nil || table.Rows[0][3] != "matches" || table.Rows[1][3] != "no saved key of this type" || table.Rows[2][3] != "saved but not offered" {
		t.Fatalf("match = %+v %+v", res, table)
	}
	if !strings.Contains(factValue(res, "Saved trust"), "entered by hand on 2026-10-01 09:00 UTC by operator") {
		t.Fatalf("trust fact = %q", factValue(res, "Saved trust"))
	}
	saved.Keys[0].Fingerprint = fpC
	res = scan()
	CompareSSHKeys(res, saved)
	if res.Verdict != ProbeFindings || !hasFinding(res, "key-changed-ssh-ed25519") || res.Findings[0].Level != "critical" {
		t.Fatalf("changed key = %+v", res)
	}
}

func TestSSHScanFingerprintsOfferedKeys(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "ssh-keyscan" || !reflect.DeepEqual(args, []string{"-T", "5", "-p", "2222", "192.0.2.4"}) {
			t.Fatalf("argv = %s %v", cmd, args)
		}
		return "# 192.0.2.4:2222 SSH-2.0-OpenSSH_9.6\n[192.0.2.4]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl", "1s", nil
	}
	res, err := New().SSHScan(t.Context(), "192.0.2.4", 2222)
	if err != nil || !res.OK || len(ObservedSSHKeys(res)) != 1 || ObservedSSHKeys(res)[0].Type != "ssh-ed25519" {
		t.Fatalf("%+v %v", res, err)
	}
}
