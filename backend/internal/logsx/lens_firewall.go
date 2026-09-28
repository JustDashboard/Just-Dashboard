package logsx

import "strings"

// The firewall lens reads the packet lines the kernel writes for a LOG rule:
// UFW's "[UFW BLOCK] IN=ens3 OUT= … SRC=… DST=… PROTO=TCP SPT=… DPT=…",
// firewalld's "filter_IN_public_REJECT: IN=…" and a hand-written iptables
// rule's own prefix. They are one format behind different prefixes, and the
// values in them — who knocked, on which port — are what the Firewall page
// groups by. The kernel lens hands its UFW and firewalld lines here, so
// kern.log and the kernel ring read them the same way ufw.log does.
func init() {
	register(&Lens{
		ID:     "firewall",
		Events: []string{"block", "allow", "audit", "limit"},
		Attrs:  []string{"client", "dst", "dpt", "spt", "proto", "iface", "flags", "len"},
		New: func() Reader {
			return ReaderFunc(func(l *Line) {
				firewallRead(l, sysStripUptime(sysEnvelope(l).text))
			})
		},
	})
}

// firewallRead reads one packet line's message and answers whether it was
// one. Every UFW line is LIMIT, BLOCK, ALLOW or AUDIT; any other LOG prefix is
// judged by the verdict word in it, and one with none still gets its values.
func firewallRead(l *Line, msg string) bool {
	var event, body string
	if strings.HasPrefix(msg, "[UFW ") {
		end := strings.IndexByte(msg, ']')
		if end < 0 {
			return false
		}
		switch action := msg[len("[UFW "):end]; {
		case strings.HasPrefix(action, "LIMIT"):
			event = "limit"
		case strings.HasPrefix(action, "AUDIT"):
			event = "audit"
		case action == "ALLOW":
			event = "allow"
		default:
			event = "block"
		}
		body = msg[end+1:]
	} else {
		// A LOG rule's prefix is free text ending where the packet starts.
		// Requiring SRC= as well keeps "IN=" in a driver's prose from
		// counting as a packet.
		at := strings.Index(msg, "IN=")
		if at < 0 || (at > 0 && msg[at-1] != ' ') || !strings.Contains(msg[at:], " SRC=") {
			return false
		}
		event, body = firewallVerdict(msg[:at]), msg[at:]
	}
	firewallPacket(l, body)
	if event == "" {
		return true
	}
	l.Event = event
	// A blocked scan is the ordinary business of a firewall on a public
	// address, and ufw logs it at warning; only a rate limit tripping is news.
	if event == "limit" {
		l.SetLevel("warn")
	} else {
		l.SetLevel("info")
	}
	return true
}

// firewallVerdict reads the outcome out of a LOG prefix such as firewalld's
// "filter_IN_public_REJECT:" or "FINAL_REJECT:", or an operator's own
// "[iptables DROP] ".
func firewallVerdict(prefix string) string {
	prefix = strings.ToUpper(prefix)
	switch {
	case strings.Contains(prefix, "LIMIT"):
		return "limit"
	case strings.Contains(prefix, "REJECT"), strings.Contains(prefix, "DROP"),
		strings.Contains(prefix, "BLOCK"), strings.Contains(prefix, "DENY"):
		return "block"
	case strings.Contains(prefix, "ACCEPT"), strings.Contains(prefix, "ALLOW"):
		return "allow"
	case strings.Contains(prefix, "AUDIT"):
		return "audit"
	}
	return ""
}

// firewallPacket reads the packet's KEY=value tokens and bare TCP flags. The
// first LEN is the IP length; UDP repeats LEN for its own header, and that
// second one is not the packet's size. The interface is the inbound one, or
// the outbound one for traffic the host itself sent.
//
// ufw.log is nothing but these lines, so the packet is copied once and every
// value is a slice of that copy: one allocation where SetAttr would make one
// per value, and the copy is the packet, not the line, so a tally keeping a
// value still pins nothing large. The kernel prints the flags side by side,
// which lets them be one slice too.
func firewallPacket(l *Line, body string) {
	body = strings.Clone(body)
	var in, out string
	flagsFrom, flagsTo := -1, -1
	seenLen := false
	for at := 0; at < len(body); {
		if body[at] == ' ' {
			at++
			continue
		}
		end := strings.IndexByte(body[at:], ' ')
		if end < 0 {
			end = len(body)
		} else {
			end += at
		}
		token := body[at:end]
		eq := strings.IndexByte(token, '=')
		if eq < 0 {
			switch token {
			case "SYN", "ACK", "FIN", "RST", "PSH", "URG", "ECE", "CWR":
				if flagsFrom < 0 {
					flagsFrom = at
				}
				flagsTo = end
			}
			at = end
			continue
		}
		value := token[eq+1:]
		switch token[:eq] {
		case "IN":
			in = value
		case "OUT":
			out = value
		case "SRC":
			sysSetShared(l, "client", value)
		case "DST":
			sysSetShared(l, "dst", value)
		case "LEN":
			if !seenLen {
				sysSetShared(l, "len", value)
				seenLen = true
			}
		case "PROTO":
			sysSetShared(l, "proto", value)
		case "SPT":
			sysSetShared(l, "spt", value)
		case "DPT":
			sysSetShared(l, "dpt", value)
		}
		at = end
	}
	if in != "" {
		sysSetShared(l, "iface", in)
	} else {
		sysSetShared(l, "iface", out)
	}
	if flagsFrom >= 0 {
		sysSetShared(l, "flags", body[flagsFrom:flagsTo])
	}
}
