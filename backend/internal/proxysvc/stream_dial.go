package proxysvc

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// A stream's Test dials from this host, which shares the host's network
// namespace: once at the upstream, to say whether nginx could reach it, and
// once at the stream's own port on the host, to say whether a client gets
// through nginx. netsec's PortCheck and BannerGrab answer the same question
// in prose with six-second timeouts of their own, and a banner read there
// outlives its context; the page needs an outcome it can colour and a result
// inside five seconds, so the dial is done here.

// StreamDialLimit is the whole test's budget: dial, banner or query reply.
const StreamDialLimit = 5 * time.Second

// streamBannerWait is how long a connected TCP test waits for the service to
// speak first. SSH, SMTP and FTP do at once; waiting longer only makes a
// service that waits for the client (HTTP, Postgres) cost more to test.
const streamBannerWait = 2 * time.Second

// StreamDialRequest is one test. Mode is "upstream" (the address nginx
// forwards to) or "nginx" (the stream's own port, dialled on the host), and
// only changes what an answer means. Query is "dns" or "ntp" to send a real
// request of that protocol; empty picks one from Port for 53 and 123, since a
// UDP datagram the service does not understand is answered with silence.
type StreamDialRequest struct {
	Target   string `json:"target"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Mode     string `json:"mode"`
	Query    string `json:"query,omitempty"`
}

// StreamDialResult is what the test saw. Outcome is connected, answered,
// closed, silent, refused, timeout, dns, unreachable or error; OK is true for
// the first two only.
type StreamDialResult struct {
	Mode     string `json:"mode"`
	Protocol string `json:"protocol"`
	// Address is what was dialled, the name resolved to an address.
	Address string `json:"address"`
	Outcome string `json:"outcome"`
	OK      bool   `json:"ok"`
	// Millis is how long the connect (TCP) or the reply (UDP) took.
	Millis int64 `json:"ms"`
	// Banner is what the service sent first, unprompted.
	Banner string `json:"banner,omitempty"`
	// Answer summarises a DNS or NTP reply.
	Answer   string   `json:"answer,omitempty"`
	Detail   string   `json:"detail"`
	Warnings []string `json:"warnings"`
	Error    string   `json:"error,omitempty"`
}

// DialStream runs one test. It returns an error only for a request it
// refuses to send; every network outcome is a result.
func DialStream(ctx context.Context, req StreamDialRequest) (*StreamDialResult, error) {
	target := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(req.Target), "["), "]")
	if !netsec.ValidTarget(target) {
		return nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if req.Protocol != "tcp" && req.Protocol != "udp" {
		return nil, fmt.Errorf("protocol must be tcp or udp")
	}
	if req.Mode != "upstream" && req.Mode != "nginx" {
		return nil, fmt.Errorf("mode must be upstream or nginx")
	}
	query := req.Query
	if query == "" {
		query = map[int]string{53: "dns", 123: "ntp"}[req.Port]
	}
	if query != "" && query != "dns" && query != "ntp" {
		return nil, fmt.Errorf("query must be dns or ntp")
	}
	if query == "ntp" && req.Protocol != "udp" {
		// NTP has no TCP form; a TCP test is a plain connect.
		query = ""
	}
	ctx, cancel := context.WithTimeout(ctx, StreamDialLimit)
	defer cancel()
	res := &StreamDialResult{Mode: req.Mode, Protocol: req.Protocol, Warnings: []string{}}
	if req.Mode == "upstream" && net.ParseIP(target) == nil {
		res.Warnings = append(res.Warnings, "nginx resolves "+target+" once, when it loads its configuration, "+
			"and keeps that address until the next reload; a change of address is not followed. "+
			"This test resolved it again just now.")
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, target)
	if err != nil || len(addrs) == 0 {
		res.Outcome, res.Detail = "dns", "The name did not resolve on this host."
		if err != nil {
			res.Error = err.Error()
		}
		return res, nil
	}
	res.Address = net.JoinHostPort(addrs[0].IP.String(), strconv.Itoa(req.Port))
	if req.Protocol == "udp" {
		dialUDP(ctx, res, query)
	} else {
		dialTCP(ctx, res, query)
	}
	return res, nil
}

func dialTCP(ctx context.Context, res *StreamDialResult, query string) {
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", res.Address)
	res.Millis = time.Since(start).Milliseconds()
	if err != nil {
		failed(res, err)
		return
	}
	defer conn.Close()
	res.Outcome, res.OK = "connected", true
	wait := time.Now().Add(streamBannerWait)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(wait) {
		wait = deadline
	}
	conn.SetDeadline(wait)
	if query == "dns" {
		// DNS over TCP is the same message behind a two-byte length.
		msg, id := dnsQuery()
		framed := binary.BigEndian.AppendUint16(nil, uint16(len(msg)))
		if _, err := conn.Write(append(framed, msg...)); err == nil {
			reply := make([]byte, 2+512)
			if n, err := io.ReadAtLeast(conn, reply, 2+12); err == nil {
				res.Outcome, res.Answer = "answered", readDNS(reply[2:n], id)
				res.Detail = throughWhat(res.Mode) + " answered a DNS query over TCP."
				return
			}
		}
	}
	buf := make([]byte, 1024)
	n, rerr := conn.Read(buf)
	switch {
	case n > 0:
		res.Banner = cleanBanner(string(buf[:n]))
		res.Detail = throughWhat(res.Mode) + " connected and the service announced itself."
	case rerr != nil && !isTimeoutErr(rerr):
		// nginx accepts before it dials the upstream, so a connection it
		// closes at once is the stream refusing this client or failing to
		// reach its upstream, not a port that is closed.
		res.Outcome, res.OK = "closed", false
		res.Error = rerr.Error()
		if res.Mode == "nginx" {
			res.Detail = "nginx accepted the connection and closed it at once: an access rule refusing this host, " +
				"a connection cap, or an upstream nginx could not reach."
		} else {
			res.Detail = "The upstream accepted the connection and closed it without a word."
		}
	case res.Mode == "nginx":
		res.Detail = fmt.Sprintf("nginx accepted the connection and held it open. The service sends nothing "+
			"until the client speaks, so this cannot show nginx reached it — only that nothing refused it within %d seconds.",
			int(streamBannerWait/time.Second))
	default:
		res.Detail = fmt.Sprintf("Connected in %d ms. The service sent nothing first, as HTTP, Postgres and most "+
			"request-driven services do.", res.Millis)
	}
}

func dialUDP(ctx context.Context, res *StreamDialResult, query string) {
	if query == "" {
		// Any other datagram would be a guess at the protocol, and silence
		// from a service that ignored it proves nothing either way.
		res.Outcome, res.Detail = "silent", "UDP has no connection to test. Only DNS (53) and NTP (123) "+
			"are tested, with a real query; this port is neither."
		return
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", res.Address)
	if err != nil {
		failed(res, err)
		return
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	var msg []byte
	var id uint16
	if query == "dns" {
		msg, id = dnsQuery()
	} else {
		msg = make([]byte, 48)
		msg[0] = 0x23 // no leap warning, version 4, client
	}
	start := time.Now()
	if _, err := conn.Write(msg); err != nil {
		failed(res, err)
		return
	}
	reply := make([]byte, 512)
	n, err := conn.Read(reply)
	res.Millis = time.Since(start).Milliseconds()
	if err != nil {
		if isTimeoutErr(err) {
			res.Outcome = "timeout"
			res.Error = err.Error()
			res.Detail = fmt.Sprintf("No %s reply within %d seconds. Over UDP that is a firewall dropping the "+
				"query, or nothing answering there.", strings.ToUpper(query), int(StreamDialLimit/time.Second))
			return
		}
		failed(res, err)
		return
	}
	if query == "dns" {
		res.Answer = readDNS(reply[:n], id)
	} else {
		res.Answer = readNTP(reply[:n])
	}
	res.Outcome, res.OK = "answered", true
	res.Detail = fmt.Sprintf("%s answered a %s query in %d ms.", throughWhat(res.Mode), strings.ToUpper(query), res.Millis)
}

func throughWhat(mode string) string {
	if mode == "nginx" {
		return "Through nginx, the service"
	}
	return "The upstream"
}

// failed records a dial that did not connect. Refused and timed out read
// differently on purpose: one is a host saying no, the other a firewall
// saying nothing.
func failed(res *StreamDialResult, err error) {
	res.Error = err.Error()
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		res.Outcome = "refused"
		if res.Mode == "nginx" {
			res.Detail = "Refused: nothing listens on this port, so nginx is not forwarding it."
		} else {
			res.Detail = "Refused: the host answered, and nothing listens on that port."
		}
	case errors.As(err, &dnsErr):
		res.Outcome, res.Detail = "dns", "The name did not resolve on this host."
	case isTimeoutErr(err):
		res.Outcome = "timeout"
		res.Detail = "Timed out with no reply, which is what a firewall dropping packets looks like."
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		res.Outcome, res.Detail = "unreachable", "No route to that address from this host."
	default:
		res.Outcome, res.Detail = "error", err.Error()
	}
}

func isTimeoutErr(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout())
}

// dnsQuery asks for the root's nameservers with recursion desired: every
// resolver can answer it, and an authoritative server that will not still
// replies with REFUSED, which is still DNS answering.
func dnsQuery() ([]byte, uint16) {
	var b [2]byte
	rand.Read(b[:])
	id := binary.BigEndian.Uint16(b[:])
	msg := binary.BigEndian.AppendUint16(nil, id)
	msg = append(msg, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0) // RD; one question
	msg = append(msg, 0, 0, 2, 0, 1)                      // ". NS IN"
	return msg, id
}

var dnsRcodes = []string{"NOERROR", "FORMERR", "SERVFAIL", "NXDOMAIN", "NOTIMP", "REFUSED"}

func readDNS(reply []byte, id uint16) string {
	if len(reply) < 12 || binary.BigEndian.Uint16(reply) != id || reply[2]&0x80 == 0 {
		return "a reply that is not an answer to this query"
	}
	code := int(reply[3] & 0x0f)
	name := "rcode " + strconv.Itoa(code)
	if code < len(dnsRcodes) {
		name = dnsRcodes[code]
	}
	return fmt.Sprintf("%s, %d answer records", name, binary.BigEndian.Uint16(reply[6:]))
}

func readNTP(reply []byte) string {
	if len(reply) < 48 || reply[0]&0x07 != 4 {
		return "a reply that is not an NTP server's"
	}
	if reply[1] == 0 {
		return "a kiss-o'-death: the server asks this client to stop (" + cleanBanner(string(reply[12:16])) + ")"
	}
	return "stratum " + strconv.Itoa(int(reply[1]))
}

// cleanBanner keeps a banner readable and the JSON honest: control bytes
// other than line breaks become dots rather than terminal escapes.
func cleanBanner(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return '.'
		}
		return r
	}, strings.TrimSpace(s))
}
