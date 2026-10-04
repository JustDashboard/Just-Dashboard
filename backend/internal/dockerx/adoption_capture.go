package dockerx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/docker/docker/api/types/container"
)

// AdoptionContainer is a private, read-only capture. Config and HostConfig
// contain credentials and must never be sent directly to a browser or audit log.
type AdoptionContainer struct {
	Inspection  container.InspectResponse    `json:"-"`
	Image       *ImageDetail                 `json:"-"`
	Changes     []container.FilesystemChange `json:"-"`
	ChangeModes map[string]os.FileMode       `json:"-"`
}

func (c *Client) CaptureAdoptionContainer(ctx context.Context, id string) (*AdoptionContainer, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	inspection, err := cli.ContainerInspect(ctx, id)
	if err != nil || inspection.ID == "" || inspection.Config == nil || inspection.HostConfig == nil {
		return nil, errors.New("the container configuration could not be captured")
	}
	image, err := c.InspectImage(ctx, inspection.Image)
	if err != nil {
		return nil, errors.New("the original container image is unavailable locally")
	}
	changes, err := cli.ContainerDiff(ctx, inspection.ID)
	if err != nil {
		return nil, errors.New("the container writable layer could not be checked")
	}
	modes := map[string]os.FileMode{}
	for _, change := range changes {
		if change.Kind == container.ChangeDelete {
			continue
		}
		stat, err := cli.ContainerStatPath(ctx, inspection.ID, change.Path)
		if err == nil {
			modes[change.Path] = stat.Mode
		}
	}
	return &AdoptionContainer{Inspection: inspection, Image: image, Changes: changes, ChangeModes: modes}, nil
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
