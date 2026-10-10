package dockerx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
)

// TestLiveNetworkDriverCatalogueAndIgnoredOption reads the actual Engine's
// catalogue, and shows on one owned internal bridge what the form warns
// about: Docker accepts an option key the bridge driver does not document
// and silently does not apply it.
func TestLiveNetworkDriverCatalogueAndIgnoredOption(t *testing.T) {
	if os.Getenv("JD_DOCKER_NETWORK_LIVE") != "1" {
		t.Skip("set JD_DOCKER_NETWORK_LIVE=1 for exact owned native Docker fixtures")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	c := New("unix:///var/run/docker.sock")
	t.Cleanup(func() { c.Close() })
	cli, err := c.api()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := c.NetworkDrivers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d := driverByName(cat, "bridge"); d == nil || !d.Creatable || d.Source != "builtin" {
		t.Fatalf("native bridge: %+v", d)
	}
	if d := driverByName(cat, "host"); d != nil && d.Creatable {
		t.Fatalf("host is a network mode: %+v", d)
	}
	if d := driverByName(cat, "overlay"); d != nil && cat.Swarm != "active" && d.Creatable {
		t.Fatalf("overlay without an active swarm must be refused: %+v %s", d, cat.Swarm)
	}
	if err := cat.Check(NetworkSpec{Name: "x", Driver: "jd-absent-driver"}, nil); err == nil {
		t.Fatal("an absent driver must be refused before the Engine")
	}
	plugins := []string{}
	for _, d := range cat.Drivers {
		if d.Source == "plugin" {
			plugins = append(plugins, d.Name)
		}
	}
	t.Logf("native catalogue: swarm=%s manager=%v pluginsRead=%v drivers=%d plugin drivers=%v", cat.Swarm, cat.Manager, cat.PluginsRead, len(cat.Drivers), plugins)

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(nonce[:])
	label := "jd.network-driver-options"
	name := "jd-netdrv-" + token
	pools := freeFixturePrefixes(t, ctx, cli, nonce[0], 1)
	spec := NetworkSpec{Name: name, Driver: "bridge", Internal: true, IPAM: []NetworkIPAM{{Subnet: pools[0].String()}},
		Labels: map[string]string{label: token}, Options: map[string]string{"com.docker.network.bridge.mtu": "1400"}}
	if ignored := UnknownDriverOptions(spec.Driver, spec.Options); len(ignored) != 1 {
		t.Fatalf("the fixture's option must be one the bridge does not document: %v", ignored)
	}
	made, err := c.CreateNetwork(ctx, spec)
	if err != nil {
		t.Logf("this Engine refused the undocumented option itself: %v", err)
		return
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		inspected, err := cli.NetworkInspect(cleanup, made.ID, network.InspectOptions{})
		if err != nil || inspected.Name != name || inspected.Labels[label] != token {
			t.Errorf("fixture network identity unverifiable; preserving it: %v", err)
			return
		}
		if err := cli.NetworkRemove(cleanup, made.ID); err != nil {
			t.Errorf("owned network cleanup: %v", err)
		}
		left, err := cli.NetworkList(cleanup, network.ListOptions{Filters: filters.NewArgs(filters.Arg("label", label+"="+token))})
		if err != nil || len(left) != 0 {
			t.Errorf("owned networks remain: %d %v", len(left), err)
		} else {
			t.Logf("cleanup verified: no network labelled %s=%s remains", label, token)
		}
	})
	device := bridgeDevice(made.ID, name, "bridge", nil)
	output, err := exec.CommandContext(ctx, "ip", "-j", "link", "show", "dev", device).Output()
	if err != nil {
		t.Fatalf("the owned bridge %s could not be read: %v", device, err)
	}
	var links []struct {
		MTU int `json:"mtu"`
	}
	if json.Unmarshal(output, &links) != nil || len(links) != 1 {
		t.Fatalf("unreadable link: %s", strings.TrimSpace(string(output)))
	}
	if links[0].MTU == 1400 {
		t.Fatalf("the undocumented key applied an MTU of 1400; the warning would be wrong")
	}
	t.Logf("the Engine accepted the undocumented option %q and the owned bridge %s kept MTU %d", "com.docker.network.bridge.mtu", device, links[0].MTU)
}
