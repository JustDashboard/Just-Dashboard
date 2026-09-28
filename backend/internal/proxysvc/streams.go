package proxysvc

import (
	"bufio"
	"context"
	"fmt"
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
	// Protocol is tcp or udp. UDP forwarding is stateless and needs its own
	// timeout, which is why it is not merely a flag on the same rule.
	Protocol string `json:"protocol"`
	// Upstream is host:port.
	Upstream string `json:"upstream"`
	// ProxyProtocol prepends the PROXY header so the backend sees the real
	// client address. It has to be turned on at both ends or the backend
	// reads the header as the first bytes of the connection and fails in a
	// way that looks like a protocol mismatch.
	ProxyProtocol bool `json:"proxyProtocol"`
	// Timeout in seconds. Zero leaves nginx's default.
	Timeout int `json:"timeout,omitempty"`
	// AllowFrom restricts who may connect. There is no basic auth for a raw
	// TCP stream, so this is the only access control there is.
	AllowFrom []string `json:"allowFrom"`
}

// StreamStatus reports whether nginx is set up to read these at all.
type StreamStatus struct {
	// Included reports that nginx.conf has a stream block pulling in our
	// directory. Without it the files are written and ignored.
	Included bool `json:"included"`
	// Snippet is what to add to nginx.conf when it is not.
	Snippet string       `json:"snippet"`
	Dir     string       `json:"dir"`
	Streams []StreamSpec `json:"streams"`
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
	switch strings.ToLower(spec.Protocol) {
	case "tcp", "udp":
		spec.Protocol = strings.ToLower(spec.Protocol)
	case "":
		spec.Protocol = "tcp"
	default:
		return fmt.Errorf("protocol must be tcp or udp")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(spec.Upstream))
	if err != nil {
		return fmt.Errorf("the upstream must look like 10.0.0.5:5432")
	}
	if host == "" {
		return fmt.Errorf("the upstream needs a host")
	}
	if strings.ContainsAny(spec.Upstream, " \t\n;{}") {
		return fmt.Errorf("the upstream contains characters that are not allowed")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("the upstream port is not valid")
	}
	if spec.Timeout < 0 || spec.Timeout > 86400 {
		return fmt.Errorf("the timeout is out of range")
	}
	for _, entry := range spec.AllowFrom {
		if err := validACLEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// RenderStream turns a spec into the nginx it means. Hand-written for the same
// reason the site renderer is: the file outlives this dashboard and somebody
// has to be able to read it.
func RenderStream(spec *StreamSpec) (string, error) {
	if err := ValidateStream(spec); err != nil {
		return "", err
	}
	l := &lines{}
	l.add(managedMarker)
	l.add("# Stream: %s", spec.Name)
	l.add("# This belongs inside nginx's top-level stream block, not inside http.")
	l.blank()
	l.add("upstream %s_backend {", strings.ReplaceAll(spec.Name, "-", "_"))
	l.add("    server %s;", spec.Upstream)
	l.add("}")
	l.blank()
	l.add("server {")
	if spec.Protocol == "udp" {
		l.add("    listen %d udp;", spec.Listen)
		l.add("    listen [::]:%d udp;", spec.Listen)
	} else {
		l.add("    listen %d;", spec.Listen)
		l.add("    listen [::]:%d;", spec.Listen)
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
	l.add("    proxy_pass %s_backend;", strings.ReplaceAll(spec.Name, "-", "_"))
	if spec.ProxyProtocol {
		l.add("    # The backend must be configured to expect this header, or it")
		l.add("    # reads it as the first bytes of the connection.")
		l.add("    proxy_protocol on;")
	}
	if spec.Timeout > 0 {
		l.add("    proxy_timeout %ds;", spec.Timeout)
		l.add("    proxy_connect_timeout %ds;", spec.Timeout)
	}
	if spec.Protocol == "udp" {
		l.add("    # UDP has no connection to close, so nginx decides a session is")
		l.add("    # over by silence rather than by a shutdown.")
		l.add("    proxy_responses 1;")
	}
	l.add("}")
	return l.String(), nil
}

// ParseStreamSpec reads a stream file back so the form can edit it.
func ParseStreamSpec(name, content string) *StreamSpec {
	spec := &StreamSpec{Name: name, Protocol: "tcp", AllowFrom: []string{}}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		trimmed := strings.TrimSpace(sc.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		directive, value := cutDirective(trimmed)
		switch directive {
		case "listen":
			fields := strings.Fields(value)
			if len(fields) == 0 {
				continue
			}
			// The v6 line repeats the port; taking the first keeps one value.
			if spec.Listen == 0 {
				port := fields[0]
				if i := strings.LastIndex(port, ":"); i >= 0 {
					port = port[i+1:]
				}
				spec.Listen, _ = strconv.Atoi(port)
			}
			if len(fields) > 1 && fields[1] == "udp" {
				spec.Protocol = "udp"
			}
		case "server":
			if spec.Upstream == "" {
				spec.Upstream = value
			}
		case "allow":
			spec.AllowFrom = append(spec.AllowFrom, value)
		case "proxy_protocol":
			spec.ProxyProtocol = value == "on"
		case "proxy_timeout":
			spec.Timeout = parseSeconds(value)
		}
	}
	return spec
}

// Streams lists what is configured, and whether nginx is reading it.
func (s *Service) Streams(ctx context.Context) *StreamStatus {
	dir := s.streamDir()
	status := &StreamStatus{
		Dir:     dir,
		Streams: []StreamSpec{},
		Snippet: "stream {\n    include " + dir + "/*.conf;\n}",
	}
	status.Included = streamIncludeFound(s.nginxDir, dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return status
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") || isBackupFile(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		status.Streams = append(status.Streams,
			*ParseStreamSpec(strings.TrimSuffix(e.Name(), ".conf"), string(b)))
	}
	sort.Slice(status.Streams, func(i, j int) bool {
		return status.Streams[i].Listen < status.Streams[j].Listen
	})
	return status
}

// streamIncludeFound looks for a stream block that pulls in our directory.
//
// Reported rather than fixed: nginx.conf is the file every other configuration
// on the host depends on, and a dashboard that edits it silently is one bad
// write away from a server that will not start.
func streamIncludeFound(nginxDir, dir string) bool {
	b, err := os.ReadFile(filepath.Join(nginxDir, "nginx.conf"))
	if err != nil {
		return false
	}
	// Comments are stripped first. The snippet this page prints is exactly
	// what people paste in commented-out while they think about it, and a
	// banner that disappears the moment the include is present-but-inert is
	// worse than no banner: the files go on being written and ignored.
	var live strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		live.WriteString(line)
		live.WriteByte('\n')
	}
	text := live.String()
	if !strings.Contains(text, "stream") {
		return false
	}
	return strings.Contains(text, dir) || strings.Contains(text, "stream.d")
}

// ApplyStream writes a stream and reloads, testing first like everything else.
//
// overwrite is the same guard ApplySite carries and for the same reason: "New
// stream" and "Edit this stream" post to one route, so without it a new one
// named after an existing one replaced it silently — and a forwarding rule
// that quietly stopped pointing where it used to is the worst shape this
// feature can fail in.
func (s *Service) ApplyStream(ctx context.Context, spec *StreamSpec, reload, overwrite bool) (*SiteResult, error) {
	content, err := RenderStream(spec)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.streamDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, spec.Name+".conf")
	original, existed := readIfPresent(path)
	if existed && !overwrite {
		return nil, fmt.Errorf("a stream called %s already exists", spec.Name)
	}
	res := &SiteResult{Name: spec.Name, Path: path, Content: content, Warnings: streamWarnings(spec)}
	s.keepLoaded(path)
	if err := writeAtomic(path, content); err != nil {
		return nil, err
	}
	res.Enabled = true

	res.Validation = runValidator(ctx, "nginx", "-t")
	if !res.Validation.Valid {
		restoreConfig(path, original, existed)
		res.Enabled = false
		return res, ErrInvalidConf
	}
	s.recordChange(ctx, Change{Path: path, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	if reload {
		out, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
		res.Output = strings.TrimSpace(string(out))
		if err != nil {
			return res, fmt.Errorf("reload failed: %s", res.Output)
		}
		res.Reloaded = true
	}
	return res, nil
}

// streamWarnings are the choices that are legal and probably not intended.
func streamWarnings(spec *StreamSpec) []string {
	warnings := []string{}
	if len(spec.AllowFrom) == 0 {
		warnings = append(warnings,
			"A stream has no authentication of any kind — anything that can reach this port is through to the backend. Restrict the source unless the service behind it authenticates for itself.")
	}
	if preset, ok := streamDanger(spec.Listen); ok && len(spec.AllowFrom) == 0 {
		warnings = append(warnings, preset)
	}
	if spec.Protocol == "udp" && spec.ProxyProtocol {
		warnings = append(warnings,
			"The PROXY protocol is a TCP thing. nginx accepts the directive on a UDP listener and the backend will not see the header.")
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

// DeleteStream removes one, keeping the previous content as .bak.
//
// nginx includes stream.d/*.conf, so the backup is inert — and a forwarding
// rule somebody spent ten minutes getting right is worth a file left behind,
// since the delete itself is the only thing this dashboard does to a stream
// that cannot be undone from the form.
func (s *Service) DeleteStream(ctx context.Context, name string) error {
	if !streamNameRe.MatchString(name) || isBackupFile(name) {
		return fmt.Errorf("invalid stream name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.streamDir(), name+".conf")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no such stream: %s", name)
	}
	b, readErr := os.ReadFile(path)
	if readErr == nil {
		os.WriteFile(path+".bak", b, 0o644)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.recordChange(ctx, Change{Path: path, Action: ChangeDelete, Before: b, BeforeExisted: true})
	return nil
}
