// This fixture answers only its finite reserved names and never forwards DNS.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const frameLimit = 4096

var noncePattern = regexp.MustCompile(`^[a-f0-9]{12}$`)

func names(nonce string) ([]string, error) {
	if !noncePattern.MatchString(nonce) {
		return nil, errors.New("fixture nonce is outside its closed format")
	}
	parent := "seed-" + nonce + ".example"
	deny := "deny-" + nonce + ".example"
	return []string{parent, "allow." + parent, "child.allow." + parent, deny, "child." + deny,
		"empty-" + nonce + ".example", "neutral-" + nonce + ".example", "seed-" + nonce + "-lookalike.example"}, nil
}

func question(nonce, name, kind string) (dnsmessage.Question, error) {
	allowed, err := names(nonce)
	if err != nil {
		return dnsmessage.Question{}, err
	}
	found := false
	for _, candidate := range allowed {
		found = found || candidate == name
	}
	if !found || kind != "A" && kind != "AAAA" {
		return dnsmessage.Question{}, errors.New("question is outside the finite fixture inventory")
	}
	owner, _ := dnsmessage.NewName(name + ".")
	typ := dnsmessage.TypeA
	if kind == "AAAA" {
		typ = dnsmessage.TypeAAAA
	}
	return dnsmessage.Question{Name: owner, Type: typ, Class: dnsmessage.ClassINET}, nil
}

func positive(q dnsmessage.Question) dnsmessage.Resource {
	r := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: dnsmessage.ClassINET, TTL: 0}}
	if q.Type == dnsmessage.TypeA {
		r.Body = &dnsmessage.AResource{A: [4]byte{198, 51, 100, 23}}
	} else {
		r.Body = &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 1, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x23}}
	}
	return r
}

func reply(nonce string, wire []byte) ([]byte, dnsmessage.Question, error) {
	var m dnsmessage.Message
	if len(wire) > frameLimit || m.Unpack(wire) != nil || m.Response || m.OpCode != 0 || len(m.Questions) != 1 || len(m.Answers) != 0 || len(m.Authorities) != 0 {
		return nil, dnsmessage.Question{}, errors.New("upstream question has an unsupported shape")
	}
	q := m.Questions[0]
	kind := q.Type.String()
	if q.Type == dnsmessage.TypeA {
		kind = "A"
	} else if q.Type == dnsmessage.TypeAAAA {
		kind = "AAAA"
	}
	want, err := question(nonce, strings.TrimSuffix(q.Name.String(), "."), kind)
	if err != nil || q != want {
		return nil, q, errors.New("upstream question is outside the finite fixture inventory")
	}
	m.Header = dnsmessage.Header{ID: m.ID, Response: true, RecursionDesired: m.RecursionDesired, RecursionAvailable: true}
	m.Answers, m.Additionals = []dnsmessage.Resource{positive(q)}, nil
	wire, err = m.Pack()
	return wire, q, err
}

func readFrame(reader io.Reader) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(size[:]))
	if n == 0 || n > frameLimit {
		return nil, errors.New("DNS frame exceeds its fixture bound")
	}
	wire := make([]byte, n)
	_, err := io.ReadFull(reader, wire)
	return wire, err
}

func writeFrame(writer io.Writer, wire []byte) error {
	frame := make([]byte, len(wire)+2)
	binary.BigEndian.PutUint16(frame, uint16(len(wire)))
	copy(frame[2:], wire)
	n, err := writer.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

type outcome struct {
	ID       uint16 `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

func readOutcome(wire []byte, q dnsmessage.Question, id uint16, protocol string) (outcome, error) {
	var m dnsmessage.Message
	if len(wire) > frameLimit || m.Unpack(wire) != nil || m.ID != id || !m.Response || m.Truncated || m.RCode != dnsmessage.RCodeSuccess || m.OpCode != 0 || len(m.Questions) != 1 || m.Questions[0] != q || len(m.Answers) != 1 || len(m.Authorities) != 0 {
		return outcome{}, errors.New("DNS response lacks the exact fixture question and single successful answer")
	}
	a := m.Answers[0]
	if a.Header.Name != q.Name || a.Header.Type != q.Type || a.Header.Class != dnsmessage.ClassINET {
		return outcome{}, errors.New("DNS answer owner/type/class differs")
	}
	address := ""
	switch body := a.Body.(type) {
	case *dnsmessage.AResource:
		address = netip.AddrFrom4(body.A).String()
	case *dnsmessage.AAAAResource:
		address = netip.AddrFrom16(body.AAAA).String()
	default:
		return outcome{}, errors.New("DNS answer is outside A/AAAA fixture bodies")
	}
	if address != "198.51.100.23" && address != "2001:db8::23" && address != "0.0.0.0" && address != "::" {
		return outcome{}, errors.New("DNS answer is neither the owned upstream value nor the explicit null-IP denial")
	}
	kind := "A"
	if q.Type == dnsmessage.TypeAAAA {
		kind = "AAAA"
	}
	return outcome{ID: id, Name: strings.TrimSuffix(q.Name.String(), "."), Type: kind, Protocol: protocol, Address: address}, nil
}

func query(ctx context.Context, nonce, endpoint, protocol, kind, name string, id uint16) (outcome, error) {
	address, err := netip.ParseAddrPort(endpoint)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsPrivate() || address.Port() != 53 || protocol != "udp" && protocol != "tcp" {
		return outcome{}, errors.New("query target must be the owned private IPv4 engine on classic DNS")
	}
	q, err := question(nonce, name, kind)
	if err != nil {
		return outcome{}, err
	}
	wire, _ := (&dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true}, Questions: []dnsmessage.Question{q}}).Pack()
	conn, err := (&net.Dialer{}).DialContext(ctx, protocol, endpoint)
	if err != nil {
		return outcome{}, errors.New("owned engine DNS connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(900 * time.Millisecond)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	conn.SetDeadline(deadline)
	if protocol == "tcp" {
		if err = writeFrame(conn, wire); err == nil {
			wire, err = readFrame(conn)
		}
	} else {
		_, err = conn.Write(wire)
		if err == nil {
			wire = make([]byte, frameLimit+1)
			var n int
			n, err = conn.Read(wire)
			wire = wire[:n]
		}
	}
	if err != nil {
		return outcome{}, errors.New("owned engine DNS exchange failed")
	}
	return readOutcome(wire, q, id, protocol)
}

func serve(ctx context.Context, nonce string) error {
	if _, err := names(nonce); err != nil {
		return err
	}
	udp, err := net.ListenPacket("udp4", "0.0.0.0:5353")
	if err != nil {
		return err
	}
	defer udp.Close()
	tcp, err := net.Listen("tcp4", "0.0.0.0:5353")
	if err != nil {
		return err
	}
	defer tcp.Close()
	var mu sync.Mutex
	var wg sync.WaitGroup
	count := 0
	connections := map[net.Conn]bool{}
	respond := func(wire []byte, protocol string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if count >= 256 {
			return nil, errors.New("upstream fixture query budget exhausted")
		}
		count++
		body, q, err := reply(nonce, wire)
		if err == nil {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"phase": "owned_upstream_answer", "sequence": count, "name": q.Name.String(), "type": q.Type.String(), "protocol": protocol})
		}
		return body, err
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			wire := make([]byte, frameLimit+1)
			n, peer, e := udp.ReadFrom(wire)
			if e != nil {
				return
			}
			if body, e := respond(wire[:n], "udp"); e == nil {
				udp.WriteTo(body, peer)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			conn, e := tcp.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			if len(connections) >= 8 {
				mu.Unlock()
				conn.Close()
				continue
			}
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				conn.SetDeadline(time.Now().Add(time.Second))
				if wire, e := readFrame(conn); e == nil {
					if body, e := respond(wire, "tcp"); e == nil {
						writeFrame(conn, body)
					}
				}
			}()
		}
	}()
	fmt.Println(`{"phase":"owned_upstream_ready","port":5353,"forwarding":false}`)
	<-ctx.Done()
	udp.Close()
	tcp.Close()
	mu.Lock()
	for conn := range connections {
		conn.Close()
	}
	mu.Unlock()
	wg.Wait()
	return nil
}

func main() {
	args := os.Args[1:]
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	var err error
	if len(args) == 2 && args[0] == "serve" {
		err = serve(ctx, args[1])
	} else if len(args) == 7 && args[0] == "query" {
		var id uint64
		id, err = strconv.ParseUint(args[6], 10, 16)
		if err == nil {
			var result outcome
			result, err = query(ctx, args[1], args[2], args[3], args[4], args[5], uint16(id))
			if err == nil {
				err = json.NewEncoder(os.Stdout).Encode(result)
			}
		}
	} else {
		err = errors.New("unsupported fixture operation")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
