package netx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

// The resolver maturity acceptance runs actual systemd-resolved in a child
// with new network and mount namespaces, a private /etc, /run/systemd and
// D-Bus, and a signed DNS hierarchy of its own: a root anchored only inside
// the namespace, example under it and corp.example under that. SetDNS writes
// the namespace's drop-in and "restarts" the namespace's resolved; nothing
// reaches the host's systemctl, resolver or files, including on a failure.
func TestDNSResolverMaturityNativeDisposable(t *testing.T) {
	if os.Getenv("JD_DNS_MATURITY_FIXTURE") == "1" {
		dnsMaturityFixture(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("native resolver fixture requires disposable namespace privileges")
	}
	for _, tool := range []string{"busctl", "dbus-daemon", "ip", "resolvectl", "getent"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("native fixture tool unavailable: %s", tool)
		}
	}
	if _, err := os.Stat("/usr/lib/systemd/systemd-resolved"); err != nil {
		t.Skip("native systemd-resolved executable unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDNSResolverMaturityNativeDisposable$", "-test.v")
	cmd.Env = append(os.Environ(), "JD_DNS_MATURITY_FIXTURE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWNET | unix.CLONE_NEWNS}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native resolver maturity fixture: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// zoneKeys are the signing keys of the fixture hierarchy, one combined
// key-signing key per zone.
type zoneKeys struct {
	root, example, corp ed25519.PrivateKey
}

func fixtureWireName(name string) []byte {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	var out []byte
	if name != "" {
		for _, label := range strings.Split(name, ".") {
			out = append(out, byte(len(label)))
			out = append(out, label...)
		}
	}
	return append(out, 0)
}

func fixtureRR(name string, kind uint16, rdata []byte) []byte {
	rr := fixtureWireName(name)
	rr = binary.BigEndian.AppendUint16(rr, kind)
	rr = binary.BigEndian.AppendUint16(rr, 1)
	rr = binary.BigEndian.AppendUint32(rr, 60)
	rr = binary.BigEndian.AppendUint16(rr, uint16(len(rdata)))
	return append(rr, rdata...)
}

func fixtureDNSKEY(key ed25519.PrivateKey) []byte {
	return append([]byte{1, 1, 3, 15}, key.Public().(ed25519.PublicKey)...)
}

func fixtureDS(zone string, key ed25519.PrivateKey) []byte {
	rdata := fixtureDNSKEY(key)
	digest, _ := dnsDSDigest(zone, rdata, 2)
	out := binary.BigEndian.AppendUint16(nil, dnsKeyTag(rdata))
	return append(append(out, 15, 2), digest...)
}

// fixtureSign returns the RRSIG record over one single-record RRset.
func fixtureSign(owner string, kind uint16, rdata []byte, signer string, key ed25519.PrivateKey) []byte {
	labels := 0
	if trimmed := strings.TrimSuffix(owner, "."); trimmed != "" {
		labels = len(strings.Split(trimmed, "."))
	}
	sig := binary.BigEndian.AppendUint16(nil, kind)
	sig = append(sig, 15, byte(labels))
	sig = binary.BigEndian.AppendUint32(sig, 60)
	sig = binary.BigEndian.AppendUint32(sig, uint32(time.Now().Add(time.Hour).Unix()))
	sig = binary.BigEndian.AppendUint32(sig, uint32(time.Now().Add(-time.Minute).Unix()))
	sig = binary.BigEndian.AppendUint16(sig, dnsKeyTag(fixtureDNSKEY(key)))
	sig = append(sig, fixtureWireName(signer)...)
	signature := ed25519.Sign(key, append(append([]byte{}, sig...), fixtureRR(owner, kind, rdata)...))
	return fixtureRR(owner, 46, append(sig, signature...))
}

func fixtureBitmap(types []uint16) []byte {
	bits := make([]byte, 32)
	highest := 0
	for _, t := range types {
		bits[t/8] |= 0x80 >> (t % 8)
		if int(t/8) > highest {
			highest = int(t / 8)
		}
	}
	return append([]byte{0, byte(highest + 1)}, bits[:highest+1]...)
}

// zoneAnswer is the authoritative answer of the fixture hierarchy, signed as
// its zone signs it. ask counts every question by name, so a test can assert
// which server never heard a private name.
func zoneAnswer(req []byte, keys zoneKeys, ask func(string, uint16)) []byte {
	name, qtype, qend := parseQuestion(req)
	if ask != nil {
		ask(name, qtype)
	}
	type node struct {
		types []uint16
		zone  string
	}
	const (
		tA, tNS, tSOA, tAAAA, tDS, tRRSIG, tNSEC, tDNSKEY = 1, 2, 6, 28, 43, 46, 47, 48
	)
	apex := []uint16{tNS, tSOA, tRRSIG, tNSEC, tDNSKEY}
	host := []uint16{tA, tAAAA, tRRSIG, tNSEC}
	nodes := map[string]node{
		"":                    {apex, ""},
		"example":             {apex, "example"},
		"corp.example":        {apex, "corp.example"},
		"www.example":         {host, "example"},
		"secret.corp.example": {host, "corp.example"},
	}
	key := map[string]ed25519.PrivateKey{"": keys.root, "example": keys.example, "corp.example": keys.corp}
	n, exists := nodes[name]
	zone := n.zone
	if qtype == tDS && (name == "example" || name == "corp.example") {
		// A DS set belongs to the parent side of the cut.
		zone = map[string]string{"example": "", "corp.example": "example"}[name]
		n = node{[]uint16{tNS, tDS, tRRSIG, tNSEC}, zone}
	}
	if !exists {
		return buildResponse(req, qend, qtype, dnsBehavior{rcode: 5})
	}
	rdata := func(kind uint16) ([]byte, bool) {
		switch kind {
		case tA:
			return []byte{192, 0, 2, 7}, true
		case tAAAA:
			return net.ParseIP("2001:db8::7").To16(), true
		case tDNSKEY:
			return fixtureDNSKEY(key[name]), true
		case tDS:
			return fixtureDS(name, key[name]), true
		case tNS:
			return fixtureWireName("ns." + strings.TrimPrefix(name+".", ".") + "fixture"), true
		case tSOA:
			out := append(fixtureWireName("ns.fixture"), fixtureWireName("hostmaster.fixture")...)
			for _, v := range []uint32{1, 3600, 600, 86400, 60} {
				out = binary.BigEndian.AppendUint32(out, v)
			}
			return out, true
		}
		return nil, false
	}
	signer := zone
	resp := make([]byte, 12, 1024)
	copy(resp, req[:2])
	binary.BigEndian.PutUint16(resp[2:], 0x8580)
	binary.BigEndian.PutUint16(resp[4:], 1)
	resp = append(resp, req[12:qend]...)
	has := false
	for _, t := range n.types {
		has = has || t == qtype
	}
	if has {
		if body, ok := rdata(qtype); ok {
			binary.BigEndian.PutUint16(resp[6:], 2)
			resp = append(resp, fixtureRR(name, qtype, body)...)
			return append(resp, fixtureSign(name, qtype, body, signer, key[zone])...)
		}
	}
	// NODATA: the zone's SOA and the name's NSEC, each signed.
	soa, _ := rdata(tSOA)
	nsec := append(fixtureWireName("zz."+strings.TrimPrefix(zone+".", ".")+"invalid"), fixtureBitmap(n.types)...)
	binary.BigEndian.PutUint16(resp[8:], 4)
	resp = append(resp, fixtureRR(zone, tSOA, soa)...)
	resp = append(resp, fixtureSign(zone, tSOA, soa, signer, key[zone])...)
	resp = append(resp, fixtureRR(name, tNSEC, nsec)...)
	return append(resp, fixtureSign(name, tNSEC, nsec, signer, key[zone])...)
}

// servePlainDNS answers classic DNS over UDP and TCP on address.
func servePlainDNS(t *testing.T, address string, answer func([]byte) []byte) {
	t.Helper()
	conn, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); listener.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo(answer(append([]byte{}, buf[:n]...)), from)
		}
	}()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(20 * time.Second))
				for {
					var size [2]byte
					if _, err := io.ReadFull(c, size[:]); err != nil {
						return
					}
					body := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					out := answer(body)
					if _, err := c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(out))), out...)); err != nil {
						return
					}
				}
			}()
		}
	}()
}

type questionLog struct {
	mu    sync.Mutex
	names map[string][]string
}

func (q *questionLog) record(server string) func(string, uint16) {
	return func(name string, kind uint16) {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.names[server] = append(q.names[server], name+"/"+strconv.Itoa(int(kind)))
	}
}

func (q *questionLog) under(server, suffix string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []string{}
	for _, entry := range q.names[server] {
		name := strings.SplitN(entry, "/", 2)[0]
		if underDomain(name, suffix) {
			out = append(out, entry)
		}
	}
	return out
}

func (q *questionLog) count(server string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.names[server])
}

func dnsMaturityFixture(t *testing.T) {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	etc, runDir := filepath.Join(dir, "etc"), filepath.Join(dir, "run")
	dbus := filepath.Join(runDir, "fixturebus")
	for _, path := range []string{filepath.Join(etc, "systemd", "resolved.conf.d"), filepath.Join(etc, "dnssec-trust-anchors.d"), filepath.Join(etc, "ssl", "certs"), filepath.Join(runDir, "resolve"), dbus} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"passwd", "group", "nsswitch.conf", "machine-id", "host.conf"} {
		if raw, err := os.ReadFile("/etc/" + name); err == nil {
			if err = os.WriteFile(filepath.Join(etc, name), raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	hosts := "127.0.0.1 localhost\n203.0.113.80 shadowed.lan\n# BEGIN Just Dashboard\n192.0.2.80 shadowed.lan\n192.0.2.81 nas.lan\n# END Just Dashboard\n"
	if err := os.WriteFile(filepath.Join(etc, "hosts"), []byte(hosts), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/run/systemd/resolve/stub-resolv.conf", filepath.Join(etc, "resolv.conf")); err != nil {
		t.Fatal(err)
	}
	var keys zoneKeys
	for _, k := range []*ed25519.PrivateKey{&keys.root, &keys.example, &keys.corp} {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		*k = private
	}
	// The namespace's own root replaces resolved's built-in anchor, in the DS
	// form the built-in anchors take, so the root's keys come from the network.
	rootDS := fixtureDS(".", keys.root)
	anchor := fmt.Sprintf(". IN DS %d 15 2 %X\n", binary.BigEndian.Uint16(rootDS), rootDS[4:])
	if err := os.WriteFile(filepath.Join(etc, "dnssec-trust-anchors.d", "fixture.positive"), []byte(anchor), 0644); err != nil {
		t.Fatal(err)
	}
	certificate, ca := dnsEvidenceCertificate(t)
	if err := os.WriteFile(filepath.Join(etc, "ssl", "certs", "ca-certificates.crt"), ca, 0644); err != nil {
		t.Fatal(err)
	}
	// The base configuration carries a fallback of its own, so clearing it in
	// the drop-in is observable in resolved's read-back.
	conf := "[Resolve]\nLLMNR=no\nMulticastDNS=no\nDNSStubListener=yes\nFallbackDNS=192.0.2.250\n"
	if err := os.WriteFile(filepath.Join(etc, "systemd", "resolved.conf"), []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}
	for _, m := range [][2]string{{etc, "/etc"}, {runDir, "/run/systemd"}} {
		if err := unix.Mount(m[0], m[1], "", unix.MS_BIND, ""); err != nil {
			t.Fatal(err)
		}
	}
	account, err := user.Lookup("systemd-resolve")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	if err := os.Chown("/run/systemd/resolve", uid, gid); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	execute := func(ctx context.Context, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		var out dnsBoundedBuffer
		cmd.Stdout, cmd.Stderr = &out, &out
		_, err := hostexec.RunGroup(ctx, cmd, 100*time.Millisecond)
		if out.exceeded {
			return "", fmt.Errorf("fixture output exceeded bound")
		}
		if err != nil {
			return out.String(), fmt.Errorf("%s", strings.TrimSpace(out.String()))
		}
		return out.String(), nil
	}
	must := func(args ...string) {
		t.Helper()
		if out, err := execute(ctx, args[0], args[1:]...); err != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
	}
	must("ip", "link", "set", "lo", "up")
	for _, link := range []struct{ name string }{{"dnspub0"}, {"dnspriv0"}} {
		must("ip", "link", "add", link.name, "type", "dummy")
		must("ip", "link", "set", link.name, "up")
	}
	must("ip", "address", "add", "203.0.113.53/24", "dev", "dnspub0")
	must("ip", "address", "add", "192.0.2.53/24", "dev", "dnspriv0")
	must("ip", "address", "add", "10.53.0.53/24", "dev", "dnspriv0")
	address := "unix:path=/run/systemd/fixturebus/bus"
	busPath := filepath.Join(dbus, "config")
	if err := os.WriteFile(busPath, []byte(`<busconfig><type>system</type><listen>`+address+`</listen><policy context="default"><allow user="*"/><allow own="*"/><allow send_destination="*"/><allow receive_sender="*"/></policy></busconfig>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", address)
	t.Setenv("SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt")
	start := func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(name, args...)
		cmd.Env = os.Environ()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, err := os.OpenFile(filepath.Join(dir, filepath.Base(name)+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); _ = cmd.Wait(); _ = log.Close() })
		return cmd
	}
	start("dbus-daemon", "--config-file="+busPath, "--nofork", "--nopidfile")

	log := &questionLog{names: map[string][]string{}}
	// A panic in a server goroutine would end the child before its cleanups
	// stop the namespace's daemons; answer SERVFAIL and record it instead.
	var answerFaults []string
	var faultMu sync.Mutex
	answer := func(server string) func([]byte) []byte {
		return func(req []byte) (out []byte) {
			defer func() {
				if r := recover(); r != nil {
					faultMu.Lock()
					answerFaults = append(answerFaults, fmt.Sprint(r))
					faultMu.Unlock()
					_, kind, qend := parseQuestion(req)
					out = buildResponse(req, qend, kind, dnsBehavior{rcode: 2})
				}
			}()
			return zoneAnswer(req, keys, log.record(server))
		}
	}
	t.Cleanup(func() {
		faultMu.Lock()
		defer faultMu.Unlock()
		if len(answerFaults) > 0 {
			t.Errorf("fixture answer faults: %v", answerFaults)
		}
	})
	// The global DoT server is on a public-looking address: the private-name
	// guard judges it as a public resolver.
	dnsEvidenceServeTLS(t, "203.0.113.53:853", certificate, answer("public"))
	dnsEvidenceServeTLS(t, "127.0.0.2:853", certificate, answer("private"))
	// Classic only: TLS to it is refused, so opportunistic DoT falls back.
	servePlainDNS(t, "127.0.0.4:53", answer("plain"))
	// A private resolver of a foreign static chain.
	servePlainDNS(t, "10.53.0.53:53", answer("foreign"))

	var resolvedCmd *exec.Cmd
	var owner string
	linkPolicy := func() error {
		for _, args := range [][]string{{"dns", "dnspriv0", "127.0.0.2#resolver.fixture.example"}, {"domain", "dnspriv0", "~corp.example"}, {"default-route", "dnspriv0", "no"}, {"dnsovertls", "dnspriv0", "yes"}, {"dnssec", "dnspriv0", "yes"}} {
			if out, err := execute(ctx, "resolvectl", args...); err != nil {
				return fmt.Errorf("link policy %v: %s %w", args, out, err)
			}
		}
		return nil
	}
	startResolved := func() error {
		resolvedCmd = start("/usr/lib/systemd/systemd-resolved")
		owner = ""
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			if o, err := dnsBusOwner(ctx, execute); err == nil {
				owner = o
				break
			}
		}
		if owner == "" {
			return errors.New("fixture resolver did not take its bus name")
		}
		// A link manager pushes its links' DNS again when resolved restarts.
		return linkPolicy()
	}
	stopResolved := func() {
		if resolvedCmd != nil {
			_ = syscall.Kill(-resolvedCmd.Process.Pid, syscall.SIGTERM)
			_ = resolvedCmd.Wait()
			resolvedCmd = nil
		}
	}
	if err := startResolved(); err != nil {
		raw, _ := os.ReadFile(filepath.Join(dir, "systemd-resolved.log"))
		t.Fatalf("%v\n%s", err, raw)
	}
	restarts := 0
	previousRun, previousNative, previousFile, previousHosts := run, dnsNativeExecutor, dnsLookupFileExecutor, dnsHostsExecutor
	previousHostRoot, previousOwnerRoot := dnsHostRoot, dnsOwnerRoot
	t.Cleanup(func() {
		run, dnsNativeExecutor, dnsLookupFileExecutor, dnsHostsExecutor = previousRun, previousNative, previousFile, previousHosts
		dnsHostRoot, dnsOwnerRoot = previousHostRoot, previousOwnerRoot
	})
	dnsHostRoot, dnsOwnerRoot = "", ""
	dnsNativeExecutor, dnsLookupFileExecutor, dnsHostsExecutor = execute, execute, execute
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name != "systemctl" {
			return execute(ctx, name, args...)
		}
		switch strings.Join(args, " ") {
		case "is-active systemd-resolved":
			if resolvedCmd == nil {
				return "inactive\n", nil
			}
			return "active\n", nil
		case "restart systemd-resolved":
			restarts++
			stopResolved()
			return "", startResolved()
		}
		return "", fmt.Errorf("the fixture never asks systemd: %v", args)
	}
	fail := func(format string, args ...any) {
		t.Helper()
		raw, _ := os.ReadFile(filepath.Join(dir, "systemd-resolved.log"))
		lines := strings.Split(string(raw), "\n")
		if len(lines) > 60 {
			lines = lines[len(lines)-60:]
		}
		t.Fatalf(format+"\nresolved log tail:\n%s", append(args, strings.Join(lines, "\n"))...)
	}
	s := New(Options{Paths: Paths{Dir: filepath.Join(dir, "state"), Resolved: "/etc/systemd/resolved.conf.d/90-just-dashboard.conf", Hosts: "/etc/hosts"}})
	checks := func(v *DNSVerification) map[string]DNSVerificationCheck {
		out := map[string]DNSVerificationCheck{}
		if v == nil {
			return out
		}
		for _, c := range v.Checks {
			out[c.Kind+" "+c.Name] = c
		}
		return out
	}

	// C066: the namespace's chain is resolved's stub, confirmed.
	ownerView, err := s.DNS(ctx, nil)
	if err != nil || ownerView.Owner.ID != "systemd-resolved" || ownerView.Owner.Chain != "stub" || ownerView.Owner.Confidence != "confirmed" || !ownerView.Owner.DashboardWrites {
		fail("owner = %+v %v", ownerView.Owner, err)
	}

	// C068, C069, C071: strict DoT through the public-looking upstream, the
	// fallback cleared, two verification names in two scopes.
	strict := DNSSettings{Servers: []string{"203.0.113.53#resolver.fixture.example"}, Domains: []string{"~."}, DNSSEC: "yes", DNSOverTLS: "yes", Clear: []string{"fallback"}, VerificationNames: []string{"www.example", "secret.corp.example"}}
	applied, err := s.SetDNS(ctx, strict, "fixture")
	if err != nil {
		var up *UpstreamError
		if errors.As(err, &up) {
			status, _ := execute(ctx, "resolvectl", "status", "--no-pager")
			fail("strict apply refused: %v %+v\nresolvectl status:\n%s", err, up.Verification, status)
		}
		fail("strict apply: %v", err)
	}
	c := checks(applied.Verification)
	if c["transport www.example"].State == "passed" && !strings.Contains(c["transport www.example"].Detail, "strict mode") {
		report, err := s.InvestigateDNS(ctx, DNSInvestigationRequest{Name: "www.example", Type: "A"}, execute)
		fail("strict trust not established: %+v %v", report, err)
	}
	if c["readback "].State != "passed" || c["resolution www.example"].State != "passed" || !strings.HasPrefix(c["resolution www.example"].Scope, "default route") ||
		c["resolution secret.corp.example"].State != "passed" || c["resolution secret.corp.example"].Scope != "~corp.example on dnspriv0" ||
		c["transport www.example"].State != "passed" || !strings.Contains(c["transport www.example"].Detail, "strict mode") || c["dnssec www.example"].State != "passed" {
		fail("strict verification = %+v", applied.Verification)
	}
	if m := readManagedDNS(s.paths.Resolved); len(m.Cleared) != 1 || m.Cleared[0] != "fallback" {
		fail("cleared drop-in = %+v", m)
	}
	status, _ := execute(ctx, "resolvectl", "status", "--no-pager")
	// resolvectl also lists the link's loopback server under Global; the
	// read-back check discounts it, and so does this.
	if g, _, _ := parseResolvedStatus(status); len(g.Fallback) != 0 || len(g.Servers) == 0 || g.Servers[0] != "203.0.113.53#resolver.fixture.example" {
		fail("resolved read-back after clearing: %+v", g)
	}
	if leaked := log.under("public", "corp.example"); len(leaked) != 0 {
		fail("private names reached the public upstream: %v", leaked)
	}
	t.Logf("strict DoT apply verified: %d checks, restarts=%d", len(applied.Verification.Checks), restarts)
	strictDropIn, _ := os.ReadFile(s.paths.Resolved)

	// C069: opportunistic TLS to a classic-only server falls back and says so.
	opportunistic := DNSSettings{Servers: []string{"127.0.0.4"}, Domains: []string{"~."}, DNSSEC: "yes", DNSOverTLS: "opportunistic", VerificationNames: []string{"www.example"}}
	applied, err = s.SetDNS(ctx, opportunistic, "fixture")
	if err != nil {
		fail("opportunistic apply: %v", err)
	}
	if tr := checks(applied.Verification)["transport www.example"]; tr.State != "warning" || !strings.Contains(tr.Detail, "fell back") || log.count("plain") == 0 {
		fail("opportunistic transport = %+v", applied.Verification)
	}
	t.Log("opportunistic DoT fallback reported as a warning")

	// C069, C071: required TLS against a wrong identity cannot answer; the
	// change is put back and resolved restarted onto the previous drop-in.
	if _, err = s.SetDNS(ctx, strict, "fixture"); err != nil {
		fail("restore strict: %v", err)
	}
	beforeRestarts := restarts
	wrong := strict
	wrong.Servers = []string{"203.0.113.53#wrong.fixture.example"}
	_, err = s.SetDNS(ctx, wrong, "fixture")
	var up *UpstreamError
	if !errors.As(err, &up) || !strings.Contains(up.Reason, "previous settings were put back") || checks(up.Verification)["resolution www.example"].State != "failed" || restarts != beforeRestarts+2 {
		fail("wrong identity = %v (restarts %d→%d)", err, beforeRestarts, restarts)
	}
	// The restored drop-in is the strict one; only its header's timestamp can differ.
	if now, _ := os.ReadFile(s.paths.Resolved); !strings.Contains(string(now), "DNS=203.0.113.53#resolver.fixture.example") || strings.Contains(string(now), "wrong.fixture") {
		fail("rollback left %q (first strict drop-in %q)", now, strictDropIn)
	}
	t.Log("wrong TLS identity rolled back with the previous drop-in restored")

	// C071: a later drop-in overriding the servers fails the read-back.
	override := "/etc/systemd/resolved.conf.d/95-fixture-override.conf"
	if err := os.WriteFile(override, []byte("[Resolve]\nDNS=\nDNS=127.0.0.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetDNS(ctx, strict, "fixture")
	if !errors.As(err, &up) || !strings.Contains(up.Reason, "not running with the settings written") || checks(up.Verification)["readback "].State != "failed" {
		fail("override read-back = %v", err)
	}
	if err := os.Remove(override); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetDNS(ctx, strict, "fixture"); err != nil {
		fail("apply after removing the override: %v", err)
	}
	t.Log("an overriding later drop-in failed the read-back and was rolled back")

	// C069: the dashboard's own certificate check of every configured server.
	report, err := s.CheckDNSTLS(ctx, nil)
	if err != nil {
		fail("tls check: %v", err)
	}
	trusted := 0
	for _, check := range report.Checks {
		if check.State == "trusted" && check.ChainLength == 2 {
			trusted++
		}
	}
	if trusted != 2 {
		fail("tls report = %+v", report)
	}
	t.Logf("independent TLS check trusted %d servers", trusted)

	// C070: the chain of www.example reaches the namespace's root, whose key
	// is not an IANA anchor; the private name's chain stays in its scope.
	publicBefore := log.count("public")
	chain, err := s.InvestigateDNSSEC(ctx, "www.example", nil)
	if err != nil || chain.Error != "" || len(chain.Levels) != 3 || chain.Levels[0].Role != "not_apex" || chain.Levels[1].Link != "digest_match" || chain.Levels[1].DS.State != "authenticated" ||
		chain.Levels[2].Link != "root_anchor_mismatch" || chain.Levels[2].DNSKEY.State != "authenticated" || chain.Verdict != "broken" {
		fail("public chain = %+v %v", chain, err)
	}
	private, err := s.InvestigateDNSSEC(ctx, "secret.corp.example", nil)
	if err != nil || private.Error != "" || private.Verdict != "anchored" || private.Levels[1].Link != "digest_match" || private.Levels[2].Role != "not_queried" || private.Levels[3].Role != "not_queried" {
		fail("private chain = %+v %v", private, err)
	}
	if leaked := log.under("public", "corp.example"); len(leaked) != 0 {
		fail("private chain questions reached the public upstream: %v (public grew by %d)", leaked, log.count("public")-publicBefore)
	}
	t.Log("DNSSEC chain: recomputed DS digests matched; fake root refused as an IANA anchor; private ancestors not asked")

	// F10: a private name nothing claims is refused before the public default
	// route hears it; a link-claimed one goes to its link.
	publicBefore = log.count("public")
	_, err = s.LookupWithOptions(ctx, "nas.home.arpa", "A", LookupOptions{})
	var refusal *DNSPolicyRefusal
	if !errors.As(err, &refusal) || refusal.Code != "dns_private_name_public_upstream" || log.count("public") != publicBefore {
		fail("unclaimed private name = %v (public %d→%d)", err, publicBefore, log.count("public"))
	}
	claimed, err := s.LookupWithOptions(ctx, "secret.corp.example", "A", LookupOptions{})
	if err != nil || len(claimed.Answers) != 1 || len(claimed.Answers[0].Answers) != 1 || len(log.under("public", "corp.example")) != 0 {
		fail("claimed private name = %+v %v", claimed, err)
	}
	// A foreign static chain: a public resolver is refused outright, a private
	// one needs the acknowledgement and then receives the name.
	if err := os.Remove("/etc/resolv.conf"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte("nameserver 203.0.113.53\n"), 0644); err != nil {
		t.Fatal(err)
	}
	publicBefore = log.count("public")
	if _, err = s.LookupWithOptions(ctx, "nas.home.arpa", "A", LookupOptions{}); !errors.As(err, &refusal) || refusal.Code != "dns_private_name_public_upstream" || log.count("public") != publicBefore {
		fail("foreign public chain = %v", err)
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte("search corp.example\nnameserver 10.53.0.53\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupWithOptions(ctx, "secret.corp.example", "A", LookupOptions{}); !errors.As(err, &refusal) || refusal.Code != "dns_private_name_unknown_forwarding" || log.count("foreign") != 0 {
		fail("foreign private chain without acknowledgement = %v", err)
	}
	acknowledged, err := s.LookupWithOptions(ctx, "secret.corp.example", "A", LookupOptions{AcknowledgeForwarding: true})
	if err != nil || len(acknowledged.Answers) != 1 || acknowledged.Answers[0].Error != "" || log.count("foreign") == 0 {
		fail("foreign private chain with acknowledgement = %+v %v", acknowledged, err)
	}
	foreignOwner, _ := s.DNS(ctx, nil)
	if foreignOwner.Owner.ID != "static" || foreignOwner.Owner.DashboardWrites || len(foreignOwner.Owner.Conflicts) == 0 {
		fail("foreign owner = %+v", foreignOwner.Owner)
	}
	if err := os.Remove("/etc/resolv.conf"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/run/systemd/resolve/stub-resolv.conf", "/etc/resolv.conf"); err != nil {
		t.Fatal(err)
	}
	t.Log("F10: unclaimed private names refused before any public question; foreign private chains need acknowledgement")

	// C072: the host's NSS answers the managed names from the namespace's
	// own hosts file, and the earlier foreign line shadows one of them.
	resolution, err := s.HostResolution(ctx)
	if err != nil {
		fail("host resolution: %v", err)
	}
	states := map[string]string{}
	for _, n := range resolution.Names {
		states[n.Name] = n.State
	}
	if states["nas.lan"] != "matches" || states["shadowed.lan"] != "includes" && states["shadowed.lan"] != "differs" {
		fail("host resolution = %+v", resolution)
	}
	preview, err := s.PreviewHostRecords(ctx, []HostRecord{{Address: "192.0.2.80", Names: []string{"shadowed.lan"}}, {Address: "192.0.2.81", Names: []string{"nas.lan"}}})
	if err != nil || len(preview.Issues) != 1 || preview.Issues[0].Kind != "shadowed" || preview.Issues[0].Line != 2 {
		fail("hosts preview = %+v %v", preview, err)
	}
	names := []string{}
	for name, state := range states {
		names = append(names, name+"="+state)
	}
	sort.Strings(names)
	t.Logf("local resolution: %s", strings.Join(names, " "))

	// The fixture's own drop-in is removed through ResetDNS, which restarts
	// only the namespace's resolver.
	if _, err := s.ResetDNS(ctx); err != nil {
		fail("reset: %v", err)
	}
	t.Logf("resolver maturity fixture passed: %d namespace resolver restarts, public questions %d, private %d, plain %d, foreign %d", restarts, log.count("public"), log.count("private"), log.count("plain"), log.count("foreign"))
}
