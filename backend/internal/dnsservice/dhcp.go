package dnsservice

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// AdGuard Home, Pi-hole and Technitium can each be a network's DHCP server as
// well as its resolver, and the leases they hand out are the names their query
// logs show. This reads that half: whether the native DHCP server is enabled,
// the ranges or scopes it serves and its own lease table, bounded and
// read-only. Nothing here starts, stops or changes a DHCP server. Malformed or
// missing collections stay unknown rather than becoming an empty table.

const maxDHCPLeases = 256

var errDHCPVersion = errors.New("unsupported native DHCP version")

// DHCPRange is one range or scope the native server hands addresses from.
type DHCPRange struct {
	Name    string `json:"name,omitempty"`
	Family  string `json:"family"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// DHCPLease is one row of the native lease table.
type DHCPLease struct {
	Address  string `json:"address"`
	Hardware string `json:"hardware,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	// Expires is the native expiry, RFC 3339; empty for a static lease or one
	// the engine reports as never expiring.
	Expires string `json:"expires,omitempty"`
	Static  bool   `json:"static"`
	Scope   string `json:"scope,omitempty"`
}

// DHCPInventory is the native DHCP configuration and leases.
type DHCPInventory struct {
	Engine        Engine      `json:"engine"`
	NativeVersion string      `json:"nativeVersion"`
	ObservedAt    time.Time   `json:"observedAt"`
	Transport     Reading     `json:"transport"`
	Enabled       *bool       `json:"enabled,omitempty"`
	Configuration Reading     `json:"configuration"`
	Interface     string      `json:"interface,omitempty"`
	Ranges        []DHCPRange `json:"ranges"`
	Leases        []DHCPLease `json:"leases"`
	LeaseEvidence Reading     `json:"leaseEvidence"`
	Limitations   []string    `json:"limitations"`
}

// DHCPView is one connection's DHCP reading.
type DHCPView struct {
	Connection Connection     `json:"connection"`
	Inventory  *DHCPInventory `json:"inventory,omitempty"`
	// State is available, partial (a section is unknown), unavailable or
	// unsupported.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// DHCP reads one connection's native DHCP server. Read-only connections may
// read it: nothing is changed.
func (s *Service) DHCP(ctx context.Context, id string) (DHCPView, error) {
	if err := s.ready(); err != nil {
		return DHCPView{}, err
	}
	connection, req, err := s.connection(ctx, id)
	if err != nil {
		return DHCPView{}, err
	}
	view := DHCPView{Connection: connection, State: "unavailable"}
	view.Inventory, err = inspectNativeDHCP(ctx, req)
	if ctx.Err() != nil {
		return DHCPView{}, ctx.Err()
	}
	current, _, currentErr := s.connection(ctx, id)
	if currentErr != nil || current != connection {
		return DHCPView{}, ErrConflict
	}
	if err != nil {
		view.Error = "Native DHCP inventory is unavailable; no DHCP operation was sent."
		if errors.Is(err, errDHCPVersion) {
			view.State, view.Error = "unsupported", "The native engine version is outside the supported DHCP inventory contract."
		}
		return view, nil
	}
	view.State = "available"
	if view.Inventory.Configuration.State == "unknown" || view.Inventory.LeaseEvidence.State == "unknown" {
		view.State = "partial"
	}
	return view, nil
}

func inspectNativeDHCP(ctx context.Context, req ConnectionRequest) (*DHCPInventory, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := newNativeClient(req)
	if err != nil {
		return nil, err
	}
	defer c.close()
	if err = c.login(ctx); err != nil {
		return nil, err
	}
	defer c.logout()
	i := &DHCPInventory{Engine: req.Engine, ObservedAt: time.Now().UTC(), Ranges: []DHCPRange{}, Leases: []DHCPLease{},
		Configuration: Reading{"unknown", "unavailable", "Native DHCP configuration is unavailable or does not match its bounded contract."},
		LeaseEvidence: Reading{"unknown", "unavailable", "The native lease table is unavailable or does not match its bounded contract."},
		Limitations: []string{
			"Read-only: the dashboard does not start, stop or change a DHCP server.",
			"Lease rows are the engine's own table, not observed DHCP packets; a client can hold an address the table no longer lists.",
			"A DHCP server in a container on a bridge network does not reach the LAN's broadcast domain unless it runs with host networking.",
		}}
	switch req.Engine {
	case AdGuard:
		err = c.adGuardDHCP(ctx, i)
	case PiHole:
		err = c.piHoleDHCP(ctx, i)
	case Technitium:
		err = c.technitiumDHCP(ctx, i)
	default:
		err = errors.New("unsupported native DHCP engine")
	}
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	i.Transport = c.transport
	encoded, _ := json.Marshal(i)
	if len(encoded) > 192<<10 {
		return nil, errors.New("native DHCP inventory exceeds its retained bound")
	}
	return i, nil
}

// dhcpAddress is a literal address of the expected family, or empty. Native
// configurations leave unset ranges as empty strings.
func dhcpAddress(value string, v4 bool) (string, bool) {
	if value == "" {
		return "", true
	}
	a, err := netip.ParseAddr(value)
	if err != nil || a.Zone() != "" || a.Is4() != v4 {
		return "", false
	}
	return a.String(), true
}

func dhcpHardware(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) > 20 {
		return "", false
	}
	return mac.String(), true
}

func (c *nativeClient) dhcpLease(address, hardware, hostname, expires string, static bool, scope string) (DHCPLease, bool) {
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" {
		return DHCPLease{}, false
	}
	mac, ok := dhcpHardware(hardware)
	if !ok || len(hostname) > 253 || len(scope) > 255 {
		return DHCPLease{}, false
	}
	return DHCPLease{Address: ip.String(), Hardware: mac, Hostname: c.text(hostname), Expires: expires, Static: static, Scope: c.text(scope)}, true
}

func (c *nativeClient) adGuardDHCP(ctx context.Context, i *DHCPInventory) error {
	var status struct {
		Version string `json:"version"`
	}
	if err := c.request(ctx, http.MethodGet, "/control/status", nil, &status); err != nil {
		return err
	}
	if !versionSupported(status.Version, "0.107.") {
		return errDHCPVersion
	}
	i.NativeVersion = status.Version
	var raw struct {
		Enabled   *bool   `json:"enabled"`
		Interface *string `json:"interface_name"`
		V4        *struct {
			Start string `json:"range_start"`
			End   string `json:"range_end"`
		} `json:"v4"`
		V6 *struct {
			Start string `json:"range_start"`
		} `json:"v6"`
		Leases json.RawMessage `json:"leases"`
		Static json.RawMessage `json:"static_leases"`
	}
	if err := c.request(ctx, http.MethodGet, "/control/dhcp/status", nil, &raw); err != nil {
		return nil
	}
	if raw.Enabled == nil || raw.Interface == nil || len(*raw.Interface) > 64 || raw.V4 == nil || raw.V6 == nil {
		return nil
	}
	start4, ok4 := dhcpAddress(raw.V4.Start, true)
	end4, okEnd := dhcpAddress(raw.V4.End, true)
	start6, ok6 := dhcpAddress(raw.V6.Start, false)
	if !ok4 || !okEnd || !ok6 {
		return nil
	}
	i.Enabled, i.Interface = raw.Enabled, c.text(*raw.Interface)
	if start4 != "" || end4 != "" {
		i.Ranges = append(i.Ranges, DHCPRange{Family: "ipv4", Start: start4, End: end4})
	}
	if start6 != "" {
		i.Ranges = append(i.Ranges, DHCPRange{Family: "ipv6", Start: start6})
	}
	i.Configuration = Reading{"configured", "native_configuration", "AdGuard Home's DHCP setting, interface and ranges as it reports them."}
	leases := []DHCPLease{}
	for index, collection := range []json.RawMessage{raw.Leases, raw.Static} {
		var rows []struct {
			MAC      *string `json:"mac"`
			IP       *string `json:"ip"`
			Hostname *string `json:"hostname"`
			Expires  *string `json:"expires"`
		}
		if json.Unmarshal(collection, &rows) != nil || rows == nil || len(leases)+len(rows) > maxDHCPLeases {
			return nil
		}
		for _, row := range rows {
			if row.MAC == nil || row.IP == nil || row.Hostname == nil {
				return nil
			}
			expires := ""
			if index == 0 {
				if row.Expires == nil {
					return nil
				}
				if *row.Expires != "" {
					at, err := time.Parse(time.RFC3339Nano, *row.Expires)
					if err != nil {
						return nil
					}
					expires = at.UTC().Format(time.RFC3339)
				}
			}
			lease, ok := c.dhcpLease(*row.IP, *row.MAC, *row.Hostname, expires, index == 1, "")
			if !ok {
				return nil
			}
			leases = append(leases, lease)
		}
	}
	i.Leases = leases
	i.LeaseEvidence = Reading{"reported", "native_table", "Dynamic and static leases from AdGuard Home's own table."}
	return nil
}

func (c *nativeClient) piHoleDHCP(ctx context.Context, i *DHCPInventory) error {
	var version struct {
		Version struct {
			FTL struct {
				Local struct {
					Version string `json:"version"`
				} `json:"local"`
			} `json:"ftl"`
		} `json:"version"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/info/version", nil, &version); err != nil {
		return err
	}
	i.NativeVersion = version.Version.FTL.Local.Version
	if !versionSupported(i.NativeVersion, "6.") {
		return errDHCPVersion
	}
	var config struct {
		Config struct {
			DHCP *struct {
				Active *bool     `json:"active"`
				Start  *string   `json:"start"`
				End    *string   `json:"end"`
				IPv6   *bool     `json:"ipv6"`
				Hosts  *[]string `json:"hosts"`
			} `json:"dhcp"`
		} `json:"config"`
	}
	static := []DHCPLease{}
	if err := c.request(ctx, http.MethodGet, "/api/config/dhcp", nil, &config); err == nil {
		d := config.Config.DHCP
		if d != nil && d.Active != nil && d.Start != nil && d.End != nil && d.IPv6 != nil && d.Hosts != nil && len(*d.Hosts) <= maxDHCPLeases {
			start, okStart := dhcpAddress(*d.Start, true)
			end, okEnd := dhcpAddress(*d.End, true)
			valid := okStart && okEnd
			for _, host := range *d.Hosts {
				// FTL's static hosts are dnsmasq dhcp-host values: MAC,IP[,name].
				parts := strings.Split(host, ",")
				if len(parts) < 2 || len(parts) > 3 {
					valid = false
					break
				}
				name := ""
				if len(parts) == 3 {
					name = parts[2]
				}
				lease, ok := c.dhcpLease(strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0]), strings.TrimSpace(name), "", true, "")
				if !ok {
					valid = false
					break
				}
				static = append(static, lease)
			}
			if valid {
				i.Enabled = d.Active
				i.Ranges = append(i.Ranges, DHCPRange{Family: "ipv4", Start: start, End: end})
				i.Configuration = Reading{"configured", "native_configuration", "FTL's dhcp.active, range and static hosts as it reports them."}
				if *d.IPv6 {
					i.Configuration.Summary += " IPv6 router advertisements and DHCPv6 are enabled on the same range's interface."
				}
			} else {
				static = []DHCPLease{}
			}
		}
	}
	var response struct {
		Leases json.RawMessage `json:"leases"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/dhcp/leases", nil, &response); err != nil {
		return nil
	}
	var rows []struct {
		Expires *json.Number `json:"expires"`
		Name    *string      `json:"name"`
		HWAddr  *string      `json:"hwaddr"`
		IP      *string      `json:"ip"`
	}
	if json.Unmarshal(response.Leases, &rows) != nil || rows == nil || len(rows)+len(static) > maxDHCPLeases {
		return nil
	}
	leases := append([]DHCPLease{}, static...)
	for _, row := range rows {
		if row.Expires == nil || row.Name == nil || row.HWAddr == nil || row.IP == nil {
			return nil
		}
		seconds, err := row.Expires.Int64()
		if err != nil || seconds < 0 {
			return nil
		}
		expires := ""
		if seconds > 0 {
			expires = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
		// A hostname of "*" is dnsmasq's word for none.
		name := *row.Name
		if name == "*" {
			name = ""
		}
		lease, ok := c.dhcpLease(*row.IP, *row.HWAddr, name, expires, false, "")
		if !ok {
			return nil
		}
		leases = append(leases, lease)
	}
	i.Leases = leases
	i.LeaseEvidence = Reading{"reported", "native_table", "Leases from FTL's dhcp.leases table, with configured static hosts."}
	return nil
}

func (c *nativeClient) technitiumDHCP(ctx context.Context, i *DHCPInventory) error {
	var settings struct {
		Version string `json:"version"`
	}
	if err := c.technitium(ctx, "/api/settings/get", nil, &settings); err != nil {
		return err
	}
	if !versionSupported(settings.Version, "15.") {
		return errDHCPVersion
	}
	i.NativeVersion = settings.Version
	var scopes struct {
		Scopes *[]struct {
			Name    *string `json:"name"`
			Enabled *bool   `json:"enabled"`
			Start   *string `json:"startingAddress"`
			End     *string `json:"endingAddress"`
		} `json:"scopes"`
	}
	if err := c.technitium(ctx, "/api/dhcp/scopes/list", nil, &scopes); err == nil && scopes.Scopes != nil && len(*scopes.Scopes) <= 64 {
		ranges := []DHCPRange{}
		enabled := false
		valid := true
		for _, scope := range *scopes.Scopes {
			if scope.Name == nil || scope.Enabled == nil || scope.Start == nil || scope.End == nil || len(*scope.Name) > 255 {
				valid = false
				break
			}
			start, okStart := dhcpAddress(*scope.Start, true)
			end, okEnd := dhcpAddress(*scope.End, true)
			if !okStart || !okEnd {
				valid = false
				break
			}
			on := *scope.Enabled
			enabled = enabled || on
			ranges = append(ranges, DHCPRange{Name: c.text(*scope.Name), Family: "ipv4", Start: start, End: end, Enabled: &on})
		}
		if valid {
			i.Ranges, i.Enabled = ranges, &enabled
			i.Configuration = Reading{"configured", "native_configuration", "Technitium's DHCP scopes; the server serves the enabled ones."}
		}
	}
	var leases struct {
		Leases *[]struct {
			Scope    *string `json:"scope"`
			Type     *string `json:"type"`
			Hardware *string `json:"hardwareAddress"`
			Address  *string `json:"address"`
			Hostname *string `json:"hostName"`
			Expires  *string `json:"leaseExpires"`
		} `json:"leases"`
	}
	if err := c.technitium(ctx, "/api/dhcp/leases/list", nil, &leases); err != nil || leases.Leases == nil || len(*leases.Leases) > maxDHCPLeases {
		return nil
	}
	rows := []DHCPLease{}
	for _, row := range *leases.Leases {
		if row.Scope == nil || row.Type == nil || row.Hardware == nil || row.Address == nil || row.Expires == nil {
			return nil
		}
		hostname := ""
		if row.Hostname != nil {
			hostname = *row.Hostname
		}
		expires := ""
		if *row.Expires != "" {
			at, err := time.Parse(time.RFC3339Nano, *row.Expires)
			if err != nil {
				return nil
			}
			expires = at.UTC().Format(time.RFC3339)
		}
		lease, ok := c.dhcpLease(*row.Address, *row.Hardware, hostname, expires, *row.Type == "Reserved", *row.Scope)
		if !ok {
			return nil
		}
		rows = append(rows, lease)
	}
	i.Leases = rows
	i.LeaseEvidence = Reading{"reported", "native_table", "Dynamic and reserved leases from Technitium's own table."}
	return nil
}
