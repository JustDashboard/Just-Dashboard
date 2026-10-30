package dnsrecords

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Route 53's REST API (2013-04-01): XML both ways, every request signed
// with SigV4 for the global endpoint (us-east-1, service route53), and a
// record set — a name, a type, every value — changed whole by one change
// batch. The credentials are the access key pair the Certificates page
// normalizes into an AWS shared-credentials file; a server whose certbot
// uses the machine's role has none, and this code does not read the role.
//
// https://docs.aws.amazon.com/Route53/latest/APIReference/API_ChangeResourceRecordSets.html

const (
	route53API       = "https://route53.amazonaws.com"
	route53Version   = "/2013-04-01"
	route53Namespace = "https://route53.amazonaws.com/doc/2013-04-01/"
)

type route53 struct {
	base        string
	client      *http.Client
	credentials awsCredentials
	now         func() time.Time
}

func newRoute53(credentials string, options Options) (Provider, error) {
	values := iniValues(credentials)
	keys := awsCredentials{
		accessKeyID: values["aws_access_key_id"], secretAccessKey: values["aws_secret_access_key"],
		sessionToken: values["aws_session_token"],
	}
	if keys.accessKeyID == "" || keys.secretAccessKey == "" {
		return nil, fmt.Errorf("%w: Route 53 records are written with a saved access key, and none is saved (certbot may be using the machine's role, which this does not read)", ErrUnsupported)
	}
	return &route53{base: options.base(route53API), client: options.client(), credentials: keys, now: options.now}, nil
}

func (r *route53) Key() string     { return "route53" }
func (r *route53) Name() string    { return "AWS Route 53" }
func (r *route53) PerRecord() bool { return false }
func (r *route53) MinTTL() int     { return 1 }

type route53Error struct {
	Error struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
	Messages []string `xml:"Messages>Message"`
}

func route53Refusal(status int, raw []byte) string {
	var answer route53Error
	if xml.Unmarshal(raw, &answer) != nil {
		return plainRefusal(status, raw)
	}
	if len(answer.Messages) > 0 {
		return "InvalidChangeBatch: " + strings.Join(answer.Messages, "; ")
	}
	if answer.Error.Code == "" && answer.Error.Message == "" {
		return plainRefusal(status, raw)
	}
	return strings.TrimSpace(answer.Error.Code + ": " + answer.Error.Message)
}

func (r *route53) call(ctx context.Context, operation, method, path string, query url.Values, body []byte, out any) error {
	target := r.base + route53Version + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body == nil {
		request.Body, request.ContentLength = http.NoBody, 0
	} else {
		request.Header.Set("Content-Type", "application/xml")
	}
	signAWSV4(request, body, r.credentials, "us-east-1", "route53", r.now())
	response, err := r.client.Do(request)
	if err != nil {
		return fmt.Errorf("AWS Route 53 could not be reached to %s: %w", operation, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		return &Error{Provider: "AWS Route 53", Operation: operation, Status: response.StatusCode, Message: route53Refusal(response.StatusCode, raw)}
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("AWS Route 53's answer to %s could not be read: %w", operation, err)
	}
	return nil
}

// Zones lists the account's public hosted zones; a private zone answers
// only inside a VPC, so a record there is not what the internet sees.
func (r *route53) Zones(ctx context.Context, _ string) ([]Zone, error) {
	out := []Zone{}
	marker := ""
	for page := 0; page < 50; page++ {
		query := url.Values{"maxitems": {"100"}}
		if marker != "" {
			query.Set("marker", marker)
		}
		var answer struct {
			Zones []struct {
				ID      string `xml:"Id"`
				Name    string `xml:"Name"`
				Private bool   `xml:"Config>PrivateZone"`
			} `xml:"HostedZones>HostedZone"`
			Truncated  bool   `xml:"IsTruncated"`
			NextMarker string `xml:"NextMarker"`
		}
		if err := r.call(ctx, "list the hosted zones", http.MethodGet, "/hostedzone", query, nil, &answer); err != nil {
			return nil, err
		}
		for _, zone := range answer.Zones {
			if zone.Private {
				continue
			}
			out = append(out, Zone{ID: strings.TrimPrefix(zone.ID, "/hostedzone/"), Name: route53Name(zone.Name)})
		}
		if !answer.Truncated || answer.NextMarker == "" {
			break
		}
		marker = answer.NextMarker
	}
	return out, nil
}

// route53Name reads a name as Route 53 writes it: octal escapes for
// anything but letters, digits, hyphens and dots, `*` among them.
func route53Name(name string) string {
	var out strings.Builder
	for index := 0; index < len(name); index++ {
		if name[index] == '\\' && index+3 < len(name) {
			if value, err := strconv.ParseUint(name[index+1:index+4], 8, 8); err == nil {
				out.WriteByte(byte(value))
				index += 3
				continue
			}
		}
		out.WriteByte(name[index])
	}
	return Canonical(out.String())
}

type route53Set struct {
	Name          string   `xml:"Name"`
	Type          string   `xml:"Type"`
	SetIdentifier string   `xml:"SetIdentifier,omitempty"`
	TTL           int      `xml:"TTL,omitempty"`
	Values        []string `xml:"ResourceRecords>ResourceRecord>Value"`
	Alias         *struct {
		DNSName string `xml:"DNSName"`
	} `xml:"AliasTarget,omitempty"`
}

// Read lists the zone's sets from the name on, which Route 53 orders by
// name and then type, so the name's own sets come first.
func (r *route53) Read(ctx context.Context, zone Zone, name string) ([]Set, error) {
	var answer struct {
		Sets []route53Set `xml:"ResourceRecordSets>ResourceRecordSet"`
	}
	if err := r.call(ctx, "read the records of "+Canonical(name), http.MethodGet, "/hostedzone/"+url.PathEscape(zone.ID)+"/rrset",
		url.Values{"name": {fqdn(name)}, "maxitems": {"20"}}, nil, &answer); err != nil {
		return nil, err
	}
	out := []Set{}
	for _, set := range answer.Sets {
		kind := Type(set.Type)
		if route53Name(set.Name) != Canonical(name) || (kind != TypeA && kind != TypeAAAA && kind != TypeCNAME) {
			continue
		}
		read := Set{Name: Canonical(name), Type: kind, TTL: set.TTL, Values: append([]string(nil), set.Values...)}
		if kind == TypeCNAME {
			for index := range read.Values {
				read.Values[index] = Canonical(read.Values[index])
			}
		}
		switch {
		case set.Alias != nil:
			read.Values = []string{"alias to " + Canonical(set.Alias.DNSName)}
			read.Unwritable = "it is an alias to " + Canonical(set.Alias.DNSName) + ", which is replaced in the Route 53 console"
		case set.SetIdentifier != "":
			read.Unwritable = "it is one of several sets of a routing policy (" + set.SetIdentifier + "), which is changed in the Route 53 console"
		}
		out = append(out, read)
	}
	return out, nil
}

type route53Change struct {
	XMLName xml.Name `xml:"ChangeResourceRecordSetsRequest"`
	XMLNS   string   `xml:"xmlns,attr"`
	Comment string   `xml:"ChangeBatch>Comment"`
	Changes []struct {
		Action string     `xml:"Action"`
		Set    route53Set `xml:"ResourceRecordSet"`
	} `xml:"ChangeBatch>Changes>Change"`
}

// Write sends one change batch: CREATE for a set the name does not have,
// which Route 53 refuses if one has appeared since it was read, and UPSERT
// to replace one it has.
func (r *route53) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	set := route53Set{Name: fqdn(want.Name), Type: string(want.Type), TTL: want.TTL}
	for _, value := range want.Values {
		if want.Type == TypeCNAME {
			value = fqdn(value)
		}
		set.Values = append(set.Values, value)
	}
	batch := route53Change{XMLNS: route53Namespace, Comment: "Just Dashboard"}
	if want.Note != "" {
		batch.Comment = want.Note
	}
	action := "CREATE"
	if current != nil {
		action = "UPSERT"
	}
	batch.Changes = append(batch.Changes, struct {
		Action string     `xml:"Action"`
		Set    route53Set `xml:"ResourceRecordSet"`
	}{Action: action, Set: set})
	raw, err := xml.Marshal(batch)
	if err != nil {
		return err
	}
	body := append([]byte(xml.Header), raw...)
	verb := "create"
	if current != nil {
		verb = "change"
	}
	return r.call(ctx, verb+" "+string(want.Type)+" "+Canonical(want.Name), http.MethodPost,
		"/hostedzone/"+url.PathEscape(zone.ID)+"/rrset", nil, body, nil)
}
