package proxysvc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A wildcard certificate cannot be issued over HTTP.
//
// Let's Encrypt will only sign *.example.com against a DNS challenge, and a
// domain behind Cloudflare's proxy cannot answer an HTTP challenge at all
// because the request never reaches this host. Between them that is most of
// the certificates people actually want and could not get from this page —
// the single biggest gap in the certificate feature as it shipped.
//
// The mechanism is a certbot plugin plus a credentials file. The plugin is the
// host's to install; what the dashboard adds is somewhere to put the
// credentials, an argv that is correct for each provider, and the propagation
// wait that is the commonest reason a DNS challenge fails on the first try.

// DNSProvider is one certbot DNS plugin this dashboard knows how to drive.
type DNSProvider struct {
	Key string `json:"key"`
	// Name is what the operator picks.
	Name string `json:"name"`
	// Plugin is the certbot argument, which is also the package name.
	Plugin string `json:"plugin"`
	// Installed reports whether the plugin is present on this host. Offering a
	// provider that cannot run produces certbot's own error three screens
	// later, which nobody reads as "install the plugin".
	Installed bool `json:"installed"`
	// Credentials describes what goes in the file, in the provider's own
	// wording, because every one of them spells it differently.
	Credentials string `json:"credentials"`
	// DefaultWait is the propagation delay in seconds. The defaults are
	// generous on purpose: a challenge that fails because the record had not
	// spread yet looks exactly like a wrong API token.
	DefaultWait int `json:"defaultWait"`
	// HasCredentials reports that a token is saved for this provider. The
	// token itself is never read back; whether one exists is the whole of
	// what the page shows, and the reason a "remove" control can exist.
	HasCredentials bool `json:"hasCredentials"`
}

// dnsProviders is a closed set. Each entry is an argv this code is willing to
// build, and certbot has dozens of plugins whose credential formats differ in
// ways that cannot be guessed.
var dnsProviders = []DNSProvider{
	{
		Key: "cloudflare", Name: "Cloudflare", Plugin: "dns-cloudflare", DefaultWait: 30,
		Credentials: "dns_cloudflare_api_token = your-scoped-token\n\nCreate the token with Zone:DNS:Edit on the zone you are issuing for.",
	},
	{
		Key: "route53", Name: "AWS Route 53", Plugin: "dns-route53", DefaultWait: 30,
		Credentials: "[default]\naws_access_key_id = AKIA...\naws_secret_access_key = ...\n\nSaved credentials are used for dashboard issuance and renewal. Host certbot timers must set AWS_SHARED_CREDENTIALS_FILE=/etc/letsencrypt/jd-dns/route53.ini and AWS_PROFILE=default. Leave empty to use the machine IAM role.",
	},
	{
		Key: "digitalocean", Name: "DigitalOcean", Plugin: "dns-digitalocean", DefaultWait: 30,
		Credentials: "dns_digitalocean_token = your-personal-access-token",
	},
	{
		Key: "google", Name: "Google Cloud DNS", Plugin: "dns-google", DefaultWait: 60,
		Credentials: "Paste the service-account JSON key here.",
	},
	{
		Key: "linode", Name: "Linode", Plugin: "dns-linode", DefaultWait: 120,
		Credentials: "dns_linode_key = your-api-key\ndns_linode_version = 4",
	},
	{
		Key: "ovh", Name: "OVH", Plugin: "dns-ovh", DefaultWait: 60,
		Credentials: "dns_ovh_endpoint = ovh-eu\ndns_ovh_application_key = ...\ndns_ovh_application_secret = ...\ndns_ovh_consumer_key = ...",
	},
	{
		Key: "gandi", Name: "Gandi", Plugin: "dns-gandi", DefaultWait: 30,
		Credentials: "dns_gandi_token = your-personal-access-token",
	},
	{
		Key: "rfc2136", Name: "RFC 2136 (BIND, Knot, PowerDNS)", Plugin: "dns-rfc2136", DefaultWait: 60,
		Credentials: "dns_rfc2136_server = 192.0.2.1\ndns_rfc2136_port = 53\ndns_rfc2136_name = keyname.\ndns_rfc2136_secret = base64secret\ndns_rfc2136_algorithm = HMAC-SHA512",
	},
}

// DNSProviderFor looks one up by key.
func DNSProviderFor(key string) (DNSProvider, bool) {
	for _, p := range dnsProviders {
		if p.Key == key {
			return p, true
		}
	}
	return DNSProvider{}, false
}

// dnsCredentialsDir is where credential files are kept. Inside certbot's own
// tree because that is the directory an operator already treats as secret and
// already backs up with the certificates it protects.
func dnsCredentialsDir() string {
	return filepath.Join(letsencryptDir, "jd-dns")
}

// credentialsPath is the file for one provider. One per provider rather than
// one per certificate: the credentials belong to the DNS account, and a
// certificate that later covers another domain in the same zone should not
// need them pasted again.
func credentialsPath(key string) string {
	return filepath.Join(dnsCredentialsDir(), key+".ini")
}

// DNSCredentials are a provider's credentials, checked and ready to save.
// Checking and saving are separate so an issuance can refuse a request
// without having written a token first.
type DNSCredentials struct {
	provider DNSProvider
	content  string
}

// CheckDNSCredentials validates what would be saved for a provider, writing
// nothing.
func CheckDNSCredentials(key, content string) (DNSCredentials, error) {
	provider, ok := DNSProviderFor(key)
	if !ok {
		return DNSCredentials{}, fmt.Errorf("%q is not a DNS provider this dashboard supports", key)
	}
	if strings.TrimSpace(content) == "" {
		return DNSCredentials{}, fmt.Errorf("%s needs its credentials before it can answer a challenge", provider.Name)
	}
	if len(content) > 64*1024 {
		return DNSCredentials{}, fmt.Errorf("credentials are unexpectedly large")
	}
	if key == "route53" {
		var err error
		content, err = normalizeRoute53Credentials(content)
		if err != nil {
			return DNSCredentials{}, err
		}
	}
	return DNSCredentials{provider: provider, content: content}, nil
}

// Provider is whose credentials these are.
func (c DNSCredentials) Provider() DNSProvider { return c.provider }

// Save stores the credentials at 0600 and returns where.
//
// certbot refuses to use a credentials file that is group- or world-readable,
// which is the one piece of file hygiene it enforces and the one people get
// wrong when they create the file by hand.
func (c DNSCredentials) Save() (string, error) {
	if err := os.MkdirAll(dnsCredentialsDir(), 0o700); err != nil {
		return "", err
	}
	path := credentialsPath(c.provider.Key)
	if err := persistDNSCredentials(path, c.content); err != nil {
		return "", err
	}
	return path, nil
}

// WriteDNSCredentials checks and stores a provider's credentials.
func WriteDNSCredentials(key, content string) (string, error) {
	credentials, err := CheckDNSCredentials(key, content)
	if err != nil {
		return "", err
	}
	return credentials.Save()
}

// HasDNSCredentials reports whether a provider is ready to use.
func HasDNSCredentials(key string) bool {
	st, err := os.Stat(credentialsPath(key))
	return err == nil && st.Size() > 0
}

// RemoveDNSCredentials deletes a provider's saved token. A token for a whole
// DNS zone that is no longer wanted should not sit on disk because the page
// had no way to say so; what it does not do is touch certbot's own renewal
// configuration, which keeps naming the file until the certificate is
// reissued another way.
func RemoveDNSCredentials(key string) error {
	provider, ok := DNSProviderFor(key)
	if !ok {
		return fmt.Errorf("%q is not a DNS provider this dashboard supports", key)
	}
	if err := os.Remove(credentialsPath(key)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s has no saved credentials", provider.Name)
		}
		return err
	}
	return nil
}

// ListDNSProviders reports the closed set with per-host detail filled in.
// Installed is read from the certbot that runs the jobs, so a plugin reported
// here is one an issuance can use; with no certbot at all, none is.
func (s *Service) ListDNSProviders(ctx context.Context) ([]DNSProvider, error) {
	rt, err := loadCertbotRuntime(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DNSProvider, 0, len(dnsProviders))
	for _, p := range dnsProviders {
		p.Installed = rt != nil && rt.authenticators[p.Plugin]
		p.HasCredentials = HasDNSCredentials(p.Key)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		// Installed first: the list is a choice, and the ones that will work
		// are the ones worth reading.
		if out[i].Installed != out[j].Installed {
			return out[i].Installed
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// dnsIssueArgs builds the plugin half of a certbot invocation.
//
// Each plugin names its own credentials and propagation arguments after
// itself, and route53 has neither — it reads the environment or the instance
// role. Getting this wrong is the difference between a certificate and an
// error message about an unrecognised flag.
func dnsIssueArgs(provider DNSProvider, wait int) []string {
	args := []string{"--" + provider.Plugin}
	if provider.Key == "route53" {
		return args
	}
	args = append(args,
		"--"+provider.Plugin+"-credentials", credentialsPath(provider.Key))
	if wait > 0 {
		args = append(args, "--"+provider.Plugin+"-propagation-seconds", fmt.Sprint(wait))
	}
	return args
}

func normalizeRoute53Credentials(content string) (string, error) {
	values := map[string]string{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || line == "[default]" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || (name != "aws_access_key_id" && name != "aws_secret_access_key" && name != "aws_session_token") || value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return "", fmt.Errorf("Route 53 credentials must contain only [default], aws_access_key_id, aws_secret_access_key and optional aws_session_token")
		}
		if _, exists := values[name]; exists {
			return "", fmt.Errorf("duplicate Route 53 credential field %s", name)
		}
		values[name] = value
	}
	if values["aws_access_key_id"] == "" || values["aws_secret_access_key"] == "" {
		return "", fmt.Errorf("Route 53 requires an access key and secret access key")
	}
	normalized := "[default]\n"
	for _, name := range []string{"aws_access_key_id", "aws_secret_access_key", "aws_session_token"} {
		if value := values[name]; value != "" {
			normalized += name + " = " + value + "\n"
		}
	}
	return normalized, nil
}

// CertbotEnvironment supplies the same AWS profile for issuance and every
// dashboard renewal. Credential values never appear in argv or job events.
func CertbotEnvironment() ([]string, error) {
	return route53EnvironmentAt(credentialsPath("route53"))
}
func route53EnvironmentAt(path string) ([]string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved Route 53 credentials: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return nil, fmt.Errorf("read saved Route 53 credentials: %w", err)
	}
	if len(raw) > 64*1024 {
		return nil, fmt.Errorf("saved Route 53 credentials are unexpectedly large")
	}
	normalized, err := normalizeRoute53Credentials(string(raw))
	if err != nil {
		return nil, fmt.Errorf("saved Route 53 credentials need correction: %w", err)
	}
	// Older releases saved bare key/value lines. Normalize on use so an
	// upgrade repairs already-saved credentials without requiring re-entry.
	if normalized != string(raw) {
		if err := persistDNSCredentials(path, normalized); err != nil {
			return nil, err
		}
	}
	return route53Environment(path), nil
}
func persistDNSCredentials(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(strings.TrimSpace(content) + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func route53Environment(path string) []string {
	return []string{
		"AWS_SHARED_CREDENTIALS_FILE=" + path, "AWS_PROFILE=default", "AWS_DEFAULT_PROFILE=default",
		"AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=", "AWS_SESSION_TOKEN=", "AWS_SECURITY_TOKEN=",
		"AWS_CONFIG_FILE=/dev/null", "AWS_WEB_IDENTITY_TOKEN_FILE=", "AWS_ROLE_ARN=", "AWS_EC2_METADATA_DISABLED=true",
	}
}
