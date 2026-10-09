package netx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
)

type nativeRecoveryOwnership struct {
	AllPrior      bool
	UnprovenPrior bool
}

func nativeMatchesPriorProfile(current *nativeProfileFile, file nativeUndoFile) bool {
	return bytes.Equal(current.Data, file.Before.Data) && (current.Identity == file.Before.Identity || current.Identity == file.RollbackID)
}

func nativeMatchesCandidateProfile(current *nativeProfileFile, file nativeUndoFile) bool {
	return current.Identity == file.Candidate.Identity && bytes.Equal(current.Data, file.Candidate.Data)
}

// Refusing an explicit rollback does not stop the same checkpoint's automatic
// writer. Release only the saved, scoped object before reporting containment;
// its original files and independent journal remain available for review.
func nativePreserveRefusedRecovery(ctx context.Context, j *changeJournal, u *nativeUndo, refusal error) error {
	if !nativeUsesCheckpoint(u) || u.Checkpoint == "" || slices.Contains([]string{"released", "recovered"}, u.CheckpointState) {
		return refusal
	}
	present, err := nativeCheckpointPresent(ctx, u)
	if err != nil {
		return errors.Join(refusal, errors.New("the saved native automatic rollback could not be scoped or stopped; its deadline remains unresolved"), err)
	}
	u.CheckpointState = "releasing"
	if err := saveNativeUndo(j, u); err != nil {
		return errors.Join(refusal, errors.New("native deadline containment could not be durably armed; no checkpoint was released"), err)
	}
	if present {
		if err := nativeVerifyEpoch(ctx, u); err != nil {
			return errors.Join(refusal, errors.New("native ownership changed before deadline containment; its automatic deadline remains unresolved"), err)
		}
		if _, err := nativeBus(nativePinnedBus(ctx, u.TransportGUID), u.OwnerBus, nmObject, nmService, "call", "CheckpointDestroy", "o", u.Checkpoint); err != nil {
			return errors.Join(refusal, errors.New("owned native checkpoint release has an unknown outcome; its automatic deadline remains unresolved until exact inventory retry"), err)
		}
	}
	present, err = nativeCheckpointPresent(ctx, u)
	if err != nil || present {
		return errors.Join(refusal, errors.New("native automatic rollback release was not verified; its deadline remains unresolved"), err)
	}
	u.CheckpointState = "released"
	if err := saveNativeUndo(j, u); err != nil {
		return errors.Join(refusal, errors.New("verified native deadline release could not be saved; exact inventory retry remains required"), err)
	}
	return refusal
}

func nativeReadOwnedRecoveryStage(path string, allowed ...nativeProfileFile) (*nativeProfileFile, error) {
	if _, err := os.Lstat(nativeHostPath(path + "-cleanup")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("pending native recovery found an unexpected cleanup claim; its evidence was preserved")
	}
	current, err := nativeReadProfile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("native recovery cannot verify its captured staging evidence")
	}
	for _, expected := range allowed {
		if current.Identity == expected.Identity && bytes.Equal(current.Data, expected.Data) {
			return current, nil
		}
	}
	return nil, errors.New("native recovery preserved foreign staging ownership or bytes before any owner effect")
}

// A checkpoint rollback is a persistent writer too. Check every selected file
// and restore stage before asking it to act, rather than discovering an external
// edit after the native writer has already overwritten it.
func nativeRecoveryOwnershipPreflight(u *nativeUndo) (nativeRecoveryOwnership, error) {
	state := nativeRecoveryOwnership{AllPrior: true}
	for _, file := range u.Files {
		current, err := nativeReadProfile(file.Before.Path)
		if err != nil {
			return state, errors.New("native recovery cannot read its exact selected profile before any owner effect")
		}
		prior := nativeMatchesPriorProfile(current, file)
		switch {
		case prior:
		case nativeMatchesCandidateProfile(current, file):
			// A retained profile can have equal prior/candidate bytes. Its exact
			// candidate inode still needs the captured prior inode restored.
			state.AllPrior = false
		case bytes.Equal(current.Data, file.Before.Data):
			if !nativeUsesCheckpoint(u) {
				return state, errors.New("native recovery preserved a replaced prior profile inode before any owner effect")
			}
			// An interrupted native writer may have returned prior bytes with a
			// new inode. This only permits full read-only prior verification.
			prior, state.UnprovenPrior = true, true
		default:
			return state, errors.New("native recovery preserved foreign candidate ownership or bytes before any owner effect")
		}
		candidate, err := nativeReadOwnedRecoveryStage(file.CandidatePath, file.Before, file.Candidate)
		if err != nil {
			return state, err
		}
		rollback, err := nativeReadOwnedRecoveryStage(file.RollbackPath, nativeProfileFile{Identity: file.RollbackID, Data: file.Before.Data}, file.Candidate)
		if err != nil {
			return state, err
		}
		if !prior && (candidate == nil || !bytes.Equal(candidate.Data, file.Before.Data)) && (rollback == nil || !bytes.Equal(rollback.Data, file.Before.Data)) {
			return state, errors.New("native recovery lacks its captured prior restore inode; no owner effect was attempted")
		}
	}
	if state.UnprovenPrior && !state.AllPrior {
		return state, errors.New("native recovery cannot attribute partial prior inode replacement; all owner effects were refused")
	}
	return state, nil
}
