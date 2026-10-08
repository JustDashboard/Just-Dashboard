package netx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strings"

	"golang.org/x/net/dns/dnsmessage"
)

var wireLookupTypes = map[string]dnsmessage.Type{
	"A": dnsmessage.TypeA, "AAAA": dnsmessage.TypeAAAA, "CNAME": dnsmessage.TypeCNAME,
	"MX": dnsmessage.TypeMX, "TXT": dnsmessage.TypeTXT, "NS": dnsmessage.TypeNS,
	"PTR": dnsmessage.TypePTR, "SRV": dnsmessage.TypeSRV,
}

// A per-resolver diagnostic must reach that resolver. net.Resolver consults
// /etc/hosts first even with PreferGo and a custom Dial, which would attribute
// a local override to every upstream and never send the question.
func lookupDNS(ctx context.Context, server, name, rtype string) ([]string, error) {
	qtype, ok := wireLookupTypes[rtype]
	if !ok {
		return nil, fmt.Errorf("unsupported record type %s", rtype)
	}
	qname := name
	if rtype == "PTR" {
		a, err := netip.ParseAddr(name)
		if err != nil {
			return nil, err
		}
		qname = reverseDNSName(a.Unmap())
	}
	fqdn, err := dnsmessage.NewName(strings.TrimSuffix(qname, ".") + ".")
	if err != nil {
		return nil, err
	}
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	question := dnsmessage.Question{Name: fqdn, Type: qtype, Class: dnsmessage.ClassINET}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(question); err != nil {
		return nil, err
	}
	packet, err := builder.Finish()
	if err != nil {
		return nil, err
	}
	address := server
	if _, _, err := net.SplitHostPort(server); err != nil {
		address = net.JoinHostPort(server, "53")
	}
	response, err := exchangeDNS(ctx, "udp", address, packet, id)
	if err != nil {
		return nil, err
	}
	answers, truncated, err := parseDNSResponse(response, id, question)
	if err != nil || !truncated {
		return answers, err
	}
	response, err = exchangeDNS(ctx, "tcp", address, packet, id)
	if err != nil {
		return nil, fmt.Errorf("truncated DNS answer could not be retried over TCP: %w", err)
	}
	answers, truncated, err = parseDNSResponse(response, id, question)
	if truncated && err == nil {
		err = errors.New("DNS answer is still truncated over TCP")
	}
	return answers, err
}

func reverseDNSName(a netip.Addr) string {
	if a.Is4() {
		v := a.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", v[3], v[2], v[1], v[0])
	}
	v := a.As16()
	var out strings.Builder
	for i := len(v) - 1; i >= 0; i-- {
		fmt.Fprintf(&out, "%x.%x.", v[i]&15, v[i]>>4)
	}
	out.WriteString("ip6.arpa")
	return out.String()
}

func exchangeDNS(ctx context.Context, network, address string, packet []byte, id uint16) ([]byte, error) {
	conn, err := dnsDial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if network == "tcp" {
		framed := make([]byte, len(packet)+2)
		binary.BigEndian.PutUint16(framed, uint16(len(packet)))
		copy(framed[2:], packet)
		if _, err := io.Copy(conn, bytes.NewReader(framed)); err != nil {
			return nil, err
		}
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return nil, err
		}
		response := make([]byte, binary.BigEndian.Uint16(size[:]))
		_, err := io.ReadFull(conn, response)
		return response, err
	}
	if _, err := conn.Write(packet); err != nil {
		return nil, err
	}
	response := make([]byte, 65535)
	for {
		n, err := conn.Read(response)
		if err != nil {
			return nil, err
		}
		if n >= 2 && binary.BigEndian.Uint16(response[:2]) == id {
			return response[:n], nil
		}
	}
}

func parseDNSResponse(packet []byte, id uint16, question dnsmessage.Question) ([]string, bool, error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(packet)
	if err != nil {
		return nil, false, err
	}
	if header.ID != id || !header.Response || header.OpCode != 0 {
		return nil, false, errors.New("the resolver answered a different question")
	}
	questions, err := parser.AllQuestions()
	if err != nil {
		return nil, false, err
	}
	if len(questions) != 1 || questions[0].Type != question.Type || questions[0].Class != question.Class || dnsNameKey(questions[0].Name) != dnsNameKey(question.Name) {
		return nil, false, errors.New("the resolver answered a different question")
	}
	if header.Truncated {
		return nil, true, nil
	}
	if header.RCode != dnsmessage.RCodeSuccess {
		failure := &net.DNSError{Name: question.Name.String(), Err: header.RCode.String()}
		if header.RCode == dnsmessage.RCodeNameError {
			failure.IsNotFound = true
		}
		return nil, false, failure
	}
	type record struct {
		owner, value string
		priority     uint16
	}
	type alias struct {
		target      string
		conflicting bool
	}
	aliases := map[string]alias{}
	var records []record
	for {
		rh, err := parser.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		if rh.Class != dnsmessage.ClassINET || rh.Type != question.Type && rh.Type != dnsmessage.TypeCNAME {
			if err := parser.SkipAnswer(); err != nil {
				return nil, false, err
			}
			continue
		}
		rec := record{owner: dnsNameKey(rh.Name)}
		switch rh.Type {
		case dnsmessage.TypeA:
			var r dnsmessage.AResource
			r, err = parser.AResource()
			rec.value = netip.AddrFrom4(r.A).String()
		case dnsmessage.TypeAAAA:
			var r dnsmessage.AAAAResource
			r, err = parser.AAAAResource()
			rec.value = netip.AddrFrom16(r.AAAA).String()
		case dnsmessage.TypeCNAME:
			var r dnsmessage.CNAMEResource
			r, err = parser.CNAMEResource()
			rec.value = r.CNAME.String()
			if err == nil {
				target := dnsNameKey(r.CNAME)
				previous, exists := aliases[rec.owner]
				aliases[rec.owner] = alias{target: target, conflicting: previous.conflicting || exists && previous.target != target}
			}
		case dnsmessage.TypeMX:
			var r dnsmessage.MXResource
			r, err = parser.MXResource()
			rec.priority, rec.value = r.Pref, fmt.Sprintf("%d %s", r.Pref, r.MX)
		case dnsmessage.TypeTXT:
			var r dnsmessage.TXTResource
			r, err = parser.TXTResource()
			rec.value = strings.Join(r.TXT, "")
		case dnsmessage.TypeNS:
			var r dnsmessage.NSResource
			r, err = parser.NSResource()
			rec.value = r.NS.String()
		case dnsmessage.TypePTR:
			var r dnsmessage.PTRResource
			r, err = parser.PTRResource()
			rec.value = r.PTR.String()
		case dnsmessage.TypeSRV:
			var r dnsmessage.SRVResource
			r, err = parser.SRVResource()
			rec.priority, rec.value = r.Priority, fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, r.Target)
		}
		if err != nil {
			return nil, false, err
		}
		if rh.Type == question.Type {
			records = append(records, rec)
		}
	}
	// Answer order is not significant. First follow the aliases rooted at the
	// question, then accept only that terminal owner's requested records. A
	// CNAME question asks for the original alias itself, not its target's data.
	original := dnsNameKey(question.Name)
	owner := original
	visited := map[string]bool{}
	for {
		if visited[owner] {
			return nil, false, errors.New("DNS answer contains a CNAME cycle")
		}
		visited[owner] = true
		a, exists := aliases[owner]
		if !exists {
			break
		}
		if a.conflicting {
			return nil, false, errors.New("DNS answer contains conflicting CNAME targets")
		}
		owner = a.target
	}
	if question.Type == dnsmessage.TypeCNAME {
		owner = original
	}
	filtered := records[:0]
	for _, rec := range records {
		if question.Type != dnsmessage.TypeCNAME && visited[rec.owner] {
			if _, isAlias := aliases[rec.owner]; isAlias {
				return nil, false, errors.New("DNS answer contains both a CNAME and its requested records")
			}
		}
		if rec.owner == owner {
			filtered = append(filtered, rec)
		}
	}
	records = filtered
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].priority != records[j].priority {
			return records[i].priority < records[j].priority
		}
		return records[i].value < records[j].value
	})
	answers := make([]string, len(records))
	for i, rec := range records {
		answers[i] = rec.value
	}
	return answers, false, nil
}

// DNS case folding applies only to ASCII letters (RFC 4343), not Unicode
// equivalents such as the Kelvin sign. Names from the wire can contain both.
func dnsNameKey(name dnsmessage.Name) string {
	key := []byte(name.String())
	for i, b := range key {
		if b >= 'A' && b <= 'Z' {
			key[i] = b + ('a' - 'A')
		}
	}
	return string(key)
}
