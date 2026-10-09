package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

const nativeRecoveryToken = "jd-native-manager-v2"
const maxNativeUndoBytes = 2 << 20

type nativeUndoFile struct {
	Before        nativeProfileFile  `json:"before"`
	Candidate     nativeProfileFile  `json:"candidate"`
	CandidatePath string             `json:"candidatePath"`
	RollbackPath  string             `json:"rollbackPath"`
	RollbackID    nativeFileIdentity `json:"rollbackId"`
	Cleanup       string             `json:"cleanup"`
}

// This is a private root-only recovery payload. Its closed vocabulary derives
// all commands from a native owner and exact profile/device identities.
type nativeUndo struct {
	Version          int              `json:"version"`
	Transaction      string           `json:"transaction"`
	Owner            string           `json:"owner"`
	Renderer         string           `json:"renderer"`
	OwnerVersion     string           `json:"ownerVersion"`
	OwnerBus         string           `json:"ownerBus"`
	BusID            string           `json:"busId"`
	TransportGUID    string           `json:"transportGuid"`
	BootID           string           `json:"bootId"`
	RecoveryStrategy string           `json:"recoveryStrategy,omitempty"`
	NMWriter         string           `json:"nmWriter,omitempty"`
	Device           string           `json:"device"`
	IfIndex          int              `json:"ifIndex"`
	Kind             string           `json:"kind"`
	MAC              string           `json:"mac"`
	Contract         NativeContract   `json:"contract"`
	UUID             string           `json:"uuid,omitempty"`
	DeviceObject     string           `json:"deviceObject,omitempty"`
	ConnectionObject string           `json:"connectionObject,omitempty"`
	NetplanID        string           `json:"netplanId,omitempty"`
	NetplanSection   string           `json:"netplanSection,omitempty"`
	BeforeIntent     NativeIntent     `json:"beforeIntent"`
	CandidateIntent  NativeIntent     `json:"candidateIntent"`
	Files            []nativeUndoFile `json:"files"`
	Checkpoint       string           `json:"checkpoint,omitempty"`
	CheckpointState  string           `json:"checkpointState"`
}

var nativeObjectID = regexp.MustCompile(`^/org/freedesktop/NetworkManager/(?:Devices|Settings|Checkpoint)/[0-9]+$`)
var nativeUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)
var nativeTransaction = regexp.MustCompile(`^[0-9a-f]{32}$`)
var nativeBusOwner = regexp.MustCompile(`^:[0-9]+\.[0-9]+$`)

func nativeGeneratedPath(renderer, id, path string) bool {
	if id == "" || len(id) > 100 || strings.ContainsAny(id, "/\\\x00\n\r") || filepath.Base(id) != id {
		return false
	}
	if renderer == "networkd" {
		return path == "/run/systemd/network/10-netplan-"+id+".network"
	}
	return renderer == "NetworkManager" && path == "/run/NetworkManager/system-connections/netplan-"+id+".nmconnection"
}

func decodeNativeUndo(c recoveryCommand) (*nativeUndo, error) {
	if c.Tool != "native" || len(c.Args) != 0 || c.AllowGone || c.AllowExists || len(c.Input) == 0 || len(c.Input) > maxNativeUndoBytes {
		return nil, errors.New("refused an invalid native recovery command")
	}
	var u nativeUndo
	d := json.NewDecoder(bytes.NewReader(c.Input))
	d.DisallowUnknownFields()
	if d.Decode(&u) != nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("refused unreadable native recovery evidence")
	}
	if !slices.Contains([]int{1, 2}, u.Version) || !nativeTransaction.MatchString(u.Transaction) || !nativeBusOwner.MatchString(u.OwnerBus) || !nativeTransaction.MatchString(u.BusID) || !nativeTransaction.MatchString(u.TransportGUID) || u.TransportGUID == strings.Repeat("0", 32) || !nativeUUID.MatchString(u.BootID) || u.IfIndex < 1 || u.IfIndex > 2147483647 || ValidIfName(u.Device) != nil || !slices.Contains([]string{"physical", "dummy", "vlan", "bridge", "bond", "vrf"}, u.Kind) || !slices.Contains([]string{"NetworkManager", "networkd", "netplan"}, u.Owner) || !slices.Contains([]string{"NetworkManager", "networkd"}, u.Renderer) || !nativeVersionSupported(u.Owner, u.OwnerVersion) || len(u.MAC) != 17 || len(u.Files) < 1 || len(u.Files) > 2 {
		return nil, errors.New("refused unexpected native owner identities")
	}
	mac, macErr := net.ParseMAC(u.MAC)
	if macErr != nil || len(mac) != 6 || len(u.Contract.Members) > 32 || (u.Contract.Master != "" && ValidIfName(u.Contract.Master) != nil) {
		return nil, errors.New("refused unreadable native link relationships")
	}
	for _, member := range u.Contract.Members {
		if ValidIfName(member) != nil {
			return nil, errors.New("refused unreadable native member identity")
		}
	}
	if (u.Owner == "netplan") != (len(u.Files) == 2) || !nativeProfilePath(u.Owner, u.Files[0].Before.Path) {
		return nil, errors.New("refused unexpected native profile scope")
	}
	if u.Owner != "netplan" && u.Owner != u.Renderer {
		return nil, errors.New("refused mismatched native owner/renderer")
	}
	if err := nativeRecoveryStrategyScope(&u); err != nil {
		return nil, err
	}
	if u.Version == 1 && (u.RecoveryStrategy != "" || u.NMWriter != "") || u.Version == 2 && u.RecoveryStrategy == "" {
		return nil, errors.New("refused mismatched native helper recovery vocabulary")
	}
	if u.Renderer == "NetworkManager" && (!nativeUUID.MatchString(u.UUID) || !nativeObjectID.MatchString(u.DeviceObject) || !strings.Contains(u.DeviceObject, "/Devices/") || !nativeObjectID.MatchString(u.ConnectionObject) || !strings.Contains(u.ConnectionObject, "/Settings/")) {
		return nil, errors.New("refused unexpected native connection identity")
	}
	if !slices.Contains([]string{"none", "creating", "armed", "holding", "held", "releasing", "released", "recovering", "recovered"}, u.CheckpointState) || (u.Renderer != "NetworkManager" && (u.Checkpoint != "" || u.CheckpointState != "none")) || (u.Checkpoint != "" && (!nativeObjectID.MatchString(u.Checkpoint) || !strings.Contains(u.Checkpoint, "/Checkpoint/"))) || (slices.Contains([]string{"armed", "holding", "held", "releasing", "released", "recovering", "recovered"}, u.CheckpointState) && u.Checkpoint == "") {
		return nil, errors.New("refused unexpected native checkpoint identity")
	}
	for i, f := range u.Files {
		path := f.Before.Path
		if i == 1 && !nativeGeneratedPath(u.Renderer, u.NetplanID, path) {
			return nil, errors.New("refused unexpected generated native profile")
		}
		prefix := filepath.Join(filepath.Dir(path), ".jd-native-"+u.Transaction+"-")
		if f.Candidate.Path != path || f.CandidatePath != prefix+"candidate-"+strconv.Itoa(i) || f.RollbackPath != prefix+"rollback-"+strconv.Itoa(i) || len(f.Before.Data) == 0 || len(f.Before.Data) > maxNativeProfileBytes || len(f.Candidate.Data) == 0 || len(f.Candidate.Data) > maxNativeProfileBytes || !slices.Contains([]string{"pending", "complete"}, f.Cleanup) {
			return nil, errors.New("refused unexpected native staging evidence")
		}
		for _, id := range []nativeFileIdentity{f.Before.Identity, f.Candidate.Identity, f.RollbackID} {
			if id.Inode == 0 || id.UID != 0 || id.Mode > 0o777 || id.Mode&0o022 != 0 {
				return nil, errors.New("refused unsafe native profile provenance")
			}
		}
	}
	for _, intent := range []NativeIntent{u.BeforeIntent, u.CandidateIntent} {
		if _, err := normalizeNativeIntent(intent); err != nil {
			return nil, errors.New("refused invalid native recovery intent")
		}
	}
	return &u, nil
}

func nativeCommand(u *nativeUndo) (recoveryCommand, error) {
	b, err := json.Marshal(u)
	if err != nil || len(b) > maxNativeUndoBytes {
		return recoveryCommand{}, errors.New("native recovery evidence exceeds its bound")
	}
	c := recoveryCommand{Tool: "native", Input: b}
	if _, err := decodeNativeUndo(c); err != nil {
		return recoveryCommand{}, err
	}
	return c, nil
}

func saveNativeUndo(j *changeJournal, u *nativeUndo) error {
	c, err := nativeCommand(u)
	if err != nil {
		return err
	}
	if len(j.Commands) != 1 || j.Commands[0].Tool != "native" || len(j.Files) != 0 || len(j.BootDependencies) != 0 || j.ID != u.Transaction {
		return errors.New("native recovery must keep its exact independent scope")
	}
	j.Commands[0] = c
	return j.save()
}

func nativeCurrentFile(f nativeProfileFile, allowed ...nativeFileIdentity) error {
	current, err := nativeReadProfile(f.Path)
	if err != nil || !bytes.Equal(current.Data, f.Data) || !slices.Contains(allowed, current.Identity) {
		return errors.New("native profile provenance changed; review the native owner before retrying")
	}
	return nil
}

func nativeSyncDirectory(path string) error {
	dir, err := os.Open(nativeHostPath(filepath.Dir(path)))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func nativeRemoveStage(path string, identity nativeFileIdentity, data []byte) error {
	return nativeRemoveOwnedStage(path, nativeProfileFile{Identity: identity, Data: data})
}

func nativeCleanupStages(j *changeJournal, u *nativeUndo) error {
	for i := range u.Files {
		f := &u.Files[i]
		if f.Cleanup == "complete" {
			continue
		}
		if err := nativeRemoveOwnedStage(f.CandidatePath, f.Candidate, f.Before); err != nil {
			return err
		}
		if err := nativeRemoveOwnedStage(f.RollbackPath, nativeProfileFile{Identity: f.RollbackID, Data: f.Before.Data}, f.Candidate); err != nil {
			return err
		}
		f.Cleanup = "complete"
		if err := saveNativeUndo(j, u); err != nil {
			return err
		}
	}
	return nil
}

func nativeCheckpointPresent(ctx context.Context, u *nativeUndo) (bool, error) {
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	if err := nativeVerifyEpoch(ctx, u); err != nil {
		return false, err
	}
	list, err := nativeBusProperty[[]string](ctx, u.OwnerBus, nmObject, nmService, "Checkpoints", "ao")
	if err != nil {
		return false, errors.New("native checkpoint inventory could not be read")
	}
	if !slices.Contains(list, u.Checkpoint) {
		return false, nil
	}
	devices, err := nativeBusProperty[[]string](ctx, u.OwnerBus, u.Checkpoint, nmService+".Checkpoint", "Devices", "ao")
	if err != nil || len(devices) != 1 || devices[0] != u.DeviceObject {
		return false, errors.New("native checkpoint scope differs from its exact device")
	}
	return true, nil
}

func nativeActivate(ctx context.Context, u *nativeUndo) error {
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	if err := nativeVerifyEpoch(ctx, u); err != nil {
		return err
	}
	if err := nativeVerifyRecoveryStrategy(ctx, u); err != nil {
		return err
	}
	if u.Renderer == "NetworkManager" {
		path := u.Files[0].Before.Path
		if len(u.Files) == 2 {
			path = u.Files[1].Before.Path
		}
		r, err := nativeBus(ctx, u.OwnerBus, nmObject+"/Settings", nmService+".Settings", "call", "LoadConnections", "as", "1", path)
		if err != nil || r.Type != "bas" || len(r.Data) != 2 {
			return errors.New("the pinned native owner returned unreadable profile-load evidence")
		}
		var loaded bool
		var failures []string
		if json.Unmarshal(r.Data[0], &loaded) != nil || json.Unmarshal(r.Data[1], &failures) != nil || !loaded || len(failures) != 0 {
			return errors.New("the pinned native owner refused its exact profile load")
		}
		_, err = nativeBus(ctx, u.OwnerBus, nmObject, nmService, "call", "ActivateConnection", "ooo", u.ConnectionObject, u.DeviceObject, "/")
		return err
	}
	owner, err := nativeOwnerIdentity(ctx, networkdService)
	if err != nil || owner != u.OwnerBus {
		return errors.New("networkd owner identity changed before selected activation")
	}
	if _, err := nativeBus(ctx, u.OwnerBus, "/org/freedesktop/network1", networkdService+".Manager", "call", "Reload"); err != nil {
		return err
	}
	links, err := nativeLinks(ctx)
	if err != nil || !slices.ContainsFunc(links, func(link ipLink) bool {
		return link.IfName == u.Device && link.IfIndex == u.IfIndex && link.Address == u.MAC && nativeLinkKind(link) == u.Kind
	}) {
		return errors.New("networkd selected device identity changed before reconfiguration")
	}
	_, err = nativeBus(ctx, u.OwnerBus, "/org/freedesktop/network1", networkdService+".Manager", "call", "ReconfigureLink", "i", strconv.Itoa(u.IfIndex))
	return err
}

func nativeVerifyUndo(ctx context.Context, j *changeJournal, u *nativeUndo, candidate bool) error {
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	p, err := New(Options{Paths: j.Paths}).readNativeProfile(ctx, u.Device)
	if err != nil || p == nil || !p.View.Editable || p.OwnerBus != u.OwnerBus || p.BusID != u.BusID || p.BootID != u.BootID || p.Device.IfIndex != u.IfIndex || p.View.Owner != u.Owner || p.View.Renderer != u.Renderer || p.Device.Address != u.MAC || p.View.Kind != u.Kind || !nativeContractsEqual(p.View.Contract, u.Contract) || (u.Renderer == "NetworkManager" && p.UUID != u.UUID) {
		return errors.New("native owner/runtime/persistence/boot evidence could not be verified; inspect current ownership before retrying")
	}
	want := u.BeforeIntent
	if candidate {
		want = u.CandidateIntent
	}
	if p.View.Intent == nil || !nativeIntentEqual(*p.View.Intent, want) {
		return errors.New("native owner's current supported intent differs from the transaction")
	}
	selected := []nativeProfileFile{p.File}
	if u.Owner == "netplan" {
		if p.NetplanID != u.NetplanID || p.NetplanSection != u.NetplanSection {
			return errors.New("native owner's authored origin differs from the recorded transaction")
		}
		selected = append(selected, p.Generated)
	}
	if len(selected) != len(u.Files) {
		return errors.New("native owner's selected file scope differs from the transaction")
	}
	for i, actual := range selected {
		f := u.Files[i]
		expected := f.Before
		if candidate {
			expected = f.Candidate
		}
		if actual.Path != expected.Path || !bytes.Equal(actual.Data, expected.Data) {
			return errors.New("native activation changed the exact recorded origin or bytes")
		}
		if candidate && actual.Identity != f.Candidate.Identity || !candidate && !nativeUsesCheckpoint(u) && actual.Identity != f.Before.Identity && actual.Identity != f.RollbackID {
			return errors.New("native activation replaced an exact-origin inode outside its recorded scope")
		}
	}
	if u.RecoveryStrategy != "" && (p.RecoveryStrategy != u.RecoveryStrategy || p.NMWriter != u.NMWriter) {
		return errors.New("native recovery strategy or writer changed; its original evidence was preserved")
	}
	return nil
}

// recoverNativeChange is called only by the ordinary journal dispatcher. A
// selected drift repair must refuse this additional owner/effect vocabulary.
func recoverNativeChange(ctx context.Context, j *changeJournal, c recoveryCommand) error {
	u, err := decodeNativeUndo(c)
	if err != nil || u.Transaction != j.ID || len(j.Commands) != 1 || len(j.Files) != 0 || len(j.BootDependencies) != 0 {
		return errors.New("refused native recovery outside its exact journal scope")
	}
	if err := nativeRebindAfterBoot(ctx, j, u); err != nil {
		return err
	}
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	if err := nativeVerifyRecoveryStrategy(ctx, u); err != nil {
		return err
	}
	if nativeUsesCheckpoint(u) {
		if u.CheckpointState == "creating" {
			checkpoints, err := nativeBusProperty[[]string](ctx, u.OwnerBus, nmObject, nmService, "Checkpoints", "ao")
			if err != nil || len(checkpoints) != 0 {
				return errors.New("native checkpoint creation was interrupted before its identity was saved; native timeout cleanup is not yet verified")
			}
			u.CheckpointState = "none"
			if err := saveNativeUndo(j, u); err != nil {
				return err
			}
		}
		if u.Checkpoint != "" && !slices.Contains([]string{"released", "recovered"}, u.CheckpointState) {
			present, err := nativeCheckpointPresent(ctx, u)
			if err != nil {
				return err
			}
			if present {
				u.CheckpointState = "recovering"
				if err := saveNativeUndo(j, u); err != nil {
					return err
				}
				r, err := nativeBus(ctx, u.OwnerBus, nmObject, nmService, "call", "CheckpointRollback", "o", u.Checkpoint)
				if err != nil {
					return errors.New("native checkpoint rollback was refused or failed")
				}
				results, err := nativeBusValue[map[string]uint32](r, "a{su}")
				if err != nil || len(results) != 1 || results[u.DeviceObject] != 0 {
					return errors.New("native checkpoint did not report successful restoration of its exact device")
				}
			}
		}
	}
	for _, f := range u.Files {
		current, err := nativeReadProfile(f.Before.Path)
		if err != nil {
			return errors.New("native recovery cannot read its exact profile")
		}
		if bytes.Equal(current.Data, f.Before.Data) {
			// A native checkpoint may already have restored the same bytes with
			// a new inode. No file is adopted or overwritten in this case.
			continue
		}
		if current.Identity != f.Candidate.Identity || !bytes.Equal(current.Data, f.Candidate.Data) {
			return errors.New("native recovery found a foreign profile change; it was preserved for native-owner review")
		}
		rollback := f.Before
		rollback.Path, rollback.Identity = f.RollbackPath, f.RollbackID
		if displaced, err := nativeReadProfile(f.CandidatePath); err == nil && displaced.Identity == f.Before.Identity && bytes.Equal(displaced.Data, f.Before.Data) {
			// An exchange retained the authored inode. Prefer returning it to
			// its selected name over the durable copy used after reconstruction.
			rollback.Path, rollback.Identity = f.CandidatePath, f.Before.Identity
		}
		if err := nativeCurrentFile(rollback, rollback.Identity); err != nil {
			return err
		}
		if err := nativeReplaceProfile(rollback, f.Candidate); err != nil {
			return err
		}
	}
	if err := nativeActivate(ctx, u); err != nil {
		return err
	}
	if err := nativeWaitVerified(ctx, j, u, false); err != nil {
		return err
	}
	return nil
}

func recoverNativeJournal(ctx context.Context, j *changeJournal) error {
	if len(j.Commands) != 1 || j.Commands[0].Tool != "native" || len(j.Files) != 0 || len(j.BootDependencies) != 0 {
		return errors.New("refused mixed native recovery ownership")
	}
	if _, err := decodeNativeUndo(j.Commands[0]); err != nil {
		return err
	}
	j.Phase, j.RecoveryErrors = "recovering", nil
	if err := j.save(); err != nil {
		return err
	}
	err := recoverNativeChange(ctx, j, j.Commands[0])
	j.Phase, j.Runtime, j.Persistence, j.Watchdog = "recovered", "restored", "restored", "recovered"
	if err != nil {
		j.Phase, j.Runtime, j.Persistence = "degraded", "unknown", "unknown"
		j.RecoveryErrors = []string{err.Error()}
	}
	if saveErr := j.save(); saveErr != nil {
		return errors.Join(err, errors.New("native recovery completion could not be saved"))
	}
	if err == nil {
		return finalizeNativeChange(ctx, j)
	}
	return err
}

func hasNativeRecovery(j *changeJournal) bool {
	return slices.ContainsFunc(j.Commands, func(c recoveryCommand) bool { return c.Tool == "native" })
}

func nativeJournalUndo(j *changeJournal) (*nativeUndo, error) {
	if len(j.Commands) != 1 || j.Commands[0].Tool != "native" || len(j.Files) != 0 || len(j.BootDependencies) != 0 {
		return nil, errors.New("refused mixed native cleanup ownership")
	}
	u, err := decodeNativeUndo(j.Commands[0])
	if err != nil || u.Transaction != j.ID {
		return nil, errors.New("refused native cleanup outside its exact journal scope")
	}
	return u, nil
}

func verifyNativeConfirmation(ctx context.Context, j *changeJournal) error {
	if !hasNativeRecovery(j) {
		return nil
	}
	u, err := nativeJournalUndo(j)
	if err != nil {
		return err
	}
	for _, f := range u.Files {
		if err := nativeCurrentFile(f.Candidate, f.Candidate.Identity); err != nil {
			return err
		}
	}
	if err := nativeVerifyUndo(ctx, j, u, true); err != nil {
		return err
	}
	if u.Checkpoint != "" {
		present, err := nativeCheckpointPresent(ctx, u)
		if err != nil || !present || !slices.Contains([]string{"armed", "held"}, u.CheckpointState) {
			return errors.New("native checkpoint is not armed for this pending profile; recover and review before retrying")
		}
	}
	return nil
}

func holdNativeCheckpoint(ctx context.Context, j *changeJournal) error {
	if !hasNativeRecovery(j) {
		return nil
	}
	u, err := nativeJournalUndo(j)
	if err != nil || u.Checkpoint == "" {
		return err
	}
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	if err := nativeVerifyEpoch(ctx, u); err != nil {
		return err
	}
	// The independent journal timer still owns unconfirmed recovery. Holding
	// this existing checkpoint prevents its native timeout from undoing a
	// durable confirmation when final cleanup fails or the backend dies.
	u.CheckpointState = "holding"
	if err := saveNativeUndo(j, u); err != nil {
		return err
	}
	if _, err := nativeBus(ctx, u.OwnerBus, nmObject, nmService, "call", "CheckpointAdjustRollbackTimeout", "ou", u.Checkpoint, "0"); err != nil {
		return errors.New("native checkpoint timeout hold failed or is uncertain; recover the pending change before retrying")
	}
	u.CheckpointState = "held"
	return saveNativeUndo(j, u)
}

// A terminal journal must reach stable storage before any recovery inode is
// removed. Cleanup never changes a confirmed decision into another rollback.
func finalizeNativeChange(ctx context.Context, j *changeJournal) error {
	if !hasNativeRecovery(j) {
		return nil
	}
	if j.Phase != "confirmed" && j.Phase != "recovered" {
		return errors.New("native cleanup requires a durable confirmed or recovered journal")
	}
	u, err := nativeJournalUndo(j)
	if err != nil {
		return err
	}
	ctx = nativePinnedBus(ctx, u.TransportGUID)
	durable, err := readChange(j.Paths.Dir)
	if err != nil || durable.ID != j.ID || durable.Phase != j.Phase || len(durable.Commands) != 1 || !bytes.Equal(durable.Commands[0].Input, j.Commands[0].Input) {
		return errors.New("native terminal decision is not durably recorded; recovery stages were preserved")
	}
	failure := func(cause error) error {
		j.Cleanup, j.RecoveryErrors = "failed", []string{cause.Error()}
		if err := j.save(); err != nil {
			return errors.Join(cause, errors.New("native terminal cleanup failure could not be saved; its prior journal remains authoritative"))
		}
		return cause
	}
	if j.Phase == "confirmed" && u.Checkpoint != "" && !slices.Contains([]string{"held", "releasing", "released", "recovered"}, u.CheckpointState) {
		return failure(errors.New("native confirmation lacks durable checkpoint timeout-hold evidence"))
	}
	if j.Cleanup == "complete" {
		if slices.ContainsFunc(u.Files, func(f nativeUndoFile) bool { return f.Cleanup != "complete" }) || u.Checkpoint != "" && !slices.Contains([]string{"released", "recovered"}, u.CheckpointState) {
			return failure(errors.New("native terminal cleanup evidence is inconsistent"))
		}
		return nil
	}
	j.Cleanup = "pending"
	if err := j.save(); err != nil {
		return err
	}
	if u.Checkpoint != "" && !slices.Contains([]string{"released", "recovered"}, u.CheckpointState) {
		boot, err := nativeBootIdentity()
		if err != nil {
			return failure(err)
		}
		if boot != u.BootID {
			// A checkpoint cannot survive kernel boot. Never query its reused
			// object path on a new bus or change a durable confirmed decision.
			u.CheckpointState = "released"
			if j.Phase == "recovered" {
				u.CheckpointState = "recovered"
			}
			if err := saveNativeUndo(j, u); err != nil {
				return failure(err)
			}
		}
	}
	if u.Checkpoint != "" && !slices.Contains([]string{"released", "recovered"}, u.CheckpointState) {
		present, err := nativeCheckpointPresent(ctx, u)
		if err != nil {
			return failure(err)
		}
		u.CheckpointState = "releasing"
		if err := saveNativeUndo(j, u); err != nil {
			return failure(err)
		}
		if present {
			if _, err := nativeBus(ctx, u.OwnerBus, nmObject, nmService, "call", "CheckpointDestroy", "o", u.Checkpoint); err != nil {
				return failure(errors.New("native decision is durable but its owned checkpoint cleanup failed; retry cleanup before another change"))
			}
		}
		u.CheckpointState = "released"
		if j.Phase == "recovered" {
			u.CheckpointState = "recovered"
		}
		if err := saveNativeUndo(j, u); err != nil {
			return failure(err)
		}
	}
	if err := nativeCleanupStages(j, u); err != nil {
		return failure(err)
	}
	j.Cleanup, j.RecoveryErrors = "complete", nil
	return j.save()
}

// NativeRecoveryCapability is the packaged-helper protocol, not evidence that
// a timer was armed or that any native owner/device is supported.
func NativeRecoveryCapability() string { return nativeRecoveryToken }

// Keep low-level ownership identity in one place for staged files.
func nativeIdentity(info os.FileInfo) (nativeFileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nativeFileIdentity{}, fmt.Errorf("native file identity is unreadable")
	}
	return nativeFileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino, UID: stat.Uid, GID: stat.Gid, Mode: uint32(info.Mode().Perm())}, nil
}
