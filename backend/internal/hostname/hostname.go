// Package hostname renames the server: the name its kernel answers to, the
// one it boots with, and the line of /etc/hosts that lets it find itself.
//
// A rename changes nothing but that. What the server is called on its tailnet
// is held where it was, because the dashboard's own address and every address
// published on the tailnet are spelt with it, and Tailscale would otherwise
// follow the new name and take them all away at once.
package hostname

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// maxLength is the kernel's limit on a host name (HOST_NAME_MAX).
const maxLength = 64

// commandTimeout bounds each program the rename runs. None of them does more
// than a local write or one call to a daemon on this machine.
const commandTimeout = 15 * time.Second

// State is what the rename form opens on.
type State struct {
	Hostname string `json:"hostname"`
	// TailscaleName is what the server is called on its tailnet, when it is
	// on one. A rename leaves it as it is.
	TailscaleName string `json:"tailscaleName,omitempty"`
}

// Result is a finished rename.
type Result struct {
	Hostname string `json:"hostname"`
	Previous string `json:"previous"`
	// TailscaleName is the tailnet name that was held, when there is one.
	TailscaleName string `json:"tailscaleName,omitempty"`
	// Warnings are what could not be finished around a name that is set.
	Warnings []string `json:"warnings,omitempty"`
}

// InvalidError is a name the server cannot take.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return e.Reason }

// Service renames this server.
type Service struct {
	mu sync.Mutex
	// run runs a program on the host and returns what it printed.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// has reports whether the host has a program.
	has func(name string) bool
	// current is the name the kernel answers to now.
	current func() (string, error)
	// root is where the host's /etc is reached. It is /host in the shipped
	// stack, because Docker mounts its own hosts and hostname files over
	// the container's, and a write to those never reaches the server.
	root string
	// onServer runs a write of one of the server's files by a path that can
	// replace it: hostexec.OnServerPath, since the files Docker mounts over
	// are busy under /host too. Nil writes under root.
	onServer func(real, mirrored string, write func(path string) error) error
}

// New returns the service for the server this process manages.
func New() *Service {
	root := ""
	if st, err := os.Stat("/host/etc/hosts"); err == nil && st.Mode().IsRegular() {
		root = "/host"
	}
	return &Service{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return hostexec.CommandOnHost(ctx, name, args...).CombinedOutput()
		},
		has:      hostexec.AvailableOnHost,
		current:  hostexec.Hostname,
		root:     root,
		onServer: hostexec.OnServerPath,
	}
}

// Normalize returns name as the server will carry it, or why it cannot: a
// host name is lower-case letters, digits and hyphens, in labels a dot may
// join, and the kernel holds 64 characters of it.
func Normalize(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "":
		return "", &InvalidError{"Give the server a name."}
	case len(name) > maxLength:
		return "", &InvalidError{fmt.Sprintf("A server's name is at most %d characters.", maxLength)}
	case name == "localhost" || strings.HasPrefix(name, "localhost."):
		return "", &InvalidError{"localhost already means this machine to every program on it; pick another name."}
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return "", &InvalidError{"A name cannot start or end with a dot, or have two in a row."}
		}
		if len(label) > 63 {
			return "", &InvalidError{"Each part of a name between dots is at most 63 characters."}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", &InvalidError{"A name cannot start or end with a hyphen."}
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", &InvalidError{"A name holds only letters, digits and hyphens."}
			}
		}
	}
	return name, nil
}

// Current is the server's name and what it is called on its tailnet.
func (s *Service) Current(ctx context.Context) (*State, error) {
	name, err := s.current()
	if err != nil {
		return nil, err
	}
	state := &State{Hostname: name}
	if node := s.tailscale(ctx); node != nil {
		state.TailscaleName = node.name
	}
	return state, nil
}

// Rename gives the server a new name: now, and for every start after this
// one. Asking for the name it already has changes nothing.
func (s *Service) Rename(ctx context.Context, name string) (*Result, error) {
	name, err := Normalize(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	previous, err := s.current()
	if err != nil {
		return nil, fmt.Errorf("read the server's name: %w", err)
	}
	result := &Result{Hostname: name, Previous: previous}
	node := s.tailscale(ctx)
	if node != nil {
		result.TailscaleName = node.name
	}
	if previous == name {
		return result, nil
	}

	// Before the name moves, not after: Tailscale reports a new host name
	// to its control server within moments of the kernel taking one.
	if node != nil {
		if err := s.holdTailscaleName(ctx, node); err != nil {
			if node.running {
				return nil, fmt.Errorf("the server was not renamed, because its Tailscale name could not be held where it is and this dashboard's address may depend on it: %w", err)
			}
			result.Warnings = append(result.Warnings, "Tailscale is not running, so its name for this server could not be held: "+err.Error())
		}
	}

	if err := s.set(ctx, name); err != nil {
		return nil, err
	}
	now, err := s.current()
	if err != nil {
		return nil, fmt.Errorf("read the server's name back: %w", err)
	}
	if now != name {
		return nil, fmt.Errorf("the server still answers to %q", now)
	}

	result.Warnings = append(result.Warnings, s.recordInHosts(name)...)
	return result, nil
}

// set changes the kernel's name and the one the server boots with.
// hostnamectl does both wherever systemd keeps the name; a server without it,
// or one whose hostnamed will not answer, is given the two by hand.
func (s *Service) set(ctx context.Context, name string) error {
	var managed error
	if s.has("hostnamectl") {
		if managed = s.command(ctx, "hostnamectl", "set-hostname", name); managed == nil {
			return nil
		}
	}
	before, existed, err := s.swap("/etc/hostname", []byte(name+"\n"))
	if err != nil {
		return errors.Join(managed, fmt.Errorf("write %s: %w", "/etc/hostname", err))
	}
	if err := s.command(ctx, "hostname", name); err != nil {
		// The kernel kept its name, so the server must also boot with the
		// one it had: a refused rename leaves the machine as it found it.
		if undo := s.restore("/etc/hostname", before, existed); undo != nil {
			return errors.Join(managed, err, fmt.Errorf("put %s back: %w", "/etc/hostname", undo))
		}
		return errors.Join(managed, err)
	}
	return nil
}

// command runs one program on the host and folds what it printed into the
// error, since "exit status 1" names nothing.
func (s *Service) command(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := s.run(ctx, name, args...)
	if err == nil {
		return nil
	}
	var missing *exec.Error
	if errors.As(err, &missing) {
		return err
	}
	if said := strings.TrimSpace(string(out)); said != "" {
		return fmt.Errorf("%s: %s", name, said)
	}
	return fmt.Errorf("%s: %w", name, err)
}

// tailscaleNode is this server as its tailnet knows it.
type tailscaleNode struct {
	// name is the first label of its MagicDNS name: what every tailnet
	// address of this server begins with.
	name    string
	running bool
}

// tailscale reads the server's tailnet name, or nil when it has none: no
// Tailscale, no sign-in, or a daemon that does not answer.
func (s *Service) tailscale(ctx context.Context) *tailscaleNode {
	if !s.has("tailscale") {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := s.run(ctx, "tailscale", "status", "--json")
	if err != nil {
		return nil
	}
	var status struct {
		BackendState string
		Self         *struct {
			HostName string
			DNSName  string
		}
	}
	// The daemon's warnings can precede the document on the same stream.
	if i := strings.IndexByte(string(out), '{'); i > 0 {
		out = out[i:]
	}
	if json.Unmarshal(out, &status) != nil || status.Self == nil {
		return nil
	}
	name, _, _ := strings.Cut(strings.TrimSuffix(status.Self.DNSName, "."), ".")
	if name == "" {
		name = status.Self.HostName
	}
	if name == "" {
		return nil
	}
	return &tailscaleNode{name: strings.ToLower(name), running: status.BackendState == "Running"}
}

// holdTailscaleName tells Tailscale to go on asking for the name the server
// has on its tailnet instead of following the host's. It is the name the node
// already carries, so nothing on the tailnet changes.
func (s *Service) holdTailscaleName(ctx context.Context, node *tailscaleNode) error {
	return s.command(ctx, "tailscale", "set", "--hostname="+node.name)
}
