package dnsservice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.org/x/net/dns/dnsmessage"
)

type nativeAcceptanceRuntime struct {
	*dockerRuntime
	t *testing.T
}

func (d *nativeAcceptanceRuntime) Destroy(ctx context.Context, s provisionSpec, r ProvisionResources) error {
	if directory := os.Getenv("JD_DNS_SERVICES_NATIVE_LOG_DIR"); directory != "" {
		if _, err := d.containerIdentity(ctx, s, r); err == nil {
			reader, e := d.cli.ContainerLogs(ctx, r.ContainerID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "120"})
			if e == nil {
				var output bytes.Buffer
				_, e = stdcopy.StdCopy(&output, &output, io.LimitReader(reader, 64<<10))
				reader.Close()
				if e == nil {
					text := output.String()
					for _, secret := range []string{"explicit-owned-fixture-password", s.Request.Password} {
						if secret != "" {
							text = strings.ReplaceAll(text, secret, "[redacted]")
						}
					}
					path := filepath.Join(directory, string(s.Request.Engine)+"-"+s.ID+".log")
					if e = os.WriteFile(path, []byte(text), 0600); e == nil {
						d.t.Logf("Owned fixture native log retained: %s (%d bytes)", path, len(text))
					} else {
						d.t.Errorf("owned fixture log retention: %v", e)
					}
				}
			}
		}
	}
	return d.dockerRuntime.Destroy(ctx, s, r)
}

func freeNativePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func nativeFixtureService(t *testing.T, path string, runtime ProvisionRuntime) *Service {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{DB: db.DB, Seal: sealer.Seal, Open: sealer.Open, Runtime: runtime})
}

func TestDNSServiceNativeOwnedEngine(t *testing.T) {
	engine := Engine(os.Getenv("JD_DNS_SERVICES_LIVE_ENGINE"))
	if engine == "" {
		t.Skip("select one explicitly cached engine for the owned Docker fixture")
	}
	if pinnedImages[engine] == "" {
		t.Fatal("unrecognized native fixture engine")
	}
	host := os.Getenv("JD_DNS_SERVICES_DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	runtime := NewDockerRuntime(host).(*dockerRuntime)
	t.Cleanup(func() { runtime.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	if _, err := runtime.Image(ctx, engine); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("JD_DNS_SERVICES_PREDEATH") == "1" {
		s := nativeFixtureService(t, os.Getenv("JD_DNS_SERVICES_STORE"), runtime)
		id := os.Getenv("JD_DNS_SERVICES_PLAN")
		p, err := s.Provision(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var sealed string
		if err = s.db.QueryRow(`SELECT secret_enc FROM network_dns_service_provisions WHERE id=?`, id).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		p.Request.Password, err = s.open(sealed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`UPDATE network_dns_service_provisions SET state='applying' WHERE id=? AND state='planned'`, id); err != nil {
			t.Fatal(err)
		}
		resources, err := runtime.Prepare(ctx, p.spec(), p.Resources, func(r ProvisionResources) error { return s.journalProvision(&p, r) })
		if err != nil {
			t.Fatal(err)
		}
		if err = runtime.Start(ctx, p.spec(), resources); err != nil {
			t.Fatal(err)
		}
		// An abrupt child exit leaves only journaled, matching owned resources.
		// The parent cold-start reconciliation must remove them without bootstrap.
		os.Exit(0)
	}
	path := t.TempDir()
	acceptanceRuntime := &nativeAcceptanceRuntime{runtime, t}
	s := nativeFixtureService(t, path, acceptanceRuntime)
	managementPort, dnsPort := freeNativePort(t), freeNativePort(t)
	for dnsPort == managementPort {
		dnsPort = freeNativePort(t)
	}
	req := ProvisionRequest{Name: "Owned native acceptance", Engine: engine, ManagementPort: managementPort, DNSPort: dnsPort, MemoryMiB: 384, CPUs: 0.5, Upstreams: []string{"192.0.2.53:53"}, Username: "admin", Password: "explicit-owned-fixture-password"}
	plan, err := s.PreviewProvision(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		current, e := s.Provision(cleanup, plan.ID)
		if e == nil && current.State != "removed" {
			if e = acceptanceRuntime.Destroy(cleanup, current.spec(), current.Resources); e != nil {
				t.Errorf("owned fixture final cleanup: %v", e)
			}
		}
	})
	plan, err = s.ApplyProvision(ctx, plan.ID)
	if err != nil || plan.State != "verified" {
		t.Fatalf("native %s provision state=%s phase=%s error=%s err=%v", engine, plan.State, plan.Resources.Phase, plan.Error, err)
	}
	if _, err = s.ApplyProvision(ctx, plan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("native setup replay accepted: %v", err)
	}
	connection, connectionReq, err := s.connection(ctx, plan.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if connection.Management {
		t.Fatal("native administrator credential opened default dashboard management")
	}
	if _, err = s.Preview(ctx, connection.ID, ChangeRequest{Action: "upstreams", Upstreams: []string{"192.0.2.54:5353"}}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("native read-only staging allowed: %v", err)
	}
	connectionReq.Management = true
	if _, err = s.Update(ctx, connection.ID, connectionReq); err != nil {
		t.Fatal(err)
	}
	protection := false
	change, err := s.Preview(ctx, connection.ID, ChangeRequest{Action: "protection", Protection: &protection})
	if err != nil {
		t.Fatal(err)
	}
	change, err = s.Apply(ctx, change.ID)
	if err != nil || change.State != "verified" || change.After == nil || change.After.Protection {
		t.Fatalf("native protection readback state=%s error=%s err=%v", change.State, change.Error, err)
	}
	change, err = s.Preview(ctx, connection.ID, ChangeRequest{Action: "upstreams", Upstreams: []string{"192.0.2.54:5353"}})
	if err != nil {
		t.Fatal(err)
	}
	change, err = s.Apply(ctx, change.ID)
	if err != nil || change.State != "verified" {
		t.Fatalf("native upstream readback state=%s error=%s err=%v", change.State, change.Error, err)
	}
	name := "owned-fixture.invalid"
	nativeClient, err := newNativeClient(connectionReq)
	if err != nil {
		t.Fatal(err)
	}
	defer nativeClient.close()
	if err = nativeClient.login(ctx); err != nil {
		t.Fatal(err)
	}
	defer nativeClient.logout()
	switch engine {
	case AdGuard:
		err = nil
	case PiHole:
		err = nativeClient.request(ctx, http.MethodPost, "/api/clients", map[string]any{"client": "198.51.100.77", "comment": "owned fixture client comment", "groups": []int{0}}, nil)
	case Technitium:
		zone, zoneErr := s.Preview(ctx, connection.ID, ChangeRequest{Action: "zone_create", Zone: name})
		if zoneErr != nil {
			t.Fatal(zoneErr)
		}
		zone, zoneErr = s.Apply(ctx, zone.ID)
		if zoneErr != nil || zone.State != "verified" {
			t.Fatalf("native authority creation state=%s error=%s err=%v", zone.State, zone.Error, zoneErr)
		}
		err = nil
	}
	if err != nil {
		t.Fatal(err)
	}
	policyRequests := []ChangeRequest{{Action: "override_add", Record: &RecordChange{Name: name, Type: "A", Value: "198.51.100.99"}}, {Action: "override_add", Record: &RecordChange{Name: "v6." + name, Type: "AAAA", Value: "2001:db8::99"}}}
	if engine == Technitium {
		for i := range policyRequests {
			policyRequests[i].Action, policyRequests[i].Zone, policyRequests[i].Record.TTL = "record_add", name, 60
		}
	}
	if engine == PiHole {
		policyRequests = append(policyRequests, ChangeRequest{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: []int{}}})
	}
	assertCurrentPolicy := func(change Change, expected *Snapshot) {
		t.Helper()
		current, e := s.CurrentChange(ctx, change.ID)
		if e != nil || current.State != "available" || current.Connection.ID != connection.ID || current.Connection.Generation != change.Generation || current.Snapshot == nil || expected == nil || current.Snapshot.PolicyFingerprint != expected.PolicyFingerprint || current.Snapshot.SelectionFingerprint != expected.SelectionFingerprint {
			t.Fatalf("native exact current %s state=%s error=%s err=%v", change.Request.Action, current.State, current.Error, e)
		}
		retained, e := s.Change(ctx, change.ID)
		if e != nil || retained.State != change.State || retained.Before == nil || retained.Before.PolicyFingerprint != change.Before.PolicyFingerprint || (retained.After == nil) != (change.After == nil) {
			t.Fatal("native current reading changed the retained review", e)
		}
		if retained.After != nil && retained.After.PolicyFingerprint != change.After.PolicyFingerprint {
			t.Fatal("native current reading replaced retained readback")
		}
		t.Logf("Native exact-selection current read passed: %s %s", change.Request.Action, change.State)
	}
	for _, request := range policyRequests {
		change, e := s.Preview(ctx, connection.ID, request)
		if e != nil {
			t.Fatal("native reviewed policy preview", request.Action, e)
		}
		assertCurrentPolicy(change, change.Before)
		change, e = s.Apply(ctx, change.ID)
		if e != nil || change.State != "verified" || !policyPreserved(request, change.Before, change.After) {
			t.Fatalf("native reviewed %s state=%s error=%s err=%v", request.Action, change.State, change.Error, e)
		}
		assertCurrentPolicy(change, change.After)
		if _, e = s.Apply(ctx, change.ID); !errors.Is(e, ErrConflict) {
			t.Fatal("native reviewed policy replay", e)
		}
	}
	for _, protocol := range []string{"udp", "tcp"} {
		nativeDNSAnswer(t, protocol, dnsPort, name, engine == Technitium)
		nativeDNSAAAAAnswer(t, protocol, dnsPort, "v6."+name, engine == Technitium)
	}
	view, err := s.Inspect(ctx, connection.ID)
	if err != nil || view.State != "available" || view.Snapshot == nil {
		t.Fatalf("native retained inspection unavailable: %v", err)
	}
	if engine == Technitium {
		if len(view.Snapshot.Zones) == 0 || view.Snapshot.ZoneEvidence.State != "native_authority_configuration" || view.Snapshot.QueryEvidence.State != "unsupported" {
			t.Fatal("native authority/log-app evidence conflated")
		}
	} else if view.Snapshot.OverrideEvidence.State != "configured" || len(view.Snapshot.LocalOverrides) == 0 || view.Snapshot.ZoneEvidence.State != "unsupported" {
		t.Fatal("native local override was presented as authoritative zone")
	}
	seconds := 5
	if err = runtime.Verify(ctx, plan.spec(), plan.Resources); err != nil {
		t.Fatal(err)
	}
	if err = runtime.cli.ContainerRestart(ctx, plan.Resources.ContainerID, container.StopOptions{Timeout: &seconds}); err != nil {
		t.Fatal(err)
	}
	for {
		snapshot, e := inspectNative(ctx, connectionReq)
		if e == nil {
			if !changeMatches(engine, ChangeRequest{Action: "upstreams", Upstreams: []string{"192.0.2.54:5353"}}, snapshot) || snapshot.Protection {
				t.Fatal("native persisted policy changed after restart")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native credential/config did not persist after restart")
		case <-time.After(250 * time.Millisecond):
		}
	}
	nativeDNSAnswer(t, "udp", dnsPort, name, engine == Technitium)
	nativeDNSAAAAAnswer(t, "tcp", dnsPort, "v6."+name, engine == Technitium)
	for _, request := range policyRequests {
		snapshot, e := inspectNativeSelection(ctx, connectionReq, &request)
		if e != nil || !policyMatches(request, snapshot) {
			t.Fatal("native reviewed policy did not persist after restart", request.Action, e)
		}
		if request.Action == "client_groups" {
			request.Client.Groups = []int{0}
		} else if request.Action == "record_add" {
			request.Action = "record_remove"
		} else {
			request.Action = "override_remove"
		}
		change, e := s.Preview(ctx, connection.ID, request)
		if e != nil {
			t.Fatal("native reviewed removal preview", e)
		}
		assertCurrentPolicy(change, change.Before)
		change, e = s.Apply(ctx, change.ID)
		if e != nil || change.State != "verified" {
			t.Fatal("native reviewed removal readback", change.State, change.Error, e)
		}
		assertCurrentPolicy(change, change.After)
	}
	removed, err := s.RemoveProvision(ctx, plan.ID)
	if err != nil || removed.State != "removed" {
		t.Fatalf("native owned removal state=%s error=%s err=%v", removed.State, removed.Error, err)
	}
	assertNativeResourcesAbsent(t, runtime, plan.Resources)
	// A fresh process ends after resource creation/start, before API bootstrap.
	// Reconstruct from the same store and clean only its persisted ownership.
	req.ManagementPort, req.DNSPort = freeNativePort(t), freeNativePort(t)
	interrupted, err := s.PreviewProvision(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		p, e := s.Provision(cleanup, interrupted.ID)
		if e == nil {
			if e = acceptanceRuntime.Destroy(cleanup, p.spec(), p.Resources); e != nil {
				t.Errorf("interrupted owned fixture final cleanup: %v", e)
			}
		}
	})
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDNSServiceNativeOwnedEngine$", "-test.v")
	child.Env = append(os.Environ(), "JD_DNS_SERVICES_PREDEATH=1", "JD_DNS_SERVICES_STORE="+path, "JD_DNS_SERVICES_PLAN="+interrupted.ID)
	if output, e := child.CombinedOutput(); e != nil {
		t.Fatalf("controlled predecessor: %v %s", e, output)
	}
	cold := nativeFixtureService(t, path, &nativeAcceptanceRuntime{runtime, t})
	if err = cold.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	interrupted, err = cold.Provision(ctx, interrupted.ID)
	if err != nil || interrupted.State != "failed" || interrupted.Resources.Phase != "cleaned" {
		t.Fatalf("native predecessor cleanup state=%s phase=%s err=%v", interrupted.State, interrupted.Resources.Phase, err)
	}
	assertNativeResourcesAbsent(t, runtime, interrupted.Resources)
	t.Logf("engine=%s image=%s nativeVersion=%s UDP/TCP=%s owner=%s nativeReadback=verified restart=persisted predecessor=no_replay resources=cleaned", engine, plan.Image, view.Snapshot.Version, name, plan.Owner)
}

func nativeDNSAnswer(t *testing.T, protocol string, port int, name string, authoritative bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := nativeDNSQuery(protocol, port, name, authoritative)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func nativeDNSQuery(protocol string, port int, name string, authoritative bool) error {
	return nativeDNSQueryType(protocol, port, name, authoritative, dnsmessage.TypeA)
}

func nativeDNSAAAAAnswer(t *testing.T, protocol string, port int, name string, authoritative bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := nativeDNSQueryType(protocol, port, name, authoritative, dnsmessage.TypeAAAA)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func nativeDNSQueryType(protocol string, port int, name string, authoritative bool, queryType dnsmessage.Type) error {
	question, err := dnsmessage.NewName(name + ".")
	if err != nil {
		return err
	}
	message := dnsmessage.Message{Header: dnsmessage.Header{ID: 0x4242, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: question, Type: queryType, Class: dnsmessage.ClassINET}}}
	wire, err := message.Pack()
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout(protocol, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if protocol == "tcp" {
		frame := make([]byte, 2+len(wire))
		binary.BigEndian.PutUint16(frame, uint16(len(wire)))
		copy(frame[2:], wire)
		if _, err = conn.Write(frame); err != nil {
			return err
		}
		var length [2]byte
		if _, err = io.ReadFull(conn, length[:]); err != nil {
			return err
		}
		wire = make([]byte, int(binary.BigEndian.Uint16(length[:])))
		_, err = io.ReadFull(conn, wire)
	} else {
		if _, err = conn.Write(wire); err != nil {
			return err
		}
		wire = make([]byte, 4096)
		var n int
		n, err = conn.Read(wire)
		wire = wire[:n]
	}
	if err != nil {
		return err
	}
	if err = message.Unpack(wire); err != nil || message.Header.ID != 0x4242 || !message.Header.Response || message.Header.Truncated || message.Header.RCode != dnsmessage.RCodeSuccess || len(message.Questions) != 1 || message.Questions[0].Name != question || message.Questions[0].Type != queryType || message.Questions[0].Class != dnsmessage.ClassINET || authoritative && !message.Header.Authoritative {
		return fmt.Errorf("native %s DNS owner/authority response rcode=%d: %v", protocol, message.Header.RCode, err)
	}
	found := false
	for _, answer := range message.Answers {
		if data, ok := answer.Body.(*dnsmessage.AResource); ok && queryType == dnsmessage.TypeA && answer.Header.Name == question && data.A == [4]byte{198, 51, 100, 99} {
			found = true
		}
		if data, ok := answer.Body.(*dnsmessage.AAAAResource); ok && queryType == dnsmessage.TypeAAAA && answer.Header.Name == question && data.AAAA == [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x99} {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("native %s DNS answer did not match owned local/authoritative fixture", protocol)
	}
	return nil
}

func assertNativeResourcesAbsent(t *testing.T, d *dockerRuntime, r ProvisionResources) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := d.cli.ContainerInspect(ctx, r.ContainerName); !errdefs.IsNotFound(err) {
		t.Fatalf("owned fixture container remains: %v", err)
	}
	if _, err := d.cli.NetworkInspect(ctx, r.NetworkName, network.InspectOptions{}); !errdefs.IsNotFound(err) {
		t.Fatalf("owned fixture bridge remains: %v", err)
	}
	for _, name := range r.Volumes {
		if _, err := d.cli.VolumeInspect(ctx, name); !errdefs.IsNotFound(err) {
			t.Fatalf("owned fixture volume remains: %v", err)
		}
	}
}
