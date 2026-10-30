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

// Linode's (Akamai's) v4 API: domains with numeric ids, one record per
// value, names relative to the domain ("" for the apex), and a TTL Linode
// rounds to one of its own steps. Only a master domain is Linode's to
// answer for; a slave domain copies another server's zone. The key is the
// personal access token saved for the DNS-01 plugin; the v3 API that plugin
// once also spoke is retired.
//
// https://techdocs.akamai.com/linode-api/reference/post-domain-record

const linodeAPI = "https://api.linode.com"

type linode struct{ api rest }

func newLinode(credentials string, options Options) (Provider, error) {
	values := iniValues(credentials)
	key := values["dns_linode_key"]
	if key == "" {
		return nil, fmt.Errorf("%w: no Linode API key is saved", ErrUnsupported)
	}
	if version := values["dns_linode_version"]; version != "" && version != "4" {
		return nil, fmt.Errorf("%w: the saved Linode key is for API version %s, which Linode has retired; save a v4 personal access token", ErrUnsupported, version)
	}
	return &linode{api: rest{
		name: "Linode", base: options.base(linodeAPI), client: options.client(),
		authorize: func(request *http.Request, _ []byte) error {
			request.Header.Set("Authorization", "Bearer "+key)
			return nil
		},
		refusal: func(status int, raw []byte) string {
			var answer struct {
				Errors []struct {
					Field  string `json:"field"`
					Reason string `json:"reason"`
				} `json:"errors"`
			}
			if json.Unmarshal(raw, &answer) != nil || len(answer.Errors) == 0 {
				return plainRefusal(status, raw)
			}
			parts := []string{}
			for _, item := range answer.Errors {
				if item.Field != "" {
					parts = append(parts, item.Field+": "+item.Reason)
				} else {
					parts = append(parts, item.Reason)
				}
			}
			return strings.Join(parts, "; ")
		},
	}}, nil
}

func (l *linode) Key() string     { return "linode" }
func (l *linode) Name() string    { return "Linode" }
func (l *linode) PerRecord() bool { return true }
func (l *linode) MinTTL() int     { return 30 }

// linodeSteps are the TTLs Linode keeps; any other is rounded to one.
var linodeSteps = []int{30, 120, 300, 3600, 7200, 14400, 28800, 57600, 86400, 172800, 345600, 604800, 1209600, 2419200}

// linodeTTL rounds up to Linode's next step, so what is shown is what it
// keeps.
func linodeTTL(ttl int) int {
	for _, step := range linodeSteps {
		if ttl <= step {
			return step
		}
	}
	return linodeSteps[len(linodeSteps)-1]
}

type linodePage struct {
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

func (l *linode) Zones(ctx context.Context, _ string) ([]Zone, error) {
	out := []Zone{}
	for page := 1; page <= 50; page++ {
		var answer struct {
			linodePage
			Data []struct {
				ID     int64  `json:"id"`
				Domain string `json:"domain"`
				Type   string `json:"type"`
			} `json:"data"`
		}
		if err := l.api.call(ctx, "list the domains", http.MethodGet, "/v4/domains",
			url.Values{"page": {strconv.Itoa(page)}, "page_size": {"100"}}, nil, &answer); err != nil {
			return nil, err
		}
		for _, domain := range answer.Data {
			if domain.Type != "" && domain.Type != "master" {
				continue
			}
			out = append(out, Zone{ID: strconv.FormatInt(domain.ID, 10), Name: Canonical(domain.Domain)})
		}
		if answer.Pages <= page {
			break
		}
	}
	return out, nil
}

type linodeRecord struct {
	ID     int64  `json:"id,omitempty"`
	Type   string `json:"type,omitempty"`
	Name   string `json:"name"`
	Target string `json:"target"`
	TTL    int    `json:"ttl_sec"`
}

func (l *linode) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	sets := map[Type]*Set{}
	order := []Type{}
	for page := 1; page <= 50; page++ {
		var answer struct {
			linodePage
			Data []linodeRecord `json:"data"`
		}
		if err := l.api.call(ctx, "read the records of "+Canonical(name), http.MethodGet,
			"/v4/domains/"+url.PathEscape(zone.ID)+"/records",
			url.Values{"page": {strconv.Itoa(page)}, "page_size": {"500"}}, nil, &answer); err != nil {
			return nil, err
		}
		for _, record := range answer.Data {
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
			value := record.Target
			if kind == TypeCNAME {
				value = Canonical(value)
			}
			set.Values = append(set.Values, value)
			set.ids = append(set.ids, strconv.FormatInt(record.ID, 10))
		}
		if answer.Pages <= page {
			break
		}
	}
	out := make([]Set, 0, len(order))
	for _, kind := range order {
		out = append(out, *sets[kind])
	}
	return out, nil
}

func (l *linode) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	records := "/v4/domains/" + url.PathEscape(zone.ID) + "/records"
	existing := 0
	if current != nil {
		existing = len(current.ids)
	}
	if existing > len(want.Values) {
		return fmt.Errorf("writing fewer values than the set has would delete a record, which nothing here does")
	}
	relative := Relative(want.Name, zone.Name)
	if relative == "@" {
		relative = ""
	}
	for index, value := range want.Values {
		if want.Type == TypeCNAME {
			value = Canonical(value)
		}
		if index < existing {
			if err := l.api.call(ctx, "change "+string(want.Type)+" "+Canonical(want.Name), http.MethodPut,
				records+"/"+url.PathEscape(current.ids[index]), nil, linodeRecord{Name: relative, Target: value, TTL: want.TTL}, nil); err != nil {
				return err
			}
			continue
		}
		if err := l.api.call(ctx, "create "+string(want.Type)+" "+Canonical(want.Name), http.MethodPost, records, nil,
			linodeRecord{Type: string(want.Type), Name: relative, Target: value, TTL: want.TTL}, nil); err != nil {
			return err
		}
	}
	return nil
}
