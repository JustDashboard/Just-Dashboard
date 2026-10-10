// Package netx manages this host's network: its devices, routing, gateway,
// protection, shaping, tunnels and resolver.
//
// netsec reads the network to judge who can reach the machine; this package
// changes it. They are separate because their failure modes are: a wrong
// reading is a misleading page, and a wrong change is a server nobody can
// reach — including the operator, through the network the dashboard itself is
// being reached over. So everything here follows three rules.
//
//   - **One spec, restored by the host.** Every device, route, rule, gateway
//     entry and kernel setting the dashboard makes is written into one spec
//     (spec.go) and rendered into files `ip`, `tc`, `nft` and sysctl read at
//     boot through a unit of their own (persist.go). The host restores them
//     whether or not the dashboard is running, and a reinstalled dashboard
//     finds what the last one made.
//   - **The client path is guarded.** Every change is judged against the way
//     the kernel answers the address the request came from (path.go). The
//     device it leaves through, the gateway it uses and the address it answers
//     from are never taken away, and a route or rule is applied, checked
//     against that path again, and rolled back if it moved.
//   - **Only what was made here is removed.** Docker's bridges, Tailscale's
//     device and table, the provider's DHCP routes and the kernel's own are
//     read and named with their owner, and never edited.
//
// Every command is hostexec with an explicit argument vector. The batch files
// the boot unit reads are data for `ip`, `tc` and `nft`, never text for a
// shell, and every value written into them has been parsed by type first
// (validate.go).
package netx

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var (
	// ErrGuarded is a change the client-path guard refused. It is a 409 with
	// the reason, never a 500: the request was understood and declined.
	ErrGuarded = errors.New("this change would cut off your own connection to the dashboard")
	// ErrNotManaged is an attempt to remove or edit something the dashboard
	// did not create.
	ErrNotManaged = errors.New("this was not created by Just Dashboard")
	// ErrNotFound is a named object that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists is an object that already exists under that name.
	ErrExists = errors.New("already exists")
	// ErrUnavailable is a tool this host does not have. Information, not a
	// fault: the page draws a placeholder with the way to install it.
	ErrUnavailable = errors.New("not available on this host")
	// ErrReadOnly is a part of the network this host's other software owns in
	// a way an edit here could not honestly take effect on.
	ErrReadOnly = errors.New("read-only on this host")
)

// GuardError carries the sentence a refusal is shown with.
type GuardError struct{ Reason string }

func (e *GuardError) Error() string { return e.Reason }
func (e *GuardError) Unwrap() error { return ErrGuarded }

func guarded(format string, args ...any) error {
	return &GuardError{Reason: fmt.Sprintf(format, args...)}
}

// UnavailableError names the tool that is missing and the package that
// provides it, so the page can offer the install rather than a dead end.
type UnavailableError struct {
	Tool    string
	Package string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%s is not installed on this host", e.Tool)
}
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// ReadOnlyError explains why a part of the network can be read but not
// written from here.
type ReadOnlyError struct{ Reason string }

func (e *ReadOnlyError) Error() string { return e.Reason }
func (e *ReadOnlyError) Unwrap() error { return ErrReadOnly }

// Service is the network module. One per process.
type Service struct {
	// mu serialises every change. Two applies racing would each render the
	// spec they read and the second would write files describing a network
	// missing the first one's change.
	mu sync.Mutex

	paths Paths
	db    *sql.DB
	log   *slog.Logger
	// trustedRanges are the dashboard's allowlist ranges narrow enough to
	// mean "the operator's networks" (trusted.go), which no drop in the
	// gateway table may ever match.
	trustedRanges []netip.Prefix

	sampler   *Sampler
	vpn       *VPNStore
	wg        *wgRecord
	flows     *flowSampler
	telemetry *gatewayTelemetry
	// forwardChecks are the last target checks, by forward id, kept for the
	// page; they are measurements, not configuration.
	checksMu            sync.Mutex
	forwardChecks       map[int]ForwardCheck
	independentRecovery bool
	recoveryInstalled   bool
	// incidentMu serialises the Overview's concurrent readers folding their
	// findings into the incident history.
	incidentMu sync.Mutex
	// history is the route observer's last reading (route_history.go).
	history routeObserver
	// forwardingSample is the last forwarded-datagram reading per family,
	// which the next read turns into a rate (forwarding.go).
	forwardingSample forwardingSamples
	// egress measures and switches egress groups (egress_monitor.go).
	egress *egressMonitor
	// egressNetns is the namespace file member probes and connection
	// tracking run in; empty is this process's own, the host's. Tests point
	// it at a throwaway namespace, as egressNetnsRoot is where simulation
	// namespaces are opened from (the host's /run/netns otherwise).
	egressNetns     string
	egressNetnsRoot string
}

// Paths are where the module reads and writes on the host. Tests point them
// at a temporary directory.
type Paths struct {
	// Dir holds the spec and everything rendered from it.
	Dir string
	// Sysctl is the drop-in the kernel settings are written to.
	Sysctl string
	// Unit is the boot unit.
	Unit string
	// Resolved is systemd-resolved's drop-in.
	Resolved string
	// Hosts is the host's hosts file.
	Hosts string
	// WireGuard is wg-quick's configuration directory.
	WireGuard string
}

// DefaultPaths are the host's own.
func DefaultPaths() Paths {
	return Paths{
		Dir:       "/etc/just-dashboard/network",
		Sysctl:    "/etc/sysctl.d/90-just-dashboard.conf",
		Unit:      "/etc/systemd/system/" + UnitName,
		Resolved:  "/etc/systemd/resolved.conf.d/90-just-dashboard.conf",
		Hosts:     "/etc/hosts",
		WireGuard: "/etc/wireguard",
	}
}

// Options are what New needs from the rest of the server.
type Options struct {
	Paths Paths
	// DB is the dashboard's store, for the interface samples and the VPN
	// clients' sealed configurations.
	DB  *sql.DB
	Log *slog.Logger
	// Allowlist is JD_ALLOWED_CIDRS.
	Allowlist []string
	// Seal and Open encrypt what the VPN store keeps; auth.Sealer's.
	Seal func(string) (string, error)
	Open func(string) (string, error)
	// SampleEvery and Retention are the metrics recorder's, so the
	// per-interface history is kept at the same grain and for as long.
	SampleEvery time.Duration
	Retention   time.Duration
	// IndependentRecovery installs this static executable on the host and
	// arms a systemd timer before managed runtime changes.
	IndependentRecovery bool
}

// New builds the module. It touches nothing on the host; Start begins the
// sampler.
func New(opts Options) *Service {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	s := &Service{
		paths:               opts.Paths,
		db:                  opts.DB,
		log:                 opts.Log,
		trustedRanges:       operatorRanges(opts.Allowlist),
		independentRecovery: opts.IndependentRecovery,
	}
	// Gateway slice: the renderer reads a fetched blocklist's cache from here
	// (gateway.go, gatewayListDir).
	gatewayListDir = filepath.Join(opts.Paths.Dir, "lists")
	s.sampler = newSampler(opts.DB, opts.Log, opts.SampleEvery, opts.Retention)
	s.vpn = newVPNStore(opts.DB, opts.Seal, opts.Open)
	s.wg = newWGRecord(opts.DB, opts.Log, opts.Retention)
	s.flows = newFlowSampler()
	s.telemetry = newGatewayTelemetry(opts.DB, opts.Log)
	s.egress = newEgressMonitor(s, newEgressStore(opts.DB))
	return s
}

// Start begins sampling interface counters and WireGuard's peers. It returns
// once the first interface read has been taken, so the first page load
// already has a rate to show.
func (s *Service) Start(ctx context.Context) {
	if s.independentRecovery {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		if err := RecoverNetwork(recovery, s.paths.Dir, "pending"); err != nil {
			s.log.Error("network recovery needs attention", "err", err)
		}
		cancel()
	}
	s.sampler.Start(ctx)
	s.wg.start(ctx)
	s.telemetry.start(ctx)
	s.egress.start(ctx)
}

// Stop ends the samplers, the gateway counter recorder and the egress
// monitor.
func (s *Service) Stop() {
	s.sampler.Stop()
	s.wg.stopLoop()
	s.telemetry.halt()
	s.egress.halt()
}

// Sampler is the interface counter recorder, for the traffic routes.
func (s *Service) Sampler() *Sampler { return s.sampler }

// run executes a host command and returns its combined output. It is a
// variable so tests stand recorded transcripts behind it — the parsers and
// the order of every apply are checked against what the tools print, on a
// machine that does not have them.
var run = runOnHost

// runStdin is run with standard input, for the few tools that take a secret
// that must not be an argument (a WireGuard key read by `wg pubkey`).
var runStdin = runOnHostStdin

func runOnHost(ctx context.Context, name string, args ...string) (string, error) {
	return runOnHostStdin(ctx, nil, name, args...)
}

func runOnHostStdin(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// A host namespace wrapper can fork. Finish canceling its children
	// before the caller starts recovery, so late mutations cannot race undo.
	if _, err := hostexec.RunGroup(ctx, cmd, 200*time.Millisecond); err != nil {
		if ctx.Err() != nil {
			return buf.String(), fmt.Errorf("%s: %w", name, ctx.Err())
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", &UnavailableError{Tool: name}
		}
		// Crossing into the host, a missing tool is not an exec error here
		// but nsenter failing to exec it, which exits 127 like a shell.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 127 &&
			strings.Contains(buf.String(), "failed to execute") {
			return "", &UnavailableError{Tool: name}
		}
		return buf.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(firstLines(buf.String(), 6)))
	}
	return buf.String(), nil
}

// has reports whether a tool exists here or on the host. A variable for the
// same reason run is.
var has = hostexec.Available

// firstLines keeps an error message to what a toast can hold; nft in
// particular prints the whole offending ruleset with carets under it.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n")
}
