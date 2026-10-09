package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestFiniteUpstreamAnswersAndNoForwarding(t *testing.T) {
	nonce := "012345abcdef"
	allowed, err := names(nonce)
	if err != nil || len(allowed) != 8 {
		t.Fatal(err)
	}
	for _, name := range allowed {
		for _, kind := range []string{"A", "AAAA"} {
			q, err := question(nonce, name, kind)
			if err != nil {
				t.Fatal(err)
			}
			wire, _ := (&dnsmessage.Message{Header: dnsmessage.Header{ID: 42, RecursionDesired: true}, Questions: []dnsmessage.Question{q}}).Pack()
			answer, got, err := reply(nonce, wire)
			if err != nil || got != q {
				t.Fatal(err)
			}
			result, err := readOutcome(answer, q, 42, "udp")
			if err != nil || result.Name != name || result.Type != kind || result.Address != map[string]string{"A": "198.51.100.23", "AAAA": "2001:db8::23"}[kind] {
				t.Fatal(result, err)
			}
			var parsed dnsmessage.Message
			parsed.Unpack(answer)
			if parsed.Answers[0].Header.TTL != 0 {
				t.Fatal("fixture introduced positive cache lifetime")
			}
		}
	}
	for _, input := range []struct{ nonce, name, kind string }{{nonce, "external.example", "A"}, {nonce, "neutral-" + nonce + ".invalid", "TXT"}, {"../secret", "external.example", "A"}, {nonce, "neutral-" + nonce + ".invalid.attacker.example", "A"}} {
		if _, err := question(input.nonce, input.name, input.kind); err == nil {
			t.Fatal("arbitrary name or type escaped the finite inventory")
		}
	}
}

func TestMalformedQuestionsAndResponsesNeverBecomeDecisions(t *testing.T) {
	nonce := "012345abcdef"
	q, _ := question(nonce, "neutral-"+nonce+".invalid", "A")
	base := dnsmessage.Message{Header: dnsmessage.Header{ID: 42, RecursionDesired: true}, Questions: []dnsmessage.Question{q}}
	for _, mutate := range []func(*dnsmessage.Message){func(m *dnsmessage.Message) { m.Response = true }, func(m *dnsmessage.Message) { m.Questions = nil }, func(m *dnsmessage.Message) { m.Questions = append(m.Questions, q) }, func(m *dnsmessage.Message) { m.Questions[0].Class = dnsmessage.ClassCHAOS }, func(m *dnsmessage.Message) { m.Answers = []dnsmessage.Resource{positive(q)} }} {
		m := base
		m.Questions = append([]dnsmessage.Question{}, base.Questions...)
		mutate(&m)
		wire, _ := m.Pack()
		if _, _, err := reply(nonce, wire); err == nil {
			t.Fatal("malformed upstream request accepted")
		}
	}
	wire, _ := base.Pack()
	valid, _, _ := reply(nonce, wire)
	var baseline dnsmessage.Message
	baseline.Unpack(valid)
	for _, mutate := range []func(*dnsmessage.Message){func(m *dnsmessage.Message) { m.ID++ }, func(m *dnsmessage.Message) { m.Truncated = true }, func(m *dnsmessage.Message) { m.RCode = dnsmessage.RCodeServerFailure }, func(m *dnsmessage.Message) { m.RCode = dnsmessage.RCodeNameError }, func(m *dnsmessage.Message) { m.Answers = nil }, func(m *dnsmessage.Message) { m.Answers = append(m.Answers, m.Answers[0]) }, func(m *dnsmessage.Message) { m.Answers[0].Header.Name, _ = dnsmessage.NewName("wrong.invalid.") }, func(m *dnsmessage.Message) { m.Answers[0].Body = &dnsmessage.AResource{A: [4]byte{192, 0, 2, 77}} }} {
		m := baseline
		m.Answers = append([]dnsmessage.Resource{}, baseline.Answers...)
		mutate(&m)
		body, _ := m.Pack()
		if _, err := readOutcome(body, q, 42, "tcp"); err == nil {
			t.Fatal("failure/foreign response became a measured positive or denial")
		}
	}
	baseline.Answers[0].Body = &dnsmessage.AResource{}
	blocked, _ := baseline.Pack()
	if result, err := readOutcome(blocked, q, 42, "udp"); err != nil || result.Address != "0.0.0.0" {
		t.Fatal("explicit null-IP refusal was lost", result, err)
	}
	if _, _, err := reply(nonce, make([]byte, frameLimit+1)); err == nil {
		t.Fatal("oversized input accepted")
	}
}

func TestFramesAndTargetBounds(t *testing.T) {
	for _, size := range []int{0, frameLimit + 1, 65535} {
		var prefix [2]byte
		binary.BigEndian.PutUint16(prefix[:], uint16(size))
		if _, err := readFrame(bytes.NewReader(prefix[:])); err == nil {
			t.Fatal("unbounded TCP frame accepted", size)
		}
	}
	var frame bytes.Buffer
	if err := writeFrame(&frame, []byte("bounded")); err != nil {
		t.Fatal(err)
	}
	if body, err := readFrame(&frame); err != nil || string(body) != "bounded" {
		t.Fatal(err)
	}
	for _, target := range []string{"example.com:53", "127.0.0.1:53", "192.0.2.53:53", "172.20.0.2:5353", "[fd00::2]:53", "172.20.0.2:53/path"} {
		if _, err := query(context.Background(), "012345abcdef", target, "udp", "A", "neutral-012345abcdef.invalid", 1); err == nil || !strings.Contains(err.Error(), "target") {
			t.Fatal("non-owned classic IPv4 target accepted", target, err)
		}
	}
}
