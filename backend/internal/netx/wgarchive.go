package netx

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A removed tunnel's file is moved into wgRemovedDir rather than deleted,
// because the server's private key exists nowhere else. Restoring puts it
// back as it was — same key, same peers, so every client's existing
// configuration works again — after the checks a new tunnel passes: a free
// name and port, networks that collide with nothing here, and site routes
// that neither contain the operator nor capture a WireGuard transport.

// wgArchiveRe is an archived file's name as RemoveWireGuard writes it.
var wgArchiveRe = regexp.MustCompile(`^([A-Za-z0-9_.-]{1,15})\.conf\.([0-9]{1,12})$`)

// WGArchived is one archived tunnel. It never carries a key.
type WGArchived struct {
	File       string   `json:"file"`
	Name       string   `json:"name"`
	ArchivedAt int64    `json:"archivedAt"`
	ListenPort int      `json:"listenPort"`
	Addresses  []string `json:"addresses"`
	Endpoint   string   `json:"endpoint"`
	Peers      int      `json:"peers"`
	// Restorable is every check passing now; Refusal says which did not.
	Restorable bool   `json:"restorable"`
	Refusal    string `json:"refusal,omitempty"`
}

// WireGuardArchive lists the archived tunnels, newest first, each with
// whether it could be restored as things are now.
func (s *Service) WireGuardArchive(ctx context.Context) ([]WGArchived, error) {
	dir := filepath.Join(s.paths.WireGuard, wgRemovedDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []WGArchived{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	confs, err := s.listWGConfs()
	if err != nil {
		return nil, err
	}
	host, hostErr := wgReadHostState(ctx)
	out := []WGArchived{}
	for _, e := range entries {
		m := wgArchiveRe.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() || validWGName(m[1]) != nil {
			continue
		}
		at, _ := strconv.ParseInt(m[2], 10, 64)
		a := WGArchived{File: e.Name(), Name: m[1], ArchivedAt: at, Addresses: []string{}}
		conf, err := s.readArchived(e.Name())
		if err != nil {
			a.Refusal = err.Error()
			out = append(out, a)
			continue
		}
		sec := conf.iface()
		a.Addresses = append(a.Addresses, sec.list("address")...)
		a.ListenPort, _ = strconv.Atoi(sec.get("listenport"))
		a.Endpoint = sec.bodyMeta()["endpoint"]
		a.Peers = len(conf.peers())
		if hostErr != nil {
			a.Refusal = hostErr.Error()
		} else if err := wgRestorable(a.Name, conf, confs, host, ""); err != nil {
			a.Refusal = err.Error()
		} else {
			a.Restorable = true
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ArchivedAt != out[j].ArchivedAt {
			return out[i].ArchivedAt > out[j].ArchivedAt
		}
		return out[i].File < out[j].File
	})
	return out, nil
}

// readArchived parses an archived file and refuses one the dashboard did not
// write or that has no usable server key.
func (s *Service) readArchived(file string) (*wgConf, error) {
	if !wgArchiveRe.MatchString(file) {
		return nil, fmt.Errorf("%q is not an archived tunnel: %w", file, ErrNotFound)
	}
	b, err := os.ReadFile(filepath.Join(s.paths.WireGuard, wgRemovedDir, file))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("archived tunnel %s: %w", file, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the archived %s: %w", file, err)
	}
	conf := parseWGConf(string(b))
	if !conf.managed {
		return nil, fmt.Errorf("%s was not written by the dashboard, so it is not restored from here: %w", file, ErrNotManaged)
	}
	if conf.iface() == nil {
		return nil, fmt.Errorf("%s has no [Interface] section", file)
	}
	if _, err := wgPublicKey(conf.iface().get("privatekey")); err != nil {
		return nil, fmt.Errorf("%s has no usable server key: %w", file, err)
	}
	return conf, nil
}

// wgRestorable is the set of checks a restore passes: the name is free as a
// file and a device, the port is free, the tunnel's own networks overlap
// nothing here or in another tunnel, and the networks its peers would have
// routed (wg-quick routes every AllowedIPs entry) neither collide with the
// host nor contain the operator's address.
func wgRestorable(name string, conf *wgConf, confs map[string]*wgConf, host wgHostState, client string) error {
	if _, ok := confs[name]; ok {
		return fmt.Errorf("a tunnel called %s exists again; remove or rename it first", name)
	}
	if host.hasDevice(name) {
		return fmt.Errorf("a network device called %s exists", name)
	}
	sec := conf.iface()
	if port, err := strconv.Atoi(sec.get("listenport")); err == nil {
		if owner := wgPortsInConfs(confs)[port]; owner != "" {
			return fmt.Errorf("UDP port %d belongs to the WireGuard tunnel %s now", port, owner)
		}
		if wgUDPListening()[port] {
			return fmt.Errorf("UDP port %d is in use on this host now", port)
		}
	}
	others := wgSubnetsInConfs(confs)
	var clientAddr netip.Addr
	if client != "" {
		clientAddr, _ = ParseAddr(client)
	}
	check := func(p netip.Prefix, what string) error {
		if hp, hit := host.overlap(p, ""); hit {
			return fmt.Errorf("%s %s overlaps %s, which is %s", what, p, hp.prefix, hp.what)
		}
		for _, o := range others {
			if o.prefix.Overlaps(p) {
				return fmt.Errorf("%s %s overlaps %s, which is %s", what, p, o.prefix, o.what)
			}
		}
		if clientAddr.IsValid() && p.Contains(clientAddr) {
			return guarded("%s %s contains your own address (%s), so restoring it would cut your connection to the dashboard", what, p, clientAddr)
		}
		return nil
	}
	for _, raw := range sec.list("address") {
		if p, err := ParsePrefix(raw); err == nil {
			if err := check(p.Masked(), "its network"); err != nil {
				return err
			}
		}
	}
	for _, peer := range conf.peers() {
		for _, raw := range peer.list("allowedips") {
			p, err := ParsePrefix(raw)
			if err != nil || p.Bits() == 0 {
				continue
			}
			inside := false
			for _, a := range sec.list("address") {
				if own, err := ParsePrefix(a); err == nil && own.Masked().Contains(p.Addr()) && p.Bits() >= own.Bits() {
					inside = true
				}
			}
			if !inside {
				if err := check(p.Masked(), "a peer's network"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// RestoreWireGuard puts an archived tunnel back and starts it. The exit node
// and the firewall opening are not part of the file; the result says which
// exit was withdrawn at removal so it can be turned on again deliberately.
func (s *Service) RestoreWireGuard(ctx context.Context, file, client, actor string) (*WGServerResult, error) {
	if err := requireWG(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.readArchived(file)
	if err != nil {
		return nil, err
	}
	name := wgArchiveRe.FindStringSubmatch(file)[1]
	confs, err := s.listWGConfs()
	if err != nil {
		return nil, err
	}
	host, err := wgReadHostState(ctx)
	if err != nil {
		return nil, err
	}
	if err := wgRestorable(name, conf, confs, host, client); err != nil {
		return nil, err
	}
	var routed []netip.Prefix
	for _, peer := range conf.peers() {
		for _, raw := range peer.list("allowedips") {
			if p, err := ParsePrefix(raw); err == nil && p.Bits() > 0 {
				routed = append(routed, p.Masked())
			}
		}
	}
	endpoints, err := s.transportEndpoints(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, peer := range conf.peers() {
		if a := wgEndpointAddress(wgPeerOf(peer).endpoint); a.IsValid() {
			endpoints = append(endpoints, a)
		}
	}
	for _, r := range routed {
		for _, e := range endpoints {
			if r.Contains(e) {
				return nil, guarded("%s would route %s, which contains the WireGuard endpoint %s; restoring it would disconnect that tunnel", name, r, e)
			}
		}
	}
	var before Path
	if client != "" {
		if before, err = clientPath(ctx, client); err != nil {
			return nil, err
		}
	}

	// A row left under this name belongs to no peer of the restored file
	// unless its id and key match, and peerClient refuses those that do not;
	// dropping them first keeps no orphaned private key and changes nothing
	// on the host if it fails.
	if err := s.vpn.DeleteInterface(ctx, name); err != nil {
		return nil, err
	}
	src := filepath.Join(s.paths.WireGuard, wgRemovedDir, file)
	dest, err := s.wgConfPath(name)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(src, dest); err != nil {
		return nil, fmt.Errorf("moving %s back: %w", file, err)
	}
	unit := "wg-quick@" + name
	fail := func(cause error) (*WGServerResult, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		// The unit may not have started; what is left afterwards is checked.
		_, _ = run(cleanup, "systemctl", "disable", "--now", unit)
		var left []string
		if err := os.Rename(dest, src); err != nil {
			left = append(left, "the file could not be moved back into the archive: "+err.Error())
		}
		if state, _ := run(cleanup, "systemctl", "is-active", unit); strings.TrimSpace(state) == "active" {
			left = append(left, unit+" is still running")
		}
		e := WGEvent{Iface: name, Kind: "restore_failed", Outcome: wgOutcomeFailed, Actor: actor, Detail: cause.Error() + "; the archive was kept"}
		if len(left) > 0 {
			e.Outcome, e.Detail = wgOutcomeDegraded, cause.Error()+"; rollback incomplete: "+strings.Join(left, "; ")
		}
		s.wg.note(cleanup, e)
		return nil, cause
	}
	if _, err := run(ctx, "systemctl", "enable", "--now", unit); err != nil {
		return fail(fmt.Errorf("starting %s: %w", unit, err))
	}
	if client != "" {
		if err := verifyPath(before)(ctx); err != nil {
			return fail(err)
		}
	}
	res := &WGServerResult{Warnings: []string{"Saved client configurations were removed with the tunnel; its peers keep working with the configurations they already have."}}
	if withdrawn := s.withdrawnExit(ctx, name); withdrawn != "" {
		res.Warnings = append(res.Warnings, "Its "+withdrawn+" exit was withdrawn when it was removed and is not restored; turn it on again if clients need it.")
	}
	view, err := s.readWireGuard(ctx, name, false)
	if err != nil {
		return nil, err
	}
	for _, ifc := range view.Interfaces {
		if ifc.Name == name {
			res.Interface = ifc
		}
	}
	s.wg.note(ctx, WGEvent{Iface: name, Kind: "restored", Actor: actor, Detail: fmt.Sprintf("from %s with %d peers", file, len(conf.peers()))})
	return res, nil
}

// withdrawnExit reads the removal event for what exit the tunnel had.
func (s *Service) withdrawnExit(ctx context.Context, name string) string {
	events, err := s.wg.events(ctx, name, "", 50)
	if err != nil {
		return ""
	}
	for _, e := range events {
		if e.Kind != "removed" || e.Outcome != wgOutcomeOK {
			continue
		}
		if _, exit, ok := strings.Cut(e.Detail, "; its "); ok {
			return strings.TrimSuffix(exit, " exit was withdrawn")
		}
		return ""
	}
	return ""
}
