package netsec

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

// These boundaries let regressions inspect argv and Ethernet frames without
// capturing or sending packets on the machine running the tests.
var (
	diagnosticRun = runProbe
	diagnosticHas = hostexec.AvailableOnHost
	lanInterface  = net.InterfaceByName
	wakeSend      = sendWakeFrame
)

func (s *Service) RouteLookup(ctx context.Context, target string) (*ProbeResult, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(target))
	if err != nil || a.Zone() != "" {
		return nil, fmt.Errorf("route lookup takes an IPv4 or IPv6 address without a zone")
	}
	a = a.Unmap()
	res := &ProbeResult{Tool: "route", Target: a.String()}
	if !diagnosticHas("ip") {
		res.Error = "ip is not installed on this host; install iproute2"
		return res, nil
	}
	family := "-4"
	if a.Is6() {
		family = "-6"
	}
	res.Output, res.Duration, err = diagnosticRun(ctx, 10*time.Second, "ip", family, "route", "get", a.String())
	res.OK = err == nil
	if err != nil {
		res.Error = err.Error()
	}
	return res, nil
}

func (s *Service) PathMTU(ctx context.Context, target string) (*ProbeResult, error) {
	if !ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	res := &ProbeResult{Tool: "mtu", Target: target}
	if !diagnosticHas("tracepath") {
		res.Error = "tracepath is not installed on this host; install iputils-tracepath"
		return res, nil
	}
	args := []string{"-n", "-m", "20"}
	if ip := net.ParseIP(target); ip != nil && ip.To4() == nil {
		args = append(args, "-6")
	}
	var err error
	res.Output, res.Duration, err = diagnosticRun(ctx, 45*time.Second, "tracepath", append(args, target)...)
	res.OK = err == nil
	if err != nil {
		res.Error = err.Error()
	}
	res.Output = strings.TrimSpace(res.Output + "\nPath MTU depends on routers returning ICMP errors; filtered replies can leave it unknown.")
	return res, nil
}

func liveLANInterface(name string) (*net.Interface, error) {
	if name == "" || len(name) > 15 || name == "." || name == ".." {
		return nil, fmt.Errorf("give an interface name of 1 to 15 characters")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_@", r)) {
			return nil, fmt.Errorf("invalid interface name")
		}
	}
	iface, err := lanInterface(name)
	if err != nil {
		return nil, fmt.Errorf("interface %q does not exist on this host", name)
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %q is down", name)
	}
	return iface, nil
}

// PacketSnapshot keeps only tcpdump's summary lines. No payload dump, capture
// file, promiscuous mode or caller-built filter reaches the host.
func (s *Service) PacketSnapshot(ctx context.Context, device, protocol string) (*ProbeResult, error) {
	if protocol == "" {
		protocol = "all"
	}
	if protocol != "all" && protocol != "tcp" && protocol != "udp" && protocol != "icmp" && protocol != "icmp6" {
		return nil, fmt.Errorf("protocol must be all, tcp, udp, icmp or icmp6")
	}
	if _, err := liveLANInterface(device); err != nil {
		return nil, err
	}
	res := &ProbeResult{Tool: "capture", Target: device}
	if !diagnosticHas("tcpdump") {
		res.Error = "tcpdump is not installed on this host; install tcpdump"
		return res, nil
	}
	args := []string{"-nn", "-p", "-q", "-l", "-s", "96", "-c", "50", "-i", device}
	if protocol != "all" {
		args = append(args, protocol)
	}
	// Reaching the time budget is normal on a quiet interface. Cancellation
	// of the parent request remains a failed run.
	budget, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var err error
	res.Output, res.Duration, err = diagnosticRun(budget, 20*time.Second, "tcpdump", args...)
	res.OK = err == nil || budget.Err() == context.DeadlineExceeded && ctx.Err() == nil
	if !res.OK && err != nil {
		res.Error = err.Error()
	}
	res.Output = strings.TrimSpace(res.Output + "\nSnapshot ended after at most 50 packets or 15 seconds. This shows packet metadata, not application payloads.")
	return res, nil
}

func magicPacket(target string) ([]byte, error) {
	mac, err := net.ParseMAC(strings.TrimSpace(target))
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return nil, fmt.Errorf("give the target's six-byte unicast MAC address")
	}
	nonzero := false
	for _, b := range mac {
		nonzero = nonzero || b != 0
	}
	if !nonzero {
		return nil, fmt.Errorf("the target MAC address cannot be all zeroes")
	}
	packet := make([]byte, 102)
	for i := range 6 {
		packet[i] = 0xff
	}
	for i := range 16 {
		copy(packet[6+i*6:], mac)
	}
	return packet, nil
}

// WakeOnLAN sends on the selected local Ethernet segment, even if the target
// has no IP address. Raw Ethernet avoids routing a broadcast through a VPN or
// out of an unrelated default interface.
func (s *Service) WakeOnLAN(ctx context.Context, target, device string) (*ProbeResult, error) {
	packet, err := magicPacket(target)
	if err != nil {
		return nil, err
	}
	iface, err := liveLANInterface(device)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagBroadcast == 0 || iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) != 6 {
		return nil, fmt.Errorf("Wake-on-LAN needs a broadcast-capable Ethernet interface, bridge or VLAN")
	}
	res := &ProbeResult{Tool: "wol", Target: target + " on " + device}
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err = wakeSend(iface.Index, packet)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	res.OK = err == nil
	if err != nil {
		res.Error = fmt.Sprintf("could not send the magic packet: %v; raw Ethernet requires CAP_NET_RAW", err)
	} else {
		res.Output = "Sent one magic packet on " + device + ". This does not confirm that the device woke up. The target must share this Ethernet segment and have Wake-on-LAN enabled in its firmware and NIC."
	}
	return res, nil
}

func sendWakeFrame(index int, packet []byte) error {
	// ETH_P_WOL (0x0842), in network byte order, is the same framing used by
	// etherwake. SOCK_DGRAM asks the kernel to supply the Ethernet header.
	protocol := int(binary.NativeEndian.Uint16([]byte{0x08, 0x42}))
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, protocol)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Sendto(fd, packet, unix.MSG_DONTWAIT, &unix.SockaddrLinklayer{
		Protocol: uint16(protocol), Ifindex: index, Halen: 6,
		Addr: [8]uint8{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	})
}
