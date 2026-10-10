package netx

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestNativeStructureRequestRetainsOrExplicitlyChangesTopology(t *testing.T) {
	before := NativeContract{Members: []string{"port0"}, BondMode: "active-backup"}
	retained, err := normalizeNativeStructureRequest("bond0", "bond", before, nil)
	if err != nil || !nativeContractsEqual(retained, before) {
		t.Fatal("omitted structure did not preserve the verified baseline", retained, err)
	}
	retained.Members[0] = "changed"
	if before.Members[0] != "port0" {
		t.Fatal("normalization mutated the prior recovery contract")
	}
	candidate, err := normalizeNativeStructureRequest("bond0", "bond", before, &NativeStructureRequest{Members: []string{"port2", "port0"}, BondMode: "balance-rr"})
	if err != nil || candidate.BondMode != "balance-rr" || !slices.Equal(candidate.Members, []string{"port0", "port2"}) || before.BondMode != "active-backup" {
		t.Fatal("candidate topology was not normalized independently", candidate, err)
	}
	candidate, err = normalizeNativeStructureRequest("vrf0", "vrf", NativeContract{VRFTable: 1001}, &NativeStructureRequest{Members: []string{}, VRFTable: 1002})
	if err != nil || candidate.VRFTable != 1002 || candidate.Members == nil {
		t.Fatal("explicit empty VRF membership or table change was lost", candidate, err)
	}
}

func TestNativeStructureRequestRefusesAmbiguousOrForeignScope(t *testing.T) {
	for _, request := range []*NativeStructureRequest{
		{BondMode: "active-backup"},
		{Members: []string{"bond0"}, BondMode: "active-backup"},
		{Members: []string{"port0", "port0"}, BondMode: "active-backup"},
		{Members: []string{"../port0"}, BondMode: "active-backup"},
		{Members: []string{"docker0"}, BondMode: "active-backup"},
		{Members: []string{"tailscale0"}, BondMode: "active-backup"},
		{Members: []string{}, BondMode: "1"},
		{Members: []string{}, BondMode: "unknown"},
		{Members: []string{}, BondMode: "active-backup", VRFTable: 1001},
	} {
		if _, err := normalizeNativeStructureRequest("bond0", "bond", NativeContract{}, request); err == nil {
			t.Errorf("unsafe native bond request admitted: %+v", request)
		}
	}
	for _, table := range []int{0, -1, 52, 253, 254, 255, 2147483648} {
		if _, err := normalizeNativeStructureRequest("vrf0", "vrf", NativeContract{}, &NativeStructureRequest{Members: []string{}, VRFTable: table}); err == nil {
			t.Errorf("reserved/unbounded VRF table admitted: %d", table)
		}
	}
	before := NativeContract{Members: []string{}}
	request := &NativeStructureRequest{Members: []string{}, BondMode: "active-backup"}
	for i := range 17 {
		before.Members = append(before.Members, fmt.Sprintf("old%d", i))
		request.Members = append(request.Members, fmt.Sprintf("new%d", i))
	}
	if _, err := normalizeNativeStructureRequest("bond0", "bond", before, request); err == nil {
		t.Fatal("old/new member union exceeded the checkpoint bound")
	}
	if _, err := normalizeNativeStructureRequest("bond0", "bond", NativeContract{Master: "vrf0"}, &NativeStructureRequest{Members: []string{}, BondMode: "active-backup"}); err == nil {
		t.Fatal("nested foreign-controller topology was admitted")
	}
}

func nativeStructurePlanFixture() *nativeStructurePlan {
	file := nativeStructureFile{Role: "profile", Before: nativeProfileFile{Path: "/etc/netplan/10-native.yaml", Data: []byte("network: before\n"), Identity: nativeFileIdentity{Inode: 17, Mode: 0o600}}, Candidate: []byte("network: candidate\n")}
	return &nativeStructurePlan{Profiles: []nativeStructureProfile{{Role: "controller", Owner: "netplan", Renderer: "networkd", Files: []nativeStructureFile{file}}}}
}

func TestNativeStructureSharedOriginsDeduplicateOrRefuseConflicts(t *testing.T) {
	plan := nativeStructurePlanFixture()
	member := plan.Profiles[0]
	member.Role = "member_retained"
	member.Files = slices.Clone(member.Files)
	plan.Profiles = append(plan.Profiles, member)
	files, err := nativeStructureFiles(plan)
	if err != nil || len(files) != 1 {
		t.Fatal("one authored Netplan inode was not deduplicated", len(files), err)
	}
	for _, conflict := range []string{"candidate", "baseline", "inode", "role"} {
		t.Run(conflict, func(t *testing.T) {
			candidate := plan.Profiles[1].Files[0]
			switch conflict {
			case "candidate":
				candidate.Candidate = []byte("foreign candidate")
			case "baseline":
				candidate.Before.Data = []byte("foreign baseline")
			case "inode":
				candidate.Before.Identity.Inode++
			case "role":
				candidate.Role = "generated"
			}
			plan.Profiles[1].Files[0] = candidate
			if _, err := nativeStructureFiles(plan); err == nil {
				t.Fatal("conflicting shared Netplan origin was silently chosen")
			}
			plan.Profiles[1].Files[0] = plan.Profiles[0].Files[0]
		})
	}
}

func TestNativeStructureSnapshotsAreClosedAndAggregateBounded(t *testing.T) {
	for _, invalid := range []string{"foreign_path", "traversal", "writable", "inode", "empty_candidate", "unknown_role"} {
		t.Run(invalid, func(t *testing.T) {
			plan := nativeStructurePlanFixture()
			file := &plan.Profiles[0].Files[0]
			switch invalid {
			case "foreign_path":
				file.Before.Path = "/etc/shadow"
			case "traversal":
				file.Before.Path = "/etc/netplan/../shadow"
			case "writable":
				file.Before.Identity.Mode = 0o666
			case "inode":
				file.Before.Identity.Inode = 0
			case "empty_candidate":
				file.Candidate = nil
			case "unknown_role":
				file.Role = "arbitrary"
			}
			if _, err := nativeStructureFiles(plan); err == nil {
				t.Fatal("invalid recovery snapshot was admitted")
			}
		})
	}
	plan := nativeStructurePlanFixture()
	for i := range 9 {
		plan.Profiles[0].Sources = append(plan.Profiles[0].Sources, nativeProfileFile{Path: fmt.Sprintf("/etc/netplan/%d-native.yaml", i), Data: bytes.Repeat([]byte{'x'}, maxNativeProfileBytes), Identity: nativeFileIdentity{Inode: uint64(i + 1), Mode: 0o600}})
	}
	if _, err := nativeStructureFiles(plan); err == nil {
		t.Fatal("unchanged authored sources escaped the aggregate recovery bound")
	}
}

func TestNativeStructuralRequestRefusesBeforeAnyL3Effect(t *testing.T) {
	before := nativeExecute
	t.Cleanup(func() { nativeExecute = before })
	nativeExecute = func(context.Context, []byte, string, ...string) (string, error) {
		t.Fatal("provisional structural request reached host execution")
		return "", nil
	}
	_, err := New(Options{}).EditNativeProfile(context.Background(), "bond0", NativeEditRequest{Structure: &NativeStructureRequest{Members: []string{}, BondMode: "active-backup"}}, "")
	if err == nil {
		t.Fatal("provisional structure was silently accepted as an L3-only edit")
	}
}
