package netcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type commandFactory func(context.Context, string, ...string) *exec.Cmd
type Native struct{ command commandFactory }

func NewNative() *Native { return &Native{command: hostexec.CommandOnHost} }

type limitedOutput struct{ buffer bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	_, _ = b.buffer.Write(p[:min(len(p), max(0, 16384-b.buffer.Len()))])
	return n, nil
}

func (n *Native) Interfaces(ctx context.Context) ([]Interface, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := n.command(ctx, "ip", "-j", "link", "show")
	var output limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if _, err := hostexec.RunGroup(ctx, cmd, 200*time.Millisecond); err != nil {
		return nil, fmt.Errorf("native interface inventory: %w", err)
	}
	var rows []struct {
		Name      string   `json:"ifname"`
		Index     int      `json:"ifindex"`
		Flags     []string `json:"flags"`
		Address   string   `json:"address"`
		LinkIndex int      `json:"link_index"`
		LinkType  string   `json:"link_type"`
		LinkInfo  struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal(output.buffer.Bytes(), &rows); err != nil || len(rows) > 512 {
		return nil, fmt.Errorf("native interface inventory is unreadable or exceeds its bound")
	}
	out := []Interface{}
	for _, r := range rows {
		if interfacePattern.MatchString(r.Name) && r.Index > 0 {
			out = append(out, Interface{Name: r.Name, Index: r.Index, Up: slices.Contains(r.Flags, "UP"), NativeKey: fmt.Sprintf("%d/%s/%s/%d/%s/%s", r.Index, r.Name, r.Address, r.LinkIndex, r.LinkType, r.LinkInfo.Kind)})
		}
	}
	return out, nil
}

func (n *Native) Ready() error {
	for _, tool := range []string{"ip", "timeout", "tcpdump"} {
		if !hostexec.Available(tool) {
			return fmt.Errorf("%w: install %s on the host", ErrUnavailable, tool)
		}
	}
	return nil
}
func selectInterface(rows []Interface, name string) (Interface, error) {
	for _, r := range rows {
		if r.Name == name && r.Up {
			return r, nil
		}
	}
	return Interface{}, fmt.Errorf("the selected native interface is absent or down")
}

var droppedPattern = regexp.MustCompile(`(?m)^([0-9]+) packets dropped by kernel$`)

func (n *Native) Capture(ctx context.Context, r Request) (*Result, error) {
	r, err := Validate(r)
	if err != nil {
		return nil, err
	}
	rows, err := n.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	before, err := selectInterface(rows, r.Interface)
	if err != nil {
		return nil, err
	}
	budget, cancel := context.WithTimeout(ctx, time.Duration(r.Seconds+5)*time.Second)
	defer cancel()
	writer := &pcapWriter{request: r, stop: cancel}
	lastProgress := time.Time{}
	writer.progress = func(packets, bytes int) {
		now := time.Now().UTC()
		if now.Sub(lastProgress) < time.Second {
			return
		}
		lastProgress = now
		ReportProgress(ctx, Result{CheckedAt: now, Packets: packets, Bytes: bytes, LinkType: writer.linkType, InterfaceIndex: before.Index, StopReason: "capturing", Cleanup: hostexec.GroupResult{ExitCode: -1}})
	}
	cmd := n.command(budget, "timeout", Argv(r)...)
	var stderr limitedOutput
	cmd.Stdout, cmd.Stderr = writer, &stderr
	cleanup, runErr := hostexec.RunGroup(budget, cmd, 200*time.Millisecond)
	result := writer.result()
	result.CheckedAt = time.Now().UTC()
	result.Cleanup = cleanup
	result.InterfaceIndex = before.Index
	result.NativeSummary = strings.TrimSpace(stderr.buffer.String())
	if match := droppedPattern.FindStringSubmatch(result.NativeSummary); len(match) > 0 {
		if count, e := strconv.ParseUint(match[1], 10, 64); e == nil {
			result.KernelDropped = &count
		}
	}
	if result.StopReason == "" {
		switch {
		case ctx.Err() != nil:
			result.StopReason = "cancelled"
		case cleanup.ExitCode == 124 || budget.Err() == context.DeadlineExceeded:
			result.StopReason = "time_limit"
		case runErr == nil:
			result.StopReason = "native_exit"
		default:
			result.StopReason = "native_error"
		}
	}
	identityCtx, identityCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer identityCancel()
	if after, e := n.Interfaces(identityCtx); e == nil {
		current, e := selectInterface(after, r.Interface)
		result.IdentityVerified = e == nil && current.Index == before.Index && current.NativeKey == before.NativeKey
	}
	if writer.err != nil {
		result.Artifact = nil
		result.ArtifactAvailable = false
		return &result, writer.err
	}
	if ctx.Err() != nil {
		return &result, ctx.Err()
	}
	if runErr != nil && result.StopReason != "packet_limit" && result.StopReason != "byte_limit" && result.StopReason != "time_limit" {
		return &result, errors.Join(fmt.Errorf("native capture failed"), runErr)
	}
	if !result.ArtifactAvailable {
		return &result, fmt.Errorf("native capture returned no valid PCAP header")
	}
	if !result.IdentityVerified {
		return &result, fmt.Errorf("native interface identity changed or could not be verified after capture")
	}
	return &result, nil
}
