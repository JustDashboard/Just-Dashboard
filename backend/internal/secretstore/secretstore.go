// Package secretstore keeps the dashboard's connections to the secret
// managers an operator already runs — HashiCorp Vault and OpenBao (KV
// version 2), Infisical, Doppler, AWS Secrets Manager and Azure Key Vault —
// and reads the values a deployment's variables reference in them. A connection's credentials are sealed like a
// notification channel's or a log drain's, and only their names are read
// back. A value read from a store is never written to the dashboard's
// database, its audit log or a transcript: it lives in memory for the run
// that asked for it.
package secretstore

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// The kinds of store. Vault and OpenBao speak the same API; they are told
// apart only so the page names the product the operator runs. AWS Secrets
// Manager and Azure Key Vault keep whole secrets rather than key-value
// pairs, so a reference to them may name the whole value or, when the value
// is a JSON object, one of its keys.
const (
	KindVault     = "vault"
	KindOpenBao   = "openbao"
	KindInfisical = "infisical"
	KindDoppler   = "doppler"
	KindAWS       = "aws"
	KindAzure     = "azure"
)

// The Azure clouds a Key Vault can be in; each has its own vault domain and
// sign-in authority. Public is the default.
const (
	AzurePublic = "public"
	AzureUSGov  = "usgov"
	AzureChina  = "china"
)

// How a Vault or OpenBao connection signs in.
const (
	AuthToken   = "token"
	AuthAppRole = "approle"
)

// The hosted services' own addresses, used when the form leaves the address
// empty.
const (
	InfisicalCloud = "https://app.infisical.com"
	DopplerAPI     = "https://api.doppler.com"
)

var (
	ErrNotFound  = errors.New("secret store not found")
	ErrNameTaken = errors.New("a secret store with that name already exists")
	ErrInvalid   = errors.New("invalid secret store")
)

// Settings are a connection's non-secret choices. Which apply depends on the
// kind; the rest stay empty.
type Settings struct {
	// Mount is the path a KV version 2 engine is mounted at (Vault and
	// OpenBao); "secret" when empty, which is where a dev server puts one.
	Mount string `json:"mount,omitempty"`
	// Namespace is sent as X-Vault-Namespace (Vault Enterprise, OpenBao).
	Namespace string `json:"namespace,omitempty"`
	// Auth is token or approle (Vault and OpenBao).
	Auth string `json:"auth,omitempty"`
	// AuthMount is the path AppRole is enabled at; "approle" when empty.
	AuthMount string `json:"authMount,omitempty"`
	// Project and Environment say which Infisical project and environment a
	// reference's path and key are read in.
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	// Region is the AWS region the Secrets Manager endpoint is in; the
	// endpoint is derived from it unless the address overrides it (a VPC
	// endpoint, LocalStack).
	Region string `json:"region,omitempty"`
	// VaultName is the Azure Key Vault's name, which becomes its host
	// ({name}.vault.azure.net) unless the address overrides it; TenantID is
	// the Entra tenant the client credentials sign in to; Cloud picks the
	// vault domain and the sign-in authority (public when empty); Authority
	// overrides the sign-in address, for a private cloud or a test.
	VaultName string `json:"vaultName,omitempty"`
	TenantID  string `json:"tenantId,omitempty"`
	Cloud     string `json:"cloud,omitempty"`
	Authority string `json:"authority,omitempty"`
	// CACert is a PEM certificate authority a self-hosted store's TLS
	// certificate is checked against, beside the system's own.
	CACert string `json:"caCert,omitempty"`
}

// Store is a connection as the page reads it: never a credential value.
type Store struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Address  string   `json:"address"`
	Settings Settings `json:"settings"`
	// Endpoint is where the store is reached: the address, or for AWS and
	// Azure, whose address is optional, the one their region or vault name
	// derives.
	Endpoint string `json:"endpoint"`
	// CredentialNames are the credentials that are set, by name.
	CredentialNames []string `json:"credentialNames"`
	// AllowPreviews lets a pull request preview's variables read this store.
	// Off by default: a preview runs code nobody has merged, and gets no
	// production secret unless somebody decides it should.
	AllowPreviews bool      `json:"allowPreviews"`
	CreatedBy     string    `json:"createdBy"`
	UpdatedBy     string    `json:"updatedBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// Test is how the last Test went, if one was asked for.
	Test *Outcome `json:"test,omitempty"`
	// LastRead is how the last run that read this store went.
	LastRead *Outcome `json:"lastRead,omitempty"`
}

// Outcome is when something was tried against a store and what it answered.
type Outcome struct {
	At      time.Time `json:"at"`
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
}

// Write is a create or an update. A nil Credentials on update keeps the
// sealed ones; given, it replaces all of them.
type Write struct {
	Name          string             `json:"name"`
	Kind          string             `json:"kind"`
	Address       string             `json:"address"`
	Settings      Settings           `json:"settings"`
	Credentials   *map[string]string `json:"credentials,omitempty"`
	AllowPreviews bool               `json:"allowPreviews"`
}

// Repository keeps connections in SQLite with their credentials sealed.
type Repository struct {
	db     *sql.DB
	sealer *auth.Sealer
	now    func() time.Time
	// tokens holds, in memory only, the access tokens a store's sign-in
	// minted that outlive one run (Azure's), until they expire.
	tokens *tokenCache
}

func NewRepository(st *store.Store, sealer *auth.Sealer) *Repository {
	return &Repository{db: st.DB, sealer: sealer, now: time.Now, tokens: newTokenCache()}
}

// A store's name is what a reference says, so it is a slug that reads the
// same in a variable as on the page, and it never changes once saved.
var (
	nameRe    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
	mountRe   = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+){0,7}$`)
	projectRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	envSlugRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// A region (us-east-1, eu-central-2, us-gov-west-1, cn-northwest-1) and
	// a Key Vault name (3 to 24 letters, digits and dashes, starting with a
	// letter) are held to their documented shapes before either becomes a
	// hostname the credentials are sent to.
	regionRe    = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]{2,16}){1,2}-[0-9]{1,2}$`)
	vaultNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{1,22}[A-Za-z0-9]$`)
	// A tenant is a GUID or a verified domain; both are a path segment of
	// the sign-in address.
	tenantRe   = regexp.MustCompile(`^[A-Za-z0-9.-]{1,64}$`)
	maxCACert  = 64 << 10
	maxCredLen = 8 << 10
)

// credentialNames are the credentials each way of signing in takes, in the
// order the form asks for them. One of them may be left empty
// (optionalCredential).
func credentialNames(kind, method string) []string {
	switch kind {
	case KindVault, KindOpenBao:
		if method == AuthAppRole {
			return []string{"roleId", "secretId"}
		}
		return []string{"token"}
	case KindInfisical:
		return []string{"clientId", "clientSecret"}
	case KindDoppler:
		return []string{"token"}
	case KindAWS:
		return []string{"accessKeyId", "secretAccessKey", "sessionToken"}
	case KindAzure:
		return []string{"clientId", "clientSecret"}
	}
	return nil
}

// optionalCredential says which credential a store may do without: an AWS
// session token belongs only to temporary credentials.
func optionalCredential(name string) bool {
	return name == "sessionToken"
}

// loopbackHost is an address that never leaves this machine. The backend
// shares the host's network, so a store published on 127.0.0.1 is reached.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// normalizeAddress holds a store's address to the rule every credentialed
// outbound call in the dashboard is held to: https, or plain http only to
// this machine. Every store connection carries a credential, so there is no
// plain-http exception for the local network as a log drain without headers
// has. AWS and Azure need no address: their region or vault name says where
// they are, and an address given overrides it (a VPC endpoint, LocalStack, a
// private cloud).
func normalizeAddress(kind, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		switch kind {
		case KindInfisical:
			return InfisicalCloud, nil
		case KindDoppler:
			return DopplerAPI, nil
		case KindAWS, KindAzure:
			return "", nil
		}
		return "", fmt.Errorf("%w: the store's address is required", ErrInvalid)
	}
	if len(raw) > 512 {
		return "", fmt.Errorf("%w: the address is too long", ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: the address must be an http or https URL without a sign-in, query or fragment", ErrInvalid)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !loopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("%w: credentials are never sent over plain http except to this machine; use https", ErrInvalid)
		}
	default:
		return "", fmt.Errorf("%w: the address must be an http or https URL", ErrInvalid)
	}
	return parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.EscapedPath(), "/"), nil
}

func validCACert(pemText string) bool {
	rest := []byte(pemText)
	found := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return false
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return false
		}
		found = true
	}
	return found && strings.TrimSpace(string(rest)) == ""
}

func normalizeSettings(kind string, settings Settings) (Settings, error) {
	out := Settings{}
	switch kind {
	case KindVault, KindOpenBao:
		out.Mount = strings.Trim(strings.TrimSpace(settings.Mount), "/")
		if out.Mount == "" {
			out.Mount = "secret"
		}
		if !mountRe.MatchString(out.Mount) {
			return out, fmt.Errorf("%w: the KV mount is a path of letters, digits, dots, dashes and underscores", ErrInvalid)
		}
		out.Namespace = strings.Trim(strings.TrimSpace(settings.Namespace), "/")
		if out.Namespace != "" && !mountRe.MatchString(out.Namespace) {
			return out, fmt.Errorf("%w: the namespace is a path of letters, digits, dots, dashes and underscores", ErrInvalid)
		}
		out.Auth = settings.Auth
		if out.Auth == "" {
			out.Auth = AuthToken
		}
		if out.Auth != AuthToken && out.Auth != AuthAppRole {
			return out, fmt.Errorf("%w: sign in with a token or with AppRole", ErrInvalid)
		}
		if out.Auth == AuthAppRole {
			out.AuthMount = strings.Trim(strings.TrimSpace(settings.AuthMount), "/")
			if out.AuthMount == "" {
				out.AuthMount = "approle"
			}
			if !mountRe.MatchString(out.AuthMount) {
				return out, fmt.Errorf("%w: the AppRole mount is a path of letters, digits, dots, dashes and underscores", ErrInvalid)
			}
		}
	case KindInfisical:
		out.Project = strings.TrimSpace(settings.Project)
		out.Environment = strings.TrimSpace(settings.Environment)
		if !projectRe.MatchString(out.Project) {
			return out, fmt.Errorf("%w: the Infisical project id is required (Project settings → Project ID)", ErrInvalid)
		}
		if !envSlugRe.MatchString(out.Environment) {
			return out, fmt.Errorf("%w: the Infisical environment is its slug, such as prod", ErrInvalid)
		}
	case KindDoppler:
		// A service token is scoped to one project and config, which is
		// therefore not asked for.
	case KindAWS:
		out.Region = strings.ToLower(strings.TrimSpace(settings.Region))
		if !regionRe.MatchString(out.Region) {
			return out, fmt.Errorf("%w: the AWS region is one such as us-east-1 or eu-west-2", ErrInvalid)
		}
	case KindAzure:
		out.VaultName = strings.TrimSpace(settings.VaultName)
		if !vaultNameRe.MatchString(out.VaultName) || strings.Contains(out.VaultName, "--") {
			return out, fmt.Errorf("%w: the Key Vault name is 3 to 24 letters, digits and dashes, starting with a letter", ErrInvalid)
		}
		out.TenantID = strings.TrimSpace(settings.TenantID)
		if !tenantRe.MatchString(out.TenantID) {
			return out, fmt.Errorf("%w: the tenant ID is the directory's GUID (Entra ID → Overview) or a verified domain", ErrInvalid)
		}
		out.Cloud = strings.TrimSpace(settings.Cloud)
		if out.Cloud == "" {
			out.Cloud = AzurePublic
		}
		if _, ok := azureClouds[out.Cloud]; !ok {
			return out, fmt.Errorf("%w: the Azure cloud is public, usgov or china", ErrInvalid)
		}
		if authority := strings.TrimSpace(settings.Authority); authority != "" {
			// The same rule as the address: a client secret goes to it.
			normalized, err := normalizeAddress(KindVault, authority)
			if err != nil {
				return out, fmt.Errorf("%w: the sign-in authority is an https URL, or http on this machine", ErrInvalid)
			}
			out.Authority = normalized
		}
	}
	if kind != KindDoppler && strings.TrimSpace(settings.CACert) != "" {
		if len(settings.CACert) > maxCACert || !validCACert(settings.CACert) {
			return out, fmt.Errorf("%w: the certificate authority must be one or more PEM certificates", ErrInvalid)
		}
		out.CACert = strings.TrimSpace(settings.CACert) + "\n"
	}
	return out, nil
}

func normalizeCredentials(kind, method string, given map[string]string) (map[string]string, error) {
	names := credentialNames(kind, method)
	out := map[string]string{}
	for name, value := range given {
		if !slices.Contains(names, name) {
			return nil, fmt.Errorf("%w: %s is not a credential this store takes", ErrInvalid, name)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxCredLen || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("%w: %s is too long or has a line break", ErrInvalid, name)
		}
		out[name] = value
	}
	for _, name := range names {
		if out[name] == "" && !optionalCredential(name) {
			return nil, fmt.Errorf("%w: %s is required", ErrInvalid, credentialLabel(name))
		}
	}
	return out, nil
}

// credentialLabel is a credential as a sentence names it.
func credentialLabel(name string) string {
	switch name {
	case "roleId":
		return "the role ID"
	case "secretId":
		return "the secret ID"
	case "clientId":
		return "the client ID"
	case "clientSecret":
		return "the client secret"
	case "accessKeyId":
		return "the access key ID"
	case "secretAccessKey":
		return "the secret access key"
	case "sessionToken":
		return "the session token"
	}
	return "the token"
}

// azureCloud is what a cloud decides: the domain a vault's host is under,
// the sign-in authority, and the scope a token is asked for.
type azureCloud struct {
	vaultSuffix, authority, scope string
}

var azureClouds = map[string]azureCloud{
	AzurePublic: {"vault.azure.net", "https://login.microsoftonline.com", "https://vault.azure.net/.default"},
	AzureUSGov:  {"vault.usgovcloudapi.net", "https://login.microsoftonline.us", "https://vault.usgovcloudapi.net/.default"},
	AzureChina:  {"vault.azure.cn", "https://login.chinacloudapi.cn", "https://vault.azure.cn/.default"},
}

// endpointOf is where a connection is reached: its address, or what its
// settings derive when the kind needs none. The region and the vault name
// were held to a strict shape before this, so neither can carry anything
// but itself into the hostname.
func endpointOf(kind, address string, settings Settings) string {
	if address != "" {
		return address
	}
	switch kind {
	case KindAWS:
		suffix := "amazonaws.com"
		if strings.HasPrefix(settings.Region, "cn-") {
			suffix = "amazonaws.com.cn"
		}
		return "https://secretsmanager." + settings.Region + "." + suffix
	case KindAzure:
		return "https://" + strings.ToLower(settings.VaultName) + "." + azureClouds[cloudOf(settings)].vaultSuffix
	}
	return address
}

func cloudOf(settings Settings) string {
	if _, ok := azureClouds[settings.Cloud]; ok {
		return settings.Cloud
	}
	return AzurePublic
}

// azureAuthority is where a Key Vault connection signs in: the authority
// given, or the cloud's.
func azureAuthority(settings Settings) string {
	if settings.Authority != "" {
		return settings.Authority
	}
	return azureClouds[cloudOf(settings)].authority
}

// row is a connection as stored, its credentials opened.
type row struct {
	Store
	credentials map[string]string
	openErr     error
}

const storeColumns = `id, name, kind, address, settings_json, credentials_enc, allow_previews, created_by, updated_by,
	created_at, updated_at, tested_at, test_ok, test_message, read_at, read_ok, read_message`

func (r *Repository) scan(scanner interface{ Scan(...any) error }) (*row, error) {
	var out row
	var settings, sealed string
	var previews, testOK, readOK int
	var created, updated, testedAt, readAt int64
	var testMessage, readMessage string
	if err := scanner.Scan(&out.ID, &out.Name, &out.Kind, &out.Address, &settings, &sealed, &previews, &out.CreatedBy, &out.UpdatedBy,
		&created, &updated, &testedAt, &testOK, &testMessage, &readAt, &readOK, &readMessage); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(settings), &out.Settings)
	out.Endpoint = endpointOf(out.Kind, out.Address, out.Settings)
	out.AllowPreviews = previews == 1
	out.CreatedAt, out.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if testedAt > 0 {
		out.Test = &Outcome{At: time.Unix(testedAt, 0).UTC(), OK: testOK == 1, Message: testMessage}
	}
	if readAt > 0 {
		out.LastRead = &Outcome{At: time.Unix(readAt, 0).UTC(), OK: readOK == 1, Message: readMessage}
	}
	out.credentials = map[string]string{}
	if sealed != "" {
		plain, err := r.sealer.Open(sealed)
		if err == nil {
			err = json.Unmarshal([]byte(plain), &out.credentials)
		}
		if err != nil {
			// A store whose key changed is still listed, so it can be given
			// its credentials again or removed; until then it reads nothing.
			out.credentials = map[string]string{}
			out.openErr = errors.New("the store's credentials cannot be decrypted; enter them again")
		}
	}
	out.CredentialNames = []string{}
	for _, name := range credentialNames(out.Kind, out.Settings.Auth) {
		if out.credentials[name] != "" {
			out.CredentialNames = append(out.CredentialNames, name)
		}
	}
	return &out, nil
}

func (r *Repository) byName(ctx context.Context, name string) (*row, error) {
	out, err := r.scan(r.db.QueryRowContext(ctx, `SELECT `+storeColumns+` FROM deploy_secret_stores WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return out, err
}

func (r *Repository) byID(ctx context.Context, id int64) (*row, error) {
	out, err := r.scan(r.db.QueryRowContext(ctx, `SELECT `+storeColumns+` FROM deploy_secret_stores WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return out, err
}

// List is every connection as the page reads it.
func (r *Repository) List(ctx context.Context) ([]Store, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+storeColumns+` FROM deploy_secret_stores ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Store{}
	for rows.Next() {
		item, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item.Store)
	}
	return out, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id int64) (*Store, error) {
	item, err := r.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &item.Store, nil
}

// GetByName is the connection a reference names.
func (r *Repository) GetByName(ctx context.Context, name string) (*Store, error) {
	item, err := r.byName(ctx, name)
	if err != nil {
		return nil, err
	}
	return &item.Store, nil
}

func (r *Repository) seal(credentials map[string]string) (string, error) {
	encoded, err := json.Marshal(credentials)
	if err != nil {
		return "", err
	}
	return r.sealer.Seal(string(encoded))
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Create saves a new connection. It does not contact the store: Test does,
// and the form offers it as soon as the connection is saved.
func (r *Repository) Create(ctx context.Context, w Write, actor string) (*Store, error) {
	w.Name = strings.TrimSpace(w.Name)
	if !nameRe.MatchString(w.Name) {
		return nil, fmt.Errorf("%w: a store's name is lowercase letters, digits and dashes, up to 40, as a reference will say it", ErrInvalid)
	}
	if credentialNames(w.Kind, "") == nil {
		return nil, fmt.Errorf("%w: the kind is vault, openbao, infisical, doppler, aws or azure", ErrInvalid)
	}
	address, err := normalizeAddress(w.Kind, w.Address)
	if err != nil {
		return nil, err
	}
	settings, err := normalizeSettings(w.Kind, w.Settings)
	if err != nil {
		return nil, err
	}
	given := map[string]string{}
	if w.Credentials != nil {
		given = *w.Credentials
	}
	credentials, err := normalizeCredentials(w.Kind, settings.Auth, given)
	if err != nil {
		return nil, err
	}
	sealed, err := r.seal(credentials)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(settings)
	now := r.now().UTC().Unix()
	result, err := r.db.ExecContext(ctx, `INSERT INTO deploy_secret_stores(name, kind, address, settings_json, credentials_enc, allow_previews,
		created_by, updated_by, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		w.Name, w.Kind, address, string(encoded), sealed, boolInt(w.AllowPreviews), actor, actor, now, now)
	if isUnique(err) {
		return nil, ErrNameTaken
	}
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return r.Get(ctx, id)
}

// Update changes a connection's address, settings, credentials or preview
// rule. Its name and kind stay: references name the store, and a Vault token
// means nothing to Infisical. Credentials left out are kept, unless the way
// of signing in changed, which needs the new method's.
func (r *Repository) Update(ctx context.Context, id int64, w Write, actor string) (*Store, bool, error) {
	current, err := r.byID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if name := strings.TrimSpace(w.Name); name != "" && name != current.Name {
		return nil, false, fmt.Errorf("%w: a store keeps its name, because references say it; add a store under the new name instead", ErrInvalid)
	}
	if w.Kind != "" && w.Kind != current.Kind {
		return nil, false, fmt.Errorf("%w: a store keeps its kind; add a store of the other kind instead", ErrInvalid)
	}
	address, err := normalizeAddress(current.Kind, w.Address)
	if err != nil {
		return nil, false, err
	}
	settings, err := normalizeSettings(current.Kind, w.Settings)
	if err != nil {
		return nil, false, err
	}
	credentials := current.credentials
	replaced := w.Credentials != nil
	if !replaced && settings.Auth != current.Settings.Auth {
		return nil, false, fmt.Errorf("%w: signing in another way needs that way's credentials", ErrInvalid)
	}
	if replaced {
		credentials, err = normalizeCredentials(current.Kind, settings.Auth, *w.Credentials)
	} else if current.openErr != nil {
		err = fmt.Errorf("%w: %v", ErrInvalid, current.openErr)
	} else {
		_, err = normalizeCredentials(current.Kind, settings.Auth, credentials)
	}
	if err != nil {
		return nil, false, err
	}
	sealed, err := r.seal(credentials)
	if err != nil {
		return nil, false, err
	}
	encoded, _ := json.Marshal(settings)
	if _, err := r.db.ExecContext(ctx, `UPDATE deploy_secret_stores SET address=?, settings_json=?, credentials_enc=?, allow_previews=?,
		updated_by=?, updated_at=? WHERE id=?`, address, string(encoded), sealed, boolInt(w.AllowPreviews), actor, r.now().UTC().Unix(), id); err != nil {
		return nil, false, err
	}
	out, err := r.Get(ctx, id)
	return out, replaced, err
}

// Delete removes a connection. Whether anything still references it is the
// deployment package's to say, since references live in its variables; the
// route asks it first.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM deploy_secret_stores WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) recordTest(ctx context.Context, id int64, ok bool, message string) {
	_, _ = r.db.ExecContext(ctx, `UPDATE deploy_secret_stores SET tested_at=?, test_ok=?, test_message=? WHERE id=?`,
		r.now().UTC().Unix(), boolInt(ok), clip(message, 400), id)
}

func (r *Repository) recordRead(ctx context.Context, id int64, ok bool, message string) {
	_, _ = r.db.ExecContext(ctx, `UPDATE deploy_secret_stores SET read_at=?, read_ok=?, read_message=? WHERE id=?`,
		r.now().UTC().Unix(), boolInt(ok), clip(message, 400), id)
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

// Reference is what a variable's ${{store.…}} value names: a store, a path
// in it, a key at that path and, optionally, the version of that key to
// read. Its text form is the reference's target:
//
//	NAME[/PATH][#KEY][@VERSION]
//
// vault-prod/apps/web#DATABASE_URL, infisical/backend#API_KEY@4,
// doppler#STRIPE_KEY, aws/apps/web#DATABASE_URL@AWSPREVIOUS,
// azure/database-url@0f2a…. What a path, a key and a version mean, and which
// are required, depends on the store's kind (checkFits): Vault, OpenBao,
// Infisical and Doppler keep keys, so a key is required and a version is a
// whole number; AWS and Azure keep whole secrets, so the path is the secret's
// name, a key names one field of a JSON value or is left out for the whole
// value, and a version is the store's own version id or stage.
type Reference struct {
	Store   string `json:"store"`
	Path    string `json:"path"`
	Key     string `json:"key"`
	Version string `json:"version,omitempty"`
}

var (
	pathSegmentRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	keyRe         = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	versionRe     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	numberRe      = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	azureNameRe   = regexp.MustCompile(`^[A-Za-z0-9-]{1,127}$`)
	azureVersion  = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// ParseReference reads a reference's target. A path never climbs (no "."
// or ".." segment, no empty one), a key is a name and a version is a word;
// all are sent to the store as a URL path, a query value or a JSON field,
// never interpreted here. A target with neither a path nor a key names
// nothing any store can answer.
func ParseReference(target string) (Reference, error) {
	if len(target) > 300 {
		return Reference{}, fmt.Errorf("%w: a store reference is at most 300 characters", ErrInvalid)
	}
	var out Reference
	location, key, hasKey := strings.Cut(target, "#")
	if strings.Contains(key, "#") {
		return Reference{}, fmt.Errorf("%w: a store reference is store/path#KEY", ErrInvalid)
	}
	// The version follows the key, or the path when there is no key.
	if hasKey {
		key, out.Version, _ = strings.Cut(key, "@")
	} else {
		location, out.Version, _ = strings.Cut(location, "@")
	}
	if strings.Contains(target, "@") {
		if !versionRe.MatchString(out.Version) {
			return Reference{}, fmt.Errorf("%w: a pinned version is letters, digits, dots, dashes and underscores", ErrInvalid)
		}
	}
	if hasKey {
		if !keyRe.MatchString(key) {
			return Reference{}, fmt.Errorf("%w: a key is letters, digits, dots, dashes and underscores", ErrInvalid)
		}
		out.Key = key
	}
	name, path, _ := strings.Cut(location, "/")
	if !nameRe.MatchString(name) {
		return Reference{}, fmt.Errorf("%w: %q is not a store's name", ErrInvalid, name)
	}
	out.Store = name
	if path != "" {
		segments := strings.Split(path, "/")
		if len(segments) > 16 {
			return Reference{}, fmt.Errorf("%w: a path has at most 16 segments", ErrInvalid)
		}
		for _, segment := range segments {
			if segment == "." || segment == ".." || !pathSegmentRe.MatchString(segment) {
				return Reference{}, fmt.Errorf("%w: a path is segments of letters, digits, dots, dashes and underscores", ErrInvalid)
			}
		}
		out.Path = path
	}
	if out.Path == "" && out.Key == "" {
		return Reference{}, fmt.Errorf("%w: a store reference names a path, a key or both: store/path#KEY", ErrInvalid)
	}
	return out, nil
}

// String is the reference's target, as ParseReference reads it.
func (r Reference) String() string {
	out := r.Store
	if r.Path != "" {
		out += "/" + r.Path
	}
	if r.Key != "" {
		out += "#" + r.Key
	}
	if r.Version != "" {
		out += "@" + r.Version
	}
	return out
}

// Location is where a reference reads, as the run's transcript says it:
// the path and key, without the store's name.
func (r Reference) Location() string {
	out := r.Path
	if r.Key != "" {
		if out != "" {
			out += "#"
		}
		out += r.Key
	}
	if r.Version != "" {
		out += "@" + r.Version
	}
	return out
}

// checkFits refuses a reference the store's kind cannot answer. Vault,
// OpenBao, Infisical and Doppler keep keys, so a reference to them names
// one, and a version is a whole number from 1; a Doppler service token reads
// one config, which has neither folders nor versions a reader can ask for,
// and a Vault secret is always at a path. AWS Secrets Manager and Azure Key
// Vault keep whole secrets: the path is the secret's name, a key is optional
// (a field of a JSON value), and a version is the store's own — a Secrets
// Manager version id or staging label, a Key Vault version's 32 hexadecimal
// characters.
func checkFits(kind string, reference Reference) error {
	switch kind {
	case KindAWS:
		if reference.Path == "" {
			return fmt.Errorf("%w: a Secrets Manager reference names the secret, %s/secret-name, and a key of its JSON value as #KEY if one field is wanted", ErrInvalid, reference.Store)
		}
		return nil
	case KindAzure:
		if reference.Path == "" || !azureNameRe.MatchString(reference.Path) {
			return fmt.Errorf("%w: a Key Vault reference names the secret, %s/secret-name; the name is letters, digits and dashes", ErrInvalid, reference.Store)
		}
		if reference.Version != "" && !azureVersion.MatchString(reference.Version) {
			return fmt.Errorf("%w: a Key Vault version is its 32 hexadecimal characters, as the vault lists it", ErrInvalid)
		}
		return nil
	}
	if reference.Key == "" {
		return fmt.Errorf("%w: a reference to this store names a key: %s/path#KEY", ErrInvalid, reference.Store)
	}
	if reference.Version != "" && !numberRe.MatchString(reference.Version) {
		return fmt.Errorf("%w: a pinned version is a whole number from 1", ErrInvalid)
	}
	switch kind {
	case KindDoppler:
		if reference.Path != "" {
			return fmt.Errorf("%w: a Doppler service token reads one config, so the reference is %s#KEY without a path", ErrInvalid, reference.Store)
		}
		if reference.Version != "" {
			return fmt.Errorf("%w: Doppler does not serve earlier versions of a secret, so a Doppler reference cannot pin one", ErrInvalid)
		}
	case KindVault, KindOpenBao:
		if reference.Path == "" {
			return fmt.Errorf("%w: a Vault secret is at a path under the KV mount, so the reference is %s/path#KEY", ErrInvalid, reference.Store)
		}
	}
	return nil
}

// Check is the save-time half of a reference: the store exists, it can
// answer this shape of reference, and a preview may read it only when the
// store says previews may. The run-time half, Resolve, checks the same again,
// since a store can change between a save and a run.
func (r *Repository) Check(ctx context.Context, reference Reference, preview bool) error {
	item, err := r.byName(ctx, reference.Store)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: there is no secret store named %s", ErrInvalid, reference.Store)
	}
	if err != nil {
		return err
	}
	if preview && !item.AllowPreviews {
		return fmt.Errorf("%w: %s does not serve previews; allow previews on the store, or use a store meant for them", ErrInvalid, item.Name)
	}
	return checkFits(item.Kind, reference)
}
