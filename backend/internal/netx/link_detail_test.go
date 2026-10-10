package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const ipStatsDetail = `[{"ifindex":2,"ifname":"ens3","stats64":{"rx":{"bytes":10,"packets":1,"errors":7,"dropped":2,"over_errors":0,"multicast":0,"length_errors":1,"crc_errors":4,"frame_errors":2,"fifo_errors":0,"missed_errors":3},"tx":{"bytes":10,"packets":1,"errors":0,"dropped":5,"carrier_errors":1,"collisions":0,"aborted_errors":0,"fifo_errors":0,"window_errors":0,"heartbeat_errors":0,"carrier_changes":6}}}]`

const ethtoolFeatures = "Features for ens3:\nrx-checksumming: on [fixed]\ntx-checksumming: on\n\ttx-checksum-ipv4: off [fixed]\nscatter-gather: on\n\ttx-scatter-gather: on\ntcp-segmentation-offload: on\ngeneric-receive-offload: off [requested on]\nlarge-receive-offload: off [fixed]\nvlan-challenged: off [fixed]\n"

func TestLinkDetailReadsDriverOffloadsAndErrorCounters(t *testing.T) {
	rec := record(t).
		on("ip -s -s -j link show dev ens3", ipStatsDetail).
		on("ethtool -i ens3", "driver: virtio_net\nversion: 1.0.0\nfirmware-version: \nbus-info: 0000:00:03.0\n").
		on("ethtool -k ens3", ethtoolFeatures)
	d, err := testService(t).LinkDetail(context.Background(), "ens3")
	if err != nil {
		t.Fatal(err)
	}
	if d.Driver == nil || d.Driver.Name != "virtio_net" || d.Driver.Bus != "0000:00:03.0" || d.Driver.Firmware != "" || d.DriverRead.State != "ok" {
		t.Fatalf("driver: %+v %+v", d.Driver, d.DriverRead)
	}
	if d.Errors == nil || d.Errors.RxCRCErrors != 4 || d.Errors.RxMissedErrors != 3 || d.Errors.TxCarrier != 1 || d.Errors.CarrierChanges != 6 {
		t.Fatalf("errors: %+v", d.Errors)
	}
	want := map[string][2]bool{
		"rx-checksumming": {true, true}, "tx-checksumming": {true, false}, "scatter-gather": {true, false},
		"tcp-segmentation-offload": {true, false}, "generic-receive-offload": {false, false}, "large-receive-offload": {false, true},
	}
	if len(d.Offloads) != len(want) {
		t.Fatalf("only meaningful top-level features: %+v", d.Offloads)
	}
	for _, o := range d.Offloads {
		if w, ok := want[o.Name]; !ok || o.Enabled != w[0] || o.Fixed != w[1] {
			t.Fatalf("%s: %+v", o.Name, o)
		}
	}
	if len(rec.commands()) != 3 {
		t.Fatalf("one stats read and two ethtool reads: %v", rec.commands())
	}
}

func TestLinkDetailKeepsCountersWhenEthtoolIsMissing(t *testing.T) {
	sys := t.TempDir()
	prev := sysClassNet
	sysClassNet = sys
	t.Cleanup(func() { sysClassNet = prev })
	if err := os.MkdirAll(filepath.Join(sys, "ens3", "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../bus/pci/drivers/ena", filepath.Join(sys, "ens3", "device", "driver")); err != nil {
		t.Fatal(err)
	}
	prevRun := run
	t.Cleanup(func() { run = prevRun })
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "ethtool" {
			return "", &UnavailableError{Tool: "ethtool"}
		}
		return ipStatsDetail, nil
	}
	d, err := testService(t).LinkDetail(context.Background(), "ens3")
	if err != nil {
		t.Fatal(err)
	}
	if d.Driver == nil || d.Driver.Name != "ena" || d.DriverRead.State != "ok" {
		t.Fatalf("sysfs names the driver without ethtool: %+v %+v", d.Driver, d.DriverRead)
	}
	if d.OffloadsRead.State != "unavailable" || d.OffloadsRead.Package != "ethtool" || len(d.Offloads) != 0 {
		t.Fatalf("missing ethtool is unavailable, not an empty feature list: %+v", d.OffloadsRead)
	}
	if d.ErrorsRead.State != "ok" || d.Errors.RxErrors != 7 {
		t.Fatalf("counters survive a missing tool: %+v", d.ErrorsRead)
	}
}

func TestLinkDetailRefusesBadNamesAndReportsMissingDevices(t *testing.T) {
	record(t).fail("ip -s -s -j link show dev gone0", `Device "gone0" does not exist.`)
	s := testService(t)
	if _, err := s.LinkDetail(context.Background(), "../etc"); err == nil {
		t.Fatal("an invalid device name must be refused before any command")
	}
	if _, err := s.LinkDetail(context.Background(), "gone0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing device is not found: %v", err)
	}
}
