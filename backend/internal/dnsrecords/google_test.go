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
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// googleFake is Google's OAuth token endpoint, which checks the service
// account's JWT against its public key, and Cloud DNS v1's managedZones
// and rrsets, one zone per page.
type googleFake struct {
	fakeStore
	public  *rsa.PublicKey
	email   string
	project string
	tokens  int
	url     string
}

func googleFail(w http.ResponseWriter, status int, state, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": message, "status": state,
		"errors": []map[string]string{{"message": message, "domain": "global", "reason": strings.ToLower(state)}}}})
}

func (f *googleFake) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	fail := func(description string) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": description})
	}
	if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		fail("Invalid grant_type")
		return
	}
	parts := strings.Split(r.Form.Get("assertion"), ".")
	if len(parts) != 3 {
		fail("Invalid JWT")
		return
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(f.public, crypto.SHA256, digest[:], signature) != nil {
		fail("Invalid JWT Signature.")
		return
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iss, Scope, Aud string
		Iat, Exp        int64
	}
	_ = json.Unmarshal(raw, &claims)
	if claims.Iss != f.email || claims.Aud != f.url+"/token" || !strings.Contains(claims.Scope, "ndev.clouddns.readwrite") ||
		claims.Exp-claims.Iat > 3600 || claims.Exp < time.Now().Unix() {
		fail("Invalid JWT: claims")
		return
	}
	f.tokens++
	writeJSON(w, http.StatusOK, map[string]any{"access_token": "ya29.fake", "expires_in": 3599, "token_type": "Bearer"})
}

func (f *googleFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	if r.URL.Path == "/token" {
		f.token(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer ya29.fake" {
		googleFail(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Request had invalid authentication credentials.")
		return
	}
	parts := segments(r.URL.Path, "/dns/v1")
	if len(parts) < 3 || parts[0] != "projects" || parts[2] != "managedZones" {
		googleFail(w, http.StatusNotFound, "NOT_FOUND", "Not Found")
		return
	}
	if parts[1] != f.project {
		googleFail(w, http.StatusForbidden, "PERMISSION_DENIED", "Forbidden")
		return
	}
	if len(parts) == 3 && r.Method == http.MethodGet {
		index := 0
		if token := r.URL.Query().Get("pageToken"); token != "" {
			index = mustAtoi(token)
		}
		answer := map[string]any{"managedZones": []any{}}
		if index < len(f.zones) {
			zone := f.zones[index]
			visibility := "public"
			if zone.Private {
				visibility = "private"
			}
			answer["managedZones"] = []any{map[string]string{"name": zone.ID, "dnsName": zone.Name + ".", "visibility": visibility}}
		}
		if index+1 < len(f.zones) {
			answer["nextPageToken"] = strconv.Itoa(index + 1)
		}
		writeJSON(w, http.StatusOK, answer)
		return
	}
	zone, ok := f.zone(parts[3])
	if !ok || len(parts) < 5 || parts[4] != "rrsets" {
		googleFail(w, http.StatusNotFound, "NOT_FOUND", "The 'parameters.managedZone' resource named '"+strings.Join(parts[3:], "/")+"' does not exist.")
		return
	}
	switch {
	case len(parts) == 5 && r.Method == http.MethodGet:
		sets := []any{}
		for _, set := range f.sets(zone.ID) {
			if r.URL.Query().Get("name") != "" && set.Name+"." != r.URL.Query().Get("name") {
				continue
			}
			values := append([]string(nil), set.Values...)
			if set.Type == "CNAME" {
				for index := range values {
					values[index] += "."
				}
			}
			sets = append(sets, googleSet{Name: set.Name + ".", Type: set.Type, TTL: set.TTL, RRDatas: values})
		}
		writeJSON(w, http.StatusOK, map[string]any{"rrsets": sets})
	case (len(parts) == 5 && r.Method == http.MethodPost) || (len(parts) == 7 && r.Method == http.MethodPatch):
		var set googleSet
		if json.NewDecoder(r.Body).Decode(&set) != nil || !strings.HasSuffix(set.Name, ".") {
			googleFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid value for 'entity.rrset.name'")
			return
		}
		name := Canonical(set.Name)
		exists := len(f.find(zone.ID, name, set.Type)) > 0
		if r.Method == http.MethodPost && exists {
			googleFail(w, http.StatusConflict, "ALREADY_EXISTS", "The resource 'entity.rrset' named '"+set.Name+" ("+set.Type+")' already exists")
			return
		}
		if r.Method == http.MethodPatch && (!exists || parts[5] != set.Name || parts[6] != set.Type) {
			googleFail(w, http.StatusNotFound, "NOT_FOUND", "The 'parameters.name' resource named '"+parts[5]+"' does not exist.")
			return
		}
		if f.conflicts(zone.ID, name, set.Type) {
			googleFail(w, http.StatusBadRequest, "INVALID_ARGUMENT", "The resource record set 'entity.rrset' is invalid because a CNAME cannot be placed at this name.")
			return
		}
		values := append([]string(nil), set.RRDatas...)
		for index := range values {
			values[index] = strings.TrimSuffix(values[index], ".")
		}
		f.replaceSet(zone.ID, name, set.Type, values, set.TTL)
		writeJSON(w, http.StatusOK, set)
	default:
		googleFail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func googleKeyFile(t *testing.T, tokenURI, project string) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": project, "private_key_id": "kid-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "certbot@" + project + ".iam.gserviceaccount.com", "token_uri": tokenURI,
	})
	return string(file), &key.PublicKey
}

// The service account signs in once for every call, a private zone of the
// same name is passed over, and a set is created and patched whole.
func TestGoogleCloudDNSSignsInAsTheServiceAccountAndWritesSets(t *testing.T) {
	fake := &googleFake{project: "acme-dns", email: "certbot@acme-dns.iam.gserviceaccount.com", fakeStore: fakeStore{zones: []fakeZone{
		{ID: "root", Name: "example.com"}, {ID: "internal", Name: "preview.example.com", Private: true}, {ID: "preview", Name: "preview.example.com"},
	}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	fake.url = server.URL
	credentials, public := googleKeyFile(t, server.URL+"/token", "acme-dns")
	fake.public = public
	provider := mustProvider(t, "google", credentials, Options{BaseURL: server.URL + "/dns/v1"})
	exercise(t, provider, "app.preview.example.com", "preview.example.com", "tunnel.preview.example.com", false)
	if fake.tokens != 1 {
		t.Fatalf("the service account signed in %d times", fake.tokens)
	}
	if len(fake.find("internal", "", "")) != 0 {
		t.Fatal("the private zone was written")
	}
}

func TestGoogleCloudDNSRefusalsAreItsOwnWords(t *testing.T) {
	fake := &googleFake{project: "acme-dns", email: "certbot@acme-dns.iam.gserviceaccount.com", fakeStore: fakeStore{zones: []fakeZone{{ID: "root", Name: "example.com"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	fake.url = server.URL
	credentials, _ := googleKeyFile(t, server.URL+"/token", "acme-dns")
	_, fake.public = googleKeyFile(t, server.URL+"/token", "acme-dns")
	_, err := mustProvider(t, "google", credentials, Options{BaseURL: server.URL + "/dns/v1"}).Zones(context.Background(), "x")
	refusedInWords(t, err, "Google refused to sign certbot@acme-dns.iam.gserviceaccount.com in", "invalid_grant: Invalid JWT Signature.")

	other, public := googleKeyFile(t, server.URL+"/token", "someone-else")
	fake.public, fake.email = public, "certbot@someone-else.iam.gserviceaccount.com"
	_, err = mustProvider(t, "google", other, Options{BaseURL: server.URL + "/dns/v1"}).Zones(context.Background(), "x")
	refusedInWords(t, err, "Google Cloud DNS refused to list the managed zones of someone-else", "Forbidden (PERMISSION_DENIED)")
}
