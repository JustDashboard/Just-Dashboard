// Package stackports keeps the dashboard's three service ports and their
// consumers in one persisted configuration during install and lifecycle work.
package stackports

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	procnet "github.com/shirou/gopsutil/v4/net"
)

type Selection struct {
	BackendPort  int
	FrontendPort int
	Port         int
	Endpoint     string
	EnvPath      string
	Backup       string
	Changed      bool
}

func (s Selection) Health() string {
	if s.BackendPort == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/healthz", s.BackendPort)
}

type inspectedContainer struct {
	ID              string
	State           struct{ Running bool }
	Config          struct{ Labels map[string]string }
	HostConfig      struct{ NetworkMode string }
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string
			HostPort string
		}
	}
}

// Reconcile never stops a port owner. Running containers from this exact
// Compose checkout are recognised through Docker labels and process evidence.
// Everything else remains reserved for its current owner.
func Reconcile(ctx context.Context, dir, compose string, out io.Writer) (Selection, error) {
	return reconcile(ctx, dir, compose, out, observe, portalloc.Available)
}

func reconcile(ctx context.Context, dir, compose string, out io.Writer, observe func(context.Context, string, string) (map[int]string, map[int]bool, error), available func(string, string, int) error) (Selection, error) {
	path := filepath.Join(dir, ".env")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Selection{}, nil
	}
	if err != nil {
		return Selection{}, err
	}
	values := envValues(string(raw))
	ports := []struct {
		key, owner string
		fallback   int
	}{
		{"JD_PORT", "proxy", 8443}, {"JD_BACKEND_PORT", "backend", 8080}, {"JD_FRONTEND_PORT", "frontend", 3000},
	}
	owned, foreign, err := observe(ctx, dir, compose)
	if err != nil {
		return Selection{}, err
	}
	reserved := map[int]bool{}
	selected := map[string]int{}
	result := Selection{EnvPath: path}
	for _, entry := range ports {
		preferred := entry.fallback
		if value := values[entry.key]; value != "" {
			preferred, err = strconv.Atoi(value)
			if err != nil {
				return result, fmt.Errorf("%s is not a port number", entry.key)
			}
		}
		if preferred < 1024 || preferred > 65535 {
			return result, fmt.Errorf("%s must be between 1024 and 65535", entry.key)
		}
		addresses := []string{"127.0.0.1"}
		if entry.owner == "proxy" {
			bind := values["JD_BIND"]
			if bind == "" {
				bind = values["JD_SITE"]
			}
			if bind != "" && bind != "localhost" && bind != "127.0.0.1" {
				ips, err := net.DefaultResolver.LookupHost(ctx, bind)
				if err != nil {
					return result, fmt.Errorf("resolve dashboard bind address: %w", err)
				}
				addresses = append(addresses, ips...)
			}
		}
		port, err := portalloc.Select(preferred, 1024, reserved, func(candidate int) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if foreign[candidate] {
				return portalloc.ErrReserved
			}
			if owner := owned[candidate]; owner != "" {
				if owner == entry.owner {
					return nil
				}
				return fmt.Errorf("%w by %s", portalloc.ErrReserved, owner)
			}
			for _, address := range addresses {
				if err := available(address, "tcp", candidate); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return result, fmt.Errorf("select %s: %w", entry.key, err)
		}
		selected[entry.key], reserved[port] = port, true
		if port != preferred || values[entry.key] == "" {
			result.Changed = true
			fmt.Fprintf(out, "%s: using %d (preferred %d)\n", entry.owner, port, preferred)
		}
	}
	result.Port, result.BackendPort, result.FrontendPort = selected["JD_PORT"], selected["JD_BACKEND_PORT"], selected["JD_FRONTEND_PORT"]
	site := values["JD_SITE"]
	if site == "" {
		site = "localhost"
	}
	scheme := "https"
	if values["JD_TLS"] == "off" {
		scheme = "http"
	}
	result.Endpoint = scheme + "://" + net.JoinHostPort(site, strconv.Itoa(result.Port))
	if !result.Changed {
		return result, nil
	}
	backup, err := os.CreateTemp(dir, ".env.jd-port-previous-*")
	if err != nil {
		return result, err
	}
	result.Backup = backup.Name()
	_, err = backup.Write(raw)
	closeErr := backup.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	if err := atomicWrite(path, []byte(replacePorts(string(raw), selected))); err != nil {
		return result, err
	}
	fmt.Fprintf(out, "Dashboard address: %s\n", result.Endpoint)
	return result, nil
}

func envAssignment(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
		quote := string(value[0])
		if end := strings.Index(value[1:], quote); end >= 0 {
			value = value[1 : end+1]
		}
	} else if index := strings.Index(value, " #"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return key, value, true
}

func envValues(raw string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		if key, value, ok := envAssignment(line); ok {
			values[key] = value
		}
	}
	return values
}

func replacePorts(raw string, ports map[string]int) string {
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		key, _, ok := envAssignment(line)
		if port, exists := ports[key]; ok && exists {
			lines[i] = key + "=" + strconv.Itoa(port)
			seen[key] = true
		}
	}
	for _, key := range []string{"JD_PORT", "JD_BACKEND_PORT", "JD_FRONTEND_PORT"} {
		if !seen[key] {
			lines = append(lines, key+"="+strconv.Itoa(ports[key]))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".jd-ports-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func observe(ctx context.Context, dir, compose string) (map[int]string, map[int]bool, error) {
	owned, foreign := map[int]string{}, map[int]bool{}
	ids, err := hostexec.Command(ctx, "docker", "ps", "--quiet").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("inspect running containers before selecting ports: %w", err)
	}
	pidOwners := map[int32]string{}
	if list := strings.Fields(string(ids)); len(list) > 0 {
		raw, err := hostexec.Command(ctx, "docker", append([]string{"inspect"}, list...)...).Output()
		if err != nil {
			return nil, nil, fmt.Errorf("inspect port ownership: %w", err)
		}
		var containers []inspectedContainer
		if err := json.Unmarshal(raw, &containers); err != nil {
			return nil, nil, fmt.Errorf("invalid Docker port evidence")
		}
		if compose == "" {
			compose = "docker-compose.yml"
		}
		if !filepath.IsAbs(compose) {
			compose = filepath.Join(dir, compose)
		}
		for _, c := range containers {
			if !c.State.Running {
				continue
			}
			service := c.Config.Labels["com.docker.compose.service"]
			ours := filepath.Clean(c.Config.Labels["com.docker.compose.project.working_dir"]) == filepath.Clean(dir) && (service == "backend" || service == "frontend" || service == "proxy")
			matchedFile := false
			for _, file := range strings.Split(c.Config.Labels["com.docker.compose.project.config_files"], ",") {
				if filepath.Clean(file) == filepath.Clean(compose) {
					matchedFile = true
				}
			}
			ours = ours && matchedFile
			for protocol, bindings := range c.NetworkSettings.Ports {
				if !strings.HasSuffix(protocol, "/tcp") {
					continue
				}
				for _, binding := range bindings {
					port, _ := strconv.Atoi(binding.HostPort)
					if port > 0 {
						if ours {
							owned[port] = service
						} else {
							foreign[port] = true
						}
					}
				}
			}
			if ours && c.HostConfig.NetworkMode == "host" {
				top, err := hostexec.Command(ctx, "docker", "top", c.ID, "-eo", "pid").Output()
				if err != nil {
					return nil, nil, fmt.Errorf("inspect %s processes: %w", service, err)
				}
				for _, value := range strings.Fields(string(top)) {
					if pid, err := strconv.ParseInt(value, 10, 32); err == nil {
						pidOwners[int32(pid)] = service
					}
				}
			}
		}
	}
	connections, err := procnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return nil, nil, err
	}
	for _, connection := range connections {
		if connection.Status != "LISTEN" {
			continue
		}
		port := int(connection.Laddr.Port)
		if owner := pidOwners[connection.Pid]; owner != "" {
			owned[port] = owner
		} else if owned[port] == "" {
			foreign[port] = true
		}
	}
	return owned, foreign, nil
}

// Start closes the observation/start race with bounded retries. start must wait
// for container health, since a host-network bind failure can occur after
// Compose has successfully created the container.
func Start(ctx context.Context, dir, compose string, out io.Writer, start func() error, accept func(Selection) error) error {
	selected, err := Reconcile(ctx, dir, compose, out)
	if err != nil {
		return err
	}
	if accept != nil {
		if err := accept(selected); err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		startErr := start()
		if startErr == nil {
			return nil
		}
		if attempt == 2 {
			return startErr
		}
		selected, err = Reconcile(ctx, dir, compose, out)
		if err != nil {
			return fmt.Errorf("%w; checking port ownership: %v", startErr, err)
		}
		if !selected.Changed {
			return startErr
		}
		if accept != nil {
			if err := accept(selected); err != nil {
				return err
			}
		}
		fmt.Fprintln(out, "A port was claimed during startup; retrying with the updated configuration.")
	}
	return fmt.Errorf("dashboard ports remained unavailable")
}
