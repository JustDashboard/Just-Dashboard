package netx

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

const recoveryFile = "change.json"
const recoveryBinary = "network-recovery"
const maxRecoveryJournalBytes = 32 << 20

type recoveryErrorsKey struct{}

type recoveryExecutorKey struct{}

type recoveryExecutor func(context.Context, []byte, string, ...string) (string, error)

func executeRecovery(ctx context.Context, input []byte, name string, args ...string) (string, error) {
	if execute, ok := ctx.Value(recoveryExecutorKey{}).(recoveryExecutor); ok {
		return execute(ctx, input, name, args...)
	}
	return runStdin(ctx, input, name, args...)
}

// RecoverNetworkStandalone already runs in the host's namespaces under
// systemd. Keeping its argv there also makes disposable-namespace acceptance
// possible without a namespace wrapper escaping back to host PID 1.
func RecoverNetworkStandalone(ctx context.Context, dir, id string) error {
	return recoverNetworkStandalone(ctx, dir, id, false)
}

// RecoverNetworkBootStandalone rebuilds managed dependencies lost at reboot
// before running the same targeted undo as the independent timer.
func RecoverNetworkBootStandalone(ctx context.Context, dir, id string) error {
	return recoverNetworkStandalone(ctx, dir, id, true)
}

func recoverNetworkStandalone(ctx context.Context, dir, id string, boot bool) error {
	execute := recoveryExecutor(func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if input != nil {
			cmd.Stdin = bytes.NewReader(input)
		}
		_, err := hostexec.RunGroup(ctx, cmd, 200*time.Millisecond)
		if err != nil {
			return out.String(), fmt.Errorf("%s: %s: %w", name, firstLines(out.String(), 6), err)
		}
		return out.String(), nil
	})
	return recoverNetwork(context.WithValue(ctx, recoveryExecutorKey{}, execute), dir, id, boot)
}

func recordRecoveryError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if failures, ok := ctx.Value(recoveryErrorsKey{}).(*[]string); ok {
		*failures = append(*failures, err.Error())
	}
}

// ChangeStatus contains evidence about phases actually reached. File snapshots
// and recovery argv remain in the root-only journal and never reach the API.
type ChangeStatus struct {
	ID             string    `json:"id"`
	Phase          string    `json:"phase"`
	Generation     string    `json:"generation"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Watchdog       string    `json:"watchdog"`
	Runtime        string    `json:"runtime"`
	Persistence    string    `json:"persistence"`
	Boot           string    `json:"boot"`
	RecoveryErrors []string  `json:"recoveryErrors,omitempty"`
	OwnerUserID    int64     `json:"ownerUserId,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt,omitzero"`
	AppliedAt      time.Time `json:"appliedAt,omitzero"`
	VerifiedAt     time.Time `json:"verifiedAt,omitzero"`
}

type recoverySnapshot struct {
	Path              string      `json:"path"`
	Data              []byte      `json:"data,omitempty"`
	Mode              os.FileMode `json:"mode"`
	Exists            bool        `json:"exists"`
	BeforeIdentity    string      `json:"beforeIdentity,omitempty"`
	CandidateIdentity string      `json:"candidateIdentity,omitempty"`
	CandidateSHA256   string      `json:"candidateSha256,omitempty"`
	CandidateMode     os.FileMode `json:"candidateMode,omitempty"`
	RestoredIdentity  string      `json:"restoredIdentity,omitempty"`
}

type recoveryCommand struct {
	Tool        string   `json:"tool"`
	Args        []string `json:"args"`
	Input       []byte   `json:"input,omitempty"`
	AllowGone   bool     `json:"allowGone,omitempty"`
	AllowExists bool     `json:"allowExists,omitempty"`
}

type changeJournal struct {
	ChangeStatus
	Paths                 Paths              `json:"paths"`
	Files                 []recoverySnapshot `json:"files"`
	Commands              []recoveryCommand  `json:"commands"`
	BootDependencies      []recoveryCommand  `json:"bootDependencies,omitempty"`
	SelectedDriftRepair   bool               `json:"selectedDriftRepair,omitempty"`
	VerificationDigest    string             `json:"verificationDigest,omitempty"`
	VerificationSession   string             `json:"verificationSession,omitempty"`
	VerificationTransport string             `json:"verificationTransport,omitempty"`
}

// lockChange also serializes the independent host process against an apply.
// A backend death releases flock, even when its in-memory mutex is lost.
func lockChange(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".change.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func unlockChange(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

func readChange(dir string) (*changeJournal, error) {
	f, err := os.Open(filepath.Join(dir, recoveryFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("the network recovery journal must be a private regular file")
	}
	if info.Size() > maxRecoveryJournalBytes {
		return nil, errors.New("the network recovery journal exceeds its size limit")
	}
	var j changeJournal
	dec := json.NewDecoder(io.LimitReader(f, maxRecoveryJournalBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return nil, fmt.Errorf("reading the network recovery journal: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("the network recovery journal has trailing data")
	}
	if j.Paths.Dir != dir || j.ID == "" || len(j.Generation) != 64 {
		return nil, fmt.Errorf("the network recovery journal does not match its directory")
	}
	return &j, nil
}

func (j *changeJournal) save() error {
	j.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if len(b)+1 > maxRecoveryJournalBytes {
		return fmt.Errorf("the network recovery snapshot exceeds its %d MB limit; nothing further can be applied safely", maxRecoveryJournalBytes>>20)
	}
	return writeFileAtomic(filepath.Join(j.Paths.Dir, recoveryFile), append(b, '\n'), 0o600)
}

func changeTerminal(phase string) bool {
	return phase == "saved" || phase == "confirmed" || phase == "recovered" || phase == "boot_degraded"
}

func (s *Service) prepareChange(ctx context.Context, sp *Spec, paths []string, previous map[string]savedNetworkFile, spec []byte, extra []recoveryCommand, extraFiles []recoverySnapshot) (*changeJournal, error) {
	if prior, err := readChange(s.paths.Dir); err == nil {
		if !changeTerminal(prior.Phase) {
			return nil, &ReadOnlyError{Reason: "An earlier network change needs recovery; its journal has been preserved."}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if pendingOwner(ctx) > 0 && (!s.independentRecovery || !has("systemctl") || !has("systemd-run")) {
		return nil, &ConfirmationError{"Pending apply requires an independent host recovery watchdog; nothing was applied."}
	}

	old, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	selectedDrift, _ := ctx.Value(selectedDriftRecoveryKey{}).(bool)
	var commands, dependencies []recoveryCommand
	if !selectedDrift {
		commands, err = s.recoveryPlan(ctx, old, sp)
		if err != nil {
			return nil, fmt.Errorf("preparing independent network recovery: %w", err)
		}
		dependencies, err = bootRecoveryDependencies(old)
		if err != nil {
			return nil, fmt.Errorf("preparing managed dependencies for boot recovery: %w", err)
		}
	}
	commands = append(commands, extra...)
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(spec)
	j := &changeJournal{Paths: s.paths, Commands: commands, BootDependencies: dependencies, SelectedDriftRepair: selectedDrift, ChangeStatus: ChangeStatus{
		ID: hex.EncodeToString(id[:]), Phase: "prepared", Generation: hex.EncodeToString(sum[:]),
		Watchdog: "unsupported", Runtime: "not_applied", Persistence: "not_written", Boot: "not_verified",
	}}
	if selectedDrift {
		j.Persistence, j.Boot = "not_applicable", "not_applicable"
	}
	for _, path := range paths {
		f := previous[path]
		j.Files = append(j.Files, recoverySnapshot{Path: path, Data: f.data, Mode: f.perm, Exists: f.exists})
	}
	j.Files = append(j.Files, extraFiles...)
	if s.independentRecovery && has("systemctl") {
		if err := s.installRecoveryBinary(ctx); err != nil {
			return nil, err
		}
		j.Watchdog = "armed"
	}
	if owner := pendingOwner(ctx); owner > 0 {
		if j.Watchdog != "armed" {
			return nil, &ConfirmationError{"Pending apply requires an armed independent recovery watchdog; nothing was applied."}
		}
		j.OwnerUserID = owner
		j.ExpiresAt = time.Now().UTC().Add(confirmationWindow)
	}

	if err := j.save(); err != nil {
		return nil, err
	}
	if j.Watchdog == "armed" {
		unit := "just-dashboard-network-recover-" + j.ID
		if _, err := run(ctx, "systemd-run", "--collect", "--unit="+unit, "--on-active=90s", "--timer-property=AccuracySec=1s", "--timer-property=RemainAfterElapse=no", "--property=Type=oneshot", "--", filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID); err != nil {
			j.Phase, j.Watchdog = "recovered", "failed_to_arm"
			if saveErr := j.save(); saveErr != nil {
				return nil, errors.Join(err, saveErr)
			}
			return nil, fmt.Errorf("the independent recovery watchdog could not be armed; nothing was applied: %w", err)
		}
	}
	return j, nil
}

func (s *Service) installRecoveryBinary(ctx context.Context) error {
	path := filepath.Join(s.paths.Dir, recoveryBinary)
	if !s.recoveryInstalled {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(executable)
		if err != nil {
			return err
		}
		if changed(path, b) {
			if err := writeFileAtomic(path, b, 0o700); err != nil {
				return fmt.Errorf("installing the independent network recovery executable: %w", err)
			}
		}
	}
	// Test execution in the host mount namespace before trusting it. The
	// shipped binary is static; a local dynamic build may not run there.
	if _, err := run(ctx, path, "--network-recovery-check"); err != nil {
		return fmt.Errorf("the recovery executable cannot run on this host: %w", err)
	}
	// A transient timer is lost at reboot. Enable a separate recovery owner
	// before touching the kernel, including on the very first managed change.
	unitName := "just-dashboard-network-recovery.service"
	unitPath := filepath.Join(filepath.Dir(s.paths.Unit), unitName)
	unit := renderRecoveryUnit(s.paths)
	if data, err := os.ReadFile(unitPath); err == nil && !strings.HasPrefix(string(data), generatedHeader) {
		return fmt.Errorf("the independent recovery unit is owned by another writer")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if changed(unitPath, []byte(unit)) {
		if err := writeFileAtomic(unitPath, []byte(unit), 0o644); err != nil {
			return err
		}
		if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
	}
	if _, err := run(ctx, "systemctl", "enable", unitName); err != nil {
		return fmt.Errorf("enabling interrupted-change recovery at boot: %w", err)
	}
	s.recoveryInstalled = true
	return nil
}

// RecoverNetwork is run by the host's systemd timer. It starts
// without the API, database, credentials or dashboard container. An old timer
// cannot recover a later change because the random journal ID must match.
func RecoverNetwork(ctx context.Context, dir, id string) error {
	return recoverNetwork(ctx, dir, id, false)
}

// RecoverNetworkBoot runs after native network owners have restored their
// devices, but before the dashboard's ordinary boot restoration begins.
func RecoverNetworkBoot(ctx context.Context, dir, id string) error {
	return recoverNetwork(ctx, dir, id, true)
}

func recoverNetwork(ctx context.Context, dir, id string, boot bool) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("network recovery needs an absolute, clean directory")
	}
	lock, err := lockChange(dir)
	if err != nil {
		return err
	}
	defer unlockChange(lock)
	j, err := readChange(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if id != "pending" && id != j.ID || changeTerminal(j.Phase) {
		return nil
	}
	return recoverChangeWithDependencies(ctx, j, boot)
}

func recoverChange(ctx context.Context, j *changeJournal) error {
	return recoverChangeWithDependencies(ctx, j, false)
}

func recoverChangeWithDependencies(ctx context.Context, j *changeJournal, boot bool) error {
	allowed := map[string]bool{
		filepath.Join(j.Paths.Dir, linksFile): true, filepath.Join(j.Paths.Dir, rules6File): true,
		filepath.Join(j.Paths.Dir, shapingFile): true, filepath.Join(j.Paths.Dir, gatewayFile): true,
		filepath.Join(j.Paths.Dir, "spec.json"): true, j.Paths.Sysctl: true, j.Paths.Unit: true,
	}
	if j.SelectedDriftRepair {
		delete(allowed, filepath.Join(j.Paths.Dir, "spec.json"))
		if len(j.BootDependencies) > 0 {
			return fmt.Errorf("selected repair recovery must not replay unselected boot dependencies")
		}
	}
	for _, f := range j.Files {
		if (!allowed[f.Path] && (j.SelectedDriftRepair || !recoveryBlocklistPath(j.Paths.Dir, f.Path))) || f.Path == "" {
			return fmt.Errorf("a recovery snapshot names an unexpected file")
		}
		if recoveryBlocklistPath(j.Paths.Dir, f.Path) {
			if err := validateRecoveryBlocklistPath(j.Paths.Dir, f.Path); err != nil {
				return err
			}
		}
		if j.SelectedDriftRepair && !validSelectedFileSnapshot(f) {
			return fmt.Errorf("selected repair recovery has incomplete file identity evidence")
		}
	}
	for _, c := range j.Commands {
		if j.SelectedDriftRepair && !validSelectedAdmissionUndo(c) {
			return fmt.Errorf("selected repair recovery must contain only canonical owned admission undo")
		}
		switch c.Tool {
		case "ip", "tc", "nft", "sysctl", "iptables", "ip6tables":
		default:
			return fmt.Errorf("refused unexpected recovery tool %s", c.Tool)
		}
	}
	for _, c := range j.BootDependencies {
		if c.Tool != "ip" || !validBootRecoveryCommand(c.Args) {
			return fmt.Errorf("refused unexpected boot dependency command")
		}
	}
	j.Phase, j.RecoveryErrors = "recovering", nil
	if err := j.save(); err != nil {
		return err
	}
	reload := !j.SelectedDriftRepair
	for i := len(j.Files) - 1; i >= 0; i-- {
		f := j.Files[i]
		var err error
		if j.SelectedDriftRepair {
			err = restoreSelectedDriftFile(j, i)
		} else {
			err = (savedNetworkFile{data: f.Data, perm: f.Mode, exists: f.Exists}).restore(f.Path)
		}
		if err != nil {
			j.RecoveryErrors = append(j.RecoveryErrors, "restore "+f.Path+": "+err.Error())
		} else if f.Path == j.Paths.Unit {
			reload = true
		}
	}
	commands := j.Commands
	if boot {
		commands = append(append([]recoveryCommand(nil), j.BootDependencies...), commands...)
	}
	for _, c := range commands {
		out, err := executeRecovery(ctx, c.Input, c.Tool, c.Args...)
		if err != nil && !(c.AllowGone && recoveryExpectedAbsence(c.Tool, out, err)) && !(c.AllowExists && recoveryExpectedExistence(c.Tool, c.Args, out, err)) {
			j.RecoveryErrors = append(j.RecoveryErrors, c.Tool+" "+strings.Join(c.Args, " ")+": "+err.Error())
		}
	}
	if reload && has("systemctl") {
		if _, err := executeRecovery(ctx, nil, "systemctl", "daemon-reload"); err != nil {
			j.RecoveryErrors = append(j.RecoveryErrors, "reload restored boot unit: "+err.Error())
		}
	}
	j.Phase, j.Persistence, j.Runtime = "recovered", "restored", "restored"
	if len(j.Files) == 0 && j.Boot == "not_applicable" {
		j.Persistence = "not_applicable"
	}
	if j.Watchdog == "armed" {
		j.Watchdog = "recovered"
	}
	if len(j.RecoveryErrors) > 0 {
		j.Phase, j.Persistence, j.Runtime = "degraded", "unknown", "unknown"
		if len(j.Files) == 0 && j.Boot == "not_applicable" {
			j.Persistence = "not_applicable"
		}
	}
	if err := j.save(); err != nil {
		return err
	}
	if len(j.RecoveryErrors) > 0 {
		return fmt.Errorf("network recovery needs attention: %s", strings.Join(j.RecoveryErrors, "; "))
	}
	return nil
}

func validSelectedAdmissionUndo(c recoveryCommand) bool {
	if c.Tool != "iptables" && c.Tool != "ip6tables" || len(c.Args) < 2 || len(c.Input) > 0 || !slices.Contains(admissionChains, c.Args[1]) {
		return false
	}
	switch c.Args[0] {
	case "-D":
		return slices.Equal(c.Args[2:], admissionRule()) && !c.AllowExists
	case "-I":
		if len(c.Args) < 3 {
			return false
		}
		pos, err := strconv.Atoi(c.Args[2])
		return err == nil && pos > 0 && slices.Equal(c.Args[3:], admissionRule()) && !c.AllowGone && !c.AllowExists
	}
	return false
}

func recoveryExpectedAbsence(tool, out string, err error) bool {
	if tool != "iptables" && tool != "ip6tables" {
		return isGone(err)
	}
	message := out + err.Error()
	return strings.Contains(message, "Bad rule") || strings.Contains(message, "matching rule") || strings.Contains(message, "No chain/target/match")
}

func recoveryBlocklistPath(dir, path string) bool {
	if filepath.Dir(path) != filepath.Join(dir, "lists") {
		return false
	}
	name := filepath.Base(path)
	id, err := strconv.Atoi(strings.TrimSuffix(name, ".txt"))
	return err == nil && id > 0 && name == strconv.Itoa(id)+".txt"
}

func validateRecoveryBlocklistPath(dir, path string) error {
	if !recoveryBlocklistPath(dir, path) {
		return errors.New("a cache recovery snapshot must name an owned numeric list file")
	}
	for _, entry := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(entry)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("a blocklist cache recovery path must not be a symlink")
		}
	}
	return nil
}
