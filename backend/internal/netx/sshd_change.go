package netx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// An SSH configuration change can remove the way back into the machine, which
// is exactly what the network journal's temporary apply exists for. netsec
// keeps the plan, the lockout guards, the write, `sshd -t` and the reload;
// this file enrols that apply in the same journal, so the same independent
// host process restores the previous files and reloads sshd when nobody
// returns a fresh dashboard response within the deadline.
//
// The enrolment is deliberately narrower than a network change: its files are
// sshd's main file, the dashboard's own drop-in and the socket drop-in, and
// its undo is a closed vocabulary of systemctl calls. A journal naming any
// other file or command is refused before recovery touches anything.

const sshSubsystem = "sshd"

const (
	sshManagedDropIn    = "99-just-dashboard.conf"
	sshSocketDropIn     = "10-just-dashboard.conf"
	sshRecoveryReload   = "reload"
	sshRecoverySocket   = "socket"
	sshRecoveryDeadline = 30 * time.Second
)

var sshSocketUnits = []string{"ssh.socket", "sshd.socket"}

// sshServiceUnits are tried in order, as netsec's reload does: ssh on the
// Debian family, sshd elsewhere.
var sshServiceUnits = []string{"ssh", "sshd"}

// SSHFile is one file a pending SSH apply will write, with the bytes it will
// write. The candidate identifies the generation; the journal keeps only the
// prior contents.
type SSHFile struct {
	Path      string
	Candidate []byte
}

// SSHChange is a begun SSH apply. ID and ExpiresAt are what the response
// headers carry, as a network change's do.
type SSHChange struct {
	ID        string
	ExpiresAt time.Time
}

// sshJournalPaths are the only files an SSH journal may snapshot.
func sshJournalPaths(p Paths) map[string]bool {
	allowed := map[string]bool{}
	if p.SSH == "" || !filepath.IsAbs(p.SSH) {
		return allowed
	}
	allowed[filepath.Join(p.SSH, "sshd_config")] = true
	allowed[filepath.Join(p.SSH, "sshd_config.d", sshManagedDropIn)] = true
	if p.Unit != "" {
		for _, unit := range sshSocketUnits {
			allowed[filepath.Join(filepath.Dir(p.Unit), unit+".d", sshSocketDropIn)] = true
		}
	}
	return allowed
}

func validSSHRecoveryCommand(c recoveryCommand) bool {
	if c.Tool != sshSubsystem || len(c.Input) > 0 || c.AllowGone || c.AllowExists {
		return false
	}
	switch {
	case len(c.Args) == 1 && c.Args[0] == sshRecoveryReload:
		return true
	case len(c.Args) == 2 && c.Args[0] == sshRecoverySocket:
		return slices.Contains(sshSocketUnits, c.Args[1])
	}
	return false
}

// BeginSSHChange snapshots the files an SSH apply will write and arms the
// independent watchdog before any of them is written. It accepts only a
// pending apply owned by an interactive administrator: an SSH change has no
// network spec to save, so an immediate apply has nothing to journal.
func (s *Service) BeginSSHChange(ctx context.Context, files []SSHFile, socketUnit string) (*SSHChange, error) {
	owner := pendingOwner(ctx)
	if owner <= 0 {
		return nil, &ConfirmationError{"Only a pending apply from an administrator's session can enrol an SSH change."}
	}
	if len(files) == 0 {
		return nil, errors.New("an SSH change names no file")
	}
	allowed := sshJournalPaths(s.paths)
	commands := []recoveryCommand{}
	if socketUnit != "" {
		if !slices.Contains(sshSocketUnits, socketUnit) {
			return nil, fmt.Errorf("refused unexpected SSH socket unit %s", socketUnit)
		}
		commands = append(commands, recoveryCommand{Tool: sshSubsystem, Args: []string{sshRecoverySocket, socketUnit}})
	}
	commands = append(commands, recoveryCommand{Tool: sshSubsystem, Args: []string{sshRecoveryReload}})
	sum := sha256.New()
	seen := map[string]bool{}
	for _, f := range files {
		if !allowed[f.Path] || seen[f.Path] {
			return nil, fmt.Errorf("an SSH change may journal only sshd's own configuration files, not %s", f.Path)
		}
		seen[f.Path] = true
		sum.Write([]byte(f.Path))
		sum.Write([]byte{0})
		sum.Write(f.Candidate)
		sum.Write([]byte{0})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	if err := finishPriorChange(ctx, s.paths.Dir); err != nil {
		return nil, err
	}
	if !s.independentRecovery || !has("systemctl") || !has("systemd-run") {
		return nil, &ConfirmationError{"Pending apply requires an independent host recovery watchdog; nothing was applied."}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	j := &changeJournal{Paths: s.paths, Commands: commands, ChangeStatus: ChangeStatus{
		ID: hex.EncodeToString(id[:]), Phase: "prepared", Generation: hex.EncodeToString(sum.Sum(nil)),
		Watchdog: "unsupported", Runtime: "not_applied", Persistence: "not_written", Boot: "not_applicable",
		Subsystem: sshSubsystem,
	}}
	for _, f := range files {
		saved, err := saveNetworkFile(f.Path)
		if err != nil {
			return nil, fmt.Errorf("snapshotting %s: %w", f.Path, err)
		}
		j.Files = append(j.Files, recoverySnapshot{Path: f.Path, Data: saved.data, Mode: saved.perm, Exists: saved.exists})
	}
	if err := s.installRecoveryBinary(ctx); err != nil {
		return nil, err
	}
	j.Watchdog = "armed"
	j.OwnerUserID = owner
	j.ExpiresAt = time.Now().UTC().Add(confirmationWindow)
	if err := j.save(); err != nil {
		return nil, err
	}
	unit := "just-dashboard-network-recover-" + j.ID
	if _, err := run(ctx, "systemd-run", "--collect", "--unit="+unit, "--on-active=90s", "--timer-property=AccuracySec=1s", "--timer-property=RemainAfterElapse=no", "--property=Type=oneshot", "--", filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID); err != nil {
		j.Phase, j.Watchdog = "recovered", "failed_to_arm"
		if saveErr := j.save(); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, fmt.Errorf("the independent recovery watchdog could not be armed; nothing was applied: %w", err)
	}
	return &SSHChange{ID: j.ID, ExpiresAt: j.ExpiresAt}, nil
}

// FinishSSHChange records how the apply ended. A successful apply waits for
// the owner's confirmation; a failed one, or one that crossed its deadline,
// is restored now rather than when the timer fires.
func (s *Service) FinishSSHChange(ctx context.Context, change *SSHChange, applyErr error) (*ChangeStatus, error) {
	if change == nil {
		return nil, errors.New("no SSH change was begun")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	j, err := readChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	if j.ID != change.ID || j.Subsystem != sshSubsystem {
		return nil, errors.New("the SSH change journal was replaced before the apply finished")
	}
	if changeTerminal(j.Phase) || j.Phase == "degraded" {
		// The timer or an explicit recovery already decided this change.
		return &j.ChangeStatus, applyErr
	}
	if applyErr == nil {
		j.Runtime, j.Persistence = "applied", "written"
		if err := finishPendingConfirmation(j); err != nil {
			applyErr = err
		} else if err := j.save(); err != nil {
			applyErr = err
		} else {
			return &j.ChangeStatus, nil
		}
	}
	recoverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	if err := recoverChange(recoverCtx, j); err != nil {
		return &j.ChangeStatus, errors.Join(applyErr, err)
	}
	return &j.ChangeStatus, applyErr
}

// recoverSSHJournal restores sshd's files and asks systemd to pick them up.
// At boot nothing waits on sshd's own jobs: the recovery unit is not ordered
// against it, and a blocking restart there could wait on itself.
func recoverSSHJournal(ctx context.Context, j *changeJournal, boot bool) error {
	allowed := sshJournalPaths(j.Paths)
	if len(j.BootDependencies) > 0 || j.SelectedDriftRepair {
		return errors.New("refused mixed SSH recovery ownership")
	}
	for _, f := range j.Files {
		if !allowed[f.Path] {
			return errors.New("an SSH recovery snapshot names an unexpected file")
		}
	}
	if len(j.Commands) == 0 {
		return errors.New("an SSH recovery journal has no reload")
	}
	for _, c := range j.Commands {
		if !validSSHRecoveryCommand(c) {
			return fmt.Errorf("refused unexpected SSH recovery command %s %s", c.Tool, strings.Join(c.Args, " "))
		}
	}
	j.Phase, j.RecoveryErrors = "recovering", nil
	if err := j.save(); err != nil {
		return err
	}
	for i := len(j.Files) - 1; i >= 0; i-- {
		f := j.Files[i]
		if err := (savedNetworkFile{data: f.Data, perm: f.Mode, exists: f.Exists}).restore(f.Path); err != nil {
			j.RecoveryErrors = append(j.RecoveryErrors, "restore "+f.Path+": "+err.Error())
		}
	}
	for _, c := range j.Commands {
		if err := runSSHRecovery(ctx, c, boot); err != nil {
			j.RecoveryErrors = append(j.RecoveryErrors, err.Error())
		}
	}
	j.Phase, j.Runtime, j.Persistence = "recovered", "restored", "restored"
	if j.Watchdog == "armed" {
		j.Watchdog = "recovered"
	}
	if len(j.RecoveryErrors) > 0 {
		j.Phase, j.Runtime, j.Persistence = "degraded", "unknown", "unknown"
	}
	if err := j.save(); err != nil {
		return err
	}
	if len(j.RecoveryErrors) > 0 {
		return fmt.Errorf("SSH recovery needs attention: %s", strings.Join(j.RecoveryErrors, "; "))
	}
	return nil
}

func runSSHRecovery(ctx context.Context, c recoveryCommand, boot bool) error {
	ctx, cancel := context.WithTimeout(ctx, sshRecoveryDeadline)
	defer cancel()
	systemctl := func(args ...string) error {
		if boot {
			args = append([]string{"--no-block"}, args...)
		}
		_, err := executeRecovery(ctx, nil, "systemctl", args...)
		return err
	}
	switch c.Args[0] {
	case sshRecoverySocket:
		if _, err := executeRecovery(ctx, nil, "systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("reload restored socket drop-in: %w", err)
		}
		if err := systemctl("restart", c.Args[1]); err != nil {
			return fmt.Errorf("restart %s: %w", c.Args[1], err)
		}
		return nil
	default:
		verb := "reload-or-restart"
		if boot {
			// A daemon that has not started yet reads the restored files when
			// it does; only a running one needs telling.
			verb = "try-reload-or-restart"
		}
		var failures []string
		for _, unit := range sshServiceUnits {
			err := systemctl(verb, unit)
			if err == nil {
				return nil
			}
			failures = append(failures, err.Error())
		}
		return fmt.Errorf("reload sshd: %s", strings.Join(failures, "; "))
	}
}
