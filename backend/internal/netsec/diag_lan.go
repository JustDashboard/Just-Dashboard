package netsec

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
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
	if err != nil {
		res.Error = err.Error()
	}
	interpretRoute(res, res.Output, err)
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
	if err != nil {
		res.Error = err.Error()
	}
	interpretPathMTU(res, parseTracepath(res.Output), target, err)
	return res, nil
}

// ValidInterfaceName checks an interface name's syntax without asking
// whether it exists now, for a saved device that names one.
func ValidInterfaceName(name string) error {
	if name == "" || len(name) > 15 || name == "." || name == ".." {
		return fmt.Errorf("give an interface name of 1 to 15 characters")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_@", r)) {
			return fmt.Errorf("invalid interface name")
		}
	}
	return nil
}

// ValidWakeMAC returns a target MAC in canonical form, or why it cannot be
// woken.
func ValidWakeMAC(target string) (string, error) {
	if _, err := magicPacket(target); err != nil {
		return "", err
	}
	mac, _ := net.ParseMAC(strings.TrimSpace(target))
	return strings.ToLower(mac.String()), nil
}

func liveLANInterface(name string) (*net.Interface, error) {
	if err := ValidInterfaceName(name); err != nil {
		return nil, err
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
	packets := 0
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "tcpdump:") && !strings.HasPrefix(line, "listening on") && !strings.Contains(line, "packets captured") &&
			!strings.Contains(line, "packets received by filter") && !strings.Contains(line, "packets dropped by kernel") {
			packets++
		}
	}
	res.Output = strings.TrimSpace(res.Output + "\nSnapshot ended after at most 50 packets or 15 seconds. Summary output can include sensitive decoded protocol fields. No hex or ASCII payload dump is requested.")
	res.fact("Interface", device, BasisConfigured)
	res.fact("Protocol filter", protocol, BasisConfigured)
	res.fact("Limits", "at most 50 packets or 15 seconds, 96-byte snapshot, not promiscuous, summaries only", BasisConfigured)
	res.metric("packets", "Packet summaries", float64(packets), "")
	res.link("Capture a retained PCAP with these settings", "/network/captures?interface="+url.QueryEscape(device)+"&protocol="+url.QueryEscape(protocol))
	res.Limitations = append(res.Limitations, "Summaries can include sensitive decoded protocol fields. A quick snapshot keeps no capture file; use a capture job for a bounded, retained PCAP.")
	if res.OK {
		res.Verdict = ProbeOK
		res.Summary = fmt.Sprintf("%d packet summaries on %s (%s).", packets, device, protocol)
		if packets == 0 {
			res.Summary = "No matching packets on " + device + " within the snapshot window."
		}
	} else {
		res.Verdict = ProbeFailed
		res.Summary = "The snapshot did not run to completion."
	}
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

// Wake verification is measured, never assumed: after sending, the chosen
// address is asked repeatedly within a bounded window. These are seams for
// tests.
var (
	wakeWindow   = 60 * time.Second
	wakeInterval = 3 * time.Second
	wakeCheck    = checkAwake
)

// checkAwake reports whether the address answered. A TCP refusal counts: the
// device's own network stack sent it.
func checkAwake(ctx context.Context, address string, port int) (bool, string) {
	if port > 0 {
		d := &net.Dialer{Timeout: 2 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
		if err == nil {
			conn.Close()
			return true, "TCP " + strconv.Itoa(port) + " connected"
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return true, "TCP " + strconv.Itoa(port) + " refused, so a network stack answered"
		}
		return false, ""
	}
	if !diagnosticHas("ping") {
		return false, ""
	}
	if _, _, err := diagnosticRun(ctx, 3*time.Second, "ping", "-n", "-c", "1", "-W", "1", address); err == nil {
		return true, "ICMP echo answered"
	}
	return false, ""
}

// cachedNeighbour finds the address the neighbour cache last associated with
// a MAC, which is where a woken device usually reappears.
func cachedNeighbour(ctx context.Context, mac net.HardwareAddr) (neighbour, bool) {
	if !diagnosticHas("ip") {
		return neighbour{}, false
	}
	out, _, err := diagnosticRun(ctx, 5*time.Second, "ip", "neigh", "show")
	if err != nil {
		return neighbour{}, false
	}
	for _, n := range parseNeighbours(out) {
		if n.MAC == strings.ToLower(mac.String()) {
			return n, true
		}
	}
	return neighbour{}, false
}

// WakeOnLAN sends on the selected local Ethernet segment, even if the target
// has no IP address. Raw Ethernet avoids routing a broadcast through a VPN or
// out of an unrelated default interface. With a verification address it then
// measures whether the device answers within a bounded window.
func (s *Service) WakeOnLAN(ctx context.Context, target, device, verify string, verifyPort int) (*ProbeResult, error) {
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
	if verify != "" {
		if _, err := ValidWakeVerification(verify, verifyPort); err != nil {
			return nil, err
		}
	}
	res := &ProbeResult{Tool: "wol", Target: target + " on " + device}
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mac, _ := net.ParseMAC(strings.TrimSpace(target))
	if n, ok := cachedNeighbour(ctx, mac); ok {
		res.fact("Last cached address for this MAC", n.Address+" on "+n.Device+" ("+nonEmptyOr(n.State, "no state")+")", BasisObserved)
		if verify == "" {
			res.finding("verify-hint", "notice", "Verification is available", "The neighbour cache last saw this device at "+n.Address+". Give it as the verification address to measure whether it wakes.", "")
		}
	}
	method := ""
	alreadyUp := false
	if verify != "" {
		method = "ICMP echo"
		if verifyPort > 0 {
			method = "TCP " + strconv.Itoa(verifyPort)
		}
		res.fact("Verification", fmt.Sprintf("%s to %s every %s for up to %s", method, verify, wakeInterval, wakeWindow), BasisConfigured)
		clock := res.begin()
		if up, detail := wakeCheck(ctx, verify, verifyPort); up {
			alreadyUp = true
			clock.done("precheck", "Before sending", StageWarning, "already answering: "+detail)
		} else {
			clock.done("precheck", "Before sending", StagePassed, "not answering")
		}
	}
	clock := res.begin()
	err = wakeSend(iface.Index, packet)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		clock.done("send", "Magic packet", StageFailed, err.Error())
		res.Error = fmt.Sprintf("could not send the magic packet: %v; raw Ethernet requires CAP_NET_RAW", err)
		res.Verdict, res.Summary = ProbeFailed, "The magic packet was not sent."
		if verify != "" {
			res.skipRemaining([][2]string{{"verify", "Wake verification"}}, "nothing was sent")
		}
		return res, nil
	}
	clock.done("send", "Magic packet", StagePassed, "one frame on "+device)
	res.OK = true
	res.Output = "Sent one magic packet on " + device + ". This does not confirm that the device woke up. The target must share this Ethernet segment and have Wake-on-LAN enabled in its firmware and NIC."
	res.Summary = "Sent one magic packet on " + device + "; whether the device woke was not measured."
	if verify == "" {
		return res, nil
	}
	clock = res.begin()
	sent := time.Now()
	deadline := sent.Add(wakeWindow)
	for {
		if up, detail := wakeCheck(ctx, verify, verifyPort); up {
			after := time.Since(sent)
			clock.done("verify", "Wake verification", StagePassed, fmt.Sprintf("%s after %s", detail, after.Round(time.Second)))
			res.metric("wake_seconds", "Answered after", math.Round(after.Seconds()*10)/10, "s")
			res.Verdict = ProbeOK
			res.Summary = fmt.Sprintf("%s answered %s after the packet (%s).", verify, after.Round(time.Second), detail)
			if alreadyUp {
				res.Summary = verify + " was already answering before the packet was sent, so waking was not demonstrated."
			}
			res.Output += "\n" + res.Summary
			res.Duration = time.Since(start).Round(time.Millisecond).String()
			return res, nil
		}
		if time.Now().Add(wakeInterval).After(deadline) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(wakeInterval):
		}
	}
	clock.done("verify", "Wake verification", StageUnknown, fmt.Sprintf("no answer to %s within %s", method, wakeWindow))
	res.Verdict = ProbeUnknown
	res.Summary = fmt.Sprintf("%s did not answer %s within %s. The device may still have woken: firmware boot can take longer, and a firewall or a new DHCP address hides it.", verify, method, wakeWindow)
	res.Output += "\n" + res.Summary
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	return res, nil
}

// ValidWakeVerification checks an optional wake verification target: a
// unicast literal on this LAN side and a TCP port, or port 0 for ICMP echo.
func ValidWakeVerification(address string, port int) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() || a.IsLoopback() {
		return "", fmt.Errorf("the verification address must be a unicast IP address without a zone")
	}
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("the verification port must be between 1 and 65535, or empty for ICMP")
	}
	return a.Unmap().String(), nil
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
