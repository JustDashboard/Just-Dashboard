// Package dnsrecords writes the one record a deployment's domain needs —
// an A and AAAA to this server, or a CNAME to its Cloudflare Tunnel — into
// the operator's own zone, with the credentials the Certificates page
// already saved for certbot's DNS-01 plugins.
//
// Those credentials exist to write a TXT record for a challenge. Every one
// of the eight providers' APIs writes an address record the same way, so a
// plan whose domain does not point here yet no longer sends the operator to
// their provider's dashboard: the Domains settings say which record is
// missing, and one press creates it.
//
// It is a leaf: given a provider's saved credentials, it finds the zone a
// name sits in (the longest zone the credentials reach), reads the A, AAAA
// and CNAME sets at the name, and creates or updates one set. It never
// deletes a record — a set this code would have to shrink by removing a
// record, or a CNAME standing where an address is wanted, is a refusal in
// words, left for the operator to remove where they keep their zone. No SDK
// is used: each provider is a page of documented REST (or, for RFC 2136, the
// DNS UPDATE message itself), and each has a fake of that API under test.
package dnsrecords

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Type is one of the record types this package writes.
type Type string

const (
	TypeA     Type = "A"
	TypeAAAA  Type = "AAAA"
	TypeCNAME Type = "CNAME"
)

// Types are the ones read at a name: a CNAME excludes the other two, so all
// three are read to know what may be written.
var Types = []Type{TypeA, TypeAAAA, TypeCNAME}

// Set is one record set: a name, a type, and every value the name holds of
// that type.
type Set struct {
	// Name is the full name, lower case, without the root's trailing dot.
	Name   string   `json:"name"`
	Type   Type     `json:"type"`
	Values []string `json:"values"`
	TTL    int      `json:"ttl"`
	// Proxied is Cloudflare's own flag: its edge answers for the name and
	// forwards to the value. A tunnel's CNAME is proxied by definition.
	Proxied bool `json:"proxied,omitempty"`
	// Comment is what the provider keeps beside the record, where it keeps
	// anything (Cloudflare), and Note what a write leaves there.
	Comment string `json:"comment,omitempty"`
	Note    string `json:"-"`
	// Unwritable says why this code cannot replace the set: a Route 53
	// routing policy or alias, for instance. Read only.
	Unwritable string `json:"unwritable,omitempty"`

	// ids are the provider's own record ids, in Values' order, for the
	// providers that keep one record per value.
	ids []string
}

// Zone is a zone the credentials reach.
type Zone struct {
	// ID is the provider's own handle: Cloudflare's and Route 53's ids,
	// Google's zone name, Linode's number; the name where there is none.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Account is whose zone it is, where the credentials reach several.
	Account string `json:"account,omitempty"`
}

// Provider is one DNS provider reached with saved credentials.
type Provider interface {
	// Key is the provider's key on the Certificates page ("cloudflare").
	Key() string
	// Name is how a sentence names it.
	Name() string
	// Zones lists the zones the credentials reach that may hold name:
	// every one of them, for an API that lists zones, or the one the
	// server answers for, for RFC 2136.
	Zones(ctx context.Context, name string) ([]Zone, error)
	// Read answers the A, AAAA and CNAME sets at name.
	Read(ctx context.Context, zone Zone, name string) ([]Set, error)
	// Write creates want, or updates current into want. current is nil
	// for a create and is the set Read answered for an update.
	Write(ctx context.Context, zone Zone, current *Set, want Set) error
	// PerRecord says the provider keeps one record per value, so a set
	// shrinks only by deleting one, which this package never does.
	PerRecord() bool
	// MinTTL is the shortest TTL the provider accepts.
	MinTTL() int
}

// Options are what a test changes: where the API answers, and the clock.
type Options struct {
	Client *http.Client
	// BaseURL replaces the provider's API address, for a fake. Google's
	// token endpoint is the key file's own token_uri.
	BaseURL string
	Now     func() time.Time
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) base(fallback string) string {
	if o.BaseURL != "" {
		return strings.TrimSuffix(o.BaseURL, "/")
	}
	return fallback
}

// ErrUnsupported is a provider whose credentials cannot write a record from
// here: Route 53 with no saved key, which certbot reads from the machine's
// role and this code does not.
var ErrUnsupported = errors.New("these credentials cannot write a record from here")

// New reaches a provider with the credentials saved for it, as the
// Certificates page saved them (an ini file of the plugin's keys, or
// Google's service-account JSON).
func New(key, credentials string, options Options) (Provider, error) {
	if strings.TrimSpace(credentials) == "" {
		return nil, fmt.Errorf("%w: nothing is saved for %s", ErrUnsupported, key)
	}
	switch key {
	case "cloudflare":
		return newCloudflare(credentials, options)
	case "route53":
		return newRoute53(credentials, options)
	case "digitalocean":
		return newDigitalOcean(credentials, options)
	case "google":
		return newGoogle(credentials, options)
	case "linode":
		return newLinode(credentials, options)
	case "ovh":
		return newOVH(credentials, options)
	case "gandi":
		return newGandi(credentials, options)
	case "rfc2136":
		return newRFC2136(credentials, options)
	}
	return nil, fmt.Errorf("%w: %q is not a provider this dashboard writes records with", ErrUnsupported, key)
}

// Error is a provider's refusal in its own words.
type Error struct {
	Provider  string
	Operation string
	Status    int
	Message   string
}

func (e *Error) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s refused to %s: %s", e.Provider, e.Operation, message)
}

// Canonical is a name as this package compares it: lower case, no
// surrounding space, no root dot.
func Canonical(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// InZone reports whether name is zone or a name under it.
func InZone(name, zone string) bool {
	name, zone = Canonical(name), Canonical(zone)
	return zone != "" && (name == zone || strings.HasSuffix(name, "."+zone))
}

// ZoneFor is the zone of zones that holds name: the longest one it is in,
// since a delegated sub-zone (preview.example.com inside example.com) is
// the one whose servers answer for it.
func ZoneFor(name string, zones []Zone) (Zone, bool) {
	best, found := Zone{}, false
	for _, zone := range zones {
		if !InZone(name, zone.Name) {
			continue
		}
		if !found || len(Canonical(zone.Name)) > len(Canonical(best.Name)) {
			best, found = zone, true
		}
	}
	return best, found
}

// Relative is name inside zone as most APIs spell it: "@" for the apex,
// the labels in front of the zone otherwise.
func Relative(name, zone string) string {
	name, zone = Canonical(name), Canonical(zone)
	if name == zone {
		return "@"
	}
	return strings.TrimSuffix(name, "."+zone)
}

// absolute is a relative name back as a full one.
func absolute(relative, zone string) string {
	relative = Canonical(relative)
	if relative == "" || relative == "@" {
		return Canonical(zone)
	}
	if InZone(relative, zone) {
		return relative
	}
	return relative + "." + Canonical(zone)
}

// DefaultTTL is what a record is written with when nobody asks for another:
// five minutes, short enough that a server's move is seen the same hour and
// the longest minimum any of these providers has (Gandi's).
const DefaultTTL = 300

// TTLFor is the TTL a provider is sent for a request: the default for
// none, never under the provider's minimum, and never over a day, which is
// longer than a deployment's address should be cached.
func TTLFor(provider Provider, requested int) int {
	ttl := requested
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if minimum := provider.MinTTL(); ttl < minimum {
		ttl = minimum
	}
	if ttl > 86400 {
		ttl = 86400
	}
	if _, ok := provider.(*linode); ok {
		ttl = linodeTTL(ttl)
	}
	return ttl
}

// normalizeValue is a value as two sets are compared: an address as itself,
// a target name as a canonical name.
func normalizeValue(kind Type, value string) string {
	value = strings.TrimSpace(value)
	if kind == TypeCNAME {
		return Canonical(value)
	}
	return strings.ToLower(value)
}

// SameValues reports whether two lists hold the same values of a type, in
// any order.
func SameValues(kind Type, a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left, right := make([]string, len(a)), make([]string, len(b))
	for index := range a {
		left[index], right[index] = normalizeValue(kind, a[index]), normalizeValue(kind, b[index])
	}
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// fqdn is a target name with the root's dot, as the providers that store
// names absolutely want a CNAME's value.
func fqdn(name string) string { return Canonical(name) + "." }
