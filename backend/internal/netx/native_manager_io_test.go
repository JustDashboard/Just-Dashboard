package netx

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNativeBusPropertyAndMethodFraming(t *testing.T) {
	previous := nativeExecute
	t.Cleanup(func() { nativeExecute = previous })
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, " get-property ") && strings.HasSuffix(joined, " Version"):
			return `{"type":"s","data":"1.52.0"}`, nil
		case strings.Contains(joined, " get-property ") && strings.HasSuffix(joined, " Checkpoints"):
			return `{"type":"ao","data":["/org/freedesktop/NetworkManager/Checkpoint/1"]}`, nil
		default:
			return `{"type":"ao","data":[["/org/freedesktop/NetworkManager/Devices/1"]]}`, nil
		}
	}
	ctx := context.Background()
	version, err := nativeBusProperty[string](ctx, nmService, nmObject, nmService, "Version", "s")
	if err != nil || version != "1.52.0" {
		t.Fatalf("scalar property: %q %v", version, err)
	}
	checkpoints, err := nativeBusProperty[[]string](ctx, nmService, nmObject, nmService, "Checkpoints", "ao")
	if err != nil || !reflect.DeepEqual(checkpoints, []string{nmObject + "/Checkpoint/1"}) {
		t.Fatalf("array property: %v %v", checkpoints, err)
	}
	reply, err := nativeBus(ctx, nmService, nmObject, nmService, "call", "GetDevices")
	devices, valueErr := nativeBusValue[[]string](reply, "ao")
	if err != nil || valueErr != nil || !reflect.DeepEqual(devices, []string{nmObject + "/Devices/1"}) {
		t.Fatalf("array method argument: %v %v %v", devices, err, valueErr)
	}
}

func TestNativeBusPinsTransportAuthentication(t *testing.T) {
	previous := nativeExecute
	t.Cleanup(func() { nativeExecute = previous })
	id := strings.Repeat("a", 32)
	calls := 0
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		calls++
		if tool != "busctl" || args[0] != "--address=unix:path=/run/dbus/system_bus_socket,guid="+id {
			t.Fatalf("transaction effect did not pin authentication GUID: %s %v", tool, args)
		}
		if !strings.Contains(strings.Join(args, " "), " call :1.15 ") {
			t.Fatalf("transaction effect lost unique native owner: %v", args)
		}
		return "", nil
	}
	if _, err := nativeBus(nativePinnedBus(context.Background(), id), ":1.15", nmObject, nmService, "call", "CheckpointDestroy", "o", nmObject+"/Checkpoint/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeBus(nativePinnedBus(context.Background(), "invalid"), ":1.15", nmObject, nmService, "call", "CheckpointDestroy", "o", nmObject+"/Checkpoint/1"); err == nil || calls != 1 {
		t.Fatal("invalid transport epoch reached native execution")
	}
	if _, err := nativeBus(nativePinnedBus(context.Background(), strings.Repeat("0", 32)), ":1.15", nmObject, nmService, "call", "CheckpointDestroy", "o", nmObject+"/Checkpoint/1"); err == nil || calls != 1 {
		t.Fatal("null transport epoch reached native execution")
	}
}

func TestNativeGenerationFencesSelectedKernelAndBusObjects(t *testing.T) {
	p := nativeProfile{
		View:     NativeProfileView{Device: "d0", Owner: "NetworkManager", Renderer: "NetworkManager", Kind: "dummy"},
		Device:   ipLink{IfIndex: 7, Address: "02:00:00:00:00:07"},
		OwnerBus: ":1.15", DeviceObject: nmObject + "/Devices/7", ConnectionObject: nmObject + "/Settings/9",
		TransportGUID: strings.Repeat("a", 32),
	}
	before := nativeGeneration(&p)
	for _, change := range []func(*nativeProfile){
		func(p *nativeProfile) { p.Device.IfIndex++ },
		func(p *nativeProfile) { p.DeviceObject = nmObject + "/Devices/8" },
		func(p *nativeProfile) { p.ConnectionObject = nmObject + "/Settings/10" },
		func(p *nativeProfile) { p.OwnerBus = ":1.16" },
		func(p *nativeProfile) { p.TransportGUID = strings.Repeat("b", 32) },
	} {
		changed := p
		change(&changed)
		if nativeGeneration(&changed) == before {
			t.Fatal("replacement kernel or bus object retained the reviewed generation")
		}
	}
}
