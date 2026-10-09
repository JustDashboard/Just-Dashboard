package dockerx

import (
	"context"
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

// Conflict levels. A block is refused by the backend as well as shown; a
// warning is a consequence the operator confirms; a note changes nothing
// about whether to go ahead.
const (
	ConflictBlock = "block"
	ConflictWarn  = "warn"
	ConflictInfo  = "info"
)

// NetworkConflict is one thing a connect, a disconnect or a removal would run
// into, said before it happens rather than read off a failure afterwards.
type NetworkConflict struct {
	Code     string   `json:"code"`
	Level    string   `json:"level"`
	Message  string   `json:"message"`
	Subjects []string `json:"subjects,omitempty"`
}

// Blocking reports whether any conflict refuses the change.
func Blocking(conflicts []NetworkConflict) bool {
	return slices.ContainsFunc(conflicts, func(c NetworkConflict) bool { return c.Level == ConflictBlock })
}

// Warning reports whether any conflict is a consequence to confirm.
func Warning(conflicts []NetworkConflict) bool {
	return slices.ContainsFunc(conflicts, func(c NetworkConflict) bool { return c.Level == ConflictWarn })
}

// Endpoint is a container's attachment to one network, as its own inspect
// reports it: the aliases live there and nowhere else.
type Endpoint struct {
	IPv4    string   `json:"ipv4,omitempty"`
	IPv6    string   `json:"ipv6,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	Aliases []string `json:"aliases"`
}

// DependentContainer is a container as the dependency checks need it: the
// listing's summary, and for the members and the container being changed,
// what an inspect adds.
type DependentContainer struct {
	Container
	NetworkMode string              `json:"networkMode,omitempty"`
	Endpoints   map[string]Endpoint `json:"endpoints,omitempty"`
	Inspected   bool                `json:"inspected"`
}

// NetworkDependencies is everything a change to one network can disturb,
// read in one pass: the network, every container (stopped ones keep naming
// the networks they will rejoin), every network's flags, and the members'
// own inspects. A failed read of the containers or of the network list fails
// the whole reading: a preview that could not see the dependents must not
// say there are none.
type NetworkDependencies struct {
	Network    network.Inspect
	Containers []DependentContainer
	Networks   map[string]Network
	// Unread names the containers whose inspection failed; their aliases and
	// network modes are unknown, which the previews say.
	Unread []string
	// SelfProject is the dashboard's own Compose project, set by the caller,
	// which is the one that knows where the dashboard keeps its data.
	SelfProject string
	// refs maps each inspected reference to the full ID it resolved to.
	refs map[string]string
}

// NetworkDependencies reads a network's dependents. extra names containers
// to inspect besides the members — the one a connect would attach.
func (c *Client) NetworkDependencies(ctx context.Context, id string, extra ...string) (*NetworkDependencies, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	insp, err := cli.NetworkInspect(ctx, id, network.InspectOptions{Verbose: true})
	if err != nil {
		return nil, err
	}
	containers, err := c.listContainerSummaries(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("the containers that depend on %s could not be listed: %w", insp.Name, err)
	}
	networks, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("the networks could not be listed: %w", err)
	}
	d := &NetworkDependencies{Network: insp, Networks: map[string]Network{}, refs: map[string]string{}}
	for _, n := range networks {
		brief := Network{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Labels: n.Labels, Subnets: []string{}}
		for _, cfg := range n.IPAM.Config {
			if cfg.Subnet != "" {
				brief.Subnets = append(brief.Subnets, cfg.Subnet)
			}
		}
		d.Networks[n.Name] = brief
	}
	wanted := map[string]bool{}
	for memberID := range insp.Containers {
		wanted[memberID] = true
	}
	for _, ref := range extra {
		if ref != "" {
			wanted[ref] = true
		}
	}
	index := map[string]int{}
	for _, ct := range containers {
		index[ct.ID] = len(d.Containers)
		d.Containers = append(d.Containers, DependentContainer{Container: ct})
	}
	refs := make([]string, 0, len(wanted))
	for ref := range wanted {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		detail, err := cli.ContainerInspect(ctx, ref)
		if err != nil {
			d.Unread = append(d.Unread, ref)
			continue
		}
		i, ok := index[detail.ID]
		if !ok {
			// Created between the listing and the inspect: it still counts,
			// with the labels its owner checks read from its own inspect.
			i = len(d.Containers)
			index[detail.ID] = i
			c := Container{ID: detail.ID, Name: strings.TrimPrefix(detail.Name, "/"), Labels: map[string]string{}, Networks: []string{}}
			if detail.Config != nil {
				c.Image = detail.Config.Image
				for k, v := range detail.Config.Labels {
					c.Labels[k] = v
				}
				c.ComposeStack, c.ComposeSvc = c.Labels["com.docker.compose.project"], c.Labels["com.docker.compose.service"]
			}
			if detail.State != nil {
				c.State = detail.State.Status
			}
			d.Containers = append(d.Containers, DependentContainer{Container: c})
		}
		// The reference asked for resolves to this container, whatever
		// form it took — a name, a short ID — so the previews judge the
		// container the Engine would act on.
		d.refs[ref] = detail.ID
		dc := &d.Containers[i]
		dc.Inspected = true
		dc.Endpoints = map[string]Endpoint{}
		if detail.HostConfig != nil {
			dc.NetworkMode = string(detail.HostConfig.NetworkMode)
		}
		if detail.NetworkSettings != nil {
			for name, ep := range detail.NetworkSettings.Networks {
				if ep == nil {
					continue
				}
				dc.Endpoints[name] = Endpoint{IPv4: ep.IPAddress, IPv6: ep.GlobalIPv6Address, Gateway: ep.Gateway, Aliases: append([]string{}, ep.Aliases...)}
			}
		}
	}
	return d, nil
}

// Container finds a container by full ID, unambiguous ID prefix or name.
func (d *NetworkDependencies) Container(ref string) *DependentContainer {
	if ref == "" {
		return nil
	}
	if id, ok := d.refs[ref]; ok {
		ref = id
	}
	var found *DependentContainer
	for i := range d.Containers {
		c := &d.Containers[i]
		if c.ID == ref || c.Name == ref || slices.Contains(c.Names, ref) {
			return c
		}
		if len(ref) >= 12 && strings.HasPrefix(c.ID, ref) {
			if found != nil {
				return nil
			}
			found = c
		}
	}
	return found
}

// Owner is the network's owner as its labels say.
func (d *NetworkDependencies) Owner() NetworkOwner {
	return OwnerOfNetwork(d.Network.Name, d.Network.Labels, d.SelfProject)
}

func (d *NetworkDependencies) member(id string) bool {
	_, ok := d.Network.Containers[id]
	return ok
}

// attached says whether a container names this network: as a running
// endpoint, or in the configuration a stopped one rejoins on start.
func (d *NetworkDependencies) attached(c *DependentContainer) bool {
	if d.member(c.ID) {
		return true
	}
	if _, ok := c.Endpoints[d.Network.Name]; ok {
		return true
	}
	return slices.Contains(c.Networks, d.Network.Name)
}

func (d *NetworkDependencies) dashboard(c *DependentContainer) bool {
	return d.SelfProject != "" && c.ComposeStack == d.SelfProject
}

// names is every DNS name a member answers to on this network: its name and
// the aliases on its endpoint. The short ID Docker adds is left out; nobody
// writes it into a connection string.
func (d *NetworkDependencies) names(c *DependentContainer) []string {
	out := []string{c.Name}
	if ep, ok := c.Endpoints[d.Network.Name]; ok {
		for _, alias := range ep.Aliases {
			if alias != "" && alias != c.Name && !strings.HasPrefix(c.ID, alias) && !slices.Contains(out, alias) {
				out = append(out, alias)
			}
		}
	}
	return out
}

func (d *NetworkDependencies) unreadConflict(subject string) []NetworkConflict {
	if len(d.Unread) == 0 {
		return nil
	}
	return []NetworkConflict{{Code: "unread_members", Level: ConflictWarn,
		Message:  fmt.Sprintf("%s failed, so %s that members hold on this network may be incomplete.", plural(len(d.Unread), "container inspection", "container inspections"), subject),
		Subjects: d.Unread}}
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// PreviewConnect says what attaching a container with these aliases runs
// into. Docker would refuse the blocks itself, except the dashboard's own
// network, which is this dashboard's policy.
func PreviewConnect(d *NetworkDependencies, candidate string, aliases []string) []NetworkConflict {
	name := d.Network.Name
	out := []NetworkConflict{}
	if name == "host" || name == "none" {
		return append(out, NetworkConflict{Code: "system_network", Level: ConflictBlock,
			Message: fmt.Sprintf("Docker does not attach containers to the %s network; it is a network mode chosen when a container is created.", name)})
	}
	c := d.Container(candidate)
	if c == nil || !c.Inspected {
		return append(out, NetworkConflict{Code: "candidate_unread", Level: ConflictBlock,
			Message: "That container could not be inspected, so its network mode and current attachments are unknown. Nothing was attached."})
	}
	if d.attached(c) {
		out = append(out, NetworkConflict{Code: "already_attached", Level: ConflictBlock, Message: fmt.Sprintf("%s is already on %s.", c.Name, name)})
	}
	switch mode := c.NetworkMode; {
	case mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:"):
		out = append(out, NetworkConflict{Code: "network_mode", Level: ConflictBlock,
			Message: fmt.Sprintf("%s runs with %s networking, which shares another network stack or has none; Docker refuses to attach it to a network.", c.Name, mode)})
	}
	if d.Network.Scope == "swarm" && !d.Network.Attachable {
		out = append(out, NetworkConflict{Code: "not_attachable", Level: ConflictBlock,
			Message: fmt.Sprintf("%s is a swarm network created without --attachable; only swarm services can join it.", name)})
	}
	owner := d.Owner()
	switch owner.Kind {
	case NetworkOwnerDashboard:
		out = append(out, NetworkConflict{Code: "dashboard_network", Level: ConflictBlock,
			Message: fmt.Sprintf("%s is the dashboard's own private network. Another container on it would reach the dashboard's services directly, past Caddy and the network allowlist.", name)})
	case NetworkOwnerDatabaseLink:
		out = append(out, NetworkConflict{Code: "managed_network", Level: ConflictWarn,
			Message: fmt.Sprintf("%s carries a deployment's database links. Attaching %s gives it a network path to those databases; the deployment reconciles its own members and leaves this one in place.", name, c.Name)})
	case NetworkOwnerDeployment:
		out = append(out, NetworkConflict{Code: "managed_network", Level: ConflictWarn,
			Message: fmt.Sprintf("%s belongs to a deployment. Attaching %s gives it a path to that deployment's containers.", name, c.Name)})
	}
	for _, alias := range aliases {
		if len(alias) > 253 || !aliasPattern.MatchString(alias) {
			out = append(out, NetworkConflict{Code: "invalid_alias", Level: ConflictBlock, Subjects: []string{alias},
				Message: fmt.Sprintf("%q is not a name Docker's resolver can answer: use letters, digits, '.', '_' and '-'.", alias)})
		}
	}
	if name == "bridge" && len(aliases) > 0 {
		out = append(out, NetworkConflict{Code: "alias_on_default_bridge", Level: ConflictBlock,
			Message: "The default bridge has no embedded DNS, so Docker refuses network-scoped aliases on it. Use a network you created."})
	}
	// Two containers answering one name is not refused by Docker: its
	// resolver returns both, and clients get whichever comes first.
	holders := map[string][]string{}
	for i := range d.Containers {
		m := &d.Containers[i]
		if m.ID == c.ID || !d.member(m.ID) {
			continue
		}
		for _, n := range d.names(m) {
			holders[strings.ToLower(n)] = append(holders[strings.ToLower(n)], m.Name)
		}
	}
	if name != "bridge" {
		for _, n := range append([]string{c.Name}, aliases...) {
			if held := holders[strings.ToLower(n)]; len(held) > 0 {
				out = append(out, NetworkConflict{Code: "shared_name", Level: ConflictWarn, Subjects: []string{n},
					Message: fmt.Sprintf("On %s, %s already answers for %s. Docker's resolver would return both containers for that name, so a client gets either one.", name, n, strings.Join(held, ", "))})
			}
		}
	}
	for other := range c.Endpoints {
		if other == name {
			continue
		}
		if overlap := overlappingSubnets(d.Networks[other].Subnets, subnetsOf(d.Network)); overlap != "" {
			out = append(out, NetworkConflict{Code: "subnet_overlap", Level: ConflictWarn, Subjects: []string{other},
				Message: fmt.Sprintf("%s is also on %s, whose range overlaps %s (%s). With two interfaces in one range, which one a reply leaves by is decided by route order, not by the name a client used.", c.Name, other, name, overlap)})
		}
	}
	if free, known := freeAddresses(d); known && free <= 0 {
		out = append(out, NetworkConflict{Code: "pool_exhausted", Level: ConflictBlock,
			Message: fmt.Sprintf("%s has no free IPv4 address left in its range for another member.", name)})
	}
	if c.State != "running" {
		out = append(out, NetworkConflict{Code: "stopped", Level: ConflictInfo,
			Message: fmt.Sprintf("%s is %s; it joins %s when it next starts.", c.Name, orState(c.State), name)})
	}
	return append(out, d.unreadConflict("the names")...)
}

// PreviewDisconnect says who loses what when a member leaves the network.
// The dashboard's own containers, the shared ingress and a deployment's
// database-link members are refused: each has an owner that put it there.
func PreviewDisconnect(d *NetworkDependencies, member string) []NetworkConflict {
	name := d.Network.Name
	out := []NetworkConflict{}
	c := d.Container(member)
	if c == nil || !d.attached(c) {
		return append(out, NetworkConflict{Code: "not_attached", Level: ConflictBlock, Message: fmt.Sprintf("That container is not on %s.", name)})
	}
	if name == "host" || name == "none" {
		return append(out, NetworkConflict{Code: "system_network", Level: ConflictBlock,
			Message: fmt.Sprintf("A container on the %s network chose it as its network mode at creation; it cannot be detached.", name)})
	}
	owner := d.Owner()
	if d.dashboard(c) {
		out = append(out, NetworkConflict{Code: "dashboard_container", Level: ConflictBlock, Subjects: []string{c.Name},
			Message: fmt.Sprintf("%s is part of the dashboard itself (%s). Detaching it from %s would cut the dashboard's own service path; change the dashboard's compose file instead.", c.Name, c.ComposeStack, name)})
	}
	if IsIngressContainer(c.Container) {
		out = append(out, NetworkConflict{Code: "ingress", Level: ConflictBlock, Subjects: []string{c.Name},
			Message: fmt.Sprintf("%s is the shared public Caddy. Deployment routes reach their containers through its network memberships, and the route reconciler puts a lost one back within 30 seconds.", c.Name)})
	}
	switch owner.Kind {
	case NetworkOwnerDatabaseLink:
		out = append(out, NetworkConflict{Code: "managed_membership", Level: ConflictBlock,
			Message: fmt.Sprintf("%s is a deployment's database network, and its members are that deployment's database links. Change the links in the deployment instead.", name)})
	case NetworkOwnerDeployment:
		out = append(out, NetworkConflict{Code: "managed_network", Level: ConflictWarn,
			Message: fmt.Sprintf("%s belongs to a deployment; its next deploy puts the attachment back.", name)})
	case NetworkOwnerCompose:
		if c.ComposeStack != "" && c.ComposeStack == owner.Project {
			out = append(out, NetworkConflict{Code: "compose_restores", Level: ConflictInfo,
				Message: fmt.Sprintf("Compose puts %s back on %s the next time %s comes up.", c.Name, name, owner.Project)})
		}
	}
	others, external := 0, 0
	if c.Inspected {
		for other := range c.Endpoints {
			if other == name {
				continue
			}
			others++
			if n, ok := d.Networks[other]; !ok || !n.Internal {
				external++
			}
		}
	} else {
		for _, other := range c.Networks {
			if other == name {
				continue
			}
			others++
			if n, ok := d.Networks[other]; !ok || !n.Internal {
				external++
			}
		}
	}
	if others == 0 {
		out = append(out, NetworkConflict{Code: "last_network", Level: ConflictWarn,
			Message: fmt.Sprintf("%s is on no other network; it keeps only its loopback interface.", c.Name)})
	}
	if published := publishedPorts(c.Ports); len(published) > 0 && !d.Network.Internal {
		if external == 0 {
			out = append(out, NetworkConflict{Code: "published_ports", Level: ConflictWarn, Subjects: published,
				Message: fmt.Sprintf("Its published ports (%s) are carried on this network, its only one with a way out, and stop answering until it is reattached.", strings.Join(published, ", "))})
		} else {
			out = append(out, NetworkConflict{Code: "published_ports", Level: ConflictWarn, Subjects: published,
				Message: fmt.Sprintf("Its published ports (%s) stop answering if Docker carries them on this network rather than on its other one.", strings.Join(published, ", "))})
		}
	}
	peers := []string{}
	for i := range d.Containers {
		m := &d.Containers[i]
		if m.ID != c.ID && d.member(m.ID) {
			peers = append(peers, m.Name)
		}
	}
	sort.Strings(peers)
	if len(peers) > 0 {
		out = append(out, NetworkConflict{Code: "peers", Level: ConflictWarn, Subjects: peers,
			Message: fmt.Sprintf("%s reach%s %s on %s as %s; those names stop resolving for them and connections open over this network are cut.", strings.Join(peers, ", "), singular(len(peers)), c.Name, name, strings.Join(d.names(c), ", "))})
	}
	return append(out, d.unreadConflict("the names")...)
}

// PreviewRemove says what removing the network leaves broken. environment
// names a deployment environment that still exists, so a dashboard-managed
// network whose deployment is gone can be cleaned up while one still in use
// is refused.
func PreviewRemove(d *NetworkDependencies, environment func(int64) (string, bool)) []NetworkConflict {
	name := d.Network.Name
	out := []NetworkConflict{}
	if IsSystemNetwork(name) {
		return append(out, NetworkConflict{Code: "system_network", Level: ConflictBlock, Message: fmt.Sprintf("%s is one of Docker's own networks; the Engine never removes it.", name)})
	}
	running := []string{}
	stopped := []string{}
	for i := range d.Containers {
		c := &d.Containers[i]
		switch {
		case d.member(c.ID):
			running = append(running, c.Name)
		case d.attached(c):
			stopped = append(stopped, c.Name)
		}
	}
	sort.Strings(running)
	sort.Strings(stopped)
	if len(running) > 0 {
		out = append(out, NetworkConflict{Code: "in_use", Level: ConflictBlock, Subjects: running,
			Message: fmt.Sprintf("%s %s attached: %s. Docker refuses to remove a network with attached containers.", plural(len(running), "container", "containers"), areIs(len(running)), strings.Join(running, ", "))})
	}
	owner := d.Owner()
	out = append(out, ownerRemovalConflicts(name, owner, environment)...)
	if len(stopped) > 0 {
		message := fmt.Sprintf("%s still name%s this network and fail%s to start until it exists again.", strings.Join(stopped, ", "), singular(len(stopped)), singular(len(stopped)))
		out = append(out, NetworkConflict{Code: "stopped_dependents", Level: ConflictWarn, Subjects: stopped, Message: message})
	}
	return out
}

func ownerRemovalConflicts(name string, owner NetworkOwner, environment func(int64) (string, bool)) []NetworkConflict {
	switch owner.Kind {
	case NetworkOwnerDashboard:
		return []NetworkConflict{{Code: "dashboard_network", Level: ConflictBlock,
			Message: fmt.Sprintf("%s is part of the dashboard's own stack (%s).", name, owner.Project)}}
	case NetworkOwnerDatabaseLink, NetworkOwnerDeployment:
		what := "network"
		if owner.Kind == NetworkOwnerDatabaseLink {
			what = "database network"
		}
		if deployment, ok := environment(owner.EnvironmentID); ok {
			return []NetworkConflict{{Code: "managed_network", Level: ConflictBlock, Subjects: []string{deployment},
				Message: fmt.Sprintf("%s is the %s of deployment %s, which still exists. Remove it through the deployment's removal plan, which knows what else goes with it.", name, what, deployment)}}
		}
		return []NetworkConflict{{Code: "orphaned_managed_network", Level: ConflictInfo,
			Message: fmt.Sprintf("%s was a deployment's %s, and that deployment environment no longer exists.", name, what)}}
	case NetworkOwnerCompose:
		return []NetworkConflict{{Code: "compose_recreates", Level: ConflictInfo, Subjects: []string{owner.Project},
			Message: fmt.Sprintf("Compose project %s declares it and recreates it the next time the project comes up.", owner.Project)}}
	}
	return nil
}

// PruneCandidate is a network the Engine's prune would remove, with what
// removing it disturbs. A prune removes only the removable ones: nothing
// refuses them and nothing names them. The rest are kept and said why, and
// each can still be removed on its own after its own preview.
type PruneCandidate struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Owner     NetworkOwner      `json:"owner"`
	Conflicts []NetworkConflict `json:"conflicts"`
	Removable bool              `json:"removable"`
}

// PruneCandidates is every network the Engine's prune would remove — not a
// system network, and no running container attached — with its dependents.
// The Engine removes a network a stopped container still names, which is why
// those are listed rather than counted as "in use".
func PruneCandidates(networks []Network, containers []Container, selfProject string, environment func(int64) (string, bool)) []PruneCandidate {
	running := map[string]bool{}
	stopped := map[string][]string{}
	for _, c := range containers {
		for _, n := range c.Networks {
			if c.State == "running" {
				running[n] = true
			} else {
				stopped[n] = append(stopped[n], c.Name)
			}
		}
	}
	out := []PruneCandidate{}
	for _, n := range networks {
		if IsSystemNetwork(n.Name) || running[n.Name] {
			continue
		}
		owner := OwnerOfNetwork(n.Name, n.Labels, selfProject)
		candidate := PruneCandidate{ID: n.ID, Name: n.Name, Owner: owner, Conflicts: ownerRemovalConflicts(n.Name, owner, environment)}
		if names := stopped[n.Name]; len(names) > 0 {
			sort.Strings(names)
			candidate.Conflicts = append(candidate.Conflicts, NetworkConflict{Code: "stopped_dependents", Level: ConflictWarn, Subjects: names,
				Message: fmt.Sprintf("%s still name%s this network and fail%s to start until it exists again.", strings.Join(names, ", "), singular(len(names)), singular(len(names)))})
		}
		candidate.Removable = !Blocking(candidate.Conflicts) && !Warning(candidate.Conflicts)
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func subnetsOf(n network.Inspect) []string {
	out := []string{}
	for _, cfg := range n.IPAM.Config {
		if cfg.Subnet != "" {
			out = append(out, cfg.Subnet)
		}
	}
	return out
}

func overlappingSubnets(a, b []string) string {
	for _, x := range a {
		px, err := netip.ParsePrefix(x)
		if err != nil {
			continue
		}
		for _, y := range b {
			py, err := netip.ParsePrefix(y)
			if err == nil && px.Overlaps(py) {
				return px.Masked().String() + " and " + py.Masked().String()
			}
		}
	}
	return ""
}

// freeAddresses is how many IPv4 addresses the network's first IPv4 pool
// has left for members: the allocation range, or the subnet less its
// network, broadcast and gateway addresses, less the running members.
// Unknown for a pool too large for the count to matter.
func freeAddresses(d *NetworkDependencies) (int64, bool) {
	for _, cfg := range d.Network.IPAM.Config {
		prefix, err := netip.ParsePrefix(cfg.Subnet)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		pool := prefix
		reserved := int64(2)
		if r, err := netip.ParsePrefix(cfg.IPRange); err == nil && r.Addr().Is4() {
			pool, reserved = r, 0
			if r.Masked().Addr() == prefix.Masked().Addr() {
				reserved++
			}
		}
		if pool.Bits() < 16 {
			return 0, false
		}
		size := new(big.Int).Lsh(big.NewInt(1), uint(32-pool.Bits())).Int64()
		if gateway, err := netip.ParseAddr(cfg.Gateway); err == nil && pool.Contains(gateway) {
			reserved++
		} else if cfg.Gateway == "" {
			reserved++
		}
		return size - reserved - int64(len(d.Network.Containers)), true
	}
	return 0, false
}

func publishedPorts(ports []Port) []string {
	out := []string{}
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		value := fmt.Sprintf("%d→%d", p.PublicPort, p.PrivatePort)
		if p.Type != "" && p.Type != "tcp" {
			value += "/" + p.Type
		}
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func orState(state string) string {
	if state == "" {
		return "not running"
	}
	return state
}

// singular is the verb ending that agrees with a count: "it fails", "they fail".
func singular(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
}

func areIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
