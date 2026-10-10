package dnsservice

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

func nativeSeedFiles(s provisionSpec, subnet string) (map[string][]byte, error) {
	files := map[string][]byte{}
	switch s.Request.Engine {
	case AdGuard:
		// The pinned native schema accepts bcrypt hashes. Seed a complete identity
		// before the first listener starts, with only declared upstreams and no
		// default downloaded filter list or unauthenticated installer interval.
		hash, err := bcrypt.GenerateFromPassword([]byte(s.Request.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, errors.New("native AdGuard password could not be hashed")
		}
		config := map[string]any{"schema_version": 32, "http": map[string]any{"address": "0.0.0.0:3000"}, "users": []map[string]string{{"name": s.Request.Username, "password": string(hash)}}, "dns": map[string]any{"bind_hosts": []string{"0.0.0.0"}, "port": 53, "upstream_dns": s.Request.Upstreams, "bootstrap_dns": []string{}, "fallback_dns": []string{}, "allowed_clients": []string{subnet}, "disallowed_clients": []string{}, "blocked_hosts": []string{}, "use_private_ptr_resolvers": false}, "filters": []any{}, "whitelist_filters": []any{}, "clients": map[string]any{"persistent": []any{}, "runtime_sources": map[string]bool{"whois": false, "arp": false, "rdns": false, "dhcp": false, "hosts": true}}}
		encoded, err := yaml.Marshal(config)
		if err != nil {
			return nil, err
		}
		files["opt/adguardhome/conf/AdGuardHome.yaml"] = encoded
	case PiHole:
		upstreams := make([]string, len(s.Request.Upstreams))
		for i, endpoint := range s.Request.Upstreams {
			ap, _ := netip.ParseAddrPort(endpoint)
			upstreams[i] = strconv.Quote(ap.Addr().String() + "#" + strconv.Itoa(int(ap.Port())))
		}
		// Environment-forced DNS fields become read-only in FTL. Native TOML
		// keeps the reviewed upstream controls writable after first boot.
		files["etc/pihole/pihole.toml"] = []byte("[dns]\nupstreams = [" + strings.Join(upstreams, ",") + "]\nlisteningMode = \"ALL\"\n[webserver]\nport = \"80\"\n")
		files["etc/pihole/adlists.list"] = []byte{}
		files["run/secrets/jd_dns_password"] = []byte(s.Request.Password)
	case Technitium:
		files["run/secrets/jd_dns_password"] = []byte(s.Request.Password)
	default:
		return nil, errors.New("unsupported native DNS seed")
	}
	return files, nil
}
