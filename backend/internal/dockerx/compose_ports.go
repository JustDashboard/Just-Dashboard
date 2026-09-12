package dockerx

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"gopkg.in/yaml.v3"
)

const composePortsHeader = "# Just Dashboard automatic published ports\n"

// composePortOverride contains only port bindings, never interpolated environment
// values or credentials from `compose config`. Source files remain untouched.
func composePortOverride(raw []byte) ([]byte, error) {
	var config struct {
		Services map[string]struct {
			NetworkMode string           `json:"network_mode"`
			Ports       []map[string]any `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, errors.New("could not read Compose port configuration")
	}
	source, _ := json.Marshal(config)
	fingerprint := fmt.Sprintf("# source-ports: %x\n", sha256.Sum256(source))
	services := map[string]any{}
	names := make([]string, 0, len(config.Services))
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	reserved := map[int]bool{}
	for _, name := range names {
		service := config.Services[name]
		if service.NetworkMode == "host" || len(service.Ports) == 0 {
			continue
		}
		changed := false
		groups := map[string][]map[string]any{}
		for _, port := range service.Ports {
			if published, ok := port["published"]; ok && fmt.Sprint(published) != "0" && fmt.Sprint(published) != "" {
				groups[fmt.Sprint(published)] = append(groups[fmt.Sprint(published)], port)
			}
		}
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			preferred, err := strconv.Atoi(key)
			// A published range is a preference for allocation too. The actual
			// chosen binding is always one stable number.
			if err != nil {
				first, _, _ := strings.Cut(key, "-")
				preferred, _ = strconv.Atoi(first)
			}
			preferred = max(1024, preferred)
			selected, err := portalloc.Select(preferred, 1024, reserved, func(candidate int) error {
				for _, port := range groups[key] {
					address, _ := port["host_ip"].(string)
					protocol, _ := port["protocol"].(string)
					if err := portalloc.Available(address, protocol, candidate); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			reserved[selected] = true
			for _, port := range groups[key] {
				port["published"] = strconv.Itoa(selected)
			}
			changed = true
		}
		if changed {
			var ports yaml.Node
			if err := ports.Encode(service.Ports); err != nil {
				return nil, err
			}
			ports.Tag = "!override"
			services[name] = map[string]any{"ports": &ports}
		}
	}
	if len(services) == 0 {
		return nil, errors.New("the conflicting Compose ports cannot be remapped")
	}
	raw, err := yaml.Marshal(map[string]any{"services": services})
	if err != nil {
		return nil, err
	}
	return append([]byte(composePortsHeader+fingerprint), raw...), nil
}

// runComposePorts retries only failed starts caused by host port allocation.
// The generated override is retained so subsequent stops, starts and recovery
// refer to the same configuration. Docker's inspection remains the address source.
func runComposePorts(ctx context.Context, dir string, base, action, environment []string, override string, emit func(LogLine) error) (int, error) {
	args := append([]string{}, base...)
	if stat, err := os.Lstat(override); err == nil {
		if !stat.Mode().IsRegular() {
			return -1, errors.New("automatic Compose port file is not a regular file")
		}
		raw, err := os.ReadFile(override)
		if err != nil {
			return -1, err
		}
		if !strings.HasPrefix(string(raw), composePortsHeader) {
			return -1, errors.New("automatic Compose port filename is occupied by an operator file")
		}
		// An edited source port list invalidates previous automatic choices.
		command := hostexec.CommandInDir(ctx, dir, "docker", append(append([]string{}, base...), "config", "--format", "json")...)
		command.Env = environment
		output := &boundedComposeBuffer{limit: 8 << 20}
		command.Stdout = output
		if err := command.Run(); err != nil {
			return -1, fmt.Errorf("read current Compose ports: %w", err)
		}
		current, err := composePortOverride(output.Bytes())
		oldLines, newLines := strings.SplitN(string(raw), "\n", 3), strings.SplitN(string(current), "\n", 3)
		if err != nil || len(oldLines) < 2 || len(newLines) < 2 || oldLines[1] != newLines[1] {
			if err := os.Remove(override); err != nil {
				return -1, err
			}
		} else {
			args = append(args, "-f", override)
		}
	} else if !os.IsNotExist(err) {
		return -1, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		command := hostexec.CommandInDir(ctx, dir, "docker", append(append([]string{}, args...), action...)...)
		command.Env = environment
		lines := make(chan LogLine, 64)
		done := make(chan struct{})
		conflict := false
		var emitErr error
		go func() {
			defer close(done)
			for line := range lines {
				if portalloc.IsConflict(errors.New(line.Text)) {
					conflict = true
				}
				if emit != nil && emitErr == nil {
					emitErr = emit(line)
				}
			}
		}()
		code, err := streamCommand(ctx, command, lines)
		close(lines)
		<-done
		if emitErr != nil {
			return code, emitErr
		}
		if code == 0 && err == nil {
			return 0, nil
		}
		if attempt >= 2 || !conflict || len(action) == 0 || (action[0] != "up" && action[0] != "start" && action[0] != "restart") {
			return code, err
		}
		configCommand := hostexec.CommandInDir(ctx, dir, "docker", append(append([]string{}, base...), "config", "--format", "json")...)
		configCommand.Env = environment
		output := &boundedComposeBuffer{limit: 8 << 20}
		configCommand.Stdout = output
		if configErr := configCommand.Run(); configErr != nil {
			return code, fmt.Errorf("read Compose ports after bind conflict: %w", configErr)
		}
		raw, configErr := composePortOverride(output.Bytes())
		if configErr != nil {
			return code, configErr
		}
		temp, writeErr := os.CreateTemp(filepath.Dir(override), ".jd-port-*")
		if writeErr != nil {
			return code, writeErr
		}
		_, writeErr = temp.Write(raw)
		closeErr := temp.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr == nil {
			writeErr = os.Rename(temp.Name(), override)
		}
		os.Remove(temp.Name())
		if writeErr != nil {
			return code, writeErr
		}
		args = append(append([]string{}, base...), "-f", override)
		if action[0] != "up" {
			action = append([]string{"up", "-d", "--no-build"}, action[1:]...)
		}
		if emit != nil {
			if err := emit(LogLine{Stream: "status", Text: "A host port is occupied. Retrying with available, persistent ports; the existing port owner remains running."}); err != nil {
				return code, err
			}
		}
	}
	return -1, errors.New("Compose ports remained unavailable")
}

// Explicit base files let the generated override coexist with Compose's normal
// default-file discovery instead of replacing the operator's configuration.
func composePortBase(dir string, environment []string) ([]string, error) {
	base := []string{"compose", "--project-directory", dir}
	configured := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, "COMPOSE_FILE=") {
			configured = strings.TrimPrefix(entry, "COMPOSE_FILE=")
		}
	}
	files := []string{}
	if configured != "" {
		files = filepath.SplitList(configured)
	} else {
		for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				files = append(files, name)
				break
			}
		}
		if len(files) == 0 {
			return nil, errors.New("no Compose configuration found")
		}
		prefix := "compose.override"
		if strings.HasPrefix(files[0], "docker-compose") {
			prefix = "docker-compose.override"
		}
		for _, suffix := range []string{".yml", ".yaml"} {
			if _, err := os.Stat(filepath.Join(dir, prefix+suffix)); err == nil {
				files = append(files, prefix+suffix)
				break
			}
		}
	}
	for _, file := range files {
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		base = append(base, "-f", file)
	}
	return base, nil
}
