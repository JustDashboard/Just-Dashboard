package netx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func nativeStageFile(path string, data []byte, id nativeFileIdentity) (nativeFileIdentity, error) {
	if err := nativeTrustedParents(path); err != nil {
		return nativeFileIdentity{}, err
	}
	file, err := os.OpenFile(nativeHostPath(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nativeFileIdentity{}, errors.New("native private staging collided or could not be created")
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(nativeHostPath(path))
		}
	}()
	if _, err := file.Write(data); err != nil {
		return nativeFileIdentity{}, err
	}
	if err := file.Chown(int(id.UID), int(id.GID)); err != nil {
		return nativeFileIdentity{}, err
	}
	if err := file.Chmod(os.FileMode(id.Mode)); err != nil {
		return nativeFileIdentity{}, err
	}
	if err := file.Sync(); err != nil {
		return nativeFileIdentity{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return nativeFileIdentity{}, err
	}
	identity, err := nativeIdentity(info)
	if err != nil {
		return identity, err
	}
	if err := nativeSyncDirectory(path); err != nil {
		return identity, err
	}
	complete = true
	return identity, nil
}

func prepareNativeFiles(p *nativeProfile, intent NativeIntent) (*nativeUndo, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	u := &nativeUndo{Version: 1, Transaction: hex.EncodeToString(nonce[:]), Owner: p.View.Owner, Renderer: p.View.Renderer, OwnerVersion: p.View.Version, OwnerBus: p.OwnerBus, BusID: p.BusID, TransportGUID: p.TransportGUID, BootID: p.BootID, Device: p.View.Device, IfIndex: p.Device.IfIndex, Kind: p.View.Kind, MAC: p.Device.Address, Contract: p.View.Contract, UUID: p.UUID, DeviceObject: p.DeviceObject, ConnectionObject: p.ConnectionObject, NetplanID: p.NetplanID, NetplanSection: p.NetplanSection, BeforeIntent: *p.View.Intent, CandidateIntent: intent, CheckpointState: "none"}
	return u, nil
}

func nativeStageUndoFiles(u *nativeUndo, before []nativeProfileFile, candidates [][]byte) error {
	cleanup := func() {
		for _, f := range u.Files {
			_ = nativeRemoveStage(f.CandidatePath, f.Candidate.Identity, f.Candidate.Data)
			_ = nativeRemoveStage(f.RollbackPath, f.RollbackID, f.Before.Data)
		}
	}
	for i, b := range before {
		if err := nativeCurrentFile(b, b.Identity); err != nil {
			cleanup()
			return err
		}
		prefix := filepath.Join(filepath.Dir(b.Path), ".jd-native-"+u.Transaction+"-")
		f := nativeUndoFile{Before: b, Candidate: nativeProfileFile{Path: b.Path, Data: candidates[i]}, CandidatePath: prefix + "candidate-" + strconv.Itoa(i), RollbackPath: prefix + "rollback-" + strconv.Itoa(i), Cleanup: "pending"}
		identity, err := nativeStageFile(f.CandidatePath, f.Candidate.Data, b.Identity)
		if err != nil {
			cleanup()
			return err
		}
		f.Candidate.Identity = identity
		u.Files = append(u.Files, f)
		identity, err = nativeStageFile(f.RollbackPath, b.Data, b.Identity)
		if err != nil {
			cleanup()
			return err
		}
		u.Files[i].RollbackID = identity
	}
	return nil
}

func (s *Service) prepareNativeChange(ctx context.Context, u *nativeUndo) (*changeJournal, error) {
	if err := finishPriorChange(ctx, s.paths.Dir); err != nil {
		return nil, err
	}
	if pendingOwner(ctx) <= 0 || !s.independentRecovery || !has("systemctl") || !has("systemd-run") {
		return nil, &ConfirmationError{"Native profile changes require an administrator's interactive pending apply and independent host recovery; nothing was applied."}
	}
	if err := s.installRecoveryBinary(ctx); err != nil {
		return nil, err
	}
	output, err := nativeExecute(ctx, nil, filepath.Join(s.paths.Dir, recoveryBinary), "--network-native-check")
	if err != nil || strings.TrimSpace(output) != nativeRecoveryToken {
		return nil, &ReadOnlyError{Reason: "The installed independent helper cannot verify native-manager recovery support; nothing was applied."}
	}
	c, err := nativeCommand(u)
	if err != nil {
		return nil, err
	}
	j := &changeJournal{Paths: s.paths, Commands: []recoveryCommand{c}, ChangeStatus: ChangeStatus{ID: u.Transaction, Phase: "prepared", Generation: digestBytes(c.Input), Watchdog: "armed", Runtime: "not_applied", Persistence: "not_written", Boot: "not_verified", Cleanup: "pending", OwnerUserID: pendingOwner(ctx), ExpiresAt: time.Now().UTC().Add(confirmationWindow)}}
	if err := j.save(); err != nil {
		return nil, err
	}
	unit := "just-dashboard-network-recover-" + j.ID
	if _, err := run(ctx, "systemd-run", "--collect", "--unit="+unit, "--on-active=90s", "--timer-property=AccuracySec=1s", "--timer-property=RemainAfterElapse=no", "--property=Type=oneshot", "--", filepath.Join(s.paths.Dir, recoveryBinary), "--network-recover", s.paths.Dir, j.ID); err != nil {
		j.Phase, j.Watchdog = "recovered", "failed_to_arm"
		if saveErr := j.save(); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, errors.New("the independent native recovery watchdog could not be armed; nothing was applied")
	}
	return j, nil
}

func nativeCheckpointCreate(ctx context.Context, j *changeJournal, u *nativeUndo) error {
	if u.Renderer != "NetworkManager" {
		return nil
	}
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	if err := nativeVerifyEpoch(ctx, u); err != nil {
		return err
	}
	checkpoints, err := nativeBusProperty[[]string](ctx, u.OwnerBus, nmObject, nmService, "Checkpoints", "ao")
	if err != nil || len(checkpoints) != 0 {
		return errors.New("an existing or unreadable native checkpoint prevents this bounded profile edit; review the native owner first")
	}
	u.CheckpointState = "creating"
	if err := saveNativeUndo(j, u); err != nil {
		return err
	}
	r, err := nativeBus(ctx, u.OwnerBus, nmObject, nmService, "call", "CheckpointCreate", "aouu", "1", u.DeviceObject, "120", "0")
	if err != nil {
		// The request outcome can be uncertain. Recovery must resolve native
		// checkpoint inventory rather than claiming cleanup on an error.
		return errors.New("native checkpoint creation failed or its outcome is unknown; no profile was activated")
	}
	u.Checkpoint, err = nativeBusValue[string](r, "o")
	if err != nil || !nativeObjectID.MatchString(u.Checkpoint) || !strings.Contains(u.Checkpoint, "/Checkpoint/") {
		return errors.New("native checkpoint returned an unexpected identity; no profile was activated")
	}
	u.CheckpointState = "armed"
	return saveNativeUndo(j, u)
}

func nativeWaitVerified(ctx context.Context, j *changeJournal, u *nativeUndo, candidate bool) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := nativeVerifyUndo(ctx, j, u, candidate)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Service) EditNativeProfile(ctx context.Context, device string, req NativeEditRequest, client string) (*NativeProfileView, error) {
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
	if pendingOwner(ctx) <= 0 {
		return nil, &ConfirmationError{"Native profile edits require pending apply with positive dashboard reconnection confirmation."}
	}
	intent, err := normalizeNativeIntent(req.Intent)
	if err != nil {
		return nil, err
	}
	p, err := s.readNativeProfile(ctx, device)
	if err != nil {
		return nil, err
	}
	if !p.View.Editable || p.View.Intent == nil {
		return nil, &ReadOnlyError{Reason: p.View.Refusal}
	}
	if req.Generation == "" || req.Generation != p.View.Generation {
		return nil, &ReadOnlyError{Reason: "Native profile ownership changed since review; refresh the current native profile and review the retained draft."}
	}
	if err := nativeL3Contract(p, intent); err != nil {
		return nil, err
	}
	path, err := clientPath(ctx, client)
	if err != nil {
		return nil, err
	}
	if err := nativeRetainsSource(path, device, intent); err != nil {
		return nil, err
	}
	var candidate []byte
	switch p.View.Owner {
	case "NetworkManager":
		candidate, err = stageNativeNM(ctx, p, intent)
	case "networkd":
		candidate, err = renderNativeNetworkd(p.File.Data, intent)
	case "netplan":
		candidate, err = renderNativeNetplan(p.File.Data, p, intent)
	}
	if err != nil {
		return nil, err
	}
	var stagedIntent NativeIntent
	switch p.View.Owner {
	case "NetworkManager":
		stagedIntent, err = parseNativeNM(candidate, p)
	case "networkd":
		stagedIntent, err = parseNativeNetworkd(candidate, p)
	case "netplan":
		stagedIntent, err = parseNativeNetplan(candidate, p)
	}
	if err != nil || !nativeIntentEqual(stagedIntent, intent) {
		return nil, errors.New("native staging did not preserve the exact requested supported intent; nothing was applied")
	}
	before, candidates := []nativeProfileFile{p.File}, [][]byte{candidate}
	if p.View.Owner == "netplan" {
		generated, err := stageNativeNetplan(ctx, p, candidate)
		if err != nil {
			return nil, err
		}
		before, candidates = append(before, p.Generated), append(candidates, generated)
	}
	// Staging may run native validators. Refresh ownership again at the effect
	// boundary so a concurrent native writer is preserved by refusal.
	fresh, err := s.readNativeProfile(ctx, device)
	if err != nil || !fresh.View.Editable || fresh.View.Generation != req.Generation {
		return nil, &ReadOnlyError{Reason: "Native ownership changed during staging; no native profile was activated."}
	}
	u, err := prepareNativeFiles(p, intent)
	if err != nil {
		return nil, err
	}
	if err := nativeStageUndoFiles(u, before, candidates); err != nil {
		return nil, err
	}
	j, err := s.prepareNativeChange(ctx, u)
	if err != nil {
		// A failed journal/arming write can leave this transaction prepared.
		// Its independent recovery evidence must survive the request failure.
		if recorded, readErr := readChange(s.paths.Dir); readErr == nil && recorded.ID == u.Transaction {
			return nil, err
		}
		for _, f := range u.Files {
			_ = nativeRemoveStage(f.CandidatePath, f.Candidate.Identity, f.Candidate.Data)
			_ = nativeRemoveStage(f.RollbackPath, f.RollbackID, f.Before.Data)
		}
		return nil, err
	}
	recoverFailure := func(cause error) (*NativeProfileView, error) {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		err := recoverNativeJournal(recoveryCtx, j)
		return nil, errors.Join(cause, err)
	}
	if err := nativeCheckpointCreate(ctx, j, u); err != nil {
		return recoverFailure(err)
	}
	for _, f := range u.Files {
		if err := nativeCurrentFile(f.Before, f.Before.Identity); err != nil {
			return recoverFailure(err)
		}
		staged := f.Candidate
		staged.Path = f.CandidatePath
		if err := nativeCurrentFile(staged, f.Candidate.Identity); err != nil {
			return recoverFailure(err)
		}
		if err := nativeReplaceProfile(staged, f.Before); err != nil {
			return recoverFailure(err)
		}
	}
	j.Persistence, j.Phase = "written", "persisted"
	if err := j.save(); err != nil {
		return recoverFailure(err)
	}
	if err := nativeActivate(ctx, u); err != nil {
		return recoverFailure(err)
	}
	j.Phase, j.Runtime, j.AppliedAt = "runtime_applied", "applied", time.Now().UTC()
	if err := j.save(); err != nil {
		return recoverFailure(err)
	}
	if err := nativeWaitVerified(ctx, j, u, true); err != nil {
		return recoverFailure(err)
	}
	if err := verifyPath(path)(ctx); err != nil {
		return recoverFailure(err)
	}
	j.Boot = "enabled"
	if err := finishPendingConfirmation(j); err != nil {
		return recoverFailure(err)
	}
	if err := j.save(); err != nil {
		return recoverFailure(err)
	}
	view, err := s.NativeProfile(ctx, device)
	if err != nil {
		return nil, fmt.Errorf("native profile applied and remains pending; refresh current change status")
	}
	return view, nil
}
