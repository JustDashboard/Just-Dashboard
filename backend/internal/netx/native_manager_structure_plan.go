package netx

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const maxNativeStructureBytes = 2 << 20

// NativeStructureRequest changes only an existing controller. Members must be
// explicit: a missing/null list must not silently detach every current port.
type NativeStructureRequest struct {
	Members  []string `json:"members"`
	BondMode string   `json:"bondMode,omitempty"`
	VRFTable int      `json:"vrfTable,omitempty"`
}

// A plan is private recovery data, never an API view. Every old/new member is
// included even when its profile bytes do not change, because activation can
// otherwise modify an uncheckpointed device or use a changed native owner.
type nativeStructurePlan struct {
	Before    NativeContract           `json:"before"`
	Candidate NativeContract           `json:"candidate"`
	Profiles  []nativeStructureProfile `json:"profiles"`
}

type nativeStructureProfile struct {
	Role             string                `json:"role"`
	Device           string                `json:"device"`
	Kind             string                `json:"kind"`
	IfIndex          int                   `json:"ifIndex"`
	MAC              string                `json:"mac"`
	Owner            string                `json:"owner"`
	Renderer         string                `json:"renderer"`
	OwnerVersion     string                `json:"ownerVersion"`
	RecoveryStrategy string                `json:"recoveryStrategy"`
	NMWriter         string                `json:"nmWriter,omitempty"`
	UUID             string                `json:"uuid,omitempty"`
	DeviceObject     string                `json:"deviceObject,omitempty"`
	ConnectionObject string                `json:"connectionObject,omitempty"`
	Before           NativeContract        `json:"before"`
	Candidate        NativeContract        `json:"candidate"`
	BeforeIntent     NativeIntent          `json:"beforeIntent"`
	CandidateIntent  NativeIntent          `json:"candidateIntent"`
	Files            []nativeStructureFile `json:"files"`
	NetplanID        string                `json:"netplanId,omitempty"`
	NetplanSection   string                `json:"netplanSection,omitempty"`
	Sources          []nativeProfileFile   `json:"sources,omitempty"`
}

type nativeStructureFile struct {
	Role      string            `json:"role"`
	Before    nativeProfileFile `json:"before"`
	Candidate []byte            `json:"candidate"`
}

func normalizeNativeStructureRequest(device, kind string, before NativeContract, request *NativeStructureRequest) (NativeContract, error) {
	result := before
	result.Members = slices.Clone(before.Members)
	if request == nil {
		return result, nil
	}
	if before.Master != "" || !slices.Contains([]string{"bond", "vrf"}, kind) || request.Members == nil || len(request.Members) > 32 {
		return result, errors.New("native structure edits need an existing standalone bond/VRF and an explicit bounded member list")
	}
	seen := map[string]bool{}
	for _, member := range request.Members {
		if ValidIfName(member) != nil || member == device || seen[member] || strings.HasPrefix(member, "tailscale") || isDockerBridgeName(member) || member == "docker0" {
			return result, errors.New("native member names must be unique supported devices distinct from their controller")
		}
		seen[member] = true
	}
	for _, member := range before.Members {
		seen[member] = true
	}
	if len(seen) > 32 {
		return result, errors.New("the union of prior and candidate native members exceeds 32 devices")
	}
	if kind == "bond" {
		mode, err := nativeBondMode(request.BondMode)
		if err != nil || request.BondMode != mode || request.VRFTable != 0 {
			return result, errors.New("native bond mode must be a supported canonical policy; a bond has no VRF table")
		}
		result.BondMode = mode
	} else {
		if request.BondMode != "" || !nativeStructureTable(request.VRFTable) {
			return result, errors.New("native VRF table must be supported and unprotected; a VRF has no bond mode")
		}
		result.VRFTable = request.VRFTable
	}
	result.Members = slices.Clone(request.Members)
	slices.Sort(result.Members)
	return result, nil
}

func nativeStructureTable(table int) bool {
	return table > 0 && table <= 2147483647 && table != tableTailscale && (table < 253 || table > 255)
}

func nativeBondMode(value string) (string, error) {
	modes := []string{"balance-rr", "active-backup", "balance-xor", "broadcast", "802.3ad", "balance-tlb", "balance-alb"}
	for index, mode := range modes {
		if value == mode || value == strconv.Itoa(index) {
			return mode, nil
		}
	}
	return "", errors.New("unreadable or unsupported native bond mode")
}

// Shared Netplan origins may appear in several profile roles. They are one
// exchanged inode, and conflicting edits must be refused rather than choosing
// whichever member happened to be collected last.
func nativeStructureFiles(plan *nativeStructurePlan) ([]nativeStructureFile, error) {
	if plan == nil || len(plan.Profiles) == 0 || len(plan.Profiles) > 33 {
		return nil, errors.New("native structure plan has no bounded controller/member scope")
	}
	var result []nativeStructureFile
	seen := map[string]int{}
	snapshots := map[string]nativeProfileFile{}
	bytesTotal := 0
	addSnapshot := func(file nativeProfileFile) error {
		if !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path || len(file.Data) == 0 || len(file.Data) > maxNativeProfileBytes || file.Identity.Inode == 0 || file.Identity.UID != 0 || file.Identity.Mode > 0o777 || file.Identity.Mode&0o022 != 0 {
			return errors.New("native structure snapshot has unsafe provenance or exceeds its bound")
		}
		if prior, found := snapshots[file.Path]; found {
			if prior.Identity != file.Identity || !bytes.Equal(prior.Data, file.Data) {
				return errors.New("native shared origin has conflicting baseline snapshots")
			}
		} else {
			snapshots[file.Path] = file
			bytesTotal += len(file.Data)
		}
		return nil
	}
	for _, profile := range plan.Profiles {
		if len(profile.Sources) > 32 || len(profile.Files) > 3 {
			return nil, errors.New("native structure profile has an unbounded origin set")
		}
		for _, source := range profile.Sources {
			if profile.Owner != "netplan" || !nativeProfilePath("netplan", source.Path) {
				return nil, errors.New("native structure has an unexpected authored origin")
			}
			if err := addSnapshot(source); err != nil {
				return nil, err
			}
		}
		for _, file := range profile.Files {
			if !nativeStructureFilePath(profile, file) || len(file.Candidate) == 0 || len(file.Candidate) > maxNativeProfileBytes {
				return nil, errors.New("native structure file has an invalid role, origin or bounded snapshot")
			}
			if err := addSnapshot(file.Before); err != nil {
				return nil, err
			}
			if index, found := seen[file.Before.Path]; found {
				prior := result[index]
				if prior.Role != file.Role || prior.Before.Identity != file.Before.Identity || !bytes.Equal(prior.Before.Data, file.Before.Data) || !bytes.Equal(prior.Candidate, file.Candidate) {
					return nil, errors.New("native shared origin has conflicting snapshots or candidate bytes")
				}
				continue
			}
			bytesTotal += len(file.Candidate)
			seen[file.Before.Path] = len(result)
			result = append(result, file)
		}
		if bytesTotal > maxNativeStructureBytes {
			return nil, errors.New("native structure snapshots exceed the aggregate recovery bound")
		}
	}
	if len(result) == 0 || len(result) > 99 {
		return nil, errors.New("native structure file set is empty or exceeds its bound")
	}
	return result, nil
}

func nativeStructureFilePath(profile nativeStructureProfile, file nativeStructureFile) bool {
	switch file.Role {
	case "profile":
		return nativeProfilePath(profile.Owner, file.Before.Path)
	case "generated":
		return profile.Owner == "netplan" && nativeGeneratedPath(profile.Renderer, profile.NetplanID, file.Before.Path)
	case "netdev":
		if profile.Owner == "networkd" {
			base := filepath.Base(file.Before.Path)
			return filepath.Dir(file.Before.Path) == "/etc/systemd/network" && len(base) <= 200 && !strings.HasPrefix(base, ".") && strings.HasSuffix(base, ".netdev")
		}
		return profile.Owner == "netplan" && profile.Renderer == "networkd" && nativeGeneratedPath("networkd", profile.NetplanID, strings.TrimSuffix(file.Before.Path, ".netdev")+".network") && strings.HasSuffix(file.Before.Path, ".netdev")
	}
	return false
}
