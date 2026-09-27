package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Not everything worth proxying speaks HTTP.
//
// A Postgres replica reachable on one address, a game server, an SSH bastion,
// a syslog collector — nginx forwards all of them through its stream module,
// and Nginx Proxy Manager calls the feature "streams" because it is the one
// thing people ask for after proxy hosts. Without it a single-server operator
// with a non-HTTP service has to leave the dashboard and write nginx by hand,
// which is the thing the site builder exists to prevent.
//
// The stream block cannot live in a server file: `stream` is a top-level
// context, a sibling of `http`, so a file under sites-available is in the
// wrong tree entirely. This writes into a directory of its own and reports
// clearly when nginx.conf does not include it, because a stream config nginx
// never reads is the same failure as a drop-in it ignores.

// Where stream files go is Service.streamDir() — a directory of its own rather
// than conf.d, which nginx includes from *inside* the http block, and hung off
// the configured nginx directory rather than a hard-coded /etc/nginx.

// StreamSpec is one forwarded port.
type StreamSpec struct {
	Name string `json:"name"`
	// Listen is the port on this host.
	Listen int `json:"listen"`
	// Address is the one address to listen on. Empty is every address of
	// both families; 0.0.0.0 or :: is every address of one. It is kept so a
	// file listening on 127.0.0.1 alone is never saved back listening on
	// every interface.
	Address string `json:"address,omitempty"`
	// Protocol is tcp, udp, or both — one port taking TCP and UDP to the
	// same upstream, as DNS and many game servers do.
	Protocol string `json:"protocol"`
	// UDPMode is how nginx ends a UDP session. "session" keeps one per
	// client until it goes quiet, which is what a game server, WireGuard or
	// VoIP needs to see one peer rather than a new one per datagram;
	// "request" ends it at the first reply, which suits DNS. It leaves TCP
	// connections alone, and is empty for a TCP-only stream.
	UDPMode string `json:"udpMode,omitempty"`
	// Upstream is host:port, or unix:/path for a local socket.
	Upstream string `json:"upstream"`
	// ProxyProtocol prepends the PROXY header so the backend sees the real
	// client address. It has to be turned on at both ends or the backend
	// reads the header as the first bytes of the connection and fails in a
	// way that looks like a protocol mismatch.
	ProxyProtocol bool `json:"proxyProtocol"`
	// Timeout is the idle timeout: how long a connection may sit silent, in
	// seconds (proxy_timeout). Zero leaves nginx's ten minutes.
	Timeout int `json:"timeout,omitempty"`
	// ConnectTimeout is how long to wait for the upstream to accept, in
	// seconds (proxy_connect_timeout). Zero leaves nginx's minute. It is its
	// own setting because the idle timeout an SSH session needs is an hour,
	// and a dead upstream should not hang a client for that long.
	ConnectTimeout int `json:"connectTimeout,omitempty"`
	// AllowFrom restricts who may connect. There is no basic auth for a raw
	// TCP stream, so this is the only access control there is.
	AllowFrom []string `json:"allowFrom"`
}

// StreamEntry is one file in the stream directory as the page lists it.
type StreamEntry struct {
	StreamSpec
	Path string `json:"path"`
	// Managed marks a file this dashboard wrote.
	Managed bool `json:"managed"`
	// Open is true when anyone may connect: no access rules, or rules that
	// let everyone through. An allow list holding `all` restricts nothing.
	Open bool `json:"open"`
	// Unsupported names what the file does that the form cannot express.
	// The form will not save over such a file: it would drop them.
	Unsupported []string `json:"unsupported"`
	// Error is why the file could not be read.
	Error string `json:"error,omitempty"`
	// Link is where the file points when it is a symbolic link, as an
	// available/enabled layout links its streams in. The form does not save
	// over one — it would put a file of its own in the link's place — and a
	// delete removes the link, never what it points to.
	Link string `json:"link,omitempty"`
}

// StreamStatus reports whether nginx is set up to read these at all.
type StreamStatus struct {
	// Included reports that a top-level stream block includes our
	// directory. Without it the files are written and ignored.
	Included bool `json:"included"`
	// IncludedIn names where the directory is included instead, when that
	// is somewhere nginx reads the files as something other than streams.
	IncludedIn string `json:"includedIn,omitempty"`
	// IncludeError is why nginx.conf could not be read to tell.
	IncludeError string `json:"includeError,omitempty"`
	// Module is whether nginx can read a stream block at all. The snippet
	// below breaks nginx where it cannot.
	Module StreamModule `json:"module"`
	// Snippet is what to add to nginx.conf when it is not included.
	Snippet string        `json:"snippet"`
	Dir     string        `json:"dir"`
	Streams []StreamEntry `json:"streams"`
}

// StreamResult is what a save did.
type StreamResult struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	Content    string            `json:"content"`
	Warnings   []string          `json:"warnings"`
	Validation *ValidationResult `json:"validation,omitempty"`
	// Renamed is the name the stream had before this save renamed it. Its
	// file is kept beside the new one as <old>.conf.bak.
	Renamed  string `json:"renamed,omitempty"`
	Reloaded bool   `json:"reloaded"`
	// ReloadError is why nginx did not reload after the file passed its
	// test. The file stays: it is valid, and it takes effect at the next
	// reload that succeeds.
	ReloadError string `json:"reloadError,omitempty"`
	Output      string `json:"output,omitempty"`
}

var (
	// ErrStreamExists refuses a new stream, or a rename, onto a name that is
	// taken: without it the other stream's file was replaced in silence.
	ErrStreamExists = errors.New("a stream by that name already exists")
	// ErrStreamNotFound is a stream to edit or delete that has no file.
	ErrStreamNotFound = errors.New("no such stream")
)

// HandwrittenStreamError refuses to save the form over a file that does more
// than the form can say. Saving would drop every one of those things — a
// deny rule, a second upstream server, TLS — and a forwarding rule that
// quietly became wider than it was is the worst way this can fail.
type HandwrittenStreamError struct {
	Name        string
	Unsupported []string
}

func (e *HandwrittenStreamError) Error() string {
	return fmt.Sprintf("%s is written by hand and uses %s, which this form cannot keep — edit the file itself",
		e.Name, strings.Join(e.Unsupported, ", "))
}

var streamNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidateStream checks a spec before it becomes a file.
func ValidateStream(spec *StreamSpec) error {
	if !streamNameRe.MatchString(spec.Name) || isBackupFile(spec.Name) {
		return fmt.Errorf("name must be lowercase letters, digits, dots, dashes or underscores")
	}
	if spec.Listen < 1 || spec.Listen > 65535 {
		return fmt.Errorf("the listening port must be between 1 and 65535")
	}
	if spec.Address != "" {
		ip := net.ParseIP(strings.Trim(spec.Address, "[]"))
		if ip == nil {
			return fmt.Errorf("the listening address must be an IP address")
		}
		spec.Address = ip.String()
	}
	switch strings.ToLower(spec.Protocol) {
	case "tcp", "udp", "both":
		spec.Protocol = strings.ToLower(spec.Protocol)
	case "":
		spec.Protocol = "tcp"
	default:
		return fmt.Errorf("protocol must be tcp, udp or both")
	}
	switch {
	case spec.Protocol == "tcp":
		spec.UDPMode = ""
	case spec.UDPMode == "":
		spec.UDPMode = "session"
	case spec.UDPMode != "session" && spec.UDPMode != "request":
		return fmt.Errorf("the UDP mode must be session or request")
	}
	if err := validStreamUpstream(strings.TrimSpace(spec.Upstream)); err != nil {
		return err
	}
	spec.Upstream = strings.TrimSpace(spec.Upstream)
	if spec.Timeout < 0 || spec.Timeout > 86400 {
		return fmt.Errorf("the idle timeout must be between 0 and 86400 seconds")
	}
	if spec.ConnectTimeout < 0 || spec.ConnectTimeout > 86400 {
		return fmt.Errorf("the connect timeout must be between 0 and 86400 seconds")
	}
	for _, entry := range spec.AllowFrom {
		if err := validACLEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// validStreamUpstream accepts host:port and unix:/absolute/path, the two
// shapes a stream upstream server takes.
func validStreamUpstream(upstream string) error {
	if strings.ContainsAny(upstream, " \t\r\n;{}#\"'$\\") {
		return fmt.Errorf("the upstream contains characters that are not allowed")
	}
	if socket, ok := strings.CutPrefix(upstream, "unix:"); ok {
		if !filepath.IsAbs(socket) {
			return fmt.Errorf("a unix socket upstream needs an absolute path, like unix:/run/app.sock")
		}
		return nil
	}
	host, port, err := net.SplitHostPort(upstream)
	if err != nil {
		return fmt.Errorf("the upstream must look like 10.0.0.5:5432 or unix:/run/app.sock")
	}
	if host == "" {
		return fmt.Errorf("the upstream needs a host")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("the upstream port must be between 1 and 65535")
	}
	return nil
}

// streamUpstreamName is the stream's upstream block. Built by NginxIdent, so
// a-b and a_b are two upstreams — folding "-" to "_" alone gave them one, and
// nginx refused the second file with "duplicate upstream".
func streamUpstreamName(name string) string {
	return NginxIdent(name) + "_backend"
}

// RenderStream turns a spec into the nginx it means. Hand-written for the same
// reason the site renderer is: the file outlives this dashboard and somebody
// has to be able to read it.
func RenderStream(spec *StreamSpec) (string, error) {
	if err := ValidateStream(spec); err != nil {
		return "", err
	}
	upstream := streamUpstreamName(spec.Name)
	l := &lines{}
	l.add(managedMarker)
	l.add("# Stream: %s", spec.Name)
	l.add("# This belongs inside nginx's top-level stream block, not inside http.")
	l.blank()
	l.add("upstream %s {", upstream)
	l.add("    server %s;", spec.Upstream)
	l.add("}")
	l.blank()
	l.add("server {")
	for _, suffix := range streamListenSuffixes(spec.Protocol) {
		switch ip := net.ParseIP(spec.Address); {
		case spec.Address == "":
			l.add("    listen %d%s;", spec.Listen, suffix)
			l.add("    listen [::]:%d%s;", spec.Listen, suffix)
		case ip.To4() == nil:
			l.add("    listen [%s]:%d%s;", spec.Address, spec.Listen, suffix)
		default:
			l.add("    listen %s:%d%s;", spec.Address, spec.Listen, suffix)
		}
	}
	if len(spec.AllowFrom) > 0 {
		l.blank()
		l.add("    # A raw stream has no authentication of any kind, so this list")
		l.add("    # is the only thing deciding who may connect.")
		for _, entry := range spec.AllowFrom {
			l.add("    allow %s;", strings.TrimSpace(entry))
		}
		l.add("    deny all;")
	}
	l.blank()
	l.add("    proxy_pass %s;", upstream)
	if spec.ProxyProtocol {
		l.add("    # The backend must be configured to expect this header, or it")
		l.add("    # reads it as the first bytes of the connection.")
		l.add("    proxy_protocol on;")
	}
	if spec.ConnectTimeout > 0 {
		l.add("    proxy_connect_timeout %s;", nginxDuration(spec.ConnectTimeout))
	}
	if spec.Timeout > 0 {
		l.add("    proxy_timeout %s;", nginxDuration(spec.Timeout))
	}
	if spec.UDPMode == "request" {
		l.add("    # One reply ends a UDP session, so every query is a session of its")
		l.add("    # own. Without this a session lasts until proxy_timeout of silence.")
		l.add("    proxy_responses 1;")
	}
	l.add("}")
	return l.String(), nil
}

// streamListenSuffixes are the listen parameters each protocol needs: none
// for TCP, udp for UDP, and a listen of each kind for both.
func streamListenSuffixes(protocol string) []string {
	switch protocol {
	case "udp":
		return []string{" udp"}
	case "both":
		return []string{"", " udp"}
	}
	return []string{""}
}

// nginxDuration writes a positive number of seconds as nginx's time syntax,
// the way a person would: 600 as 10m and 5400 as 1h30m, not 600s and 5400s.
func nginxDuration(seconds int) string {
	out := ""
	for _, unit := range []struct {
		size int
		name string
	}{{3600, "h"}, {60, "m"}, {1, "s"}} {
		if n := seconds / unit.size; n > 0 {
			out += strconv.Itoa(n) + unit.name
			seconds -= n * unit.size
		}
	}
	return out
}

// parsedStream is a stream file as the form sees it, plus what the listing
// and the port check need.
type parsedStream struct {
	spec        StreamSpec
	managed     bool
	open        bool
	unsupported []string
	binds       []bind
}

func (p *parsedStream) cannot(what string) {
	for _, have := range p.unsupported {
		if have == what {
			return
		}
	}
	p.unsupported = append(p.unsupported, what)
}

// ParseStreamSpec reads a stream file back so the form can edit it, with
// whether the dashboard wrote it and what in it the form cannot express.
//
// It reads the file as nginx does, token by token, so a one-line file, two
// directives on a line and a quoted argument all read correctly. Everything
// the form would drop on save is named in unsupported rather than ignored:
// the old line reader dropped deny rules, extra upstream servers and every
// directive it did not know, and saving then wrote a wider forward than the
// one on disk.
func ParseStreamSpec(name, content string) (*StreamSpec, bool, []string) {
	p := parseStreamFile(name+".conf", content)
	return &p.spec, p.managed, p.unsupported
}

func parseStreamFile(fileName, content string) parsedStream {
	p := parsedStream{
		spec:        StreamSpec{Name: strings.TrimSuffix(fileName, ".conf"), Protocol: "tcp", AllowFrom: []string{}},
		managed:     strings.Contains(content, managedMarker),
		unsupported: []string{},
	}
	directives, err := ParseNginxFile(fileName, content, []string{"stream"})
	if err != nil {
		p.cannot("a syntax error (" + err.Error() + ")")
		return p
	}
	upstreams := map[string]Directive{}
	var servers []Directive
	for _, d := range directives {
		switch {
		case d.Name == "upstream" && d.Block != nil && len(d.Args) == 1:
			upstreams[d.Args[0]] = d
		case d.Name == "server" && d.Block != nil:
			servers = append(servers, d)
		default:
			p.cannot(d.Name)
		}
	}
	switch len(servers) {
	case 0:
		p.cannot("no server block")
		return p
	case 1:
	default:
		p.cannot(fmt.Sprintf("%d server blocks", len(servers)))
	}

	var rules []accessRule
	used := map[string]bool{}
	for _, d := range servers[0].Block {
		switch d.Name {
		case "listen":
			p.readListen(d.Args)
		case "proxy_pass":
			if len(d.Args) != 1 {
				p.cannot("proxy_pass")
				continue
			}
			p.readProxyPass(d.Args[0], upstreams, used)
		case "allow", "deny":
			if len(d.Args) != 1 {
				p.cannot(d.Name)
				continue
			}
			rules = append(rules, accessRule{allow: d.Name == "allow", source: d.Args[0]})
		case "proxy_protocol":
			p.spec.ProxyProtocol = len(d.Args) == 1 && d.Args[0] == "on"
		case "proxy_timeout", "proxy_connect_timeout":
			seconds, ok := streamSeconds(d.Args)
			if !ok {
				p.cannot(d.Name + " " + strings.Join(d.Args, " "))
				continue
			}
			if d.Name == "proxy_timeout" {
				p.spec.Timeout = seconds
			} else {
				p.spec.ConnectTimeout = seconds
			}
		case "proxy_responses":
			if len(d.Args) == 1 && d.Args[0] == "1" {
				p.spec.UDPMode = "request"
			} else {
				p.cannot("proxy_responses " + strings.Join(d.Args, " "))
			}
		default:
			p.cannot(d.Name)
		}
	}
	for name := range upstreams {
		if !used[name] {
			p.cannot("an upstream nothing uses")
		}
	}
	if p.spec.Upstream == "" {
		p.cannot("no proxy_pass")
	}
	p.reduceBinds()
	if p.spec.UDPMode == "request" && p.spec.Protocol == "tcp" {
		p.cannot("proxy_responses on TCP")
	}
	if p.spec.Protocol != "tcp" && p.spec.UDPMode == "" {
		p.spec.UDPMode = "session"
	}
	p.readAccess(rules)
	return p
}

// readListen takes one listen line. Only the shapes RenderStream writes are
// the form's; anything else — a port range, a host name, ssl, reuseport — is
// named, and its socket is still read so a port check sees it.
func (p *parsedStream) readListen(args []string) {
	if len(args) == 0 {
		p.cannot("listen")
		return
	}
	b := bind{}
	for _, param := range args[1:] {
		if param == "udp" {
			b.udp = true
		} else {
			p.cannot("listen option " + param)
		}
	}
	addr, port := "", args[0]
	switch {
	case strings.HasPrefix(port, "unix:"):
		p.cannot("a unix socket listener")
		return
	case strings.HasPrefix(port, "["):
		end := strings.Index(port, "]")
		if end < 0 || !strings.HasPrefix(port[end+1:], ":") {
			p.cannot("listen " + args[0])
			return
		}
		addr, port = port[1:end], port[end+2:]
	case strings.Contains(port, ":"):
		i := strings.LastIndex(port, ":")
		addr, port = port[:i], port[i+1:]
	}
	if addr == "" || addr == "*" {
		addr = "0.0.0.0"
	}
	if strings.Contains(port, "-") {
		p.cannot("a port range")
		return
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		p.cannot("listen " + args[0])
		return
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		p.cannot("a host name in listen")
		return
	}
	b.addr, b.port = ip.String(), n
	for _, have := range p.binds {
		if have == b {
			return
		}
	}
	p.binds = append(p.binds, b)
}

// reduceBinds folds the sockets into the spec's one port, one protocol and
// one address — the dashboard's pair of wildcards reading as "every address",
// and TCP and UDP listens on the same addresses reading as both.
func (p *parsedStream) reduceBinds() {
	if len(p.binds) == 0 {
		p.cannot("no listen")
		return
	}
	first := p.binds[0]
	p.spec.Listen, p.spec.Address = first.port, first.addr
	addrs := map[bool]map[string]bool{}
	for _, b := range p.binds {
		if b.port != first.port {
			p.cannot("several listen ports")
		}
		if addrs[b.udp] == nil {
			addrs[b.udp] = map[string]bool{}
		}
		addrs[b.udp][b.addr] = true
	}
	tcp, udp := addrs[false], addrs[true]
	switch {
	case tcp != nil && udp != nil:
		p.spec.Protocol = "both"
		if !maps.Equal(tcp, udp) {
			p.cannot("TCP and UDP on different addresses")
		}
	case udp != nil:
		p.spec.Protocol = "udp"
	}
	own := addrs[first.udp]
	switch {
	case len(own) == 2 && own["0.0.0.0"] && own["::"]:
		p.spec.Address = ""
	case len(own) > 1:
		p.cannot("several listen addresses")
	}
}

// readProxyPass takes the target either as an upstream block in this file,
// whose one plain server is the form's upstream, or as an address written
// straight into proxy_pass, which means the same.
func (p *parsedStream) readProxyPass(target string, upstreams map[string]Directive, used map[string]bool) {
	if strings.Contains(target, "$") {
		p.cannot("proxy_pass with a variable")
		return
	}
	block, ok := upstreams[target]
	if !ok {
		if !strings.HasPrefix(target, "unix:") && !strings.Contains(target, ":") {
			p.cannot("an upstream defined in another file")
		}
		p.spec.Upstream = target
		return
	}
	used[target] = true
	var servers []Directive
	for _, d := range block.Block {
		if d.Name == "server" && len(d.Args) > 0 {
			servers = append(servers, d)
		} else {
			p.cannot(d.Name)
		}
	}
	if len(servers) == 0 {
		p.cannot("an empty upstream")
		return
	}
	p.spec.Upstream = servers[0].Args[0]
	if len(servers) > 1 {
		p.cannot(fmt.Sprintf("%d upstream servers", len(servers)))
	}
	for _, s := range servers {
		if len(s.Args) > 1 {
			p.cannot("upstream server options")
		}
	}
}

type accessRule struct {
	allow  bool
	source string
}

// readAccess turns allow and deny lines into the form's allow list where
// that says the same thing, and works out who may connect either way.
//
// nginx takes the first rule that matches and lets through anyone none
// matches, so: an allow list closed by `deny all` is the form's list; rules
// that reach `allow all` before any deny let everyone in and are the form's
// empty list; anything else — a deny list, allows with no `deny all` behind
// them — is kept by hand.
func (p *parsedStream) readAccess(rules []accessRule) {
	p.open = rulesAdmitEveryone(rules)
	var allows []string
	onlyAllows := true
	for i, r := range rules {
		if r.allow && r.source == "all" && onlyAllows {
			return
		}
		if r.allow {
			allows = append(allows, r.source)
			continue
		}
		if i == len(rules)-1 && r.source == "all" && len(allows) > 0 && onlyAllows {
			p.spec.AllowFrom = allows
			return
		}
		onlyAllows = false
	}
	if len(rules) == 0 {
		return
	}
	p.spec.AllowFrom = append([]string{}, allows...)
	if onlyAllows {
		p.cannot("allow rules with no deny all after them")
	} else {
		p.cannot("deny rules")
	}
}

// rulesAdmitEveryone walks the rules as nginx does: an allow of everyone
// before any deny of everyone opens the port, a deny of everyone closes it,
// and running off the end lets the client in.
func rulesAdmitEveryone(rules []accessRule) bool {
	deniedV4, deniedV6 := false, false
	for _, r := range rules {
		v4 := r.source == "all" || r.source == "0.0.0.0/0"
		v6 := r.source == "all" || r.source == "::/0"
		if r.allow && (v4 || v6) {
			return true
		}
		if !r.allow {
			deniedV4 = deniedV4 || v4
			deniedV6 = deniedV6 || v6
			if deniedV4 && deniedV6 {
				return false
			}
		}
	}
	return true
}

// streamOpen is whether anyone may connect to a spec's port.
func streamOpen(spec *StreamSpec) bool {
	if len(spec.AllowFrom) == 0 {
		return true
	}
	rules := make([]accessRule, 0, len(spec.AllowFrom)+1)
	for _, entry := range spec.AllowFrom {
		rules = append(rules, accessRule{allow: true, source: strings.TrimSpace(entry)})
	}
	return rulesAdmitEveryone(append(rules, accessRule{source: "all"}))
}

// streamSeconds reads an nginx time — "90", "10m", "1h30m" — in whole
// seconds. A value with milliseconds in it, or past a day, is not one the
// form can hold.
func streamSeconds(args []string) (int, bool) {
	if len(args) != 1 {
		return 0, false
	}
	ms, ok := parseNginxDuration(args[0])
	if !ok || ms%1000 != 0 || ms/1000 > 86400 {
		return 0, false
	}
	return int(ms / 1000), true
}

// parseNginxDuration reads nginx's time syntax into milliseconds: numbers
// each followed by a unit — ms, s, m, h, d, w, M, y — or bare seconds.
func parseNginxDuration(value string) (int64, bool) {
	units := map[string]int64{
		"ms": 1, "s": 1000, "m": 60_000, "h": 3_600_000, "d": 86_400_000,
		"w": 7 * 86_400_000, "M": 30 * 86_400_000, "y": 365 * 86_400_000,
	}
	if value == "" {
		return 0, false
	}
	var total int64
	for value != "" {
		i := 0
		for i < len(value) && value[i] >= '0' && value[i] <= '9' {
			i++
		}
		if i == 0 || i > 12 {
			return 0, false
		}
		n, _ := strconv.ParseInt(value[:i], 10, 64)
		value = value[i:]
		unit := "s"
		if strings.HasPrefix(value, "ms") {
			unit, value = "ms", value[2:]
		} else if value != "" {
			unit, value = value[:1], value[1:]
		}
		scale, ok := units[unit]
		if !ok {
			return 0, false
		}
		total += n * scale
	}
	return total, true
}

// streamFileName is a file nginx would read from the stream directory:
// *.conf, not a backup, and not a dotfile, which nginx's glob skips.
func streamFileName(e os.DirEntry) bool {
	name := e.Name()
	return !e.IsDir() && strings.HasSuffix(name, ".conf") && !isBackupFile(name) && !strings.HasPrefix(name, ".")
}

// Streams lists what is configured, and whether nginx is reading it.
//
// A stream directory that cannot be read is an error rather than an empty
// list: "nothing forwarded" is a claim, and permission denied is not
// evidence for it. A missing directory is the empty list — nothing has been
// written yet.
func (s *Service) Streams(ctx context.Context) (*StreamStatus, error) {
	dir := s.streamDir()
	status := &StreamStatus{
		Dir:     dir,
		Streams: []StreamEntry{},
		Snippet: "stream {\n    include " + dir + "/*.conf;\n}",
	}
	include := readStreamInclude(s.nginxDir, dir)
	status.Included, status.IncludedIn = include.included, include.misplaced
	if include.err != nil {
		status.IncludeError = include.err.Error()
	}
	status.Module = s.StreamModule(ctx)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if streamFileName(e) {
			status.Streams = append(status.Streams, listStream(dir, e))
		}
	}
	sort.SliceStable(status.Streams, func(i, j int) bool {
		return status.Streams[i].Listen < status.Streams[j].Listen
	})
	return status, nil
}

// streamLinked is what the form cannot keep about a stream that is a symbolic
// link: a save would put a file of its own in the link's place.
const streamLinked = "a symbolic link"

// listStream reads one file of the stream directory for the listing. A
// symbolic link is read through, as nginx reads it, and named as one; a link
// to nothing says so rather than calling its own name missing.
func listStream(dir string, e os.DirEntry) StreamEntry {
	path := filepath.Join(dir, e.Name())
	linked := e.Type()&os.ModeSymlink != 0
	link := ""
	if linked {
		link, _ = os.Readlink(path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		reason := err.Error()
		if linked && errors.Is(err, fs.ErrNotExist) {
			reason = fmt.Sprintf("it links to %s, which does not exist", link)
		}
		entry := StreamEntry{
			StreamSpec:  StreamSpec{Name: strings.TrimSuffix(e.Name(), ".conf"), Protocol: "tcp", AllowFrom: []string{}},
			Path:        path,
			Unsupported: []string{},
			Error:       reason,
			Link:        link,
		}
		if linked {
			entry.Unsupported = append(entry.Unsupported, streamLinked)
		}
		return entry
	}
	p := parseStreamFile(e.Name(), string(b))
	if linked {
		p.cannot(streamLinked)
	}
	return StreamEntry{
		StreamSpec: p.spec, Path: path, Managed: p.managed, Open: p.open, Unsupported: p.unsupported, Link: link,
	}
}

// streamFile is the file of an existing stream, found by the name the
// listing gave it. The listing shows any *.conf, so this takes any plain file
// name rather than only the names the form may create — a hand-made
// Upper.conf could otherwise be listed and never edited, renamed or deleted.
//
// The file may be a symbolic link, which linked reports: the listing shows
// one, and a link to nothing is the one file that breaks `nginx -t` for the
// whole host, so it has to be deletable from here. Anything else that is not
// a regular file is refused.
func (s *Service) streamFile(name string) (path string, linked bool, err error) {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") ||
		strings.ContainsAny(name, "/\\\x00") {
		return "", false, fmt.Errorf("invalid stream name")
	}
	path = filepath.Join(s.streamDir(), name+".conf")
	st, err := os.Lstat(path)
	if err != nil {
		return "", false, fmt.Errorf("%w: %s", ErrStreamNotFound, name)
	}
	linked = st.Mode()&os.ModeSymlink != 0
	if !linked && !st.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s is not a regular file — change it by hand", path)
	}
	return path, linked, nil
}

// ApplyStream writes a stream, tests the whole configuration and reloads.
//
// previous is the name the form opened on, empty for a new stream. A new
// stream, or a rename, onto a name that is taken is refused: the form posts
// both to one route, and without the check "New stream" — or renaming one
// stream to another's name — replaced the other's file in silence. A rename
// moves the old file to <old>.conf.bak before the test, so the two never
// claim one port, and puts both back if the test fails.
//
// A file doing more than the form can say is refused (HandwrittenStreamError),
// and so is a port another stream or another program already holds
// (PortInUseError): nginx -t passes both, and nginx then ignores the stream
// or fails the reload in a way the reload command does not report.
//
// A reload that fails after the test passed is reported in the result, not as
// an error: the file is written and valid, and "not applied" was untrue.
func (s *Service) ApplyStream(ctx context.Context, spec *StreamSpec, previous string, reload bool) (*StreamResult, error) {
	content, err := RenderStream(spec)
	if err != nil {
		return nil, err
	}
	// Read before the lock: walking /proc takes a while, and the lock holds
	// up every other save on the host.
	listeners, listenErr := readListeners(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.streamDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, spec.Name+".conf")
	var oldPath, before string
	var oldBinds []bind
	oldManaged := true
	if previous != "" {
		var linked bool
		if oldPath, linked, err = s.streamFile(previous); err != nil {
			return nil, err
		}
		if linked {
			return nil, &HandwrittenStreamError{Name: previous, Unsupported: []string{streamLinked}}
		}
		b, err := os.ReadFile(oldPath)
		if err != nil {
			return nil, err
		}
		before = string(b)
		old := parseStreamFile(previous+".conf", before)
		if len(old.unsupported) > 0 {
			return nil, &HandwrittenStreamError{Name: previous, Unsupported: old.unsupported}
		}
		oldBinds, oldManaged = old.binds, old.managed
	}
	if path != oldPath {
		if _, err := os.Lstat(path); err == nil {
			return nil, fmt.Errorf("%w: %s", ErrStreamExists, spec.Name)
		}
	}
	if err := streamPortConflict(dir, spec, oldPath, oldBinds, listeners); err != nil {
		return nil, err
	}

	res := &StreamResult{Name: spec.Name, Path: path, Content: content, Warnings: StreamWarnings(spec)}
	if listenErr != nil {
		res.Warnings = append(res.Warnings, "The host's listening sockets could not be read ("+listenErr.Error()+
			"), so a port another program holds was not checked for.")
	}
	if err := writeAtomic(path, content); err != nil {
		return nil, err
	}
	renamed := oldPath != "" && oldPath != path
	backup := oldPath + ".bak"
	earlierBackup, backupExisted := "", false
	if renamed {
		earlierBackup, backupExisted = readIfPresent(backup)
		if err := os.Rename(oldPath, backup); err != nil {
			os.Remove(path)
			return nil, err
		}
	}

	res.Validation = runValidator(ctx, "nginx", "-t")
	res.Validation.Note = s.includeNote(path)
	if !res.Validation.Valid {
		if renamed {
			os.Remove(path)
			os.Rename(backup, oldPath)
			restoreConfig(backup, earlierBackup, backupExisted)
		} else {
			restoreConfig(path, before, oldPath != "")
		}
		return res, ErrInvalidConf
	}
	if !renamed && oldPath != "" && !oldManaged {
		// The form rewrites a hand-written file in its own layout, comments
		// and all; the original is worth a file left behind.
		if err := os.WriteFile(backup, []byte(before), 0o644); err != nil {
			res.Warnings = append(res.Warnings, "The hand-written original could not be kept: "+err.Error())
		}
	}
	if renamed {
		res.Renamed = previous
		s.recordChange(ctx, Change{Path: oldPath, Action: ChangeDelete, Before: []byte(before), BeforeExisted: true})
		s.recordChange(ctx, Change{Path: path, Action: ChangeWrite, After: []byte(content)})
	} else {
		s.recordChange(ctx, Change{Path: path, Action: ChangeWrite,
			Before: []byte(before), BeforeExisted: oldPath != "", After: []byte(content)})
	}
	if reload {
		out, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
		res.Output = strings.TrimSpace(string(out))
		if err != nil {
			res.ReloadError = res.Output
			if res.ReloadError == "" {
				res.ReloadError = err.Error()
			}
		} else {
			res.Reloaded = true
		}
	}
	return res, nil
}

// StreamWarnings are the choices that are legal and probably not intended.
func StreamWarnings(spec *StreamSpec) []string {
	warnings := []string{}
	open := streamOpen(spec)
	if open {
		warnings = append(warnings,
			"A stream has no authentication of any kind — anything that can reach this port is through to the backend. Restrict the source unless the service behind it authenticates for itself.")
	}
	if preset, ok := streamDanger(spec.Listen); ok && open {
		warnings = append(warnings, preset)
	}
	if spec.Protocol != "tcp" && spec.ProxyProtocol {
		warnings = append(warnings,
			"On UDP, nginx puts the PROXY header in front of the first datagram of every session. Turn it on only if the backend expects it there — most UDP services read that datagram as garbage.")
	}
	return warnings
}

// streamDanger reuses the port catalogue's judgement, which is the same
// judgement whether the port is opened by the firewall or forwarded by nginx.
func streamDanger(port int) (string, bool) {
	switch port {
	case 5432, 3306, 6379, 27017, 11211, 9200, 2375:
		return "Forwarding a database or cache port to the internet is the same exposure as opening it in the firewall. Restrict the source.", true
	}
	return "", false
}

// StreamDeletion is what a delete did with the file.
type StreamDeletion struct {
	// Read is whether nginx reads the stream directory, so whether stopping
	// the stream needs a reload.
	Read bool
	// Backup is the .bak the content was kept in.
	Backup string
	// Link is where a removed symbolic link pointed. The link goes and what
	// it points to stays, so nothing needs keeping.
	Link string
	// Unread is why a file's content could not be kept. The file is removed
	// all the same: refusing for want of a backup left the one file that may
	// be breaking nginx where it was.
	Unread string
}

// DeleteStream removes one, keeping the previous content as .bak, and
// reports whether nginx was reading it — a stream it never read needs no
// reload to stop.
//
// nginx includes stream.d/*.conf, so the backup is inert — and a forwarding
// rule somebody spent ten minutes getting right is worth a file left behind,
// since the delete itself is the only thing this dashboard does to a stream
// that cannot be undone from the form. A symbolic link is removed as a link,
// and a file that cannot be read is removed without a backup; both are
// recorded without content.
func (s *Service) DeleteStream(ctx context.Context, name string) (*StreamDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, linked, err := s.streamFile(name)
	if err != nil {
		return nil, err
	}
	out := &StreamDeletion{}
	var before []byte
	if linked {
		out.Link, _ = os.Readlink(path)
	} else if b, err := os.ReadFile(path); err != nil {
		out.Unread = err.Error()
	} else {
		if err := os.WriteFile(path+".bak", b, 0o644); err != nil {
			return nil, err
		}
		out.Backup, before = path+".bak", b
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	s.recordChange(ctx, Change{Path: path, Action: ChangeDelete, Before: before, BeforeExisted: true})
	out.Read = streamIncludeFound(s.nginxDir, s.streamDir())
	return out, nil
}
