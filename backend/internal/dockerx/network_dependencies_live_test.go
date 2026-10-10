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
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// freeFixturePrefixes picks /28s in the benchmarking range that overlap no
// Docker pool and no host route, so the fixture's bridges never shadow an
// existing path.
func freeFixturePrefixes(t *testing.T, ctx context.Context, cli *client.Client, seed byte, n int) []netip.Prefix {
	t.Helper()
	occupied := []netip.Prefix{}
	rows, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil || len(rows) > 512 {
		t.Fatalf("bounded native inventory: %d %v", len(rows), err)
	}
	for _, row := range rows {
		for _, pool := range row.IPAM.Config {
			if p, err := netip.ParsePrefix(pool.Subnet); err == nil {
				occupied = append(occupied, p.Masked())
			}
		}
	}
	output, err := exec.CommandContext(ctx, "ip", "-j", "-4", "route", "show", "table", "all").Output()
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
		if p, err := netip.ParsePrefix(row.Destination); err == nil && p.Bits() > 0 {
			occupied = append(occupied, p.Masked())
		} else if a, err := netip.ParseAddr(row.Destination); err == nil {
			occupied = append(occupied, netip.PrefixFrom(a, 32))
		}
	}
	out := []netip.Prefix{}
	for i := 0; i < 4096 && len(out) < n; i++ {
		p := netip.MustParsePrefix(fmt.Sprintf("198.19.%d.%d/28", (int(seed)+i/16)%256, (i%16)*16))
		if !slices.ContainsFunc(append(occupied, out...), func(o netip.Prefix) bool { return o.Overlaps(p) }) {
			out = append(out, p)
		}
	}
	if len(out) < n {
		t.Fatal("no nonoverlapping owned test pools in the observed host inventory")
	}
	return out
}

// TestLiveNetworkDependencyPreviewsAgainstTheEngine runs the previews over
// owned objects on the actual Engine, and shows the hazard the removal
// preview warns about: the Engine removes a network a stopped container
// still names, and that container then cannot start.
func TestLiveNetworkDependencyPreviewsAgainstTheEngine(t *testing.T) {
	if os.Getenv("JD_DOCKER_NETWORK_LIVE") != "1" {
		t.Skip("set JD_DOCKER_NETWORK_LIVE=1 for exact owned native Docker fixtures")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
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
	prefix := "jd-netdeps-" + token
	label := "jd.network-dependencies"
	pools := freeFixturePrefixes(t, ctx, cli, nonce[0], 2)

	owned := func() (networks, containers []string) {
		list, err := cli.NetworkList(context.Background(), network.ListOptions{Filters: filters.NewArgs(filters.Arg("label", label+"="+token))})
		if err == nil {
			for _, n := range list {
				networks = append(networks, n.Name)
			}
		}
		rows, err := cli.ContainerList(context.Background(), container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", label+"="+token))})
		if err == nil {
			for _, r := range rows {
				containers = append(containers, strings.Join(r.Names, ","))
			}
		}
		return networks, containers
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		rows, err := cli.ContainerList(cleanup, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", label+"="+token))})
		if err != nil {
			t.Errorf("cannot list fixture containers for cleanup: %v", err)
		}
		for _, r := range rows {
			if r.Labels[label] != token || len(r.Names) != 1 || !strings.HasPrefix(strings.TrimPrefix(r.Names[0], "/"), prefix) {
				t.Errorf("fixture container identity changed; preserving %v", r.Names)
				continue
			}
			if err := cli.ContainerRemove(cleanup, r.ID, container.RemoveOptions{Force: true}); err != nil {
				t.Errorf("owned container cleanup: %v", err)
			}
		}
		list, err := cli.NetworkList(cleanup, network.ListOptions{Filters: filters.NewArgs(filters.Arg("label", label+"="+token))})
		if err != nil {
			t.Errorf("cannot list fixture networks for cleanup: %v", err)
		}
		for _, n := range list {
			if n.Labels[label] != token || !strings.HasPrefix(n.Name, prefix) {
				t.Errorf("fixture network identity changed; preserving %s", n.Name)
				continue
			}
			if err := cli.NetworkRemove(cleanup, n.ID); err != nil {
				t.Errorf("owned network cleanup: %v", err)
			}
		}
		if networks, containers := owned(); len(networks)+len(containers) != 0 {
			t.Errorf("owned objects remain after cleanup: networks %v containers %v", networks, containers)
		} else {
			t.Logf("cleanup verified: no network or container labelled %s=%s remains", label, token)
		}
	})

	makeNetwork := func(suffix string, pool netip.Prefix) string {
		made, err := c.CreateNetwork(ctx, NetworkSpec{Name: prefix + "-" + suffix, Driver: "bridge", Internal: true,
			IPAM: []NetworkIPAM{{Subnet: pool.String()}}, Labels: map[string]string{label: token}})
		if err != nil {
			t.Fatal("owned network create:", err)
		}
		return made.ID
	}
	lab := makeNetwork("lab", pools[0])
	dormant := makeNetwork("dormant", pools[1])
	makeContainer := func(suffix, networkID string, aliases []string, start bool) string {
		var endpoints *network.NetworkingConfig
		if len(aliases) > 0 {
			endpoints = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{networkID: {Aliases: aliases}}}
		}
		created, err := cli.ContainerCreate(ctx, &container.Config{Image: "python:3.11-slim", Cmd: []string{"python", "-c", "import time;time.sleep(120)"}, Labels: map[string]string{label: token}},
			&container.HostConfig{NetworkMode: container.NetworkMode(networkID)}, endpoints, nil, prefix+"-"+suffix)
		if err != nil {
			t.Fatal(err)
		}
		if start {
			if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		return created.ID
	}
	member := makeContainer("member", lab, []string{"db"}, true)
	makeContainer("peer", lab, nil, true)
	stopped := makeContainer("stopped", dormant, nil, false)

	d, err := c.NetworkDependencies(ctx, lab, stopped)
	if err != nil {
		t.Fatal(err)
	}
	levels := conflictLevels(PreviewDisconnect(d, member))
	if levels["peers"] != ConflictWarn || levels["last_network"] != ConflictWarn || Blocking(PreviewDisconnect(d, member)) {
		t.Fatalf("native disconnect preview: %+v", levels)
	}
	levels = conflictLevels(PreviewConnect(d, stopped, []string{"db"}))
	if levels["shared_name"] != ConflictWarn || levels["stopped"] != ConflictInfo || Blocking(PreviewConnect(d, stopped, []string{"db"})) {
		t.Fatalf("native connect preview: %+v", levels)
	}
	networks, err := c.ListNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, err := c.listContainerSummaries(ctx, container.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	keep := func(int64) (string, bool) { return "", true }
	var dormantCandidate *PruneCandidate
	for _, candidate := range PruneCandidates(networks, all, "", keep) {
		if candidate.ID == lab {
			t.Fatal("a network with running members is never a prune candidate")
		}
		if candidate.ID == dormant {
			dormantCandidate = &candidate
		}
	}
	if dormantCandidate == nil || dormantCandidate.Removable || !Warning(dormantCandidate.Conflicts) {
		t.Fatalf("the Engine's prune would take the dormant network; the reviewed prune must keep it: %+v", dormantCandidate)
	}
	d, err = c.NetworkDependencies(ctx, dormant)
	if err != nil {
		t.Fatal(err)
	}
	if levels := conflictLevels(PreviewRemove(d, keep)); levels["stopped_dependents"] != ConflictWarn {
		t.Fatalf("native removal preview: %+v", levels)
	}
	// The hazard itself, on the owned network only.
	if err := cli.NetworkRemove(ctx, dormant); err != nil {
		t.Logf("this Engine refuses to remove a network a stopped container names: %v", err)
	} else if err := cli.ContainerStart(ctx, stopped, container.StartOptions{}); err == nil {
		t.Fatal("a container whose network was removed started anyway; the warning would be wrong")
	} else {
		t.Logf("the Engine removed a network a stopped container named, and the container then failed to start: %v", err)
	}
	t.Logf("native previews: disconnect warned of peers and the last network, connect warned of a shared name, the reviewed prune kept the network a stopped container names, removal preview listed it")
}
