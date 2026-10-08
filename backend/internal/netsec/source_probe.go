package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// SourceCommand comes from a freshly verified, pinned container namespace.
// It is never constructed from request-provided executable names or paths.
type SourceCommand func(context.Context, string, ...string) (*exec.Cmd, func(), error)

func RunSourceDiagnostic(ctx context.Context, command SourceCommand, tool string, args ...string) (string, string, error) {
	if tool != "ip" && tool != "dig" && tool != "nc" {
		return "", "", fmt.Errorf("unsupported source diagnostic tool")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	start := time.Now()
	cmd, closeLease, err := command(ctx, tool, args...)
	if err != nil {
		return "", "", err
	}
	defer closeLease()
	var raw probeOutput
	cmd.Stdout, cmd.Stderr = &raw, &raw
	_, err = hostexec.RunGroup(ctx, cmd, 200*time.Millisecond)
	return strings.TrimSpace(raw.String()), time.Since(start).Round(time.Millisecond).String(), err
}

// SourcePortCheck measures one literal TCP destination. UDP connect success
// cannot prove a listener, and is deliberately not offered as reachability.
func SourcePortCheck(ctx context.Context, command SourceCommand, target string, port int, source string) (*ProbeResult, error) {
	addr, err := netip.ParseAddr(target)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("a literal target and port from 1 to 65535 are required")
	}
	args := []string{"-z", "-w", "6"}
	if addr.Is6() {
		args = append(args, "-6")
	}
	if source != "" {
		src, err := netip.ParseAddr(source)
		if err != nil || src.Is4() != addr.Is4() {
			return nil, fmt.Errorf("source and target must have the same family")
		}
		args = append(args, "-s", src.String())
	}
	args = append(args, addr.String(), strconv.Itoa(port))
	output, elapsed, err := RunSourceDiagnostic(ctx, command, "nc", args...)
	result := &ProbeResult{Tool: "port", Target: addr.String(), Output: output, Duration: elapsed, OK: err == nil}
	if err != nil {
		result.Error = err.Error()
		var exit *exec.ExitError
		unavailable := strings.ToLower(output)
		if !errors.As(err, &exit) || exit.ExitCode() == 126 || exit.ExitCode() == 127 || strings.Contains(unavailable, "invalid option") || strings.Contains(unavailable, "unrecognized option") || strings.Contains(unavailable, "failed to execute") {
			return result, fmt.Errorf("the selected-source TCP adapter could not run: %w", err)
		}
	} else {
		result.Output = "Connected from the selected container network namespace."
	}
	return result, nil
}

// SourceDNS queries only native configured servers in the selected namespace.
// Absolute wire names avoid search-domain surprises. No host/public fallback
// or NSS/search/encrypted-transport claim is made.
func SourceDNS(ctx context.Context, command SourceCommand, target, family string, servers []string) (*ProbeResult, error) {
	if !ValidTarget(target) || (family != "inet" && family != "inet6") {
		return nil, fmt.Errorf("a DNS name and selected family are required")
	}
	if _, err := netip.ParseAddr(target); err == nil {
		return nil, fmt.Errorf("a DNS name is required")
	}
	record := "A"
	if family == "inet6" {
		record = "AAAA"
	}
	res := &ProbeResult{Tool: "dns", Target: target, Records: []string{}}
	if len(servers) == 0 {
		res.Error = "No representable native container nameserver is available; no fallback was queried."
		return res, nil
	}
	// A native negative response is an answer. Trying another resolver after
	// NXDOMAIN could disclose a private name outside its intended authority.
	for _, server := range servers[:min(3, len(servers))] {
		if _, err := netip.ParseAddr(server); err != nil {
			return nil, fmt.Errorf("native nameservers must be literal addresses")
		}
		output, elapsed, err := RunSourceDiagnostic(ctx, command, "dig", "@"+server, "+time=2", "+tries=1", "+noall", "+answer", "+comments", strings.TrimSuffix(target, ".")+".", record)
		res.Output, res.Duration = output, elapsed
		if err != nil {
			res.Error = err.Error()
			continue
		}
		res.Error = ""
		res.OK = strings.Contains(output, "status: NOERROR")
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 5 || fields[3] != record {
				continue
			}
			addr, err := netip.ParseAddr(fields[4])
			if err == nil && ((family == "inet") == addr.Is4()) && len(res.Records) < 8 {
				res.Records = append(res.Records, addr.String())
			}
		}
		return res, nil
	}
	return res, nil
}
