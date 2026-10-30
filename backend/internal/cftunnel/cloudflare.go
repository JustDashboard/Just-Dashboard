// Package cftunnel is Cloudflare Tunnel as an ingress: a cloudflared
// connector the dashboard runs as a container of its own, pointed at this
// host's proxy, so a deployment's domain can be served through Cloudflare's
// edge without a public address or an open port on this server.
//
// The pieces are the Cloudflare API (this file), the tunnel's ingress
// configuration (config.go), the connector container (connector.go) and the
// connection kept in the store with the reconcile pass that keeps all three
// in agreement with the deployments routed through it (manager.go).
package cftunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIBase is Cloudflare's v4 API.
const DefaultAPIBase = "https://api.cloudflare.com/client/v4"

// API is the slice of Cloudflare's API a tunnel needs: tunnels, their
// configuration, and the zone records that send a hostname to one. It
// authenticates with the credentials saved for the DNS-01 plugin under
// Certificates — an API token, or the account e-mail and global key the
// plugin also accepts — so the operator saves one credential for both.
type API struct {
	Base string
	// Token is an API token, sent as a bearer token.
	Token string
	// Email and Key are the legacy global key pair.
	Email, Key string
	Client     *http.Client
}

// Error is Cloudflare's refusal in its own words: the status and every
// error it listed, code and message.
type Error struct {
	Status   int
	Messages []ErrorMessage
	// Operation is what was being asked, for the sentence.
	Operation string
}

// ErrorMessage is one entry of Cloudflare's errors array.
type ErrorMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Messages))
	for _, message := range e.Messages {
		if message.Code != 0 {
			parts = append(parts, fmt.Sprintf("%s (code %d)", message.Message, message.Code))
		} else {
			parts = append(parts, message.Message)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, http.StatusText(e.Status))
	}
	return fmt.Sprintf("Cloudflare refused to %s: %s", e.Operation, strings.Join(parts, "; "))
}

// envelope is every v4 answer's shape.
type envelope struct {
	Success bool            `json:"success"`
	Errors  []ErrorMessage  `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

func (a API) configured() error {
	if a.Token == "" && (a.Email == "" || a.Key == "") {
		return errors.New("no Cloudflare credentials are saved: save an API token for the Cloudflare DNS provider under Certificates")
	}
	return nil
}

// call sends one request and decodes the result into out.
func (a API) call(ctx context.Context, operation, method, path string, query url.Values, body, out any) error {
	if err := a.configured(); err != nil {
		return err
	}
	base := strings.TrimSuffix(a.Base, "/")
	if base == "" {
		base = DefaultAPIBase
	}
	target := base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if a.Token != "" {
		request.Header.Set("Authorization", "Bearer "+a.Token)
	} else {
		request.Header.Set("X-Auth-Email", a.Email)
		request.Header.Set("X-Auth-Key", a.Key)
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		// The transport's error names the URL, never the credentials.
		return fmt.Errorf("Cloudflare could not be reached to %s: %w", operation, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	var answer envelope
	if err := json.Unmarshal(raw, &answer); err != nil {
		return &Error{Status: response.StatusCode, Operation: operation,
			Messages: []ErrorMessage{{Message: "an answer that is not Cloudflare's JSON (" + http.StatusText(response.StatusCode) + ")"}}}
	}
	if !answer.Success || response.StatusCode >= 300 {
		return &Error{Status: response.StatusCode, Messages: answer.Errors, Operation: operation}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(answer.Result, out); err != nil {
		return fmt.Errorf("Cloudflare's answer to %s could not be read: %w", operation, err)
	}
	return nil
}

// Account is one account the credentials reach.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Accounts lists the accounts the credentials reach.
func (a API) Accounts(ctx context.Context) ([]Account, error) {
	var accounts []Account
	err := a.call(ctx, "list the accounts", http.MethodGet, "/accounts", url.Values{"per_page": {"50"}}, nil, &accounts)
	return accounts, err
}

// Zone is a zone and the account it belongs to.
type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// ZoneFor finds the zone a hostname is in: the longest of its suffixes that
// is a zone these credentials reach.
func (a API) ZoneFor(ctx context.Context, hostname string) (Zone, error) {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(hostname), "."), ".")
	for index := 0; index < len(labels)-1; index++ {
		name := strings.Join(labels[index:], ".")
		var zones []Zone
		if err := a.call(ctx, "look up the zone "+name, http.MethodGet, "/zones", url.Values{"name": {name}}, nil, &zones); err != nil {
			return Zone{}, err
		}
		if len(zones) > 0 {
			return zones[0], nil
		}
	}
	return Zone{}, fmt.Errorf("no zone these Cloudflare credentials reach holds %s", hostname)
}

// Tunnel is one Cloudflare Tunnel.
type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_tag"`
	Status    string `json:"status,omitempty"`
}

// CreateTunnel creates a tunnel whose configuration Cloudflare keeps, so the
// dashboard sets its ingress rules through the API rather than a file
// inside the connector.
func (a API) CreateTunnel(ctx context.Context, accountID, name string) (Tunnel, error) {
	var tunnel Tunnel
	err := a.call(ctx, "create the tunnel "+name, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel", nil,
		map[string]any{"name": name, "config_src": "cloudflare"}, &tunnel)
	if tunnel.AccountID == "" {
		tunnel.AccountID = accountID
	}
	return tunnel, err
}

// GetTunnel reads a tunnel, which is how a pasted token's tunnel is checked
// against the credentials that would configure it.
func (a API) GetTunnel(ctx context.Context, accountID, tunnelID string) (Tunnel, error) {
	var tunnel Tunnel
	err := a.call(ctx, "read the tunnel", http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel/"+url.PathEscape(tunnelID), nil, nil, &tunnel)
	return tunnel, err
}

// TunnelToken is the token a connector runs the tunnel with.
func (a API) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var token string
	err := a.call(ctx, "issue the tunnel's token", http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel/"+url.PathEscape(tunnelID)+"/token", nil, nil, &token)
	return token, err
}

// PutConfiguration replaces the tunnel's ingress rules.
func (a API) PutConfiguration(ctx context.Context, accountID, tunnelID string, configuration Configuration) error {
	return a.call(ctx, "set the tunnel's routes", http.MethodPut,
		"/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel/"+url.PathEscape(tunnelID)+"/configurations", nil,
		map[string]any{"config": configuration}, nil)
}

// DNSRecord is one record of a zone.
type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

// DNSOutcome is what EnsureCNAME found or did.
type DNSOutcome string

const (
	DNSCreated DNSOutcome = "created"
	DNSPresent DNSOutcome = "present"
	DNSUpdated DNSOutcome = "updated"
)

// RecordComment marks a record this dashboard created, so it is the one it
// may move to another tunnel later and never an operator's own. The
// Domains settings' Create the record leaves the same mark on a tunnel's
// CNAME it writes (api/handlers_deploy_dns_record.go).
const RecordComment = "Just Dashboard: routed through Cloudflare Tunnel"

// EnsureCNAME makes hostname a proxied CNAME to the tunnel. A record of the
// name that already points there is left as it is; one this dashboard made
// for another tunnel is moved; any other record of the name is refused in
// Cloudflare's words rather than replaced — it is somebody's, and the
// hostname may be serving from it now.
func (a API) EnsureCNAME(ctx context.Context, zoneID, hostname, tunnelID string) (DNSOutcome, error) {
	target := TunnelTarget(tunnelID)
	var records []DNSRecord
	if err := a.call(ctx, "read the records of "+hostname, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records", url.Values{"name": {hostname}}, nil, &records); err != nil {
		return "", err
	}
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSuffix(record.Name, "."), hostname) {
			continue
		}
		if record.Type == "CNAME" && strings.EqualFold(record.Content, target) {
			if record.Proxied {
				return DNSPresent, nil
			}
			record.Proxied = true
			err := a.call(ctx, "proxy the record of "+hostname, http.MethodPatch, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(record.ID), nil,
				map[string]any{"proxied": true}, nil)
			return DNSUpdated, err
		}
		if record.Type == "CNAME" && record.Comment == RecordComment {
			err := a.call(ctx, "point "+hostname+" at the tunnel", http.MethodPatch, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(record.ID), nil,
				map[string]any{"content": target, "proxied": true}, nil)
			return DNSUpdated, err
		}
		return "", fmt.Errorf("%s already has a %s record to %s in Cloudflare, which is not this dashboard's; remove it, and the next pass points the name at the tunnel", hostname, record.Type, record.Content)
	}
	err := a.call(ctx, "create the record of "+hostname, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", nil,
		DNSRecord{Type: "CNAME", Name: hostname, Content: target, Proxied: true, Comment: RecordComment}, nil)
	if err != nil {
		return "", err
	}
	return DNSCreated, nil
}

// TunnelTarget is the name a hostname's CNAME points at to reach a tunnel.
func TunnelTarget(tunnelID string) string { return tunnelID + ".cfargotunnel.com" }
