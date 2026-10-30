package dnsrecords

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The copy of the signer against AWS's documented example, as
// secretstore's original is checked.
func TestSigV4MatchesAWSsDocumentedExample(t *testing.T) {
	credentials := awsCredentials{accessKeyID: "AKIDEXAMPLE", secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"}
	request, _ := http.NewRequest(http.MethodGet, "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signed := signAWSV4(request, nil, credentials, "us-east-1", "iam", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	if signed.stringToSign != "AWS4-HMAC-SHA256\n20150830T123600Z\n20150830/us-east-1/iam/aws4_request\nf536975d06c0309214f805bb90ccff089219ecd68b2577efef23edd43b7e1a59" {
		t.Fatalf("string to sign:\n%s", signed.stringToSign)
	}
	if hex.EncodeToString(signed.signingKey) != "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9" {
		t.Fatalf("signing key = %x", signed.signingKey)
	}
	if signed.signature != "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7" {
		t.Fatalf("signature = %s", signed.signature)
	}
}

// route53Fake is Route 53's 2013-04-01 REST API, checking every request's
// signature with a verifier of its own, one hosted zone per page.
type route53Fake struct {
	fakeStore
	accessKey, secretKey string
}

func route53Fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<?xml version="1.0"?>
<ErrorResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Error><Type>Sender</Type><Code>`+code+`</Code><Message>`+message+`</Message></Error><RequestId>f00</RequestId></ErrorResponse>`)
}

func route53Escape(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		switch c := text[index]; {
		case c == '*':
			out.WriteString(`\052`)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// verify is SigV4 as the service checks it, written apart from the client.
func (f *route53Fake) verify(r *http.Request, body []byte) bool {
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 "), ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		fields[name] = value
	}
	scope := strings.Split(fields["Credential"], "/")
	if len(scope) != 5 || scope[0] != f.accessKey || scope[2] != "us-east-1" || scope[3] != "route53" {
		return false
	}
	query := []string{}
	for name, values := range r.URL.Query() {
		for _, value := range values {
			query = append(query, strings.ReplaceAll(url.QueryEscape(name), "+", "%20")+"="+strings.ReplaceAll(url.QueryEscape(value), "+", "%20"))
		}
	}
	sort.Strings(query)
	var headers strings.Builder
	for _, name := range strings.Split(fields["SignedHeaders"], ";") {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		headers.WriteString(name + ":" + strings.TrimSpace(value) + "\n")
	}
	payload := sha256.Sum256(body)
	canonical := strings.Join([]string{r.Method, r.URL.EscapedPath(), strings.Join(query, "&"), headers.String(), fields["SignedHeaders"], hex.EncodeToString(payload[:])}, "\n")
	hashed := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + r.Header.Get("X-Amz-Date") + "\n" + strings.Join(scope[1:], "/") + "\n" + hex.EncodeToString(hashed[:])
	key := []byte("AWS4" + f.secretKey)
	for _, part := range scope[1:] {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	return hex.EncodeToString(mac.Sum(nil)) == fields["Signature"]
}

func (f *route53Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)
	body, _ := io.ReadAll(r.Body)
	if !f.verify(r, body) {
		route53Fail(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided.")
		return
	}
	parts := segments(r.URL.Path, "/2013-04-01")
	switch {
	case r.Method == http.MethodGet && len(parts) == 1 && parts[0] == "hostedzone":
		start := 0
		if marker := r.URL.Query().Get("marker"); marker != "" {
			for index, zone := range f.zones {
				if zone.ID == marker {
					start = index
				}
			}
		}
		var out strings.Builder
		out.WriteString(`<?xml version="1.0"?><ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones>`)
		if start < len(f.zones) {
			zone := f.zones[start]
			out.WriteString(`<HostedZone><Id>/hostedzone/` + zone.ID + `</Id><Name>` + zone.Name + `.</Name><CallerReference>r</CallerReference><Config><PrivateZone>` +
				map[bool]string{true: "true", false: "false"}[zone.Private] + `</PrivateZone></Config></HostedZone>`)
		}
		out.WriteString(`</HostedZones>`)
		if start+1 < len(f.zones) {
			out.WriteString(`<IsTruncated>true</IsTruncated><NextMarker>` + f.zones[start+1].ID + `</NextMarker>`)
		} else {
			out.WriteString(`<IsTruncated>false</IsTruncated>`)
		}
		out.WriteString(`<MaxItems>1</MaxItems></ListHostedZonesResponse>`)
		_, _ = io.WriteString(w, out.String())
	case len(parts) == 3 && parts[0] == "hostedzone" && parts[2] == "rrset":
		zone, ok := f.zone(parts[1])
		if !ok {
			route53Fail(w, http.StatusNotFound, "NoSuchHostedZone", "No hosted zone found with ID: "+parts[1])
			return
		}
		if r.Method == http.MethodGet {
			from := Canonical(route53Name(r.URL.Query().Get("name")))
			var out strings.Builder
			out.WriteString(`<?xml version="1.0"?><ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ResourceRecordSets>`)
			for _, set := range f.sets(zone.ID) {
				if set.Name < from {
					continue
				}
				out.WriteString(`<ResourceRecordSet><Name>` + route53Escape(set.Name) + `.</Name><Type>` + set.Type + `</Type><TTL>` + strconv.Itoa(set.TTL) + `</TTL><ResourceRecords>`)
				for _, value := range set.Values {
					out.WriteString(`<ResourceRecord><Value>` + value + `</Value></ResourceRecord>`)
				}
				out.WriteString(`</ResourceRecords></ResourceRecordSet>`)
			}
			out.WriteString(`</ResourceRecordSets><IsTruncated>false</IsTruncated><MaxItems>20</MaxItems></ListResourceRecordSetsResponse>`)
			_, _ = io.WriteString(w, out.String())
			return
		}
		var batch struct {
			Changes []struct {
				Action string     `xml:"Action"`
				Set    route53Set `xml:"ResourceRecordSet"`
			} `xml:"ChangeBatch>Changes>Change"`
		}
		if xml.Unmarshal(body, &batch) != nil || len(batch.Changes) == 0 {
			route53Fail(w, http.StatusBadRequest, "MalformedInput", "Could not parse the change batch")
			return
		}
		for _, change := range batch.Changes {
			name := route53Name(change.Set.Name)
			exists := len(f.find(zone.ID, name, change.Set.Type)) > 0
			switch {
			case !InZone(name, zone.Name):
				route53Fail(w, http.StatusBadRequest, "InvalidChangeBatch", "RRSet with DNS name "+name+" is not permitted in zone "+zone.Name)
				return
			case change.Action == "CREATE" && exists:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `<?xml version="1.0"?><InvalidChangeBatch xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Messages><Message>Tried to create resource record set [name='`+
					name+`.', type='`+change.Set.Type+`'] but it already exists</Message></Messages><RequestId>f00</RequestId></InvalidChangeBatch>`)
				return
			case change.Set.TTL == 0 || len(change.Set.Values) == 0:
				route53Fail(w, http.StatusBadRequest, "InvalidInput", "Invalid request: Missing field 'TTL' or 'ResourceRecords'")
				return
			case f.conflicts(zone.ID, name, change.Set.Type):
				route53Fail(w, http.StatusBadRequest, "InvalidChangeBatch", "RRSet of type CNAME with DNS name "+name+". is not permitted as it conflicts with other records")
				return
			}
			values := append([]string(nil), change.Set.Values...)
			for index := range values {
				values[index] = strings.TrimSuffix(values[index], ".")
			}
			f.replaceSet(zone.ID, name, change.Set.Type, values, change.Set.TTL)
		}
		_, _ = io.WriteString(w, `<?xml version="1.0"?><ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ChangeInfo><Id>/change/C1</Id><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`)
	default:
		route53Fail(w, http.StatusNotFound, "UnknownOperation", r.URL.Path)
	}
}

// An account with a public and a private zone of the same name, and a
// delegated sub-zone, listed a page at a time: the private one is never
// written.
func TestRoute53WritesSignedChangeBatchesInThePublicZone(t *testing.T) {
	fake := &route53Fake{accessKey: "AKIAFAKE", secretKey: "fake-secret", fakeStore: fakeStore{zones: []fakeZone{
		{ID: "ZPRIVATE", Name: "preview.example.com", Private: true},
		{ID: "ZROOT", Name: "example.com"},
		{ID: "ZPREVIEW", Name: "preview.example.com"},
	}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	provider := mustProvider(t, "route53", "[default]\naws_access_key_id = AKIAFAKE\naws_secret_access_key = fake-secret\n", Options{BaseURL: server.URL})
	zones, err := provider.Zones(context.Background(), "x")
	if err != nil || len(zones) != 2 || zones[1].ID != "ZPREVIEW" {
		t.Fatalf("zones = %+v %v", zones, err)
	}
	exercise(t, provider, "app.preview.example.com", "preview.example.com", "tunnel.preview.example.com", false)
	if len(fake.find("ZPRIVATE", "", "")) != 0 {
		t.Fatal("the private zone was written")
	}
	// A wildcard name is read back from Route 53's octal escape.
	wildcard := Set{Name: "*.preview.example.com", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 300}
	zone := Zone{ID: "ZPREVIEW", Name: "preview.example.com"}
	if _, err := Apply(context.Background(), provider, zone, Diff([]Set{wildcard}, nil, false)); err != nil {
		t.Fatal(err)
	}
	held, err := provider.Read(context.Background(), zone, "*.preview.example.com")
	if err != nil || actions(Diff([]Set{wildcard}, held, false)) != "A:keep" {
		t.Fatalf("the wildcard read back = %+v %v", held, err)
	}
}

func TestRoute53RefusalsAreItsOwnWords(t *testing.T) {
	fake := &route53Fake{accessKey: "AKIAFAKE", secretKey: "fake-secret", fakeStore: fakeStore{zones: []fakeZone{{ID: "ZROOT", Name: "example.com"}}}}
	server := httptest.NewServer(fake)
	defer server.Close()
	wrong := mustProvider(t, "route53", "[default]\naws_access_key_id = AKIAFAKE\naws_secret_access_key = not-it\n", Options{BaseURL: server.URL})
	_, err := wrong.Zones(context.Background(), "x")
	refusedInWords(t, err, "AWS Route 53 refused to list the hosted zones", "SignatureDoesNotMatch")

	right := mustProvider(t, "route53", "[default]\naws_access_key_id = AKIAFAKE\naws_secret_access_key = fake-secret\n", Options{BaseURL: server.URL})
	zone := Zone{ID: "ZROOT", Name: "example.com"}
	set := Set{Name: "app.example.com", Type: TypeA, Values: []string{"203.0.113.7"}, TTL: 300}
	if err := right.Write(context.Background(), zone, nil, set); err != nil {
		t.Fatal(err)
	}
	// A create of a set that appeared since it was read is refused, not
	// overwritten.
	refusedInWords(t, right.Write(context.Background(), zone, nil, set), "InvalidChangeBatch", "but it already exists")
}
