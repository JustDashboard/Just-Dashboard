package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SocketUnit is one address a systemd .socket unit listens on for a service
// it starts: `ssh.socket` holding 0.0.0.0:22 for `ssh.service`. systemd keeps
// the listening socket itself and hands it to the service, so the kernel
// names PID 1 among its holders — and, until the first connection, alone.
type SocketUnit struct {
	// Listen is systemd's spelling: "0.0.0.0:22", "[::]:22", a path, or
	// "route 1361" for a netlink socket.
	Listen string `json:"listen"`
	// Type is Stream, Datagram, SequentialPacket, FIFO, Netlink, Special…
	Type string `json:"type"`
	Unit string `json:"unit"`
	// Activates is the service the socket starts, empty for a socket that
	// starts one instance per connection (Accept=yes), which names none.
	Activates string `json:"activates,omitempty"`
}

// Sockets lists every socket unit's listening addresses. `--all` keeps a
// socket unit that is loaded but not listening from hiding one that is, and
// JSON keeps an address with a space in it ("route 1361") in one column.
func (s *Systemd) Sockets(ctx context.Context) ([]SocketUnit, error) {
	if !s.Available() {
		return nil, fmt.Errorf("systemctl %w", ErrNotInstalled)
	}
	res, err := run(ctx, 10*time.Second, "systemctl",
		"list-sockets", "--all", "--no-legend", "--no-pager", "--full", "--show-types", "--output=json")
	if err != nil {
		return nil, err
	}
	return parseSocketUnits(res.Stdout)
}

func parseSocketUnits(stdout string) ([]SocketUnit, error) {
	if strings.TrimSpace(stdout) == "" {
		return nil, fmt.Errorf("systemctl returned no socket list")
	}
	var raw []struct {
		Listen    string  `json:"listen"`
		Type      string  `json:"type"`
		Unit      string  `json:"unit"`
		Activates *string `json:"activates"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("parse systemctl list-sockets: %w", err)
	}
	out := make([]SocketUnit, 0, len(raw))
	for _, r := range raw {
		unit := SocketUnit{Listen: r.Listen, Type: r.Type, Unit: r.Unit}
		// systemctl prints "-" in its table and null in JSON for none.
		if r.Activates != nil && *r.Activates != "-" {
			unit.Activates = *r.Activates
		}
		out = append(out, unit)
	}
	return out, nil
}
