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
	// Servers is the pool behind the stream when there is more than one
	// server. Upstream is always its first address, so everything that
	// reads Upstream keeps working; a pool of one is folded back into
	// Upstream alone (foldStreamServers), so one file never reads back two
	// ways.
	Servers []StreamServer `json:"servers,omitempty"`
	// Balance is how nginx spreads connections over the pool: empty for
	// round robin, least-conn, client-ip (a consistent hash of the client
	// address, so one client keeps reaching one server) or random. It is
	// empty whenever there is one server, where it would change nothing.
	Balance string `json:"balance,omitempty"`
	// NoRetry stops nginx trying the next server when one fails to accept
	// (proxy_next_upstream off). nginx retries by default, which is what a
	// pool is for; turning it off suits a backend where a half-made
	// connection must not be repeated. Only a pool carries it.
	NoRetry bool `json:"noRetry,omitempty"`
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
	// Rules are the ordered access list for what an allow list cannot say:
	// a deny ahead of a wider allow, or a blocklist that lets everyone else
	// in. nginx takes the first rule a client matches, and DefaultAllow is
	// what happens to a client no rule matches. Rules that are only allows
	// with everyone else denied are always folded into AllowFrom, so one file
	// never reads back two ways; DefaultAllow is nil whenever Rules is empty.
	Rules        []StreamRule `json:"rules,omitempty"`
	DefaultAllow *bool        `json:"defaultAllow,omitempty"`
	// MaxConnPerIP and MaxConnTotal cap the connections (UDP sessions, for
	// UDP) open at once from one client address and in all. nginx closes the
	// one over the cap as soon as it is accepted. Zero is no cap.
	MaxConnPerIP int `json:"maxConnPerIp,omitempty"`
	MaxConnTotal int `json:"maxConnTotal,omitempty"`
	// UploadRate and DownloadRate limit each connection's speed from the
	// client and to it, in KiB per second. nginx applies them per connection,
	// so a client opening four gets four times the rate. Zero is no limit.
	UploadRate   int `json:"uploadRate,omitempty"`
	DownloadRate int `json:"downloadRate,omitempty"`
	// TLS makes nginx the TLS end for clients: every listen takes ssl and
	// presents CertPath with KeyPath, and the backend sees plain TCP unless
	// UpstreamTLS is on too. TCP only — nginx has no DTLS.
	TLS      bool   `json:"tls,omitempty"`
	CertPath string `json:"certPath,omitempty"`
	KeyPath  string `json:"keyPath,omitempty"`
	// UpstreamTLS makes nginx a TLS client of the backend (proxy_ssl).
	UpstreamTLS bool `json:"upstreamTls,omitempty"`
	// UpstreamName is the backend's host name, sent as SNI and, with
	// UpstreamVerify, the name its certificate must carry. Without it nginx
	// would send no SNI and check the certificate against the upstream
	// block's generated name, which no certificate carries.
	UpstreamName string `json:"upstreamName,omitempty"`
	// UpstreamVerify refuses a backend whose certificate does not chain to
	// UpstreamCA or does not carry UpstreamName. nginx checks nothing by
	// default, so without it the link is encrypted but not authenticated.
	UpstreamVerify bool   `json:"upstreamVerify,omitempty"`
	UpstreamCA     string `json:"upstreamCa,omitempty"`
	// LogConnections writes a line per session to StreamLogPath, which the
	// traffic view reads (stream_traffic.go).
	LogConnections bool `json:"logConnections,omitempty"`
}

// StreamServer is one server of a stream's pool, with nginx's own options.
type StreamServer struct {
	// Address is host:port or unix:/path, as Upstream is.
	Address string `json:"address"`
	// Weight is the server's share of connections. Zero is nginx's 1.
	Weight int `json:"weight,omitempty"`
	// MaxFails is how many failed connects within FailTimeout mark the
	// server unavailable for FailTimeout. Nil is nginx's 1; zero never marks
	// it, which is why it is a pointer.
	MaxFails *int `json:"maxFails,omitempty"`
	// FailTimeout is that window and pause, in seconds. Zero is nginx's 10s.
	FailTimeout int `json:"failTimeout,omitempty"`
	// Backup is used only while every other server is unavailable. nginx
	// refuses it under client-ip and random balancing.
	Backup bool `json:"backup,omitempty"`
	// Down takes the server out of the pool without deleting it.
	Down bool `json:"down,omitempty"`
}

// streamBalances maps the form's balancing methods to the directive each
// renders as, written first in the upstream block: nginx checks a server's
// backup against the method already read, so the order matters.
var streamBalances = map[string]string{
	"least-conn": "least_conn",
	"client-ip":  "hash $remote_addr consistent",
	"random":     "random",
}

// StreamRule is one line of a stream's ordered access list.
type StreamRule struct {
	// Action is allow or deny.
	Action string `json:"action"`
	// Source is an address or a CIDR. Never all: everyone else is the
	// spec's DefaultAllow, and a rule matching everyone would leave every
	// rule below it unreachable.
	Source string `json:"source"`
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
	// State is what nginx does with the stream now: live, not-listening,
	// shadowed, not-read or unknown (stream_state.go).
	State string `json:"state"`
	// StateReason says why, in a sentence, for every state but a plain live
	// one; a live stream changed since nginx loaded it says that.
	StateReason string `json:"stateReason,omitempty"`
	// Blocker is what has the stream's port: the stream nginx reads first on
	// it, a site on it, or the program holding it.
	Blocker *PortOwner `json:"blocker,omitempty"`
	// BindError is the last bind() failure nginx logged for one of the
	// stream's sockets, with its time, in nginx's words.
	BindError string `json:"bindError,omitempty"`
	// Paused marks a stream kept in paused/, out of nginx's include
	// (stream_pause.go). The form does not edit one; resume it first.
	Paused bool `json:"paused,omitempty"`
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
	// Connection is the include the dashboard added, when that is what reads
	// the directory: the one the page can take out again.
	Connection *StreamConnection `json:"connection,omitempty"`
	// StreamBlock is the file holding a top-level stream block that does not
	// include the directory. A second stream block is "duplicate" to nginx,
	// so the snippet is then the include line that goes inside this one.
	StreamBlock string `json:"streamBlock,omitempty"`
	// Snippet is what to add to nginx.conf when it is not included.
	Snippet string        `json:"snippet"`
	Dir     string        `json:"dir"`
	Streams []StreamEntry `json:"streams"`
	// Paused are the streams kept in paused/ (stream_pause.go), apart from
	// Streams so nothing counting what nginx reads counts them.
	Paused []StreamEntry `json:"paused"`
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
	// test — the command failing, or the master refusing the configuration
	// for another stream's or a site's port after the command succeeded. The
	// file stays: it is valid, and it takes effect at the next reload that
	// succeeds.
	ReloadError string `json:"reloadError,omitempty"`
	Output      string `json:"output,omitempty"`
	// Listening is whether nginx held every socket the stream asks for once
	// it had taken the reload up, watched for up to three seconds. Absent
	// when there was no reload to watch or it could not be watched.
	Listening *bool `json:"listening,omitempty"`
	// ListenNote says why Listening is absent, or what nginx had not done
	// when the wait ran out.
	ListenNote string `json:"listenNote,omitempty"`
}

var (
	// ErrStreamExists refuses a new stream, or a rename, onto a name that is
	// taken: without it the other stream's file was replaced in silence.
	ErrStreamExists = errors.New("a stream by that name already exists")
	// ErrStreamNotFound is a stream to edit or delete that has no file.
	ErrStreamNotFound = errors.New("no such stream")
	// ErrNoStreamSSL refuses TLS on a stream when nginx was built without
	// stream_ssl_module. nginx -t catches it only while it reads the stream
	// directory; saved for later, the file would fail the reload that
	// connects the directory, and every one after.
	ErrNoStreamSSL = errors.New("this nginx was built without stream_ssl_module, so a stream cannot use TLS")
)

// HandwrittenStreamError refuses to save the form over a file that does more
// than the form can say. Saving would drop every one of those things — a
// server option the form lacks, a listen option — and a forwarding rule that
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
	if err := validStreamServers(spec); err != nil {
		return err
	}
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
	if err := validStreamAccess(spec); err != nil {
		return err
	}
	if err := validStreamTLS(spec); err != nil {
		return err
	}
	for _, limit := range []struct {
		value, max int
		what       string
	}{
		{spec.MaxConnPerIP, 1_000_000, "the connections per client"},
		{spec.MaxConnTotal, 1_000_000, "the connections in total"},
		{spec.UploadRate, 10 << 20, "the upload rate"},
		{spec.DownloadRate, 10 << 20, "the download rate"},
	} {
		if limit.value < 0 || limit.value > limit.max {
			return fmt.Errorf("%s must be between 0 and %d", limit.what, limit.max)
		}
	}
	return nil
}

// validStreamTLS checks both TLS ends and drops what an end that is off
// would carry, so a file reads back one way.
func validStreamTLS(spec *StreamSpec) error {
	if !spec.TLS {
		spec.CertPath, spec.KeyPath = "", ""
	}
	if !spec.UpstreamTLS {
		spec.UpstreamName, spec.UpstreamVerify = "", false
	}
	if !spec.UpstreamVerify {
		spec.UpstreamCA = ""
	}
	if (spec.TLS || spec.UpstreamTLS) && spec.Protocol != "tcp" {
		return fmt.Errorf("TLS is for TCP only — nginx has no DTLS for UDP")
	}
	if spec.TLS && (!absPathRe.MatchString(spec.CertPath) || !absPathRe.MatchString(spec.KeyPath)) {
		return fmt.Errorf("serving TLS needs an absolute path to the certificate and to its key")
	}
	if spec.UpstreamName != "" && (!domainRe.MatchString(spec.UpstreamName) || strings.HasPrefix(spec.UpstreamName, "*")) {
		return fmt.Errorf("the backend's name must be a host name like db.internal")
	}
	if spec.UpstreamVerify {
		if spec.UpstreamName == "" {
			return fmt.Errorf("verifying the backend needs the name its certificate carries")
		}
		if !absPathRe.MatchString(spec.UpstreamCA) {
			return fmt.Errorf("verifying the backend needs an absolute path to the CA certificates to trust")
		}
	}
	return nil
}

// streamUsesTLS is whether a stream needs nginx built with
// stream_ssl_module: without it nginx refuses ssl and proxy_ssl alike.
func streamUsesTLS(spec *StreamSpec) bool {
	return spec.TLS || spec.UpstreamTLS
}

// validStreamAccess checks the ordered rules and folds them into the one
// shape a file reads back as.
func validStreamAccess(spec *StreamSpec) error {
	if len(spec.Rules) == 0 && spec.DefaultAllow == nil {
		spec.Rules = nil
		return nil
	}
	if len(spec.AllowFrom) > 0 {
		return fmt.Errorf("send the access list as allowFrom or as rules, not both")
	}
	if spec.DefaultAllow == nil {
		return fmt.Errorf("the access rules need a choice for everyone else")
	}
	for i := range spec.Rules {
		r := &spec.Rules[i]
		r.Source = strings.TrimSpace(r.Source)
		if r.Action != "allow" && r.Action != "deny" {
			return fmt.Errorf("an access rule must allow or deny")
		}
		if r.Source == "all" {
			return fmt.Errorf("a rule for everyone leaves the rules below it unreachable — set what happens to everyone else instead")
		}
		if err := validACLEntry(r.Source); err != nil {
			return err
		}
	}
	if !*spec.DefaultAllow && len(spec.Rules) == 0 {
		return fmt.Errorf("denying everyone with no rule to let anyone in closes the port to all — pause the stream instead")
	}
	foldStreamAccess(spec)
	return nil
}

// foldStreamAccess writes ordered rules the form's simplest way: nothing
// when everyone is let in, and an allow list when the rules only allow and
// everyone else is denied.
func foldStreamAccess(spec *StreamSpec) {
	if *spec.DefaultAllow {
		if len(spec.Rules) == 0 {
			spec.Rules, spec.DefaultAllow = nil, nil
		}
		return
	}
	allows := make([]string, 0, len(spec.Rules))
	for _, r := range spec.Rules {
		if r.Action != "allow" {
			return
		}
		allows = append(allows, r.Source)
	}
	spec.AllowFrom, spec.Rules, spec.DefaultAllow = allows, nil, nil
}

// maxStreamServers bounds a pool to what a form row per server can show.
const maxStreamServers = 32

// validStreamServers checks the pool and folds it into the one shape a file
// reads back as. With no Servers the pool is Upstream alone.
func validStreamServers(spec *StreamSpec) error {
	if len(spec.Servers) == 0 {
		spec.Servers = []StreamServer{{Address: spec.Upstream}}
	}
	if len(spec.Servers) > maxStreamServers {
		return fmt.Errorf("a stream takes at most %d servers", maxStreamServers)
	}
	if _, ok := streamBalances[spec.Balance]; !ok && spec.Balance != "" {
		return fmt.Errorf("balancing must be round robin, least-conn, client-ip or random")
	}
	seen := map[string]bool{}
	primary, up := false, false
	for i := range spec.Servers {
		srv := &spec.Servers[i]
		srv.Address = strings.TrimSpace(srv.Address)
		if err := validStreamUpstream(srv.Address); err != nil {
			if len(spec.Servers) > 1 {
				return fmt.Errorf("server %d: %w", i+1, err)
			}
			return err
		}
		if seen[srv.Address] {
			return fmt.Errorf("%s is in the pool twice", srv.Address)
		}
		seen[srv.Address] = true
		if srv.Weight < 0 || srv.Weight > 1000 {
			return fmt.Errorf("a server's weight must be between 1 and 1000")
		}
		if srv.Weight == 1 {
			srv.Weight = 0
		}
		if srv.MaxFails != nil && (*srv.MaxFails < 0 || *srv.MaxFails > 1000) {
			return fmt.Errorf("a server's max fails must be between 0 and 1000")
		}
		if srv.MaxFails != nil && *srv.MaxFails == 1 {
			srv.MaxFails = nil
		}
		if srv.FailTimeout < 0 || srv.FailTimeout > 86400 {
			return fmt.Errorf("a server's fail timeout must be between 0 and 86400 seconds")
		}
		if srv.FailTimeout == 10 {
			srv.FailTimeout = 0
		}
		if srv.Backup && (spec.Balance == "client-ip" || spec.Balance == "random") {
			return fmt.Errorf("nginx has no backup server under %s balancing — use least-conn or round robin, or take backup off", spec.Balance)
		}
		if !srv.Down {
			up = true
			primary = primary || !srv.Backup
		}
	}
	if !up {
		return fmt.Errorf("every server is down, so nothing would answer — pause the stream instead")
	}
	if !primary {
		return fmt.Errorf("a backup only stands in for another server — at least one server that is up must not be a backup")
	}
	foldStreamServers(spec)
	return nil
}

// foldStreamServers writes a pool the form's simplest way: one server is
// Upstream alone. nginx keeps no failure count for a lone server and has
// nothing to weigh it against or retry on, so its weight, failure options,
// method and retry setting change nothing and are dropped. A lone backup or
// down server never gets here: validation refuses both, and a file holding
// one reads back unfolded so the refusal is shown rather than lost.
func foldStreamServers(spec *StreamSpec) {
	if len(spec.Servers) > 0 {
		spec.Upstream = spec.Servers[0].Address
	}
	if len(spec.Servers) < 2 {
		spec.Balance, spec.NoRetry = "", false
	}
	if len(spec.Servers) == 1 && !spec.Servers[0].Backup && !spec.Servers[0].Down {
		spec.Servers = nil
	}
}

// serverLine is a pool server as its upstream line writes it.
func serverLine(srv StreamServer) string {
	out := srv.Address
	if srv.Weight > 0 {
		out += " weight=" + strconv.Itoa(srv.Weight)
	}
	if srv.MaxFails != nil {
		out += " max_fails=" + strconv.Itoa(*srv.MaxFails)
	}
	if srv.FailTimeout > 0 {
		out += " fail_timeout=" + nginxDuration(srv.FailTimeout)
	}
	if srv.Backup {
		out += " backup"
	}
	if srv.Down {
		out += " down"
	}
	return out
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

// streamZoneNames are the stream's connection-counting zones. A zone is
// declared at the top of the stream context and its name is global to
// nginx — an http zone of the same name is refused as "already declared for
// a different use" — so it is built from the same ident as the upstream.
// The total is keyed on $server_port, which is one value for every socket
// the stream listens on.
func streamZoneNames(name string) (perIP, total string) {
	ident := NginxIdent(name)
	return ident + "_conn_ip", ident + "_conn_all"
}

// nginxRate writes KiB per second in nginx's size syntax.
func nginxRate(kib int) string {
	if kib%1024 == 0 {
		return strconv.Itoa(kib/1024) + "m"
	}
	return strconv.Itoa(kib) + "k"
}

// streamRate reads a proxy_upload_rate or proxy_download_rate back as KiB
// per second. A value that is not whole KiB is not one the form can hold,
// and zero — nginx's "no limit" — is the form's zero.
func streamRate(args []string) (int, bool) {
	if len(args) != 1 || args[0] == "" {
		return 0, false
	}
	value, scale := args[0], int64(1)
	switch value[len(value)-1] {
	case 'k', 'K':
		value, scale = value[:len(value)-1], 1<<10
	case 'm', 'M':
		value, scale = value[:len(value)-1], 1<<20
	case 'g', 'G':
		value, scale = value[:len(value)-1], 1<<30
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || n > 1<<40 {
		return 0, false
	}
	bytes := n * scale
	if bytes%1024 != 0 || bytes/1024 > 10<<20 {
		return 0, false
	}
	return int(bytes / 1024), true
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
	perIP, total := streamZoneNames(spec.Name)
	if spec.MaxConnPerIP > 0 {
		l.add("limit_conn_zone $binary_remote_addr zone=%s:1m;", perIP)
	}
	if spec.MaxConnTotal > 0 {
		l.add("limit_conn_zone $server_port zone=%s:1m;", total)
	}
	if spec.MaxConnPerIP > 0 || spec.MaxConnTotal > 0 {
		l.blank()
	}
	if spec.LogConnections {
		l.add("log_format %s '%s';", streamLogFormatName(spec.Name), streamLogFormat)
		l.blank()
	}
	l.add("upstream %s {", upstream)
	if method := streamBalances[spec.Balance]; method != "" {
		l.add("    %s;", method)
	}
	if len(spec.Servers) == 0 {
		l.add("    server %s;", spec.Upstream)
	}
	for _, srv := range spec.Servers {
		l.add("    server %s;", serverLine(srv))
	}
	l.add("}")
	l.blank()
	l.add("server {")
	for _, suffix := range streamListenSuffixes(spec.Protocol) {
		if spec.TLS {
			suffix += " ssl"
		}
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
	if spec.TLS {
		l.blank()
		l.add("    # nginx ends the client's TLS here; the backend sees what is inside.")
		l.add("    ssl_certificate     %s;", spec.CertPath)
		l.add("    ssl_certificate_key %s;", spec.KeyPath)
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
	if len(spec.Rules) > 0 {
		l.blank()
		l.add("    # A raw stream has no authentication of any kind, so these rules")
		l.add("    # decide who may connect. The first one a client matches wins.")
		for _, r := range spec.Rules {
			l.add("    %s %s;", r.Action, r.Source)
		}
		if *spec.DefaultAllow {
			l.add("    allow all;")
		} else {
			l.add("    deny all;")
		}
	}
	if spec.MaxConnPerIP > 0 || spec.MaxConnTotal > 0 {
		l.blank()
		l.add("    # A connection over either cap is closed as soon as it is accepted.")
		if spec.MaxConnPerIP > 0 {
			l.add("    limit_conn %s %d;", perIP, spec.MaxConnPerIP)
		}
		if spec.MaxConnTotal > 0 {
			l.add("    limit_conn %s %d;", total, spec.MaxConnTotal)
		}
	}
	if spec.LogConnections {
		l.blank()
		l.add("    # One line per session, which the dashboard's traffic view reads.")
		l.add("    %s", streamLogDirective(spec.Name))
	}
	l.blank()
	l.add("    proxy_pass %s;", upstream)
	if spec.NoRetry {
		l.add("    # A server that fails to accept fails the connection; no other is tried.")
		l.add("    proxy_next_upstream off;")
	}
	if spec.UpstreamTLS {
		l.add("    # nginx opens its own TLS connection to the backend.")
		l.add("    proxy_ssl on;")
		if spec.UpstreamName != "" {
			l.add("    proxy_ssl_name %s;", spec.UpstreamName)
			l.add("    proxy_ssl_server_name on;")
		}
		if spec.UpstreamVerify {
			l.add("    proxy_ssl_verify on;")
			l.add("    proxy_ssl_trusted_certificate %s;", spec.UpstreamCA)
		} else {
			l.add("    # The backend's certificate is not checked: encrypted, not authenticated.")
		}
	}
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
	if spec.UploadRate > 0 {
		l.add("    proxy_upload_rate %s;", nginxRate(spec.UploadRate))
	}
	if spec.DownloadRate > 0 {
		l.add("    proxy_download_rate %s;", nginxRate(spec.DownloadRate))
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
	// sslListens and plainListens count the listens with and without ssl:
	// the form puts ssl on every one or none.
	sslListens, plainListens int
	// sni is whether proxy_ssl_server_name is on, which the form writes
	// exactly when it writes proxy_ssl_name.
	sni bool
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
	// zones are the connection zones this file declares the way RenderStream
	// does, by name, with whether a limit_conn uses them.
	perIP, total := streamZoneNames(p.spec.Name)
	zones := map[string]bool{}
	// logFormat is whether the file declares the format RenderStream writes.
	logFormat := false
	for _, d := range directives {
		switch {
		case d.Name == "upstream" && d.Block != nil && len(d.Args) == 1:
			upstreams[d.Args[0]] = d
		case d.Name == "server" && d.Block != nil:
			servers = append(servers, d)
		case d.Name == "limit_conn_zone" && len(d.Args) == 2 &&
			(d.Args[0] == "$binary_remote_addr" && d.Args[1] == "zone="+perIP+":1m" ||
				d.Args[0] == "$server_port" && d.Args[1] == "zone="+total+":1m"):
			zones[strings.TrimSuffix(strings.TrimPrefix(d.Args[1], "zone="), ":1m")] = false
		case d.Name == "log_format" && len(d.Args) == 2 &&
			d.Args[0] == streamLogFormatName(p.spec.Name) && d.Args[1] == streamLogFormat:
			logFormat = true
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
		case "proxy_next_upstream":
			switch {
			case len(d.Args) == 1 && d.Args[0] == "off":
				p.spec.NoRetry = true
			case len(d.Args) != 1 || d.Args[0] != "on":
				p.cannot("proxy_next_upstream " + strings.Join(d.Args, " "))
			}
		case "proxy_responses":
			if len(d.Args) == 1 && d.Args[0] == "1" {
				p.spec.UDPMode = "request"
			} else {
				p.cannot("proxy_responses " + strings.Join(d.Args, " "))
			}
		case "limit_conn":
			p.readLimitConn(d.Args, zones, perIP, total)
		case "access_log":
			switch {
			case len(d.Args) == 1 && d.Args[0] == "off":
				// The stream module's default, so saving without it is the same.
			case logFormat && len(d.Args) == 2 && d.Args[0] == StreamLogPath(p.spec.Name) &&
				d.Args[1] == streamLogFormatName(p.spec.Name):
				p.spec.LogConnections = true
			default:
				p.cannot("access_log " + strings.Join(d.Args, " "))
			}
		case "ssl_certificate", "ssl_certificate_key", "proxy_ssl_trusted_certificate", "proxy_ssl_name":
			p.readTLSValue(d)
		case "proxy_ssl", "proxy_ssl_server_name", "proxy_ssl_verify":
			on, ok := onOff(d.Args)
			switch {
			case !ok:
				p.cannot(d.Name + " " + strings.Join(d.Args, " "))
			case d.Name == "proxy_ssl":
				p.spec.UpstreamTLS = on
			case d.Name == "proxy_ssl_server_name":
				p.sni = on
			default:
				p.spec.UpstreamVerify = on
			}
		case "proxy_upload_rate", "proxy_download_rate":
			kib, ok := streamRate(d.Args)
			switch {
			case !ok:
				p.cannot(d.Name + " " + strings.Join(d.Args, " "))
			case d.Name == "proxy_upload_rate":
				p.spec.UploadRate = kib
			default:
				p.spec.DownloadRate = kib
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
	if logFormat && !p.spec.LogConnections {
		p.cannot("a log_format nothing uses")
	}
	for _, inUse := range zones {
		if !inUse {
			p.cannot("a limit_conn_zone nothing uses")
		}
	}
	if p.spec.Upstream == "" {
		p.cannot("no proxy_pass")
	}
	p.reduceBinds()
	p.checkTLS()
	if p.spec.UDPMode == "request" && p.spec.Protocol == "tcp" {
		p.cannot("proxy_responses on TCP")
	}
	if p.spec.Protocol != "tcp" && p.spec.UDPMode == "" {
		p.spec.UDPMode = "session"
	}
	foldStreamServers(&p.spec)
	p.readAccess(rules)
	return p
}

// onOff reads a flag directive's one argument.
func onOff(args []string) (on, ok bool) {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return false, false
	}
	return args[0] == "on", true
}

// readTLSValue takes one of the TLS lines holding a path or a name, once,
// in the shape validStreamTLS accepts.
func (p *parsedStream) readTLSValue(d Directive) {
	field, valid := &p.spec.CertPath, absPathRe.MatchString
	switch d.Name {
	case "ssl_certificate_key":
		field = &p.spec.KeyPath
	case "proxy_ssl_trusted_certificate":
		field = &p.spec.UpstreamCA
	case "proxy_ssl_name":
		field = &p.spec.UpstreamName
		valid = func(name string) bool { return domainRe.MatchString(name) && !strings.HasPrefix(name, "*") }
	}
	if len(d.Args) != 1 || !valid(d.Args[0]) || *field != "" {
		p.cannot(d.Name + " " + strings.Join(d.Args, " "))
		return
	}
	*field = d.Args[0]
}

// checkTLS names the TLS the form cannot write back as it is: ssl on some
// listens only, a certificate with no ssl listen to use it, a TLS line with
// its end turned off, or verification without the name or the CAs nginx
// needs to check with.
func (p *parsedStream) checkTLS() {
	switch {
	case p.sslListens > 0 && p.plainListens > 0:
		p.cannot("ssl on some listens only")
	case p.sslListens > 0:
		p.spec.TLS = true
	}
	if p.spec.TLS != (p.spec.CertPath != "") || p.spec.TLS != (p.spec.KeyPath != "") {
		p.cannot("an ssl listen and its certificate apart")
	}
	if (p.spec.TLS || p.spec.UpstreamTLS) && p.spec.Protocol != "tcp" {
		p.cannot("TLS on UDP")
	}
	if p.sni != (p.spec.UpstreamName != "") {
		p.cannot("proxy_ssl_server_name without proxy_ssl_name, or the other way round")
	}
	if !p.spec.UpstreamTLS && (p.spec.UpstreamName != "" || p.spec.UpstreamVerify) {
		p.cannot("proxy_ssl options with proxy_ssl off")
	}
	if p.spec.UpstreamVerify != (p.spec.UpstreamCA != "") ||
		p.spec.UpstreamVerify && p.spec.UpstreamName == "" {
		p.cannot("proxy_ssl_verify without its CA or its name")
	}
}

// readLimitConn takes a connection cap on one of the zones this file
// declares under the stream's own names. A zone from another file is not
// the form's: saving would declare a second one, or drop the cap.
func (p *parsedStream) readLimitConn(args []string, zones map[string]bool, perIP, total string) {
	if len(args) != 2 {
		p.cannot("limit_conn")
		return
	}
	n, err := strconv.Atoi(args[1])
	used, declared := zones[args[0]]
	switch {
	case !declared:
		p.cannot("limit_conn on a zone declared elsewhere")
		return
	case used || err != nil || n < 1 || n > 1_000_000:
		p.cannot("limit_conn " + strings.Join(args, " "))
		return
	}
	zones[args[0]] = true
	if args[0] == perIP {
		p.spec.MaxConnPerIP = n
	} else if args[0] == total {
		p.spec.MaxConnTotal = n
	}
}

// readListen takes one listen line. Only the shapes RenderStream writes are
// the form's; anything else — a port range, a host name, reuseport — is
// named, and its socket is still read so a port check sees it.
func (p *parsedStream) readListen(args []string) {
	if len(args) == 0 {
		p.cannot("listen")
		return
	}
	b := bind{}
	ssl := false
	for _, param := range args[1:] {
		switch param {
		case "udp":
			b.udp = true
		case "ssl":
			ssl = true
		default:
			p.cannot("listen option " + param)
		}
	}
	if ssl {
		p.sslListens++
	} else {
		p.plainListens++
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
// whose servers and balancing method are the form's pool, or as an address
// written straight into proxy_pass, which is a pool of one.
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
	var servers []StreamServer
	for _, d := range block.Block {
		if d.Name == "server" && len(d.Args) > 0 {
			servers = append(servers, p.readPoolServer(d.Args))
			continue
		}
		balance := ""
		switch {
		case d.Name == "least_conn" && len(d.Args) == 0:
			balance = "least-conn"
		case d.Name == "random" && len(d.Args) == 0:
			balance = "random"
		case d.Name == "hash" && len(d.Args) == 2 && d.Args[0] == "$remote_addr" && d.Args[1] == "consistent":
			balance = "client-ip"
		default:
			p.cannot(d.Name)
			continue
		}
		// A method after a server, or a second one, is read by nginx with a
		// warning and not the way the form would write it: first, once.
		if p.spec.Balance != "" || len(servers) > 0 {
			p.cannot("a balancing method after the servers or twice")
			continue
		}
		p.spec.Balance = balance
	}
	if len(servers) == 0 {
		p.cannot("an empty upstream")
		return
	}
	if len(servers) > maxStreamServers {
		p.cannot(fmt.Sprintf("%d upstream servers", len(servers)))
	}
	p.spec.Upstream, p.spec.Servers = servers[0].Address, servers
}

// readPoolServer reads one upstream server line with the options the form
// holds. Any other option — max_conns, resolve, service — is hand-written.
func (p *parsedStream) readPoolServer(args []string) StreamServer {
	srv := StreamServer{Address: args[0]}
	for _, opt := range args[1:] {
		key, value, _ := strings.Cut(opt, "=")
		switch {
		case opt == "backup":
			srv.Backup = true
		case opt == "down":
			srv.Down = true
		case key == "weight":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 1000 {
				p.cannot("upstream server option " + opt)
				continue
			}
			if n > 1 {
				srv.Weight = n
			}
		case key == "max_fails":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 1000 {
				p.cannot("upstream server option " + opt)
				continue
			}
			if n != 1 {
				srv.MaxFails = &n
			}
		case key == "fail_timeout":
			seconds, ok := streamSeconds([]string{value})
			if !ok {
				p.cannot("upstream server option " + opt)
				continue
			}
			if seconds != 10 {
				srv.FailTimeout = seconds
			}
		default:
			p.cannot("upstream server option " + key)
		}
	}
	return srv
}

type accessRule struct {
	allow  bool
	source string
}

// readAccess turns allow and deny lines into the form's access list and
// works out who may connect either way.
//
// nginx takes the first rule that matches and lets through anyone none
// matches. The first rule for everyone is therefore the form's "everyone
// else", and nothing after it is ever reached; without one, everyone else
// is let in. The result is folded as a save folds it, so an allow list
// closed by `deny all` reads back as the plain allow list it always was.
func (p *parsedStream) readAccess(rules []accessRule) {
	p.open = rulesAdmitEveryone(rules)
	defaultAllow := true
	var ordered []StreamRule
	for _, r := range rules {
		if r.source == "all" {
			defaultAllow = r.allow
			break
		}
		action := "deny"
		if r.allow {
			action = "allow"
		}
		ordered = append(ordered, StreamRule{Action: action, Source: r.source})
	}
	for _, r := range ordered {
		if validACLEntry(r.Source) != nil {
			p.cannot(r.Action + " " + r.Source)
		}
	}
	if !defaultAllow && len(ordered) == 0 {
		p.cannot("deny all with nothing allowed")
		return
	}
	p.spec.Rules, p.spec.DefaultAllow = ordered, &defaultAllow
	foldStreamAccess(&p.spec)
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
	if len(spec.Rules) > 0 {
		rules := make([]accessRule, 0, len(spec.Rules)+1)
		for _, r := range spec.Rules {
			rules = append(rules, accessRule{allow: r.Action == "allow", source: r.Source})
		}
		return rulesAdmitEveryone(append(rules, accessRule{allow: *spec.DefaultAllow, source: "all"}))
	}
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
// form can hold. Nor is zero: the form's zero is "unset", so a file saying
// proxy_timeout 0 — which nginx takes, and drops every connection at once —
// would open with the field empty and be saved as nginx's default.
func streamSeconds(args []string) (int, bool) {
	if len(args) != 1 {
		return 0, false
	}
	ms, ok := parseNginxDuration(args[0])
	if !ok || ms == 0 || ms%1000 != 0 || ms/1000 > 86400 {
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
		Paused:  []StreamEntry{},
		Snippet: "stream {\n    " + streamIncludeDirective(dir) + "\n}",
	}
	include := readStreamInclude(s.nginxDir, dir)
	status.Included, status.IncludedIn = include.included, include.misplaced
	if include.err != nil {
		status.IncludeError = include.err.Error()
	} else {
		status.Connection = ownConnection(include.files, dir)
		if !include.included && include.misplaced == "" {
			if status.StreamBlock = streamBlockFile(include.files); status.StreamBlock != "" {
				status.Snippet = streamIncludeDirective(dir)
			}
		}
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
	paused, err := os.ReadDir(s.pausedStreamDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range paused {
		if streamFileName(e) && e.Type().IsRegular() {
			entry := listStream(s.pausedStreamDir(), e)
			entry.Paused, entry.State, entry.StateReason = true, StreamPaused, streamPausedReason
			status.Paused = append(status.Paused, entry)
		}
	}
	sort.SliceStable(status.Streams, func(i, j int) bool {
		return status.Streams[i].Listen < status.Streams[j].Listen
	})
	sort.SliceStable(status.Paused, func(i, j int) bool {
		return status.Paused[i].Listen < status.Paused[j].Listen
	})
	s.fillStreamStates(ctx, status, include)
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
// and so is a port another stream, a site or another program already holds
// (PortInUseError): nginx -t passes all three, and nginx then ignores the
// stream or fails the reload in a way the reload command does not report.
//
// A reload is watched until nginx has taken it up (stream_probe.go). One
// nginx refuses on the stream's own port all the same — a program that took
// it since the check — puts the stream back as it was and is the same
// refusal, with nginx's own words: a stream nginx cannot bind makes every
// later reload on the host fail, so it is not left behind. A reload that fails
// for any other reason is reported in the result, not as an error: the file
// is written and valid, and "not applied" was untrue.
func (s *Service) ApplyStream(ctx context.Context, spec *StreamSpec, previous string, reload bool) (*StreamResult, error) {
	content, err := RenderStream(spec)
	if err != nil {
		return nil, err
	}
	// Asked before the lock, like the reads below. A module that could not
	// be asked about is left to nginx -t.
	if streamUsesTLS(spec) {
		if m := s.StreamModule(ctx); m.State != ModuleUnknown && !m.SSL {
			return nil, ErrNoStreamSSL
		}
	}
	// Read before the lock: finding the running nginx walks /proc, and the
	// lock holds up every other save on the host.
	files, _ := readConfigFiles(s.nginxDir)
	view := readNginx(ctx, s, files, 0)

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
		// A paused stream keeps its name: resuming it would otherwise meet
		// this one's file.
		if _, err := os.Lstat(filepath.Join(s.pausedStreamDir(), spec.Name+".conf")); err == nil {
			return nil, fmt.Errorf("%w: %s (paused)", ErrStreamExists, spec.Name)
		}
	}
	// The configuration is read again under the lock: it is what the
	// reload will load.
	files, _ = readConfigFiles(s.nginxDir)
	if refused := s.portClaims(oldPath, files, view).conflict(ctx, spec, oldBinds); refused != nil {
		return nil, refused
	}

	res := &StreamResult{Name: spec.Name, Path: path, Content: content, Warnings: StreamWarnings(spec)}
	if view.nginx == nil && view.listenErr != nil {
		res.Warnings = append(res.Warnings, "The host's listening sockets could not be read ("+view.listenErr.Error()+
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
	undo := func() {
		if renamed {
			os.Remove(path)
			os.Rename(backup, oldPath)
			restoreConfig(backup, earlierBackup, backupExisted)
		} else {
			restoreConfig(path, before, oldPath != "")
		}
	}

	res.Validation = runValidator(ctx, "nginx", "-t")
	res.Validation.Note = s.includeNote(path)
	if !res.Validation.Valid {
		undo()
		return res, ErrInvalidConf
	}
	if reload {
		if refused := s.reloadAndWatch(ctx, res, spec, oldBinds); refused != nil {
			undo()
			return nil, refused
		}
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
	return res, nil
}

// reloadAndWatch reloads nginx and, where nginx reads the stream, watches it
// take the stream up. It returns the refusal for a reload nginx failed on the
// stream's own port, for the caller to put the stream back; nginx kept the
// configuration it had, so the host is then where it was.
func (s *Service) reloadAndWatch(ctx context.Context, res *StreamResult, spec *StreamSpec, own []bind) *PortInUseError {
	files, err := readConfigFiles(s.nginxDir)
	read := false
	if err == nil {
		if tree, err := NginxTree(files); err == nil {
			read = streamBlockFiles(tree)[filepath.Clean(res.Path)]
		}
	}
	var mark reloadMark
	if read {
		mark = s.markReload(ctx, files)
	}
	res.Reloaded, res.Output, res.ReloadError = reloadNginx(ctx)
	if !res.Reloaded {
		return nil
	}
	if !read {
		res.ListenNote = "nginx does not read this file as a stream, so there was no listen to watch it take up."
		return nil
	}
	check := s.awaitListening(ctx, mark, streamBinds(spec))
	switch {
	case check.bindError != "":
		return s.bindRefusal(ctx, spec, own, res.Path, files, check.bindError)
	case check.failure != "":
		res.Reloaded = false
		res.ReloadError = "nginx did not take the reload up: " + check.failure
	case check.checked:
		res.Listening = &check.listening
		res.ListenNote = check.note
	default:
		res.ListenNote = check.note
	}
	return nil
}

// bindRefusal is the refusal for a stream nginx could not bind when it
// reloaded, naming what holds the port now where something can be named.
// self is the stream's new file, which the port check must not count.
func (s *Service) bindRefusal(ctx context.Context, spec *StreamSpec, own []bind, self string, files []ConfigFile, text string) *PortInUseError {
	binds := streamBinds(spec)
	failed := binds[0]
	if failure, ok := parseBindFailure(text); ok {
		for _, b := range binds {
			if b.address() == failure.address {
				failed = b
			}
		}
	}
	claims := s.portClaims(self, files, readNginx(ctx, s, files, 0))
	refused := claims.holder(ctx, failed, own)
	if refused == nil {
		refused = &PortInUseError{PortOwner: PortOwner{Port: failed.port, Proto: failed.proto()}}
	}
	refused.BindError = text
	refused.Suggest = claims.freePort(ctx, spec, own)
	return refused
}

// StreamConflict is the refusal a save of spec would meet for its port, so
// the form can say so while it is filled in; nil when nothing holds the port.
// previous is the stream the form opened on. spec has been through
// RenderStream.
func (s *Service) StreamConflict(ctx context.Context, spec *StreamSpec, previous string) *PortInUseError {
	var skip string
	var own []bind
	if previous != "" {
		if path, _, err := s.streamFile(previous); err == nil {
			skip = path
			if b, err := os.ReadFile(path); err == nil {
				own = parseStreamFile(previous+".conf", string(b)).binds
			}
		}
	}
	files, _ := readConfigFiles(s.nginxDir)
	return s.portClaims(skip, files, readNginx(ctx, s, files, 0)).conflict(ctx, spec, own)
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
	paused := false
	if errors.Is(err, ErrStreamNotFound) {
		// The listing shows paused streams too, and deletes each it lists.
		if pausedPath, pausedErr := s.pausedStreamFile(name); pausedErr == nil {
			path, paused, err = pausedPath, true, nil
		}
	}
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
	if paused {
		// nginx never read it, so there is nothing to reload.
		os.Remove(s.pausedStreamDir())
		return out, nil
	}
	out.Read = streamIncludeFound(s.nginxDir, s.streamDir())
	return out, nil
}
