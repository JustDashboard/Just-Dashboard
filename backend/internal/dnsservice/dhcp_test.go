package dnsservice

import (
	"context"
	"errors"
	"testing"
)

func TestNativeDHCPInventoryAcrossEngines(t *testing.T) {
	adguard := map[string]any{
		"/control/status": filterAdGuardStatus(),
		"/control/dhcp/status": map[string]any{"enabled": true, "interface_name": "eth0",
			"v4":            map[string]any{"gateway_ip": "192.168.1.1", "subnet_mask": "255.255.255.0", "range_start": "192.168.1.100", "range_end": "192.168.1.200", "lease_duration": 86400},
			"v6":            map[string]any{"range_start": "", "lease_duration": 0},
			"leases":        []any{map[string]any{"mac": "AA:BB:CC:DD:EE:01", "ip": "192.168.1.101", "hostname": "laptop", "expires": "2026-10-10T12:00:00Z"}},
			"static_leases": []any{map[string]any{"mac": "aa:bb:cc:dd:ee:02", "ip": "192.168.1.10", "hostname": "nas"}}},
	}
	pihole := map[string]any{
		"/api/info/version": map[string]any{"version": map[string]any{"ftl": map[string]any{"local": map[string]any{"version": "v6.2.1"}}}},
		"/api/config/dhcp": map[string]any{"config": map[string]any{"dhcp": map[string]any{"active": false, "start": "192.168.0.10", "end": "192.168.0.250", "router": "192.168.0.1", "netmask": "0.0.0.0", "leaseTime": "24h", "ipv6": true,
			"hosts": []string{"11:22:33:44:55:66,192.168.0.123", "11:22:33:44:55:67,192.168.0.124,printer"}}}},
		"/api/dhcp/leases": map[string]any{"leases": []any{
			map[string]any{"expires": 1675671991, "name": "raspberrypi", "hwaddr": "00:00:00:00:00:01", "ip": "192.168.2.111", "clientid": "x"},
			map[string]any{"expires": 0, "name": "*", "hwaddr": "00:00:00:00:00:02", "ip": "192.168.2.112", "clientid": "y"}}},
	}
	technitium := map[string]any{
		"/api/settings/get":     map[string]any{"version": "15.6.0"},
		"/api/dhcp/scopes/list": map[string]any{"scopes": []any{map[string]any{"name": "Default", "enabled": false, "startingAddress": "192.168.1.1", "endingAddress": "192.168.1.254", "subnetMask": "255.255.255.0"}}},
		"/api/dhcp/leases/list": map[string]any{"leases": []any{map[string]any{"scope": "Default", "type": "Reserved", "hardwareAddress": "00-11-22-33-44-55", "clientIdentifier": "1-001122334455", "address": "192.168.1.5", "hostName": "server", "leaseObtained": "2026-10-09T10:00:00Z", "leaseExpires": "2026-10-10T10:00:00Z"}}},
	}
	for _, tc := range []struct {
		engine  Engine
		policy  map[string]any
		enabled bool
		leases  int
		check   func(*testing.T, *DHCPInventory)
	}{
		{AdGuard, adguard, true, 2, func(t *testing.T, i *DHCPInventory) {
			if i.Interface != "eth0" || len(i.Ranges) != 1 || i.Ranges[0].Start != "192.168.1.100" || i.Leases[0].Hardware != "aa:bb:cc:dd:ee:01" || i.Leases[0].Expires != "2026-10-10T12:00:00Z" || !i.Leases[1].Static {
				t.Fatalf("adguard = %+v", i)
			}
		}},
		{PiHole, pihole, false, 4, func(t *testing.T, i *DHCPInventory) {
			if len(i.Ranges) != 1 || i.Ranges[0].End != "192.168.0.250" || !i.Leases[1].Static || i.Leases[1].Hostname != "printer" || i.Leases[3].Hostname != "" || i.Leases[3].Expires != "" || i.Leases[2].Expires != "2023-02-06T08:26:31Z" {
				t.Fatalf("pihole = %+v", i)
			}
		}},
		{Technitium, technitium, false, 1, func(t *testing.T, i *DHCPInventory) {
			if len(i.Ranges) != 1 || i.Ranges[0].Name != "Default" || i.Ranges[0].Enabled == nil || *i.Ranges[0].Enabled || !i.Leases[0].Static || i.Leases[0].Hardware != "00:11:22:33:44:55" || i.Leases[0].Scope != "Default" {
				t.Fatalf("technitium = %+v", i)
			}
		}},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			req, mutations := filterFixture(t, tc.engine, tc.policy)
			i, err := inspectNativeDHCP(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if i.Enabled == nil || *i.Enabled != tc.enabled || len(i.Leases) != tc.leases || i.Configuration.State != "configured" || i.LeaseEvidence.State != "reported" || mutations.Load() != 0 {
				t.Fatalf("inventory = %+v (mutations %d)", i, mutations.Load())
			}
			tc.check(t, i)
		})
	}
}

// Malformed or missing native collections stay unknown: an empty table is
// never invented from a null, a bad address or a missing field.
func TestNativeDHCPMalformedSectionsStayUnknown(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"null leases": func(p map[string]any) { p["/control/dhcp/status"].(map[string]any)["leases"] = nil },
		"bad lease address": func(p map[string]any) {
			p["/control/dhcp/status"].(map[string]any)["leases"] = []any{map[string]any{"mac": "aa:bb:cc:dd:ee:01", "ip": "not-an-ip", "hostname": "x", "expires": ""}}
		},
		"bad hardware": func(p map[string]any) {
			p["/control/dhcp/status"].(map[string]any)["static_leases"] = []any{map[string]any{"mac": "zz", "ip": "192.168.1.9", "hostname": "x"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := map[string]any{
				"/control/status": filterAdGuardStatus(),
				"/control/dhcp/status": map[string]any{"enabled": false, "interface_name": "", "v4": map[string]any{"range_start": "", "range_end": ""}, "v6": map[string]any{"range_start": ""},
					"leases": []any{}, "static_leases": []any{}},
			}
			mutate(policy)
			req, _ := filterFixture(t, AdGuard, policy)
			i, err := inspectNativeDHCP(context.Background(), req)
			if err != nil || i.LeaseEvidence.State != "unknown" || len(i.Leases) != 0 || i.Configuration.State != "configured" {
				t.Fatalf("inventory = %+v, %v", i, err)
			}
		})
	}
	req, _ := filterFixture(t, AdGuard, map[string]any{"/control/status": filterAdGuardStatus()})
	if i, err := inspectNativeDHCP(context.Background(), req); err != nil || i.Configuration.State != "unknown" || i.LeaseEvidence.State != "unknown" {
		t.Fatalf("missing status = %+v, %v", i, err)
	}
	req, _ = filterFixture(t, AdGuard, map[string]any{"/control/status": map[string]any{"version": "v0.108.0", "running": true, "protection_enabled": true}})
	if _, err := inspectNativeDHCP(context.Background(), req); !errors.Is(err, errDHCPVersion) {
		t.Fatalf("unsupported version = %v", err)
	}
}
