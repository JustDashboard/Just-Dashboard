package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ComposePersistentSources uses the same parser, project identity, files and
// frozen interpolation environment as activation. Parsing each source file
// separately misses merged mounts and external volume names.
func (c *Client) ComposePersistentSources(ctx context.Context, spec ComposeReleaseSpec) ([]string, error) {
	raw, err := c.composeReleaseConfiguration(ctx, spec)
	if err != nil {
		return nil, err
	}
	return composePersistentSources(raw)
}

func (c *Client) composeReleaseConfiguration(ctx context.Context, spec ComposeReleaseSpec) ([]byte, error) {
	if !validComposeProjectName(spec.ProjectName) || !dirExists(spec.ProjectDirectory) ||
		len(spec.Files) == 0 || len(spec.Files) > 16 {
		return nil, errors.New("invalid deployment Compose configuration invocation")
	}
	args := []string{"compose", "--project-name", spec.ProjectName, "--project-directory", spec.ProjectDirectory}
	for _, configured := range spec.Files {
		path := configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(spec.ProjectDirectory, path)
		}
		if !containedComposeFile(spec.ProjectDirectory, path) {
			return nil, errors.New("deployment Compose file escapes its workspace")
		}
		args = append(args, "-f", path)
	}
	if spec.OverrideFile != "" {
		if !containedComposeFile(spec.ProjectDirectory, spec.OverrideFile) {
			return nil, errors.New("deployment Compose override escapes its workspace")
		}
		args = append(args, "-f", spec.OverrideFile)
	}
	envFile, err := writeComposeReleaseEnv(spec.ProjectDirectory, spec.Environment)
	if err != nil {
		return nil, err
	}
	defer os.Remove(envFile)
	args = append(args, "--env-file", envFile, "config", "--format", "json")
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := hostexec.CommandInDir(ctx, spec.ProjectDirectory, "docker", args...)
	cmd.Env = composeReleaseProcessEnvironment(c.host, spec.ProjectDirectory)
	output := &boundedComposeBuffer{limit: 8 << 20}
	cmd.Stdout = output
	// Parser diagnostics and the full normalized document can contain secret
	// interpolation values. Only the selected storage fields may leave here.
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Compose could not resolve runtime configuration")
	}
	if output.Len() >= 8<<20 {
		return nil, errors.New("Compose runtime configuration exceeds the supported size")
	}
	return output.Bytes(), nil
}

func composePersistentSources(raw []byte) ([]string, error) {
	var config struct {
		Services map[string]struct {
			Volumes []struct {
				Type     string `json:"type"`
				Source   string `json:"source"`
				ReadOnly bool   `json:"read_only"`
			} `json:"volumes"`
		} `json:"services"`
		Volumes map[string]struct {
			Name string `json:"name"`
		} `json:"volumes"`
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Services) == 0 {
		return nil, errors.New("Compose returned no supported storage configuration")
	}
	seen := map[string]bool{}
	for name, service := range config.Services {
		for _, mount := range service.Volumes {
			if mount.ReadOnly || mount.Type == "tmpfs" {
				continue
			}
			source := mount.Source
			switch mount.Type {
			case "bind":
				if !filepath.IsAbs(source) {
					return nil, fmt.Errorf("Compose service %s has an unresolved bind mount", name)
				}
			case "volume":
				volume, ok := config.Volumes[source]
				if !ok || volume.Name == "" || strings.ContainsAny(volume.Name, "/\\:$\x00") {
					return nil, fmt.Errorf("Compose service %s needs an explicit named volume for backup coverage", name)
				}
				source = volume.Name
			default:
				return nil, fmt.Errorf("Compose service %s uses storage without a backup adapter", name)
			}
			seen[source] = true
		}
	}
	sources := make([]string, 0, len(seen))
	for source := range seen {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	return sources, nil
}
