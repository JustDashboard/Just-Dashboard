package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const nmService = "org.freedesktop.NetworkManager"
const nmObject = "/org/freedesktop/NetworkManager"
const networkdService = "org.freedesktop.network1"

type nativeProfile struct {
	View             NativeProfileView
	File             nativeProfileFile
	Device           ipLink
	UUID             string
	DeviceObject     string
	ConnectionObject string
	OwnerBus         string
	BusID            string
	TransportGUID    string
	BootID           string
	NetplanID        string
	NetplanSection   string
	Generated        nativeProfileFile
	Sources          []nativeProfileFile
}

func nativeServiceOwned(ctx context.Context, name string) (bool, error) {
	r, err := nativeBus(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "call", "NameHasOwner", "s", name)
	if err != nil {
		return false, err
	}
	return nativeBusValue[bool](r, "b")
}

func nativeOwnerIdentity(ctx context.Context, name string) (string, error) {
	r, err := nativeBus(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "call", "GetNameOwner", "s", name)
	if err != nil {
		return "", err
	}
	owner, err := nativeBusValue[string](r, "s")
	if err != nil || !nativeBusOwner.MatchString(owner) {
		return "", errors.New("native owner identity is unreadable")
	}
	return owner, nil
}

func nativeCapability(ctx context.Context, manager string) NativeManagerCapability {
	c := NativeManagerCapability{Manager: manager, Status: "unknown", Edits: []string{}}
	var out string
	var err error
	switch manager {
	case "NetworkManager":
		owned, readErr := nativeServiceOwned(ctx, nmService)
		if readErr != nil {
			c.Reason = "NetworkManager bus ownership could not be read."
			return c
		}
		if !owned {
			c.Status, c.Reason = "absent", "NetworkManager has no running native D-Bus owner."
			return c
		}
		c.Version, err = nativeBusProperty[string](ctx, nmService, nmObject, nmService, "Version", "s")
		if err == nil {
			out, err = nativeExecute(ctx, nil, "nmcli", "--version")
			if err == nil && !strings.Contains(out, c.Version) {
				err = errors.New("manager/client version mismatch")
			}
		}
	case "networkd":
		owned, readErr := nativeServiceOwned(ctx, networkdService)
		if readErr != nil {
			c.Reason = "networkd bus ownership could not be read."
			return c
		}
		if !owned {
			c.Status, c.Reason = "absent", "systemd-networkd has no running native D-Bus owner."
			return c
		}
		out, err = nativeExecute(ctx, nil, "networkctl", "--version")
		c.Version = strings.Split(strings.TrimSpace(out), "\n")[0]
	case "netplan":
		// Netplan's info command exposes features but no release version on
		// supported installs. Unknown package metadata stays a refusal.
		out, err = nativeExecute(ctx, nil, "dpkg-query", "-W", "-f=${Version}", "netplan.io")
		c.Version = strings.TrimSpace(out)
		if err != nil && has("rpm") {
			out, err = nativeExecute(ctx, nil, "rpm", "-q", "--qf", "%{VERSION}", "netplan")
			c.Version = strings.TrimSpace(out)
		}
		if err == nil {
			_, err = nativeExecute(ctx, nil, "netplan", "info", "--json")
		}
	}
	if err != nil {
		c.Reason = "The native owner/client version or required inspection feature could not be verified."
		return c
	}
	if !nativeVersionSupported(manager, c.Version) {
		c.Status, c.Reason = "unsupported", "This native owner version is outside the verified adapter range."
		return c
	}
	c.Status = "supported"
	c.Edits = []string{"existing static/automatic addresses", "per-link DNS and domains", "explicit unicast routes"}
	return c
}

func nativeLinks(ctx context.Context) ([]ipLink, error) {
	out, err := nativeExecute(ctx, nil, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return nil, err
	}
	var links []ipLink
	if json.Unmarshal([]byte(out), &links) != nil || links == nil || len(links) > 256 {
		return nil, errors.New("native device inventory is unreadable or exceeds its bound")
	}
	return links, nil
}

func nativeLinkKind(l ipLink) string {
	if l.LinkInfo.InfoKind != "" {
		return l.LinkInfo.InfoKind
	}
	if l.LinkType == "loopback" {
		return "loopback"
	}
	return "physical"
}

func (s *Service) NativeManagers(ctx context.Context) (*NativeManagerView, error) {
	v := &NativeManagerView{CheckedAt: time.Now().UTC(), Devices: []NativeDeviceSummary{}, Warnings: []string{}}
	for _, manager := range []string{"NetworkManager", "networkd", "netplan"} {
		v.Capabilities = append(v.Capabilities, nativeCapability(ctx, manager))
	}
	links, err := nativeLinks(ctx)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.IfName != "lo" {
			v.Devices = append(v.Devices, NativeDeviceSummary{Name: link.IfName, Kind: nativeLinkKind(link), MAC: link.Address})
		}
	}
	return v, nil
}

type nativeNetworkdStatus struct {
	Name                string `json:"Name"`
	NetworkFile         string `json:"NetworkFile"`
	AdministrativeState string `json:"AdministrativeState"`
}

func nativeNetworkdSelection(ctx context.Context, device string) (string, error) {
	out, err := nativeExecute(ctx, nil, "networkctl", "--no-pager", "--json=short", "status", device)
	if err != nil {
		return "", err
	}
	var state nativeNetworkdStatus
	if json.Unmarshal([]byte(out), &state) != nil || state.Name != device || state.AdministrativeState == "" {
		return "", errors.New("networkd selected profile evidence is unreadable")
	}
	if state.AdministrativeState == "unmanaged" {
		return "", nil
	}
	if state.NetworkFile == "" {
		return "", errors.New("networkd claims the device without a readable selected profile")
	}
	return state.NetworkFile, nil
}

func nativeNMSelection(ctx context.Context, device string) (profile, uuid, deviceObject, connectionObject string, err error) {
	r, e := nativeBus(ctx, nmService, nmObject, nmService, "call", "GetDeviceByIpIface", "s", device)
	if e != nil {
		err = e
		return
	}
	deviceObject, err = nativeBusValue[string](r, "o")
	if err != nil || !strings.HasPrefix(deviceObject, nmObject+"/Devices/") {
		err = errors.New("unreadable NetworkManager device identity")
		return
	}
	managed, e := nativeBusProperty[bool](ctx, nmService, deviceObject, nmService+".Device", "Managed", "b")
	if e != nil {
		err = e
		return
	}
	if !managed {
		return
	}
	active, e := nativeBusProperty[string](ctx, nmService, deviceObject, nmService+".Device", "ActiveConnection", "o")
	if e != nil {
		err = e
		return
	}
	if active == "/" {
		err = errors.New("NetworkManager has no active existing profile for this device")
		return
	}
	connectionObject, err = nativeBusProperty[string](ctx, nmService, active, nmService+".Connection.Active", "Connection", "o")
	if err != nil {
		return
	}
	uuid, err = nativeBusProperty[string](ctx, nmService, active, nmService+".Connection.Active", "Uuid", "s")
	if err != nil {
		return
	}
	profile, err = nativeBusProperty[string](ctx, nmService, connectionObject, nmService+".Settings.Connection", "Filename", "s")
	return
}

func nativeContract(link ipLink, links []ipLink) NativeContract {
	c := NativeContract{Master: link.Master, Members: []string{}}
	for _, other := range links {
		if other.Master == link.IfName {
			c.Members = append(c.Members, other.IfName)
		}
	}
	slices.Sort(c.Members)
	var data struct {
		Mode  string `json:"mode"`
		Table int    `json:"table"`
	}
	_ = json.Unmarshal(link.LinkInfo.InfoData, &data)
	if nativeLinkKind(link) == "bond" {
		c.BondMode = data.Mode
	}
	if nativeLinkKind(link) == "vrf" {
		c.VRFTable = data.Table
	}
	return c
}

func (s *Service) NativeProfile(ctx context.Context, device string) (*NativeProfileView, error) {
	p, err := s.readNativeProfile(ctx, device)
	if p != nil {
		return &p.View, nil
	}
	return nil, err
}

func (s *Service) readNativeProfile(ctx context.Context, device string) (*nativeProfile, error) {
	if err := ValidIfName(device); err != nil {
		return nil, err
	}
	p := &nativeProfile{View: NativeProfileView{CheckedAt: time.Now().UTC(), Device: device, Owner: "unknown", Coverage: []string{}, Contract: NativeContract{Members: []string{}}, Configured: NativeEvidence{Status: "unknown"}, Runtime: NativeEvidence{Status: "unknown"}, Boot: NativeEvidence{Status: "unknown"}}}
	refuse := func(reason string) (*nativeProfile, error) { p.View.Refusal = reason; return p, nil }
	links, err := nativeLinks(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, l := range links {
		if l.IfName == device {
			p.Device = l
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	p.View.Kind = nativeLinkKind(p.Device)
	p.View.Contract = nativeContract(p.Device, links)
	if !slices.Contains([]string{"physical", "dummy", "vlan", "bridge", "bond", "vrf"}, p.View.Kind) || strings.HasPrefix(device, "tailscale") || isDockerBridgeName(device) || device == "docker0" {
		return refuse("This device remains with its existing subsystem; this native profile adapter does not edit it.")
	}
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	for _, owned := range sp.Links {
		if owned.Name == device {
			return refuse("This device belongs to the dashboard's managed spec; use its interface controls.")
		}
	}
	nm, nwd := nativeCapability(ctx, "NetworkManager"), nativeCapability(ctx, "networkd")
	if nm.Status == "unknown" || nwd.Status == "unknown" {
		return refuse("Native manager ownership could not be read completely; refresh or review the native owner.")
	}
	var nmPath, nwPath string
	if nm.Status != "absent" {
		nmPath, p.UUID, p.DeviceObject, p.ConnectionObject, err = nativeNMSelection(ctx, device)
		if err != nil {
			return refuse("NetworkManager's active profile identity could not be read; review it through its native owner.")
		}
	}
	if nwd.Status != "absent" {
		nwPath, err = nativeNetworkdSelection(ctx, device)
		if err != nil {
			return refuse("networkd's selected profile could not be read; review it through its native owner.")
		}
	}
	if nmPath != "" && nwPath != "" {
		return refuse("Both NetworkManager and networkd claim this device; resolve ownership through the native owners first.")
	}
	if nmPath == "" && nwPath == "" {
		return refuse("No existing active native profile owns this device; adoption is not supported.")
	}
	selected := nwPath
	if nmPath != "" {
		p.View.Owner, p.View.Version, p.View.Renderer, selected = "NetworkManager", nm.Version, "NetworkManager", nmPath
		if nm.Status != "supported" {
			return refuse(nm.Reason)
		}
	} else {
		p.View.Owner, p.View.Version, p.View.Renderer = "networkd", nwd.Version, "networkd"
		if nwd.Status != "supported" {
			return refuse(nwd.Reason)
		}
	}
	if strings.HasPrefix(selected, "/run/systemd/network/10-netplan-") || strings.HasPrefix(selected, "/run/NetworkManager/system-connections/netplan-") {
		generated, readErr := nativeReadProfile(selected)
		if readErr != nil {
			return refuse("The selected generated native profile is unreadable or has unsafe ownership.")
		}
		p.Generated = *generated
		p.Generated.Path = selected
		capability := nativeCapability(ctx, "netplan")
		p.View.Owner, p.View.Version = "netplan", capability.Version
		if capability.Status != "supported" {
			return refuse(capability.Reason)
		}
		if err := nativeNetplanOrigin(p, selected); err != nil {
			return refuse(err.Error())
		}
		selected = p.File.Path
	}
	if !nativeProfilePath(p.View.Owner, selected) {
		return refuse("The active profile is vendor-owned, generated, or runtime-only; no persistent profile is created alongside it.")
	}
	ownerName := networkdService
	if p.View.Renderer == "NetworkManager" {
		ownerName = nmService
	}
	p.OwnerBus, err = nativeOwnerIdentity(ctx, ownerName)
	if err != nil {
		return refuse("The native owner's current process identity could not be pinned.")
	}
	p.BusID, err = nativeBusIdentity(ctx)
	if err != nil {
		return refuse("The native owner's current bus identity could not be pinned.")
	}
	p.TransportGUID, err = nativeTransportIdentity(ctx)
	if err != nil {
		return refuse("The native bus authentication identity could not be pinned.")
	}
	ctx = nativePinnedBus(ctx, p.TransportGUID)
	p.BootID, err = nativeBootIdentity()
	if err != nil {
		return refuse("The native owner's current boot identity could not be pinned.")
	}
	if p.File.Data == nil {
		file, err := nativeReadProfile(selected)
		if err != nil {
			return refuse("The selected native profile is unreadable or changed while being read.")
		}
		p.File = *file
		p.File.Path = selected
	}
	if strings.Contains(strings.ToLower(string(p.File.Data)), "cloud-init") {
		return refuse("cloud-init regenerates this profile; change that owner before enabling persistent edits here.")
	}
	if p.View.Owner == "networkd" {
		for _, root := range []string{"/etc/systemd/network", "/run/systemd/network", "/usr/lib/systemd/network"} {
			entries, e := os.ReadDir(nativeHostPath(filepath.Join(root, filepath.Base(selected)+".d")))
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				return refuse("Native profile drop-ins could not be read.")
			}
			if len(entries) > 0 {
				return refuse("Native profile drop-ins require review through networkd; this adapter cannot combine their ownership safely.")
			}
		}
	}
	var intent NativeIntent
	switch p.View.Owner {
	case "NetworkManager":
		intent, err = parseNativeNM(p.File.Data, p)
	case "networkd":
		intent, err = parseNativeNetworkd(p.File.Data, p)
	case "netplan":
		intent, err = parseNativeNetplan(p.File.Data, p)
	}
	if err != nil {
		return refuse("The existing native profile contains unsupported or ambiguous address/DNS/route properties; review it through its native owner.")
	}
	p.View.Intent = &intent
	p.View.Profile = filepath.Base(selected)
	p.View.Coverage = []string{"existing persistent native profile", "IPv4/IPv6 addressing methods", "per-link DNS/domains", "explicit unicast routes", "observed native relationships"}
	p.View.Generation = nativeGeneration(p)
	p.View.Configured = NativeEvidence{Status: "matching", Reason: "Supported intent was read from the existing selected native profile."}
	if p.View.Renderer == "NetworkManager" {
		if err := nativeNMLoadedIntent(ctx, p, intent); err != nil {
			p.View.Configured = NativeEvidence{Status: "drift", Reason: "The active/loaded native intent differs from its saved profile, or could not be read completely."}
			return refuse("Review and synchronize the profile through NetworkManager before retrying; no native profile was adopted.")
		}
	}
	p.View.Runtime = readNativeRuntime(ctx, p, intent)
	unit := "systemd-networkd.service"
	if p.View.Renderer == "NetworkManager" {
		unit = "NetworkManager.service"
	}
	enabled, e := nativeExecute(ctx, nil, "systemctl", "--root=/", "is-enabled", unit)
	if e == nil && strings.TrimSpace(enabled) == "enabled" {
		p.View.Boot = NativeEvidence{Status: "matching", Reason: "The existing persistent profile's native owner is enabled; no reboot was measured."}
	} else {
		p.View.Boot.Reason = "The native owner's persistent boot enablement could not be verified."
	}
	p.View.Editable = p.View.Runtime.Status == "matching" && p.View.Boot.Status == "matching"
	ownerNow, ownerErr := nativeOwnerIdentity(ctx, ownerName)
	busNow, busErr := nativeBusIdentity(ctx)
	if ownerErr != nil || ownerNow != p.OwnerBus || busErr != nil || busNow != p.BusID {
		p.View.Editable = false
		p.View.Refusal = "The native manager restarted during inspection; refresh the current owner before editing."
		return p, nil
	}
	if !p.View.Editable {
		p.View.Refusal = "Current runtime/profile or native boot ownership is not verified; resolve it through the native owner before editing."
	}
	if err := nativeStructureGuard(p); err != nil {
		p.View.Editable = false
		p.View.Refusal = err.Error()
	}
	fieldErr := nativeProfileFieldsGuard(p, p.File.Data, p.View.Renderer)
	if p.View.Owner == "netplan" {
		fieldErr = nativeNetplanFieldsGuard(p)
	}
	if fieldErr != nil {
		p.View.Editable = false
		p.View.Refusal = fieldErr.Error()
	}
	if p.View.Renderer == "NetworkManager" {
		if err := nativeNMOriginGuard(ctx, p.OwnerBus); err != nil {
			p.View.Editable = false
			p.View.Refusal = err.Error()
		}
	}
	return p, nil
}

func nativeGeneration(p *nativeProfile) string {
	meta, _ := json.Marshal(struct {
		Owner, Renderer, Version, Path, UUID, MAC, Kind, OwnerBus, BusID, BootID, TransportGUID string
		DeviceObject, ConnectionObject                                                          string
		IfIndex                                                                                 int
		Identity                                                                                nativeFileIdentity
		Contract                                                                                NativeContract
	}{Owner: p.View.Owner, Renderer: p.View.Renderer, Version: p.View.Version, Path: p.File.Path, UUID: p.UUID, MAC: p.Device.Address, Kind: p.View.Kind, OwnerBus: p.OwnerBus, BusID: p.BusID, BootID: p.BootID, TransportGUID: p.TransportGUID, DeviceObject: p.DeviceObject, ConnectionObject: p.ConnectionObject, IfIndex: p.Device.IfIndex, Identity: p.File.Identity, Contract: p.View.Contract})
	meta = append(meta, p.File.Data...)
	if p.View.Owner == "netplan" {
		generated, _ := json.Marshal(p.Generated)
		sources, _ := json.Marshal(p.Sources)
		meta = append(append(meta, generated...), sources...)
	}
	return digestBytes(meta)
}
