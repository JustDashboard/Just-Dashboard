package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"gopkg.in/yaml.v3"
)

func (r *dockerRecovery) readOriginalCompose(ctx context.Context, candidate WorkloadCandidate, reader DockerWorkloadRecoveryReader) {
	var first *dockerx.AdoptionContainer
	for _, name := range sortedRecoveryServices(r.containers) {
		if len(r.containers[name]) > 0 {
			first = r.containers[name][0]
			break
		}
	}
	labels := first.Inspection.Config.Labels
	directory := labels["com.docker.compose.project.working_dir"]
	if directory == "" {
		directory = candidate.SourcePath
		if info, err := os.Stat(directory); err == nil && info.Mode().IsRegular() {
			directory = filepath.Dir(directory)
		}
	}
	resolvedDirectory, err := r.paths.Resolve(directory)
	if err != nil || !filepath.IsAbs(directory) {
		r.issue("source_path_unavailable", "The original Compose working directory is unavailable or outside the permitted file roots.", "", "source", true)
		return
	}
	files := splitOriginalPaths(labels["com.docker.compose.project.config_files"], resolvedDirectory)
	if len(files) == 0 {
		r.issue("compose_files_missing", "The original ordered Compose files are not recorded on the running containers.", "", "source", true)
		return
	}
	envFiles := splitOriginalPaths(labels["com.docker.compose.project.environment_file"], resolvedDirectory)
	if len(envFiles) == 0 {
		defaultEnv := filepath.Join(resolvedDirectory, ".env")
		if _, statErr := os.Lstat(defaultEnv); statErr == nil {
			envFiles = []string{defaultEnv}
		}
	}
	for _, path := range envFiles {
		if _, err := r.containedOriginalFile(path); err != nil {
			r.issue("environment_file_unavailable", "An original Compose interpolation file is missing, unreadable or outside the permitted file roots.", "", "environment", true)
			return
		}
	}
	for i, path := range files {
		resolved, err := r.containedOriginalFile(path)
		if err != nil || r.checkOriginalReferences(resolved, resolvedDirectory) != nil {
			r.issue("compose_source_not_contained", "An original Compose file or referenced environment, configuration or build path is unavailable, dynamic or outside the permitted file roots.", "", "source", true)
			return
		}
		files[i] = resolved
	}
	raw, err := reader.ReadComposeAdoptionConfiguration(ctx, candidate.ResourceID, resolvedDirectory, files, envFiles)
	if err != nil || json.Unmarshal(raw, &r.model) != nil || object(r.model["services"]) == nil {
		r.issue("compose_resolution_failed", "Compose could not resolve the original ordered files. Restore the original files and interpolation inputs before adoption.", "", "source", true)
		return
	}
	r.result.Adoption.OriginalSourcePath, r.result.Adoption.ConfigFiles = resolvedDirectory, files
	if containsSourceInterpolation(r.model) {
		r.issue("compose_interpolation_unresolved", "The original Compose configuration still contains unresolved interpolation inputs.", "", "source", true)
	}
	for _, key := range []string{"configs", "secrets"} {
		if len(object(r.model[key])) > 0 {
			r.issue("compose_file_resources", "File-based Compose configs and secrets need an explicitly reviewed immutable source snapshot before adoption.", "", key, true)
		}
	}
}

func (r *dockerRecovery) containedOriginalFile(path string) (string, error) {
	resolved, err := r.paths.Resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return "", fmt.Errorf("original file is unavailable")
	}
	return resolved, nil
}

func (r *dockerRecovery) checkOriginalReferences(path, directory string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return fmt.Errorf("original file is unavailable")
	}
	file, err := openRegularSnapshotFile(path, info)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return fmt.Errorf("original file exceeds its bound")
	}
	var root yaml.Node
	if yaml.Unmarshal(raw, &root) != nil || countYAMLNodes(&root, 0) > 100_000 {
		return fmt.Errorf("original Compose is invalid")
	}
	// Compose follows aliases and merge keys. Inspect the same effective
	// mappings so an inherited file reference cannot bypass containment.
	var expanded map[string]any
	if root.Decode(&expanded) != nil || root.Encode(expanded) != nil || countYAMLNodes(&root, 0) > 100_000 {
		return fmt.Errorf("original Compose mappings could not be resolved safely")
	}
	mapping := documentMapping(&root)
	if mapping == nil {
		return fmt.Errorf("original Compose is invalid")
	}
	// Parsing these can make Compose read another project's configuration
	// before containment has been established. Refuse rather than let its
	// parser become a second filesystem boundary.
	if mappingValue(mapping, "include") != nil {
		return fmt.Errorf("includes require a source snapshot")
	}
	services := mappingValue(mapping, "services")
	if services != nil && services.Kind == yaml.MappingNode {
		for index := 1; index < len(services.Content); index += 2 {
			service := services.Content[index]
			if mappingValue(service, "extends") != nil {
				return fmt.Errorf("extends requires a source snapshot")
			}
			if err := r.checkOriginalEnvironmentFiles(directory, mappingValue(service, "env_file")); err != nil {
				return err
			}
			if build := mappingValue(service, "build"); build != nil {
				context := build.Value
				if build.Kind == yaml.MappingNode {
					context = scalarMappingValue(build, "context")
				}
				if context == "" {
					context = "."
				}
				if _, err := r.resolveOriginalReference(directory, context, false); err != nil {
					return err
				}
			}
		}
	}
	for _, key := range []string{"configs", "secrets"} {
		entries := mappingValue(mapping, key)
		if entries != nil && entries.Kind == yaml.MappingNode {
			for index := 1; index < len(entries.Content); index += 2 {
				if ref := scalarMappingValue(entries.Content[index], "file"); ref != "" {
					if _, err := r.resolveOriginalReference(directory, ref, true); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (r *dockerRecovery) checkOriginalEnvironmentFiles(directory string, node *yaml.Node) error {
	if node == nil || node.Tag == "!!null" {
		return nil
	}
	items := []*yaml.Node{node}
	if node.Kind == yaml.SequenceNode {
		items = node.Content
	}
	// Do not reuse composeEnvFiles: its display inventory stops at sixteen,
	// while containment must check every reference Compose will read.
	for _, item := range items {
		var path string
		required := true
		switch item.Kind {
		case yaml.ScalarNode:
			if item.Tag == "!!str" {
				path = item.Value
			}
		case yaml.MappingNode:
			if value := mappingValue(item, "path"); value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" {
				path = value.Value
			}
			if value := mappingValue(item, "required"); value != nil {
				if value.Tag != "!!bool" || value.Decode(&required) != nil {
					return fmt.Errorf("original environment file requirement is invalid")
				}
			}
		}
		if path == "" {
			return fmt.Errorf("original environment file path is invalid")
		}
		resolved, err := r.resolveOriginalReference(directory, path, false)
		if err != nil {
			return err
		}
		info, err := os.Stat(resolved)
		if !required && os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return fmt.Errorf("original environment file is unavailable")
		}
	}
	return nil
}

func (r *dockerRecovery) resolveOriginalReference(directory, value string, regular bool) (string, error) {
	if strings.ContainsAny(value, "$\x00\r\n") || strings.Contains(value, "://") {
		return "", fmt.Errorf("original reference is dynamic")
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(directory, value)
	}
	if regular {
		return r.containedOriginalFile(value)
	}
	return r.paths.Resolve(value)
}

func splitOriginalPaths(value, directory string) []string {
	var paths []string
	for _, path := range strings.Split(value, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		paths = append(paths, path)
	}
	return paths
}

func (r *dockerRecovery) externalizeResources() {
	for _, key := range []string{"volumes", "networks"} {
		entries := object(r.model[key])
		for name, raw := range entries {
			entry := object(raw)
			actual, _ := entry["name"].(string)
			if actual == "" {
				r.issue("resource_identity_unresolved", "The original persistent volume or network name could not be resolved.", "", key+"."+name, true)
				continue
			}
			entries[name] = map[string]any{"name": actual, "external": true}
		}
	}
}
