// Package portalloc implements the shared policy for relocatable host ports.
// A requested number is a preference; bind addresses and protocols are not.
package portalloc

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
)

var ErrReserved = errors.New("port reserved")

// Select keeps a usable preference, otherwise searches the remaining range.
// reserved includes choices in the same not-yet-started group of services.
func Select(preferred, minimum int, reserved map[int]bool, available func(int) error) (int, error) {
	if minimum < 1 || preferred < minimum || preferred > 65535 {
		return 0, fmt.Errorf("invalid port preference %d", preferred)
	}
	for offset := 0; offset <= 65535-minimum; offset++ {
		port := minimum + (preferred-minimum+offset)%(65536-minimum)
		if reserved[port] {
			continue
		}
		if err := available(port); err == nil {
			return port, nil
		} else if !IsConflict(err) && !errors.Is(err, ErrReserved) {
			return 0, err
		}
	}
	return 0, errors.New("no available host port in the allowed range")
}

// Available is a preflight observation, not a reservation. The service owner
// must still handle a competing bind between this check and its actual start.
func Available(address, protocol string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid host port %d", port)
	}
	if address == "" {
		address = "0.0.0.0"
	}
	family := ""
	if ip := net.ParseIP(address); ip != nil {
		if ip.To4() != nil {
			family = "4"
		} else {
			family = "6"
		}
	}
	endpoint := net.JoinHostPort(address, strconv.Itoa(port))
	switch protocol {
	case "udp":
		listener, err := net.ListenPacket("udp"+family, endpoint)
		if err != nil {
			return err
		}
		return listener.Close()
	case "", "tcp":
		listener, err := net.Listen("tcp"+family, endpoint)
		if err != nil {
			return err
		}
		return listener.Close()
	default:
		return fmt.Errorf("unsupported port protocol %q", protocol)
	}
}

// IsConflict recognises bind failures without retrying unrelated startup errors.
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{"address already in use", "port is already allocated", "port is already in use", "failed to bind host port", "bind: address already in use"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
