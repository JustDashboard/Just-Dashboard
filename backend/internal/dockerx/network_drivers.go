package dockerx

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/system"
)

// DriverOption is an option a built-in driver documents. A plugin's options
// are its own and are not listed: the dashboard cannot vouch for them.
type DriverOption struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// NetworkDriver is one driver this Engine can create a network with, or one
// it reports and refuses, with the reason.
type NetworkDriver struct {
	Name string `json:"name"`
	// Source is "builtin" for the drivers the Engine ships and "plugin" for
	// an installed network plugin.
	Source    string         `json:"source"`
	Scope     string         `json:"scope"`
	Creatable bool           `json:"creatable"`
	Reason    string         `json:"reason,omitempty"`
	Options   []DriverOption `json:"options"`
}

// NetworkDriverCatalogue is what the Engine says it can drive, read when
// asked: a driver is a plugin that can be disabled or removed at any time.
type NetworkDriverCatalogue struct {
	Drivers     []NetworkDriver `json:"drivers"`
	Swarm       string          `json:"swarm"`
	Manager     bool            `json:"manager"`
	PluginsRead bool            `json:"pluginsRead"`
	CheckedAt   time.Time       `json:"checkedAt"`
	Limitations []string        `json:"limitations"`
}

// builtinDriverOptions are the driver options Docker documents for the
// drivers it ships. Docker ignores a key a driver does not know, so a typo
// is a setting that silently never applied — which is what the form warns
// about, not something it refuses: a newer Engine adds keys.
var builtinDriverOptions = map[string][]DriverOption{
	"bridge": {
		{"com.docker.network.bridge.name", "Name of the host bridge device"},
		{"com.docker.network.bridge.enable_ip_masquerade", "Masquerade traffic leaving the bridge (true/false)"},
		{"com.docker.network.bridge.enable_icc", "Allow containers on the bridge to reach each other (true/false)"},
		{"com.docker.network.bridge.host_binding_ipv4", "Default host address for published ports"},
		{"com.docker.network.bridge.gateway_mode_ipv4", "nat, nat-unprotected, routed or isolated"},
		{"com.docker.network.bridge.gateway_mode_ipv6", "nat, nat-unprotected, routed or isolated"},
		{"com.docker.network.bridge.inhibit_ipv4", "Leave the IPv4 gateway address off the bridge (true/false)"},
		{"com.docker.network.bridge.trusted_host_interfaces", "Host interfaces allowed to reach containers directly"},
		{"com.docker.network.driver.mtu", "MTU of the containers' interfaces"},
		{"com.docker.network.container_iface_prefix", "Prefix of the containers' interface names"},
	},
	"macvlan": {
		{"parent", "Host interface the network is carried on; parent.VLAN creates a VLAN sub-interface"},
		{"macvlan_mode", "bridge, vepa, passthru or private"},
	},
	"ipvlan": {
		{"parent", "Host interface the network is carried on; parent.VLAN creates a VLAN sub-interface"},
		{"ipvlan_mode", "l2, l3 or l3s"},
		{"ipvlan_flag", "bridge, private or vepa"},
	},
	"overlay": {
		{"encrypted", "Encrypt the VXLAN data plane (IPsec)"},
		{"com.docker.network.driver.overlay.vxlanid_list", "VXLAN IDs to use"},
		{"com.docker.network.driver.mtu", "MTU of the containers' interfaces"},
	},
}

// systemDrivers are network modes rather than drivers a network can be
// created with; NormalizeNetworkSpec refuses them already.
var systemDrivers = []string{"host", "null", "none"}

// NetworkDrivers reads the Engine's drivers and its installed plugins.
func (c *Client) NetworkDrivers(ctx context.Context) (NetworkDriverCatalogue, error) {
	cli, err := c.api()
	if err != nil {
		return NetworkDriverCatalogue{}, err
	}
	info, err := cli.Info(ctx)
	if err != nil {
		return NetworkDriverCatalogue{}, err
	}
	plugins, pluginErr := cli.PluginList(ctx, filters.NewArgs())
	return DescribeNetworkDrivers(info, plugins, pluginErr, time.Now().UTC()), nil
}

// DescribeNetworkDrivers is the catalogue as a pure function of what the
// Engine returned.
func DescribeNetworkDrivers(info system.Info, plugins types.PluginsListResponse, pluginErr error, now time.Time) NetworkDriverCatalogue {
	out := NetworkDriverCatalogue{
		Drivers:     []NetworkDriver{},
		Swarm:       string(info.Swarm.LocalNodeState),
		Manager:     info.Swarm.ControlAvailable,
		PluginsRead: pluginErr == nil,
		CheckedAt:   now,
		Limitations: []string{
			"A plugin driver's options belong to the plugin; this dashboard passes them through without validating them.",
			"Availability is read when asked. A plugin disabled or removed afterwards fails creation at the Engine.",
		},
	}
	if out.Swarm == "" {
		out.Swarm = string(swarm.LocalNodeStateInactive)
	}
	if pluginErr != nil {
		out.Limitations = append(out.Limitations, "The Engine's plugin list could not be read, so a disabled plugin is not told apart from an enabled one: "+pluginErr.Error())
	}
	seen := map[string]bool{}
	add := func(d NetworkDriver) {
		if seen[d.Name] {
			return
		}
		seen[d.Name] = true
		if d.Options == nil {
			d.Options = []DriverOption{}
		}
		out.Drivers = append(out.Drivers, d)
	}
	enabled := map[string]bool{}
	for _, p := range plugins {
		if p == nil {
			continue
		}
		network := slices.ContainsFunc(p.Config.Interface.Types, func(t types.PluginInterfaceType) bool { return t.Capability == "networkdriver" })
		if !network {
			continue
		}
		name := p.Name
		enabled[name] = p.Enabled
		enabled[strings.TrimSuffix(name, ":latest")] = p.Enabled
	}
	for _, name := range info.Plugins.Network {
		d := NetworkDriver{Name: name, Source: "builtin", Scope: "local", Creatable: true}
		if options, ok := builtinDriverOptions[name]; ok {
			d.Options = options
		} else if !slices.Contains(systemDrivers, name) && name != "bridge" {
			d.Source = "plugin"
		}
		switch {
		case slices.Contains(systemDrivers, name):
			d.Creatable, d.Reason = false, "A network mode chosen when a container is created, not a driver for a new network."
		case name == "overlay":
			d.Scope = "swarm"
			if out.Swarm != string(swarm.LocalNodeStateActive) || !out.Manager {
				d.Creatable, d.Reason = false, "Overlay networks span a swarm; this Engine is not a swarm manager."
			}
		case d.Source == "plugin":
			if on, known := enabled[name]; known && !on {
				d.Creatable, d.Reason = false, "The plugin is installed but disabled."
			}
		}
		add(d)
	}
	for name, on := range enabled {
		if strings.HasSuffix(name, ":latest") || seen[name] || seen[name+":latest"] {
			continue
		}
		d := NetworkDriver{Name: name, Source: "plugin", Scope: "local", Creatable: on}
		if !on {
			d.Reason = "The plugin is installed but disabled."
		}
		add(d)
	}
	sort.SliceStable(out.Drivers, func(i, j int) bool {
		if out.Drivers[i].Creatable != out.Drivers[j].Creatable {
			return out.Drivers[i].Creatable
		}
		return out.Drivers[i].Name < out.Drivers[j].Name
	})
	return out
}

// Check refuses a spec this Engine cannot create, in a sentence, before the
// Engine is asked. interfaces are the host's device names, for the parent a
// macvlan or ipvlan network hangs off.
func (cat NetworkDriverCatalogue) Check(spec NetworkSpec, interfaces []string) error {
	driver := spec.Driver
	if driver == "" {
		driver = "bridge"
	}
	var found *NetworkDriver
	for i := range cat.Drivers {
		if cat.Drivers[i].Name == driver {
			found = &cat.Drivers[i]
		}
	}
	if found == nil {
		return fmt.Errorf("this Engine has no %q network driver; install and enable its plugin first", driver)
	}
	if !found.Creatable {
		return fmt.Errorf("the %s driver cannot create a network here: %s", driver, strings.TrimSuffix(found.Reason, "."))
	}
	if parent := spec.Options["parent"]; parent != "" && (driver == "macvlan" || driver == "ipvlan") {
		device, _, _ := strings.Cut(parent, ".")
		if !slices.Contains(interfaces, device) {
			return fmt.Errorf("the parent interface %q is not a device on this host", device)
		}
	}
	return nil
}

// UnknownDriverOptions are the keys a built-in driver does not document.
// Docker ignores them, so each is a setting that would silently not apply.
// A plugin's options are its own and are never reported.
func UnknownDriverOptions(driver string, options map[string]string) []string {
	if driver == "" {
		driver = "bridge"
	}
	known, ok := builtinDriverOptions[driver]
	if !ok {
		return nil
	}
	out := []string{}
	for key := range options {
		if !slices.ContainsFunc(known, func(o DriverOption) bool { return o.Key == key }) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
