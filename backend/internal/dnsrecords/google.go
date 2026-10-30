package dnsrecords

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Google Cloud DNS's v1 API, signed in as the service account whose JSON
// key the Certificates page saved: a JWT signed with the key's RS256
// private key is exchanged at the key's token_uri for an access token
// (the OAuth 2.0 JWT bearer grant), which every call carries. Records are
// sets created whole (rrsets.create) and changed whole (rrsets.patch), so
// nothing is ever deleted to change one; the zones are the key's own
// project's, and a private zone, which answers only inside a VPC, is left
// out.
//
// https://cloud.google.com/dns/docs/reference/rest/v1/resourceRecordSets
// https://developers.google.com/identity/protocols/oauth2/service-account#httprest

const (
	googleAPI   = "https://dns.googleapis.com/dns/v1"
	googleToken = "https://oauth2.googleapis.com/token"
	googleScope = "https://www.googleapis.com/auth/ndev.clouddns.readwrite"
)

type googleKey struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

type google struct {
	api     rest
	key     googleKey
	signer  *rsa.PrivateKey
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	token   string
	expires time.Time
}

func newGoogle(credentials string, options Options) (Provider, error) {
	var key googleKey
	if err := json.Unmarshal([]byte(credentials), &key); err != nil {
		return nil, fmt.Errorf("%w: the saved Google Cloud DNS key is not the service account's JSON key: %v", ErrUnsupported, err)
	}
	if key.Type != "service_account" || key.ClientEmail == "" || key.PrivateKey == "" || key.ProjectID == "" {
		return nil, fmt.Errorf("%w: the saved Google Cloud DNS key has no service account, project or private key", ErrUnsupported)
	}
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("%w: the service account's private key is not PEM", ErrUnsupported)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	signer, ok := parsed.(*rsa.PrivateKey)
	if err != nil || !ok {
		return nil, fmt.Errorf("%w: the service account's private key is not an RSA key", ErrUnsupported)
	}
	if key.TokenURI == "" {
		key.TokenURI = googleToken
	}
	provider := &google{key: key, signer: signer, client: options.client(), now: options.now}
	provider.api = rest{
		name: "Google Cloud DNS", base: options.base(googleAPI), client: options.client(),
		authorize: func(request *http.Request, _ []byte) error {
			token, err := provider.accessToken(request.Context())
			if err != nil {
				return err
			}
			request.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
		refusal: func(status int, raw []byte) string {
			var answer struct {
				Error struct {
					Message string `json:"message"`
					Status  string `json:"status"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &answer) != nil || answer.Error.Message == "" {
				return plainRefusal(status, raw)
			}
			if answer.Error.Status != "" {
				return answer.Error.Message + " (" + answer.Error.Status + ")"
			}
			return answer.Error.Message
		},
	}
	return provider, nil
}

func (g *google) Key() string     { return "google" }
func (g *google) Name() string    { return "Google Cloud DNS" }
func (g *google) PerRecord() bool { return false }
func (g *google) MinTTL() int     { return 1 }

// assertion is the signed JWT the token endpoint exchanges.
func (g *google) assertion(now time.Time) (string, error) {
	segment := func(value any) (string, error) {
		raw, err := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw), err
	}
	header, err := segment(map[string]string{"alg": "RS256", "typ": "JWT", "kid": g.key.PrivateKeyID})
	if err != nil {
		return "", err
	}
	claims, err := segment(map[string]any{
		"iss": g.key.ClientEmail, "scope": googleScope, "aud": g.key.TokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(header + "." + claims))
	signature, err := rsa.SignPKCS1v15(rand.Reader, g.signer, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return header + "." + claims + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// accessToken signs in once and reuses the token until a minute before it
// expires.
func (g *google) accessToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if g.token != "" && now.Before(g.expires.Add(-time.Minute)) {
		return g.token, nil
	}
	assertion, err := g.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.key.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := g.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Google could not be reached to sign the service account in: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var answer struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &answer)
	if response.StatusCode >= 300 || answer.AccessToken == "" {
		message := strings.TrimSpace(answer.Error + ": " + answer.Description)
		if answer.Error == "" {
			message = plainRefusal(response.StatusCode, raw)
		}
		return "", &Error{Provider: "Google", Operation: "sign " + g.key.ClientEmail + " in", Status: response.StatusCode, Message: message}
	}
	if answer.ExpiresIn <= 0 {
		answer.ExpiresIn = 3600
	}
	g.token, g.expires = answer.AccessToken, now.Add(time.Duration(answer.ExpiresIn)*time.Second)
	return g.token, nil
}

func (g *google) project() string { return "/projects/" + url.PathEscape(g.key.ProjectID) }

func (g *google) Zones(ctx context.Context, _ string) ([]Zone, error) {
	out := []Zone{}
	pageToken := ""
	for page := 0; page < 50; page++ {
		query := url.Values{"maxResults": {"500"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var answer struct {
			Zones []struct {
				Name       string `json:"name"`
				DNSName    string `json:"dnsName"`
				Visibility string `json:"visibility"`
			} `json:"managedZones"`
			Next string `json:"nextPageToken"`
		}
		if err := g.api.call(ctx, "list the managed zones of "+g.key.ProjectID, http.MethodGet, g.project()+"/managedZones", query, nil, &answer); err != nil {
			return nil, err
		}
		for _, zone := range answer.Zones {
			if zone.Visibility == "private" {
				continue
			}
			out = append(out, Zone{ID: zone.Name, Name: Canonical(zone.DNSName), Account: g.key.ProjectID})
		}
		if answer.Next == "" {
			break
		}
		pageToken = answer.Next
	}
	return out, nil
}

type googleSet struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	RRDatas []string `json:"rrdatas"`
}

func (g *google) rrsets(zone Zone) string {
	return g.project() + "/managedZones/" + url.PathEscape(zone.ID) + "/rrsets"
}

func (g *google) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	var answer struct {
		Sets []googleSet `json:"rrsets"`
	}
	if err := g.api.call(ctx, "read the records of "+Canonical(name), http.MethodGet, g.rrsets(zone),
		url.Values{"name": {fqdn(name)}}, nil, &answer); err != nil {
		return nil, err
	}
	out := []Set{}
	for _, set := range answer.Sets {
		kind := Type(strings.ToUpper(set.Type))
		if Canonical(set.Name) != Canonical(name) || (kind != TypeA && kind != TypeAAAA && kind != TypeCNAME) {
			continue
		}
		read := Set{Name: Canonical(name), Type: kind, TTL: set.TTL, Values: append([]string(nil), set.RRDatas...)}
		if kind == TypeCNAME {
			for index := range read.Values {
				read.Values[index] = Canonical(read.Values[index])
			}
		}
		out = append(out, read)
	}
	return out, nil
}

func (g *google) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	set := googleSet{Name: fqdn(want.Name), Type: string(want.Type), TTL: want.TTL}
	for _, value := range want.Values {
		if want.Type == TypeCNAME {
			value = fqdn(value)
		}
		set.RRDatas = append(set.RRDatas, value)
	}
	if current == nil {
		return g.api.call(ctx, "create "+string(want.Type)+" "+Canonical(want.Name), http.MethodPost, g.rrsets(zone), nil, set, nil)
	}
	return g.api.call(ctx, "change "+string(want.Type)+" "+Canonical(want.Name), http.MethodPatch,
		g.rrsets(zone)+"/"+url.PathEscape(set.Name)+"/"+url.PathEscape(set.Type), nil, set, nil)
}
