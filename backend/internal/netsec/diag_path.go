package netsec

import (
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Structured readings of ping, traceroute, tracepath and `ip route get`. The
// tools' own text stays the evidence; these parse it into numbers a later run
// of the same request can be compared with, and into verdicts that do not
// over-claim: an unanswered probe is unknown, not down.

type pingReply struct {
	Seq int
	TTL int
	MS  float64
}

type pingStats struct {
	Address                 string
	Transmitted, Received   int
	Errors                  int
	Loss                    float64
	Min, Avg, Max, Mdev     float64
	HasRTT, HasMdev, Parsed bool
	Replies                 []pingReply
	ErrorLines              []string
}

var (
	pingAddress = regexp.MustCompile(`^PING \S+?\s*\(([0-9A-Fa-f:.]+)\)`)
	pingReplyRe = regexp.MustCompile(`bytes from .+?: (?:icmp_)?seq=(\d+) ttl=(\d+) time[=<]([\d.]+) ?ms`)
	pingSummary = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received(?:, \+(\d+) errors)?(?:, \+\d+ duplicates)?, ([\d.]+)% packet loss`)
	pingRTT     = regexp.MustCompile(`(?:rtt|round-trip) min/avg/max(/mdev|/stddev)? = ([\d.]+)/([\d.]+)/([\d.]+)(?:/([\d.]+))? ms`)
	pingFrom    = regexp.MustCompile(`^From (\S+?):? (?:icmp_seq=\d+ )?(.+)$`)
)

func parsePing(out string) pingStats {
	var st pingStats
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if m := pingAddress.FindStringSubmatch(line); m != nil && st.Address == "" {
			st.Address = m[1]
			continue
		}
		if m := pingReplyRe.FindStringSubmatch(line); m != nil {
			seq, _ := strconv.Atoi(m[1])
			ttl, _ := strconv.Atoi(m[2])
			ms, _ := strconv.ParseFloat(m[3], 64)
			st.Replies = append(st.Replies, pingReply{Seq: seq, TTL: ttl, MS: ms})
			continue
		}
		if m := pingSummary.FindStringSubmatch(line); m != nil {
			st.Parsed = true
			st.Transmitted, _ = strconv.Atoi(m[1])
			st.Received, _ = strconv.Atoi(m[2])
			st.Errors, _ = strconv.Atoi(m[3])
			st.Loss, _ = strconv.ParseFloat(m[4], 64)
			continue
		}
		if m := pingRTT.FindStringSubmatch(line); m != nil {
			st.HasRTT = true
			st.Min, _ = strconv.ParseFloat(m[2], 64)
			st.Avg, _ = strconv.ParseFloat(m[3], 64)
			st.Max, _ = strconv.ParseFloat(m[4], 64)
			if m[5] != "" {
				st.HasMdev = true
				st.Mdev, _ = strconv.ParseFloat(m[5], 64)
			}
			continue
		}
		if m := pingFrom.FindStringSubmatch(line); m != nil && len(st.ErrorLines) < 8 {
			st.ErrorLines = append(st.ErrorLines, m[1]+": "+m[2])
		}
	}
	return st
}

// replyJitter is the mean absolute difference between consecutive replies'
// round trips, the RFC 3550 idea without its smoothing. Fewer than two
// replies have no jitter to report.
func replyJitter(replies []pingReply) (float64, bool) {
	if len(replies) < 2 {
		return 0, false
	}
	total := 0.0
	for i := 1; i < len(replies); i++ {
		total += math.Abs(replies[i].MS - replies[i-1].MS)
	}
	return math.Round(total/float64(len(replies)-1)*1000) / 1000, true
}

func interpretPing(res *ProbeResult, st pingStats, target string, runErr error) {
	res.Limitations = append(res.Limitations,
		"ICMP echo is often rate-limited or filtered by hosts, providers and firewalls; its loss and latency can differ from TCP or UDP service traffic.")
	res.fact("Probes", "4 ICMP echo requests, 2 s per-reply wait, 12 s overall limit", BasisConfigured)
	if st.Address != "" {
		res.fact("Destination address", st.Address, BasisObserved)
	}
	if !st.Parsed || st.Transmitted == 0 {
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "ping did not send any echo request."
		if runErr != nil && res.Error == "" {
			res.Error = runErr.Error()
		}
		return
	}
	res.metric("sent", "Sent", float64(st.Transmitted), "")
	res.metric("received", "Received", float64(st.Received), "")
	res.metric("loss", "Packet loss", st.Loss, "%")
	if st.HasRTT {
		res.metric("rtt_min", "Minimum round trip", st.Min, "ms")
		res.metric("rtt_avg", "Average round trip", st.Avg, "ms")
		res.metric("rtt_max", "Maximum round trip", st.Max, "ms")
		if st.HasMdev {
			res.metric("rtt_mdev", "Round-trip deviation (mdev)", st.Mdev, "ms")
		}
	}
	jitter, hasJitter := replyJitter(st.Replies)
	if hasJitter {
		res.metric("jitter", "Jitter between consecutive replies", jitter, "ms")
	}
	if len(st.Replies) > 0 {
		table := ProbeTable{ID: "replies", Title: "Echo replies", Columns: []string{"Sequence", "TTL", "Round trip (ms)"}}
		for _, reply := range st.Replies {
			table.Rows = append(table.Rows, []string{strconv.Itoa(reply.Seq), strconv.Itoa(reply.TTL), formatFloat(reply.MS)})
		}
		res.Tables = append(res.Tables, table)
	}
	switch {
	case st.Received == 0 && len(st.ErrorLines) > 0:
		res.OK, res.Verdict, res.Error = false, ProbeFailed, ""
		res.Summary = "No echo replies; the path answered with ICMP errors instead (" + st.ErrorLines[0] + ")."
		res.finding("icmp-errors", "warning", "A router reported the destination unreachable",
			"ICMP error messages came back instead of echo replies: "+strings.Join(st.ErrorLines, "; ")+". This is evidence about routing toward the address, not about a particular service.", "Routing toward the destination")
	case st.Received == 0:
		// Exit status 1 is ping's "no reply", which is the reading itself
		// rather than a fault in running the tool.
		res.OK, res.Verdict, res.Error = false, ProbeUnknown, ""
		res.Summary = fmt.Sprintf("No ICMP echo replies to %d requests. Reachability is unknown; this does not show the host or its services are down.", st.Transmitted)
		res.finding("icmp-filtered", "notice", "ICMP echo may be filtered",
			"Many hosts, cloud security groups and firewalls drop ICMP echo while serving TCP normally. Check the service port before concluding the host is offline.", "Target firewall or network path")
		res.link("Check a TCP port on this target", toolsLink("port", target))
	case st.Loss > 0:
		res.OK, res.Verdict, res.Error = true, ProbeFindings, ""
		res.Summary = fmt.Sprintf("%d of %d replies (%s%% loss)", st.Received, st.Transmitted, formatFloat(st.Loss))
		if st.HasRTT {
			res.Summary += fmt.Sprintf(", average %s ms", formatFloat(st.Avg))
		}
		res.Summary += ". ICMP rate limiting can cause loss that service traffic does not see."
	default:
		res.OK, res.Verdict, res.Error = true, ProbeOK, ""
		res.Summary = fmt.Sprintf("%d of %d replies", st.Received, st.Transmitted)
		if st.HasRTT {
			res.Summary += fmt.Sprintf(", average %s ms", formatFloat(st.Avg))
		}
		if hasJitter {
			res.Summary += fmt.Sprintf(", jitter %s ms", formatFloat(jitter))
		}
		res.Summary += "."
	}
}

type traceHop struct {
	N       int
	Address string
	RTTs    []float64
	Stars   int
	Notes   []string
}

type traceReport struct {
	Destination string
	Hops        []traceHop
	LocalMTU    int
	PMTU        int
	PMTUHop     int
	Reached     bool
	Resume      bool
	TooMany     bool
	ResumeHops  int
}

var (
	traceHeader = regexp.MustCompile(`^traceroute6? to \S+ \(([0-9A-Fa-f:.]+)\)`)
	traceHopRe  = regexp.MustCompile(`^\s*(\d+)(\??):\s*(.*)$`)
	traceLine   = regexp.MustCompile(`^\s*(\d+)\s+(.*)$`)
	pmtuRe      = regexp.MustCompile(`pmtu (\d+)`)
	resumeRe    = regexp.MustCompile(`Resume: pmtu (\d+)(?: hops (\d+))?`)
)

// parseTraceroute reads `traceroute -n` output: one line per hop, each
// probe an address followed by its time, or a star.
func parseTraceroute(out string) traceReport {
	var r traceReport
	for _, line := range strings.Split(out, "\n") {
		if m := traceHeader.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			r.Destination = m[1]
			continue
		}
		m := traceLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		hop := traceHop{}
		hop.N, _ = strconv.Atoi(m[1])
		fields := strings.Fields(m[2])
		for i := 0; i < len(fields); i++ {
			field := fields[i]
			switch {
			case field == "*":
				hop.Stars++
			case strings.HasPrefix(field, "!"):
				hop.Notes = append(hop.Notes, tracerouteAnnotation(field))
			case field == "ms":
			default:
				if v, err := strconv.ParseFloat(field, 64); err == nil {
					hop.RTTs = append(hop.RTTs, v)
				} else if _, err := netip.ParseAddr(field); err == nil && hop.Address == "" {
					hop.Address = field
				}
			}
		}
		r.Hops = append(r.Hops, hop)
	}
	if n := len(r.Hops); n > 0 && r.Destination != "" && r.Hops[n-1].Address == r.Destination {
		r.Reached = true
	}
	return r
}

func tracerouteAnnotation(code string) string {
	switch {
	case code == "!H":
		return "host unreachable"
	case code == "!N":
		return "network unreachable"
	case code == "!P":
		return "protocol unreachable"
	case code == "!S":
		return "source route failed"
	case code == "!X":
		return "administratively prohibited"
	case strings.HasPrefix(code, "!F"):
		return "fragmentation needed"
	}
	return "ICMP " + strings.TrimPrefix(code, "!")
}

// parseTracepath reads `tracepath -n`: hops print once per probe, a pmtu note
// follows a router's "fragmentation needed", and Resume summarises the run.
func parseTracepath(out string) traceReport {
	var r traceReport
	index := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := resumeRe.FindStringSubmatch(trimmed); m != nil {
			r.Resume = true
			r.PMTU, _ = strconv.Atoi(m[1])
			if m[2] != "" {
				r.ResumeHops, _ = strconv.Atoi(m[2])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "Too many hops") {
			r.TooMany = true
			if m := pmtuRe.FindStringSubmatch(trimmed); m != nil {
				r.PMTU, _ = strconv.Atoi(m[1])
			}
			continue
		}
		m := traceHopRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		rest := m[3]
		if p := pmtuRe.FindStringSubmatch(rest); p != nil {
			v, _ := strconv.Atoi(p[1])
			if m[2] == "?" && strings.Contains(rest, "[LOCALHOST]") {
				r.LocalMTU = v
			} else {
				r.PMTUHop = n
			}
			r.PMTU = v
		}
		if m[2] == "?" {
			continue
		}
		i, seen := index[n]
		if !seen {
			r.Hops = append(r.Hops, traceHop{N: n})
			i = len(r.Hops) - 1
			index[n] = i
		}
		hop := &r.Hops[i]
		fields := strings.Fields(rest)
		if len(fields) >= 2 && fields[0] == "no" && fields[1] == "reply" {
			if hop.Address == "" {
				hop.Stars++
			}
			continue
		}
		for j, field := range fields {
			switch {
			case strings.HasSuffix(field, "ms"):
				if v, ok := parseMillis(field); ok {
					hop.RTTs = append(hop.RTTs, v)
				}
			case field == "reached":
				r.Reached = true
				hop.Notes = appendOnce(hop.Notes, "destination")
			case field == "asymm" && j+1 < len(fields):
				hop.Notes = appendOnce(hop.Notes, "asymmetric return path ("+fields[j+1]+" hops back)")
			default:
				if _, err := netip.ParseAddr(field); err == nil && hop.Address == "" {
					hop.Address = field
					hop.Stars = 0
				}
			}
		}
	}
	return r
}

func appendOnce(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

func traceTable(r traceReport) ProbeTable {
	table := ProbeTable{ID: "hops", Title: "Hops", Columns: []string{"Hop", "Address", "Round trip (ms)", "Note"}}
	for _, hop := range r.Hops {
		address, rtt := hop.Address, ""
		if address == "" {
			address = "no reply"
		}
		parts := make([]string, 0, len(hop.RTTs))
		for _, v := range hop.RTTs {
			parts = append(parts, formatFloat(v))
		}
		rtt = strings.Join(parts, " / ")
		table.Rows = append(table.Rows, []string{strconv.Itoa(hop.N), address, rtt, strings.Join(hop.Notes, "; ")})
	}
	return table
}

func traceRecords(r traceReport) []string {
	records := make([]string, 0, len(r.Hops))
	for _, hop := range r.Hops {
		address := hop.Address
		if address == "" {
			address = "no reply"
		}
		records = append(records, fmt.Sprintf("hop %d %s", hop.N, address))
	}
	return records
}

func interpretTrace(res *ProbeResult, r traceReport, tool, target string, runErr error) {
	if tool == "traceroute" {
		res.fact("Tool", "traceroute, one UDP probe per hop, up to 20 hops", BasisConfigured)
	} else {
		res.fact("Tool", "tracepath fallback (UDP, no root needed), up to 20 hops", BasisConfigured)
	}
	destination := r.Destination
	if destination == "" {
		if a, err := netip.ParseAddr(target); err == nil {
			destination = a.Unmap().String()
		}
	}
	if destination != "" {
		res.fact("Destination address", destination, BasisObserved)
		if !r.Reached && len(r.Hops) > 0 && r.Hops[len(r.Hops)-1].Address == destination {
			r.Reached = true
		}
	}
	res.Limitations = append(res.Limitations,
		"Each router answers from its own address and may rate-limit or deprioritise these replies; a silent or slow hop is not necessarily loss on the forward path.",
		"Paths can differ per flow (load balancing) and per direction; comparing runs shows changes, not their cause.")
	if len(r.Hops) == 0 {
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "No hops were reported."
		if runErr != nil && res.Error == "" {
			res.Error = runErr.Error()
		}
		return
	}
	res.Error = ""
	res.Tables = append(res.Tables, traceTable(r))
	res.Records = append(res.Records, traceRecords(r)...)
	responding := 0
	lastRTT := -1.0
	for _, hop := range r.Hops {
		if hop.Address != "" {
			responding++
			if len(hop.RTTs) > 0 {
				lastRTT = hop.RTTs[len(hop.RTTs)-1]
			}
		}
	}
	last := r.Hops[len(r.Hops)-1].N
	res.metric("hops", "Hops listed", float64(last), "")
	res.metric("responding_hops", "Hops that answered", float64(responding), "")
	reached := 0.0
	if r.Reached {
		reached = 1
	}
	res.metric("reached", "Destination answered", reached, "")
	if lastRTT >= 0 {
		res.metric("last_rtt", "Last answering hop round trip", lastRTT, "ms")
	}
	res.OK = true
	if r.Reached {
		res.Verdict = ProbeOK
		res.Summary = fmt.Sprintf("Reached the destination in %d hops; %d of %d hops answered.", last, responding, len(r.Hops))
		return
	}
	res.Verdict = ProbeUnknown
	res.Summary = fmt.Sprintf("The destination did not answer within %d hops; %d hops answered. Probes are often filtered near the destination, so this does not show it is unreachable.", last, responding)
	res.link("Check a TCP port on this target", toolsLink("port", target))
}

func interpretPathMTU(res *ProbeResult, r traceReport, target string, runErr error) {
	res.fact("Method", "tracepath: routers report a smaller MTU with ICMP fragmentation-needed / packet-too-big messages", BasisConfigured)
	if r.LocalMTU > 0 {
		res.fact("First-hop MTU on this host", strconv.Itoa(r.LocalMTU), BasisConfigured)
		res.metric("local_mtu", "First-hop MTU", float64(r.LocalMTU), "bytes")
	}
	res.Limitations = append(res.Limitations, "Path MTU depends on routers returning ICMP errors; filtered replies leave it unknown and can black-hole large packets.")
	if len(r.Hops) > 0 {
		res.Tables = append(res.Tables, traceTable(r))
		res.Records = append(res.Records, traceRecords(r)...)
	}
	answered := 0
	for _, hop := range r.Hops {
		if hop.Address != "" {
			answered++
		}
	}
	switch {
	case len(r.Hops) == 0 && r.PMTU == 0:
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "tracepath reported no hops."
		if runErr != nil && res.Error == "" {
			res.Error = runErr.Error()
		}
		return
	case r.Resume && r.Reached && !r.TooMany && r.PMTU > 0:
		res.OK, res.Verdict, res.Error = true, ProbeOK, ""
		hops := r.ResumeHops
		if hops == 0 {
			hops = len(r.Hops)
		}
		res.Summary = fmt.Sprintf("Path MTU %d bytes to the destination (%d hops).", r.PMTU, hops)
		res.fact("Path MTU", strconv.Itoa(r.PMTU)+" bytes", BasisObserved)
		res.metric("pmtu", "Path MTU", float64(r.PMTU), "bytes")
		res.Records = append(res.Records, "pmtu "+strconv.Itoa(r.PMTU))
	case answered == 0:
		res.OK, res.Verdict, res.Error = true, ProbeUnknown, ""
		res.Summary = "No router on the path answered; ICMP appears filtered, so the path MTU is unknown."
		res.fact("Path MTU", "unknown (no ICMP replies)", BasisUnknown)
	default:
		res.OK, res.Verdict, res.Error = true, ProbeUnknown, ""
		res.Summary = fmt.Sprintf("The destination was not reached. Up to hop %d the path carried %d-byte packets; beyond it the path MTU is unknown.", r.Hops[len(r.Hops)-1].N, r.PMTU)
		res.fact("Path MTU", fmt.Sprintf("unknown (at most %d observed before the destination)", r.PMTU), BasisUnknown)
	}
	if r.LocalMTU > 0 && r.PMTU > 0 && r.PMTU < r.LocalMTU {
		res.finding("smaller-mtu", "notice", fmt.Sprintf("A link on the path carries at most %d bytes", r.PMTU),
			fmt.Sprintf("This host's first hop uses %d. Tunnels and PPPoE commonly reduce the MTU; if fragmentation-needed messages are filtered elsewhere, large packets can stall.", r.LocalMTU), "Network path")
	}
}

type routeGet struct {
	Type, Destination, Gateway, Device, Source, Table, Protocol, Metric string
}

var routeTypes = map[string]bool{"unicast": true, "local": true, "broadcast": true, "unreachable": true, "prohibit": true, "blackhole": true, "multicast": true, "anycast": true, "throw": true, "nat": true}

// parseRouteGet reads the first line of `ip route get`, whose keys come in no
// guaranteed order.
func parseRouteGet(out string) (routeGet, bool) {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return routeGet{}, false
	}
	r := routeGet{Type: "unicast", Table: "main"}
	i := 0
	if routeTypes[fields[0]] {
		r.Type, i = fields[0], 1
	}
	if i >= len(fields) {
		return r, false
	}
	if _, err := netip.ParseAddr(fields[i]); err != nil {
		return r, false
	}
	r.Destination = fields[i]
	for i++; i < len(fields)-1; i++ {
		value := fields[i+1]
		switch fields[i] {
		case "via":
			r.Gateway = value
		case "dev":
			r.Device = value
		case "src":
			r.Source = value
		case "table":
			r.Table = value
		case "proto":
			r.Protocol = value
		case "metric":
			r.Metric = value
		default:
			continue
		}
		i++
	}
	return r, true
}

func interpretRoute(res *ProbeResult, out string, runErr error) {
	route, ok := parseRouteGet(out)
	if !ok {
		res.OK, res.Verdict = false, ProbeFailed
		if strings.Contains(strings.ToLower(out), "unreachable") {
			res.Summary = "The kernel has no usable route to this address."
		} else {
			res.Summary = "The kernel did not report a route."
		}
		if runErr != nil && res.Error == "" {
			res.Error = runErr.Error()
		}
		return
	}
	res.OK, res.Verdict, res.Error = true, ProbeOK, ""
	nextHop := "directly connected"
	if route.Gateway != "" {
		nextHop = route.Gateway
	}
	res.fact("Route type", route.Type, BasisObserved)
	res.fact("Next hop", nextHop, BasisObserved)
	if route.Device != "" {
		res.fact("Interface", route.Device, BasisObserved)
		res.Records = append(res.Records, "dev "+route.Device)
	}
	if route.Source != "" {
		res.fact("Selected source", route.Source, BasisObserved)
		res.Records = append(res.Records, "src "+route.Source)
	}
	if route.Gateway != "" {
		res.Records = append(res.Records, "via "+route.Gateway)
	}
	res.fact("Routing table", route.Table, BasisObserved)
	res.Records = append(res.Records, "table "+route.Table, "type "+route.Type)
	if route.Protocol != "" {
		res.fact("Route origin", route.Protocol, BasisObserved)
	}
	if route.Metric != "" {
		res.fact("Metric", route.Metric, BasisObserved)
	}
	switch route.Type {
	case "unicast":
		res.Summary = "The kernel sends this address out " + nonEmptyOr(route.Device, "an unnamed device") + " via " + nextHop
		if route.Source != "" {
			res.Summary += " from " + route.Source
		}
		res.Summary += "."
	case "local":
		res.Summary = "This address belongs to this host; traffic to it is delivered locally."
	default:
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "The selected route is of type " + route.Type + "; traffic to this address is not forwarded."
	}
	res.Limitations = append(res.Limitations, "The query uses the dashboard's UID, no mark and an unspecified source port; another application can select a different route.")
}

func nonEmptyOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
