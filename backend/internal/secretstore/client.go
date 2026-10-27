package secretstore

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// requestTimeout bounds one request to a store, and maxAnswer what is read of
// its answer: a KV secret or a folder of secrets is kilobytes, and a store that
// answers with more is not answering the question asked.
const (
	requestTimeout = 15 * time.Second
	maxAnswer      = 1 << 20
)

// Failure is a store not giving what was asked, in its own words: the
// status and the message it answered with, or why it could not be reached.
// It never carries a value — a store's error answers hold none — and any
// credential of the connection that turns up in one is masked.
type Failure struct {
	Store     string
	Reference string
	Message   string
}

func (f *Failure) Error() string {
	if f.Reference != "" {
		return fmt.Sprintf("secret store %s, %s: %s", f.Store, f.Reference, f.Message)
	}
	return fmt.Sprintf("secret store %s: %s", f.Store, f.Message)
}

// secretValue is one key's value and the version the store reported for it.
type secretValue struct {
	value   string
	version string
}

// session is one sign-in to a store, for one resolution or one test.
type session interface {
	// readPath reads every key at a path, at its current version.
	readPath(ctx context.Context, path string) (map[string]secretValue, error)
	// readVersion reads one key at one version.
	readVersion(ctx context.Context, path, key string, version int) (secretValue, error)
	// identity says who the store took the connection to be, for Test.
	identity(ctx context.Context) (string, error)
	// close gives back what signing in obtained, where the store lets a
	// client do that: a token minted for this run is revoked rather than
	// left to expire.
	close(ctx context.Context)
}

// httpClient is a store's client: its own certificate authority beside the
// system's, TLS 1.2 or later, a bound on each request, and no redirect
// followed — a redirect would carry the token to an address nobody typed.
func httpClient(settings Settings) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if settings.CACert != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(settings.CACert)) {
			return nil, errors.New("the certificate authority cannot be read")
		}
		config.RootCAs = pool
	}
	transport.TLSClientConfig = config
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return fmt.Errorf("the store redirected to %s; set the address to where it answers itself", request.URL.Redacted())
		},
	}, nil
}

// exchange sends one request and reads the answer, bounded.
func exchange(ctx context.Context, client *http.Client, method, address string, headers map[string]string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Just-Dashboard")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, reachError(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer+1))
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("the answer could not be read: %v", err)
	}
	if len(raw) > maxAnswer {
		return response.StatusCode, nil, errors.New("the answer is larger than 1 MiB")
	}
	return response.StatusCode, raw, nil
}

// reachError says why a store could not be reached, without Go's wrapping of
// the method and the whole URL in front of the cause.
func reachError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return fmt.Errorf("no answer within %s from %s", requestTimeout, hostOf(urlErr.URL))
		}
		return fmt.Errorf("could not reach %s: %v", hostOf(urlErr.URL), urlErr.Err)
	}
	return err
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "the store"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// answerMessage reads the store's own words out of an error answer: Vault's
// and OpenBao's errors list, Infisical's message, Doppler's messages. What
// is not JSON is quoted as text, cut short.
func answerMessage(status int, raw []byte) string {
	var shaped struct {
		Errors   []string        `json:"errors"`
		Messages []string        `json:"messages"`
		Message  json.RawMessage `json:"message"`
		Error    string          `json:"error"`
	}
	text := ""
	if json.Unmarshal(raw, &shaped) == nil {
		switch {
		case len(shaped.Errors) > 0:
			text = strings.Join(shaped.Errors, "; ")
		case len(shaped.Messages) > 0:
			text = strings.Join(shaped.Messages, "; ")
		case len(shaped.Message) > 0:
			var message string
			if json.Unmarshal(shaped.Message, &message) == nil {
				text = message
			} else {
				// Infisical answers a validation failure with a list of
				// issues; each says what it found wrong.
				var issues []struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(shaped.Message, &issues) == nil {
					parts := []string{}
					for _, issue := range issues {
						parts = append(parts, issue.Message)
					}
					text = strings.Join(parts, "; ")
				}
			}
		case shaped.Error != "":
			text = shaped.Error
		}
	} else {
		text = string(raw)
	}
	text = cleanText(text)
	if text == "" {
		text = http.StatusText(status)
	}
	return clip(text, 300) + " (" + strconv.Itoa(status) + ")"
}

// cleanText folds a store's message onto one line: Vault wraps its errors in
// newlines and tabs ("1 error occurred:\n\t* permission denied\n\n").
func cleanText(text string) string {
	fields := strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	out := strings.Join(fields, " ")
	out = strings.TrimPrefix(out, "1 error occurred: * ")
	return strings.TrimSpace(out)
}

// openSession signs in to a store with its connection's credentials.
func openSession(ctx context.Context, item *row) (session, error) {
	if item.openErr != nil {
		return nil, item.openErr
	}
	client, err := httpClient(item.Settings)
	if err != nil {
		return nil, err
	}
	switch item.Kind {
	case KindVault, KindOpenBao:
		return openVault(ctx, item, client)
	case KindInfisical:
		return openInfisical(ctx, item, client)
	case KindDoppler:
		return &dopplerSession{base: item.Address, token: item.credentials["token"], client: client}, nil
	}
	return nil, fmt.Errorf("unknown store kind %q", item.Kind)
}

// escapePath escapes each segment of a path for a URL; the segments are
// already restricted to characters that need no escaping, so this is the
// second line, not the first.
func escapePath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// jsonValue renders a value a store keeps as JSON rather than a string — a
// KV secret's field may be a number or an object — the way it was written.
func jsonValue(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) == nil {
		return compact.String()
	}
	return string(raw)
}

// --- Vault and OpenBao -------------------------------------------------------

type vaultSession struct {
	base, mount, namespace, token string
	client                        *http.Client
	minted                        bool
}

func openVault(ctx context.Context, item *row, client *http.Client) (*vaultSession, error) {
	s := &vaultSession{base: item.Address, mount: item.Settings.Mount, namespace: item.Settings.Namespace, client: client}
	if item.Settings.Auth != AuthAppRole {
		s.token = item.credentials["token"]
		return s, nil
	}
	status, raw, err := exchange(ctx, client, http.MethodPost, s.base+"/v1/auth/"+escapePath(item.Settings.AuthMount)+"/login",
		s.headers(false), map[string]string{"role_id": item.credentials["roleId"], "secret_id": item.credentials["secretId"]})
	if err != nil {
		return nil, fmt.Errorf("AppRole sign-in: %v", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("AppRole sign-in refused: %s", answerMessage(status, raw))
	}
	var answer struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.Auth.ClientToken == "" {
		return nil, errors.New("AppRole sign-in answered without a token")
	}
	s.token, s.minted = answer.Auth.ClientToken, true
	return s, nil
}

func (s *vaultSession) headers(withToken bool) map[string]string {
	headers := map[string]string{}
	if withToken {
		headers["X-Vault-Token"] = s.token
	}
	if s.namespace != "" {
		headers["X-Vault-Namespace"] = s.namespace
	}
	return headers
}

// kvAnswer is a KV version 2 read: the fields, and the version they are.
type kvAnswer struct {
	Data struct {
		Data     map[string]json.RawMessage `json:"data"`
		Metadata struct {
			Version      int    `json:"version"`
			DeletionTime string `json:"deletion_time"`
			Destroyed    bool   `json:"destroyed"`
		} `json:"metadata"`
	} `json:"data"`
}

func (s *vaultSession) read(ctx context.Context, path string, version int) (map[string]secretValue, error) {
	address := s.base + "/v1/" + escapePath(s.mount) + "/data/" + escapePath(path)
	if version > 0 {
		address += "?version=" + strconv.Itoa(version)
	}
	status, raw, err := exchange(ctx, s.client, http.MethodGet, address, s.headers(true), nil)
	if err != nil {
		return nil, err
	}
	var answer kvAnswer
	parsed := json.Unmarshal(raw, &answer) == nil
	// A version that was deleted or destroyed answers 404 with its metadata,
	// which says which; a path that holds nothing answers 404 with none.
	if parsed && answer.Data.Data == nil && answer.Data.Metadata.Version > 0 {
		state := "deleted"
		if answer.Data.Metadata.Destroyed {
			state = "destroyed"
		}
		return nil, fmt.Errorf("version %d of %s/%s is %s", answer.Data.Metadata.Version, s.mount, path, state)
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("%s/%s holds no secret (404); a KV version 2 engine has to be mounted at %s", s.mount, path, s.mount)
	}
	if status != http.StatusOK {
		return nil, errors.New(answerMessage(status, raw))
	}
	if !parsed || answer.Data.Data == nil {
		return nil, fmt.Errorf("%s/%s did not answer as a KV version 2 secret", s.mount, path)
	}
	out := make(map[string]secretValue, len(answer.Data.Data))
	reported := strconv.Itoa(answer.Data.Metadata.Version)
	for key, value := range answer.Data.Data {
		out[key] = secretValue{value: jsonValue(value), version: reported}
	}
	return out, nil
}

func (s *vaultSession) readPath(ctx context.Context, path string) (map[string]secretValue, error) {
	return s.read(ctx, path, 0)
}

func (s *vaultSession) readVersion(ctx context.Context, path, key string, version int) (secretValue, error) {
	values, err := s.read(ctx, path, version)
	if err != nil {
		return secretValue{}, err
	}
	value, ok := values[key]
	if !ok {
		return secretValue{}, fmt.Errorf("version %d of %s/%s has no key %s", version, s.mount, path, key)
	}
	return value, nil
}

func (s *vaultSession) identity(ctx context.Context) (string, error) {
	status, raw, err := exchange(ctx, s.client, http.MethodGet, s.base+"/v1/auth/token/lookup-self", s.headers(true), nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", errors.New(answerMessage(status, raw))
	}
	var answer struct {
		Data struct {
			DisplayName string   `json:"display_name"`
			Policies    []string `json:"policies"`
			TTL         int64    `json:"ttl"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return "", errors.New("the token lookup did not answer as expected")
	}
	who := "signed in as " + firstNonEmpty(answer.Data.DisplayName, "a token")
	if len(answer.Data.Policies) > 0 {
		who += " with " + strings.Join(answer.Data.Policies, ", ")
	}
	if answer.Data.TTL > 0 {
		who += fmt.Sprintf("; the token expires in %s", (time.Duration(answer.Data.TTL) * time.Second).String())
	}
	return who, nil
}

func (s *vaultSession) close(ctx context.Context) {
	if !s.minted {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _, _ = exchange(ctx, s.client, http.MethodPost, s.base+"/v1/auth/token/revoke-self", s.headers(true), nil)
}

// --- Infisical ---------------------------------------------------------------

// infisicalSession reads through the v3 "raw" secrets API, which every
// self-hosted release since 2023 serves, Infisical Cloud included, with a
// Universal Auth machine identity's access token.
type infisicalSession struct {
	base, project, environment, token string
	client                            *http.Client
}

func openInfisical(ctx context.Context, item *row, client *http.Client) (*infisicalSession, error) {
	s := &infisicalSession{base: item.Address, project: item.Settings.Project, environment: item.Settings.Environment, client: client}
	status, raw, err := exchange(ctx, client, http.MethodPost, s.base+"/api/v1/auth/universal-auth/login", nil,
		map[string]string{"clientId": item.credentials["clientId"], "clientSecret": item.credentials["clientSecret"]})
	if err != nil {
		return nil, fmt.Errorf("Universal Auth sign-in: %v", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Universal Auth sign-in refused: %s", answerMessage(status, raw))
	}
	var answer struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.AccessToken == "" {
		return nil, errors.New("Universal Auth sign-in answered without an access token")
	}
	s.token = answer.AccessToken
	return s, nil
}

func (s *infisicalSession) folder(path string) string {
	return "/" + strings.Trim(path, "/")
}

func (s *infisicalSession) query(path string) url.Values {
	query := url.Values{}
	query.Set("workspaceId", s.project)
	query.Set("environment", s.environment)
	query.Set("secretPath", s.folder(path))
	return query
}

type infisicalSecret struct {
	SecretKey   string `json:"secretKey"`
	SecretValue string `json:"secretValue"`
	Version     int    `json:"version"`
}

func (s *infisicalSession) readPath(ctx context.Context, path string) (map[string]secretValue, error) {
	query := s.query(path)
	query.Set("expandSecretReferences", "true")
	status, raw, err := exchange(ctx, s.client, http.MethodGet, s.base+"/api/v3/secrets/raw?"+query.Encode(),
		map[string]string{"Authorization": "Bearer " + s.token}, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, errors.New(answerMessage(status, raw))
	}
	var answer struct {
		Secrets []infisicalSecret `json:"secrets"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return nil, errors.New("the secrets list did not answer as expected")
	}
	out := make(map[string]secretValue, len(answer.Secrets))
	for _, secret := range answer.Secrets {
		out[secret.SecretKey] = secretValue{value: secret.SecretValue, version: strconv.Itoa(secret.Version)}
	}
	return out, nil
}

func (s *infisicalSession) readVersion(ctx context.Context, path, key string, version int) (secretValue, error) {
	query := s.query(path)
	query.Set("version", strconv.Itoa(version))
	status, raw, err := exchange(ctx, s.client, http.MethodGet, s.base+"/api/v3/secrets/raw/"+url.PathEscape(key)+"?"+query.Encode(),
		map[string]string{"Authorization": "Bearer " + s.token}, nil)
	if err != nil {
		return secretValue{}, err
	}
	if status != http.StatusOK {
		return secretValue{}, errors.New(answerMessage(status, raw))
	}
	var answer struct {
		Secret infisicalSecret `json:"secret"`
	}
	if json.Unmarshal(raw, &answer) != nil || answer.Secret.SecretKey == "" {
		return secretValue{}, errors.New("the secret did not answer as expected")
	}
	return secretValue{value: answer.Secret.SecretValue, version: strconv.Itoa(answer.Secret.Version)}, nil
}

func (s *infisicalSession) identity(ctx context.Context) (string, error) {
	values, err := s.readPath(ctx, "")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("signed in; %d %s at / in %s", len(values), pluralWord(len(values), "secret"), s.environment), nil
}

func (s *infisicalSession) close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _, _ = exchange(ctx, s.client, http.MethodPost, s.base+"/api/v1/auth/token/revoke", nil, map[string]string{"accessToken": s.token})
}

// --- Doppler -----------------------------------------------------------------

// dopplerSession reads a service token's config through the download
// endpoint: one request answers every secret, and the token alone says which
// project and config.
type dopplerSession struct {
	base, token string
	client      *http.Client
}

func (s *dopplerSession) download(ctx context.Context) (map[string]string, error) {
	status, raw, err := exchange(ctx, s.client, http.MethodGet, s.base+"/v3/configs/config/secrets/download?format=json",
		map[string]string{"Authorization": "Bearer " + s.token}, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, errors.New(answerMessage(status, raw))
	}
	var values map[string]string
	if json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("the config download did not answer as expected")
	}
	return values, nil
}

func (s *dopplerSession) readPath(ctx context.Context, _ string) (map[string]secretValue, error) {
	values, err := s.download(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]secretValue, len(values))
	for key, value := range values {
		out[key] = secretValue{value: value}
	}
	return out, nil
}

func (s *dopplerSession) readVersion(context.Context, string, string, int) (secretValue, error) {
	return secretValue{}, errors.New("Doppler does not serve earlier versions of a secret")
}

func (s *dopplerSession) identity(ctx context.Context) (string, error) {
	values, err := s.download(ctx)
	if err != nil {
		return "", err
	}
	count := 0
	for key := range values {
		if !strings.HasPrefix(key, "DOPPLER_") {
			count++
		}
	}
	where := "this token's config"
	if values["DOPPLER_PROJECT"] != "" && values["DOPPLER_CONFIG"] != "" {
		where = values["DOPPLER_PROJECT"] + " / " + values["DOPPLER_CONFIG"]
	}
	return fmt.Sprintf("read %s; %d %s", where, count, pluralWord(count, "secret")), nil
}

func (s *dopplerSession) close(context.Context) {}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func pluralWord(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
