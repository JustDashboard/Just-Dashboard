package proxysvc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// What a stream carried, read from its own access log.
//
// nginx's stream module writes one line per finished session, and nothing
// else on the host knows who connected to a forwarded port, how many were
// turned away by its access list, or how long they stayed. A stream that
// logs gets a file of its own under /var/log/nginx — which Debian's
// logrotate already rotates — in a format this file declares and reads back.
//
// The format is declared in each stream's own file, under a name built from
// the stream's, rather than once in a shared file: a paused or copied stream
// then carries everything it needs, and there is no second managed file for
// the listing to hide or a delete to leave behind. $msec rather than
// $time_local, because a Unix time needs no month names or zone to read.

// streamLogDir is where stream logs go: nginx's own log directory, which
// logrotate's nginx rule covers with its *.log pattern.
var streamLogDir = "/var/log/nginx"

// streamLogFormat is the line each session leaves. The upstream is quoted
// because a retried session names every server it tried, comma-separated.
const streamLogFormat = `$msec $remote_addr $protocol $status $bytes_sent $bytes_received $session_time "$upstream_addr" $server_port`

// streamLogFormatName is the stream's log_format. Formats share one namespace
// in the stream context, so the name is built from the same ident as the
// upstream and the zones.
func streamLogFormatName(name string) string { return NginxIdent(name) + "_log" }

// StreamLogPath is the file a stream logs its sessions to.
func StreamLogPath(name string) string {
	return filepath.Join(streamLogDir, "stream-"+name+".log")
}

// The budgets a reading may spend on a log, from its end: a stream's detail
// reads up to 8 MiB — about a hundred thousand sessions — and each card of
// the listing 1 MiB, since the listing reads every stream's.
const (
	streamTrafficBudget = 8 << 20
	streamSummaryBudget = 1 << 20
	streamTopClients    = 12
	maxStreamSessions   = 500
)

// streamWindows are the spans a traffic reading covers, with how many
// buckets its timeline has.
var streamWindows = map[string]struct {
	span    time.Duration
	buckets int
}{
	"1h":  {time.Hour, 12},
	"24h": {24 * time.Hour, 24},
	"7d":  {7 * 24 * time.Hour, 28},
}

// ErrStreamWindow is a traffic window this does not offer.
var ErrStreamWindow = errors.New("the window must be 1h, 24h or 7d")

// StreamTraffic is what one stream carried over a window.
type StreamTraffic struct {
	Name string `json:"name"`
	// Logging is whether the stream's file logs sessions now. A stream that
	// stopped logging still shows what its log holds.
	Logging bool   `json:"logging"`
	Path    string `json:"path"`
	Window  string `json:"window"`
	Since   int64  `json:"since"`
	Until   int64  `json:"until"`
	// Complete is whether every session logged in the window was read: false
	// when the reading's budget ran out, or a compressed rotation holds the
	// rest, before the window's start. Every figure is then a floor.
	Complete bool `json:"complete"`
	// Oldest is the first session read, in Unix seconds; absent with none.
	Oldest   int64 `json:"oldest,omitempty"`
	Sessions int   `json:"sessions"`
	// Denied is sessions the access list turned away (status 403); Failed is
	// those nginx could not forward (5xx: no server answered, a limit hit).
	Denied int `json:"denied"`
	Failed int `json:"failed"`
	// BytesIn is what clients sent, BytesOut what they were sent.
	BytesIn  int64                `json:"bytesIn"`
	BytesOut int64                `json:"bytesOut"`
	Statuses map[string]int       `json:"statuses"`
	Buckets  []StreamTrafficSlice `json:"buckets"`
	// Duration is how long sessions lasted, in seconds.
	Duration StreamDurations `json:"duration"`
	Clients  []StreamClient  `json:"clients"`
	// Skipped is lines in the window's reach that are not in the format:
	// written by a hand-made access_log, or cut by a crash.
	Skipped int `json:"skipped"`
	// Unreadable is why the log could not be read; absent when it could, or
	// when there is none yet.
	Unreadable string `json:"unreadable,omitempty"`
}

// StreamTrafficSlice is one bucket of the timeline, from Start (Unix seconds).
type StreamTrafficSlice struct {
	Start    int64 `json:"start"`
	Sessions int   `json:"sessions"`
	Denied   int   `json:"denied"`
	Failed   int   `json:"failed"`
	Bytes    int64 `json:"bytes"`
}

// StreamDurations are session lengths, in seconds.
type StreamDurations struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// StreamClient is one client address and what it did.
type StreamClient struct {
	Address  string `json:"address"`
	Sessions int    `json:"sessions"`
	Denied   int    `json:"denied"`
	Failed   int    `json:"failed"`
	Bytes    int64  `json:"bytes"`
	Last     int64  `json:"last"`
}

// StreamTrafficSummary is a card's reading: the last hour.
type StreamTrafficSummary struct {
	Sessions int   `json:"sessions"`
	Denied   int   `json:"denied"`
	Bytes    int64 `json:"bytes"`
	Complete bool  `json:"complete"`
}

// streamLogLine is one session as the format writes it.
type streamLogLine struct {
	at       float64
	client   string
	status   int
	sent     int64
	received int64
	seconds  float64
}

// parseStreamLogLine reads one line of streamLogFormat.
func parseStreamLogLine(line string) (streamLogLine, bool) {
	quote := strings.IndexByte(line, '"')
	if quote < 0 {
		return streamLogLine{}, false
	}
	head := strings.Fields(line[:quote])
	if len(head) != 7 {
		return streamLogLine{}, false
	}
	var out streamLogLine
	var err error
	if out.at, err = strconv.ParseFloat(head[0], 64); err != nil {
		return out, false
	}
	out.client = head[1]
	if out.status, err = strconv.Atoi(head[3]); err != nil {
		return out, false
	}
	if out.sent, err = strconv.ParseInt(head[4], 10, 64); err != nil {
		return out, false
	}
	if out.received, err = strconv.ParseInt(head[5], 10, 64); err != nil {
		return out, false
	}
	if out.seconds, err = strconv.ParseFloat(head[6], 64); err != nil {
		return out, false
	}
	return out, true
}

// readLogTail is the last budget bytes of a file as lines, oldest first, and
// whether they reach its first byte. A line the cut falls inside is dropped.
func readLogTail(path string, budget int64) (lines []string, whole bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := max(st.Size()-budget, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	b, err := io.ReadAll(io.LimitReader(f, budget))
	if err != nil {
		return nil, false, err
	}
	if start > 0 {
		cut := bytes.IndexByte(b, '\n')
		if cut < 0 {
			return nil, false, nil
		}
		b = b[cut+1:]
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, start == 0, nil
}

// readStreamLog is every session a log and its first rotation hold from
// since on, newest file last read, within budget bytes; and whether that is
// all of them.
func readStreamLog(path string, since time.Time, budget int64) (sessions []streamLogLine, skipped int, complete bool, err error) {
	cutoff := float64(since.UnixNano()) / 1e9
	for i, file := range []string{path, path + ".1"} {
		lines, whole, readErr := readLogTail(file, budget)
		if errors.Is(readErr, os.ErrNotExist) {
			// Nothing older was logged, so what was read is everything.
			return sessions, skipped, true, nil
		}
		if readErr != nil {
			if i == 0 {
				return nil, 0, false, readErr
			}
			return sessions, skipped, false, nil
		}
		var inWindow []streamLogLine
		reached := false
		for _, line := range lines {
			entry, ok := parseStreamLogLine(line)
			switch {
			case !ok:
				skipped++
			case entry.at < cutoff:
				reached = true
			default:
				inWindow = append(inWindow, entry)
			}
		}
		sessions = append(inWindow, sessions...)
		for _, l := range lines {
			budget -= int64(len(l)) + 1
		}
		if reached {
			return sessions, skipped, true, nil
		}
		if !whole || budget <= 0 {
			return sessions, skipped, false, nil
		}
	}
	// Both files were read whole and neither reached the window's start: a
	// compressed rotation may hold the rest.
	_, statErr := os.Stat(path + ".2.gz")
	return sessions, skipped, statErr != nil, nil
}

// StreamTraffic reads what one stream carried over a window ("1h", "24h" or
// "7d"; empty is the last hour), ending at now. nginx writes a session's line
// when it ends, so a session is counted in the bucket it ended in.
func (s *Service) StreamTraffic(ctx context.Context, name, window string, now time.Time) (*StreamTraffic, error) {
	if window == "" {
		window = "1h"
	}
	shape, ok := streamWindows[window]
	if !ok {
		return nil, ErrStreamWindow
	}
	spec, _, err := s.streamSpecByName(name)
	if err != nil {
		return nil, err
	}
	since := now.Add(-shape.span)
	out := &StreamTraffic{
		Name:     name,
		Logging:  spec.LogConnections,
		Path:     StreamLogPath(name),
		Window:   window,
		Since:    since.Unix(),
		Until:    now.Unix(),
		Statuses: map[string]int{},
		Buckets:  make([]StreamTrafficSlice, shape.buckets),
		Clients:  []StreamClient{},
	}
	step := shape.span / time.Duration(shape.buckets)
	for i := range out.Buckets {
		out.Buckets[i].Start = since.Add(time.Duration(i) * step).Unix()
	}
	sessions, skipped, complete, err := readStreamLog(out.Path, since, streamTrafficBudget)
	if err != nil {
		out.Unreadable = err.Error()
		return out, nil
	}
	out.Skipped, out.Complete = skipped, complete
	clients := map[string]*StreamClient{}
	lengths := make([]float64, 0, len(sessions))
	for _, entry := range sessions {
		if entry.at > float64(now.Unix()+1) {
			continue
		}
		if out.Oldest == 0 || int64(entry.at) < out.Oldest {
			out.Oldest = int64(entry.at)
		}
		denied, failed := entry.status == 403, entry.status >= 500
		out.Sessions++
		out.BytesIn += entry.received
		out.BytesOut += entry.sent
		out.Statuses[strconv.Itoa(entry.status)]++
		if denied {
			out.Denied++
		}
		if failed {
			out.Failed++
		}
		lengths = append(lengths, entry.seconds)
		i := int((entry.at - float64(since.UnixNano())/1e9) / step.Seconds())
		bucket := &out.Buckets[min(max(i, 0), shape.buckets-1)]
		bucket.Sessions++
		bucket.Bytes += entry.sent + entry.received
		if denied {
			bucket.Denied++
		}
		if failed {
			bucket.Failed++
		}
		c := clients[entry.client]
		if c == nil {
			c = &StreamClient{Address: entry.client}
			clients[entry.client] = c
		}
		c.Sessions++
		c.Bytes += entry.sent + entry.received
		c.Last = max(c.Last, int64(entry.at))
		if denied {
			c.Denied++
		}
		if failed {
			c.Failed++
		}
	}
	out.Duration = durations(lengths)
	for _, c := range clients {
		out.Clients = append(out.Clients, *c)
	}
	sort.Slice(out.Clients, func(i, j int) bool {
		a, b := out.Clients[i], out.Clients[j]
		if a.Sessions != b.Sessions {
			return a.Sessions > b.Sessions
		}
		return a.Address < b.Address
	})
	if len(out.Clients) > streamTopClients {
		out.Clients = out.Clients[:streamTopClients]
	}
	return out, nil
}

// durations are the nearest-rank percentiles of session lengths.
func durations(lengths []float64) StreamDurations {
	if len(lengths) == 0 {
		return StreamDurations{}
	}
	sort.Float64s(lengths)
	rank := func(p float64) float64 {
		i := int(math.Ceil(p*float64(len(lengths)))) - 1
		return lengths[min(max(i, 0), len(lengths)-1)]
	}
	return StreamDurations{P50: rank(0.5), P90: rank(0.9), P99: rank(0.99), Max: lengths[len(lengths)-1]}
}

// StreamTrafficSummaries is the last hour of every stream that logs, by
// name, for the listing's cards.
func (s *Service) StreamTrafficSummaries(ctx context.Context, now time.Time) (map[string]StreamTrafficSummary, error) {
	out := map[string]StreamTrafficSummary{}
	since := now.Add(-time.Hour)
	for _, dir := range []string{s.streamDir(), s.pausedStreamDir()} {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !streamFileName(e) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			p := parseStreamFile(e.Name(), string(b))
			if !p.spec.LogConnections {
				continue
			}
			sessions, _, complete, err := readStreamLog(StreamLogPath(p.spec.Name), since, streamSummaryBudget)
			if err != nil {
				continue
			}
			sum := StreamTrafficSummary{Complete: complete}
			for _, entry := range sessions {
				sum.Sessions++
				sum.Bytes += entry.sent + entry.received
				if entry.status == 403 {
					sum.Denied++
				}
			}
			out[p.spec.Name] = sum
		}
	}
	return out, nil
}

// streamSpecByName reads a stream's file, live or paused, as the form would.
func (s *Service) streamSpecByName(name string) (*StreamSpec, bool, error) {
	path, _, err := s.streamFile(name)
	paused := false
	if errors.Is(err, ErrStreamNotFound) {
		if pausedPath, pausedErr := s.pausedStreamFile(name); pausedErr == nil {
			path, paused, err = pausedPath, true, nil
		}
	}
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	p := parseStreamFile(name+".conf", string(b))
	return &p.spec, paused, nil
}

// StreamSessions is who is connected to a stream now.
type StreamSessions struct {
	Name   string `json:"name"`
	Listen int    `json:"listen"`
	// Paused is a stream nginx does not read, so nothing is connected.
	Paused bool `json:"paused,omitempty"`
	// UDPOnly is a stream with no TCP side: nginx answers every UDP client
	// from its one listening socket, so there is no socket per session to
	// list.
	UDPOnly  bool            `json:"udpOnly,omitempty"`
	Sessions []StreamSession `json:"sessions"`
	// Total is every TCP session found; Sessions holds the first 500.
	Total int `json:"total"`
}

// StreamSession is one established TCP connection to the stream's port.
type StreamSession struct {
	Client string `json:"client"`
	Local  string `json:"local"`
	PID    int32  `json:"pid,omitempty"`
}

// StreamSessions lists the TCP connections nginx has accepted on a stream's
// port. A socket whose owner is known and is not nginx is left out: another
// program on the same port number, on another address, is not the stream's.
func (s *Service) StreamSessions(ctx context.Context, name string) (*StreamSessions, error) {
	spec, paused, err := s.streamSpecByName(name)
	if err != nil {
		return nil, err
	}
	out := &StreamSessions{Name: name, Listen: spec.Listen, Paused: paused, Sessions: []StreamSession{}}
	if paused {
		return out, nil
	}
	if spec.Protocol == "udp" {
		out.UDPOnly = true
		return out, nil
	}
	conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return nil, err
	}
	var address net.IP
	if spec.Address != "" {
		address = net.ParseIP(spec.Address)
	}
	nginx := map[int32]bool{}
	for _, c := range conns {
		if c.Status != "ESTABLISHED" || c.Laddr.Port < uint32(spec.Listen) || c.Laddr.Port > uint32(streamLastPort(spec)) {
			continue
		}
		if address != nil && !address.Equal(net.ParseIP(c.Laddr.IP)) {
			continue
		}
		if c.Pid > 0 {
			is, known := nginx[c.Pid]
			if !known {
				if p, err := process.NewProcessWithContext(ctx, c.Pid); err == nil {
					name, _ := p.NameWithContext(ctx)
					is = name == "nginx"
				}
				nginx[c.Pid] = is
			}
			if !is {
				continue
			}
		}
		out.Total++
		if len(out.Sessions) < maxStreamSessions {
			out.Sessions = append(out.Sessions, StreamSession{
				Client: net.JoinHostPort(c.Raddr.IP, strconv.Itoa(int(c.Raddr.Port))),
				Local:  net.JoinHostPort(c.Laddr.IP, strconv.Itoa(int(c.Laddr.Port))),
				PID:    c.Pid,
			})
		}
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].Client < out.Sessions[j].Client })
	return out, nil
}

// streamLogDirective is the access_log a logging stream's server carries.
func streamLogDirective(name string) string {
	return fmt.Sprintf("access_log %s %s;", StreamLogPath(name), streamLogFormatName(name))
}
