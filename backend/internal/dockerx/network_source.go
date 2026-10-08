package dockerx

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/docker/docker/api/types/container"
)

var networkContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// NetworkSource carries only network metadata. PID identity is private and
// freshly checked; environment variables and mounts never enter diagnostics.
type NetworkSource struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Mode        string   `json:"mode"`
	Addresses   []string `json:"addresses"`
	Nameservers []string `json:"nameservers"`
	DNSError    string   `json:"dnsError,omitempty"`
	PID         int      `json:"-"`
	StartedAt   string   `json:"-"`
	StartTicks  string   `json:"-"`
}

func (c *Client) NetworkSource(ctx context.Context, id string) (*NetworkSource, error) {
	if !networkContainerID.MatchString(id) {
		return nil, fmt.Errorf("select a full container ID from the running-container inventory")
	}
	insp, err := c.networkInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if insp.State == nil || !insp.State.Running || insp.State.Pid <= 1 || insp.ID != id {
		return nil, fmt.Errorf("the selected container is not running")
	}
	s := &NetworkSource{ID: id, Name: strings.TrimPrefix(insp.Name, "/"), PID: insp.State.Pid, StartedAt: insp.State.StartedAt, Addresses: []string{}, Nameservers: []string{}}
	if insp.HostConfig != nil {
		s.Mode = string(insp.HostConfig.NetworkMode)
	}
	if insp.NetworkSettings != nil {
		for _, n := range insp.NetworkSettings.Networks {
			if n == nil {
				continue
			}
			for _, value := range []string{n.IPAddress, n.GlobalIPv6Address} {
				if addr, err := netip.ParseAddr(value); err == nil {
					s.Addresses = append(s.Addresses, addr.Unmap().String())
				}
			}
		}
	}
	stat, err := readNetworkProc(s.PID, "stat")
	if err != nil {
		return nil, err
	}
	s.StartTicks, err = processStartTicks(stat)
	if err != nil {
		return nil, err
	}
	if err := c.checkNetworkSource(ctx, s); err != nil {
		return nil, err
	}
	// This is a server-derived path under a verified process root, never a
	// client filesystem path. Only literal nameserver addresses are retained.
	resolv, err := readNetworkProc(s.PID, "root/etc/resolv.conf")
	if err != nil {
		s.DNSError = "The container's native resolver configuration is unreadable."
	} else {
		s.Nameservers = configuredNameservers(resolv)
	}
	if err := c.checkNetworkSource(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *Client) checkNetworkSource(ctx context.Context, source *NetworkSource) error {
	if source == nil || !networkContainerID.MatchString(source.ID) || source.PID <= 1 {
		return fmt.Errorf("the container network source identity is invalid")
	}
	insp, err := c.networkInspect(ctx, source.ID)
	if err != nil {
		return err
	}
	if insp.State == nil || !insp.State.Running || insp.State.Pid != source.PID || insp.State.StartedAt != source.StartedAt {
		return fmt.Errorf("the container restarted while its network path was being read")
	}
	stat, err := readNetworkProc(source.PID, "stat")
	if err != nil {
		return err
	}
	ticks, err := processStartTicks(stat)
	if err != nil || ticks != source.StartTicks {
		return fmt.Errorf("the container process identity changed")
	}
	cgroup, err := readNetworkProc(source.PID, "cgroup")
	if err != nil || !containerCgroup(cgroup, source.ID) {
		return fmt.Errorf("the process cgroup cannot be attributed to the selected container")
	}
	return nil
}

// Namespace preconditions must bypass inventory snapshots, including when a
// caller happens to reuse a read-scoped context.
func (c *Client) networkInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	cli, err := c.api()
	if err != nil {
		return container.InspectResponse{}, err
	}
	return cli.ContainerInspect(ctx, id)
}

// NetworkSourceCommand pins the verified namespace before forking. A PID
// reused between validation and exec cannot redirect the command elsewhere.
// Callers supply a closed diagnostic tool/argv and close the returned lease.
func (c *Client) NetworkSourceCommand(ctx context.Context, source *NetworkSource, tool string, args ...string) (*exec.Cmd, func(), error) {
	if err := c.checkNetworkSource(ctx, source); err != nil {
		return nil, nil, err
	}
	fd, err := os.Open(hostexec.HostPath(fmt.Sprintf("/proc/%d/ns/net", source.PID)))
	if err != nil {
		return nil, nil, err
	}
	if err := c.checkNetworkSource(ctx, source); err != nil {
		fd.Close()
		return nil, nil, err
	}
	argv := append([]string{"--net=/proc/self/fd/3", "--", tool}, args...)
	cmd := hostexec.CommandOnHost(ctx, "nsenter", argv...)
	cmd.ExtraFiles = []*os.File{fd}
	return cmd, func() { _ = fd.Close() }, nil
}

func readNetworkProc(pid int, leaf string) (string, error) {
	f, err := os.Open(hostexec.HostPath(fmt.Sprintf("/proc/%d/%s", pid, leaf)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(b) > 64<<10 {
		return "", fmt.Errorf("the container's network metadata is unreadable or exceeds 64 KiB")
	}
	return string(b), nil
}

func processStartTicks(stat string) (string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("the process identity is unreadable")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("the process identity is incomplete")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", fmt.Errorf("the process start identity is invalid")
	}
	return fields[19], nil
}

func containerCgroup(cgroup, id string) bool {
	for _, line := range strings.Split(cgroup, "\n") {
		for _, component := range strings.Split(line, "/") {
			if component == id || component == "docker-"+id+".scope" {
				return true
			}
		}
	}
	return false
}

func configuredNameservers(config string) []string {
	servers := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(config, "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		addr, err := netip.ParseAddr(fields[1])
		if err != nil || addr.IsUnspecified() || addr.IsMulticast() {
			continue
		}
		value := addr.Unmap().String()
		if !seen[value] && len(servers) < 3 {
			seen[value] = true
			servers = append(servers, value)
		}
	}
	return servers
}
