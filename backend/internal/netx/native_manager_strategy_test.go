package netx

import (
	"context"
	"strings"
	"testing"
)

func TestNativeExactOriginNeverCreatesOpaqueCheckpoint(t *testing.T) {
	previous := nativeExecute
	t.Cleanup(func() { nativeExecute = previous })
	nativeExecute = func(context.Context, []byte, string, ...string) (string, error) {
		t.Fatal("exact-origin admission sent an opaque checkpoint creation request")
		return "", nil
	}
	u := nativeUndo{Renderer: "NetworkManager", Owner: "netplan", RecoveryStrategy: nativeExactOriginStrategy, NMWriter: "netplan", CheckpointState: "none", Files: make([]nativeUndoFile, 2)}
	if err := nativeRecoveryStrategyScope(&u); err != nil {
		t.Fatal(err)
	}
	if err := nativeCheckpointCreate(context.Background(), &changeJournal{}, &u); err != nil || u.Checkpoint != "" || u.CheckpointState != "none" {
		t.Fatalf("exact-origin strategy acquired an unrecoverable checkpoint outcome: %+v %v", u, err)
	}
	for _, change := range []func(*nativeUndo){
		func(u *nativeUndo) { u.Owner = "NetworkManager" },
		func(u *nativeUndo) { u.Checkpoint, u.CheckpointState = nmObject+"/Checkpoint/1", "armed" },
		func(u *nativeUndo) { u.RecoveryStrategy = "client-selected" },
		func(u *nativeUndo) { u.NMWriter = "" },
	} {
		changed := u
		change(&changed)
		if err := nativeRecoveryStrategyScope(&changed); err == nil {
			t.Fatalf("mismatched or client-selected recovery evidence was admitted: %+v", changed)
		}
	}
	legacy := nativeUndo{Renderer: "NetworkManager", Owner: "netplan", Checkpoint: nmObject + "/Checkpoint/1", CheckpointState: "held"}
	if err := nativeRecoveryStrategyScope(&legacy); err != nil || !nativeUsesCheckpoint(&legacy) || legacy.RecoveryStrategy != "" {
		t.Fatalf("legacy checkpoint evidence was silently converted: %+v %v", legacy, err)
	}
}

func TestNativeNetplanMetadataKeepsExactNativeIdentity(t *testing.T) {
	uuid := "27c153d6-ade1-4dde-988b-7acb135a8495"
	p := nativeProfile{View: NativeProfileView{Owner: "netplan", Renderer: "NetworkManager", Device: "d0", Kind: "dummy"}, UUID: uuid, NetplanID: "d0", NetplanSection: "dummy-devices"}
	p.File.Data = []byte("network:\n  version: 2\n  renderer: NetworkManager\n  dummy-devices:\n    d0:\n      networkmanager:\n        uuid: '" + uuid + "'\n        name: fixture\n")
	p.Generated = nativeProfileFile{Path: "/run/NetworkManager/system-connections/netplan-d0.nmconnection", Data: []byte("[connection]\nid=fixture\nuuid=" + uuid + "\n")}
	if err := nativeNetplanNMIdentity(&p); err != nil {
		t.Fatal(err)
	}
	for _, replacements := range [][2]string{{uuid, "27c153d6-ade1-4dde-988b-7acb135a8496"}, {"name: fixture", "name: foreign"}, {"name: fixture", "name: fixture\n        passthrough:\n          connection.type: bridge"}} {
		changed := p
		changed.File.Data = []byte(strings.ReplaceAll(string(p.File.Data), replacements[0], replacements[1]))
		if err := nativeNetplanNMIdentity(&changed); err == nil {
			t.Fatal("authored metadata disagreement could redirect a native activation")
		}
	}
}
