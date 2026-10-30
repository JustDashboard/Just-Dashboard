package dnsrecords

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// DigitalOcean's v2 API: domains and their records, one record per value,
// names inside the domain relative ("@" for the apex) and a CNAME's target
// absolute with its dot. The token is the personal access token saved for
// the DNS-01 plugin, which needs write scope on domains.
//
// https://docs.digitalocean.com/reference/api/digitalocean/#tag/Domain-Records

const digitalOceanAPI = "https://api.digitalocean.com"

type digitalOcean struct{ api rest }

func newDigitalOcean(credentials string, options Options) (Provider, error) {
	token := iniValues(credentials)["dns_digitalocean_token"]
	if token == "" {
		return nil, fmt.Errorf("%w: no DigitalOcean token is saved", ErrUnsupported)
	}
	return &digitalOcean{api: rest{
		name: "DigitalOcean", base: options.base(digitalOceanAPI), client: options.client(),
		authorize: func(request *http.Request, _ []byte) error {
			request.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
		refusal: func(status int, raw []byte) string {
			var answer struct {
				ID      string `json:"id"`
				Message string `json:"message"`
			}
			if json.Unmarshal(raw, &answer) != nil || answer.Message == "" {
				return plainRefusal(status, raw)
			}
			return answer.Message + " (" + answer.ID + ")"
		},
	}}, nil
}

func (d *digitalOcean) Key() string     { return "digitalocean" }
func (d *digitalOcean) Name() string    { return "DigitalOcean" }
func (d *digitalOcean) PerRecord() bool { return true }
func (d *digitalOcean) MinTTL() int     { return 30 }

type digitalOceanLinks struct {
	Pages struct {
		Next string `json:"next"`
	} `json:"pages"`
}

func (d *digitalOcean) Zones(ctx context.Context, _ string) ([]Zone, error) {
	out := []Zone{}
	for page := 1; page <= 50; page++ {
		var answer struct {
			Domains []struct {
				Name string `json:"name"`
			} `json:"domains"`
			Links digitalOceanLinks `json:"links"`
		}
		if err := d.api.call(ctx, "list the domains", http.MethodGet, "/v2/domains",
			url.Values{"per_page": {"200"}, "page": {strconv.Itoa(page)}}, nil, &answer); err != nil {
			return nil, err
		}
		for _, domain := range answer.Domains {
			out = append(out, Zone{ID: Canonical(domain.Name), Name: Canonical(domain.Name)})
		}
		if answer.Links.Pages.Next == "" {
			break
		}
	}
	return out, nil
}

type digitalOceanRecord struct {
	ID   int64  `json:"id,omitempty"`
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
	TTL  int    `json:"ttl"`
}

func (d *digitalOcean) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	sets := map[Type]*Set{}
	order := []Type{}
	for page := 1; page <= 20; page++ {
		var answer struct {
			Records []digitalOceanRecord `json:"domain_records"`
			Links   digitalOceanLinks    `json:"links"`
		}
		// The name filter takes the full name.
		if err := d.api.call(ctx, "read the records of "+Canonical(name), http.MethodGet,
			"/v2/domains/"+url.PathEscape(zone.ID)+"/records",
			url.Values{"name": {Canonical(name)}, "per_page": {"200"}, "page": {strconv.Itoa(page)}}, nil, &answer); err != nil {
			return nil, err
		}
		for _, record := range answer.Records {
			kind := Type(strings.ToUpper(record.Type))
			if absolute(record.Name, zone.Name) != Canonical(name) || (kind != TypeA && kind != TypeAAAA && kind != TypeCNAME) {
				continue
			}
			set, ok := sets[kind]
			if !ok {
				set = &Set{Name: Canonical(name), Type: kind, TTL: record.TTL}
				sets[kind] = set
				order = append(order, kind)
			}
			value := record.Data
			if kind == TypeCNAME {
				value = zoneFileTarget(record.Data, zone.Name)
			}
			set.Values = append(set.Values, value)
			set.ids = append(set.ids, strconv.FormatInt(record.ID, 10))
		}
		if answer.Links.Pages.Next == "" {
			break
		}
	}
	out := make([]Set, 0, len(order))
	for _, kind := range order {
		out = append(out, *sets[kind])
	}
	return out, nil
}

func (d *digitalOcean) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	records := "/v2/domains/" + url.PathEscape(zone.ID) + "/records"
	existing := 0
	if current != nil {
		existing = len(current.ids)
	}
	if existing > len(want.Values) {
		return fmt.Errorf("writing fewer values than the set has would delete a record, which nothing here does")
	}
	for index, value := range want.Values {
		if want.Type == TypeCNAME {
			value = fqdn(value)
		}
		record := digitalOceanRecord{Type: string(want.Type), Name: Relative(want.Name, zone.Name), Data: value, TTL: want.TTL}
		if index < existing {
			if err := d.api.call(ctx, "change "+string(want.Type)+" "+Canonical(want.Name), http.MethodPatch,
				records+"/"+url.PathEscape(current.ids[index]), nil, record, nil); err != nil {
				return err
			}
			continue
		}
		if err := d.api.call(ctx, "create "+string(want.Type)+" "+Canonical(want.Name), http.MethodPost, records, nil, record, nil); err != nil {
			return err
		}
	}
	return nil
}

// zoneFileTarget reads a target as a zone file writes it: "@" is the apex,
// a name with its root dot is absolute, and one without is inside the zone.
func zoneFileTarget(data, zone string) string {
	switch {
	case data == "@":
		return Canonical(zone)
	case strings.HasSuffix(data, "."):
		return Canonical(data)
	}
	return absolute(data, zone)
}
