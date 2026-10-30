package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// The credentials saved for certbot's DNS-01 plugins also write a
// deployment's address record (dnsrecords). These are the proxy's half of
// that: which providers have something saved, what is saved for one, read
// for a single call and kept by nobody, and whose nameservers a zone names
// when two providers both hold it.

// SavedDNSProviderKeys are the providers with credentials saved under
// Certificates, in the page's own order.
func SavedDNSProviderKeys() []string {
	keys := []string{}
	for _, provider := range savedDNSProviders() {
		keys = append(keys, provider.Key)
	}
	return keys
}

// SavedDNSCredentials reads what the Certificates page saved for a
// provider. The file stays certbot's, 0600 inside its tree; the caller uses
// the content for one request and keeps none of it, and nothing here logs
// or returns it to a client.
func SavedDNSCredentials(key string) (string, error) {
	provider, ok := DNSProviderFor(key)
	if !ok {
		return "", fmt.Errorf("%q is not a DNS provider this dashboard supports", key)
	}
	file, err := os.Open(credentialsPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s has no credentials saved under %s", provider.Name, certificatesPage)
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 64*1024 {
		return "", fmt.Errorf("the credentials saved for %s are unexpectedly large", provider.Name)
	}
	return string(raw), nil
}

// ZoneHostedBy answers which provider the nameservers of the zone holding
// name belong to, by the same lookup a wildcard's challenge uses, and ""
// when they name none of the providers this dashboard drives.
func ZoneHostedBy(ctx context.Context, name string) string {
	_, hosts := zoneNameservers(ctx, name, deploymentNSLookup)
	found := ""
	for _, provider := range dnsProviders {
		for _, host := range hosts {
			if provider.nameservers != nil && provider.nameservers.MatchString(host) {
				if found != "" && found != provider.Key {
					return ""
				}
				found = provider.Key
			}
		}
	}
	return found
}
