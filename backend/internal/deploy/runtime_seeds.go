package deploy

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// RuntimeSeed is something a release puts into one of its managed volumes
// before its container starts: an install-time file a definition rendered,
// or a download verified against the digest the definition pins. A seed is
// written only where nothing is yet, so the first release of a game server
// writes its settings file, and every later release, rollback and restart
// leaves the operator's edits, the world and a newer download alone.
type RuntimeSeed struct {
	// Mount is the target of the plan mount the seed lives under, and Path
	// is where inside that mount, relative to it.
	Mount string `json:"mount"`
	Path  string `json:"path"`
	// Content is a file's text. Its only placeholders are
	// {{variable.NAME}} references to the release's own variables, filled in
	// as the helper writes it, so the plan holds no value. Format
	// "properties" escapes a filled-in value as a Java properties file reads
	// it.
	Content string `json:"content,omitempty"`
	Format  string `json:"format,omitempty"`
	// URL, Checksum and MaxBytes are an artifact's: an https source, the
	// sha256 its bytes must have, and how many bytes it may be.
	URL            string `json:"url,omitempty"`
	Checksum       string `json:"checksum,omitempty"`
	MaxBytes       int64  `json:"maxBytes,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// Seed bounds. A file is a configuration file, not a payload; an artifact
// has the catalogue's own ceiling.
const (
	maxRuntimeSeeds       = 32
	maxSeedContentBytes   = 64 << 10
	maxSeedArtifactBytes  = 8 << 30
	defaultSeedFetchLimit = 10 * time.Minute
	maxSeedFetchSeconds   = 3600
)

// SeedFetchImage is the helper that downloads an artifact into a volume. It
// is pinned by digest like every image a release runs, it runs as the
// volume's owner with that one volume mounted, and nothing it fetches is used
// until the dashboard has read the bytes back and compared their sha256.
const SeedFetchImage = "curlimages/curl:8.17.0@sha256:935d9100e9ba842cdb060de42472c7ca90cfe9a7c96e4dacb55e79e560b3ff40"

var (
	seedReferenceRE  = regexp.MustCompile(`\{\{variable\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	seedPlaceholder  = regexp.MustCompile(`\{\{[^}]*\}\}`)
	seedDigestRE     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	seedSecretLineRE = regexp.MustCompile(`(?i)^\s*[A-Za-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)[A-Za-z0-9_.-]*\s*[=:]\s*(.*?)\s*$`)
)

// validateRuntimeSeeds holds every seed to the volume it names and to the
// plan's own rule about values: a file may carry references to the release's
// variables and no credential of its own.
func validateRuntimeSeeds(c PlanConfiguration) error {
	seeds := c.Runtime.Seeds
	if len(seeds) == 0 {
		return nil
	}
	if len(seeds) > maxRuntimeSeeds {
		return invalidField("runtime.seeds", "a release writes at most %d seeds", maxRuntimeSeeds)
	}
	if c.Build.Method == BuildCompose || c.Build.Method == BuildLegacyCompose {
		return invalidField("runtime.seeds", "a Compose release mounts its own volumes; seeds belong to a single container")
	}
	mounts := map[string]RuntimeMount{}
	for _, mount := range c.Runtime.Mounts {
		mounts[filepath.Clean(mount.Target)] = mount
	}
	variables := map[string]bool{}
	for _, variable := range c.Variables {
		variables[variable.Name] = true
	}
	seen := map[string]bool{}
	for index, seed := range seeds {
		field := fmt.Sprintf("runtime.seeds[%d]", index)
		mount, found := mounts[filepath.Clean(seed.Mount)]
		if !found || mount.Ownership != OwnershipManaged || filepath.IsAbs(mount.Source) || strings.Contains(mount.Source, "/") {
			return invalidField(field, "a seed is written into a managed volume the plan mounts")
		}
		if seed.Path == "" || len(seed.Path) > 512 || path.IsAbs(seed.Path) || path.Clean(seed.Path) != seed.Path ||
			strings.HasPrefix(seed.Path, "..") || strings.ContainsAny(seed.Path, "\x00\r\n") {
			return invalidField(field, "a seed's path is a clean path inside its volume")
		}
		key := filepath.Clean(seed.Mount) + "\x00" + seed.Path
		if seen[key] {
			return invalidField(field, "%s/%s is seeded twice", seed.Mount, seed.Path)
		}
		seen[key] = true
		file, artifact := seed.Content != "", seed.URL != ""
		switch {
		case file == artifact:
			return invalidField(field, "a seed is either a file's content or an artifact's URL")
		case file:
			if seed.Format != "" && seed.Format != "properties" {
				return invalidField(field, "a seed's format is properties or plain text")
			}
			if len(seed.Content) > maxSeedContentBytes || strings.ContainsRune(seed.Content, 0) ||
				seed.Checksum != "" || seed.MaxBytes != 0 || seed.TimeoutSeconds != 0 {
				return invalidField(field, "a seeded file holds at most %d KiB of text and nothing an artifact takes", maxSeedContentBytes>>10)
			}
			references := seedReferenceRE.FindAllStringSubmatch(seed.Content, -1)
			if len(seedPlaceholder.FindAllString(seed.Content, -1)) != len(references) {
				return invalidField(field, "a seeded file's only placeholders are {{variable.NAME}} references")
			}
			for _, reference := range references {
				if !variables[reference[1]] {
					return invalidField(field, "a seeded file references %s, which is not a variable of this plan", reference[1])
				}
			}
			for _, line := range strings.Split(seed.Content, "\n") {
				if match := seedSecretLineRE.FindStringSubmatch(line); match != nil && match[1] != "" &&
					!seedReferenceRE.MatchString(match[1]) {
					return invalidField(field, "a seeded file contains credential material; reference a generated or sealed variable")
				}
			}
		default:
			parsed, err := url.Parse(seed.URL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
				strings.ContainsAny(seed.URL, " \x00\r\n") {
				return invalidField(field, "an artifact is fetched from an https URL without credentials")
			}
			if !seedDigestRE.MatchString(seed.Checksum) {
				return invalidField(field, "an artifact's bytes are pinned by a sha256 checksum")
			}
			if seed.MaxBytes <= 0 || seed.MaxBytes > maxSeedArtifactBytes {
				return invalidField(field, "an artifact needs a size limit of at most 8 GiB")
			}
			if seed.TimeoutSeconds < 0 || seed.TimeoutSeconds > maxSeedFetchSeconds || seed.Format != "" {
				return invalidField(field, "an artifact's download is bounded by at most an hour")
			}
		}
	}
	return nil
}

// blueprintSeeds maps a render's install files and artifacts onto the plan
// mounts the definition's volumes became.
func blueprintSeeds(definition *blueprint.Blueprint, rendered *blueprint.Plan) []RuntimeSeed {
	seeds := []RuntimeSeed{}
	for _, file := range rendered.InstallFiles {
		if volume, relative, found := blueprint.VolumeFor(definition, file.Path); found {
			seeds = append(seeds, RuntimeSeed{Mount: volume.Target, Path: relative, Content: file.Content, Format: file.Format})
		}
	}
	for _, artifact := range rendered.Artifacts {
		if artifact.Checksum == "" {
			continue
		}
		if volume, relative, found := blueprint.VolumeFor(definition, artifact.Path); found {
			seeds = append(seeds, RuntimeSeed{
				Mount: volume.Target, Path: relative, URL: artifact.URL, Checksum: artifact.Checksum,
				MaxBytes: artifact.MaxBytes, TimeoutSeconds: artifact.TimeoutSeconds,
			})
		}
	}
	return seeds
}

// runtimeSeedBackend is the part of the Docker client seeding uses, so the
// sequence is tested without a daemon.
type runtimeSeedBackend interface {
	Create(context.Context, dockerx.ContainerSpec, chan<- dockerx.PullProgress) (*dockerx.CreateResult, error)
	RemoveContainer(context.Context, string, bool, bool) error
	PathOwnership(context.Context, string, string) (dockerx.PathOwner, error)
	WriteContainerTree(context.Context, string, string, string, []byte, int64, int, int) error
	DigestContainerFile(context.Context, string, string, int64) (string, int64, error)
	RunToCompletion(context.Context, dockerx.ContainerSpec, chan<- dockerx.LogLine) (int, bool, error)
}

var ErrSeedRefused = errors.New("a release seed could not be written")

// materializeSeeds writes a release's seeds, volume by volume, before its
// container exists. For each volume a helper container is created from the
// release's own image with that one volume mounted where the release mounts
// it and no network. It is never started: creating it is what initializes a
// new volume from the image, as the release's own creation would, so the
// volume's root keeps the owner the image gave it, and the Engine's archive
// API writes through it. An artifact is downloaded by a second helper that
// runs the pinned fetch image as the volume's owner, again with only that
// volume, into a partial name; the dashboard reads the bytes back and
// compares their digest, and only a match is moved into place.
func materializeSeeds(
	ctx context.Context,
	backend runtimeSeedBackend,
	request CandidateRuntimeRequest,
	image string,
	emit func(BuildLog) error,
) error {
	plan := request.Snapshot.Plan
	if len(plan.Seeds) == 0 {
		return nil
	}
	say := func(text string) {
		if emit != nil {
			_ = emit(BuildLog{Stream: "status", Text: text})
		}
	}
	mounts := map[string]RuntimeMount{}
	for _, mount := range plan.Mounts {
		mounts[filepath.Clean(mount.Target)] = mount
	}
	order := []string{}
	byMount := map[string][]RuntimeSeed{}
	for _, seed := range plan.Seeds {
		target := filepath.Clean(seed.Mount)
		if _, found := byMount[target]; !found {
			order = append(order, target)
		}
		byMount[target] = append(byMount[target], seed)
	}
	for index, target := range order {
		mount, found := mounts[target]
		if !found || mount.Ownership != OwnershipManaged || filepath.IsAbs(mount.Source) {
			return fmt.Errorf("%w: %s is not a managed volume of this release", ErrSeedRefused, target)
		}
		name := fmt.Sprintf("jd-e%d-r%d-seed%d", request.Release.EnvironmentID, request.Release.Number, index)
		if err := seedVolume(ctx, backend, request, image, name, mount, byMount[target], say); err != nil {
			return err
		}
	}
	return nil
}

// seedHelperLabels name a helper's release without the managed label, so a
// helper that exists for the seconds a seed takes is never read as one of
// the release's own containers.
func seedHelperLabels(request CandidateRuntimeRequest) []dockerx.LabelSpec {
	return []dockerx.LabelSpec{
		{Name: "io.just-dashboard.seed", Value: "true"},
		{Name: "io.just-dashboard.environment-id", Value: strconv.FormatInt(request.Release.EnvironmentID, 10)},
		{Name: "io.just-dashboard.release-number", Value: strconv.FormatInt(request.Release.Number, 10)},
	}
}

func seedVolume(
	ctx context.Context,
	backend runtimeSeedBackend,
	request CandidateRuntimeRequest,
	image, name string,
	mount RuntimeMount,
	seeds []RuntimeSeed,
	say func(string),
) error {
	// A helper left behind by a run that died part way has this name; it
	// was never started and holds nothing but the mount.
	_ = backend.RemoveContainer(ctx, name, true, true)
	helper, err := backend.Create(ctx, dockerx.ContainerSpec{
		Name: name, Image: image, NetworkMode: "none", Pull: "missing",
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: mount.Source, Target: mount.Target}},
		Labels: seedHelperLabels(request),
	}, nil)
	if err != nil {
		return fmt.Errorf("%w: the helper for %s could not be created: %v", ErrSeedRefused, mount.Source, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = backend.RemoveContainer(cleanup, helper.ID, true, true)
	}()
	root, err := backend.PathOwnership(ctx, helper.ID, mount.Target)
	if err != nil || !root.Exists || !root.Dir {
		return fmt.Errorf("%w: the volume %s could not be read", ErrSeedRefused, mount.Source)
	}
	for _, seed := range seeds {
		destination := path.Join(mount.Target, seed.Path)
		existing, err := backend.PathOwnership(ctx, helper.ID, destination)
		if err != nil {
			return fmt.Errorf("%w: %s could not be read: %v", ErrSeedRefused, destination, err)
		}
		if existing.Exists {
			say(fmt.Sprintf("Kept %s: it is already in the volume", destination))
			continue
		}
		base, relative, owner, err := nearestDirectory(ctx, backend, helper.ID, mount.Target, seed.Path, root)
		if err != nil {
			return err
		}
		if seed.URL != "" {
			if err := fetchSeed(ctx, backend, request, mount, seed, owner, helper.ID); err != nil {
				return err
			}
			say(fmt.Sprintf("Fetched %s into %s and verified %s", seed.URL, destination, seed.Checksum))
			continue
		}
		content, err := renderSeed(seed, request.RuntimeVariables)
		if err != nil {
			return err
		}
		if err := backend.WriteContainerTree(ctx, helper.ID, base, relative, []byte(content), 0o644, owner.UID, owner.GID); err != nil {
			return fmt.Errorf("%w: %s could not be written: %v", ErrSeedRefused, destination, err)
		}
		say(fmt.Sprintf("Wrote %s before the first start", destination))
	}
	return nil
}

// nearestDirectory finds the deepest directory of a seed's path that already
// exists, so the write creates only what is missing, owned as that
// directory is.
func nearestDirectory(
	ctx context.Context,
	backend runtimeSeedBackend,
	helper, mountTarget, relative string,
	root dockerx.PathOwner,
) (string, string, dockerx.PathOwner, error) {
	parts := strings.Split(relative, "/")
	for depth := len(parts) - 1; depth > 0; depth-- {
		candidate := path.Join(mountTarget, strings.Join(parts[:depth], "/"))
		owner, err := backend.PathOwnership(ctx, helper, candidate)
		if err != nil {
			return "", "", dockerx.PathOwner{}, fmt.Errorf("%w: %s could not be read: %v", ErrSeedRefused, candidate, err)
		}
		if owner.Exists && !owner.Dir {
			return "", "", dockerx.PathOwner{}, fmt.Errorf("%w: %s is a file, so nothing can be written beneath it", ErrSeedRefused, candidate)
		}
		if owner.Exists {
			return candidate, strings.Join(parts[depth:], "/"), owner, nil
		}
	}
	return mountTarget, relative, root, nil
}

// renderSeed fills a file's references from the release's own variables. A
// reference to a variable the release does not have is refused rather than
// written as an empty value: a settings file with a blank console password
// is a server the console cannot reach.
func renderSeed(seed RuntimeSeed, variables map[string]string) (string, error) {
	var failure error
	content := seedReferenceRE.ReplaceAllStringFunc(seed.Content, func(match string) string {
		name := seedReferenceRE.FindStringSubmatch(match)[1]
		value, found := variables[name]
		if !found || value == "" {
			failure = fmt.Errorf("%w: %s needs %s, which this release does not have", ErrSeedRefused, seed.Path, name)
			return ""
		}
		if seed.Format == "properties" {
			return blueprint.EscapeProperty(value)
		}
		return value
	})
	return content, failure
}

// fetchSeed downloads one artifact and keeps it only if its digest matches.
// curl refuses anything but https, follows a bounded number of redirects and
// stops past the size limit; the bytes land under a partial name the release
// never reads, and are moved to their name only once the dashboard has
// hashed them.
func fetchSeed(
	ctx context.Context,
	backend runtimeSeedBackend,
	request CandidateRuntimeRequest,
	mount RuntimeMount,
	seed RuntimeSeed,
	owner dockerx.PathOwner,
	helper string,
) error {
	const fetchRoot = "/jd-seed"
	partial := path.Join(fetchRoot, seed.Path) + ".jd-partial"
	final := path.Join(fetchRoot, seed.Path)
	timeout := defaultSeedFetchLimit
	if seed.TimeoutSeconds > 0 {
		timeout = time.Duration(seed.TimeoutSeconds) * time.Second
	}
	run := func(entrypoint string, arguments ...string) (int, error) {
		runCtx, cancel := context.WithTimeout(ctx, timeout+time.Minute)
		defer cancel()
		code, _, err := backend.RunToCompletion(runCtx, dockerx.ContainerSpec{
			Image: SeedFetchImage, Entrypoint: []string{entrypoint}, Command: arguments,
			User:   strconv.Itoa(owner.UID) + ":" + strconv.Itoa(owner.GID),
			Mounts: []dockerx.MountSpec{{Type: "volume", Source: mount.Source, Target: fetchRoot}},
			Labels: seedHelperLabels(request),
		}, nil)
		return code, err
	}
	discard := func() { _, _ = run("rm", "-f", partial) }
	code, err := run("curl",
		"--proto", "=https", "--proto-redir", "=https", "--fail", "--location", "--max-redirs", "5",
		"--max-filesize", strconv.FormatInt(seed.MaxBytes, 10), "--max-time", strconv.Itoa(int(timeout.Seconds())),
		"--silent", "--show-error", "--create-dirs", "--output", partial, seed.URL)
	if err != nil || code != 0 {
		discard()
		return fmt.Errorf("%w: %s could not be downloaded (exit %d)", ErrSeedRefused, seed.URL, code)
	}
	digest, size, err := backend.DigestContainerFile(ctx, helper, path.Join(mount.Target, seed.Path)+".jd-partial", seed.MaxBytes)
	if err != nil {
		discard()
		if errors.Is(err, dockerx.ErrContainerFileLimit) {
			return fmt.Errorf("%w: %s is larger than its %d-byte limit", ErrSeedRefused, seed.URL, seed.MaxBytes)
		}
		return fmt.Errorf("%w: the download of %s could not be read back: %v", ErrSeedRefused, seed.URL, err)
	}
	if digest != seed.Checksum {
		discard()
		return fmt.Errorf("%w: %s has digest %s (%d bytes), but the definition pins %s; it was removed and nothing was started",
			ErrSeedRefused, seed.URL, digest, size, seed.Checksum)
	}
	if code, err := run("mv", "-f", partial, final); err != nil || code != 0 {
		discard()
		return fmt.Errorf("%w: the verified %s could not be moved into place", ErrSeedRefused, seed.Path)
	}
	return nil
}
