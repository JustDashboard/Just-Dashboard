package uninstall

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// Removing the deployments, which is the half of a wipe that runs while the
// dashboard is still up.
//
// It runs in the backend rather than the sibling because the backend is what
// knows which volumes, paths and routes are the deployments' own — Docker
// labels mark the containers and the networks, but a managed volume or bind
// path is known only to the database — and because the browser that asked is
// still connected while it happens, so the slow half of the job is the half
// somebody can watch.

const (
	managedLabel          = "io.just-dashboard.managed"
	composeProjectLabel   = "com.docker.compose.project"
	ingressContainer      = "just-dashboard-ingress"
	ingressLabel          = "com.just-dashboard.ingress"
	deploymentImagePrefix = "just-dashboard/deployment-"
	// tailnetGateLabel marks the loopback-only Caddy that gates tailnet-only
	// deployments, and its two volumes (proxysvc/tailnet_gate.go).
	tailnetGateLabel = "com.just-dashboard.tailnet-gate"
)

// The public Caddy the first deployment provisions. Its routes are the
// deployments' and nothing else's, so it goes with them.
var ingressVolumes = []string{"just-dashboard-ingress-config", "just-dashboard-ingress-data"}

// Docker is the part of dockerx.Client the teardown uses.
type Docker interface {
	ListContainers(ctx context.Context, all bool) ([]dockerx.Container, error)
	RemoveContainer(ctx context.Context, id string, force, removeVolumes bool) error
	ListNetworks(ctx context.Context) ([]dockerx.Network, error)
	DisconnectNetwork(ctx context.Context, networkID, containerID string, force bool) error
	RemoveNetwork(ctx context.Context, id string) error
	ListVolumes(ctx context.Context) ([]dockerx.Volume, error)
	RemoveVolume(ctx context.Context, name string, force bool) error
	ListImages(ctx context.Context, all bool) ([]dockerx.Image, error)
	RemoveImage(ctx context.Context, id string, force, pruneChildren bool) ([]string, error)
}

// Teardown is everything removing the deployments needs. Docker aside, each
// hook is the dashboard's own code for that one job, so a route is removed
// the way the proxy removes one and a path is deleted only inside the
// deployment roots.
type Teardown struct {
	Docker Docker
	// Targets is every project's removal plan, flattened: what the database
	// says each deployment owns.
	Targets func(ctx context.Context) (projects int, targets []deploy.RemovalTarget, err error)
	// OwnProject is the dashboard's own compose project, which nothing here
	// may touch however it is labelled.
	OwnProject       string
	RemoveRoute      func(ctx context.Context, name string) error
	RemovePath       func(path string) error
	WithdrawPreviews func(ctx context.Context) (int, error)
	RemoveIngressDir func() error
	// RemoveTailnetGateDir removes the tailnet gate's Caddyfile once the
	// gate itself is gone.
	RemoveTailnetGateDir func() error
}

// Item is one thing that goes, by the name an operator knows it by.
type Item struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Owned is what the deployments own on this host, one list per kind.
type Owned struct {
	Projects   int    `json:"projects"`
	Containers []Item `json:"containers"`
	Routes     []Item `json:"routes"`
	Networks   []Item `json:"networks"`
	Volumes    []Item `json:"volumes"`
	Paths      []Item `json:"paths"`
	Images     []Item `json:"images"`
	Ingress    *Item  `json:"ingress,omitempty"`
	// TailnetGate is the Caddy in front of the tailnet-only deployments that
	// name their visitors: theirs alone, so it goes with them.
	TailnetGate *Item `json:"tailnetGate,omitempty"`
}

// Empty is whether there is nothing to remove.
func (o Owned) Empty() bool {
	return len(o.Containers)+len(o.Routes)+len(o.Networks)+len(o.Volumes)+len(o.Paths)+len(o.Images) == 0 &&
		o.Ingress == nil && o.TailnetGate == nil
}

// Inventory works out what the deployments own: by label for what Docker can
// mark, and by the removal plans for what only the database knows.
func (t Teardown) Inventory(ctx context.Context) (Owned, error) {
	owned := Owned{Containers: []Item{}, Routes: []Item{}, Networks: []Item{}, Volumes: []Item{}, Paths: []Item{}, Images: []Item{}}
	var targets []deploy.RemovalTarget
	if t.Targets != nil {
		projects, list, err := t.Targets(ctx)
		if err != nil {
			return owned, fmt.Errorf("read the deployments' removal plans: %w", err)
		}
		owned.Projects, targets = projects, list
	}
	byKind := map[string][]string{}
	for _, target := range targets {
		byKind[target.Kind] = append(byKind[target.Kind], target.ResourceID)
	}
	if t.Docker == nil {
		for _, name := range byKind["proxy_site"] {
			owned.Routes = append(owned.Routes, Item{ID: name, Name: name})
		}
		for _, path := range byKind["bind_path"] {
			owned.Paths = append(owned.Paths, Item{ID: path, Name: path})
		}
		return owned, nil
	}

	containers, err := t.Docker.ListContainers(ctx, true)
	if err != nil {
		return owned, fmt.Errorf("list containers: %w", err)
	}
	projects := set(byKind["compose_stack"])
	recorded := set(byKind["docker_container"])
	for _, c := range containers {
		project := c.Labels[composeProjectLabel]
		if t.OwnProject != "" && project == t.OwnProject {
			continue
		}
		if c.Name == ingressContainer && c.Labels[ingressLabel] == "true" {
			owned.Ingress = &Item{ID: c.ID, Name: c.Name}
			continue
		}
		if c.Labels[tailnetGateLabel] == "true" {
			owned.TailnetGate = &Item{ID: c.ID, Name: c.Name}
			continue
		}
		if c.Labels[managedLabel] != "true" && !recorded[c.ID] {
			continue
		}
		owned.Containers = append(owned.Containers, Item{ID: c.ID, Name: c.Name})
		if project != "" {
			projects[project] = true
		}
	}
	delete(projects, t.OwnProject)

	for _, name := range byKind["proxy_site"] {
		owned.Routes = append(owned.Routes, Item{ID: name, Name: name})
	}

	networks, err := t.Docker.ListNetworks(ctx)
	if err != nil {
		return owned, fmt.Errorf("list networks: %w", err)
	}
	recordedNetworks := set(byKind["deployment_database_network"])
	for _, n := range networks {
		if n.Labels[managedLabel] == "true" || projects[n.Labels[composeProjectLabel]] || recordedNetworks[n.ID] {
			owned.Networks = append(owned.Networks, Item{ID: n.ID, Name: n.Name})
		}
	}

	volumes, err := t.Docker.ListVolumes(ctx)
	if err != nil {
		return owned, fmt.Errorf("list volumes: %w", err)
	}
	recordedVolumes := set(byKind["docker_volume"])
	if owned.Ingress != nil {
		for _, name := range ingressVolumes {
			recordedVolumes[name] = true
		}
	}
	for _, v := range volumes {
		if v.Labels[managedLabel] == "true" || projects[v.Labels[composeProjectLabel]] || recordedVolumes[v.Name] || v.Labels[tailnetGateLabel] == "true" {
			owned.Volumes = append(owned.Volumes, Item{ID: v.Name, Name: v.Name})
		}
	}

	for _, path := range byKind["bind_path"] {
		owned.Paths = append(owned.Paths, Item{ID: path, Name: path})
	}

	images, err := t.Docker.ListImages(ctx, false)
	if err != nil {
		return owned, fmt.Errorf("list images: %w", err)
	}
	recordedImages := set(byKind["docker_image"])
	for _, image := range images {
		// By tag where the dashboard built it: an image it only retagged
		// keeps whatever else it was called, and removing the tag leaves it.
		tagged := false
		for _, tag := range image.RepoTags {
			if strings.HasPrefix(tag, deploymentImagePrefix) {
				owned.Images = append(owned.Images, Item{ID: tag, Name: tag})
				tagged = true
			}
		}
		if !tagged && recordedImages[image.ID] {
			name := image.ID
			if len(image.RepoDigests) > 0 {
				name = image.RepoDigests[0]
			}
			owned.Images = append(owned.Images, Item{ID: image.ID, Name: name})
		}
	}

	for _, list := range [][]Item{owned.Containers, owned.Routes, owned.Networks, owned.Volumes, owned.Paths, owned.Images} {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	return owned, nil
}

// Remove takes away everything in owned and reports what it could not. The
// order is the one the dependencies need: containers before the networks and
// volumes they hold, routes before the ingress that serves them.
func (t Teardown) Remove(ctx context.Context, owned Owned, out io.Writer) []string {
	var warnings []string
	fail := func(what string, err error) {
		line := fmt.Sprintf("could not remove %s: %v", what, err)
		fmt.Fprintln(out, line)
		warnings = append(warnings, line)
	}

	if t.WithdrawPreviews != nil {
		if n, err := t.WithdrawPreviews(ctx); err != nil {
			fail("the previews' tailnet addresses", err)
		} else if n > 0 {
			fmt.Fprintf(out, "withdrew %d preview address%s from the tailnet\n", n, plural(n, "", "es"))
		}
	}
	for _, c := range owned.Containers {
		if err := t.Docker.RemoveContainer(ctx, c.ID, true, true); err != nil && !gone(err) {
			fail("container "+c.Name, err)
			continue
		}
		fmt.Fprintf(out, "removed container %s\n", c.Name)
	}
	for _, r := range owned.Routes {
		if t.RemoveRoute == nil {
			break
		}
		if err := t.RemoveRoute(ctx, r.ID); err != nil {
			fail("route "+r.Name, err)
			continue
		}
		fmt.Fprintf(out, "removed route %s\n", r.Name)
	}
	if owned.Ingress != nil {
		if err := t.Docker.RemoveContainer(ctx, owned.Ingress.ID, true, false); err != nil && !gone(err) {
			fail("container "+owned.Ingress.Name, err)
		} else {
			fmt.Fprintf(out, "removed container %s\n", owned.Ingress.Name)
			if t.RemoveIngressDir != nil {
				if err := t.RemoveIngressDir(); err != nil {
					fail("the ingress Caddyfile", err)
				}
			}
		}
	}

	if owned.TailnetGate != nil {
		if err := t.Docker.RemoveContainer(ctx, owned.TailnetGate.ID, true, false); err != nil && !gone(err) {
			fail("container "+owned.TailnetGate.Name, err)
		} else {
			fmt.Fprintf(out, "removed container %s\n", owned.TailnetGate.Name)
			if t.RemoveTailnetGateDir != nil {
				if err := t.RemoveTailnetGateDir(); err != nil {
					fail("the tailnet gate's Caddyfile", err)
				}
			}
		}
	}

	if len(owned.Networks) > 0 {
		// Listed again: what is still attached now that the deployments'
		// own containers are gone is somebody else's — a database a
		// deployment was linked to — and has to be let go of first.
		attached := map[string][]dockerx.NetworkEndpoint{}
		if networks, err := t.Docker.ListNetworks(ctx); err == nil {
			for _, n := range networks {
				attached[n.ID] = n.Endpoints
			}
		}
		for _, n := range owned.Networks {
			for _, ep := range attached[n.ID] {
				if err := t.Docker.DisconnectNetwork(ctx, n.ID, ep.Container, true); err != nil && !gone(err) {
					fmt.Fprintf(out, "could not disconnect %s from %s: %v\n", ep.Name, n.Name, err)
				} else {
					fmt.Fprintf(out, "disconnected %s from %s\n", ep.Name, n.Name)
				}
			}
			if err := t.Docker.RemoveNetwork(ctx, n.ID); err != nil && !gone(err) {
				fail("network "+n.Name, err)
				continue
			}
			fmt.Fprintf(out, "removed network %s\n", n.Name)
		}
	}
	for _, v := range owned.Volumes {
		if err := t.Docker.RemoveVolume(ctx, v.ID, true); err != nil && !gone(err) {
			fail("volume "+v.Name, err)
			continue
		}
		fmt.Fprintf(out, "removed volume %s\n", v.Name)
	}
	for _, p := range owned.Paths {
		if t.RemovePath == nil {
			break
		}
		if err := t.RemovePath(p.ID); err != nil && !gone(err) {
			fail(p.Name, err)
			continue
		}
		fmt.Fprintf(out, "removed %s\n", p.Name)
	}
	for _, image := range owned.Images {
		// Never forced: an image a container outside the deployments still
		// runs is not the deployments' to take.
		if _, err := t.Docker.RemoveImage(ctx, image.ID, false, true); err != nil && !gone(err) {
			fail("image "+image.Name, err)
			continue
		}
		fmt.Fprintf(out, "removed image %s\n", image.Name)
	}
	return warnings
}

// gone is an error that means the thing was already not there, which for a
// removal is success.
func gone(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such") || strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist")
}

func set(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		if v != "" {
			out[v] = true
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
