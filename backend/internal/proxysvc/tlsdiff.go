package proxysvc

import (
	"fmt"
	"strings"
)

// ScanChange is one way a scan differs from the scan of the same target
// before it. Area groups them on the page; Before and After are what each
// scan said, "" where it said nothing.
type ScanChange struct {
	Area   string `json:"area"`
	Title  string `json:"title"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// DiffScans lists what changed from prev to cur, in the order an operator
// asks it: is it still up, is it the same certificate, did the grade move,
// then protocols and headers. A scan that reached nothing has no certificate,
// protocols or headers to compare, so only reachability and the grade are
// compared against it — "every header vanished" would be noise.
func DiffScans(prev, cur *TLSScan) []ScanChange {
	changes := []ScanChange{}
	if prev == nil || cur == nil {
		return changes
	}
	add := func(area, title, before, after string) {
		if before != after {
			changes = append(changes, ScanChange{Area: area, Title: title, Before: before, After: after})
		}
	}
	reach := func(s *TLSScan) string {
		if s.Reachable {
			return "answered"
		}
		if s.Failure != nil && s.Failure.Stage != "" {
			return "failed at " + s.Failure.Stage
		}
		return "no answer"
	}
	add("reachability", "Handshake", reach(prev), reach(cur))
	add("grade", "Grade", prev.Grade, cur.Grade)
	if !prev.Reachable || !cur.Reachable {
		return changes
	}

	if prev.Fingerprint != cur.Fingerprint {
		add("certificate", "Certificate replaced", certWords(prev), certWords(cur))
	}
	add("certificate", "Trust", trustWord(prev), trustWord(cur))
	add("certificate", "Key", keyWords(prev), keyWords(cur))

	add("protocols", "Negotiated", prev.Negotiated, cur.Negotiated)
	before := map[string]string{}
	for _, p := range prev.Protocols {
		before[p.Name] = p.Status
	}
	for _, p := range cur.Protocols {
		if was, ok := before[p.Name]; ok {
			add("protocols", p.Name, was, p.Status)
		}
	}

	if prev.HTTP == nil || cur.HTTP == nil || prev.HTTP.Service != "http" || cur.HTTP.Service != "http" {
		return changes
	}
	add("headers", "Strict-Transport-Security", hstsWords(prev.HTTP.HSTS), hstsWords(cur.HTTP.HSTS))
	headers := map[string]string{}
	for _, h := range prev.HTTP.Headers {
		headers[h.Name] = headerWords(h)
	}
	for _, h := range cur.HTTP.Headers {
		if was, ok := headers[h.Name]; ok && h.Name != "Strict-Transport-Security" {
			add("headers", h.Name, was, headerWords(h))
		}
	}
	add("headers", "Port 80", redirectWords(prev.HTTP), redirectWords(cur.HTTP))
	return changes
}

func certWords(s *TLSScan) string {
	if s.Certificate == nil {
		return "none"
	}
	return fmt.Sprintf("%s, until %s", s.Certificate.Issuer, s.Certificate.NotAfter.UTC().Format("2006-01-02"))
}

func trustWord(s *TLSScan) string {
	switch {
	case !s.Trusted:
		return "untrusted"
	case !s.ChainComplete:
		return "trusted, intermediate missing"
	default:
		return "trusted"
	}
}

func keyWords(s *TLSScan) string {
	if s.KeyType == "" {
		return ""
	}
	return fmt.Sprintf("%s %d", s.KeyType, s.KeyBits)
}

func hstsWords(h *HSTS) string {
	if h == nil {
		return "absent"
	}
	words := []string{fmt.Sprintf("max-age=%d", h.MaxAge)}
	if h.IncludeSubDomains {
		words = append(words, "includeSubDomains")
	}
	if h.Preload {
		words = append(words, "preload")
	}
	return strings.Join(words, "; ")
}

func headerWords(h HeaderCheck) string {
	if !h.Present {
		return "absent"
	}
	return h.Value
}

func redirectWords(h *HTTPScan) string {
	if h.PlainRedirects {
		return "redirects to HTTPS"
	}
	return "does not redirect"
}
