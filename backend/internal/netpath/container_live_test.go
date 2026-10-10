package netpath

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// This opt-in fixture has no host networking or published ports. Both native
// DNS and TCP listeners live on its isolated loopback; only its own container
// ID is removed. No production route, firewall or resolver is changed.
func TestLiveContainerPathUsesNativeDNSPinnedNamespaceAndSource(t *testing.T) {
	if os.Getenv("JD_NETPATH_LIVE") != "1" {
		t.Skip("set JD_NETPATH_LIVE=1 for the disposable network-none Docker fixture")
	}
	if os.Geteuid() != 0 {
		t.Skip("run the compiled fixture as root to open its process root and network namespace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.WithHost("unix:///var/run/docker.sock"), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.ImageInspect(ctx, "python:3.11-slim"); err != nil {
		t.Skip("local python:3.11-slim fixture image is unavailable; no image is pulled")
	}
	const fixture = `import socket,struct,threading,time
def dns():
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('127.0.0.1',53))
 while True:
  data,peer=s.recvfrom(2048);end=12
  while data[end]:end+=data[end]+1
  end+=5;question=data[12:end]
  # One absolute answer, inside this namespace only.
  response=data[:2]+struct.pack('!HHHHH',0x8180,1,1,0,0)+question+b'\xc0\x0c'+struct.pack('!HHIH',1,1,30,4)+socket.inet_aton('127.0.0.2')
  s.sendto(response,peer)
threading.Thread(target=dns,daemon=True).start()
s=socket.socket();s.bind(('127.0.0.2',18443));s.listen()
while True:
 c,peer=s.accept();c.close()
`
	created, err := cli.ContainerCreate(ctx, &container.Config{Image: "python:3.11-slim", Cmd: []string{"python", "-u", "-c", fixture}, Labels: map[string]string{"jd.test": "network-investigator"}}, &container.HostConfig{NetworkMode: "none", DNS: []string{"127.0.0.1"}}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = cli.ContainerRemove(cleanup, created.ID, container.RemoveOptions{Force: true})
	})
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	owner := dockerx.New("unix:///var/run/docker.sock")
	defer owner.Close()
	var source *dockerx.NetworkSource
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		source, err = owner.NetworkSource(ctx, created.ID)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if source.DNSError != "" || len(source.Nameservers) != 1 || source.Nameservers[0] != "127.0.0.1" {
		t.Fatalf("container resolver replaced by host: %#v", source)
	}
	command := func(ctx context.Context, tool string, args ...string) (*exec.Cmd, func(), error) {
		return owner.NetworkSourceCommand(ctx, source, tool, args...)
	}
	execute := func(ctx context.Context, tool string, args ...string) (string, error) {
		output, _, err := netsec.RunSourceDiagnostic(ctx, command, tool, args...)
		return output, err
	}
	req := Request{SourceKind: "container", ContainerID: created.ID, Target: "fixture.private", SourceAddress: "127.0.0.3", Family: "inet", Protocol: "tcp", Port: 18443, Measure: true}
	p := Providers{SourceName: source.Name, DNS: func(ctx context.Context, r Request) (DNSAnswer, error) {
		answer, err := netsec.SourceDNS(ctx, command, r.Target, r.Family, source.Nameservers)
		if err != nil {
			return DNSAnswer{}, err
		}
		if answer.Error != "" {
			return DNSAnswer{}, fmt.Errorf("%s", answer.Error)
		}
		return DNSAnswer{Addresses: answer.Records, Owner: "fixture native resolver"}, nil
	}, Route: func(ctx context.Context, r Request) (netx.RouteLookup, error) {
		return (&netx.Service{}).LookupTrafficRoute(ctx, r.Address, r.SourceAddress, r.Mark, r.Protocol, r.Port, execute)
	}, Probe: func(ctx context.Context, r Request) (*netsec.ProbeResult, error) {
		return netsec.SourcePortCheck(ctx, command, r.Address, r.Port, r.SourceAddress)
	}}
	var result *Result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		result, err = Investigate(ctx, req, p)
		if err == nil && result.Measurement != nil && result.Measurement.OK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || result.Measurement == nil || !result.Measurement.OK || result.Scope.Address != "127.0.0.2" || result.Scope.SourceAddress != "127.0.0.3" || findEvidence(t, result, "route").Basis != Observed || findEvidence(t, result, "owner").Basis != Unknown {
		t.Fatalf("wrong native path %#v %v", result, err)
	}
	// A restart invalidates the captured process identity. It cannot enter a
	// new process using a stale source, even when an inventory context was cached.
	if err := cli.ContainerRestart(ctx, created.ID, container.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := owner.NetworkSourceCommand(ctx, source, "ip", "-j", "route", "show"); err == nil || !strings.Contains(err.Error(), "restarted") {
		t.Fatalf("stale container identity accepted: %v", err)
	}
}
