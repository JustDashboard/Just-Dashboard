package dockerx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
)

// AdoptionContainer is a private, read-only capture. Config and HostConfig
// contain credentials and must never be sent directly to a browser or audit log.
type AdoptionContainer struct {
	Inspection              container.InspectResponse    `json:"-"`
	Image                   *ImageDetail                 `json:"-"`
	Changes                 []container.FilesystemChange `json:"-"`
	ChangeModes             map[string]os.FileMode       `json:"-"`
	MissingImage            bool                         `json:"-"`
	RegenerablePaths        []string                     `json:"-"`
	VerifiedPythonCaches    []string                     `json:"-"`
	RegenerableProofFailure string                       `json:"-"`
	UnrepresentedOptions    []string                     `json:"-"`
}

// AdoptionCaptureError exposes a bounded phase name, never Docker response
// bodies or configuration values. Cancellation remains inspectable internally.
type AdoptionCaptureError struct {
	Stage string
	cause error
}

func (e *AdoptionCaptureError) Error() string {
	reason := "could not be verified"
	if errors.Is(e.cause, context.DeadlineExceeded) {
		reason = "exceeded the capture time limit"
	} else if errors.Is(e.cause, context.Canceled) {
		reason = "was canceled"
	}
	return "container " + strings.ReplaceAll(e.Stage, "_", " ") + " " + reason
}
func (e *AdoptionCaptureError) Unwrap() error { return e.cause }
func adoptionCaptureFailure(ctx context.Context, stage string, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return &AdoptionCaptureError{Stage: stage, cause: err}
}

func (c *Client) CaptureAdoptionContainer(ctx context.Context, id string) (*AdoptionContainer, error) {
	cli, err := c.api()
	if err != nil {
		return nil, adoptionCaptureFailure(ctx, "daemon", err)
	}
	inspection, raw, err := cli.ContainerInspectWithRaw(ctx, id, false)
	if err != nil || inspection.ID == "" || inspection.Config == nil || inspection.HostConfig == nil {
		return nil, adoptionCaptureFailure(ctx, "inspection", err)
	}
	unknownOptions, err := adoptionUnrepresentedConfiguration(raw)
	if err != nil {
		return nil, adoptionCaptureFailure(ctx, "configuration", err)
	}
	image, err := c.inspectImage(ctx, inspection.Image, false)
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, adoptionCaptureFailure(ctx, "image", err)
	}
	missingImage := err != nil
	changes, err := cli.ContainerDiff(ctx, inspection.ID)
	if err != nil {
		return nil, adoptionCaptureFailure(ctx, "writable_layer", err)
	}
	modes := map[string]os.FileMode{}
	for _, change := range changes {
		if change.Kind == container.ChangeDelete {
			continue
		}
		// One bounded archive proves n8n's generated assets and their modes.
		// Unknown generators remain blocked without thousands of stat calls.
		if n8nDefaultStart(inspection.Config) && (change.Path == n8nEditorCache || strings.HasPrefix(change.Path, n8nEditorCache+"/")) {
			continue
		}
		stat, err := cli.ContainerStatPath(ctx, inspection.ID, change.Path)
		if err == nil {
			modes[change.Path] = stat.Mode
		}
	}
	captured := &AdoptionContainer{Inspection: inspection, Image: image, Changes: changes, ChangeModes: modes, MissingImage: missingImage, UnrepresentedOptions: unknownOptions}
	c.captureRegenerableN8nCache(ctx, captured)
	c.captureRegenerablePythonCaches(ctx, captured)
	if ctx.Err() != nil {
		return nil, adoptionCaptureFailure(ctx, "generated_cache", ctx.Err())
	}
	return captured, nil
}

// ReadComposeAdoptionConfiguration resolves the original files without writing
// to the project or running up/build/pull. The caller first contains every file
// reference, including includes, env files, build contexts and configuration files.
func (c *Client) ReadComposeAdoptionConfiguration(ctx context.Context, project, directory string, files, envFiles []string) ([]byte, error) {
	if !validComposeProjectName(project) || !filepath.IsAbs(directory) || len(files) == 0 || len(files) > 16 || len(envFiles) > 16 {
		return nil, errors.New("the original Compose configuration is incomplete")
	}
	args := []string{"compose", "--project-name", project, "--project-directory", directory}
	for _, path := range files {
		if !filepath.IsAbs(path) {
			return nil, errors.New("the original Compose file is not an absolute path")
		}
		args = append(args, "-f", path)
	}
	for _, path := range envFiles {
		if !filepath.IsAbs(path) {
			return nil, errors.New("the original Compose environment file is not an absolute path")
		}
		args = append(args, "--env-file", path)
	}
	args = append(args, "config", "--format", "json")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := hostexec.CommandInDir(ctx, directory, "docker", args...)
	cmd.Env = composeReleaseProcessEnvironment(c.host, directory)
	output := &boundedComposeBuffer{limit: 8 << 20}
	cmd.Stdout = output
	if cmd.Run() != nil {
		return nil, errors.New("Compose could not resolve the original configuration; check its files and interpolation inputs")
	}
	if output.Len() >= 8<<20 {
		return nil, errors.New("the original Compose configuration exceeds the supported size")
	}
	return append([]byte(nil), output.Bytes()...), nil
}
