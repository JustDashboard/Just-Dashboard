package secretstoretest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// AzureVersion is one version of a Key Vault secret: its 32-character id and
// its value.
type AzureVersion struct {
	ID    string
	Value string
}

// Azure is an Entra tenant's token endpoint and one Key Vault, served at the
// same address: the vault's secrets under /secrets, the token under
// /{tenant}/oauth2/v2.0/token. Secrets maps a name to its versions, oldest
// first; the last is the current one.
type Azure struct {
	TenantID, ClientID, ClientSecret string
	Secrets                          map[string][]AzureVersion
	// ExpiresIn is what a token says it lasts, in seconds; 3599 when zero.
	ExpiresIn int
	// ListDenied answers the list with Forbidden, as an application given
	// only the get permission is answered.
	ListDenied bool

	mu       sync.Mutex
	tokens   int
	requests []string
}

// Tokens is how many access tokens were issued.
func (f *Azure) Tokens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokens
}

// Requests are the vault requests so far, as "METHOD /path".
func (f *Azure) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func vaultError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (f *Azure) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
		f.token(w, r)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if r.URL.Query().Get("api-version") == "" {
		vaultError(w, http.StatusBadRequest, "BadParameter", "api-version must be specified")
		return
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer fixture-token-") {
		w.Header().Set("WWW-Authenticate", `Bearer authorization="https://login.microsoftonline.com/`+f.TenantID+`", resource="https://vault.azure.net"`)
		vaultError(w, http.StatusUnauthorized, "Unauthorized", "AKV10000: Request is missing a Bearer or PoP token.")
		return
	}
	base := "http://" + r.Host + "/secrets/"
	switch {
	case r.URL.Path == "/secrets":
		if f.ListDenied {
			vaultError(w, http.StatusForbidden, "Forbidden",
				"The user, group or application 'appid="+f.ClientID+"' does not have secrets list permission on key vault 'jdtest;location=westeurope'. For help resolving this issue, please see https://go.microsoft.com/fwlink/?linkid=2125287")
			return
		}
		list := []map[string]any{}
		for name, versions := range f.Secrets {
			if len(versions) == 0 {
				continue
			}
			list = append(list, map[string]any{"id": base + name, "attributes": map[string]any{"enabled": true, "created": 1700000000, "updated": 1700000000}})
		}
		if len(list) > 1 {
			list = list[:1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"value": list, "nextLink": nil})
	case strings.HasPrefix(r.URL.Path, "/secrets/"):
		rest := strings.TrimPrefix(r.URL.Path, "/secrets/")
		name, version, _ := strings.Cut(rest, "/")
		versions := f.Secrets[name]
		var found *AzureVersion
		if version == "" && len(versions) > 0 {
			found = &versions[len(versions)-1]
		}
		for index := range versions {
			if version != "" && versions[index].ID == version {
				found = &versions[index]
			}
		}
		if found == nil {
			what := "(name/id) " + name
			if version != "" {
				what = "(name/id) " + name + "/" + version
			}
			vaultError(w, http.StatusNotFound, "SecretNotFound",
				"A secret with "+what+" was not found in this key vault. If you recently deleted this secret you may be able to recover it using the correct recovery command. For help resolving this issue, please see https://go.microsoft.com/fwlink/?linkid=2125182")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": found.Value, "id": base + name + "/" + found.ID,
			"attributes": map[string]any{"enabled": true, "created": 1700000000, "updated": 1700000000, "recoveryLevel": "Recoverable+Purgeable"},
		})
	default:
		vaultError(w, http.StatusNotFound, "NotFound", "")
	}
}

func (f *Azure) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	refuse := func(status int, code, description string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": code, "error_description": description + "\r\nTrace ID: 0ff1ce00-1234-4321-abcd-00000000beef\r\nCorrelation ID: c0ffee00-1234-4321-abcd-000000000001\r\nTimestamp: 2026-10-28 09:15:42Z",
			"error_codes": []int{7000215}, "timestamp": "2026-10-28 09:15:42Z", "trace_id": "0ff1ce00-1234-4321-abcd-00000000beef",
			"correlation_id": "c0ffee00-1234-4321-abcd-000000000001",
		})
	}
	if err := r.ParseForm(); err != nil || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		refuse(http.StatusBadRequest, "invalid_request", "AADSTS900144: The request body must contain the following parameter: 'grant_type'.")
		return
	}
	tenant := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/oauth2/v2.0/token")
	if tenant != f.TenantID {
		refuse(http.StatusBadRequest, "invalid_request", "AADSTS90002: Tenant '"+tenant+"' not found. Check to make sure you have the correct tenant ID and are signing into the correct cloud.")
		return
	}
	if r.PostForm.Get("grant_type") != "client_credentials" || !strings.HasSuffix(r.PostForm.Get("scope"), "/.default") {
		refuse(http.StatusBadRequest, "invalid_request", "AADSTS70011: The provided request must include a 'scope' input parameter.")
		return
	}
	if r.PostForm.Get("client_id") != f.ClientID {
		refuse(http.StatusBadRequest, "unauthorized_client", "AADSTS700016: Application with identifier '"+r.PostForm.Get("client_id")+"' was not found in the directory 'jdtest'.")
		return
	}
	if r.PostForm.Get("client_secret") != f.ClientSecret {
		refuse(http.StatusUnauthorized, "invalid_client", "AADSTS7000215: Invalid client secret provided. Ensure the secret being sent in the request is the client secret value, not the client secret ID, for a secret added to app '"+f.ClientID+"'.")
		return
	}
	f.mu.Lock()
	f.tokens++
	token := fmt.Sprintf("fixture-token-%d", f.tokens)
	f.mu.Unlock()
	expires := f.ExpiresIn
	if expires == 0 {
		expires = 3599
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"token_type": "Bearer", "expires_in": expires, "ext_expires_in": expires, "access_token": token})
}
