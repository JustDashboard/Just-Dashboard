package netsec

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// hostRoot is where the host's own filesystem is mounted in the container;
// firewalld's shipped zones live under /usr, which is not mounted directly.
// A variable so tests can point it at fixtures.
var hostRoot = "/host"

// zoneDefaults is a shipped zone's target and the rules it admits.
type zoneDefaults struct {
	target string
	rules  []Rule
}

// firewalldZoneDefaults reads the zone firewalld ships, which is what
// --load-zone-defaults restores. A zone the distribution does not ship has
// no defaults; a shipped rich rule is listed with a form the access checks
// treat as unknown rather than ignore.
func firewalldZoneDefaults(zone string) (zoneDefaults, error) {
	if !zoneNameRe.MatchString(zone) {
		return zoneDefaults{}, fmt.Errorf("%q is not a zone name", zone)
	}
	raw, err := os.ReadFile(filepath.Join(hostRoot, "usr", "lib", "firewalld", "zones", zone+".xml"))
	if err != nil {
		return zoneDefaults{}, readOnly(fmt.Sprintf("zone %s has no shipped defaults to reload (%v); reset applies only to the zones firewalld ships", zone, err))
	}
	var doc struct {
		Target   string `xml:"target,attr"`
		Services []struct {
			Name string `xml:"name,attr"`
		} `xml:"service"`
		Ports []struct {
			Protocol string `xml:"protocol,attr"`
			Port     string `xml:"port,attr"`
		} `xml:"port"`
		Rules []struct {
			Family string `xml:"family,attr"`
		} `xml:"rule"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return zoneDefaults{}, fmt.Errorf("zone %s's shipped definition could not be read: %w", zone, err)
	}
	d := zoneDefaults{target: firstNonBlank(doc.Target, "default"), rules: []Rule{}}
	for _, svc := range doc.Services {
		if !appNameRe.MatchString(svc.Name) {
			continue
		}
		d.rules = append(d.rules, Rule{Action: "ALLOW", Direction: "IN", From: "Anywhere", To: svc.Name, Service: svc.Name,
			Handle: "service:" + svc.Name, Raw: "service " + svc.Name, BothFamilies: true})
	}
	for _, p := range doc.Ports {
		spec := p.Port + "/" + strings.ToLower(p.Protocol)
		r := Rule{Action: "ALLOW", Direction: "IN", From: "Anywhere", To: spec, Handle: "port:" + spec, Raw: "port " + spec,
			Port: p.Port, Protocol: strings.ToLower(p.Protocol), BothFamilies: true}
		d.rules = append(d.rules, r)
	}
	for range doc.Rules {
		d.rules = append(d.rules, Rule{Action: "UNKNOWN", Direction: "IN", From: "Anywhere", To: "any", Raw: "shipped rich rule", BothFamilies: true})
	}
	return d, nil
}

// Reset reloads the default zone's shipped settings: firewalld's nearest
// equivalent of `ufw reset`. Only a shipped zone has defaults; the access
// guard has already judged the result against the zone file it reads.
func (b firewalldBackend) Reset(ctx context.Context) (string, error) {
	zone := b.zone(ctx)
	if _, err := firewalldZoneDefaults(zone); err != nil {
		return "", err
	}
	if _, err := run(ctx, "firewall-cmd", "--permanent", "--load-zone-defaults="+zone); err != nil {
		return "", err
	}
	return run(ctx, "firewall-cmd", "--reload")
}
