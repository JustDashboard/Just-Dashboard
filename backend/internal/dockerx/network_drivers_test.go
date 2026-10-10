package dockerx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
)

func driverByName(cat NetworkDriverCatalogue, name string) *NetworkDriver {
	for i := range cat.Drivers {
		if cat.Drivers[i].Name == name {
			return &cat.Drivers[i]
		}
	}
	return nil
}

func TestDescribeNetworkDriversSeparatesBuiltinsPluginsAndRefusals(t *testing.T) {
	info := system.Info{Plugins: system.PluginsInfo{Network: []string{"bridge", "host", "ipvlan", "macvlan", "null", "overlay", "weave:latest"}}}
	info.Swarm.LocalNodeState = swarm.LocalNodeStateInactive
	plugins := types.PluginsListResponse{
		{Name: "weave:latest", Enabled: false, Config: types.PluginConfig{Interface: types.PluginConfigInterface{Types: []types.PluginInterfaceType{{Capability: "networkdriver"}}}}},
		{Name: "acme/fabric:latest", Enabled: true, Config: types.PluginConfig{Interface: types.PluginConfigInterface{Types: []types.PluginInterfaceType{{Capability: "networkdriver"}}}}},
		{Name: "acme/volumes:latest", Enabled: true, Config: types.PluginConfig{Interface: types.PluginConfigInterface{Types: []types.PluginInterfaceType{{Capability: "volumedriver"}}}}},
	}
	cat := DescribeNetworkDrivers(info, plugins, nil, time.Unix(0, 0))
	if d := driverByName(cat, "bridge"); d == nil || !d.Creatable || d.Source != "builtin" || len(d.Options) == 0 {
		t.Fatalf("bridge: %+v", d)
	}
	if d := driverByName(cat, "host"); d == nil || d.Creatable {
		t.Fatalf("host is a network mode, not a creatable driver: %+v", d)
	}
	if d := driverByName(cat, "overlay"); d == nil || d.Creatable || !strings.Contains(d.Reason, "swarm manager") {
		t.Fatalf("overlay needs a swarm manager: %+v", d)
	}
	if d := driverByName(cat, "weave:latest"); d == nil || d.Creatable || d.Source != "plugin" {
		t.Fatalf("a disabled plugin is reported and refused: %+v", d)
	}
	if d := driverByName(cat, "acme/fabric"); d == nil || !d.Creatable || d.Source != "plugin" || len(d.Options) != 0 {
		t.Fatalf("an enabled plugin's driver is creatable, with no options vouched for: %+v", d)
	}
	if driverByName(cat, "weave") != nil {
		t.Fatal("a plugin the Engine lists by its tagged name is listed once")
	}
	if driverByName(cat, "acme/volumes") != nil {
		t.Fatal("a volume plugin is not a network driver")
	}
	if !cat.Drivers[0].Creatable {
		t.Fatal("creatable drivers are listed first")
	}
	if err := cat.Check(NetworkSpec{Name: "x", Driver: "macvlan", Options: map[string]string{"parent": "eth0.20"}}, []string{"lo", "eth0"}); err != nil {
		t.Fatalf("a VLAN of an existing parent is allowed: %v", err)
	}
	if err := cat.Check(NetworkSpec{Name: "x", Driver: "ipvlan", Options: map[string]string{"parent": "eth9"}}, []string{"lo", "eth0"}); err == nil {
		t.Fatal("a parent that is not a host device is refused")
	}
	if err := cat.Check(NetworkSpec{Name: "x", Driver: "calico"}, nil); err == nil || !strings.Contains(err.Error(), "no \"calico\" network driver") {
		t.Fatalf("an absent driver is refused before the Engine: %v", err)
	}
	if err := cat.Check(NetworkSpec{Name: "x", Driver: "overlay"}, nil); err == nil {
		t.Fatal("overlay without a swarm is refused")
	}
	if err := cat.Check(NetworkSpec{Name: "x"}, nil); err != nil {
		t.Fatalf("the default bridge driver: %v", err)
	}
	unread := DescribeNetworkDrivers(info, nil, errors.New("plugins endpoint failed"), time.Unix(0, 0))
	if unread.PluginsRead || !strings.Contains(strings.Join(unread.Limitations, " "), "could not be read") {
		t.Fatalf("an unread plugin list is said: %+v", unread)
	}
	info.Swarm.LocalNodeState, info.Swarm.ControlAvailable = swarm.LocalNodeStateActive, true
	if d := driverByName(DescribeNetworkDrivers(info, plugins, nil, time.Unix(0, 0)), "overlay"); d == nil || !d.Creatable || d.Scope != "swarm" {
		t.Fatalf("a swarm manager can create overlay networks: %+v", d)
	}
}

func TestUnknownDriverOptionsAreTheBuiltinTyposOnly(t *testing.T) {
	got := UnknownDriverOptions("", map[string]string{"com.docker.network.driver.mtu": "1400", "com.docker.network.bridge.enable_icc": "false", "com.docker.network.bridge.mtu": "1400"})
	if len(got) != 1 || got[0] != "com.docker.network.bridge.mtu" {
		t.Fatalf("unknown bridge keys: %v", got)
	}
	if got := UnknownDriverOptions("acme/fabric", map[string]string{"anything": "1"}); got != nil {
		t.Fatalf("a plugin's options are its own: %v", got)
	}
}

func TestNetworkDriversReadsInfoAndPlugins(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/v1.47") {
		case "/info":
			_, _ = w.Write([]byte(`{"Plugins":{"Network":["bridge","host","macvlan","null"]},"Swarm":{"LocalNodeState":"inactive"}}`))
		case "/plugins":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected Engine request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer engine.Close()
	api, err := client.NewClientWithOpts(client.WithHost(engine.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	cat, err := (&Client{cli: api}).NetworkDrivers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !cat.PluginsRead || cat.Swarm != "inactive" || driverByName(cat, "macvlan") == nil {
		t.Fatalf("catalogue: %+v", cat)
	}
}
