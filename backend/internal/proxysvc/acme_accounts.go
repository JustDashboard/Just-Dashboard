package proxysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Certificate authorities other than Let's Encrypt, and the ACME accounts
// certbot keeps with each.
//
// certbot orders from one directory per run (--server) and registers an
// account there the first time. ZeroSSL and Google Trust Services register an
// account only against External Account Binding: a key ID and an HMAC key
// from the authority's own console, which certbot needs once, to register.
// Those are a credential, so they never go into argv, where any local user
// reads them from /proc: the job writes them to a temporary file certbot
// reads as its configuration (--config), mode 0600, and removes it after.

// ACMEAuthorityOption is an authority the issue form offers by name.
type ACMEAuthorityOption struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Directory string `json:"directory"`
	// EABRequired is an authority that registers no account without
	// External Account Binding.
	EABRequired bool `json:"eabRequired"`
	// caa is the authority's CAA identifier (its CPS names it), which a
	// domain's CAA record has to list for the authority to issue.
	caa string
}

// Buypass is not offered: it stopped issuing ACME (Go SSL) certificates in
// October 2025. Its directory can still be named as another directory.
var acmeAuthorities = []ACMEAuthorityOption{
	{Key: "letsencrypt", Name: "Let's Encrypt", Directory: letsEncryptProductionDirectory, caa: letsEncryptCAA},
	{Key: "zerossl", Name: "ZeroSSL", Directory: "https://acme.zerossl.com/v2/DV90", EABRequired: true, caa: "sectigo.com"},
	{Key: "google", Name: "Google Trust Services", Directory: "https://dv.acme-v02.api.pki.goog/directory", EABRequired: true, caa: "pki.goog"},
}

// ACMEAuthorities is the catalog the issue form offers.
func ACMEAuthorities() []ACMEAuthorityOption { return acmeAuthorities }

func knownAuthority(directory string) (ACMEAuthorityOption, bool) {
	for _, a := range acmeAuthorities {
		if a.Directory == directory {
			return a, true
		}
	}
	return ACMEAuthorityOption{}, false
}

// IssueAuthority is whom one issuance orders from.
type IssueAuthority struct {
	Name string `json:"name"`
	// Server is the directory the run talks to: for a test run with Let's
	// Encrypt, its staging endpoint.
	Server      string `json:"server"`
	LetsEncrypt bool   `json:"letsEncrypt"`
	// Staging is a directory that signs only test certificates.
	Staging     bool `json:"staging"`
	EABRequired bool `json:"eabRequired"`
	// Account is certbot having an account there already: no email and no
	// External Account Binding are needed to register one.
	Account bool `json:"account"`
	// url is what --server names; empty is certbot's own default, Let's
	// Encrypt's production directory, which certbot's --dry-run swaps for
	// staging only when it is not named.
	url string
	caa string
}

func (a IssueAuthority) certbotArgs() []string {
	if a.url == "" {
		return nil
	}
	return []string{"--server", a.url}
}

// IssueAuthorityFor is whom req would order from.
func IssueAuthorityFor(req IssueRequest) (IssueAuthority, error) { return authorityFor(req) }

// authorityFor resolves a request's choice: a known authority by key, another
// directory by URL, or, with neither, what JD_ACME_DIRECTORY configures.
func authorityFor(req IssueRequest) (IssueAuthority, error) {
	var a IssueAuthority
	switch req.CA {
	case "":
		d := acmeDirectory()
		switch {
		case !d.configured():
			a = IssueAuthority{Name: "Let's Encrypt", LetsEncrypt: true, caa: letsEncryptCAA}
		case d.letsEncrypt():
			a = IssueAuthority{Name: "Let's Encrypt", LetsEncrypt: true, Staging: d.staging(), caa: letsEncryptCAA}
			if len(d.certbotArgs()) > 0 {
				a.url = d.URL
			}
		default:
			a = directoryAuthority(d.URL)
		}
	case "custom":
		directory := strings.TrimSpace(req.Directory)
		parsed, err := url.Parse(directory)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			len(directory) > 512 || strings.ContainsAny(directory, " \t\r\n\"'{};\\") {
			return a, fmt.Errorf("the directory must be an https:// ACME directory URL")
		}
		if strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".api.letsencrypt.org") {
			return a, fmt.Errorf("choose Let's Encrypt rather than naming its directory")
		}
		a = directoryAuthority(directory)
	default:
		found := false
		for _, known := range acmeAuthorities {
			if known.Key == req.CA {
				a, found = directoryAuthority(known.Directory), true
			}
		}
		if !found {
			return a, fmt.Errorf("%q is not a certificate authority this dashboard knows", req.CA)
		}
		if req.CA == "letsencrypt" {
			a = IssueAuthority{Name: "Let's Encrypt", LetsEncrypt: true, caa: letsEncryptCAA}
		}
	}
	if req.CA != "custom" && req.Directory != "" {
		return a, fmt.Errorf("a directory is named only for another ACME directory")
	}
	a.Server = a.url
	if a.Server == "" {
		a.Server = letsEncryptProductionDirectory
		if req.Staging {
			a.Server = letsEncryptStagingDirectory
		}
	}
	a.Account = acmeAccountExists(letsencryptDir, a.Server)
	return a, nil
}

func directoryAuthority(directory string) IssueAuthority {
	a := IssueAuthority{Name: authorityHost(directory), url: directory, Staging: strings.Contains(directory, "staging")}
	if known, ok := knownAuthority(directory); ok {
		a.Name, a.EABRequired, a.caa = known.Name, known.EABRequired, known.caa
	}
	return a
}

func authorityHost(directory string) string {
	if parsed, err := url.Parse(directory); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return directory
}

// EAB is an External Account Binding: the key ID and base64url HMAC key an
// authority's console hands out for registering an account.
type EAB struct {
	KeyID   string
	HMACKey string
}

var (
	eabKeyIDRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,256}$`)
	eabHMACRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}={0,2}$`)
)

// CheckEAB refuses what certbot's configuration file could not carry as one
// value, and what no authority hands out.
func CheckEAB(keyID, hmacKey string) (EAB, error) {
	eab := EAB{KeyID: strings.TrimSpace(keyID), HMACKey: strings.TrimSpace(hmacKey)}
	if !eabKeyIDRe.MatchString(eab.KeyID) {
		return EAB{}, fmt.Errorf("the EAB key ID is letters, digits, dots, colons, dashes and underscores")
	}
	if !eabHMACRe.MatchString(eab.HMACKey) {
		return EAB{}, fmt.Errorf("the EAB HMAC key is the base64url text the authority's console shows")
	}
	return eab, nil
}

const certbotEABPrefix = "just-dashboard-eab."

// CertbotEABConfig writes eab where certbot runs, in a file only root reads,
// for --config. mktemp creates it 0600 under a name nobody could have taken
// first, and the key reaches it on tee's stdin rather than in any argv. The
// cleanup removes it and is safe to call more than once.
func CertbotEABConfig(ctx context.Context, eab EAB) (string, func(), error) {
	rt, err := loadCertbotRuntime(ctx)
	if rt == nil {
		if err == nil {
			err = fmt.Errorf("certbot is not installed on this host")
		}
		return "", nil, err
	}
	out, err := rt.on(ctx, "mktemp", "-t", certbotEABPrefix+"XXXXXXXXXX").Output()
	path := strings.TrimSpace(string(out))
	if err != nil || !filepath.IsAbs(path) || !strings.HasPrefix(filepath.Base(path), certbotEABPrefix) {
		return "", nil, fmt.Errorf("mktemp made no file to hand certbot the EAB key in")
	}
	cleanup := func() {
		// The job's own context may be the one that ended it.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = rt.on(ctx, "rm", "-f", "--", path).Run()
	}
	write := rt.on(ctx, "tee", "--", path)
	write.Stdin = strings.NewReader("eab-kid = " + eab.KeyID + "\neab-hmac-key = " + eab.HMACKey + "\n")
	if err := write.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("the EAB key could not be written for certbot: %w", err)
	}
	return path, cleanup, nil
}

// ACMEAccountEntry is one account certbot keeps under accounts/: read from
// its regr.json and meta.json, and its contact asked of the authority. The
// account's private key is never read.
type ACMEAccountEntry struct {
	ID        string `json:"id"`
	Server    string `json:"server"`
	Authority string `json:"authority"`
	Staging   bool   `json:"staging"`
	URL       string `json:"url,omitempty"`
	Email     string `json:"email,omitempty"`
	// Contacted is show_account having answered: Email is then the whole
	// truth, and empty means the account has no contact.
	Contacted bool      `json:"contacted"`
	Created   time.Time `json:"created,omitzero"`
	Error     string    `json:"error,omitempty"`
}

var acmeAccountIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// v1Directories are the Let's Encrypt v1 paths certbot links its v2
// directories' accounts to; an account found there is the v2 one's.
var v1Directories = map[string]string{
	"acme-v01.api.letsencrypt.org/directory":     letsEncryptProductionDirectory,
	"acme-staging.api.letsencrypt.org/directory": letsEncryptStagingDirectory,
}

// readACMEAccounts lists the accounts under dir/accounts.
func readACMEAccounts(dir string) []ACMEAccountEntry {
	root := filepath.Join(dir, "accounts")
	seen := map[string]bool{}
	accounts := []ACMEAccountEntry{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "regr.json" {
			return nil
		}
		accountDir := filepath.Dir(path)
		id := filepath.Base(accountDir)
		rel, err := filepath.Rel(root, filepath.Dir(accountDir))
		if err != nil || !acmeAccountIDRe.MatchString(id) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		server, ok := v1Directories[rel]
		if !ok {
			server = "https://" + rel
		}
		if seen[server+" "+id] {
			return nil
		}
		seen[server+" "+id] = true
		entry := ACMEAccountEntry{ID: id, Server: server, Authority: directoryAuthority(server).Name,
			Staging: strings.Contains(server, "staging")}
		if server == letsEncryptProductionDirectory || server == letsEncryptStagingDirectory {
			entry.Authority = "Let's Encrypt"
		}
		var regr struct {
			URI  string `json:"uri"`
			Body struct {
				Contact []string `json:"contact"`
			} `json:"body"`
		}
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &regr) == nil {
			entry.URL = regr.URI
			for _, c := range regr.Body.Contact {
				if email := strings.TrimPrefix(c, "mailto:"); emailRe.MatchString(email) {
					entry.Email = email
					break
				}
			}
		}
		var meta struct {
			Created time.Time `json:"creation_dt"`
		}
		if raw, err := os.ReadFile(filepath.Join(accountDir, "meta.json")); err == nil && json.Unmarshal(raw, &meta) == nil {
			entry.Created = meta.Created
		}
		accounts = append(accounts, entry)
		return nil
	})
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Staging != accounts[j].Staging {
			return !accounts[i].Staging
		}
		if accounts[i].Server != accounts[j].Server {
			return accounts[i].Server < accounts[j].Server
		}
		return accounts[i].Created.Before(accounts[j].Created)
	})
	return accounts
}

// maxAccountLookups bounds how many authorities one listing asks: each
// show_account is a round trip, and certbot runs them one at a time.
const maxAccountLookups = 8

// ACMEAccounts lists certbot's accounts with each one's contact, asked of
// its authority one at a time (certbot takes its lock for each).
func ACMEAccounts(ctx context.Context) ([]ACMEAccountEntry, error) {
	accounts := readACMEAccounts(letsencryptDir)
	if len(accounts) == 0 {
		return accounts, nil
	}
	rt, err := loadCertbotRuntime(ctx)
	if rt == nil {
		if err == nil {
			err = fmt.Errorf("certbot is not installed on this host")
		}
		return accounts, err
	}
	for i := range accounts {
		a := &accounts[i]
		if i >= maxAccountLookups || ctx.Err() != nil {
			a.Error = "not asked: too many accounts to ask their authorities in one listing"
			continue
		}
		lookup, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, err := rt.command(lookup, "show_account", "--non-interactive", "--server", a.Server, "--account", a.ID).CombinedOutput()
		cancel()
		if err != nil {
			a.Error = lastMeaningfulLine(strings.TrimSpace(string(out)))
			if a.Error == "" {
				a.Error = err.Error()
			}
			continue
		}
		var shown ACMEAccount
		parseShowAccount(string(out), &shown)
		a.Contacted, a.Email = true, shown.Email
		if shown.URL != "" {
			a.URL = shown.URL
		}
	}
	return accounts, nil
}

// UpdateAccountEmailArgs is certbot's update_account for one account on
// disk. The server and ID are looked up among the accounts certbot keeps, so
// the run talks only to an authority it is already registered with.
func UpdateAccountEmailArgs(server, id, email string) (ACMEAccountEntry, []string, error) {
	email = strings.TrimSpace(email)
	if !emailRe.MatchString(email) {
		return ACMEAccountEntry{}, nil, fmt.Errorf("%q is not an email address the certificate authority will take", email)
	}
	for _, a := range readACMEAccounts(letsencryptDir) {
		if a.Server == server && a.ID == id {
			return a, []string{"update_account", "--non-interactive", "--no-eff-email",
				"--server", a.Server, "--account", a.ID, "-m", email}, nil
		}
	}
	return ACMEAccountEntry{}, nil, fmt.Errorf("certbot keeps no such account")
}
