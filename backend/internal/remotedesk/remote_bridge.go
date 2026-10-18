package remotedesk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A remote bridge is Moonlight Web running somewhere other than this server,
// named by JD_REMOTE_DESKTOP_BRIDGE. The picture always passes through the
// bridge, so a bridge on this server costs a browser and a computer near
// each other, and far from the server, the server's distance twice: the
// operator runs one on a machine near them instead, on the tailnet, and the
// dashboard proxies to it exactly as it would to its own container. Nothing
// is installed or removed from here; the status says whether it answers.
//
// Its WebRTC is assumed to be published on its own address, which the
// operator configures there (nat_1to1 to that address, a port range), so a
// browser on the tailnet is offered WebRTC as it is for the local bridge.
type remoteBridge struct {
	address string
	probe   func(ctx context.Context, address string) error
	now     func() time.Time

	mu      sync.Mutex
	state   string
	err     string
	checked time.Time
}

// remoteBridgeStatusFor is how long one probe's answer stands: the proxy asks
// on every file the stream page loads.
const remoteBridgeStatusFor = 10 * time.Second

// ParseBridgeAddress checks a JD_REMOTE_DESKTOP_BRIDGE value, host:port, at
// configuration rather than at the first stream.
func ParseBridgeAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", fmt.Errorf("JD_REMOTE_DESKTOP_BRIDGE must be host:port, got %q", address)
	}
	if !validHost(host) {
		return "", fmt.Errorf("JD_REMOTE_DESKTOP_BRIDGE names %q, which is not an address or a host name", host)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("JD_REMOTE_DESKTOP_BRIDGE names port %q, which is not one", port)
	}
	return net.JoinHostPort(host, port), nil
}

// NewRemoteBridge is the engine for a bridge at address (host:port).
func NewRemoteBridge(address string) (Engine, error) {
	address, err := ParseBridgeAddress(address)
	if err != nil {
		return nil, err
	}
	return &remoteBridge{address: address, probe: probeBridge, now: time.Now}, nil
}

func (b *remoteBridge) Status(ctx context.Context) EngineStatus {
	b.mu.Lock()
	state, errText, fresh := b.state, b.err, b.now().Before(b.checked.Add(remoteBridgeStatusFor))
	b.mu.Unlock()
	if !fresh {
		state, errText = EngineRunning, ""
		if err := b.probe(ctx, b.address); err != nil {
			state, errText = EngineStopped, "The stream bridge at "+b.address+" is not answering: "+err.Error()
		}
		b.mu.Lock()
		b.state, b.err, b.checked = state, errText, b.now()
		b.mu.Unlock()
	}
	host, _, _ := net.SplitHostPort(b.address)
	return EngineStatus{
		State: state, Image: "remote", Version: BridgeVersion, Pulled: true,
		Address: b.address, Media: host, Error: errText,
	}
}

var errRemoteBridge = errors.New("the stream bridge runs on another machine")

// Install is the status: a bridge elsewhere is started there. An error says
// so, in the install job's console, when it is not answering.
func (b *remoteBridge) Install(ctx context.Context, progress func(string)) error {
	b.mu.Lock()
	b.checked = time.Time{}
	b.mu.Unlock()
	st := b.Status(ctx)
	if st.State != EngineRunning {
		return fmt.Errorf("%w: %s. Start Moonlight Web there (JD_REMOTE_DESKTOP_BRIDGE names it), or clear JD_REMOTE_DESKTOP_BRIDGE to run one on this server.", errRemoteBridge, st.Error)
	}
	progress("Moonlight Web answers at " + b.address + "; it runs on another machine and is left as it is")
	return nil
}

func (b *remoteBridge) Remove(context.Context) error {
	return fmt.Errorf("%w (JD_REMOTE_DESKTOP_BRIDGE names %s); stop it there", errRemoteBridge, b.address)
}

func (b *remoteBridge) Close() error { return nil }
