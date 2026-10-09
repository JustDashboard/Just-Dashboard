package netsec

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// A diagnostic DNS question asked of one named server, with every section of
// the answer kept. net.Resolver hides the server, the transport, TTLs, the
// authoritative flag and the referral sections — exactly what provenance and
// delegation checks are about.

type dnsRR struct {
	Name   string
	Type   string
	TTL    uint32
	Value  string
	Serial uint32
}

type dnsReply struct {
	Server        string
	Transport     string
	RCode         string
	Authoritative bool
	Truncated     bool
	Answer        []dnsRR
	Authority     []dnsRR
	Additional    []dnsRR
	Elapsed       time.Duration
}

var dnsQueryTypes = map[string]dnsmessage.Type{
	"A": dnsmessage.TypeA, "AAAA": dnsmessage.TypeAAAA, "CNAME": dnsmessage.TypeCNAME,
	"MX": dnsmessage.TypeMX, "TXT": dnsmessage.TypeTXT, "NS": dnsmessage.TypeNS,
	"PTR": dnsmessage.TypePTR, "SOA": dnsmessage.TypeSOA,
}

// dnsExchange is the seam tests replace to answer from a fixture.
var dnsExchange = exchangeDNSWire

// exchangeDNSWire asks server (host:port) one question over UDP, retrying over
// TCP when the answer is truncated, as a stub resolver would. recursion sets
// RD: false asks an authoritative server about its own zone only.
func exchangeDNSWire(ctx context.Context, server, name, rtype string, recursion bool) (*dnsReply, error) {
	qtype, ok := dnsQueryTypes[rtype]
	if !ok {
		return nil, fmt.Errorf("unsupported record type %s", rtype)
	}
	fqdn, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return nil, err
	}
	var idb [2]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(idb[:])
	question := dnsmessage.Question{Name: fqdn, Type: qtype, Class: dnsmessage.ClassINET}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: recursion})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(question); err != nil {
		return nil, err
	}
	if err := b.StartAdditionals(); err != nil {
		return nil, err
	}
	var opt dnsmessage.ResourceHeader
	if err := opt.SetEDNS0(1232, dnsmessage.RCodeSuccess, false); err != nil {
		return nil, err
	}
	if err := b.OPTResource(opt, dnsmessage.OPTResource{}); err != nil {
		return nil, err
	}
	packet, err := b.Finish()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	start := time.Now()
	raw, err := dnsRoundTrip(ctx, "udp", server, packet)
	if err != nil {
		return nil, err
	}
	reply, err := parseDNSReply(raw, id, question)
	if err != nil {
		return nil, err
	}
	reply.Transport = "UDP"
	if reply.Truncated {
		raw, err = dnsRoundTrip(ctx, "tcp", server, packet)
		if err != nil {
			return nil, fmt.Errorf("the UDP answer was truncated and TCP failed: %w", err)
		}
		if reply, err = parseDNSReply(raw, id, question); err != nil {
			return nil, err
		}
		reply.Transport = "TCP after a truncated UDP answer"
	}
	reply.Server, reply.Elapsed = server, time.Since(start)
	return reply, nil
}

func dnsRoundTrip(ctx context.Context, network, server string, packet []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if network == "udp" {
		if _, err := conn.Write(packet); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	frame := make([]byte, 2+len(packet))
	binary.BigEndian.PutUint16(frame, uint16(len(packet)))
	copy(frame[2:], packet)
	if _, err := conn.Write(frame); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func parseDNSReply(raw []byte, id uint16, question dnsmessage.Question) (*dnsReply, error) {
	var p dnsmessage.Parser
	h, err := p.Start(raw)
	if err != nil {
		return nil, err
	}
	if h.ID != id || !h.Response {
		return nil, errors.New("the server answered a different question")
	}
	reply := &dnsReply{RCode: dnsRCode(h.RCode), Authoritative: h.Authoritative, Truncated: h.Truncated}
	if h.Truncated {
		return reply, nil
	}
	questions, err := p.AllQuestions()
	if err != nil {
		return nil, err
	}
	if len(questions) != 1 || !strings.EqualFold(questions[0].Name.String(), question.Name.String()) || questions[0].Type != question.Type {
		return nil, errors.New("the server answered a different question")
	}
	for _, section := range []struct {
		header func() (dnsmessage.ResourceHeader, error)
		into   *[]dnsRR
	}{{p.AnswerHeader, &reply.Answer}, {p.AuthorityHeader, &reply.Authority}, {p.AdditionalHeader, &reply.Additional}} {
		for {
			rh, err := section.header()
			if errors.Is(err, dnsmessage.ErrSectionDone) {
				break
			}
			if err != nil {
				return nil, err
			}
			rr, keep, err := readRR(&p, rh)
			if err != nil {
				return nil, err
			}
			if keep && len(*section.into) < 64 {
				*section.into = append(*section.into, rr)
			}
		}
	}
	return reply, nil
}

func readRR(p *dnsmessage.Parser, rh dnsmessage.ResourceHeader) (dnsRR, bool, error) {
	rr := dnsRR{Name: strings.ToLower(strings.TrimSuffix(rh.Name.String(), ".")), TTL: rh.TTL}
	switch rh.Type {
	case dnsmessage.TypeA:
		r, err := p.AResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "A", netip.AddrFrom4(r.A).String()
	case dnsmessage.TypeAAAA:
		r, err := p.AAAAResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "AAAA", netip.AddrFrom16(r.AAAA).String()
	case dnsmessage.TypeCNAME:
		r, err := p.CNAMEResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "CNAME", dnsHost(r.CNAME)
	case dnsmessage.TypeNS:
		r, err := p.NSResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "NS", dnsHost(r.NS)
	case dnsmessage.TypePTR:
		r, err := p.PTRResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "PTR", dnsHost(r.PTR)
	case dnsmessage.TypeMX:
		r, err := p.MXResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "MX", strconv.Itoa(int(r.Pref))+" "+dnsHost(r.MX)
	case dnsmessage.TypeTXT:
		r, err := p.TXTResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Value = "TXT", strings.Join(r.TXT, "")
	case dnsmessage.TypeSOA:
		r, err := p.SOAResource()
		if err != nil {
			return rr, false, err
		}
		rr.Type, rr.Serial = "SOA", r.Serial
		rr.Value = fmt.Sprintf("%s %s %d", dnsHost(r.NS), dnsHost(r.MBox), r.Serial)
	default:
		// UnknownResource consumes a record in whichever section the parser
		// is in, including the OPT pseudo-record among the additionals.
		_, err := p.UnknownResource()
		return rr, false, err
	}
	return rr, true, nil
}

func dnsHost(name dnsmessage.Name) string {
	host := strings.ToLower(strings.TrimSuffix(name.String(), "."))
	if host == "" {
		return "."
	}
	return host
}

func dnsRCode(rc dnsmessage.RCode) string {
	switch rc {
	case dnsmessage.RCodeSuccess:
		return "NOERROR"
	case dnsmessage.RCodeFormatError:
		return "FORMERR"
	case dnsmessage.RCodeServerFailure:
		return "SERVFAIL"
	case dnsmessage.RCodeNameError:
		return "NXDOMAIN"
	case dnsmessage.RCodeNotImplemented:
		return "NOTIMP"
	case dnsmessage.RCodeRefused:
		return "REFUSED"
	}
	return "RCODE " + strconv.Itoa(int(rc))
}

// reverseName is the in-addr.arpa / ip6.arpa owner name for an address.
func reverseName(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		b := a.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", b[3], b[2], b[1], b[0])
	}
	b := a.As16()
	const hexd = "0123456789abcdef"
	var sb strings.Builder
	for i := len(b) - 1; i >= 0; i-- {
		sb.WriteByte(hexd[b[i]&0x0f])
		sb.WriteByte('.')
		sb.WriteByte(hexd[b[i]>>4])
		sb.WriteByte('.')
	}
	return sb.String() + "ip6.arpa"
}

// values returns the RR values of one type in a section, in order.
func rrValues(rrs []dnsRR, rtype string) []string {
	var out []string
	for _, rr := range rrs {
		if rr.Type == rtype {
			out = append(out, rr.Value)
		}
	}
	return out
}
