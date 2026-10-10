package netflows

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type DockerSources interface {
	ListRunning(context.Context) ([]dockerx.Container, error)
	NetworkSource(context.Context, string) (*dockerx.NetworkSource, error)
	NetworkSourceCommand(context.Context, *dockerx.NetworkSource, string, ...string) (*exec.Cmd, func(), error)
}

type Collector interface{ Collect(context.Context) Cycle }

type Native struct {
	Docker                DockerSources
	cursor, batchSamples  int
	hostNamespacePath     string
	procReader            func(int, string) (string, error)
	linkReader            func(string) (string, error)
	versionRead           bool
	version, versionError string
	versionAt             time.Time
}
type verifiedSource struct {
	source *dockerx.NetworkSource
	ns     string
}

var ssArgs = []string{"-H", "-n", "-t", "-u", "-a", "-i", "-e", "-p", "-O"}

type cappedOutput struct {
	buffer    bytes.Buffer
	cap       int
	truncated bool
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.cap - w.buffer.Len()
	if n > remaining {
		w.truncated = true
		p = p[:remaining]
	}
	_, _ = w.buffer.Write(p)
	return n, nil
}
func (w *cappedOutput) String() string { return w.buffer.String() }

func readCommand(ctx context.Context, cmd *exec.Cmd) (string, bool, error) {
	out, stderr := &cappedOutput{cap: MaxOutputBytes}, &cappedOutput{cap: 4096}
	cmd.Stdout, cmd.Stderr = out, stderr
	_, err := hostexec.RunGroup(ctx, cmd, 75*time.Millisecond)
	if err != nil {
		return "", out.truncated, fmt.Errorf("native socket inventory failed: %w (%s)", err, clip(stderr.String(), 512))
	}
	data := out.String()
	if out.truncated {
		i := strings.LastIndexByte(data, '\n')
		if i < 0 {
			data = ""
		} else {
			data = data[:i+1]
		}
	}
	return data, out.truncated, nil
}

func namespaceIdentity(file *os.File) string {
	if file == nil {
		return ""
	}
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	}
	return ""
}

func (n *Native) Collect(ctx context.Context) Cycle {
	start := time.Now().UTC()
	c := Cycle{At: start, Sources: []Source{}, DockerStatus: "unavailable", Tool: "ss -H -n -t -u -a -i -e -p -O"}
	if !n.versionRead || start.Sub(n.versionAt) >= time.Hour {
		versionCtx, cancel := context.WithTimeout(ctx, time.Second)
		out, _, err := readCommand(versionCtx, hostexec.CommandOnHost(versionCtx, "ss", "-V"))
		cancel()
		if err != nil {
			n.versionError = "The native ss version could not be read."
			n.version = ""
		} else {
			n.version = clip(strings.TrimSpace(out), 128)
			n.versionError = ""
		}
		n.versionRead = true
		n.versionAt = time.Now().UTC()
	}
	c.ToolVersion, c.ToolVersionError = n.version, n.versionError
	c.ToolVersionCheckedAt = n.versionAt
	if release, err := os.ReadFile(hostexec.HostPath("/proc/sys/kernel/osrelease")); err == nil {
		c.KernelRelease = clip(strings.TrimSpace(string(release)), 128)
	}
	boot, err := os.ReadFile(hostexec.HostPath("/proc/sys/kernel/random/boot_id"))
	if err == nil {
		c.BootID = strings.TrimSpace(string(boot))
	}
	type candidate struct {
		source *dockerx.NetworkSource
		cmd    *exec.Cmd
		close  func()
		ns     string
	}
	candidates := []candidate{}
	verified := map[string]verifiedSource{}
	validated := map[string]bool{}
	if n.Docker != nil {
		items, err := n.Docker.ListRunning(ctx)
		if err != nil {
			c.DockerError = clip(err.Error(), 512)
		} else {
			c.DockerStatus = "observed"
			sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
			count := len(items)
			admitted := min(count, MaxSources)
			c.OmittedSources = count - admitted
			if c.OmittedSources > 0 {
				c.DockerStatus = "partial"
			}
			for i := 0; i < admitted; i++ {
				item := items[(n.cursor+i)%count]
				source, err := n.Docker.NetworkSource(ctx, item.ID)
				if err != nil {
					c.Sources = append(c.Sources, Source{ID: item.ID, Name: clip(item.Name, 128), Status: "unavailable", Error: clip(err.Error(), 512), ObservedAt: time.Now().UTC()})
					continue
				}
				cmd, close, err := n.Docker.NetworkSourceCommand(ctx, source, "ss", ssArgs...)
				if err != nil || cmd == nil || len(cmd.ExtraFiles) != 1 {
					if close != nil {
						close()
					}
					c.Sources = append(c.Sources, Source{ID: item.ID, Name: clip(item.Name, 128), Status: "unavailable", Error: "The container namespace identity could not be pinned.", ObservedAt: time.Now().UTC()})
					continue
				}
				if close != nil {
					defer close()
				}
				ns := namespaceIdentity(cmd.ExtraFiles[0])
				verified[source.ID] = verifiedSource{source, ns}
				candidates = append(candidates, candidate{source, cmd, close, ns})
			}
			// Each bounded batch gets two successive reads before rotating. A
			// newly admitted batch always starts with an uncounted baseline.
			n.batchSamples++
			if count > 0 && n.batchSamples >= 2 {
				n.cursor = (n.cursor + admitted) % count
				n.batchSamples = 0
			}
		}
	}
	seen := map[string]bool{}
	hostPath := n.hostNamespacePath
	if hostPath == "" {
		hostPath = hostexec.HostPath("/proc/1/ns/net")
	}
	host, err := os.Open(hostPath)
	if err == nil {
		defer host.Close()
		ns := namespaceIdentity(host)
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		args := append([]string{"--net=/proc/self/fd/3", "--", "ss"}, ssArgs...)
		cmd := hostexec.CommandOnHost(readCtx, "nsenter", args...)
		cmd.ExtraFiles = []*os.File{host}
		out, capped, err := readCommand(readCtx, cmd)
		cancel()
		s := nativeSource("host", "Host", ns, out, capped, err)
		if ns != "" {
			seen[ns] = true
		}
		n.attribute(ctx, &s, verified, validated)
		c.Sources = append(c.Sources, s)
	} else {
		c.Sources = append(c.Sources, Source{ID: "host", Name: "Host", Status: "unavailable", Error: "The host namespace identity is unreadable.", ObservedAt: time.Now().UTC()})
	}
	for _, candidate := range candidates {
		if candidate.ns == "" || seen[candidate.ns] {
			c.Sources = append(c.Sources, Source{ID: candidate.source.ID, Name: clip(candidate.source.Name, 128), Namespace: candidate.ns, Status: "shared_namespace", Error: "This namespace is already sampled; socket ownership is checked per descriptor, without counting it twice.", ObservedAt: time.Now().UTC()})
			continue
		}
		seen[candidate.ns] = true
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, capped, err := readCommand(readCtx, candidate.cmd)
		cancel()
		if err == nil {
			post, close, checkErr := n.Docker.NetworkSourceCommand(ctx, candidate.source, "ss", ssArgs...)
			if checkErr != nil || post == nil || len(post.ExtraFiles) != 1 || namespaceIdentity(post.ExtraFiles[0]) != candidate.ns {
				err = fmt.Errorf("container identity or namespace changed during socket observation")
			}
			if close != nil {
				close()
			}
		}
		s := nativeSource(candidate.source.ID, clip(candidate.source.Name, 128), candidate.ns, out, capped, err)
		n.attribute(ctx, &s, verified, validated)
		c.Sources = append(c.Sources, s)
	}
	// Descriptor reads and the full capture are bracketed by a fresh Docker
	// instance check. Cached checks bound cost within a cycle; this final read
	// prevents a restart later in that cycle from inheriting the old name.
	for id, known := range verified {
		post, close, err := n.Docker.NetworkSourceCommand(ctx, known.source, "ss", ssArgs...)
		valid := err == nil && post != nil && len(post.ExtraFiles) == 1 && namespaceIdentity(post.ExtraFiles[0]) == known.ns
		if close != nil {
			close()
		}
		if valid {
			continue
		}
		for i := range c.Sources {
			src := &c.Sources[i]
			if src.ID == id && src.Status != "shared_namespace" {
				src.Status = "unavailable"
				src.Error = "Container identity changed before the observation completed."
				src.Values = nil
				src.Sockets = 0
			}
			for j := range src.Values {
				if src.Values[j].Owner.ContainerID == id {
					src.Values[j].Owner = Owner{Status: "unknown", Reason: "Container identity changed before attribution completed."}
				}
			}
		}
	}
	if c.BootID == "" {
		for i := range c.Sources {
			if c.Sources[i].Status == "observed" {
				c.Sources[i].Status = "partial"
			}
			c.Sources[i].Error = "Boot identity is unreadable; stable socket accounting is unavailable."
		}
	}
	for i := range c.Sources {
		src := &c.Sources[i]
		for _, sk := range src.Values {
			if sk.Protocol == "tcp" {
				src.TCP++
				if sk.Tx != nil {
					src.TCPTxCounters++
				}
				if sk.Rx != nil {
					src.TCPRxCounters++
				}
			} else {
				src.UDP++
			}
			if sk.Owner.Status != "verified_process" && sk.Owner.Status != "verified_container" {
				src.UnverifiedOwners++
			}
		}
	}
	c.FinishedAt = time.Now().UTC()
	c.ElapsedMillis = c.FinishedAt.Sub(start).Milliseconds()
	return c
}

func nativeSource(id, name, ns, out string, capped bool, err error) Source {
	s := Source{ID: id, Name: name, Namespace: ns, ObservedAt: time.Now().UTC(), Status: "unavailable"}
	if err != nil {
		s.Error = clip(err.Error(), 512)
		return s
	}
	if ns == "" {
		s.Error = "The network namespace identity is unknown."
		return s
	}
	s = parseSS(out, MaxSockets)
	s.ID, s.Name, s.Namespace, s.ObservedAt = id, name, ns, time.Now().UTC()
	s.Truncated = s.Truncated || capped
	if s.Truncated {
		s.Status = "partial"
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func readProc(pid int, leaf string) (string, error) {
	f, err := os.Open(hostexec.HostPath(fmt.Sprintf("/proc/%d/%s", pid, leaf)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 {
		return "", fmt.Errorf("bounded process metadata is unreadable")
	}
	return string(b), nil
}

func (n *Native) proc(pid int, leaf string) (string, error) {
	if n.procReader != nil {
		return n.procReader(pid, leaf)
	}
	return readProc(pid, leaf)
}
func (n *Native) link(path string) (string, error) {
	if n.linkReader != nil {
		return n.linkReader(path)
	}
	return os.Readlink(hostexec.HostPath(path))
}

func startTicks(stat string) string {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 20 {
		return ""
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return ""
	}
	return f[19]
}

func cgroupID(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		for _, part := range strings.Split(line, "/") {
			part = strings.TrimSuffix(strings.TrimPrefix(part, "docker-"), ".scope")
			if len(part) == 64 {
				if _, err := strconv.ParseUint(part[:16], 16, 64); err != nil {
					continue
				}
				valid := true
				for _, c := range part {
					if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
						valid = false
					}
				}
				if valid {
					return part
				}
			}
		}
	}
	return ""
}

func (n *Native) attribute(ctx context.Context, s *Source, verified map[string]verifiedSource, validated map[string]bool) {
	for i := range s.Values {
		if ctx.Err() != nil {
			return
		}
		sk := &s.Values[i]
		if len(sk.owners) != 1 || sk.Inode == "" {
			continue
		}
		who := sk.owners[0]
		before, err := n.proc(who.PID, "stat")
		if err != nil {
			continue
		}
		ticks := startTicks(before)
		if ticks == "" {
			continue
		}
		cg, err := n.proc(who.PID, "cgroup")
		if err != nil {
			continue
		}
		fd, err := n.link(fmt.Sprintf("/proc/%d/fd/%d", who.PID, who.FD))
		if err != nil || fd != "socket:["+sk.Inode+"]" {
			continue
		}
		after, err := n.proc(who.PID, "stat")
		if err != nil || startTicks(after) != ticks {
			continue
		}
		id := cgroupID(cg)
		owner := Owner{Status: "verified_process", Program: clip(who.Name, 128), PID: who.PID, StartTicks: ticks}
		if id != "" {
			known, ok := verified[id]
			if !ok {
				sk.Owner.Reason = "The descriptor belongs to a container outside the verified source budget."
				continue
			}
			valid, checked := validated[id]
			if !checked {
				post, close, err := n.Docker.NetworkSourceCommand(ctx, known.source, "ss", ssArgs...)
				valid = err == nil && post != nil && len(post.ExtraFiles) == 1 && namespaceIdentity(post.ExtraFiles[0]) == known.ns
				if close != nil {
					close()
				}
				validated[id] = valid
			}
			if !valid {
				sk.Owner.Reason = "Container identity changed or became unreadable during attribution."
				continue
			}
			// Recheck the descriptor after Docker inspection too: PID reuse or
			// descriptor transfer must not label another owner's observation.
			last, err := n.proc(who.PID, "stat")
			if err != nil || startTicks(last) != ticks {
				continue
			}
			lastCG, err := n.proc(who.PID, "cgroup")
			if err != nil || lastCG != cg {
				continue
			}
			lastFD, err := n.link(fmt.Sprintf("/proc/%d/fd/%d", who.PID, who.FD))
			if err != nil || lastFD != fd {
				continue
			}
			owner.Status, owner.ContainerID, owner.ContainerName, owner.ContainerStartedAt = "verified_container", known.source.ID, clip(known.source.Name, 128), known.source.StartedAt
		}
		sk.Owner = owner
	}
}
