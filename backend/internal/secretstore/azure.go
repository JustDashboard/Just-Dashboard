package secretstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// --- Azure Key Vault ---------------------------------------------------------

// azureSession reads a Key Vault's secrets with a token from the OAuth 2.0
// client-credentials grant: the connection's application (client ID and
// secret) signs in to its tenant at the cloud's authority for the vault
// scope, and the token is sent as a bearer. A Key Vault secret is one
// string with versions named by 32 hexadecimal characters.
type azureSession struct {
	vault, token string
	client       *http.Client
	item         *row
}

const azureAPIVersion = "7.4"

// tokenCache keeps the access tokens that are the connection's rather than a
// run's — Entra issues one for about an hour and offers no way to revoke it —
// in memory only, until a minute before they expire. A rotated client secret
// or a changed tenant is a different key, so a change to the connection
// signs in again.
type tokenCache struct {
	mu     sync.Mutex
	tokens map[string]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

func newTokenCache() *tokenCache { return &tokenCache{tokens: map[string]cachedToken{}} }

// tokenReuse is how long before a token expires it stops being reused.
const tokenReuse = time.Minute

func (c *tokenCache) get(key string, now time.Time) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.tokens[key]
	if !ok || now.After(entry.expires.Add(-tokenReuse)) {
		delete(c.tokens, key)
		return "", false
	}
	return entry.token, true
}

func (c *tokenCache) put(key, token string, expires time.Time, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for other, entry := range c.tokens {
		if now.After(entry.expires) {
			delete(c.tokens, other)
		}
	}
	c.tokens[key] = cachedToken{token: token, expires: expires}
}

// azureTokenKey names a token by everything that decides it: the connection,
// its authority, tenant, scope and client, and a digest of the client secret
// (never the secret itself).
func azureTokenKey(item *row, authority, scope string) string {
	digest := sha256.Sum256([]byte(item.credentials["clientSecret"]))
	return fmt.Sprintf("%d|%s|%s|%s|%s|%s", item.ID, authority, item.Settings.TenantID, scope, item.credentials["clientId"], hex.EncodeToString(digest[:8]))
}

func openAzure(ctx context.Context, item *row, client *http.Client, tokens *tokenCache) (*azureSession, error) {
	s := &azureSession{vault: item.Endpoint, client: client, item: item}
	authority := azureAuthority(item.Settings)
	scope := azureClouds[cloudOf(item.Settings)].scope
	key := azureTokenKey(item, authority, scope)
	now := time.Now()
	if token, ok := tokens.get(key, now); ok {
		s.token = token
		return s, nil
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", item.credentials["clientId"])
	form.Set("client_secret", item.credentials["clientSecret"])
	form.Set("scope", scope)
	status, raw, err := exchangeBytes(ctx, client, http.MethodPost, authority+"/"+url.PathEscape(item.Settings.TenantID)+"/oauth2/v2.0/token",
		nil, []byte(form.Encode()), "application/x-www-form-urlencoded", nil)
	if err != nil {
		return nil, fmt.Errorf("Entra sign-in: %v", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Entra sign-in refused: %s", answerMessage(status, raw))
	}
	var answer struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.AccessToken == "" {
		return nil, errors.New("Entra sign-in answered without an access token")
	}
	s.token = answer.AccessToken
	if answer.ExpiresIn > 0 {
		tokens.put(key, s.token, now.Add(time.Duration(answer.ExpiresIn)*time.Second), now)
	}
	return s, nil
}

func (s *azureSession) get(ctx context.Context, path string, query url.Values, into any) error {
	query.Set("api-version", azureAPIVersion)
	status, raw, err := exchange(ctx, s.client, http.MethodGet, s.vault+path+"?"+query.Encode(),
		map[string]string{"Authorization": "Bearer " + s.token}, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return &azureError{status: status, code: azureErrorCode(raw), message: answerMessage(status, raw)}
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return errors.New("Key Vault did not answer as expected")
	}
	return nil
}

// azureError is a Key Vault refusal with its code kept.
type azureError struct {
	status  int
	code    string
	message string
}

func (e *azureError) Error() string { return e.message }

func azureErrorCode(raw []byte) string {
	var shaped struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &shaped)
	return shaped.Error.Code
}

// read is one secret at its current version or at the version named, as a
// whole and, when it is a JSON object, by its fields. The version read is
// the last segment of the secret's id.
func (s *azureSession) read(ctx context.Context, name, version string) (map[string]secretValue, error) {
	path := "/secrets/" + url.PathEscape(name)
	if version != "" {
		path += "/" + url.PathEscape(version)
	}
	var answer struct {
		Value *string `json:"value"`
		ID    string  `json:"id"`
	}
	if err := s.get(ctx, path, url.Values{}, &answer); err != nil {
		return nil, err
	}
	if answer.Value == nil {
		return nil, fmt.Errorf("%s answered without a value", name)
	}
	return wholeAndFields(*answer.Value, answer.ID[strings.LastIndex(answer.ID, "/")+1:]), nil
}

func (s *azureSession) readPath(ctx context.Context, path string) (map[string]secretValue, error) {
	return s.read(ctx, path, "")
}

func (s *azureSession) readVersion(ctx context.Context, path, key, version string) (secretValue, error) {
	values, err := s.read(ctx, path, version)
	if err != nil {
		return secretValue{}, err
	}
	value, ok := values[key]
	if !ok {
		return secretValue{}, fmt.Errorf("version %s of %s has no field %s: its value is not a JSON object with that key", version, path, key)
	}
	return value, nil
}

// identity lists one secret, which needs the list permission; an
// application given only get is still signed in, and says so.
func (s *azureSession) identity(ctx context.Context) (string, error) {
	var answer struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	where := "Key Vault " + s.item.Settings.VaultName + " in tenant " + s.item.Settings.TenantID
	err := s.get(ctx, "/secrets", url.Values{"maxresults": {"1"}}, &answer)
	var refused *azureError
	if errors.As(err, &refused) && refused.status == http.StatusForbidden {
		return fmt.Sprintf("signed in to %s; this application may not list secrets (%s)", where, refused.message), nil
	}
	if err != nil {
		return "", err
	}
	if len(answer.Value) == 0 {
		return "signed in to " + where + "; no secret is listed", nil
	}
	id := answer.Value[0].ID
	return fmt.Sprintf("signed in to %s; the first secret listed is %s", where, id[strings.LastIndex(id, "/")+1:]), nil
}

// close keeps the token: it is the connection's until it expires, and Entra
// offers nothing to revoke it with.
func (s *azureSession) close(context.Context) {}
