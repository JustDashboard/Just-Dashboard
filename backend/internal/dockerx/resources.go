package dockerx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
)

// This file completes the two halves of Docker the dashboard could previously
// only look at: volumes and networks could be listed and deleted, never made.
// A panel that can delete but not create is not a management tool, it is a
// viewer with a bin — and it pushed anyone who needed a network for two
// containers to talk to each other straight back to a shell.
//
// Both types also carry a "what is using this" answer here rather than in the
// UI, because the honest version of it needs a pass over every container and
// doing that in the browser means one request per row.

// VolumeSpec is a volume to create. Almost always just a name: the local
// driver is right unless somebody is deliberately doing something else, and
// the UI does not ask about drivers until asked to.
type VolumeSpec struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

func (c *Client) CreateVolume(ctx context.Context, spec VolumeSpec) (*Volume, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, errors.New("a volume name is required")
	}
	if !validResourceName(name) {
		return nil, errors.New("a volume name may contain letters, digits, and _ . - after the first character")
	}
	driver := spec.Driver
	if driver == "" {
		driver = "local"
	}
	v, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: name, Driver: driver, Labels: spec.Labels, DriverOpts: spec.Options,
	})
	if err != nil {
		return nil, err
	}
	// The disk-usage cache now describes a world without this volume in it.
	c.invalidateDiskUsage()
	return &Volume{
		Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint,
		CreatedAt: v.CreatedAt, Scope: v.Scope, Labels: v.Labels, RefCount: 0,
	}, nil
}

// VolumeDetail is a volume plus the only two things anybody opens it to find
// out: where its data actually is on the server, and whether deleting it will
// break something.
type VolumeDetail struct {
	Volume
	// UsedBy names the containers mounting it, running or not, with the path
	// each one sees it at. Docker's own RefCount counts running containers
	// only, so a volume belonging to a stopped stack reads as unused and is
	// exactly the one an operator prunes by accident.
	UsedBy []VolumeUser `json:"usedBy"`
	// Options and the driver, for the volumes that are not plain local ones.
	Options map[string]string `json:"options,omitempty"`
}

type VolumeUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
	Stack       string `json:"stack,omitempty"`
}

func (c *Client) VolumeDetail(ctx context.Context, name string) (*VolumeDetail, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	v, err := cli.VolumeInspect(ctx, name)
	if err != nil {
		return nil, err
	}
	d := &VolumeDetail{
		Volume: Volume{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint,
			CreatedAt: v.CreatedAt, Scope: v.Scope, Labels: v.Labels, RefCount: -1,
		},
		UsedBy:  []VolumeUser{},
		Options: v.Options,
	}
	if du := c.diskUsage(ctx); du != nil {
		for _, entry := range du.Volumes {
			if entry.Name == v.Name && entry.UsageData != nil {
				d.Size = entry.UsageData.Size
				d.RefCount = entry.UsageData.RefCount
				d.InUse = entry.UsageData.RefCount > 0
			}
		}
	}
	users, err := c.volumeUsers(ctx)
	if err == nil {
		d.UsedBy = users[v.Name]
		if d.UsedBy == nil {
			d.UsedBy = []VolumeUser{}
		}
		// A stopped container still counts as a user for the purpose of "is
		// this safe to delete", which is the only question being asked.
		d.InUse = d.InUse || len(d.UsedBy) > 0
	}
	return d, nil
}

// volumeUsers maps every volume name to the containers mounting it.
//
// One listing, not an inspect per container: the Engine's container summary
// already carries the mounts, and a host with sixty containers would otherwise
// spend sixty round trips on the socket every time the volumes tab polls.
func (c *Client) volumeUsers(ctx context.Context) (map[string][]VolumeUser, error) {
	list, err := c.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	out := map[string][]VolumeUser{}
	for _, ct := range list {
		for _, m := range ct.Mounts {
			if m.Type != "volume" || m.Name == "" {
				continue
			}
			out[m.Name] = append(out[m.Name], VolumeUser{
				ID: ct.ID, Name: ct.Name, State: ct.State,
				Destination: m.Destination, ReadOnly: !m.RW, Stack: ct.ComposeStack,
			})
		}
	}
	return out, nil
}

// ListVolumesWithUsers is the volumes tab's query: the list, plus who is using
// each one. The join happens here because doing it in the browser is one
// request per volume, and because "unused" is the word the delete button is
// keyed off — it had better mean what it says.
func (c *Client) ListVolumesWithUsers(ctx context.Context) ([]VolumeDetail, error) {
	vols, err := c.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	users, _ := c.volumeUsers(ctx)
	out := make([]VolumeDetail, 0, len(vols))
	for _, v := range vols {
		d := VolumeDetail{Volume: v, UsedBy: users[v.Name]}
		if d.UsedBy == nil {
			d.UsedBy = []VolumeUser{}
		}
		d.InUse = d.InUse || len(d.UsedBy) > 0
		out = append(out, d)
	}
	return out, nil
}

// NetworkSpec is a network to create.
//
// The defaults are the useful ones: a bridge network with Docker choosing the
// subnet, which is what "let these two containers talk to each other" needs
// and is the answer 95% of the time. Everything else is here for the case
// where it is not.
type NetworkSpec struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver,omitempty"`
	Subnet     string            `json:"subnet,omitempty"`
	Gateway    string            `json:"gateway,omitempty"`
	IPRange    string            `json:"ipRange,omitempty"`
	Internal   bool              `json:"internal,omitempty"`
	Attachable bool              `json:"attachable,omitempty"`
	IPv6       bool              `json:"ipv6,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Options    map[string]string `json:"options,omitempty"`
}

func (c *Client) CreateNetwork(ctx context.Context, spec NetworkSpec) (*Network, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, errors.New("a network name is required")
	}
	if !validResourceName(name) {
		return nil, errors.New("a network name may contain letters, digits, and _ . - after the first character")
	}
	driver := spec.Driver
	if driver == "" {
		driver = "bridge"
	}
	opts := network.CreateOptions{
		Driver:     driver,
		Internal:   spec.Internal,
		Attachable: spec.Attachable,
		EnableIPv6: &spec.IPv6,
		Labels:     spec.Labels,
		Options:    spec.Options,
	}
	if spec.Subnet != "" {
		cfg := network.IPAMConfig{Subnet: spec.Subnet, Gateway: spec.Gateway, IPRange: spec.IPRange}
		opts.IPAM = &network.IPAM{Driver: "default", Config: []network.IPAMConfig{cfg}}
	}
	res, err := cli.NetworkCreate(ctx, name, opts)
	if err != nil {
		return nil, err
	}
	insp, err := cli.NetworkInspect(ctx, res.ID, network.InspectOptions{})
	if err != nil {
		return &Network{ID: res.ID, Name: name, Driver: driver}, nil
	}
	out := &Network{
		ID: insp.ID, Name: insp.Name, Driver: insp.Driver, Scope: insp.Scope,
		Internal: insp.Internal, Attachable: insp.Attachable, IPv6: insp.EnableIPv6,
		Created: insp.Created.UTC(), Labels: insp.Labels, Subnets: []string{},
	}
	for _, cfg := range insp.IPAM.Config {
		if cfg.Subnet != "" {
			out.Subnets = append(out.Subnets, cfg.Subnet)
		}
	}
	return out, nil
}

// NetworkDetail is a network and who is on it.
//
// The member list is the point. "Two containers cannot see each other" is the
// most common Docker problem there is, and its answer is almost always that
// they are on different networks — a fact the Engine will tell you and no
// dashboard in this class puts on screen next to the name each container
// answers to.
type NetworkDetail struct {
	Network
	Gateway string            `json:"gateway,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Members []NetworkMember   `json:"members"`
	// System marks bridge, host and none: the three networks Docker creates
	// and will not let you remove, so the UI can say why rather than offering
	// a button that always fails.
	System bool `json:"system"`
}

type NetworkMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// IPv4 is the address other containers on this network reach it at, and
	// Aliases are the names DNS resolves to it — which is what you actually
	// put in a connection string.
	IPv4    string   `json:"ipv4,omitempty"`
	IPv6    string   `json:"ipv6,omitempty"`
	MAC     string   `json:"mac,omitempty"`
	Aliases []string `json:"aliases"`
	State   string   `json:"state,omitempty"`
	Stack   string   `json:"stack,omitempty"`
}

func (c *Client) NetworkDetail(ctx context.Context, id string) (*NetworkDetail, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	insp, err := cli.NetworkInspect(ctx, id, network.InspectOptions{Verbose: true})
	if err != nil {
		return nil, err
	}
	d := &NetworkDetail{
		Network: Network{
			ID: insp.ID, Name: insp.Name, Driver: insp.Driver, Scope: insp.Scope,
			Internal: insp.Internal, Attachable: insp.Attachable, IPv6: insp.EnableIPv6,
			Created: insp.Created.UTC(), Labels: insp.Labels, Subnets: []string{},
			Containers: len(insp.Containers),
			// The list view fills UsedBy by joining against the containers;
			// this route never did, so it went out as `null` and the detail
			// panel's `usedBy.length` took the page down. Members below carries
			// the same answer in more detail, and this stays a list.
			UsedBy: []string{},
		},
		Options: insp.Options,
		Members: []NetworkMember{},
		System:  IsSystemNetwork(insp.Name),
	}
	for _, cfg := range insp.IPAM.Config {
		if cfg.Subnet != "" {
			d.Subnets = append(d.Subnets, cfg.Subnet)
		}
		if cfg.Gateway != "" && d.Gateway == "" {
			d.Gateway = cfg.Gateway
		}
	}
	// Aliases and state are not in the network inspect, only in the
	// container's — which is why this joins rather than reads one endpoint.
	state := map[string]Container{}
	if list, err := c.ListContainers(ctx, true); err == nil {
		for _, ct := range list {
			state[ct.ID] = ct
		}
	}
	for memberID, ep := range insp.Containers {
		m := NetworkMember{
			ID: memberID, Name: strings.TrimPrefix(ep.Name, "/"),
			IPv4: ep.IPv4Address, IPv6: ep.IPv6Address, MAC: ep.MacAddress,
			Aliases: []string{},
		}
		if ct, ok := state[memberID]; ok {
			m.State = ct.State
			m.Stack = ct.ComposeStack
		}
		if member, err := cli.ContainerInspect(ctx, memberID); err == nil && member.NetworkSettings != nil {
			if eps := member.NetworkSettings.Networks[d.Name]; eps != nil {
				m.Aliases = append(m.Aliases, eps.Aliases...)
			}
		}
		d.Members = append(d.Members, m)
		d.UsedBy = append(d.UsedBy, m.Name)
	}
	sort.Slice(d.Members, func(i, j int) bool { return d.Members[i].Name < d.Members[j].Name })
	return d, nil
}

// IsSystemNetwork reports the three networks the Engine owns. Removing one is
// not a permissions question — Docker refuses outright — so the UI hides the
// action rather than letting it fail.
func IsSystemNetwork(name string) bool {
	return name == "bridge" || name == "host" || name == "none"
}

// ConnectNetwork attaches a running or stopped container to a network.
//
// Aliases are the reason this is worth having in a UI at all: they are the
// hostnames the other containers on the network use, and getting one wrong is
// the difference between `postgres:5432` resolving and not.
func (c *Client) ConnectNetwork(ctx context.Context, networkID, containerID string, aliases []string) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	var settings *network.EndpointSettings
	if len(aliases) > 0 {
		settings = &network.EndpointSettings{Aliases: aliases}
	}
	return cli.NetworkConnect(ctx, networkID, containerID, settings)
}

func (c *Client) DisconnectNetwork(ctx context.Context, networkID, containerID string, force bool) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	return cli.NetworkDisconnect(ctx, networkID, containerID, force)
}

// PruneNetworks is exported so the networks tab can offer it next to the list
// it applies to, rather than only as part of "prune everything".
func (c *Client) PruneNetworks(ctx context.Context) (PruneReport, error) {
	return c.pruneNetworks(ctx)
}

// invalidateDiskUsage drops the cached walk after an operation that changed
// what is on disk, so the figure the operator sees next is not the one from
// before they acted.
func (c *Client) invalidateDiskUsage() {
	c.duMu.Lock()
	c.duVal, c.duAt = nil, time.Time{}
	c.duMu.Unlock()
}

// validResourceName matches what the Engine accepts for volumes and networks:
// [a-zA-Z0-9][a-zA-Z0-9_.-]*. Checked here so the operator gets a sentence
// rather than a 500 from the daemon.
func validResourceName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '_' || r == '.' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// SuggestVolumeName turns a container name and a path into the name a person
// would have picked — `plausible-db` mounting /var/lib/postgresql/data
// becomes `plausible-db-data`. Small, and the difference between a volume list
// you can read and forty hex strings.
func SuggestVolumeName(container, target string) string {
	base := strings.Trim(target, "/")
	if base == "" {
		return container
	}
	parts := strings.Split(base, "/")
	last := parts[len(parts)-1]
	// A trailing "data" or "db" is more useful than the full path, but a
	// generic last segment on its own is not.
	if last == "" {
		last = "data"
	}
	name := fmt.Sprintf("%s-%s", container, last)
	if validResourceName(name) {
		return name
	}
	return container
}
