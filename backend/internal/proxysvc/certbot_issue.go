package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	letsEncryptProductionDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStagingDirectory    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// certbotServer is the directory a run orders from: the configured one when
// certbot is given --server, otherwise certbot's own default, which a
// --dry-run swaps for Let's Encrypt's staging endpoint.
func certbotServer(dryRun bool) string {
	d := acmeDirectory()
	if len(d.certbotArgs()) > 0 {
		return d.URL
	}
	if dryRun {
		return letsEncryptStagingDirectory
	}
	return letsEncryptProductionDirectory
}

// acmeAccountExists reports an account certbot keeps for server under
// dir/accounts, where it files one by the directory URL's host and path.
// Let's Encrypt's v2 directories also take the account of their v1
// predecessor, which certbot links in on first use.
func acmeAccountExists(dir, server string) bool {
	paths := []string{accountsPath(server)}
	switch server {
	case letsEncryptProductionDirectory:
		paths = append(paths, "acme-v01.api.letsencrypt.org/directory")
	case letsEncryptStagingDirectory:
		paths = append(paths, "acme-staging.api.letsencrypt.org/directory")
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		base := filepath.Join(dir, "accounts", filepath.FromSlash(p))
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if _, err := os.Stat(filepath.Join(base, entry.Name(), "regr.json")); err == nil {
				return true
			}
		}
	}
	return false
}

func accountsPath(server string) string {
	parsed, err := url.Parse(server)
	if err != nil || parsed.Host == "" || strings.Contains(parsed.Path, "..") {
		return ""
	}
	return parsed.Host + parsed.Path
}

// checkWebroot refuses a folder that is not there on the side certbot runs
// on: certbot would create it, write the challenge into a folder no server
// serves, and the authority's request would come back 404 a minute later.
func checkWebroot(ctx context.Context, path string) error {
	rt, _ := loadCertbotRuntime(ctx)
	if rt == nil {
		// No certbot: the job says so when it starts, and there is no side
		// to look on.
		return nil
	}
	if rt.on(ctx, "test", "-d", path).Run() != nil {
		return fmt.Errorf("%s is not a folder where certbot runs (%s)", path, rt.where())
	}
	return nil
}

// issueKeyArgs is the key the certificate is to have.
func issueKeyArgs(req IssueRequest) ([]string, error) {
	switch req.KeyType {
	case "":
		if req.RSAKeySize != 0 {
			return nil, fmt.Errorf("an RSA key size needs the RSA key type")
		}
		return nil, nil
	case "ecdsa":
		if req.RSAKeySize != 0 {
			return nil, fmt.Errorf("an RSA key size needs the RSA key type")
		}
		return []string{"--key-type", "ecdsa"}, nil
	case "rsa":
		size := req.RSAKeySize
		if size == 0 {
			size = 2048
		}
		if !slices.Contains(rsaKeySizes, size) {
			return nil, fmt.Errorf("an RSA key is 2048, 3072 or 4096 bits")
		}
		return []string{"--key-type", "rsa", "--rsa-key-size", strconv.Itoa(size)}, nil
	}
	return nil, fmt.Errorf("the key type must be ecdsa or rsa")
}

// keyMatches reports a certificate whose key is the type, and for RSA the
// size, asked for.
func keyMatches(leaf *x509.Certificate, keyType string, rsaSize int) bool {
	switch key := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		if rsaSize == 0 {
			rsaSize = 2048
		}
		return keyType == "rsa" && key.N.BitLen() == rsaSize
	case *ecdsa.PublicKey:
		return keyType == "ecdsa"
	}
	return false
}

// ACMEAccount is the account certbot orders under, as show_account tells it.
type ACMEAccount struct {
	// Server is the directory the account is with: the one a real issuance
	// orders from.
	Server string `json:"server"`
	Exists bool   `json:"exists"`
	// Email is the account's first contact; empty when it has none.
	Email      string `json:"email,omitempty"`
	URL        string `json:"url,omitempty"`
	Thumbprint string `json:"thumbprint,omitempty"`
	// Error is show_account failing for an account on disk: the authority
	// unreachable, or the account deactivated there.
	Error string `json:"error,omitempty"`
}

// CertbotAccount reads the account a real issuance would use. The files say
// whether there is one; the contact is asked of the authority, which is
// where certbot keeps it.
func CertbotAccount(ctx context.Context) (ACMEAccount, error) {
	account := ACMEAccount{Server: certbotServer(false)}
	account.Exists = acmeAccountExists(letsencryptDir, account.Server)
	if !account.Exists {
		return account, nil
	}
	rt, err := loadCertbotRuntime(ctx)
	if rt == nil {
		if err == nil {
			err = fmt.Errorf("certbot is not installed on this host")
		}
		return account, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := append([]string{"show_account", "--non-interactive"}, acmeDirectory().certbotArgs()...)
	out, err := rt.command(ctx, args...).CombinedOutput()
	if err != nil {
		account.Error = lastMeaningfulLine(strings.TrimSpace(string(out)))
		if account.Error == "" {
			account.Error = err.Error()
		}
		return account, nil
	}
	parseShowAccount(string(out), &account)
	return account, nil
}

// parseShowAccount reads certbot's show_account:
//
//	Account details for server https://acme-v02.api.letsencrypt.org/directory:
//	  Account URL: https://acme-v02.api.letsencrypt.org/acme/acct/123
//	  Account Thumbprint: 3xH…
//	  Email contact: ops@example.com
//
// "Email contacts:" lists several, comma-separated, and "none" is none.
func parseShowAccount(out string, account *ACMEAccount) {
	for _, line := range strings.Split(out, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch name {
		case "Account URL":
			account.URL = value
		case "Account Thumbprint":
			account.Thumbprint = value
		case "Email contact", "Email contacts":
			first, _, _ := strings.Cut(value, ",")
			first = strings.TrimSpace(first)
			if emailRe.MatchString(first) {
				account.Email = first
			}
		}
	}
}
