package dockerx

import (
	"context"
	"sort"
	"strings"
)

// Cleanup as a decision rather than a button.
//
// "Prune" is a word Docker uses for five different sweeps with five different
// blast radii, one of which deletes databases. A single button labelled with it
// is either too timid to help — dangling images only, which on a host that
// redeploys through compose frees nothing — or too dangerous to press.
//
// So the operator is shown what each category holds, what removing it would
// give back, and what it costs, and chooses. Volumes are in the list because
// leaving them out sends people hunting for missing disk in the wrong place,
// and are never selected by default because they are the one category that is
// the data.

// CleanupCategory is one class of removable thing.
type CleanupCategory struct {
	// Key is what the caller passes back to run this category.
	Key   string `json:"key"`
	Label string `json:"label"`
	// Items is how many objects, Reclaimable what removing them gives back.
	// Reclaimable is Docker's own figure, which counts a shared layer once —
	// the naive "size of everything unused" is always larger and is what makes
	// a reclaim button look broken when it delivers a third of the promise.
	Items       int   `json:"items"`
	Reclaimable int64 `json:"reclaimable"`
	// Cost is what removing this actually costs, in a sentence. Empty is not
	// an option: everything here costs something, even if it is only a slower
	// next build.
	Cost string `json:"cost"`
	// Destroys marks the category that destroys data. Exactly one does.
	Destroys bool `json:"destroys"`
	// Examples name a few of the objects, so "3 unused images" can be checked
	// rather than trusted. Always a list, never nil: an empty category is the
	// common case, and a nil slice reaches the browser as `null`, where
	// `examples.length` is a TypeError that blanks the whole page.
	Examples []string `json:"examples"`
	// Recommended is whether this belongs in the default selection.
	Recommended bool `json:"recommended"`
}

// CleanupPreview is what a sweep would do, per category.
type CleanupPreview struct {
	Categories []CleanupCategory `json:"categories"`
	// SafeTotal is what the recommended categories together would reclaim.
	SafeTotal int64 `json:"safeTotal"`
	// Summary is the sentence above the list.
	Summary string `json:"summary"`
}

// PreviewCleanup measures each category without removing anything.
func (c *Client) PreviewCleanup(ctx context.Context) (*CleanupPreview, error) {
	du := c.diskUsage(ctx)
	if du == nil {
		return nil, ErrUnavailable
	}
	usage, err := c.DiskUsage(ctx)
	if err != nil {
		return nil, err
	}

	out := &CleanupPreview{Categories: []CleanupCategory{}}

	stopped := CleanupCategory{
		Key: "containers", Label: "Stopped containers", Recommended: true, Examples: []string{},
		Cost: "Their writable layers go with them — anything written inside a container rather than into a volume. Their volumes are not touched.",
	}
	for _, ct := range du.Containers {
		if strings.EqualFold(ct.State, "running") {
			continue
		}
		stopped.Items++
		stopped.Reclaimable += ct.SizeRw
		if len(stopped.Examples) < 4 && len(ct.Names) > 0 {
			stopped.Examples = append(stopped.Examples, strings.TrimPrefix(ct.Names[0], "/"))
		}
	}
	out.Categories = append(out.Categories, stopped)

	dangling := CleanupCategory{
		Key: "dangling-images", Label: "Untagged images", Recommended: true, Examples: []string{},
		Cost: "Layers left behind by a rebuild that took their tag. Nothing references them by name and no container is using them.",
	}
	unused := CleanupCategory{
		Key: "unused-images", Label: "Images no container is using", Examples: []string{},
		Cost: "Each comes back with a pull. On a server that redeploys through compose this is usually the largest line — and it includes the previous version of everything you are running, so a rollback would have to pull again.",
	}
	for _, img := range du.Images {
		if img.Containers > 0 {
			continue
		}
		name := "<untagged>"
		if len(img.RepoTags) > 0 && img.RepoTags[0] != danglingRef {
			name = img.RepoTags[0]
		}
		size := img.Size - img.SharedSize
		if size < 0 {
			size = 0
		}
		if name == "<untagged>" {
			dangling.Items++
			dangling.Reclaimable += size
			if len(dangling.Examples) < 4 {
				dangling.Examples = append(dangling.Examples, ShortID(img.ID))
			}
			continue
		}
		unused.Items++
		unused.Reclaimable += size
		if len(unused.Examples) < 4 {
			unused.Examples = append(unused.Examples, name)
		}
	}
	out.Categories = append(out.Categories, dangling, unused)

	cache := CleanupCategory{
		Key: "build-cache", Label: "Build cache", Recommended: true, Examples: []string{},
		Items:       usage.BuildCacheLine.Total - usage.BuildCacheLine.Active,
		Reclaimable: usage.BuildCacheLine.Reclaimable,
		Cost:        "The next build of each image is slower, because it starts from scratch instead of from what BuildKit remembered. Nothing else changes.",
	}
	out.Categories = append(out.Categories, cache)

	networks := CleanupCategory{
		Key: "networks", Label: "Unused networks", Recommended: true, Examples: []string{},
		Cost: "Networks occupy no disk. Removing the unused ones frees address space and tidies the list; a network a stopped stack will want again is recreated on the next deploy.",
	}
	if list, err := c.ListNetworks(ctx); err == nil {
		for _, n := range list {
			if n.Containers == 0 && !IsSystemNetwork(n.Name) && n.Name != "" {
				networks.Items++
				if len(networks.Examples) < 4 {
					networks.Examples = append(networks.Examples, n.Name)
				}
			}
		}
	}
	out.Categories = append(out.Categories, networks)

	// Never recommended, always shown. A volume list that is hidden until you
	// go looking is how somebody prunes a database by accident somewhere else.
	volumes := CleanupCategory{
		Key: "volumes", Label: "Volumes attached to nothing", Destroys: true, Examples: []string{},
		Cost: "This destroys data permanently. A volume outlives the container that made it, so an unattached volume is often the only copy of something — and Docker counts a volume belonging to a merely *stopped* stack as unused.",
	}
	for _, v := range du.Volumes {
		if v.UsageData == nil || v.UsageData.RefCount > 0 {
			continue
		}
		volumes.Items++
		volumes.Reclaimable += v.UsageData.Size
		if len(volumes.Examples) < 4 {
			volumes.Examples = append(volumes.Examples, v.Name)
		}
	}
	out.Categories = append(out.Categories, volumes)

	for _, cat := range out.Categories {
		if cat.Recommended {
			out.SafeTotal += cat.Reclaimable
		}
	}
	// Largest first among the recommended ones, so the line worth acting on is
	// at the top; volumes stay last wherever they land.
	sort.SliceStable(out.Categories, func(i, j int) bool {
		if out.Categories[i].Destroys != out.Categories[j].Destroys {
			return !out.Categories[i].Destroys
		}
		return out.Categories[i].Reclaimable > out.Categories[j].Reclaimable
	})

	switch {
	case out.SafeTotal == 0:
		out.Summary = "Nothing worth reclaiming. Docker is holding " + humanBytes(usage.LayersSize+usage.Containers) + " and effectively all of it is in use."
	default:
		out.Summary = humanBytes(out.SafeTotal) + " can be reclaimed without touching anything a container is using."
	}
	return out, nil
}

// RunCleanup carries out the selected categories and reports each separately.
//
// Per category rather than as one sweep, because "reclaimed 4.2 GB" tells an
// operator nothing about which of the five things they agreed to actually
// happened — and because a daemon that refuses one of them must not take the
// rest down with it.
func (c *Client) RunCleanup(ctx context.Context, keys []string) ([]PruneReport, error) {
	if _, err := c.api(); err != nil {
		return nil, err
	}
	defer c.forgetDiskUsage()

	want := map[string]bool{}
	for _, k := range keys {
		want[strings.TrimSpace(k)] = true
	}
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

	if want["containers"] {
		reclaimed, deleted, err := c.PruneContainers(ctx)
		add(PruneReport{Kind: "containers", SpaceReclaimed: reclaimed, Items: deleted}, err)
	}
	// `image prune -a` covers dangling as well, so asking for both runs the
	// wider sweep once rather than twice.
	if want["unused-images"] {
		add(c.PruneImages(ctx, true))
	} else if want["dangling-images"] {
		add(c.PruneImages(ctx, false))
	}
	if want["build-cache"] {
		add(c.PruneBuildCache(ctx, true))
	}
	if want["networks"] {
		add(c.pruneNetworks(ctx))
	}
	if want["volumes"] {
		add(c.PruneVolumes(ctx))
	}
	return reports, nil
}
