package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Reading is whether one part of a detailed reading arrived: ok, unavailable
// (the tool or the kernel interface is not there), not_applicable (the
// device has no such thing) or failed, with the reason. A part that did not
// arrive is never presented as an empty one.
type Reading struct {
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Package string `json:"package,omitempty"`
}

func readingOK() Reading { return Reading{State: "ok"} }

// readingOf turns a failed command into the reading that says why.
func readingOf(err error) Reading {
	var missing *UnavailableError
	if errors.As(err, &missing) {
		return Reading{State: "unavailable", Reason: missing.Error(), Package: firstNonEmpty(missing.Package, missing.Tool)}
	}
	return Reading{State: "failed", Reason: firstLines(err.Error(), 2)}
}

// LinkDetail is the part of a device's reading that is too costly or too
// noisy for the list: its driver, its hardware offloads and every error
// counter the kernel keeps for it. The device sheet asks for it when opened.
type LinkDetail struct {
	Name      string    `json:"name"`
	CheckedAt time.Time `json:"checkedAt"`

	Driver     *LinkDriver `json:"driver,omitempty"`
	DriverRead Reading     `json:"driverRead"`

	// Offloads are the meaningful top-level features `ethtool -k` lists.
	Offloads     []LinkOffload `json:"offloads"`
	OffloadsRead Reading       `json:"offloadsRead"`

	Errors     *LinkErrors `json:"errors,omitempty"`
	ErrorsRead Reading     `json:"errorsRead"`
}

// LinkDriver is the kernel driver behind a device.
type LinkDriver struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Bus      string `json:"bus,omitempty"`
}

// LinkOffload is one feature: whether it is on, and whether the driver lets
// it change.
type LinkOffload struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Fixed   bool   `json:"fixed"`
}

// LinkErrors are the kernel's detailed counters since the device was made.
// Most mean a cable, a driver or a queue: CRC and frame errors are the wire,
// missed and FIFO errors a NIC that could not keep up, carrier changes a link
// that went down and came back.
type LinkErrors struct {
	RxErrors       uint64 `json:"rxErrors"`
	RxDropped      uint64 `json:"rxDropped"`
	RxOverErrors   uint64 `json:"rxOverErrors"`
	RxLengthErrors uint64 `json:"rxLengthErrors"`
	RxCRCErrors    uint64 `json:"rxCrcErrors"`
	RxFrameErrors  uint64 `json:"rxFrameErrors"`
	RxFIFOErrors   uint64 `json:"rxFifoErrors"`
	RxMissedErrors uint64 `json:"rxMissedErrors"`
	TxErrors       uint64 `json:"txErrors"`
	TxDropped      uint64 `json:"txDropped"`
	TxCarrier      uint64 `json:"txCarrierErrors"`
	TxCollisions   uint64 `json:"txCollisions"`
	TxAborted      uint64 `json:"txAbortedErrors"`
	TxFIFOErrors   uint64 `json:"txFifoErrors"`
	TxWindow       uint64 `json:"txWindowErrors"`
	TxHeartbeat    uint64 `json:"txHeartbeatErrors"`
	CarrierChanges uint64 `json:"carrierChanges"`
}

// meaningfulOffloads are the features worth reading on a server: what the
// NIC computes for the kernel (checksums, segmentation, receive coalescing),
// what it does with VLAN tags, and the receive-side steering.
var meaningfulOffloads = map[string]bool{
	"rx-checksumming": true, "tx-checksumming": true, "scatter-gather": true,
	"tcp-segmentation-offload": true, "generic-segmentation-offload": true,
	"generic-receive-offload": true, "large-receive-offload": true,
	"rx-vlan-offload": true, "tx-vlan-offload": true, "rx-vlan-filter": true,
	"ntuple-filters": true, "receive-hashing": true, "rx-gro-hw": true,
	"rx-udp-gro-forwarding": true, "tx-udp-segmentation": true, "highdma": true,
}

// sysClassNet is where device facts are read without a process; a variable
// for tests.
var sysClassNet = "/sys/class/net"

// LinkDetail reads one device's driver, offloads and error counters. Each
// part reports its own outcome: a missing ethtool leaves the counters.
func (s *Service) LinkDetail(ctx context.Context, name string) (*LinkDetail, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	stats, err := run(ctx, "ip", "-s", "-s", "-j", "link", "show", "dev", name)
	if err != nil {
		if isGone(err) {
			return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
		}
		return nil, err
	}
	d := &LinkDetail{Name: name, CheckedAt: time.Now().UTC(), Offloads: []LinkOffload{}}
	d.Errors, d.ErrorsRead = parseLinkErrors(stats)

	if out, err := run(ctx, "ethtool", "-i", name); err == nil {
		d.Driver = parseEthtoolDriver(out)
		d.DriverRead = readingOK()
	} else if target, linkErr := os.Readlink(filepath.Join(sysClassNet, name, "device", "driver")); linkErr == nil {
		// Without ethtool the driver's name is still in sysfs.
		d.Driver = &LinkDriver{Name: filepath.Base(target)}
		d.DriverRead = readingOK()
	} else {
		d.DriverRead = readingOf(err)
		if d.DriverRead.State == "unavailable" {
			d.DriverRead.Package = "ethtool"
		}
	}
	if d.Driver == nil && d.DriverRead.State == "ok" {
		d.DriverRead = Reading{State: "not_applicable", Reason: "This device has no driver of its own."}
	}

	if out, err := run(ctx, "ethtool", "-k", name); err == nil {
		d.Offloads = parseEthtoolFeatures(out)
		d.OffloadsRead = readingOK()
		if len(d.Offloads) == 0 {
			d.OffloadsRead = Reading{State: "not_applicable", Reason: "The driver reports no offload features."}
		}
	} else {
		d.OffloadsRead = readingOf(err)
		if d.OffloadsRead.State == "unavailable" {
			d.OffloadsRead.Package = "ethtool"
		}
	}
	return d, nil
}

// parseLinkErrors reads `ip -s -s -j link show dev X`.
func parseLinkErrors(out string) (*LinkErrors, Reading) {
	var raw []struct {
		Stats64 *struct {
			Rx map[string]uint64 `json:"rx"`
			Tx map[string]uint64 `json:"tx"`
		} `json:"stats64"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil || len(raw) == 0 {
		return nil, Reading{State: "failed", Reason: "ip printed something unreadable"}
	}
	st := raw[0].Stats64
	if st == nil {
		return nil, Reading{State: "unavailable", Reason: "The kernel kept no statistics for this device."}
	}
	return &LinkErrors{
		RxErrors: st.Rx["errors"], RxDropped: st.Rx["dropped"], RxOverErrors: st.Rx["over_errors"],
		RxLengthErrors: st.Rx["length_errors"], RxCRCErrors: st.Rx["crc_errors"], RxFrameErrors: st.Rx["frame_errors"],
		RxFIFOErrors: st.Rx["fifo_errors"], RxMissedErrors: st.Rx["missed_errors"],
		TxErrors: st.Tx["errors"], TxDropped: st.Tx["dropped"], TxCarrier: st.Tx["carrier_errors"],
		TxCollisions: st.Tx["collisions"], TxAborted: st.Tx["aborted_errors"], TxFIFOErrors: st.Tx["fifo_errors"],
		TxWindow: st.Tx["window_errors"], TxHeartbeat: st.Tx["heartbeat_errors"], CarrierChanges: st.Tx["carrier_changes"],
	}, readingOK()
}

// parseEthtoolDriver reads `ethtool -i`.
func parseEthtoolDriver(out string) *LinkDriver {
	d := &LinkDriver{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "driver":
			d.Name = value
		case "version":
			d.Version = value
		case "firmware-version":
			if value != "N/A" {
				d.Firmware = value
			}
		case "bus-info":
			if value != "N/A" {
				d.Bus = value
			}
		}
	}
	if d.Name == "" {
		return nil
	}
	return d
}

// parseEthtoolFeatures reads `ethtool -k`'s top-level features, keeping the
// meaningful ones. An indented line is a sub-feature of the one above it.
func parseEthtoolFeatures(out string) []LinkOffload {
	features := []LinkOffload{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" || line[0] == '\t' || line[0] == ' ' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !meaningfulOffloads[key] {
			continue
		}
		value = strings.TrimSpace(value)
		features = append(features, LinkOffload{
			Name:    key,
			Enabled: strings.HasPrefix(value, "on"),
			Fixed:   strings.Contains(value, "[fixed]"),
		})
	}
	return features
}
