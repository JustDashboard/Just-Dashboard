package dockerx

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
)

type Image struct {
	ID          string            `json:"id"`
	RepoTags    []string          `json:"repoTags"`
	RepoDigests []string          `json:"repoDigests"`
	Size        int64             `json:"size"`
	Created     time.Time         `json:"created"`
	Containers  int64             `json:"containers"`
	Labels      map[string]string `json:"labels"`
	Dangling    bool              `json:"dangling"`
}

func (c *Client) ListImages(ctx context.Context, all bool) ([]Image, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	items, err := cli.ImageList(ctx, image.ListOptions{All: all})
	if err != nil {
		return nil, err
	}
	out := make([]Image, 0, len(items))
	for _, it := range items {
		// Every slice is normalised to empty rather than left nil. A nil slice
		// marshals to `null`, and the client reads these as arrays — an
		// untagged image would arrive with `repoTags: null` and take the page
		// down on `.length`. The API's contract is that a list field is
		// always a list.
		img := Image{
			ID:          it.ID,
			RepoTags:    orEmpty(it.RepoTags),
			RepoDigests: orEmpty(it.RepoDigests),
			Size:        it.Size,
			Created:     time.Unix(it.Created, 0).UTC(),
			Containers:  it.Containers,
			Labels:      it.Labels,
		}
		if img.Labels == nil {
			img.Labels = map[string]string{}
		}
		if len(img.RepoTags) == 0 || (len(img.RepoTags) == 1 && img.RepoTags[0] == "<none>:<none>") {
			img.Dangling = true
		}
		out = append(out, img)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out, nil
}

// PullProgress is one line of the pull stream, forwarded to the browser so the
// user sees layer progress rather than a spinner.
type PullProgress struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status"`
	Progress string `json:"progress,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (c *Client) PullImage(ctx context.Context, ref string, out chan<- PullProgress) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			Error          string `json:"error"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
			Progress string `json:"progress"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- PullProgress{ID: msg.ID, Status: msg.Status, Progress: msg.Progress, Error: msg.Error}:
		}
	}
}

func (c *Client) RemoveImage(ctx context.Context, id string, force, pruneChildren bool) ([]string, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	res, err := cli.ImageRemove(ctx, id, image.RemoveOptions{Force: force, PruneChildren: pruneChildren})
	if err != nil {
		return nil, err
	}
	defer c.forgetDiskUsage()
	out := []string{}
	for _, r := range res {
		if r.Deleted != "" {
			out = append(out, "deleted "+r.Deleted)
		}
		if r.Untagged != "" {
			out = append(out, "untagged "+r.Untagged)
		}
	}
	return out, nil
}

// PruneReport is the shared shape for every prune operation so the UI can
// render one confirmation summary regardless of what was reclaimed.
type PruneReport struct {
	Kind           string   `json:"kind"`
	SpaceReclaimed uint64   `json:"spaceReclaimed"`
	Items          []string `json:"items"`
	// Error names the step that did not run. A sweep is several independent
	// prunes and one of them failing is not a reason to abandon the rest, but
	// it is a reason to say so: silently dropping the line made a refused
	// prune indistinguishable from one that found nothing to do.
	Error string `json:"error,omitempty"`
}

// PruneImages removes dangling images by default. Passing all=true removes
// every image not referenced by a container, which is far more destructive and
// is why the handler demands a typed confirmation for it.
func (c *Client) PruneImages(ctx context.Context, all bool) (PruneReport, error) {
	cli, err := c.api()
	if err != nil {
		return PruneReport{}, err
	}
	args := filters.NewArgs()
	args.Add("dangling", boolStr(!all))
	rep, err := cli.ImagesPrune(ctx, args)
	if err != nil {
		return PruneReport{}, err
	}
	defer c.forgetDiskUsage()
	out := PruneReport{Kind: "images", SpaceReclaimed: rep.SpaceReclaimed, Items: []string{}}
	for _, d := range rep.ImagesDeleted {
		if d.Deleted != "" {
			out.Items = append(out.Items, "deleted "+d.Deleted)
		}
		if d.Untagged != "" {
			out.Items = append(out.Items, "untagged "+d.Untagged)
		}
	}
	return out, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	CreatedAt  string            `json:"createdAt"`
	Scope      string            `json:"scope"`
	Labels     map[string]string `json:"labels"`
	Size       int64             `json:"size"`
	RefCount   int64             `json:"refCount"`
	InUse      bool              `json:"inUse"`
}

func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	res, err := cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	// The volume list has no usage data; disk usage does. Joining them is what
	// makes "which volume is safe to delete" answerable in the UI.
	usage := map[string]struct {
		size int64
		refs int64
	}{}
	if du := c.diskUsage(ctx); du != nil {
		for _, v := range du.Volumes {
			if v.UsageData != nil {
				usage[v.Name] = struct {
					size int64
					refs int64
				}{v.UsageData.Size, v.UsageData.RefCount}
			}
		}
	}
	out := make([]Volume, 0, len(res.Volumes))
	for _, v := range res.Volumes {
		vol := Volume{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint,
			CreatedAt: v.CreatedAt, Scope: v.Scope, Labels: v.Labels, RefCount: -1,
		}
		if vol.Labels == nil {
			vol.Labels = map[string]string{}
		}
		if u, ok := usage[v.Name]; ok {
			vol.Size = u.size
			vol.RefCount = u.refs
			vol.InUse = u.refs > 0
		}
		out = append(out, vol)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) InspectVolume(ctx context.Context, name string) (volume.Volume, error) {
	cli, err := c.api()
	if err != nil {
		return volume.Volume{}, err
	}
	return cli.VolumeInspect(ctx, name)
}

// VolumeExists reports whether a named volume is already on this host, so a
// caller about to mount one by name can tell "fresh" from "leftover" before
// Docker silently reuses whatever is in it.
func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	cli, err := c.api()
	if err != nil {
		return false, err
	}
	_, err = cli.VolumeInspect(ctx, name)
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	defer c.forgetDiskUsage()
	return cli.VolumeRemove(ctx, name, force)
}

func (c *Client) PruneVolumes(ctx context.Context) (PruneReport, error) {
	cli, err := c.api()
	if err != nil {
		return PruneReport{}, err
	}
	// "all" is required to reach named volumes; without it Docker only
	// considers anonymous ones, which is rarely what the operator meant.
	args := filters.NewArgs()
	args.Add("all", "true")
	rep, err := cli.VolumesPrune(ctx, args)
	if err != nil {
		return PruneReport{}, err
	}
	defer c.forgetDiskUsage()
	items := rep.VolumesDeleted
	if items == nil {
		items = []string{}
	}
	return PruneReport{Kind: "volumes", SpaceReclaimed: rep.SpaceReclaimed, Items: items}, nil
}

type Network struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Internal   bool              `json:"internal"`
	Attachable bool              `json:"attachable"`
	IPv6       bool              `json:"ipv6"`
	Created    time.Time         `json:"created"`
	Labels     map[string]string `json:"labels"`
	Subnets    []string          `json:"subnets"`
	Containers int               `json:"containers"`
	// UsedBy names the containers attached, so the delete dialog can say which
	// ones rather than only how many.
	UsedBy []string `json:"usedBy"`
}

func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	items, err := cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	// Membership is joined from the containers rather than read off the
	// listing, because Docker's network *list* never populates the container
	// map — only an inspect does. `len(n.Containers)` here is not "nothing is
	// attached", it is "this endpoint does not say", and rendering it as a
	// count produced a column of zeroes on every host and a delete dialog that
	// told the operator nothing was attached to a network carrying a running
	// stack.
	//
	// It costs one call for the whole table: a container's networks come free
	// with the container listing, the same way its mounts do for the volumes
	// view. An inspect per network would be one round trip per row.
	members := map[string][]string{}
	if containers, err := c.ListContainers(ctx, true); err == nil {
		for _, ct := range containers {
			for _, name := range ct.Networks {
				members[name] = append(members[name], ct.Name)
			}
		}
		for name := range members {
			sort.Strings(members[name])
		}
	}
	out := make([]Network, 0, len(items))
	for _, n := range items {
		nw := Network{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope,
			Internal: n.Internal, Attachable: n.Attachable, IPv6: n.EnableIPv6,
			Created: n.Created.UTC(), Labels: n.Labels, Subnets: []string{},
			UsedBy: members[n.Name],
		}
		if nw.UsedBy == nil {
			nw.UsedBy = []string{}
		}
		nw.Containers = len(nw.UsedBy)
		if nw.Labels == nil {
			nw.Labels = map[string]string{}
		}
		for _, cfg := range n.IPAM.Config {
			if cfg.Subnet != "" {
				nw.Subnets = append(nw.Subnets, cfg.Subnet)
			}
		}
		out = append(out, nw)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *Client) InspectNetwork(ctx context.Context, id string) (network.Inspect, error) {
	cli, err := c.api()
	if err != nil {
		return network.Inspect{}, err
	}
	return cli.NetworkInspect(ctx, id, network.InspectOptions{Verbose: true})
}

func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	return cli.NetworkRemove(ctx, id)
}

// DiskUsageLine is one row of `docker system df`: how many objects of a kind
// exist, how many are in use, what they occupy and what pruning would give
// back.
//
// Reclaimable is the field the whole page hangs off, and it is deliberately
// not "the size of the things nothing is using". Layers are shared, so an
// unused image whose every layer also belongs to a running one frees nothing
// when it goes — python:3.11-slim measures 189 MB and gives back 2. Promising
// the naive figure means a prune that reports a fraction of what the page
// said, which reads as a broken button rather than as arithmetic.
type DiskUsageLine struct {
	Total       int   `json:"total"`
	Active      int   `json:"active"`
	Size        int64 `json:"size"`
	Reclaimable int64 `json:"reclaimable"`
}

type DiskUsage struct {
	LayersSize int64 `json:"layersSize"`
	Images     int64 `json:"imagesSize"`
	Containers int64 `json:"containersSize"`
	Volumes    int64 `json:"volumesSize"`
	BuildCache int64 `json:"buildCacheSize"`

	// The breakdown behind the totals above, added rather than replacing them
	// so nothing reading the flat fields had to change.
	ImagesLine     DiskUsageLine `json:"images"`
	ContainersLine DiskUsageLine `json:"containers"`
	VolumesLine    DiskUsageLine `json:"volumes"`
	BuildCacheLine DiskUsageLine `json:"buildCache"`

	// SharedLayers is the difference between adding up every image's own size
	// and what the images actually occupy.
	//
	// It is the reason two honest figures on one page can look like a
	// contradiction. `Images` above is the sum of what each image reports, and
	// an image that shares three quarters of its layers with another reports
	// all of them; `LayersSize` is what the disk holds, counting each layer
	// once. Naming the gap is what turns "these two numbers disagree" into
	// "these two numbers measure different things, and here is by how much".
	SharedLayers int64 `json:"sharedLayers"`

	// Writable is every container's own layer, largest first, so "what is
	// filling the disk" is answerable without a second walk. Reported here
	// rather than on the container listing because the Engine will not put
	// SizeRw in a listing unless it is asked to walk every layer, which is far
	// too expensive for a table that polls.
	Writable []ContainerDisk `json:"writable"`

	// Definitions say what each figure counts, so a tooltip is data rather
	// than prose repeated in four components — and so nothing on screen can
	// quietly redefine a word.
	Definitions []DiskDefinition `json:"definitions"`
}

// ContainerDisk is one container's writable layer.
type ContainerDisk struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Stack string `json:"stack,omitempty"`
	// SizeRw is what has been written into the container's own filesystem
	// since it was created from its image. Volumes and bind mounts are not in
	// it: those are storage, this is the part that disappears on recreate.
	SizeRw int64 `json:"sizeRw"`
	// SizeRootFs is the writable layer plus the image underneath it — Docker's
	// "virtual size". Carried because `docker ps -s` shows it and an operator
	// comparing the two screens should find the same numbers, and marked as
	// what it is so nobody adds it to an image total and counts it twice.
	SizeRootFs int64 `json:"sizeRootFs"`
}

// DiskDefinition is what one figure on the disk page actually measures.
type DiskDefinition struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Measures string `json:"measures"`
	Excludes string `json:"excludes,omitempty"`
	// Source is which Docker API the number came from. Two figures from
	// different endpoints are not always comparable, and saying so is better
	// than letting somebody subtract one from the other.
	Source string `json:"source"`
}

// diskDefinitions is the single place these words are defined. Changing a
// label here changes it everywhere it is shown.
func diskDefinitions() []DiskDefinition {
	return []DiskDefinition{
		{
			Key:      "images",
			Label:    "Images",
			Measures: "What the image layers occupy on disk, counting a layer shared between images once.",
			Excludes: "Container writable layers, volumes and build cache.",
			Source:   "docker system df",
		},
		{
			Key:      "imagesApparent",
			Label:    "Images, added up",
			Measures: "Every image's own reported size, summed. An image shares layers with others and reports all of them, so this is always larger than what the disk holds.",
			Excludes: "Nothing — that is the problem with it. Use it to compare two images, never to work out free space.",
			Source:   "docker system df",
		},
		{
			Key:      "writableLayer",
			Label:    "Writable layer",
			Measures: "Data written into a container's own filesystem since it was created.",
			Excludes: "Named volumes, bind mounts, and the image the container started from.",
			Source:   "docker system df",
		},
		{
			Key:      "virtualSize",
			Label:    "Virtual size",
			Measures: "A container's writable layer plus the image beneath it.",
			Excludes: "Named volumes and bind mounts.",
			Source:   "docker system df",
		},
		{
			Key:      "volumes",
			Label:    "Volumes",
			Measures: "What Docker-managed volumes hold on disk.",
			Excludes: "Bind mounts, which are ordinary directories on this server and are not Docker's to measure.",
			Source:   "docker system df",
		},
		{
			Key:      "buildCache",
			Label:    "Build cache",
			Measures: "Intermediate layers BuildKit keeps to make the next build faster, counting a record shared with another once.",
			Excludes: "Anything a running container needs.",
			Source:   "docker system df",
		},
		{
			Key:      "reclaimable",
			Label:    "Reclaimable",
			Measures: "What a prune would actually give back. An unused image whose layers are all shared with a running one frees nothing when it goes.",
			Excludes: "Volumes, always. They are the one Docker object that is the data.",
			Source:   "docker system df",
		},
		{
			Key:      "logs",
			Label:    "Container logs",
			Measures: "The json-file log each container writes, measured on the host filesystem.",
			Excludes: "Everything else. This is not part of the writable layer and is not in any line above.",
			Source:   "the daemon's data root, read directly",
		},
	}
}

// Reclaimable is everything a prune could give back without touching a volume.
// Volumes are excluded on purpose: they are the one Docker object that is the
// data, and a headline figure that silently counted them would be inviting the
// operator to reclaim their database.
func (d DiskUsage) Reclaimable() int64 {
	return d.ImagesLine.Reclaimable + d.ContainersLine.Reclaimable + d.BuildCacheLine.Reclaimable
}

func (c *Client) DiskUsage(ctx context.Context) (DiskUsage, error) {
	if _, err := c.api(); err != nil {
		return DiskUsage{}, err
	}
	// Shares the cache with the volume list: the same multi-second walk of
	// every layer and volume answers both, and it is not worth doing twice.
	du := c.diskUsage(ctx)
	if du == nil {
		return DiskUsage{}, ErrUnavailable
	}
	out := DiskUsage{LayersSize: du.LayersSize}

	// Images. `Size - SharedSize` over the unused ones is Docker's own
	// definition of reclaimable and is what `docker system df` prints; summing
	// Size over-reports by every layer the unused image has in common with one
	// that is running.
	out.ImagesLine = DiskUsageLine{Total: len(du.Images), Size: du.LayersSize}
	for _, i := range du.Images {
		out.Images += i.Size
		if i.Containers > 0 {
			out.ImagesLine.Active++
			continue
		}
		if i.Size >= 0 && i.SharedSize >= 0 {
			out.ImagesLine.Reclaimable += i.Size - i.SharedSize
		}
	}

	// Containers. Only the writable layer counts — the image underneath it is
	// the images line's business, and adding it here would report the same
	// bytes twice.
	out.ContainersLine.Total = len(du.Containers)
	out.Writable = make([]ContainerDisk, 0, len(du.Containers))
	for _, ct := range du.Containers {
		out.Containers += ct.SizeRw
		out.ContainersLine.Size += ct.SizeRw
		if strings.EqualFold(ct.State, "running") {
			out.ContainersLine.Active++
		} else {
			out.ContainersLine.Reclaimable += ct.SizeRw
		}
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		out.Writable = append(out.Writable, ContainerDisk{
			ID: ct.ID, Name: name, State: ct.State,
			Stack:  ct.Labels[labelProject],
			SizeRw: ct.SizeRw, SizeRootFs: ct.SizeRootFs,
		})
	}
	sort.Slice(out.Writable, func(i, j int) bool { return out.Writable[i].SizeRw > out.Writable[j].SizeRw })

	out.VolumesLine.Total = len(du.Volumes)
	for _, v := range du.Volumes {
		if v.UsageData == nil {
			continue
		}
		out.Volumes += v.UsageData.Size
		out.VolumesLine.Size += v.UsageData.Size
		if v.UsageData.RefCount > 0 {
			out.VolumesLine.Active++
		} else {
			out.VolumesLine.Reclaimable += v.UsageData.Size
		}
	}

	// Build cache. A shared record's bytes are already counted by the record
	// it is shared with, so the size line skips them; without that the total
	// exceeds what the disk actually holds.
	out.BuildCacheLine.Total = len(du.BuildCache)
	var inUse int64
	for _, b := range du.BuildCache {
		out.BuildCache += b.Size
		if !b.Shared {
			out.BuildCacheLine.Size += b.Size
		}
		if b.InUse {
			out.BuildCacheLine.Active++
			inUse += b.Size
		}
	}
	out.BuildCacheLine.Reclaimable = out.BuildCacheLine.Size - inUse
	if out.BuildCacheLine.Reclaimable < 0 {
		out.BuildCacheLine.Reclaimable = 0
	}

	// The gap between "every image's size, added up" and "what the images
	// occupy". Reported rather than hidden, because both figures are true and
	// a page showing them without this reads as arithmetic that does not work.
	if out.SharedLayers = out.Images - out.LayersSize; out.SharedLayers < 0 {
		out.SharedLayers = 0
	}
	out.Definitions = diskDefinitions()
	return out, nil
}

// PruneOptions is what a sweep is allowed to touch. Every field defaults to
// the conservative answer, so a zero value is `docker system prune`: stopped
// containers, dangling images, unused networks and dangling build cache.
type PruneOptions struct {
	// Volumes is the only one that destroys data, which is why it is the only
	// one behind a typed phrase at the route.
	Volumes bool
	// AllImages reaches tagged images no container is using, not merely the
	// dangling ones. It is the difference between `docker image prune` and
	// `docker image prune -a`, and on a server that has been up for a while it
	// is the difference between reclaiming nothing and reclaiming gigabytes:
	// an image left behind by a compose redeploy keeps its tag, so nothing is
	// dangling and the conservative sweep frees zero bytes.
	AllImages bool
	// BuildCache reaches BuildKit's cache. Kept as its own flag rather than
	// folded into AllImages because it is by far the largest line on a build
	// server and the operator should be able to see it named.
	BuildCache bool
	// AllBuildCache is to BuildCache what AllImages is to images.
	AllBuildCache bool
}

// PruneAll is the "reclaim everything" sweep, reported as separate lines so
// the operator can see exactly what went and how much each part gave back.
//
// A failing step no longer takes the sweep down silently. Each prune used to
// be `if err == nil` with no else, so a daemon that refused one of them
// returned a short list that read as "there was nothing there" — the report
// carries the error now and the caller can say which part did not run.
func (c *Client) PruneAll(ctx context.Context, opts PruneOptions) ([]PruneReport, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	defer c.forgetDiskUsage()
	reports := []PruneReport{}
	add := func(rep PruneReport, err error) {
		if err != nil {
			rep.Error = err.Error()
		}
		if rep.Items == nil {
			rep.Items = []string{}
		}
		reports = append(reports, rep)
	}

	ctRep, err := cli.ContainersPrune(ctx, filters.NewArgs())
	add(PruneReport{
		Kind: "containers", SpaceReclaimed: ctRep.SpaceReclaimed, Items: ctRep.ContainersDeleted,
	}, err)
	add(c.PruneImages(ctx, opts.AllImages))
	if opts.BuildCache {
		add(c.PruneBuildCache(ctx, opts.AllBuildCache))
	}
	if opts.Volumes {
		add(c.PruneVolumes(ctx))
	}
	add(c.pruneNetworks(ctx))
	return reports, nil
}

func (c *Client) pruneNetworks(ctx context.Context) (PruneReport, error) {
	cli, err := c.api()
	if err != nil {
		return PruneReport{}, err
	}
	rep, err := cli.NetworksPrune(ctx, filters.NewArgs())
	if err != nil {
		return PruneReport{}, err
	}
	items := rep.NetworksDeleted
	if items == nil {
		items = []string{}
	}
	return PruneReport{Kind: "networks", Items: items}, nil
}

// ImageRef normalises a user-supplied reference so "nginx" pulls nginx:latest
// rather than failing, matching CLI behaviour.
//
// The rules live in imageref.go, where the difference between a name, a digest
// and a bare image id is written down once. This used to append `:latest` to
// anything without a colon after the last slash, which meant an image id
// arrived at the daemon as `sha256:ab…` — read as the repository `sha256` and
// the tag `ab…` — and inspecting an image by id could not work.
func ImageRef(ref string) string { return NormalizeImageRef(ref) }

// orEmpty turns a nil slice into an empty one, so it marshals as `[]` rather
// than `null`. Every list-shaped field on the wire goes through this or is
// initialised at its declaration: the frontend reads them as arrays, and one
// `null` is a TypeError that blanks the page rather than a missing value.
func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
