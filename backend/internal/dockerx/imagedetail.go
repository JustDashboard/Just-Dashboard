package dockerx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
)

// ImageDetail is everything worth knowing about an image before running or
// deleting it.
//
// The list view answers "how big and how old". This answers the two questions
// that actually decide an action: what will this image do when it starts
// (entrypoint, command, ports, volumes, the user it runs as), and what is
// still using it. An image row with "0 containers" next to it is the only
// thing standing between an operator and deleting the image their production
// service will need the next time it restarts — so "used by" is resolved
// against stopped containers too, which Docker's own `Containers` count does
// not do reliably.
type ImageDetail struct {
	ID           string            `json:"id"`
	RepoTags     []string          `json:"repoTags"`
	RepoDigests  []string          `json:"repoDigests"`
	Size         int64             `json:"size"`
	Created      time.Time         `json:"created"`
	Architecture string            `json:"architecture,omitempty"`
	OS           string            `json:"os,omitempty"`
	Author       string            `json:"author,omitempty"`
	Labels       map[string]string `json:"labels"`

	Entrypoint   []string `json:"entrypoint"`
	Command      []string `json:"command"`
	WorkingDir   string   `json:"workingDir,omitempty"`
	User         string   `json:"user,omitempty"`
	ExposedPorts []string `json:"exposedPorts"`
	VolumePaths  []string `json:"volumePaths"`
	// Env is the image's baked-in environment. Credential-shaped values are
	// masked by the handler for the same reason a container's are: an image
	// built from a Dockerfile with an ARG-turned-ENV secret is a common enough
	// mistake that the dashboard should not be the thing that publishes it.
	Env    []string     `json:"env"`
	Layers []ImageLayer `json:"layers"`
	// UsedBy names the containers built from this image, running or not.
	UsedBy []ImageUser `json:"usedBy"`

	// How this image can be referred to, and what that allows.
	//
	// Kind is what the *requested* reference was — a tag, a digest, a bare
	// image id, or Docker's dangling placeholder. Ref is the name the UI
	// should lead with. Pullable and Checkable say whether the pull and
	// "check for updates" controls have anything to act on: an image built
	// here has no registry to ask, and offering the button anyway is offering
	// a button whose only outcome is an error.
	Kind       ImageRefKind `json:"kind"`
	Ref        string       `json:"ref"`
	Pullable   bool         `json:"pullable"`
	Checkable  bool         `json:"checkable"`
	MovingTag  bool         `json:"movingTag"`
	Dangling   bool         `json:"dangling"`
	LocalBuild bool         `json:"localBuild"`
}

// ImageLayer is one line of `docker history`, which is the only place the
// answer to "why is this image 1.8 GB" is written down.
type ImageLayer struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	// CreatedBy is the Dockerfile instruction, cleaned of the builder's
	// `/bin/sh -c #(nop)` noise that makes the raw output unreadable.
	CreatedBy string   `json:"createdBy"`
	Size      int64    `json:"size"`
	Comment   string   `json:"comment,omitempty"`
	Tags      []string `json:"tags"`
}

type ImageUser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Stack   string `json:"stack,omitempty"`
	Service string `json:"service,omitempty"`
}

// InspectImage answers for any of the four things an image string can be.
//
// The requested reference is classified before it reaches the Engine rather
// than normalised blindly. An id and a digest are exact and are passed
// through; only a name gets the implicit `:latest`. Sending an id through the
// name path is what produced `invalid reference format: repository name
// (library/sha256…) must be lowercase` for every attempt to open an image by
// its id — including every dangling image, which has no other handle.
func (c *Client) InspectImage(ctx context.Context, ref string) (*ImageDetail, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	requested := ClassifyImageRef(ref)
	ref = NormalizeImageRef(ref)
	insp, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	d := &ImageDetail{
		ID:           insp.ID,
		RepoTags:     insp.RepoTags,
		RepoDigests:  insp.RepoDigests,
		Size:         insp.Size,
		Architecture: insp.Architecture,
		OS:           insp.Os,
		Author:       insp.Author,
		Labels:       map[string]string{},
		Entrypoint:   []string{},
		Command:      []string{},
		ExposedPorts: []string{},
		VolumePaths:  []string{},
		Env:          []string{},
		Layers:       []ImageLayer{},
		UsedBy:       []ImageUser{},
	}
	d.RepoTags = orEmpty(d.RepoTags)
	d.RepoDigests = orEmpty(d.RepoDigests)
	if t, err := time.Parse(time.RFC3339Nano, insp.Created); err == nil {
		d.Created = t.UTC()
	}
	if insp.Config != nil {
		// orEmpty rather than a plain assignment: these are initialised to
		// empty above, and an image whose config leaves one nil would
		// otherwise overwrite that with nil and put a `null` on the wire where
		// the client expects an array.
		d.Entrypoint = orEmpty(insp.Config.Entrypoint)
		d.Command = orEmpty(insp.Config.Cmd)
		d.WorkingDir = insp.Config.WorkingDir
		d.User = insp.Config.User
		d.Env = orEmpty(insp.Config.Env)
		if insp.Config.Labels != nil {
			d.Labels = insp.Config.Labels
		}
		for port := range insp.Config.ExposedPorts {
			d.ExposedPorts = append(d.ExposedPorts, string(port))
		}
		sort.Strings(d.ExposedPorts)
		for path := range insp.Config.Volumes {
			d.VolumePaths = append(d.VolumePaths, path)
		}
		sort.Strings(d.VolumePaths)
	}
	if hist, err := cli.ImageHistory(ctx, ref); err == nil {
		for _, h := range hist {
			d.Layers = append(d.Layers, ImageLayer{
				ID:        h.ID,
				Created:   time.Unix(h.Created, 0).UTC(),
				CreatedBy: cleanHistoryLine(h.CreatedBy),
				Size:      h.Size,
				Comment:   h.Comment,
				Tags:      orEmpty(h.Tags),
			})
		}
	}
	d.UsedBy = c.imageUsers(ctx, insp.ID)
	d.describeReference(requested)
	return d, nil
}

// describeReference records what this image can be asked to do.
//
// An image with no repository digest never came from a registry: it was built
// on this host, so "no update available" and "cannot be checked" are the same
// sentence and the UI should say the true one. A dangling image has no name at
// all, which is why it is reachable only by id and why nothing here offers to
// pull it.
func (d *ImageDetail) describeReference(requested ImageRefKind) {
	d.Ref = PreferredRef(d.ID, d.RepoTags, d.RepoDigests)
	d.Dangling = len(d.RepoTags) == 0 ||
		(len(d.RepoTags) == 1 && ClassifyImageRef(d.RepoTags[0]) == RefDangling)
	d.LocalBuild = len(d.RepoDigests) == 0

	d.Kind = requested
	if d.Kind == RefUnknown || d.Kind == RefDangling {
		d.Kind = ClassifyImageRef(d.Ref)
	}
	if d.Dangling {
		d.Kind = RefDangling
	}

	// Pulling and checking are properties of the *name*, not of the request:
	// an image opened by id that still carries a tag can be pulled by that
	// tag, and saying otherwise would hide a working control.
	d.Pullable = !d.Dangling && Pullable(d.Ref)
	d.Checkable = d.Pullable && !d.LocalBuild && Checkable(d.Ref)
	d.MovingTag = MovingTag(d.Ref)
}

// cleanHistoryLine strips the wrapper the classic builder records around every
// instruction. `/bin/sh -c #(nop) COPY file:abc… in /app` is the same
// information as `COPY file: in /app` with forty characters of noise in front
// of it, and the noise is what makes `docker history` unreadable.
func cleanHistoryLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/bin/sh -c #(nop) ")
	if rest, found := strings.CutPrefix(s, "/bin/sh -c "); found {
		s = "RUN " + rest
	}
	// BuildKit records instructions with a leading step marker.
	if _, rest, found := strings.Cut(s, "|"); found && strings.HasPrefix(s, "|") {
		s = strings.TrimSpace(rest)
	}
	return strings.TrimSpace(s)
}

func (c *Client) imageUsers(ctx context.Context, imageID string) []ImageUser {
	out := []ImageUser{}
	list, err := c.ListContainers(ctx, true)
	if err != nil {
		return out
	}
	for _, ct := range list {
		if ct.ImageID != imageID && !strings.HasPrefix(imageID, ct.ImageID) && ct.ImageID != "" {
			continue
		}
		out = append(out, ImageUser{
			ID: ct.ID, Name: ct.Name, State: ct.State,
			Stack: ct.ComposeStack, Service: ct.ComposeSvc,
		})
	}
	return out
}

// UpdateStatus is the answer to the question every self-hoster actually has
// about an image: is the tag I am running still what that tag points at?
//
// Nobody else in this class of tool answers it without a second daemon.
// Watchtower answers it and then restarts things unasked; Portainer's version
// is a paid feature. It is one registry request per tag: the digest the
// registry currently serves for `nginx:alpine`, compared against the digest
// that was pulled. A container is out of date when its image's digest differs,
// which is a different and much more useful question than "is there a newer
// tag", because it catches the case that matters — a moving tag that moved.
type UpdateStatus struct {
	Ref string `json:"ref"`
	// State is "current", "outdated", "unknown" or "local".
	//   current  — the registry's digest matches what is here.
	//   outdated — the tag now points somewhere else. Pull to move.
	//   local    — never came from a registry (built here), so there is
	//              nothing to compare against and that is not a failure.
	//   unknown  — the registry could not be asked. Private registries
	//              needing credentials land here, as do rate limits.
	//   pinned   — named by digest, so it is the same image every time and
	//              there is nothing to check.
	State        string    `json:"state"`
	LocalDigest  string    `json:"localDigest,omitempty"`
	RemoteDigest string    `json:"remoteDigest,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	CheckedAt    time.Time `json:"checkedAt"`
}

// updateCacheTTL keeps a registry answer for long enough that opening the
// images tab twice does not spend two round trips per image — and short
// enough that "check for updates" after a release means something. Docker Hub
// rate-limits manifest requests by IP, so this is also what stops a dashboard
// left open on a screen from burning the host's quota.
const updateCacheTTL = 30 * time.Minute

type updateEntry struct {
	status UpdateStatus
	at     time.Time
}

var (
	updateMu    sync.Mutex
	updateCache = map[string]updateEntry{}
)

// CheckUpdate asks the registry what a tag points at now.
//
// Never fails the caller: a registry that cannot be reached, or one that wants
// credentials the dashboard does not have, is reported as "unknown" with the
// reason attached. An update check that errors is not worth interrupting a
// page for.
func (c *Client) CheckUpdate(ctx context.Context, ref string, force bool) UpdateStatus {
	ref = ImageRef(ref)
	if !force {
		updateMu.Lock()
		entry, ok := updateCache[ref]
		updateMu.Unlock()
		if ok && time.Since(entry.at) < updateCacheTTL {
			return entry.status
		}
	}
	status := c.checkUpdate(ctx, ref)
	updateMu.Lock()
	updateCache[ref] = updateEntry{status: status, at: time.Now()}
	updateMu.Unlock()
	return status
}

func (c *Client) checkUpdate(ctx context.Context, ref string) UpdateStatus {
	out := UpdateStatus{Ref: ref, State: "unknown", CheckedAt: time.Now().UTC()}
	// A reference that cannot move has no update to find, and reporting that
	// as "unknown" reads as a failed check rather than as the answer. A digest
	// reference is pinned by construction; a bare image id is a local object
	// with no registry name at all.
	switch ClassifyImageRef(ref) {
	case RefDigest:
		out.State = "pinned"
		out.Reason = "this reference names an exact image by digest, so it cannot change under you"
		if _, digest, ok := strings.Cut(ref, "@"); ok {
			out.LocalDigest = digest
		}
		return out
	case RefID, RefDangling:
		out.State = "local"
		out.Reason = "this image has no registry name, so there is nothing to compare it against"
		return out
	}
	cli, err := c.api()
	if err != nil {
		out.Reason = err.Error()
		return out
	}
	insp, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		out.Reason = "this image is not present locally"
		return out
	}
	if len(insp.RepoDigests) == 0 {
		// No manifest digest means the image was never pulled from or pushed
		// to a registry — it was built here. There is nothing to compare it
		// against, and saying "unknown" would read as a failure.
		out.State = "local"
		out.Reason = "built on this server rather than pulled, so there is no registry copy to compare against"
		return out
	}
	for _, d := range insp.RepoDigests {
		if _, digest, ok := strings.Cut(d, "@"); ok {
			out.LocalDigest = digest
			break
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dist, err := cli.DistributionInspect(ctx, ref, "")
	if err != nil {
		// An image built here can still carry a digest — BuildKit writes one —
		// so "no RepoDigests" is not a reliable test for "never came from a
		// registry", and locally built images were landing in `unknown` with
		// a message about credentials that made no sense for them.
		//
		// The reliable signal is the name. A bare reference like `my-app:1`
		// resolves to docker.io/**library**/my-app, and the library namespace
		// holds official images only — nobody can have a private one there.
		// So a registry refusing a bare name means no registry has this image,
		// which is what "built here" looks like. A namespaced or hosted
		// reference (`me/app`, `ghcr.io/org/app`) genuinely could be private,
		// and keeps the credentials message.
		if isBareReference(ref) && registryDeniedOrMissing(err) {
			out.State = "local"
			out.Reason = "no registry has an image by this name, which is what an image built on this server looks like"
			return out
		}
		out.Reason = registryReason(err)
		return out
	}
	out.RemoteDigest = dist.Descriptor.Digest.String()
	if out.RemoteDigest == "" || out.LocalDigest == "" {
		out.Reason = "the registry did not report a digest"
		return out
	}
	if out.RemoteDigest == out.LocalDigest {
		out.State = "current"
		return out
	}
	out.State = "outdated"
	return out
}

// isBareReference reports a reference with no registry host and no namespace,
// which Docker resolves into docker.io/library/<name>.
func isBareReference(ref string) bool {
	// The tag is the part after the last colon *that follows the last slash* —
	// splitting at the first colon reads the port in `registry:5000/app:v1` as
	// a tag and the host as the whole name, which then looks bare. Same rule
	// ImageRef uses to decide whether a tag is present at all.
	name := ref
	if lastColon, lastSlash := strings.LastIndex(ref, ":"), strings.LastIndex(ref, "/"); lastColon > lastSlash {
		name = ref[:lastColon]
	}
	return !strings.Contains(name, "/")
}

// registryDeniedOrMissing covers the two ways a registry says "not here".
// Docker Hub answers 401 rather than 404 for a repository that does not exist,
// which is why both have to count.
func registryDeniedOrMissing(err error) bool {
	lower := strings.ToLower(err.Error())
	for _, hint := range []string{"unauthorized", "authentication required", "manifest unknown", "not found", "repository does not exist"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// registryReason turns the SDK's transport errors into something an operator
// can act on. "unauthorized" from a private registry is the common case and is
// not a bug in anything.
func registryReason(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication required"):
		return "the registry wants credentials the dashboard does not have. Pull from a shell where you are logged in, or leave it: this only affects the update check."
	case strings.Contains(lower, "toomanyrequests"), strings.Contains(lower, "rate limit"):
		return "the registry is rate-limiting this server's address. It will answer again shortly."
	case strings.Contains(lower, "no such host"), strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline"):
		return "the registry could not be reached from this server."
	case strings.Contains(lower, "manifest unknown"), strings.Contains(lower, "not found"):
		return "the registry no longer has this tag."
	default:
		return msg
	}
}

// CheckUpdates answers for a whole list at once, concurrently but politely: a
// small worker pool, because a host with forty images should not open forty
// simultaneous connections to Docker Hub and be rate-limited for it.
func (c *Client) CheckUpdates(ctx context.Context, refs []string, force bool) map[string]UpdateStatus {
	out := map[string]UpdateStatus{}
	if len(refs) == 0 {
		return out
	}
	const workers = 4
	type result struct {
		ref    string
		status UpdateStatus
	}
	jobs := make(chan string)
	results := make(chan result, len(refs))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ref := range jobs {
				results <- result{ref: ref, status: c.CheckUpdate(ctx, ref, force)}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, ref := range refs {
			select {
			case <-ctx.Done():
				return
			case jobs <- ref:
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	for r := range results {
		out[r.ref] = r.status
	}
	return out
}

func (c *Client) TagImage(ctx context.Context, source, target string) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	return cli.ImageTag(ctx, source, ImageRef(target))
}

// BuildOptions describes a build from a directory on this server.
type BuildOptions struct {
	// Dir is the build context. The caller has already checked it against the
	// configured roots.
	Dir        string            `json:"dir"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Tag        string            `json:"tag"`
	NoCache    bool              `json:"noCache,omitempty"`
	Pull       bool              `json:"pull,omitempty"`
	Target     string            `json:"target,omitempty"`
	BuildArgs  map[string]string `json:"buildArgs,omitempty"`
}

var ErrNoDockerfile = errors.New("no Dockerfile in that directory")

// Build runs a build and streams its output.
//
// This drives the `docker` binary rather than the Engine's build endpoint, for
// the same reason RunCompose does: BuildKit is a separate builder that the
// classic API path does not reach, and a dashboard that silently built with
// the legacy builder would produce images that differ from the ones the same
// Dockerfile produces from a shell — different caching, no mount or secret
// support, different layer output. Being wrong in a way that only shows up
// later is worse than shelling out. The argument vector is built explicitly
// and never passed through a shell.
func (c *Client) Build(ctx context.Context, opts BuildOptions, out chan<- LogLine) (int, error) {
	if !dirExists(opts.Dir) {
		return -1, os.ErrNotExist
	}
	dockerfile := opts.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if _, err := os.Stat(filepath.Join(opts.Dir, dockerfile)); err != nil {
		return -1, fmt.Errorf("%w: looked for %s", ErrNoDockerfile, dockerfile)
	}
	if strings.TrimSpace(opts.Tag) == "" {
		return -1, errors.New("a name for the built image is required")
	}

	args := []string{"build", "--file", dockerfile, "--tag", ImageRef(opts.Tag)}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if opts.Pull {
		args = append(args, "--pull")
	}
	if opts.Target != "" {
		args = append(args, "--target", opts.Target)
	}
	for k, v := range opts.BuildArgs {
		args = append(args, "--build-arg", k+"="+v)
	}
	args = append(args, ".")

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = opts.Dir
	// Plain progress: the default TTY renderer redraws with escape codes that
	// are meaningless once the output is a list of lines in a browser.
	cmd.Env = append(os.Environ(), "BUILDKIT_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
	return streamCommand(ctx, cmd, out)
}

// streamCommand runs a command and forwards its output line by line, keeping
// stdout and stderr apart so the UI can colour them. Shared by Build and the
// streaming compose runner.
func streamCommand(ctx context.Context, cmd *exec.Cmd, out chan<- LogLine) (int, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	scan := func(r *bufio.Scanner, stream string) {
		defer wg.Done()
		r.Buffer(make([]byte, 0, 64*1024), maxLogLine)
		for r.Scan() {
			select {
			case <-ctx.Done():
				return
			case out <- LogLine{Stream: stream, Text: strings.TrimRight(r.Text(), "\r")}:
			}
		}
	}
	go scan(bufio.NewScanner(stdout), "stdout")
	go scan(bufio.NewScanner(stderr), "stderr")
	wg.Wait()
	err = cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if err != nil && code == 0 {
		code = -1
	}
	return code, nil
}

// PullAndReport pulls an image and says what changed, which is what makes a
// pull button honest: "already up to date" and "downloaded 300 MB" look
// identical in a progress stream that has scrolled past.
type PullResult struct {
	Ref     string `json:"ref"`
	Updated bool   `json:"updated"`
	Digest  string `json:"digest,omitempty"`
	Before  string `json:"before,omitempty"`
}

func (c *Client) PullAndReport(ctx context.Context, ref string, out chan<- PullProgress) (*PullResult, error) {
	ref = ImageRef(ref)
	res := &PullResult{Ref: ref}
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	if before, err := cli.ImageInspect(ctx, ref); err == nil {
		res.Before = before.ID
	}
	if err := c.PullImage(ctx, ref, out); err != nil {
		return nil, err
	}
	if after, err := cli.ImageInspect(ctx, ref); err == nil {
		res.Digest = after.ID
		res.Updated = res.Before != "" && res.Before != after.ID
		if res.Before == "" {
			res.Updated = true
		}
	}
	// The cached verdict is stale the moment a pull finishes.
	updateMu.Lock()
	delete(updateCache, ref)
	updateMu.Unlock()
	return res, nil
}

// PruneBuildCache reclaims BuildKit's cache, which lives outside the image
// store and is routinely the largest single consumer on a server that builds —
// which is why `docker system df` reports it as its own line and why an
// operator staring at a full disk is nearly always staring at this.
//
// It is exported because it was not, and that was the bug: the helper existed,
// its comment claimed it was "part of reclaim everything", and nothing in the
// product ever called it. A dashboard reporting forty gigabytes of reclaimable
// cache with no route able to reclaim any of it is worse than one that never
// mentioned the cache at all.
//
// `all` is the difference between `docker builder prune` and `docker builder
// prune -a`: without it only cache no longer reachable from any build is
// removed, with it every entry not currently in use goes. The reclaimable
// figure this dashboard reports is the second one, so the button that quotes
// that figure passes true.
func (c *Client) PruneBuildCache(ctx context.Context, all bool) (PruneReport, error) {
	cli, err := c.api()
	if err != nil {
		return PruneReport{}, err
	}
	rep, err := cli.BuildCachePrune(ctx, build.CachePruneOptions{All: all})
	if err != nil {
		return PruneReport{}, err
	}
	defer c.forgetDiskUsage()
	items := rep.CachesDeleted
	if items == nil {
		items = []string{}
	}
	return PruneReport{Kind: "build cache", SpaceReclaimed: rep.SpaceReclaimed, Items: items}, nil
}

// containerImageRefs is the set of image references containers are actually
// running, which is the list worth checking for updates: an image nothing uses
// is one nobody needs told about.
func (c *Client) containerImageRefs(ctx context.Context) []string {
	list, err := c.ListContainers(ctx, true)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	refs := []string{}
	for _, ct := range list {
		ref := ImageRef(ct.Image)
		if ref == "" || seen[ref] || strings.HasPrefix(ref, "sha256:") {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

// InspectRaw returns the Engine's own inspect document, decoded only enough to
// be re-encoded as JSON.
//
// A map rather than a typed struct on purpose: the point of the raw view is to
// show fields this dashboard's types do not have, which a struct would drop on
// the floor — including whatever a newer Engine adds after this code was
// written.
func (c *Client) InspectRaw(ctx context.Context, id string) (map[string]any, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	_, raw, err := cli.ContainerInspectWithRaw(ctx, id, true)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// FileChange is one path the container has written since it started from its
// image.
type FileChange struct {
	Path string `json:"path"`
	// Kind is "modified", "added" or "deleted".
	Kind string `json:"kind"`
}

// Changes answers "what has this container written to itself".
//
// Every one of these paths is data living in the writable layer rather than in
// a volume, which means it is invisible to backups and destroyed the next time
// the container is recreated. Docker exposes it and no dashboard in this class
// shows it, which is why "I updated the image and lost my database" is a genre
// of support question rather than a rare accident.
func (c *Client) Changes(ctx context.Context, id string) ([]FileChange, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	raw, err := cli.ContainerDiff(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]FileChange, 0, len(raw))
	for _, ch := range raw {
		out = append(out, FileChange{Path: ch.Path, Kind: changeKind(ch.Kind)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func changeKind(k container.ChangeType) string {
	switch k {
	case container.ChangeModify:
		return "modified"
	case container.ChangeAdd:
		return "added"
	case container.ChangeDelete:
		return "deleted"
	default:
		return "modified"
	}
}

// CheckRunningUpdates checks the images containers are actually using, which
// is the list worth spending registry round trips on.
func (c *Client) CheckRunningUpdates(ctx context.Context, force bool) map[string]UpdateStatus {
	return c.CheckUpdates(ctx, c.containerImageRefs(ctx), force)
}
