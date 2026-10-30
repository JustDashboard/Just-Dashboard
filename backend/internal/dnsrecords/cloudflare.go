package dnsrecords

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Cloudflare's v4 API: one record per value, an envelope around every
// answer, and the proxied flag and comment kept beside each record. The
// token is the one saved for the DNS-01 plugin (Zone › DNS › Edit), or the
// account e-mail with its global key. cftunnel speaks to the same API for
// the tunnel itself; this is the zone half, kept here so this package
// stays a leaf.
//
// https://developers.cloudflare.com/api/resources/dns/subresources/records/

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

type cloudflare struct{ api rest }

func newCloudflare(credentials string, options Options) (Provider, error) {
	values := iniValues(credentials)
	token := values["dns_cloudflare_api_token"]
	email, key := values["dns_cloudflare_email"], values["dns_cloudflare_api_key"]
	if token == "" && (email == "" || key == "") {
		return nil, fmt.Errorf("%w: the saved Cloudflare credentials hold neither an API token nor an e-mail and global key", ErrUnsupported)
	}
	return &cloudflare{api: rest{
		name: "Cloudflare", base: options.base(cloudflareAPI), client: options.client(),
		authorize: func(request *http.Request, _ []byte) error {
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			} else {
				request.Header.Set("X-Auth-Email", email)
				request.Header.Set("X-Auth-Key", key)
			}
			return nil
		},
		refusal: cloudflareRefusal,
	}}, nil
}

func (c *cloudflare) Key() string     { return "cloudflare" }
func (c *cloudflare) Name() string    { return "Cloudflare" }
func (c *cloudflare) PerRecord() bool { return true }

// MinTTL is 60 seconds on every plan but Enterprise's; 1 is "automatic",
// which a proxied record is given.
func (c *cloudflare) MinTTL() int { return 60 }

type cloudflareEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

func cloudflareRefusal(status int, raw []byte) string {
	var answer cloudflareEnvelope
	if json.Unmarshal(raw, &answer) != nil || len(answer.Errors) == 0 {
		return plainRefusal(status, raw)
	}
	parts := make([]string, 0, len(answer.Errors))
	for _, item := range answer.Errors {
		if item.Code != 0 {
			parts = append(parts, fmt.Sprintf("%s (code %d)", item.Message, item.Code))
		} else {
			parts = append(parts, item.Message)
		}
	}
	return strings.Join(parts, "; ")
}

// envelope reads one answer: a success flag Cloudflare can set false with
// a 200, then the result.
func (c *cloudflare) envelope(ctx context.Context, operation, method, path string, query url.Values, body, out any) (cloudflareEnvelope, error) {
	var answer cloudflareEnvelope
	if err := c.api.call(ctx, operation, method, path, query, body, &answer); err != nil {
		return answer, err
	}
	if !answer.Success {
		raw, _ := json.Marshal(answer)
		return answer, &Error{Provider: "Cloudflare", Operation: operation, Status: http.StatusOK, Message: cloudflareRefusal(http.StatusOK, raw)}
	}
	if out != nil && len(answer.Result) > 0 {
		if err := json.Unmarshal(answer.Result, out); err != nil {
			return answer, fmt.Errorf("Cloudflare's answer to %s could not be read: %w", operation, err)
		}
	}
	return answer, nil
}

// Zones lists every zone the token reaches, across every account it is
// scoped to.
func (c *cloudflare) Zones(ctx context.Context, _ string) ([]Zone, error) {
	out := []Zone{}
	for page := 1; page <= 50; page++ {
		var zones []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Account struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"account"`
		}
		answer, err := c.envelope(ctx, "list the zones", http.MethodGet, "/zones",
			url.Values{"per_page": {"50"}, "page": {strconv.Itoa(page)}}, nil, &zones)
		if err != nil {
			return nil, err
		}
		for _, zone := range zones {
			account := zone.Account.Name
			if account == "" {
				account = zone.Account.ID
			}
			out = append(out, Zone{ID: zone.ID, Name: Canonical(zone.Name), Account: account})
		}
		if answer.ResultInfo.TotalPages <= page || len(zones) == 0 {
			break
		}
	}
	return out, nil
}

type cloudflareRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied *bool  `json:"proxied,omitempty"`
	Comment string `json:"comment,omitempty"`
}

func (c *cloudflare) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	var records []cloudflareRecord
	_, err := c.envelope(ctx, "read the records of "+Canonical(name), http.MethodGet,
		"/zones/"+url.PathEscape(zone.ID)+"/dns_records", url.Values{"name": {Canonical(name)}, "per_page": {"100"}}, nil, &records)
	if err != nil {
		return nil, err
	}
	sets := map[Type]*Set{}
	order := []Type{}
	for _, record := range records {
		kind := Type(strings.ToUpper(record.Type))
		if Canonical(record.Name) != Canonical(name) || (kind != TypeA && kind != TypeAAAA && kind != TypeCNAME) {
			continue
		}
		set, ok := sets[kind]
		if !ok {
			set = &Set{Name: Canonical(name), Type: kind, TTL: record.TTL, Comment: record.Comment}
			sets[kind] = set
			order = append(order, kind)
		}
		set.Values = append(set.Values, record.Content)
		set.ids = append(set.ids, record.ID)
		set.Proxied = set.Proxied || (record.Proxied != nil && *record.Proxied)
	}
	out := make([]Set, 0, len(order))
	for _, kind := range order {
		out = append(out, *sets[kind])
	}
	return out, nil
}

// Write patches the records a set already has, value by value, and creates
// one for each value beyond them. A proxied record is given TTL 1, which
// Cloudflare reads as automatic; an update leaves an address record's
// proxied flag as the operator set it.
func (c *cloudflare) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	records := "/zones/" + url.PathEscape(zone.ID) + "/dns_records"
	name := Canonical(want.Name)
	ttl := want.TTL
	if want.Proxied {
		ttl = 1
	}
	existing := 0
	if current != nil {
		existing = len(current.ids)
	}
	if existing > len(want.Values) {
		return errors.New("writing fewer values than the set has would delete a record, which nothing here does")
	}
	for index, value := range want.Values {
		if index < existing {
			patch := map[string]any{"content": value}
			if want.Type == TypeCNAME {
				patch["proxied"] = want.Proxied
			}
			if !current.Proxied || want.Type == TypeCNAME {
				patch["ttl"] = ttl
			}
			if want.Note != "" {
				patch["comment"] = want.Note
			}
			if _, err := c.envelope(ctx, "change "+string(want.Type)+" "+name, http.MethodPatch,
				records+"/"+url.PathEscape(current.ids[index]), nil, patch, nil); err != nil {
				return err
			}
			continue
		}
		proxied := want.Proxied
		record := cloudflareRecord{Type: string(want.Type), Name: name, Content: value, TTL: ttl, Proxied: &proxied, Comment: want.Note}
		if _, err := c.envelope(ctx, "create "+string(want.Type)+" "+name, http.MethodPost, records, nil, record, nil); err != nil {
			return err
		}
	}
	return nil
}
