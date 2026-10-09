package netx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
)

func nativeBootIdentity() (string, error) {
	b, err := os.ReadFile(nativeHostPath("/proc/sys/kernel/random/boot_id"))
	id := strings.TrimSpace(string(b))
	if err != nil || !nativeUUID.MatchString(id) {
		return "", errors.New("native boot identity could not be verified")
	}
	return id, nil
}

func nativeBusIdentity(ctx context.Context) (string, error) {
	r, err := nativeBus(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "call", "GetId")
	if err != nil {
		return "", err
	}
	id, err := nativeBusValue[string](r, "s")
	if err != nil || !nativeTransaction.MatchString(id) {
		return "", errors.New("native bus identity could not be verified")
	}
	return id, nil
}

func nativeTransportIdentity(ctx context.Context) (string, error) {
	endpoint, err := nativeBusEndpoint(ctx)
	if err != nil {
		return "", err
	}
	out, err := nativeExecute(ctx, nil, "busctl", endpoint, "--no-pager", "--augment-creds=no", "--timeout=10", "status")
	if err != nil {
		return "", errors.New("native bus authentication identity is unreadable")
	}
	guid := ""
	for _, line := range strings.Split(out, "\n") {
		if value, found := strings.CutPrefix(line, "BusID="); found {
			if guid != "" || !nativeTransaction.MatchString(value) || value == strings.Repeat("0", 32) {
				return "", errors.New("native bus authentication identity is ambiguous")
			}
			guid = value
		}
	}
	if guid == "" {
		return "", errors.New("native bus authentication identity is missing")
	}
	return guid, nil
}

func nativeVerifyEpoch(ctx context.Context, u *nativeUndo) error {
	boot, err := nativeBootIdentity()
	if err != nil || boot != u.BootID {
		return errors.New("native checkpoint/process identity belongs to another boot")
	}
	bus, err := nativeBusIdentity(ctx)
	if err != nil || bus != u.BusID {
		return errors.New("native bus restarted; its old object identities were preserved for review")
	}
	return nil
}

// Only an observed different kernel boot permits adopting new native process
// objects. The same names alone never authorize a daemon restart or profile
// takeover. Checkpoints from the prior boot cannot own the new runtime.
func nativeRebindAfterBoot(ctx context.Context, j *changeJournal, u *nativeUndo) error {
	boot, err := nativeBootIdentity()
	if err != nil {
		return err
	}
	if boot == u.BootID {
		return nativeVerifyEpoch(ctx, u)
	}
	p, err := New(Options{Paths: j.Paths}).readNativeProfile(ctx, u.Device)
	if err != nil || p == nil || p.OwnerBus == "" || p.BusID == "" || p.View.Owner != u.Owner || p.View.Renderer != u.Renderer || p.View.Version != u.OwnerVersion || p.Device.Address != u.MAC || p.View.Kind != u.Kind || !nativeContractsEqual(p.View.Contract, u.Contract) || p.File.Path != u.Files[0].Before.Path || p.View.Intent == nil || (u.Renderer == "NetworkManager" && p.UUID != u.UUID) {
		return errors.New("native boot recovery cannot verify the exact persistent owner/device contract")
	}
	selected := []nativeProfileFile{p.File}
	if u.Owner == "netplan" {
		if p.NetplanID != u.NetplanID || p.NetplanSection != u.NetplanSection || p.Generated.Path != u.Files[1].Before.Path {
			return errors.New("native boot recovery found a different netplan origin")
		}
		selected = append(selected, p.Generated)
	}
	for i, current := range selected {
		f := &u.Files[i]
		if !bytes.Equal(current.Data, f.Before.Data) && !bytes.Equal(current.Data, f.Candidate.Data) {
			return errors.New("native boot recovery preserved a foreign profile change")
		}
		if i == 0 {
			if bytes.Equal(current.Data, f.Candidate.Data) && current.Identity != f.Candidate.Identity {
				return errors.New("native boot recovery preserved a replaced persistent profile")
			}
			continue
		}
		// The selected netplan artifact and its staging directory are in /run,
		// which is reconstructed at boot. Journal bytes remain authoritative;
		// an exact selected artifact may have a new root-owned inode.
		if bytes.Equal(current.Data, f.Candidate.Data) {
			f.Candidate.Identity = current.Identity
		}
		rollback, err := nativeReadProfile(f.RollbackPath)
		if errors.Is(err, os.ErrNotExist) {
			f.RollbackID, err = nativeStageFile(f.RollbackPath, f.Before.Data, f.Before.Identity)
		} else if err == nil && bytes.Equal(rollback.Data, f.Before.Data) {
			f.RollbackID = rollback.Identity
		} else {
			return errors.New("native boot recovery preserved foreign generated rollback staging")
		}
		if err != nil {
			return err
		}
	}
	u.OwnerBus, u.BusID, u.TransportGUID, u.BootID = p.OwnerBus, p.BusID, p.TransportGUID, boot
	u.DeviceObject, u.ConnectionObject = p.DeviceObject, p.ConnectionObject
	u.IfIndex = p.Device.IfIndex
	if u.Checkpoint != "" {
		u.CheckpointState = "recovered"
	} else {
		u.CheckpointState = "none"
	}
	return saveNativeUndo(j, u)
}
