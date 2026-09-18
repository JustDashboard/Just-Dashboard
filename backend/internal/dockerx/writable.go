package dockerx

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Where the writable layer went.
//
// "Writable layer: 38.7 GB" is a true sentence that helps nobody. The
// interesting questions are which directory holds it, whether that directory
// looks like data somebody meant to keep, and whether it is backed by anything
// that survives the container being recreated — because a writable layer is
// destroyed by every image update, and 35 GB under /app/data with no volume
// mounted there is a database that will vanish on the next redeploy.
//
// The measurement is honest about how it was taken. Docker has no API for
// "what is inside a container's writable layer, by directory": the daemon can
// report the total and can list the changed *paths*, and neither is a
// breakdown. So this runs `du` inside the container, which means it only works
// on a running container that ships `du`, and says so plainly when it does
// not. The alternative — walking the overlay upperdir from the host — needs
// the daemon's data root to be readable by this process and does not work at
// all for other storage drivers, so it is not attempted rather than half
// attempted.

// WritableLayerReport is the answer, with its own provenance attached.
type WritableLayerReport struct {
	ContainerID string    `json:"containerId"`
	Name        string    `json:"name"`
	MeasuredAt  time.Time `json:"measuredAt"`

	// Total is the writable layer as Docker reports it, which is the figure
	// every other screen shows. Accounted is what the breakdown adds up to.
	// They will not match exactly — `du` counts allocated blocks and the
	// daemon counts the diff — and the difference is reported rather than
	// hidden, because two numbers that nearly agree are more trustworthy than
	// one that has been silently reconciled.
	Total     int64 `json:"total"`
	Accounted int64 `json:"accounted"`

	// State is "measured", "unavailable" or "failed". A report that could not
	// be taken is not an empty report.
	State  string `json:"state"`
	Method string `json:"method,omitempty"`
	Reason string `json:"reason,omitempty"`

	Entries []WritableEntry `json:"entries"`
	// Unbacked names the directories holding real data that no volume or bind
	// mount covers — the ones that disappear on recreate. This is the finding
	// the whole feature exists for.
	Unbacked []WritableEntry `json:"unbacked"`
}

// WritableEntry is one directory inside the container.
type WritableEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Mounted is the mount destination covering this path, empty when nothing
	// does. A directory under a mount is not in the writable layer at all and
	// is reported so the operator can see it was considered.
	Mounted string `json:"mounted,omitempty"`
	// Persistent marks a path that looks like it holds data somebody intended
	// to keep, rather than a cache or a log. Inferred from the path, and
	// labelled as inferred everywhere it is shown.
	Persistent bool `json:"persistent"`
	// Kind is "data", "logs", "cache", "temporary" or "other" — a guess from
	// the path, used only to order the list and to word the advice.
	Kind string `json:"kind"`
}

// writableScanTimeout bounds the `du`. A container with millions of small
// files can take minutes to walk, and a request that hangs for minutes is
// indistinguishable from a broken dashboard.
const writableScanTimeout = 90 * time.Second

// writableCacheTTL keeps a breakdown long enough that opening the panel twice
// does not walk the filesystem twice, and short enough that "analyze again"
// after a cleanup means something.
const writableCacheTTL = 10 * time.Minute

type writableEntry struct {
	report *WritableLayerReport
	at     time.Time
}

var (
	writableMu    sync.Mutex
	writableCache = map[string]writableEntry{}
)

// scanRoots are the directories worth measuring. Walking `/` inside a
// container means walking the whole image as well as the writable layer, which
// on a 2 GB image is a minute of work to answer a question about the 40 GB
// that is not the image. These are where containers actually write.
var scanRoots = []string{
	"/app", "/data", "/srv", "/var/lib", "/var/log", "/var/cache",
	"/var/tmp", "/tmp", "/home", "/opt", "/usr/local", "/root", "/mnt",
}

// AnalyzeWritableLayer measures where a container's writable layer went.
func (c *Client) AnalyzeWritableLayer(ctx context.Context, id string, force bool) (*WritableLayerReport, error) {
	if !force {
		writableMu.Lock()
		entry, ok := writableCache[id]
		writableMu.Unlock()
		if ok && time.Since(entry.at) < writableCacheTTL {
			return entry.report, nil
		}
	}
	report, err := c.analyzeWritableLayer(ctx, id)
	if err != nil {
		return nil, err
	}
	writableMu.Lock()
	writableCache[id] = writableEntry{report: report, at: time.Now()}
	writableMu.Unlock()
	return report, nil
}

func (c *Client) analyzeWritableLayer(ctx context.Context, id string) (*WritableLayerReport, error) {
	detail, err := c.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &WritableLayerReport{
		ContainerID: detail.ID,
		Name:        detail.Name,
		MeasuredAt:  time.Now().UTC(),
		State:       "unavailable",
		Entries:     []WritableEntry{},
		Unbacked:    []WritableEntry{},
	}
	if du := c.diskUsage(ctx); du != nil {
		for _, ct := range du.Containers {
			if ct.ID == detail.ID {
				out.Total = ct.SizeRw
			}
		}
	}
	if detail.State != "running" {
		out.Reason = "The breakdown is measured by running `du` inside the container, which needs it to be running. Docker reports the total for a stopped container but has no API that says which directory holds it."
		return out, nil
	}

	sizes, method, err := c.measureDirectories(ctx, detail.ID)
	if err != nil {
		out.State = "failed"
		out.Reason = err.Error()
		return out, nil
	}
	out.State = "measured"
	out.Method = method

	for path, size := range sizes {
		entry := WritableEntry{Path: path, Size: size, Kind: classifyPath(path)}
		entry.Persistent = entry.Kind == "data"
		entry.Mounted = coveringMount(detail.Mounts, path)
		out.Entries = append(out.Entries, entry)
		if entry.Mounted == "" {
			out.Accounted += size
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Size > out.Entries[j].Size })

	for _, entry := range out.Entries {
		if entry.Mounted == "" && entry.Persistent && entry.Size > 0 {
			out.Unbacked = append(out.Unbacked, entry)
		}
	}
	return out, nil
}

// measureDirectories runs one `du` inside the container.
//
// One invocation, not one per directory: an exec costs a round trip and a
// process, and a container with twelve candidate roots would otherwise pay for
// twelve. `-x` keeps it on the container's own filesystem so a bind-mounted
// host directory is not walked by accident; the paths are a fixed list defined
// in this file and never come from a request, so there is nothing here a
// caller could inject.
func (c *Client) measureDirectories(ctx context.Context, id string) (map[string]int64, string, error) {
	args := append([]string{"du", "-x", "-s", "-k"}, scanRoots...)
	ctx, cancel := context.WithTimeout(ctx, writableScanTimeout)
	defer cancel()

	// A non-zero exit is expected and is not a failure: `du` returns 1 when
	// any of the paths does not exist, which is true of most containers for
	// most of this list, and still prints a line for every path that does.
	_, raw, err := c.ExecCheck(ctx, id, args, writableScanTimeout)
	sizes := parseDu(string(raw))
	if len(sizes) == 0 {
		if err != nil {
			return nil, "", errors.New("`du` could not be run inside this container: " + err.Error() +
				". Minimal images often ship no shell utilities, in which case the breakdown cannot be measured from inside.")
		}
		return nil, "", errors.New("`du` produced nothing readable inside this container")
	}
	return sizes, "`du -x -s -k` run inside the container", nil
}

// parseDu reads `du -k` output: a size in kibibytes, a tab, a path.
func parseDu(out string) map[string]int64 {
	sizes := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field, path, found := strings.Cut(line, "\t")
		if !found {
			// Some implementations separate with spaces.
			field, path, found = strings.Cut(line, " ")
			if !found {
				continue
			}
			path = strings.TrimSpace(path)
		}
		kb, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
		if err != nil || path == "" {
			continue
		}
		if strings.Contains(path, "No such file") || strings.Contains(path, "cannot") {
			continue
		}
		sizes[path] = kb * 1024
	}
	return sizes
}

// coveringMount returns the mount destination that contains a path, if any.
// A directory under a volume is storage that survives; the same directory with
// nothing mounted at it is not.
func coveringMount(mounts []MountPoint, path string) string {
	best := ""
	for _, m := range mounts {
		dest := strings.TrimSuffix(m.Destination, "/")
		if dest == "" {
			continue
		}
		if path == dest || strings.HasPrefix(path, dest+"/") {
			if len(dest) > len(best) {
				best = m.Destination
			}
		}
	}
	return best
}

// classifyPath guesses what a directory is for.
//
// A guess, and treated as one: it decides wording and ordering, never an
// action. /var/log is logs on every image there has ever been; /app/data is
// data on most of them and is a build directory on a few.
func classifyPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/var/log"):
		return "logs"
	case strings.HasPrefix(path, "/var/cache"), strings.Contains(path, "/cache"):
		return "cache"
	case path == "/tmp", strings.HasPrefix(path, "/tmp/"),
		path == "/var/tmp", strings.HasPrefix(path, "/var/tmp/"):
		return "temporary"
	case strings.HasPrefix(path, "/var/lib"), strings.HasPrefix(path, "/data"),
		strings.HasPrefix(path, "/app"), strings.HasPrefix(path, "/srv"),
		strings.HasPrefix(path, "/home"), strings.HasPrefix(path, "/mnt"),
		strings.HasPrefix(path, "/root"):
		return "data"
	default:
		return "other"
	}
}

// MigrationPlan is the guided move from a writable-layer directory to a named
// volume, written out rather than performed.
//
// Deliberately not automatic. The safe version of this needs the service
// stopped, the data copied, the compose file edited and the service started
// again, with a rollback if any step fails — and the copy is of data the
// operator has told us they cannot afford to lose. So the dashboard produces
// the exact plan, the exact commands and the exact compose change, and the
// operator runs it. Everything here is a description; nothing in this file
// executes any of it.
type MigrationPlan struct {
	Container string `json:"container"`
	Service   string `json:"service,omitempty"`
	Stack     string `json:"stack,omitempty"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Volume    string `json:"volume"`

	// Steps are what will happen, in order, in sentences.
	Steps []MigrationStep `json:"steps"`
	// Commands are the same plan as a shell script the operator can read
	// before running. Kept as text rather than executed: see the type comment.
	Commands []string `json:"commands"`
	// ComposePatch is the fragment to add to the compose file, when this
	// container belongs to a stack.
	ComposePatch string `json:"composePatch,omitempty"`
	// Warnings are the things that make this dangerous for this particular
	// container.
	Warnings []string `json:"warnings"`
}

type MigrationStep struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Reversible says whether this step can be undone if the next one fails.
	Reversible bool `json:"reversible"`
}

// PlanMigration writes the plan for moving one directory onto a named volume.
func (c *Client) PlanMigration(ctx context.Context, id, path string) (*MigrationPlan, error) {
	detail, err := c.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	path = "/" + strings.Trim(strings.TrimSpace(path), "/")
	if path == "/" {
		return nil, errors.New("a directory inside the container is required")
	}
	if dest := coveringMount(detail.Mounts, path); dest != "" {
		return nil, errors.New(path + " is already backed by the mount at " + dest)
	}

	plan := &MigrationPlan{
		Container: detail.Name,
		Service:   detail.ComposeSvc,
		Stack:     detail.ComposeStack,
		Path:      path,
		Volume:    SuggestVolumeName(detail.Name, path),
		Warnings:  []string{},
	}
	if report, err := c.AnalyzeWritableLayer(ctx, id, false); err == nil {
		for _, entry := range report.Entries {
			if entry.Path == path {
				plan.Size = entry.Size
			}
		}
	}

	stopped := "The service is down for the length of the copy."
	if plan.Size > 0 {
		stopped += " " + humanBytes(plan.Size) + " has to be copied, so budget accordingly."
	}
	plan.Steps = []MigrationStep{
		{
			Title:      "Create the volume",
			Detail:     "A new named volume called " + plan.Volume + ". Nothing uses it yet, so this changes nothing that is running.",
			Reversible: true,
		},
		{
			Title:      "Copy the data out while the container is stopped",
			Detail:     "Stopping first is what makes the copy consistent: copying a database that is being written to produces a corrupt copy that looks fine. " + stopped,
			Reversible: true,
		},
		{
			Title:      "Mount the volume at " + path,
			Detail:     "For a compose stack this is an edit to the compose file, so it survives the next deploy. For a standalone container it means recreating it with the mount added.",
			Reversible: true,
		},
		{
			Title:      "Start it and check",
			Detail:     "The service comes back reading from the volume. Its own logs and health check are what say whether the data arrived intact.",
			Reversible: true,
		},
		{
			Title:      "Keep the original until you are sure",
			Detail:     "The old data stays in the previous container's writable layer until that container is removed. Do not remove it until the service has been running correctly for long enough to trust.",
			Reversible: false,
		},
	}

	quoted := shellQuote(path)
	plan.Commands = []string{
		"docker volume create " + shellQuote(plan.Volume),
		"docker stop " + shellQuote(detail.Name),
		"# copy the existing data into the new volume, using a throwaway container",
		"docker run --rm -v " + shellQuote(plan.Volume) + ":/dest --volumes-from " + shellQuote(detail.Name) +
			" alpine sh -c " + shellQuote("cp -a "+quoted+"/. /dest/"),
		"# then add the mount and start the service again",
	}
	if plan.Stack != "" {
		plan.ComposePatch = "services:\n  " + plan.Service + ":\n    volumes:\n      - " +
			plan.Volume + ":" + path + "\n\nvolumes:\n  " + plan.Volume + ":\n"
		plan.Commands = append(plan.Commands,
			"docker compose up -d "+shellQuote(plan.Service))
	} else {
		plan.Commands = append(plan.Commands,
			"# recreate the container with -v "+plan.Volume+":"+path)
	}

	if detail.State == "running" {
		plan.Warnings = append(plan.Warnings,
			"This container is running. The copy is only consistent if it is stopped first, and for a database that is not optional — copying live database files produces a copy that mounts and then fails.")
	}
	if plan.Stack != "" {
		plan.Warnings = append(plan.Warnings,
			"This container belongs to the compose stack "+plan.Stack+". Add the volume to the compose file rather than to the container, or the next deploy will undo it.")
	}
	plan.Warnings = append(plan.Warnings,
		"Nothing here has been carried out. These are the steps and the exact commands; run them yourself so you can stop between any two of them.")
	return plan, nil
}
