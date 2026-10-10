package netx

import (
	"context"
	"errors"
	"slices"
)

const nativeCheckpointStrategy = "native-checkpoint"
const nativeExactOriginStrategy = "exact-origin"

func nativeUsesCheckpoint(u *nativeUndo) bool {
	return u.Renderer == "NetworkManager" && u.RecoveryStrategy != nativeExactOriginStrategy
}

func nativeRecoveryStrategyScope(u *nativeUndo) error {
	if !slices.Contains([]string{"", nativeCheckpointStrategy, nativeExactOriginStrategy}, u.RecoveryStrategy) || !slices.Contains([]string{"", "keyfile", "netplan"}, u.NMWriter) {
		return errors.New("refused an unknown native recovery strategy")
	}
	// Empty strategy is the earlier checkpoint vocabulary. It remains a
	// checkpoint journal; loading it cannot reinterpret its recovery method.
	if u.RecoveryStrategy == "" {
		if u.NMWriter != "" {
			return errors.New("refused incomplete native strategy evidence")
		}
		return nil
	}
	if u.Renderer != "NetworkManager" {
		if u.RecoveryStrategy != nativeExactOriginStrategy || u.NMWriter != "" {
			return errors.New("refused a native strategy outside its renderer scope")
		}
		return nil
	}
	if u.NMWriter == "" || u.RecoveryStrategy == nativeCheckpointStrategy && (u.NMWriter != "keyfile" || u.Owner != "NetworkManager") {
		return errors.New("refused mismatched checkpoint/writer ownership")
	}
	if u.RecoveryStrategy == nativeExactOriginStrategy && (u.Owner != "netplan" || u.Checkpoint != "" || u.CheckpointState != "none" || len(u.Files) != 2) {
		return errors.New("refused exact-origin recovery outside its authored Netplan scope")
	}
	return nil
}

func nativeNMOriginStrategy(ctx context.Context, p *nativeProfile) error {
	migrates, err := nativeNMMigratingWriter(ctx, p.OwnerBus)
	if err != nil {
		return err
	}
	p.NMWriter = "keyfile"
	if migrates {
		p.NMWriter = "netplan"
	}
	p.RecoveryStrategy = nativeCheckpointStrategy
	if p.View.Owner != "netplan" {
		if migrates {
			return errors.New("this native daemon migrates saved profiles through Netplan during checkpoint restoration; its exact-origin recovery adapter requires an authored Netplan origin")
		}
		return nil
	}
	if err := nativeNetplanNMIdentity(p); err != nil {
		return err
	}
	p.RecoveryStrategy = nativeExactOriginStrategy
	return nativeNoCheckpoints(ctx, p.OwnerBus)
}

func nativeNoCheckpoints(ctx context.Context, owner string) error {
	checkpoints, err := nativeBusProperty[[]string](ctx, owner, nmObject, nmService, "Checkpoints", "ao")
	if err != nil || len(checkpoints) != 0 {
		return errors.New("exact-origin recovery requires empty native checkpoint inventory; foreign checkpoints were preserved")
	}
	return nil
}

func nativeVerifyRecoveryStrategy(ctx context.Context, u *nativeUndo) error {
	if err := nativeRecoveryStrategyScope(u); err != nil {
		return err
	}
	if u.Renderer != "NetworkManager" {
		return nil
	}
	if u.RecoveryStrategy == "" {
		return nativeNMOriginGuard(ctx, u.OwnerBus)
	}
	migrates, err := nativeNMMigratingWriter(ctx, u.OwnerBus)
	if err != nil {
		return err
	}
	writer := "keyfile"
	if migrates {
		writer = "netplan"
	}
	if writer != u.NMWriter {
		return errors.New("native persistent writer changed; the recorded recovery strategy was preserved")
	}
	if u.RecoveryStrategy == nativeExactOriginStrategy {
		return nativeNoCheckpoints(ctx, u.OwnerBus)
	}
	return nil
}
