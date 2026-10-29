package imagescan

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// DockerDaemon is the scanner's Docker on the real daemon.
type DockerDaemon struct{ Client *dockerx.Client }

func (d DockerDaemon) EnsureVolume(ctx context.Context, name string, labels map[string]string) error {
	exists, err := d.Client.VolumeExists(ctx, name)
	if err != nil || exists {
		return err
	}
	_, err = d.Client.CreateVolume(ctx, dockerx.VolumeSpec{Name: name, Labels: labels})
	return err
}

func (d DockerDaemon) ImagePresent(ctx context.Context, ref string) bool {
	_, err := d.Client.InspectImage(ctx, ref)
	return err == nil
}

func (d DockerDaemon) PullImage(ctx context.Context, ref string) error {
	_, err := d.Client.PullImmutable(ctx, ref, "", nil)
	return err
}

func (d DockerDaemon) RemoveImage(ctx context.Context, ref string) error {
	_, err := d.Client.RemoveImage(ctx, ref, false, false)
	return err
}

func (d DockerDaemon) SaveImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	return d.Client.SaveImage(ctx, ref)
}

// CopyIntoVolume makes a container of image that never starts, with the
// volume at /cache and no network, copies the archive in through it and
// removes it.
func (d DockerDaemon) CopyIntoVolume(ctx context.Context, volume, image, dir string, archive io.Reader) error {
	created, err := d.Client.Create(ctx, dockerx.ContainerSpec{
		Name: "jd-image-scan-copy-" + randomSuffix(), Image: image, NetworkMode: "none",
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: volume, Target: "/cache"}},
		Labels: []dockerx.LabelSpec{{Name: RoleLabel, Value: "copy"}},
		Start:  false,
	}, nil)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = d.Client.RemoveContainer(cleanup, created.ID, true, false)
	}()
	return d.Client.CopyArchive(ctx, created.ID, dir, archive)
}

// Run runs the container to completion, keeping the last forty lines of
// standard error for the reason a failed scan gives.
func (d DockerDaemon) Run(ctx context.Context, spec dockerx.ContainerSpec) (int, []string, error) {
	lines := make(chan dockerx.LogLine, 64)
	var mu sync.Mutex
	var stderr []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for line := range lines {
			if line.Stream != "stderr" {
				continue
			}
			mu.Lock()
			stderr = append(stderr, line.Text)
			if len(stderr) > 40 {
				stderr = stderr[len(stderr)-40:]
			}
			mu.Unlock()
		}
	}()
	code, removed, err := d.Client.RunToCompletion(ctx, spec, lines)
	close(lines)
	<-done
	if err == nil && !removed {
		err = errors.New("the scanner's container could not be removed")
	}
	mu.Lock()
	defer mu.Unlock()
	return code, append([]string(nil), stderr...), err
}

// Sweep removes the scanner's containers a stopped process left behind. It
// runs once, before the first scan of this process.
func (d DockerDaemon) Sweep(ctx context.Context) error {
	var failed error
	for _, role := range []string{"scan", "copy"} {
		containers, err := d.Client.ListContainersWithLabels(ctx, map[string]string{RoleLabel: role})
		if err != nil {
			return err
		}
		for _, item := range containers {
			if err := d.Client.RemoveContainer(ctx, item.ID, true, false); err != nil {
				failed = err
			}
		}
	}
	return failed
}
