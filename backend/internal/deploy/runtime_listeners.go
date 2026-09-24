package deploy

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// A candidate that never answered its readiness check has usually either
// crashed, which its output says, or is listening where nothing can reach
// it, which its output rarely says. The kernel does: /proc/net/tcp lists the
// container's listening sockets, and one bound only to loopback is a server
// nobody outside the container can reach. Reading it runs `cat` inside the
// candidate's own container with a closed argv, never on the host, and only
// the parsed addresses are kept.

// ListeningSocket is one TCP socket a container listens on.
type ListeningSocket struct {
	Address string
	Port    int
}

func (s ListeningSocket) String() string {
	return net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
}

const listenerReadDeadline = 3 * time.Second

// listeningSockets reads a running container's listening TCP sockets; an
// image with no cat, or any other failure, reports none.
func (o *DockerRuntimeOwner) listeningSockets(ctx context.Context, id string) []ListeningSocket {
	_, output, _ := o.client.ExecCheck(ctx, id, []string{"cat", "/proc/net/tcp", "/proc/net/tcp6"}, listenerReadDeadline)
	return parseProcNetTCP(output)
}

// parseProcNetTCP reads the LISTEN rows of /proc/net/tcp and /proc/net/tcp6.
// Addresses are hexadecimal, each 32-bit word in the host's byte order,
// which on every architecture the dashboard runs on is little-endian.
func parseProcNetTCP(content []byte) []ListeningSocket {
	var sockets []ListeningSocket
	seen := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		address, portHex, found := strings.Cut(fields[1], ":")
		if !found {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		raw, err := hex.DecodeString(address)
		if err != nil || (len(raw) != 4 && len(raw) != 16) {
			continue
		}
		for word := 0; word < len(raw); word += 4 {
			raw[word], raw[word+1], raw[word+2], raw[word+3] = raw[word+3], raw[word+2], raw[word+1], raw[word]
		}
		socket := ListeningSocket{Address: net.IP(raw).String(), Port: int(port)}
		if !seen[socket.String()] {
			seen[socket.String()] = true
			sockets = append(sockets, socket)
		}
	}
	return sockets
}

// listenerCause names a candidate whose every listening socket is bound to
// loopback: it is up, and unreachable from the proxy and the check alike.
func listenerCause(containers []ContainerDiagnostics) *OutputCause {
	for _, container := range containers {
		if len(container.Listening) == 0 {
			continue
		}
		loopbackOnly := true
		for _, socket := range container.Listening {
			if ip := net.ParseIP(socket.Address); ip == nil || !ip.IsLoopback() {
				loopbackOnly = false
				break
			}
		}
		if loopbackOnly {
			return &OutputCause{Code: "loopback_only", Listener: container.Listening[0].String()}
		}
	}
	return nil
}

// runtimeOutputCause is the one cause a failed candidate's diagnostics
// prove: what its output says first, then where it listens.
func runtimeOutputCause(containers []ContainerDiagnostics) *OutputCause {
	if cause := applicationOutputCause(containers); cause != nil {
		return cause
	}
	return listenerCause(containers)
}

func loopbackOnlySentence(listener string) string {
	return fmt.Sprintf("the application is listening only on %s, a loopback address that nothing outside its container reaches; bind it to 0.0.0.0 (or read the address from HOST) so the proxy and the readiness check can connect", listener)
}
