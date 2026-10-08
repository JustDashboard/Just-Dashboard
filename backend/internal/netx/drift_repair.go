package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxDriftReviewBytes = 512 << 10

type selectedDriftRecoveryKey struct{}

type DriftRepairSelection struct {
	ID          string `json:"id"`
	ReviewToken string `json:"reviewToken"`
}

type DriftRepairRequest struct {
	Generation string                 `json:"generation"`
	Selections []DriftRepairSelection `json:"selections"`
}

// These six files have durable snapshot coverage. Recovery infrastructure,
// boot enablement and managed kernel objects remain advice until their own
// selected undo and verification contracts are implemented.
func (s *Service) driftRenderFiles(sp *Spec) rendered {
	gateway, err := s.driftGatewayRender(sp)
	files := rendered{
		filepath.Join(s.paths.Dir, linksFile):   []byte(renderLinks(sp)),
		filepath.Join(s.paths.Dir, rules6File):  []byte(renderIPv6Rules(sp)),
		filepath.Join(s.paths.Dir, shapingFile): []byte(renderShaping(sp)),
		s.paths.Sysctl:                          []byte(renderSysctl(sp)),
		s.paths.Unit:                            []byte(renderUnit(s.paths, needsAdmission(sp), s.independentRecovery)),
	}
	if err == nil {
		files[filepath.Join(s.paths.Dir, gatewayFile)] = []byte(gateway)
	}
	return files
}

// The inspection does not execute a helper or install a unit. Apply will run
// the existing independent recovery preflight and arm its timer before effects.
func (s *Service) driftRecoveryReady(ctx context.Context) string {
	if !s.independentRecovery || !has("systemctl") || !has("systemd-run") {
		return "An independent host recovery watchdog is required for selected repairs."
	}
	unit := filepath.Join(filepath.Dir(s.paths.Unit), "just-dashboard-network-recovery.service")
	helper := filepath.Join(s.paths.Dir, recoveryBinary)
	for _, path := range []string{unit, helper} {
		if err := driftNoSymlinkPath(path); err != nil {
			return "Recovery infrastructure cannot be established: " + err.Error()
		}
	}
	data, err := readDriftFile(unit)
	if err != nil || string(data) != renderRecoveryUnit(s.paths) {
		return "The independent recovery unit must already match its owned render."
	}
	out, err := run(ctx, "systemctl", "show", "just-dashboard-network-recovery.service", "--property=LoadState,FragmentPath,DropInPaths,NeedDaemonReload,UnitFileState")
	if err != nil {
		return "The loaded independent recovery unit cannot be inspected."
	}
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			props[key] = strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"LoadState", "FragmentPath", "DropInPaths", "NeedDaemonReload", "UnitFileState"} {
		if _, ok := props[key]; !ok {
			return "The loaded independent recovery unit evidence is incomplete."
		}
	}
	if props["LoadState"] != "loaded" || props["FragmentPath"] != unit || props["DropInPaths"] != "" || props["NeedDaemonReload"] != "no" || props["UnitFileState"] != "enabled" {
		return "The loaded independent recovery unit must use its owned fragment without overrides and be enabled persistently."
	}
	data, err = readDriftFile(helper)
	if err != nil {
		return "The independent recovery helper must already be installed."
	}
	info, err := os.Lstat(helper)
	if err != nil || info.Mode().Perm() != 0o700 {
		return "The independent recovery helper must retain its private executable mode."
	}
	executable, err := os.Executable()
	if err != nil {
		return "The running backend executable cannot be identified."
	}
	current, err := os.ReadFile(executable)
	if err != nil || digestBytes(data) != digestBytes(current) {
		return "The independent recovery helper's current backend ownership cannot be established."
	}
	return ""
}

func driftNoSymlinkPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("managed paths must be absolute and clean")
	}
	for entry := path; entry != "/"; entry = filepath.Dir(entry) {
		info, err := os.Lstat(entry)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", entry)
		}
	}
	return nil
}

func (s *Service) prepareDriftRepairs(ctx context.Context, r *DriftReport, sp *Spec) {
	if sp == nil || r.RepairPlan.Status == "blocked" {
		return
	}
	files := s.driftRenderFiles(sp)
	observations := map[string]DriftObservation{}
	for _, o := range append(append([]DriftObservation{}, r.Files...), r.Runtime...) {
		observations[o.ID] = o
	}
	readiness := s.driftRecoveryReady(ctx)
	for i := range r.RepairPlan.Items {
		item := &r.RepairPlan.Items[i]
		o := observations[item.ObservationID]
		switch item.Action {
		case "regenerate_owned_file":
			data, allowed := files[item.Resource]
			if !allowed {
				item.Blocker = "This resource has no selected repair executor."
				continue
			}
			if err := driftNoSymlinkPath(item.Resource); err != nil {
				item.Blocker = err.Error()
				continue
			}
			if info, err := os.Stat(filepath.Dir(item.Resource)); err != nil || !info.IsDir() {
				item.Blocker = "The containing directory must already exist for selected file repair."
				continue
			}
			if !driftBootFilesWritable(r.Boot, s.paths.Unit) {
				item.Blocker = "The ordinary boot unit must have a settled execution and established ownership before changing its inputs."
				continue
			}
			if item.Resource == s.paths.Unit && !driftUnitRepairOwned(r.Boot, s.paths.Unit) {
				item.Blocker = "The loaded unit's ownership and overrides cannot be established."
				continue
			}
			before, err := readDriftFile(item.Resource)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				item.Blocker = "The observed owned file cannot be reread."
				continue
			}
			if len(data) > maxDriftReviewBytes || len(before) > maxDriftReviewBytes {
				item.Blocker = "The exact file review exceeds the selected repair size limit."
				continue
			}
			if o.Expected["sha256"] != digestBytes(data) || (o.Status != "missing" && o.Observed["sha256"] != digestBytes(before)) {
				item.Blocker = "File evidence changed during plan preparation; inspect again."
				continue
			}
			item.Before, item.After, item.Effect = string(before), string(data), "boot_files"
		case "reconcile_owned_admission":
			if o.Observed["needed"] != "true" || o.Observed["chainSha256"] == "" {
				item.Blocker = "A readable required filtering chain is needed."
				continue
			}
			count, err := strconv.Atoi(o.Observed["ownedCount"])
			if err != nil || count >= admissionDeleteCap {
				item.Blocker = "The owned rule count exceeds bounded selected repair recovery."
				continue
			}
			item.Before = "Owned rule count: " + o.Observed["ownedCount"] + "\nOwned positions: " + o.Observed["ownedPositions"] + "\nObserved chain SHA-256: " + o.Observed["chainSha256"] + "\n" + o.Reason
			item.After = "Exactly one owned rule at position 1: " + strings.Join(admissionRule(), " ")
			item.Effect = "runtime"
		default:
			item.Blocker = "This resource has no selected repair executor."
			continue
		}
		item.Blocker = readiness
		item.Executable = readiness == ""
		item.ReviewToken = driftRepairToken(*r, *item, o)
		r.RepairPlan.Executable = r.RepairPlan.Executable || item.Executable
	}
}

func driftUnitRepairOwned(b BootHealth, path string) bool {
	return b.DropInPaths == "" && (b.LoadState == "not-found" && b.FragmentPath == "" || b.LoadState == "loaded" && b.FragmentPath == path && b.Owned && b.Status != "unreadable" && b.Status != "unknown" && b.Status != "conflict")
}

func driftAdmissionPositions(listing, chain string) ([]int, bool) {
	valid := false
	positions := []int{}
	position := 0
	for _, line := range strings.Split(listing, "\n") {
		if strings.HasPrefix(line, "-P "+chain+" ") || line == "-N "+chain {
			valid = true
			continue
		}
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "-A "+chain+" ") {
			return nil, false
		}
		position++
		if strings.ReplaceAll(line, "\"", "") == strings.Join(append([]string{"-A", chain}, admissionRule()...), " ") {
			positions = append(positions, position)
		}
	}
	return positions, valid
}

func driftBootFilesWritable(b BootHealth, path string) bool {
	if b.LoadState == "not-found" {
		return b.ActiveState == "inactive" && b.FragmentPath == "" && b.DropInPaths == ""
	}
	return driftUnitRepairOwned(b, path) && (b.ActiveState == "active" || b.ActiveState == "inactive" || b.ActiveState == "failed")
}

func driftBootReviewEvidence(r DriftReport) map[string]string {
	boot := map[string]string{
		"status": r.Boot.Status, "owned": fmt.Sprint(r.Boot.Owned),
		"fragment": r.Boot.FragmentPath, "dropins": r.Boot.DropInPaths,
		"load": r.Boot.LoadState, "reload": r.Boot.NeedDaemonReload,
		"enabled": r.Boot.UnitFileState, "active": r.Boot.ActiveState,
		"bootId": r.Boot.Execution.BootID, "invocationId": r.Boot.Execution.InvocationID,
		"startedMonotonicUs": strconv.FormatUint(r.Boot.Execution.StartedMonotonicUS, 10),
	}
	for _, file := range r.Files {
		if file.Resource == r.Boot.FragmentPath || file.Domain == "render" && filepath.Base(file.Resource) == r.Boot.Unit {
			boot["ownerFileSha256"], boot["ownerFileIdentity"] = file.Observed["sha256"], file.Observed["identity"]
		}
	}
	return boot
}

func driftRepairToken(r DriftReport, item OwnedRepair, o DriftObservation) string {
	var journal map[string]string
	if r.Change != nil {
		journal = map[string]string{"id": r.Change.ID, "phase": r.Change.Phase, "generation": r.Change.Generation}
	}
	var boot map[string]string
	if item.Domain == "render" {
		boot = driftBootReviewEvidence(r)
	}
	data, _ := json.Marshal(struct {
		Generation                        string
		Observation                       DriftObservation
		ID, Action, Before, After, Effect string
		Journal, Boot                     map[string]string
	}{r.SavedGeneration, o, item.ID, item.Action, item.Before, item.After, item.Effect, journal, boot})
	return digestBytes(data)
}

// RepairDrift accepts server-produced identities, never paths or commands.
// Both locks bind review evidence to the apply and serialize independent undo.
func (s *Service) RepairDrift(ctx context.Context, req DriftRepairRequest, client string) (*ChangeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range []string{s.paths.Dir, filepath.Join(s.paths.Dir, ".change.lock"), filepath.Join(s.paths.Dir, recoveryFile)} {
		if err := driftNoSymlinkPath(path); err != nil {
			return nil, guarded("selected repair metadata ownership cannot be established: %s", err)
		}
		if path != s.paths.Dir {
			if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
				return nil, guarded("selected repair metadata must be a regular file")
			}
		}
	}
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	if len(req.Selections) == 0 || len(req.Selections) > 12 {
		return nil, fmt.Errorf("select between one and twelve owned repairs")
	}
	sp, specObservation, generation := readDriftSpec(s)
	if sp == nil || specObservation.Status != "matching" || req.Generation != generation {
		return nil, guarded("the reviewed saved configuration changed or cannot be read; inspect again")
	}
	if j, err := readChange(s.paths.Dir); err == nil && !changeTerminal(j.Phase) {
		return nil, &ReadOnlyError{Reason: "An earlier network change must be confirmed or recovered first."}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	r := s.drift(ctx, true)
	if !r.Consistent || r.SavedGeneration != req.Generation || r.RepairPlan.Status == "blocked" {
		return nil, guarded("the reviewed evidence is no longer consistent; inspect again")
	}
	byID := map[string]OwnedRepair{}
	for _, item := range r.RepairPlan.Items {
		byID[item.ID] = item
	}
	var selected []OwnedRepair
	seen := map[string]bool{}
	for _, selection := range req.Selections {
		item, ok := byID[selection.ID]
		if !ok || !item.Executable || selection.ReviewToken == "" || selection.ReviewToken != item.ReviewToken || seen[selection.ID] {
			return nil, guarded("a selected repair changed, is unavailable, or was duplicated; inspect again")
		}
		seen[selection.ID] = true
		selected = append(selected, item)
	}
	sort.Slice(selected, func(i, j int) bool {
		// Keep the reviewed boot owner unchanged through the other selected
		// inputs; its own file is repaired last and then reloaded explicitly.
		if (selected[i].Resource == s.paths.Unit) != (selected[j].Resource == s.paths.Unit) {
			return selected[j].Resource == s.paths.Unit
		}
		return selected[i].ID < selected[j].ID
	})
	files := s.driftRenderFiles(sp)
	previous := map[string]savedNetworkFile{}
	identities := map[string]string{}
	observations := map[string]DriftObservation{}
	for _, o := range r.Files {
		observations[o.ID] = o
	}
	var paths []string
	chains := map[string]bool{}
	for _, item := range selected {
		if item.Action == "regenerate_owned_file" {
			f, err := driftSnapshotFile(item.Resource)
			if err != nil || string(f.data) != item.Before || string(files[item.Resource]) != item.After || f.exists && (fmt.Sprintf("%04o", f.perm) != observations[item.ObservationID].Observed["mode"] || f.identity != observations[item.ObservationID].Observed["identity"]) {
				return nil, guarded("a selected owned file changed during preflight; inspect again")
			}
			previous[item.Resource] = f.savedNetworkFile
			identities[item.Resource] = f.identity
			paths = append(paths, item.Resource)
		} else {
			chains[item.Resource] = true
		}
	}
	if len(chains) > 0 {
		if _, err := s.requireWritable(ctx); err != nil {
			return nil, err
		}
	}
	beforePath, err := clientPath(ctx, client)
	if err != nil {
		return nil, err
	}
	// Preserve the saved generation exactly, including harmless formatting.
	spec, err := readDriftFile(s.specPath())
	if err != nil || digestBytes(spec) != req.Generation {
		return nil, guarded("the saved configuration changed during preflight")
	}
	preparedCtx := context.WithValue(ctx, selectedDriftRecoveryKey{}, true)
	j, err := s.prepareChange(preparedCtx, sp, nil, nil, spec, nil, nil)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		j.Persistence, j.Boot = "not_applicable", "not_applicable"
	}
	attemptedPaths, attemptedChains := map[string]bool{}, map[string]bool{}
	recover := func(cause error) (*ChangeStatus, error) {
		// A foreign writer may have changed a reviewed resource before its
		// effect began. Do not restore an unattempted resource over that writer.
		kept := j.Files[:0]
		for _, f := range j.Files {
			if attemptedPaths[f.Path] {
				kept = append(kept, f)
			}
		}
		j.Files = kept
		undo := j.Commands[:0]
		for _, c := range j.Commands {
			family := "inet"
			if c.Tool == "ip6tables" {
				family = "inet6"
			}
			if len(c.Args) > 1 && attemptedChains[family+"/"+c.Args[1]] {
				undo = append(undo, c)
			}
		}
		j.Commands = undo
		if len(attemptedPaths) == 0 && len(attemptedChains) == 0 {
			j.Phase, j.Runtime, j.Watchdog = "recovered", "not_applied", "recovered"
			if len(paths) > 0 {
				j.Persistence = "not_written"
			}
			// Nothing selected was attempted, so even a global daemon reload
			// would be an unnecessary change to another writer's loaded state.
			return &j.ChangeStatus, errors.Join(cause, j.save())
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		undoErr := recoverChange(recovery, j)
		return &j.ChangeStatus, errors.Join(cause, undoErr)
	}
	// Journal phase evidence must be durable before the first attempted effect.
	if err := j.save(); err != nil {
		return recover(err)
	}
	if reason := s.driftRecoveryReady(ctx); reason != "" {
		return recover(guarded("independent recovery prerequisites changed after preparation: %s", reason))
	}
	if len(paths) > 0 && !driftBootFilesWritable(s.driftBoot(ctx), s.paths.Unit) {
		return recover(guarded("the boot unit began executing or changed ownership after review"))
	}
	for _, item := range selected {
		_, currentSpec, currentGeneration := readDriftSpec(s)
		if currentSpec.Status != "matching" || currentGeneration != req.Generation {
			return recover(guarded("the saved configuration changed immediately before a selected effect"))
		}
		if item.Action == "regenerate_owned_file" {
			f, err := driftSnapshotFile(item.Resource)
			old := previous[item.Resource]
			if err != nil || f.exists != old.exists || f.perm != old.perm || f.identity != identities[item.Resource] || string(f.data) != string(old.data) {
				return recover(guarded("a selected file changed after watchdog preparation"))
			}
			if string(s.driftRenderFiles(sp)[item.Resource]) != item.After {
				return recover(guarded("the selected expected render changed immediately before its effect"))
			}
			if !driftBootFilesWritable(s.driftBoot(ctx), s.paths.Unit) {
				return recover(guarded("the boot unit began executing or changed ownership before a selected file effect"))
			}
			mode := old.perm
			if !old.exists {
				mode = 0o644
			}
			if err := writeSelectedDriftFile(item.Resource, []byte(item.After), mode, func(candidateIdentity string) error {
				// The staged inode is known before rename. Recovery can distinguish
				// this candidate from a native replacement, including identical bytes.
				j.Files = append(j.Files, recoverySnapshot{
					Path: item.Resource, Data: old.data, Mode: old.perm, Exists: old.exists,
					BeforeIdentity: f.identity, CandidateIdentity: candidateIdentity,
					CandidateSHA256: digestBytes([]byte(item.After)), CandidateMode: mode,
				})
				j.Persistence, j.Boot = "not_written", "not_verified"
				if err := j.save(); err != nil {
					return err
				}
				_, latestSpec, latestGeneration := readDriftSpec(s)
				if latestSpec.Status != "matching" || latestGeneration != req.Generation || string(s.driftRenderFiles(sp)[item.Resource]) != item.After {
					return guarded("the selected expected generation or render changed immediately before rename")
				}
				current := DriftReport{Boot: s.driftBoot(ctx), Files: []DriftObservation{fileObservation(s.paths.Unit, "render", nil, nil)}}
				beforeBoot, _ := json.Marshal(driftBootReviewEvidence(r))
				afterBoot, _ := json.Marshal(driftBootReviewEvidence(current))
				if !driftBootFilesWritable(current.Boot, s.paths.Unit) || string(beforeBoot) != string(afterBoot) {
					return guarded("the reviewed boot owner or execution changed immediately before rename")
				}
				fresh, err := driftSnapshotFile(item.Resource)
				if err != nil || fresh.exists != old.exists || fresh.perm != old.perm || fresh.identity != identities[item.Resource] || string(fresh.data) != string(old.data) {
					return guarded("a selected file changed immediately before rename")
				}
				attemptedPaths[item.Resource] = true
				return nil
			}); err != nil {
				return recover(err)
			}
		} else {
			if _, err := s.requireWritable(ctx); err != nil {
				return recover(err)
			}
			if err := repairSelectedAdmission(ctx, item, r, func(ch AdmissionChainState, listing string) error {
				undo, err := driftAdmissionUndo(ch, listing)
				if err != nil {
					return err
				}
				j.Commands = append(j.Commands, undo...)
				if err := j.save(); err != nil {
					return err
				}
				attemptedChains[item.Resource] = true
				return nil
			}); err != nil {
				return recover(err)
			}
		}
	}
	if seen["repair:render:"+s.paths.Unit] {
		if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
			return recover(err)
		}
		boot := s.driftBoot(ctx)
		if boot.LoadState != "loaded" || boot.FragmentPath != s.paths.Unit || !boot.Owned || boot.DropInPaths != "" || boot.NeedDaemonReload != "no" {
			return recover(fmt.Errorf("the selected owned unit reload could not be verified"))
		}
	}
	if err := verifyPath(beforePath)(ctx); err != nil {
		return recover(err)
	}
	for _, item := range selected {
		if item.Action == "regenerate_owned_file" {
			data, err := readDriftFile(item.Resource)
			info, statErr := os.Lstat(item.Resource)
			mode := previous[item.Resource].perm
			if !previous[item.Resource].exists {
				mode = 0o644
			}
			if err != nil || statErr != nil || string(data) != item.After || info.Mode().Perm() != mode {
				return recover(fmt.Errorf("the selected file repair could not be verified"))
			}
		}
	}
	j.Phase, j.AppliedAt = "runtime_applied", time.Now().UTC()
	if len(chains) > 0 {
		j.Runtime = "applied"
	}
	if len(paths) > 0 {
		j.Persistence = "written"
	}
	if err := j.save(); err != nil {
		return recover(err)
	}
	j.Phase, j.Watchdog = "saved", "completed"
	if err := finishPendingConfirmation(j); err != nil {
		return recover(err)
	}
	if err := j.save(); err != nil {
		return recover(err)
	}
	return &j.ChangeStatus, nil
}

type driftOwnedFile struct {
	savedNetworkFile
	identity string
}

func driftFileIdentity(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("device=%d,inode=%d,mode=%04o", stat.Dev, stat.Ino, info.Mode().Perm())
	}
	return ""
}

func driftSnapshotFile(path string) (driftOwnedFile, error) {
	if err := driftNoSymlinkPath(path); err != nil {
		return driftOwnedFile{}, err
	}
	data, err := readDriftFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return driftOwnedFile{}, nil
	}
	if err != nil {
		return driftOwnedFile{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return driftOwnedFile{}, err
	}
	if !strings.HasPrefix(string(data), generatedHeader) {
		return driftOwnedFile{}, fmt.Errorf("the file's managed ownership cannot be established")
	}
	if !info.Mode().IsRegular() || driftFileIdentity(info) == "" {
		return driftOwnedFile{}, fmt.Errorf("the file identity cannot be established")
	}
	return driftOwnedFile{savedNetworkFile: savedNetworkFile{data: data, perm: info.Mode().Perm(), exists: true}, identity: driftFileIdentity(info)}, nil
}

func repairSelectedAdmission(ctx context.Context, item OwnedRepair, r DriftReport, beforeEffect func(AdmissionChainState, string) error) error {
	var ch *AdmissionChainState
	for i := range r.Admission.Chains {
		candidate := &r.Admission.Chains[i]
		if candidate.Family+"/"+candidate.Chain == item.Resource {
			ch = candidate
			break
		}
	}
	if ch == nil || !ch.Needed {
		return fmt.Errorf("selected admission is not required")
	}
	listing, err := run(ctx, ch.Tool, "-S", ch.Chain)
	if err != nil {
		return err
	}
	for _, o := range r.Runtime {
		if o.ID == item.ObservationID && o.Observed["chainSha256"] != digestBytes([]byte(listing)) {
			return guarded("the selected filtering chain changed after review")
		}
	}
	if err := beforeEffect(*ch, listing); err != nil {
		return err
	}
	for i := 0; i < admissionDeleteCap; i++ {
		out, err := run(ctx, ch.Tool, append([]string{"-D", ch.Chain}, admissionRule()...)...)
		if err == nil {
			continue
		}
		if !recoveryExpectedAbsence(ch.Tool, out, err) {
			return err
		}
		if _, err := run(ctx, ch.Tool, append([]string{"-I", ch.Chain, "1"}, admissionRule()...)...); err != nil {
			return err
		}
		state := inspectAdmissionChain(ctx, ch.Tool, ch.Chain, ch.Family, true)
		if state.Status != "present" {
			return fmt.Errorf("selected admission repair did not verify: %s", state.Reason)
		}
		return nil
	}
	return fmt.Errorf("selected admission exceeds the bounded duplicate rule limit %s", strconv.Itoa(admissionDeleteCap))
}

func driftAdmissionUndo(ch AdmissionChainState, listing string) ([]recoveryCommand, error) {
	positions, valid := driftAdmissionPositions(listing, ch.Chain)
	if !valid || len(positions) >= admissionDeleteCap {
		return nil, fmt.Errorf("selected owned admission snapshot is incomplete or exceeds bounded recovery")
	}
	commands := []recoveryCommand{}
	for i := 0; i < admissionDeleteCap; i++ {
		commands = append(commands, recoveryCommand{Tool: ch.Tool, Args: append([]string{"-D", ch.Chain}, admissionRule()...), AllowGone: true})
	}
	for _, pos := range positions {
		commands = append(commands, recoveryCommand{Tool: ch.Tool, Args: append([]string{"-I", ch.Chain, strconv.Itoa(pos)}, admissionRule()...)})
	}
	return commands, nil
}
