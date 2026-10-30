package dnsrecords

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OVHcloud's API: every call signed with the application secret and the
// consumer key over the method, the full URL, the body and a timestamp in
// the API's own clock, one record per value with a subdomain relative to
// the zone, and a refresh of the zone after a change, which is what makes
// OVH's servers serve it. The keys are the four the DNS-01 plugin reads,
// with its endpoint name (ovh-eu, ovh-ca, ovh-us, kimsufi-*, soyoustart-*).
//
// https://help.ovhcloud.com/csm/en-gb-api-getting-started-ovhcloud-api

var ovhEndpoints = map[string]string{
	"ovh-eu":        "https://eu.api.ovh.com/1.0",
	"ovh-ca":        "https://ca.api.ovh.com/1.0",
	"ovh-us":        "https://api.us.ovhcloud.com/1.0",
	"kimsufi-eu":    "https://eu.api.kimsufi.com/1.0",
	"kimsufi-ca":    "https://ca.api.kimsufi.com/1.0",
	"soyoustart-eu": "https://eu.api.soyoustart.com/1.0",
	"soyoustart-ca": "https://ca.api.soyoustart.com/1.0",
}

type ovh struct {
	api                               rest
	applicationKey, applicationSecret string
	consumerKey                       string
	now                               func() time.Time
	clockOnce                         sync.Once
	clockErr                          error
	delta                             time.Duration
}

func newOVH(credentials string, options Options) (Provider, error) {
	values := iniValues(credentials)
	endpoint := values["dns_ovh_endpoint"]
	base, known := ovhEndpoints[endpoint]
	if !known {
		if !strings.HasPrefix(endpoint, "https://") {
			return nil, fmt.Errorf("%w: %q is not an OVH endpoint (ovh-eu, ovh-ca, ovh-us, kimsufi-eu, kimsufi-ca, soyoustart-eu, soyoustart-ca)", ErrUnsupported, endpoint)
		}
		base = strings.TrimSuffix(endpoint, "/")
	}
	provider := &ovh{
		applicationKey: values["dns_ovh_application_key"], applicationSecret: values["dns_ovh_application_secret"],
		consumerKey: values["dns_ovh_consumer_key"], now: options.now,
	}
	if provider.applicationKey == "" || provider.applicationSecret == "" || provider.consumerKey == "" {
		return nil, fmt.Errorf("%w: the saved OVH credentials need the application key, application secret and consumer key", ErrUnsupported)
	}
	provider.api = rest{
		name: "OVH", base: options.base(base), client: options.client(), authorize: provider.sign,
		refusal: func(status int, raw []byte) string {
			var answer struct {
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
				Class     string `json:"class"`
			}
			if json.Unmarshal(raw, &answer) != nil || answer.Message == "" {
				return plainRefusal(status, raw)
			}
			if answer.ErrorCode != "" {
				return answer.Message + " (" + answer.ErrorCode + ")"
			}
			if answer.Class != "" {
				return answer.Message + " (" + answer.Class + ")"
			}
			return answer.Message
		},
	}
	return provider, nil
}

func (o *ovh) Key() string     { return "ovh" }
func (o *ovh) Name() string    { return "OVH" }
func (o *ovh) PerRecord() bool { return true }
func (o *ovh) MinTTL() int     { return 60 }

// clock reads the API's time once: a signature carries the API's own
// timestamp, and a server whose clock drifts would be refused otherwise.
func (o *ovh) clock(ctx context.Context) (time.Duration, error) {
	o.clockOnce.Do(func() {
		unsigned := o.api
		unsigned.authorize = nil
		var seconds int64
		if err := unsigned.call(ctx, "read its clock", http.MethodGet, "/auth/time", nil, nil, &seconds); err != nil {
			o.clockErr = err
			return
		}
		o.delta = time.Unix(seconds, 0).Sub(o.now())
	})
	return o.delta, o.clockErr
}

// sign is OVH's signature: "$1$" and the SHA-1 of the application secret,
// consumer key, method, full URL, body and timestamp joined by "+".
func (o *ovh) sign(request *http.Request, body []byte) error {
	delta, err := o.clock(request.Context())
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(o.now().Add(delta).Unix(), 10)
	sum := sha1.Sum([]byte(strings.Join([]string{
		o.applicationSecret, o.consumerKey, request.Method, request.URL.String(), string(body), timestamp,
	}, "+")))
	request.Header.Set("X-Ovh-Application", o.applicationKey)
	request.Header.Set("X-Ovh-Consumer", o.consumerKey)
	request.Header.Set("X-Ovh-Timestamp", timestamp)
	request.Header.Set("X-Ovh-Signature", "$1$"+hex.EncodeToString(sum[:]))
	return nil
}

func (o *ovh) Zones(ctx context.Context, _ string) ([]Zone, error) {
	var names []string
	if err := o.api.call(ctx, "list the zones", http.MethodGet, "/domain/zone", nil, nil, &names); err != nil {
		return nil, err
	}
	out := make([]Zone, 0, len(names))
	for _, name := range names {
		out = append(out, Zone{ID: Canonical(name), Name: Canonical(name)})
	}
	return out, nil
}

type ovhRecord struct {
	ID        int64  `json:"id,omitempty"`
	FieldType string `json:"fieldType,omitempty"`
	SubDomain string `json:"subDomain"`
	Target    string `json:"target"`
	TTL       int    `json:"ttl"`
}

func (o *ovh) subDomain(name string, zone Zone) string {
	relative := Relative(name, zone.Name)
	if relative == "@" {
		return ""
	}
	return relative
}

func (o *ovh) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	records := "/domain/zone/" + url.PathEscape(zone.ID) + "/record"
	out := []Set{}
	for _, kind := range Types {
		var ids []int64
		if err := o.api.call(ctx, "list the "+string(kind)+" records of "+Canonical(name), http.MethodGet, records,
			url.Values{"fieldType": {string(kind)}, "subDomain": {o.subDomain(name, zone)}}, nil, &ids); err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		set := Set{Name: Canonical(name), Type: kind}
		for _, id := range ids {
			var record ovhRecord
			if err := o.api.call(ctx, "read a record of "+Canonical(name), http.MethodGet, records+"/"+strconv.FormatInt(id, 10), nil, nil, &record); err != nil {
				return nil, err
			}
			value := record.Target
			if kind == TypeCNAME {
				value = zoneFileTarget(value, zone.Name)
			}
			set.Values = append(set.Values, value)
			set.ids = append(set.ids, strconv.FormatInt(id, 10))
			set.TTL = record.TTL
		}
		out = append(out, set)
	}
	return out, nil
}

func (o *ovh) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	records := "/domain/zone/" + url.PathEscape(zone.ID) + "/record"
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
		if index < existing {
			if err := o.api.call(ctx, "change "+string(want.Type)+" "+Canonical(want.Name), http.MethodPut, records+"/"+url.PathEscape(current.ids[index]), nil,
				ovhRecord{SubDomain: o.subDomain(want.Name, zone), Target: value, TTL: want.TTL}, nil); err != nil {
				return err
			}
			continue
		}
		if err := o.api.call(ctx, "create "+string(want.Type)+" "+Canonical(want.Name), http.MethodPost, records, nil,
			ovhRecord{FieldType: string(want.Type), SubDomain: o.subDomain(want.Name, zone), Target: value, TTL: want.TTL}, nil); err != nil {
			return err
		}
	}
	return o.api.call(ctx, "refresh the zone "+zone.Name, http.MethodPost, "/domain/zone/"+url.PathEscape(zone.ID)+"/refresh", nil, nil, nil)
}
