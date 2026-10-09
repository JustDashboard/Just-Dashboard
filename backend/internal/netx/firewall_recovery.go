package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

// A change to ufw or firewalld is the host firewall's own, not the spec's:
// those tools keep their configuration and restore it at boot. What the
// dashboard adds is the same protection every network change has. Before the
// tool runs, the files it owns and whether it was running are recorded in the
// serialized journal; a synchronous failure restores them at once, and an
// interactive temporary apply is restored by the independent host helper if
// no reconnection confirms it in time. Recovery uses a closed vocabulary:
// the named files and a handful of fixed commands, never argv from a request.

// FirewallState is the host firewall before a change: which tool, whether
// it enforced, whether its unit starts at boot (enabled, disabled, or empty
// when that could not be read, which the recovery then leaves alone), and
// firewalld's default zone, whose permanent file a rule change edits.
type FirewallState struct {
	Backend string
	Enabled bool
	Unit    string
	Zone    string
}

// ufwRecoveryFiles are every file ufw's rules, defaults and switch live in;
// a reset moves them aside and writes templates in their place.
var ufwRecoveryFiles = []string{
	"/etc/ufw/user.rules", "/etc/ufw/user6.rules", "/etc/ufw/before.rules", "/etc/ufw/before6.rules",
	"/etc/ufw/after.rules", "/etc/ufw/after6.rules", "/etc/ufw/ufw.conf", "/etc/default/ufw",
}

var firewalldZoneName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// firewallRecoveryRoot prefixes the host firewall's paths; tests point it at
// a directory standing in for the host.
var firewallRecoveryRoot = ""

func firewallFiles(state FirewallState) ([]string, error) {
	var paths []string
	switch state.Backend {
	case "ufw":
		paths = append(paths, ufwRecoveryFiles...)
	case "firewalld":
		if !firewalldZoneName.MatchString(state.Zone) {
			return nil, fmt.Errorf("%q is not a firewalld zone name", state.Zone)
		}
		paths = append(paths, "/etc/firewalld/firewalld.conf", "/etc/firewalld/zones/"+state.Zone+".xml")
	default:
		return nil, fmt.Errorf("%q has no independent recovery", state.Backend)
	}
	for i := range paths {
		paths[i] = filepath.Join(firewallRecoveryRoot, paths[i])
	}
	return paths, nil
}

// firewallRecoveryCommands put the tool back in its prior state after its
// files are restored. ufw's enable starts a stopped firewall from those
// files but does nothing to a running one, so a reload follows it.
func firewallRecoveryCommands(state FirewallState) []recoveryCommand {
	switch state.Backend {
	case "ufw":
		if state.Enabled {
			return []recoveryCommand{{Tool: "ufw", Args: []string{"--force", "enable"}}, {Tool: "ufw", Args: []string{"reload"}}}
		}
		return []recoveryCommand{{Tool: "ufw", Args: []string{"--force", "disable"}}}
	case "firewalld":
		var out []recoveryCommand
		switch state.Unit {
		case "enabled":
			out = append(out, recoveryCommand{Tool: "systemctl", Args: []string{"enable", "firewalld"}})
		case "disabled":
			out = append(out, recoveryCommand{Tool: "systemctl", Args: []string{"disable", "firewalld"}})
		}
		if state.Enabled {
			return append(out, recoveryCommand{Tool: "systemctl", Args: []string{"start", "firewalld"}}, recoveryCommand{Tool: "firewall-cmd", Args: []string{"--reload"}})
		}
		return append(out, recoveryCommand{Tool: "systemctl", Args: []string{"stop", "firewalld"}})
	}
	return nil
}

// validFirewallRecovery admits exactly the vocabulary above.
func validFirewallRecovery(j *changeJournal, c recoveryCommand) bool {
	if len(c.Input) > 0 || c.AllowGone || c.AllowExists {
		return false
	}
	switch {
	case j.Firewall == "ufw" && c.Tool == "ufw":
		return slices.Equal(c.Args, []string{"--force", "enable"}) || slices.Equal(c.Args, []string{"--force", "disable"}) || slices.Equal(c.Args, []string{"reload"})
	case j.Firewall == "firewalld" && c.Tool == "systemctl":
		return len(c.Args) == 2 && c.Args[1] == "firewalld" && slices.Contains([]string{"start", "stop", "enable", "disable"}, c.Args[0])
	case j.Firewall == "firewalld" && c.Tool == "firewall-cmd":
		return slices.Equal(c.Args, []string{"--reload"})
	}
	return false
}

// validFirewallFile admits the files a firewall journal may restore.
func validFirewallFile(j *changeJournal, path string) bool {
	switch j.Firewall {
	case "ufw":
		return slices.Contains(ufwRecoveryFilePaths(), path)
	case "firewalld":
		dir, name := filepath.Split(path)
		if path == filepath.Join(firewallRecoveryRoot, "/etc/firewalld/firewalld.conf") {
			return true
		}
		zone := name[:max(0, len(name)-len(".xml"))]
		return filepath.Clean(dir) == filepath.Join(firewallRecoveryRoot, "/etc/firewalld/zones") && filepath.Ext(name) == ".xml" && firewalldZoneName.MatchString(zone)
	}
	return false
}

func ufwRecoveryFilePaths() []string {
	out := make([]string, len(ufwRecoveryFiles))
	for i, p := range ufwRecoveryFiles {
		out[i] = filepath.Join(firewallRecoveryRoot, p)
	}
	return out
}

type firewallJournalKey struct{}

// ProtectFirewallChange runs a host firewall change inside the network
// journal. The spec is untouched; the firewall's own files and state are the
// snapshot. check runs first under the same lock and refuses without opening
// a journal, so an invalid or guarded request never sets off a recovery that
// would rewrite and reload a firewall nothing changed. verify runs after
// apply, before the change is saved or left awaiting confirmation, and a
// failure of either restores the snapshot.
func (s *Service) ProtectFirewallChange(ctx context.Context, state FirewallState, check, apply, verify func(context.Context) error) error {
	paths, err := firewallFiles(state)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return err
	}
	defer unlockChange(lock)
	if err := check(ctx); err != nil {
		return err
	}
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return err
	}
	var files []recoverySnapshot
	for _, path := range paths {
		saved, err := saveNetworkFile(path)
		if err != nil {
			return fmt.Errorf("reading %s before changing the firewall: %w", path, err)
		}
		files = append(files, recoverySnapshot{Path: path, Data: saved.data, Mode: saved.perm, Exists: saved.exists})
	}
	j, err := s.prepareChange(context.WithValue(ctx, firewallJournalKey{}, state.Backend), sp, nil, nil, append(data, '\n'), firewallRecoveryCommands(state), files)
	if err != nil {
		return err
	}
	j.Persistence, j.Boot = "not_applicable", "not_applicable"
	recoverNow := func(cause error) error {
		// The tool's own files and switch go back exactly as the helper
		// would put them, with a context the canceled request cannot end.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := recoverChange(rctx, j); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	if err := apply(ctx); err != nil {
		return recoverNow(err)
	}
	j.Phase, j.Runtime = "runtime_applied", "applied"
	j.AppliedAt = time.Now().UTC()
	if err := j.save(); err != nil {
		return recoverNow(err)
	}
	if verify != nil {
		if err := verify(ctx); err != nil {
			return recoverNow(err)
		}
	}
	j.Phase = "saved"
	if j.Watchdog == "armed" {
		j.Watchdog = "completed"
	}
	if err := finishPendingConfirmation(j); err != nil {
		return recoverNow(err)
	}
	if err := j.save(); err != nil {
		return recoverNow(err)
	}
	return nil
}
