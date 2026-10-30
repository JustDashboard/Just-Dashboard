package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A job's source can be a Docker volume's directory — the coverage report
// suggests exactly that for every local volume. On the host that took the
// backup the volume is there; on a fresh one it is not, and a directory made
// where its data would live is not a volume: Docker does not know it, and its
// own create later refuses the path that is already there. So the manifest
// says which sources are volumes, and a restore in place asks Docker for the
// volume — making it, with the labels it had, when it does not exist — and
// writes into wherever that host keeps its data.

// SourceVolume is ManifestSource.Kind for a Docker volume's directory.
const SourceVolume = "volume"

// defaultDockerVolumes is where the local driver keeps volumes under the
// default Docker root: <root>/volumes/<name>/_data. A manifest written before
// sources carried their kind is read by this layout and no other, so a path
// that merely looks like it somewhere else is never taken for a volume.
const defaultDockerVolumes = "/var/lib/docker/volumes"

// Docker's own rule for a volume name, which dockerx.CreateVolume applies too.
var volumeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

func validVolumeName(name string) bool { return volumeNameRe.MatchString(name) }

func volumeFromMountpoint(path string) (string, bool) {
	rest, ok := strings.CutPrefix(filepath.Clean(path), defaultDockerVolumes+"/")
	if !ok {
		return "", false
	}
	name, tail, ok := strings.Cut(rest, "/")
	if !ok || tail != "_data" || !validVolumeName(name) {
		return "", false
	}
	return name, true
}

// LocalVolume is a local-driver volume as the Docker owner reports it.
type LocalVolume struct {
	Name       string
	Mountpoint string
	Labels     map[string]string
}

// VolumeProvisioner is the Docker owner's side of the volumes a job archives:
// which volume a source directory is when the archive is taken, and on a
// restore where a volume's data lives on this host, made first when absent.
// Backups decides when; Docker decides how.
type VolumeProvisioner interface {
	// VolumeAt answers the local volume whose data directory path is, if
	// it is one.
	VolumeAt(ctx context.Context, path string) (LocalVolume, bool, error)
	// VolumeMountpoint answers where a volume keeps its data and whether it
	// exists; it never creates one.
	VolumeMountpoint(ctx context.Context, name string) (string, bool, error)
	// EnsureVolume answers a volume's mountpoint, creating it with these
	// labels when it does not exist, and says whether it did.
	EnsureVolume(ctx context.Context, name string, labels map[string]string) (string, bool, error)
}

func (r *Runner) WithVolumeProvisioner(volumes VolumeProvisioner) *Runner {
	r.volumes = volumes
	return r
}

// annotateVolumes marks the sources that are a local volume's directory. With
// no Docker owner, or one that cannot answer, the default layout is the
// reading; the run never fails over it, because the archive is the same
// either way and only a later restore elsewhere reads the mark.
func (r *Runner) annotateVolumes(ctx context.Context, manifest *Manifest, logBuf io.Writer) {
	asked := r.volumes != nil
	for i := range manifest.Sources {
		source := &manifest.Sources[i]
		if asked {
			volume, ok, err := r.volumes.VolumeAt(ctx, source.Path)
			if err == nil {
				if ok && validVolumeName(volume.Name) {
					source.Kind, source.Volume, source.VolumeLabels = SourceVolume, volume.Name, boundedLabels(volume.Labels)
				}
				continue
			}
			fmt.Fprintf(logBuf, "could not ask Docker which sources are volumes (%v); reading them by their path\n", err)
			asked = false
		}
		if name, ok := volumeFromMountpoint(source.Path); ok {
			source.Kind, source.Volume = SourceVolume, name
		}
	}
}

// boundedLabels keeps what a manifest carries of a volume's labels small: a
// volume labelled by something other than Compose is not worth a large
// manifest, and Compose's own are a handful of short ones.
func boundedLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range labels {
		if len(out) == 32 {
			break
		}
		if key == "" || len(key) > 128 || len(value) > 512 {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RestoreTarget is where one recorded source goes in a restore in place on
// this host, and what the restore has to make for it.
type RestoreTarget struct {
	ArchivePath string `json:"archivePath"`
	// Path is where the source was taken from; Target where it is written
	// here, which differs for a volume whose host keeps volumes elsewhere.
	Path   string `json:"path"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Volume string `json:"volume,omitempty"`
	// Exists says whether something is there now, which the restore then
	// overwrites; Creates names what it makes first: a Docker volume, or a
	// directory with whatever parents it lacks.
	Exists  bool   `json:"exists"`
	Creates string `json:"creates,omitempty"`
	// Refused is why this source cannot be put back here, when it cannot.
	Refused string `json:"refused,omitempty"`
}

// errDockerForVolume is a volume a restore would have to create with no
// Docker owner to ask.
var errDockerForVolume = errors.New("Docker is not reachable from the dashboard to create it")

// restoreTargets answers, for each recorded source the restore touches, where
// it goes here. With ensure, a missing volume is created; without, the answer
// is only a plan. Every target is resolved through the file service, so a
// root restriction on this host holds whatever the manifest says.
func (r *Runner) restoreTargets(ctx context.Context, manifest *Manifest, filter *pathFilter, ensure bool) ([]RestoreTarget, error) {
	var out []RestoreTarget
	for _, source := range manifest.Sources {
		if !filter.touches(source.ArchivePath) {
			continue
		}
		target := RestoreTarget{ArchivePath: source.ArchivePath, Path: source.Path, Target: source.Path, Kind: "path"}
		if name, ok := source.SourceVolume(); ok {
			target.Kind, target.Volume = SourceVolume, name
			switch {
			case r.volumes == nil:
				// Without Docker the recorded directory is all there is; a
				// volume that is not here cannot be made as a bare directory.
				if _, err := os.Lstat(source.Path); err != nil {
					target.Refused = fmt.Sprintf("volume %s is not on this host and %v", name, errDockerForVolume)
				}
			case ensure:
				mountpoint, created, err := r.volumes.EnsureVolume(ctx, name, source.VolumeLabels)
				if err != nil {
					return nil, fmt.Errorf("could not create volume %s: %w", name, err)
				}
				target.Target = mountpoint
				if created {
					target.Creates = SourceVolume
				}
			default:
				mountpoint, exists, err := r.volumes.VolumeMountpoint(ctx, name)
				if err != nil {
					return nil, fmt.Errorf("could not read volume %s: %w", name, err)
				}
				if exists {
					target.Target = mountpoint
				} else {
					target.Creates = SourceVolume
				}
			}
		}
		// A volume only planned has no mountpoint until Docker makes it;
		// everything else is checked against this host's roots now.
		planned := target.Kind == SourceVolume && target.Creates == SourceVolume && !ensure
		if target.Refused == "" && !planned {
			resolved, err := r.store.paths.Resolve(target.Target)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", source.Path, err)
			}
			if resolved == "/" {
				return nil, fmt.Errorf("refusing to restore directly over /")
			}
			target.Target = resolved
			if _, err := os.Lstat(resolved); err == nil {
				target.Exists = target.Creates == ""
			} else if target.Kind == "path" {
				target.Creates = "directory"
			}
		}
		out = append(out, target)
	}
	return out, nil
}

// PlanRestoreInPlace says where a restore in place would write on this host
// and what it would make, from the manifest alone: nothing is downloaded,
// created or written. The restore dialog shows it before the confirmation.
func (r *Runner) PlanRestoreInPlace(ctx context.Context, runID int64, opts ...RestoreOptions) ([]RestoreTarget, error) {
	run, err := r.store.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != StatusSuccess || run.Artifact == "" {
		return nil, fmt.Errorf("run %d has no successful artifact", runID)
	}
	if run.Manifest == nil || !run.Manifest.Complete {
		return nil, errors.New("this archive has no manifest, so its files cannot be put back where they came from; restore it into a directory instead")
	}
	targets, err := r.restoreTargets(ctx, run.Manifest, newPathFilter(opts), false)
	if targets == nil && err == nil {
		targets = []RestoreTarget{}
	}
	return targets, err
}
