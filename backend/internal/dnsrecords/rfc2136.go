package dnsrecords

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// RFC 2136: the operator's own authoritative server (BIND, Knot,
// PowerDNS) taking a DNS UPDATE signed with the TSIG key saved for the
// DNS-01 plugin. Everything goes over TCP to the saved server and port: the
// zone is found by asking the server for the SOA of the name and each
// parent until it answers authoritatively; the sets are read with ordinary
// queries (signed when the plugin's dns_rfc2136_sign_query says so); and a
// set is written by one UPDATE whose prerequisite says it is still as read
// — absent for a create, present for a change — so a set that changed in
// between is refused rather than overwritten. A change replaces the set in
// the same message (RFC 2136's "delete an RRset" followed by its new
// records), which the server applies atomically; nothing else at the name
// is touched. Every answer's signature is verified.
//
// https://www.rfc-editor.org/rfc/rfc2136 · https://www.rfc-editor.org/rfc/rfc8945

type rfc2136 struct {
	server      string
	key         tsigKey
	signQueries bool
	now         func() time.Time
}

func newRFC2136(credentials string, options Options) (Provider, error) {
	values := iniValues(credentials)
	server := values["dns_rfc2136_server"]
	if server == "" || values["dns_rfc2136_name"] == "" || values["dns_rfc2136_secret"] == "" {
		return nil, fmt.Errorf("%w: the saved RFC 2136 credentials need the server, the key's name and its secret", ErrUnsupported)
	}
	port := values["dns_rfc2136_port"]
	if port == "" {
		port = "53"
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("%w: %q is not a port", ErrUnsupported, port)
	}
	if options.BaseURL != "" {
		// A test's server, as host:port.
		server, port, _ = net.SplitHostPort(options.BaseURL)
	}
	secret, err := base64.StdEncoding.DecodeString(values["dns_rfc2136_secret"])
	if err != nil || len(secret) == 0 {
		return nil, fmt.Errorf("%w: the saved TSIG secret is not base64", ErrUnsupported)
	}
	name := values["dns_rfc2136_algorithm"]
	if name == "" {
		// The plugin's own default.
		name = "HMAC-MD5"
	}
	algorithm, known := tsigAlgorithms[strings.ToUpper(name)]
	if !known {
		return nil, fmt.Errorf("%w: %s is not a TSIG algorithm (HMAC-MD5, HMAC-SHA1, HMAC-SHA224, HMAC-SHA256, HMAC-SHA384, HMAC-SHA512)", ErrUnsupported, name)
	}
	sign := strings.ToLower(values["dns_rfc2136_sign_query"])
	return &rfc2136{
		server:      net.JoinHostPort(strings.Trim(server, "[]"), port),
		key:         tsigKey{name: strings.TrimSuffix(values["dns_rfc2136_name"], "."), algorithm: algorithm.wire, secret: secret, hash: algorithm.hash},
		signQueries: sign == "true" || sign == "1" || sign == "yes",
		now:         options.now,
	}, nil
}

func (r *rfc2136) Key() string     { return "rfc2136" }
func (r *rfc2136) Name() string    { return "RFC 2136 (" + r.server + ")" }
func (r *rfc2136) PerRecord() bool { return false }
func (r *rfc2136) MinTTL() int     { return 1 }

// exchange sends one message over TCP and reads one answer, both prefixed
// with their length as DNS over TCP is.
func (r *rfc2136) exchange(ctx context.Context, message []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", r.server)
	if err != nil {
		return nil, fmt.Errorf("the RFC 2136 server %s could not be reached: %w", r.server, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	framed := appendUint16(nil, uint16(len(message)))
	if _, err := conn.Write(append(framed, message...)); err != nil {
		return nil, fmt.Errorf("the RFC 2136 server %s: %w", r.server, err)
	}
	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return nil, fmt.Errorf("the RFC 2136 server %s did not answer: %w", r.server, err)
	}
	answer := make([]byte, binary.BigEndian.Uint16(length[:]))
	if _, err := io.ReadFull(conn, answer); err != nil {
		return nil, fmt.Errorf("the RFC 2136 server %s's answer was cut short: %w", r.server, err)
	}
	return answer, nil
}

// send signs a message when asked to, exchanges it, and answers the reply
// once its signature, if the request had one, verifies.
func (r *rfc2136) send(ctx context.Context, message []byte, sign bool) (dnsReply, error) {
	var requestMAC []byte
	if sign {
		signed, mac, err := r.key.sign(message, r.now())
		if err != nil {
			return dnsReply{}, err
		}
		message, requestMAC = signed, mac
	}
	raw, err := r.exchange(ctx, message)
	if err != nil {
		return dnsReply{}, err
	}
	reply, err := parseReply(raw)
	if err != nil {
		return dnsReply{}, fmt.Errorf("the RFC 2136 server's answer could not be read: %w", err)
	}
	if reply.ID != binary.BigEndian.Uint16(message) {
		return dnsReply{}, errors.New("the RFC 2136 server answered another request")
	}
	if sign {
		if err := r.key.verify(raw, reply, requestMAC, r.now()); err != nil {
			if reply.Rcode != 0 {
				return dnsReply{}, fmt.Errorf("the RFC 2136 server refused: %s; %w", rcodeWords(reply.Rcode), err)
			}
			return dnsReply{}, fmt.Errorf("the RFC 2136 server's answer: %w", err)
		}
	}
	return reply, nil
}

func randomID() uint16 {
	var id [2]byte
	_, _ = rand.Read(id[:])
	return binary.BigEndian.Uint16(id[:])
}

// query asks the server for one type at a name.
func (r *rfc2136) query(ctx context.Context, name string, kind uint16) (dnsReply, error) {
	message := dnsHeader(randomID(), dnsOpQuery, false, [4]uint16{1, 0, 0, 0})
	message, err := appendName(message, name)
	if err != nil {
		return dnsReply{}, err
	}
	message = appendUint16(message, kind)
	message = appendUint16(message, dnsClassIN)
	return r.send(ctx, message, r.signQueries)
}

// Zones asks for the SOA of the name and each parent in turn: the first
// the server answers for authoritatively, with the SOA in its answer or the
// zone's SOA in its authority section, is the zone.
func (r *rfc2136) Zones(ctx context.Context, name string) ([]Zone, error) {
	name = Canonical(name)
	var refused error
	for candidate := name; candidate != ""; {
		reply, err := r.query(ctx, candidate, dnsTypeSOA)
		if err != nil {
			return nil, err
		}
		if reply.Rcode == 5 || reply.Rcode == 9 {
			refused = fmt.Errorf("the RFC 2136 server %s answers for no zone holding %s: %s", r.server, name, rcodeWords(reply.Rcode))
		}
		if reply.Authoritative {
			for _, record := range append(append([]dnsRR{}, reply.Answers...), reply.Authority...) {
				if record.Type == dnsTypeSOA && InZone(name, record.Name) {
					zone := Canonical(record.Name)
					return []Zone{{ID: zone, Name: zone}}, nil
				}
			}
		}
		_, parent, found := strings.Cut(candidate, ".")
		if !found {
			break
		}
		candidate = parent
	}
	if refused != nil {
		return nil, refused
	}
	return []Zone{}, nil
}

func (r *rfc2136) Read(ctx context.Context, _ Zone, name string) ([]Set, error) {
	out := []Set{}
	for _, kind := range Types {
		number := dnsTypeNumbers[kind]
		reply, err := r.query(ctx, Canonical(name), number)
		if err != nil {
			return nil, err
		}
		if reply.Rcode != 0 && reply.Rcode != 3 {
			return nil, fmt.Errorf("the RFC 2136 server would not answer for %s %s: %s", kind, Canonical(name), rcodeWords(reply.Rcode))
		}
		set := Set{Name: Canonical(name), Type: kind}
		for _, record := range reply.Answers {
			if record.Type != number || Canonical(record.Name) != Canonical(name) {
				continue
			}
			value, err := r.value(reply, record)
			if err != nil {
				return nil, err
			}
			set.Values = append(set.Values, value)
			set.TTL = int(record.TTL)
		}
		if len(set.Values) > 0 {
			out = append(out, set)
		}
	}
	return out, nil
}

// value reads a record's data as text.
func (r *rfc2136) value(reply dnsReply, record dnsRR) (string, error) {
	switch record.Type {
	case dnsTypeA, dnsTypeAAAA:
		address, ok := netip.AddrFromSlice(record.Data)
		if !ok {
			return "", errors.New("an address record holds no address")
		}
		return address.Unmap().String(), nil
	case dnsTypeCNAME:
		target, _, err := readName(reply.raw, record.dataAt)
		return Canonical(target), err
	}
	return "", fmt.Errorf("record type %d is not read here", record.Type)
}

// rdata is a value as a record's data.
func rdata(kind Type, value string) ([]byte, error) {
	switch kind {
	case TypeA, TypeAAAA:
		address, err := netip.ParseAddr(value)
		if err != nil || (kind == TypeA) != address.Is4() {
			return nil, fmt.Errorf("%q is not an %s address", value, kind)
		}
		return address.AsSlice(), nil
	case TypeCNAME:
		return appendName(nil, Canonical(value))
	}
	return nil, fmt.Errorf("%s is not written here", kind)
}

// Write sends one signed UPDATE for the set.
func (r *rfc2136) Write(ctx context.Context, zone Zone, current *Set, want Set) error {
	number := dnsTypeNumbers[want.Type]
	name := Canonical(want.Name)
	updates := uint16(len(want.Values))
	if current != nil {
		updates++
	}
	message := dnsHeader(randomID(), dnsOpUpdate, false, [4]uint16{1, 1, updates, 0})
	message, err := appendName(message, Canonical(zone.Name))
	if err != nil {
		return err
	}
	message = appendUint16(message, dnsTypeSOA)
	message = appendUint16(message, dnsClassIN)
	// The prerequisite: the set is absent (class NONE) for a create, and
	// present whatever it holds (class ANY) for a change.
	prerequisite := dnsClassNONE
	if current != nil {
		prerequisite = dnsClassANY
	}
	if message, err = appendRR(message, name, number, prerequisite, 0, nil); err != nil {
		return err
	}
	if current != nil {
		// Delete the set, then add its new records: one update, applied
		// as one.
		if message, err = appendRR(message, name, number, dnsClassANY, 0, nil); err != nil {
			return err
		}
	}
	for _, value := range want.Values {
		data, err := rdata(want.Type, value)
		if err != nil {
			return err
		}
		if message, err = appendRR(message, name, number, dnsClassIN, uint32(want.TTL), data); err != nil {
			return err
		}
	}
	reply, err := r.send(ctx, message, true)
	if err != nil {
		return err
	}
	if reply.Rcode != 0 {
		verb := "create"
		if current != nil {
			verb = "change"
		}
		return &Error{Provider: r.Name(), Operation: verb + " " + string(want.Type) + " " + name, Message: rcodeWords(reply.Rcode)}
	}
	return nil
}
