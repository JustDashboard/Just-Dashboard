package netx

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
)

func nativeVariantValue[T any](m map[string]nativeVariant, key, kind string, fallback T) (T, error) {
	v, found := m[key]
	if !found {
		return fallback, nil
	}
	var out T
	if v.Type != kind || json.Unmarshal(v.Data, &out) != nil {
		return out, errors.New("unreadable selected native property")
	}
	return out, nil
}

func nativeNMSettingsIntent(settings map[string]map[string]nativeVariant, p *nativeProfile) (NativeIntent, error) {
	var in NativeIntent
	connection := settings["connection"]
	uuid, err := nativeVariantValue(connection, "uuid", "s", "")
	if err != nil || uuid != p.UUID {
		return in, errors.New("native loaded connection identity differs")
	}
	name, err := nativeVariantValue(connection, "interface-name", "s", "")
	if err != nil || name != p.View.Device {
		return in, errors.New("native loaded device binding differs")
	}
	for i, f := range []*NativeFamilyIntent{&in.IPv4, &in.IPv6} {
		section := "ipv4"
		if i == 1 {
			section = "ipv6"
		}
		properties := settings[section]
		f.Method, err = nativeVariantValue(properties, "method", "s", "")
		if err != nil {
			return in, err
		}
		metric, err := nativeVariantValue(properties, "route-metric", "x", int64(-1))
		if err != nil || metric < -1 || metric > 1000000 {
			return in, errors.New("unsupported loaded native metric")
		}
		table, err := nativeVariantValue(properties, "route-table", "u", uint32(0))
		if err != nil {
			return in, err
		}
		if table == 0 {
			table = uint32(nativeRouteTable(p))
		}
		addresses, err := nativeVariantValue(properties, "address-data", "aa{sv}", []map[string]nativeVariant{})
		if err != nil || len(addresses) > 16 {
			return in, errors.New("unsupported loaded native addresses")
		}
		for _, address := range addresses {
			if len(address) != 2 {
				return in, errors.New("advanced loaded address properties require native review")
			}
			literal, e := nativeVariantValue(address, "address", "s", "")
			prefix, e2 := nativeVariantValue(address, "prefix", "u", uint32(0))
			if e != nil || e2 != nil {
				return in, errors.New("unreadable loaded address")
			}
			f.Addresses = append(f.Addresses, fmt.Sprintf("%s/%d", literal, prefix))
		}
		routes, err := nativeVariantValue(properties, "route-data", "aa{sv}", []map[string]nativeVariant{})
		if err != nil || len(routes) > 32 {
			return in, errors.New("unsupported loaded native routes")
		}
		for _, route := range routes {
			for key := range route {
				if !slices.Contains([]string{"dest", "prefix", "next-hop", "metric", "table"}, key) {
					return in, errors.New("advanced loaded route attributes require native review")
				}
			}
			dest, e := nativeVariantValue(route, "dest", "s", "")
			prefix, e2 := nativeVariantValue(route, "prefix", "u", uint32(0))
			gateway, e3 := nativeVariantValue(route, "next-hop", "s", "")
			rm, e4 := nativeVariantValue(route, "metric", "u", uint32(0))
			rt, e5 := nativeVariantValue(route, "table", "u", table)
			if e != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
				return in, errors.New("unreadable loaded route")
			}
			m := int(rm)
			if rm == 0 {
				m = int(metric)
			}
			f.Routes = append(f.Routes, NativeRoute{Destination: fmt.Sprintf("%s/%d", dest, prefix), Gateway: gateway, Metric: m, Table: int(rt)})
		}
		gateway, err := nativeVariantValue(properties, "gateway", "s", "")
		if err != nil {
			return in, err
		}
		if gateway != "" {
			dest := "0.0.0.0/0"
			if i == 1 {
				dest = "::/0"
			}
			f.Routes = append(f.Routes, NativeRoute{Destination: dest, Gateway: gateway, Metric: int(metric), Table: int(table)})
		}
		if i == 0 {
			dns, e := nativeVariantValue(properties, "dns", "au", []uint32{})
			if e != nil || len(dns) > 8 {
				return in, errors.New("unreadable loaded DNS")
			}
			for _, raw := range dns {
				var bytes [4]byte
				binary.NativeEndian.PutUint32(bytes[:], raw)
				f.DNS = append(f.DNS, netip.AddrFrom4(bytes).String())
			}
		} else {
			dns, e := nativeVariantValue(properties, "dns", "aay", [][]byte{})
			if e != nil || len(dns) > 8 {
				return in, errors.New("unreadable loaded DNS")
			}
			for _, raw := range dns {
				if len(raw) != 16 {
					return in, errors.New("unreadable loaded IPv6 DNS")
				}
				var bytes [16]byte
				copy(bytes[:], raw)
				f.DNS = append(f.DNS, netip.AddrFrom16(bytes).String())
			}
		}
		f.Domains, err = nativeVariantValue(properties, "dns-search", "as", []string{})
		if err != nil {
			return in, err
		}
		f.IgnoreAutoDNS, err = nativeVariantValue(properties, "ignore-auto-dns", "b", false)
		if err != nil {
			return in, err
		}
		f.IgnoreAutoRoutes, err = nativeVariantValue(properties, "ignore-auto-routes", "b", false)
		if err != nil {
			return in, err
		}
	}
	return normalizeNativeIntent(in)
}

func nativeIntentEqual(a, b NativeIntent) bool {
	// Native serializers may reorder addresses/routes. DNS/domain order remains
	// meaningful to the owner and is compared directly.
	for _, in := range []*NativeIntent{&a, &b} {
		for _, f := range []*NativeFamilyIntent{&in.IPv4, &in.IPv6} {
			f.Addresses = slices.Clone(f.Addresses)
			slices.Sort(f.Addresses)
			f.Routes = slices.Clone(f.Routes)
			slices.SortFunc(f.Routes, func(a, b NativeRoute) int {
				return slices.Compare([]string{a.Destination, a.Gateway, fmt.Sprint(a.Metric), fmt.Sprint(a.Table)}, []string{b.Destination, b.Gateway, fmt.Sprint(b.Metric), fmt.Sprint(b.Table)})
			})
		}
	}
	return reflect.DeepEqual(a, b)
}

func nativeNMLoadedIntent(ctx context.Context, p *nativeProfile, want NativeIntent) error {
	unsaved, err := nativeBusProperty[bool](ctx, nmService, p.ConnectionObject, nmService+".Settings.Connection", "Unsaved", "b")
	if err != nil || unsaved {
		return errors.New("native loaded profile has unsaved changes")
	}
	r, err := nativeBus(ctx, nmService, p.ConnectionObject, nmService+".Settings.Connection", "call", "GetSettings")
	if err != nil {
		return err
	}
	settings, err := nativeBusValue[map[string]map[string]nativeVariant](r, "a{sa{sv}}")
	if err != nil {
		return err
	}
	loaded, err := nativeNMSettingsIntent(settings, p)
	if err != nil || !nativeIntentEqual(loaded, want) {
		return errors.New("native saved and loaded supported intent differ")
	}
	r, err = nativeBus(ctx, nmService, p.DeviceObject, nmService+".Device", "call", "GetAppliedConnection", "u", "0")
	if err != nil || r.Type != "a{sa{sv}}t" || len(r.Data) != 2 || json.Unmarshal(r.Data[0], &settings) != nil {
		return errors.New("native applied connection evidence is unreadable")
	}
	applied, err := nativeNMSettingsIntent(settings, p)
	if err != nil || !nativeIntentEqual(applied, want) {
		return errors.New("native saved and applied supported intent differ")
	}
	return nil
}
