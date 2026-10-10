package dockerx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func TestLiveAdvancedNetworkDualStackReadbackAndAttachment(t *testing.T) {
	if os.Getenv("JD_DOCKER_NETWORK_LIVE") != "1" {
		t.Skip("set JD_DOCKER_NETWORK_LIVE=1 for exact owned native Docker fixtures")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	c := New("unix:///var/run/docker.sock")
	t.Cleanup(func() { c.Close() })
	cli, err := c.api()
	if err != nil {
		t.Fatal(err)
	}
	// No pull: this fixture never admits a third-party image or changes a workload.
	if _, _, err := cli.ImageInspectWithRaw(ctx, "python:3.11-slim"); err != nil {
		t.Fatal("make the fixture image available locally first:", err)
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(nonce[:])
	name := "jd-net-accept-" + token
	label := "jd.network-acceptance"
	occupied := []netip.Prefix{}
	rows, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil || len(rows) > 512 {
		t.Fatalf("bounded native inventory: %d %v", len(rows), err)
	}
	for _, row := range rows {
		detail, err := cli.NetworkInspect(ctx, row.ID, network.InspectOptions{})
		if err != nil {
			t.Fatal("native pool inventory unreadable:", err)
		}
		for _, pool := range detail.IPAM.Config {
			p, err := netip.ParsePrefix(pool.Subnet)
			if err != nil {
				t.Fatal("unreadable native pool:", pool.Subnet, err)
			}
			occupied = append(occupied, p.Masked())
		}
	}
	for _, family := range []string{"-4", "-6"} {
		command := exec.CommandContext(ctx, "ip", "-j", family, "route", "show", "table", "all")
		output, err := command.Output()
		if err != nil || len(output) > 512<<10 {
			t.Fatalf("bounded route inventory: %v", err)
		}
		var routes []struct {
			Destination string `json:"dst"`
		}
		if json.Unmarshal(output, &routes) != nil {
			t.Fatal("native route inventory malformed")
		}
		for _, row := range routes {
			if row.Destination == "" || row.Destination == "default" {
				continue
			}
			p, err := netip.ParsePrefix(row.Destination)
			if err != nil {
				a, e := netip.ParseAddr(row.Destination)
				if e != nil {
					t.Fatal("native route destination malformed:", row.Destination)
				}
				p = netip.PrefixFrom(a, a.BitLen())
			}
			if p.Bits() > 0 {
				occupied = append(occupied, p.Masked())
			}
		}
	}
	available := func(p netip.Prefix) bool {
		return !slices.ContainsFunc(occupied, func(other netip.Prefix) bool { return p.Overlaps(other) })
	}
	var v4 netip.Prefix
	for i := range 512 {
		p := netip.MustParsePrefix(fmt.Sprintf("198.%d.%d.0/24", 18+(int(nonce[0])+i)/256%2, (int(nonce[1])+i)%256))
		if available(p) {
			v4 = p
			break
		}
	}
	v6 := netip.MustParsePrefix(fmt.Sprintf("fd%s:%s:%s:%s::/64", token[:2], token[2:6], token[6:10], token[10:14]))
	if !v4.IsValid() || !available(v6) {
		t.Fatal("no nonoverlapping owned test pools in the observed host inventory")
	}
	v4base := v4.Addr().As4()
	v4base[3] = 128
	range4 := netip.PrefixFrom(netip.AddrFrom4(v4base), 25)
	range6 := netip.PrefixFrom(v6.Addr(), 80)
	spec := NetworkSpec{Name: name, Driver: "bridge", IPv6: true, Internal: true, Attachable: true,
		IPAM:    []NetworkIPAM{{Subnet: v4.String(), Gateway: v4.Addr().Next().String(), IPRange: range4.String()}, {Subnet: v6.String(), Gateway: v6.Addr().Next().String(), IPRange: range6.String()}},
		Options: map[string]string{"com.docker.network.driver.mtu": "1400"}, Labels: map[string]string{label: token}}
	made, err := c.CreateNetwork(ctx, spec)
	if err != nil {
		t.Fatal("owned network create:", err)
	}
	networkID := made.ID
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		inspected, err := cli.NetworkInspect(cleanup, networkID, network.InspectOptions{})
		if err != nil {
			t.Errorf("cannot verify fixture network for cleanup: %v", err)
			return
		}
		if inspected.Name != name || inspected.Labels[label] != token {
			t.Error("fixture network identity changed; preserve it")
			return
		}
		if err := cli.NetworkRemove(cleanup, networkID); err != nil {
			t.Errorf("owned network cleanup: %v", err)
		}
	})
	actual, err := cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err != nil || actual.Name != name || actual.Driver != "bridge" || !actual.Internal || !actual.Attachable || !actual.EnableIPv6 || actual.Options["com.docker.network.driver.mtu"] != "1400" || actual.Labels[label] != token || len(actual.IPAM.Config) != 2 {
		t.Fatalf("advanced native fields not retained: %+v %v", actual, err)
	}
	for _, expected := range spec.IPAM {
		if !slices.ContainsFunc(actual.IPAM.Config, func(got network.IPAMConfig) bool {
			return got.Subnet == expected.Subnet && got.Gateway == expected.Gateway && got.IPRange == expected.IPRange
		}) {
			t.Fatalf("native family pool missing: %+v %+v", expected, actual.IPAM.Config)
		}
	}
	detail, err := c.NetworkDetail(ctx, networkID)
	if err != nil || !detail.IPv6 || len(detail.Subnets) != 2 || len(detail.Members) != 0 || detail.UsedBy == nil {
		t.Fatalf("empty native detail=%+v %v", detail, err)
	}
	conflicting := spec
	conflicting.Name = name + "-conflict"
	if unexpected, err := c.CreateNetwork(ctx, conflicting); err == nil {
		// An unexpected native acceptance is still ours; remove only its exact identity.
		if unexpected != nil {
			_ = cli.NetworkRemove(ctx, unexpected.ID)
		}
		t.Fatal("Engine unexpectedly accepted an overlapping local allocation")
	}
	created, err := cli.ContainerCreate(ctx, &container.Config{Image: "python:3.11-slim", Cmd: []string{"python", "-c", "import time;time.sleep(120)"}, Labels: map[string]string{label: token}}, &container.HostConfig{NetworkMode: container.NetworkMode(networkID)}, nil, nil, name+"-member")
	if err != nil {
		t.Fatal(err)
	}
	containerID := created.ID
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		inspected, err := cli.ContainerInspect(cleanup, containerID)
		if err != nil {
			t.Errorf("cannot verify fixture container cleanup: %v", err)
			return
		}
		if strings.TrimPrefix(inspected.Name, "/") != name+"-member" || inspected.Config.Labels[label] != token {
			t.Error("fixture container identity changed; preserve it")
			return
		}
		if err := cli.ContainerRemove(cleanup, containerID, container.RemoveOptions{Force: true}); err != nil {
			t.Errorf("owned container cleanup: %v", err)
		}
	})
	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.DisconnectNetwork(ctx, networkID, containerID, false); err != nil {
		t.Fatal(err)
	}
	if err := c.ConnectNetwork(ctx, networkID, containerID, []string{"owned-dual-proof"}); err != nil {
		t.Fatal(err)
	}
	detail, err = c.NetworkDetail(ctx, networkID)
	if err != nil || len(detail.Members) != 1 || detail.Members[0].ID != containerID || !slices.Contains(detail.Members[0].Aliases, "owned-dual-proof") {
		t.Fatalf("native membership=%+v %v", detail, err)
	}
	member := detail.Members[0]
	for _, address := range []struct {
		literal string
		prefix  netip.Prefix
	}{{member.IPv4, range4}, {member.IPv6, range6}} {
		p, err := netip.ParsePrefix(address.literal)
		if err != nil || !address.prefix.Contains(p.Addr()) {
			t.Fatalf("native allocation %q is outside %s", address.literal, address.prefix)
		}
	}
	if err := c.DisconnectNetwork(ctx, networkID, containerID, false); err != nil {
		t.Fatal(err)
	}
	detail, err = c.NetworkDetail(ctx, networkID)
	if err != nil || len(detail.Members) != 0 || len(detail.UsedBy) != 0 {
		t.Fatalf("native disconnect=%+v %v", detail, err)
	}
	t.Logf("native bridge accepted exact IPv4/IPv6 pools/gateways/allocation ranges, internal/attachable/IPv6, MTU option and label; connected owned full-ID member with both-family addresses/alias; refused overlap; disconnected; cleanup verifies exact labels/IDs")
}
