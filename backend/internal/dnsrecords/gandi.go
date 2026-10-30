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

// Gandi's LiveDNS v5 API: a domain's records as sets (a name, a type, every
// value), the name relative ("@" for the apex), a CNAME's target absolute
// with its dot, and a five-minute floor on the TTL. The credential is the
// personal access token saved for the DNS-01 plugin (Bearer), or the
// LiveDNS API key it replaced (Apikey), with the sharing id of an
// organization when one is saved.
//
// https://api.gandi.net/docs/livedns/

const gandiAPI = "https://api.gandi.net/v5/livedns"

type gandi struct {
	api     rest
	sharing string
}

func newGandi(credentials string, options Options) (Provider, error) {
	values := iniValues(credentials)
	token, key := values["dns_gandi_token"], values["dns_gandi_api_key"]
	if token == "" && key == "" {
		return nil, fmt.Errorf("%w: no Gandi token or API key is saved", ErrUnsupported)
	}
	return &gandi{sharing: values["dns_gandi_sharing_id"], api: rest{
		name: "Gandi", base: options.base(gandiAPI), client: options.client(),
		authorize: func(request *http.Request, _ []byte) error {
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			} else {
				request.Header.Set("Authorization", "Apikey "+key)
			}
			return nil
		},
		refusal: func(status int, raw []byte) string {
			var answer struct {
				Message string `json:"message"`
				Cause   string `json:"cause"`
				Errors  []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"errors"`
			}
			if json.Unmarshal(raw, &answer) != nil || (answer.Message == "" && len(answer.Errors) == 0) {
				return plainRefusal(status, raw)
			}
			parts := []string{}
			if answer.Message != "" {
				parts = append(parts, answer.Message)
			}
			for _, item := range answer.Errors {
				parts = append(parts, item.Name+": "+item.Description)
			}
			return strings.Join(parts, "; ")
		},
	}}, nil
}

func (g *gandi) Key() string     { return "gandi" }
func (g *gandi) Name() string    { return "Gandi" }
func (g *gandi) PerRecord() bool { return false }
func (g *gandi) MinTTL() int     { return 300 }

func (g *gandi) query(extra url.Values) url.Values {
	if g.sharing != "" {
		if extra == nil {
			extra = url.Values{}
		}
		extra.Set("sharing_id", g.sharing)
	}
	return extra
}

func (g *gandi) Zones(ctx context.Context, _ string) ([]Zone, error) {
	out := []Zone{}
	for page := 1; page <= 50; page++ {
		var domains []struct {
			FQDN string `json:"fqdn"`
		}
		if err := g.api.call(ctx, "list the domains", http.MethodGet, "/domains",
			g.query(url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}), nil, &domains); err != nil {
			return nil, err
		}
		for _, domain := range domains {
			out = append(out, Zone{ID: Canonical(domain.FQDN), Name: Canonical(domain.FQDN)})
		}
		if len(domains) < 100 {
			break
		}
	}
	return out, nil
}

type gandiSet struct {
	Name   string   `json:"rrset_name,omitempty"`
	Type   string   `json:"rrset_type,omitempty"`
	TTL    int      `json:"rrset_ttl"`
	Values []string `json:"rrset_values"`
}

func (g *gandi) records(zone Zone, name string) string {
	return "/domains/" + url.PathEscape(zone.ID) + "/records/" + url.PathEscape(Relative(name, zone.Name))
}

// Read asks for every set at the name; a name with none is a 404.
func (g *gandi) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	var sets []gandiSet
	status, err := g.api.send(ctx, "read the records of "+Canonical(name), http.MethodGet, g.records(zone, name), g.query(nil), nil, &sets)
	var refused *Error
	if status == http.StatusNotFound && errors.As(err, &refused) {
		return []Set{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Set{}
	for _, set := range sets {
		kind := Type(strings.ToUpper(set.Type))
		if kind != TypeA && kind != TypeAAAA && kind != TypeCNAME {
			continue
		}
		read := Set{Name: Canonical(name), Type: kind, TTL: set.TTL, Values: append([]string(nil), set.Values...)}
		if kind == TypeCNAME {
			for index, value := range read.Values {
				read.Values[index] = zoneFileTarget(value, zone.Name)
			}
		}
		out = append(out, read)
	}
	return out, nil
}

// Write creates a set with POST, which Gandi refuses if the set exists, and
// replaces one with PUT.
func (g *gandi) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	set := gandiSet{TTL: want.TTL}
	for _, value := range want.Values {
		if want.Type == TypeCNAME {
			value = fqdn(value)
		}
		set.Values = append(set.Values, value)
	}
	path := g.records(zone, want.Name) + "/" + string(want.Type)
	if current == nil {
		return g.api.call(ctx, "create "+string(want.Type)+" "+Canonical(want.Name), http.MethodPost, path, g.query(nil), set, nil)
	}
	return g.api.call(ctx, "change "+string(want.Type)+" "+Canonical(want.Name), http.MethodPut, path, g.query(nil), set, nil)
}
