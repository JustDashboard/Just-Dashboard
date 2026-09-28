package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// maxConfigFiles bounds readConfigFiles on a configuration that includes far
// more than any real one does.
const maxConfigFiles = 20000

// readConfigFiles reads nginx.conf and every file its includes can name, from
// disk. It answers the questions that cannot wait for `nginx -T`: the ones
// asked with the service lock held, and the ones that matter most while the
// configuration fails its test — when "is the stream block what broke it?" is
// the question.
//
// Every match of every include is read, dotfiles and all; NginxTree then
// applies nginx's own rules to decide which of them an include takes, so
// reading a file too many costs nothing but the read. An included file that
// does not parse is left out, as nginx would refuse it anyway; the main file
// not parsing is an error.
func readConfigFiles(nginxDir string) ([]ConfigFile, error) {
	main := filepath.Join(nginxDir, "nginx.conf")
	b, err := os.ReadFile(main)
	if err != nil {
		return nil, err
	}
	files := []ConfigFile{{Path: main, Content: string(b)}}
	seen := map[string]bool{main: true}
	for i := 0; i < len(files); i++ {
		directives, err := ParseNginxFile(files[i].Path, files[i].Content, nil)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			files = append(files[:i], files[i+1:]...)
			i--
			continue
		}
		for _, pattern := range includePatterns(directives) {
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(nginxDir, pattern)
			}
			matches, _ := filepath.Glob(globToMatch(filepath.Clean(pattern)))
			for _, match := range matches {
				if seen[match] || len(files) >= maxConfigFiles {
					continue
				}
				seen[match] = true
				if st, err := os.Stat(match); err != nil || !st.Mode().IsRegular() {
					continue
				}
				content, err := os.ReadFile(match)
				if err != nil {
					continue
				}
				files = append(files, ConfigFile{Path: match, Content: string(content)})
			}
		}
	}
	return files, nil
}

func includePatterns(directives []Directive) []string {
	var out []string
	for _, d := range directives {
		if d.Name == "include" && d.Block == nil && len(d.Args) == 1 {
			out = append(out, d.Args[0])
		}
		out = append(out, includePatterns(d.Block)...)
	}
	return out
}

// streamProbe is a directive planted in a file that does not exist, named as
// a new stream would be, to see where nginx would read it.
const streamProbe = "just_dashboard_stream_probe"

// streamIncludeContext is where nginx reads a new file in the stream
// directory: nil with found false when nothing includes it, and otherwise the
// blocks around it — ["stream"] is the one place it works. It asks NginxTree,
// which resolves includes as nginx does, rather than searching the text: a
// search for "stream" matched every `upstream`, and one for "stream.d"
// matched other-stream.d and an include sitting inside http.
func streamIncludeContext(files []ConfigFile, dir string) (context []string, found bool, err error) {
	probe := ConfigFile{Path: filepath.Join(dir, "zz-just-dashboard-probe.conf"), Content: streamProbe + ";\n"}
	tree, err := NginxTree(append(append([]ConfigFile{}, files...), probe))
	if err != nil {
		return nil, false, err
	}
	context, found = contextOf(tree, streamProbe)
	return context, found, nil
}

func contextOf(directives []Directive, name string) ([]string, bool) {
	for _, d := range directives {
		if d.Name == name {
			return d.Context, true
		}
		if context, ok := contextOf(d.Block, name); ok {
			return context, true
		}
	}
	return nil, false
}

// streamInclude is where the stream directory stands in the configuration.
type streamInclude struct {
	included bool
	// misplaced names the blocks the directory is included in when that is
	// not a top-level stream block.
	misplaced string
	err       error
	// files is the configuration it was read from.
	files []ConfigFile
}

func readStreamInclude(nginxDir, dir string) streamInclude {
	files, err := readConfigFiles(nginxDir)
	if err != nil {
		return streamInclude{err: err}
	}
	context, found, err := streamIncludeContext(files, dir)
	switch {
	case err != nil:
		return streamInclude{err: err}
	case !found:
		return streamInclude{files: files}
	case len(context) == 1 && context[0] == "stream":
		return streamInclude{included: true, files: files}
	case len(context) == 0:
		return streamInclude{misplaced: "the top level, outside any block", files: files}
	}
	return streamInclude{misplaced: strings.Join(context, " › "), files: files}
}

// streamIncludeFound reports whether nginx reads the stream directory inside a
// top-level stream block. Never fixed silently: nginx.conf is the file every
// other configuration on the host depends on, so the fix is a change the
// operator is shown and asks for (main_dropin.go).
func streamIncludeFound(nginxDir, dir string) bool {
	return readStreamInclude(nginxDir, dir).included
}

// streamDirRead reports whether nginx reads the stream directory at all,
// wherever it is included. One included inside http is still read — its test
// refuses every stream file there — so a test of such a file is no dry run.
func streamDirRead(nginxDir, dir string) bool {
	include := readStreamInclude(nginxDir, dir)
	return include.included || include.misplaced != ""
}

// streamListeners is ListListeners, replaced in tests.
var streamListeners = ListListeners

// PortOwner is what holds a port a stream asks for: another stream, a site,
// or a program.
type PortOwner struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	// Kind is stream, site or program.
	Kind string `json:"kind"`
	// Name is the stream's name, the site's server name — else its name on
	// the Sites page — or the program's. Empty for a stream or an http server
	// written straight into another file, which File names, and for a
	// program nothing could name.
	Name string `json:"name,omitempty"`
	// Site is the site's name on the Sites page, where it has one.
	Site string `json:"site,omitempty"`
	// File is the configuration file of a stream or a site.
	File string `json:"file,omitempty"`
	PID  int32  `json:"pid,omitempty"`
}

// Kinds of PortOwner.
const (
	OwnerStream  = "stream"
	OwnerSite    = "site"
	OwnerProgram = "program"
)

// String names the owner the way a sentence does.
func (o PortOwner) String() string {
	switch {
	case o.Kind == OwnerStream && o.Name != "":
		return "the stream " + o.Name
	case o.Kind == OwnerStream:
		return "a stream server in " + o.File
	case o.Kind == OwnerSite && o.Name != "":
		return "the site " + o.Name
	case o.Kind == OwnerSite:
		return "an http server in " + o.File
	case o.Name == "":
		return "another program"
	case o.PID > 0:
		return fmt.Sprintf("%s (pid %d)", o.Name, o.PID)
	}
	return o.Name
}

// PortInUseError is a save refused because something already listens where
// the stream would. nginx -t does not bind, so it passes; the reload then
// fails inside the master with "Address in use" while the command that sent
// the signal exits 0, and a second stream on a taken port is only a warning
// nginx prints and ignores. Neither says anything to the dashboard, which is
// why the check happens before the file is written — and why a reload that
// fails on the stream's own port all the same puts the stream back.
type PortInUseError struct {
	PortOwner
	// Suggest is the next port up that nothing holds, or 0.
	Suggest int
	// BindError is nginx's own refusal, when only the reload found the port
	// taken. The stream was then put back as it was.
	BindError string
}

func (e *PortInUseError) Error() string {
	var msg string
	if e.BindError != "" {
		msg = fmt.Sprintf("nginx could not bind port %d/%s when it reloaded: %s", e.Port, e.Proto, e.BindError)
		if e.Kind != "" {
			msg += " — it is held by " + e.PortOwner.String()
		}
		msg += "; the stream was put back as it was"
	} else {
		msg = fmt.Sprintf("port %d/%s is already in use by %s", e.Port, e.Proto, e.PortOwner)
	}
	if e.Suggest > 0 {
		msg += fmt.Sprintf(" — %d is free", e.Suggest)
	}
	return msg
}

// inUse is owner holding the socket b, as a refusal.
func inUse(owner PortOwner, b bind) *PortInUseError {
	owner.Port, owner.Proto = b.port, b.proto()
	return &PortInUseError{PortOwner: owner}
}

func programOwner(name string, pid int32) PortOwner {
	return PortOwner{Kind: OwnerProgram, Name: name, PID: pid}
}

// bind is one socket a stream asks nginx for.
type bind struct {
	addr string
	port int
	udp  bool
}

// streamBinds are the sockets a spec's listen lines ask for: a TCP and a UDP
// socket on each address for a stream of both.
func streamBinds(spec *StreamSpec) []bind {
	var out []bind
	for _, suffix := range streamListenSuffixes(spec.Protocol) {
		udp := suffix != ""
		if spec.Address == "" {
			out = append(out, bind{"0.0.0.0", spec.Listen, udp}, bind{"::", spec.Listen, udp})
		} else {
			out = append(out, bind{spec.Address, spec.Listen, udp})
		}
	}
	return out
}

// proto names a bind's protocol as a port is written: 53/udp.
func (b bind) proto() string {
	if b.udp {
		return "udp"
	}
	return "tcp"
}

// label is a bind as a sentence names it: port 5432/tcp where it takes every
// address, 127.0.0.1:5432/tcp where it takes one.
func (b bind) label() string {
	if ip := net.ParseIP(b.addr); ip != nil && ip.IsUnspecified() {
		return fmt.Sprintf("port %d/%s", b.port, b.proto())
	}
	return fmt.Sprintf("%s/%s", b.address(), b.proto())
}

// bindsLabel names a stream's sockets without repeating the port its two
// wildcards share.
func bindsLabel(binds []bind) string {
	var labels []string
	seen := map[string]bool{}
	for _, b := range binds {
		if label := b.label(); !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	return strings.Join(labels, " and ")
}

// sentence starts a string with a capital, as a sentence that starts with a
// port does.
func sentence(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// bindsClash reports whether nginx could not hold both sockets. nginx's own
// [::] listeners are IPv6-only, so a v4 and a v6 bind never clash; within a
// family a wildcard takes every address.
func bindsClash(a, b bind) bool {
	if a.port != b.port || a.udp != b.udp {
		return false
	}
	ipA, ipB := net.ParseIP(a.addr), net.ParseIP(b.addr)
	if ipA == nil || ipB == nil || (ipA.To4() == nil) != (ipB.To4() == nil) {
		return false
	}
	return ipA.IsUnspecified() || ipB.IsUnspecified() || ipA.Equal(ipB)
}

// listenerHolds reports whether a socket another program holds takes the
// bind. A program's [::] socket is dual-stack unless it asked otherwise,
// which cannot be seen from outside, so it is taken to hold IPv4 too.
func listenerHolds(l Listener, b bind) bool {
	if int(l.Port) != b.port || (l.Protocol == "udp") != b.udp {
		return false
	}
	ip := net.ParseIP(l.Address)
	if ip == nil {
		return isWildcard(l.Address)
	}
	if ip.IsUnspecified() && ip.To4() == nil {
		return true
	}
	return bindsClash(bind{l.Address, b.port, b.udp}, b)
}

// claim is a server block that asks nginx for sockets: a stream, or a site.
type claim struct {
	owner PortOwner
	binds []bind
}

// streamClaims are the sockets every other file in the stream directory asks
// for, read from the directory itself — so a stream staged there before
// nginx reads the directory still has its port. skip is the file being
// replaced.
func streamClaims(dir, skip string) []claim {
	var out []claim
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if path == skip || !streamFileName(e) {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, claim{
			owner: PortOwner{Kind: OwnerStream, Name: strings.TrimSuffix(e.Name(), ".conf"), File: path},
			binds: parseStreamFile(e.Name(), string(b)).binds,
		})
	}
	return out
}

// configClaims are the stream and http server blocks of nginx's
// configuration, in the order nginx reads them. A stream in the stream
// directory is named by its file; a site by its first server name, else its
// file, and by the name the Sites page lists it under where it has one.
func configClaims(tree []Directive, nginxDir, streamDir string) (streams, sites []claim) {
	for _, top := range tree {
		if len(top.Context) != 0 || top.Block == nil {
			continue
		}
		http := top.Name == "http"
		if top.Name != "stream" && !http {
			continue
		}
		for _, d := range top.Block {
			if d.Name != "server" || d.Block == nil {
				continue
			}
			cl := claim{owner: PortOwner{File: d.File}}
			listens, names := 0, []string{}
			for _, inner := range d.Block {
				switch inner.Name {
				case "listen":
					listens++
					if b, ok := listenBind(inner.Args, http); ok && !slices.Contains(cl.binds, b) {
						cl.binds = append(cl.binds, b)
					}
				case "server_name":
					names = append(names, inner.Args...)
				}
			}
			if !http {
				cl.owner.Kind = OwnerStream
				if filepath.Dir(filepath.Clean(d.File)) == filepath.Clean(streamDir) {
					cl.owner.Name = strings.TrimSuffix(filepath.Base(d.File), ".conf")
				}
				streams = append(streams, cl)
				continue
			}
			// An http server with no listen takes nginx's default, *:80.
			if listens == 0 {
				cl.binds = []bind{{"0.0.0.0", 80, false}}
			}
			cl.owner.Kind = OwnerSite
			switch filepath.Dir(filepath.Clean(d.File)) {
			case filepath.Join(nginxDir, "sites-enabled"), filepath.Join(nginxDir, "sites-available"), filepath.Join(nginxDir, "conf.d"):
				cl.owner.Site = filepath.Base(d.File)
			}
			cl.owner.Name = cl.owner.Site
			for _, name := range names {
				if name != "" && name != "_" && name != `""` {
					cl.owner.Name = name
					break
				}
			}
			sites = append(sites, cl)
		}
	}
	return streams, sites
}

// listenBind reads the socket a listen directive of a server nginx reads asks
// for. A stream's `udp` and an http server's `quic` are UDP; an http listen
// with no port is on 80. A unix socket, a host name or a port range is no
// port to compare, and is left out.
func listenBind(args []string, http bool) (bind, bool) {
	if len(args) == 0 {
		return bind{}, false
	}
	b := bind{}
	for _, param := range args[1:] {
		if param == "udp" && !http || param == "quic" && http {
			b.udp = true
		}
	}
	addr, port := "", args[0]
	switch {
	case strings.HasPrefix(port, "unix:"):
		return bind{}, false
	case strings.HasPrefix(port, "["):
		end := strings.Index(port, "]")
		if end < 0 {
			return bind{}, false
		}
		addr, port = port[1:end], strings.TrimPrefix(port[end+1:], ":")
	case strings.Contains(port, ":"):
		i := strings.LastIndex(port, ":")
		addr, port = port[:i], port[i+1:]
	default:
		if _, err := strconv.Atoi(port); err != nil {
			addr, port = port, ""
		}
	}
	if port == "" {
		if !http {
			return bind{}, false
		}
		port = "80"
	}
	if addr == "" || addr == "*" {
		addr = "0.0.0.0"
	}
	n, err := strconv.Atoi(port)
	ip := net.ParseIP(addr)
	if err != nil || n < 1 || n > 65535 || ip == nil {
		return bind{}, false
	}
	b.addr, b.port = ip.String(), n
	return b, true
}

// portClaims is everything that may already hold a port a stream asks for.
type portClaims struct {
	streams []claim
	sites   []claim
	// configRead is whether nginx's configuration could be read for its
	// servers. Without it nginx holding a port is a conflict of its own.
	configRead bool
	view       *socketView
}

// portClaims gathers them: the stream directory's other files, the stream
// and http servers of the configuration nginx reads, and the sockets of the
// running nginx's network namespace. skip is the file a save replaces.
func (s *Service) portClaims(skip string, files []ConfigFile, view *socketView) *portClaims {
	dir := filepath.Clean(s.streamDir())
	c := &portClaims{streams: streamClaims(dir, skip), view: view}
	if files == nil {
		return c
	}
	tree, err := NginxTree(files)
	if err != nil {
		return c
	}
	c.configRead = true
	streams, sites := configClaims(tree, s.nginxDir, dir)
	for _, cl := range streams {
		// The directory's own files were read from it above, included or
		// not.
		if filepath.Dir(filepath.Clean(cl.owner.File)) != dir {
			c.streams = append(c.streams, cl)
		}
	}
	c.sites = sites
	return c
}

// holder is what already holds a socket that takes b, or nil. own are the
// sockets the stream asked for before this save: nginx holding those is the
// stream serving, not a conflict.
func (c *portClaims) holder(ctx context.Context, b bind, own []bind) *PortInUseError {
	for _, list := range [][]claim{c.streams, c.sites} {
		for _, cl := range list {
			for _, o := range cl.binds {
				if bindsClash(b, o) {
					return inUse(cl.owner, b)
				}
			}
		}
	}
	v := c.view
	if v.nginx == nil {
		for _, l := range v.hostListeners(ctx) {
			if listenerHolds(l, b) && !ownSocket(l, own) {
				return inUse(programOwner(l.Process, l.PID), b)
			}
		}
		return nil
	}
	for _, sock := range v.sockets {
		l := sock.listener()
		if !listenerHolds(l, b) {
			continue
		}
		nginx := v.nginxOwns(sock)
		if nginx && heldFor(l, own) {
			continue
		}
		if nginx && v.held != nil {
			// Every port nginx's configuration asks for was named above; one
			// it still holds for a server that is gone is let go at the
			// reload.
			if c.configRead {
				continue
			}
			return inUse(programOwner("nginx", v.nginx.master), b)
		}
		name, pid := v.programOn(ctx, sock)
		return inUse(programOwner(name, pid), b)
	}
	return nil
}

// heldFor reports whether a socket is one of these binds'.
func heldFor(l Listener, binds []bind) bool {
	for _, b := range binds {
		if listenerHolds(l, b) {
			return true
		}
	}
	return false
}

// ownSocket is nginx holding a socket for the stream being saved, where the
// host's listener list is all there is to go on. A listener whose owner could
// not be read is given the benefit of the doubt only when the stream already
// asks for that very socket.
func ownSocket(l Listener, previous []bind) bool {
	return (l.Process == "" || l.Process == "nginx") && heldFor(l, previous)
}

// conflict is the refusal for a spec whose sockets something already holds,
// naming what, with the next port up that nothing does; nil when none is.
func (c *portClaims) conflict(ctx context.Context, spec *StreamSpec, own []bind) *PortInUseError {
	for _, b := range streamBinds(spec) {
		if refused := c.holder(ctx, b, own); refused != nil {
			refused.Suggest = c.freePort(ctx, spec, own)
			return refused
		}
	}
	return nil
}

// freePort is the next port above the requested one that nothing holds,
// looked for within a hundred ports.
func (c *portClaims) freePort(ctx context.Context, spec *StreamSpec, own []bind) int {
	try := *spec
	for port := spec.Listen + 1; port <= 65535 && port <= spec.Listen+100; port++ {
		try.Listen = port
		free := true
		for _, b := range streamBinds(&try) {
			if c.holder(ctx, b, own) != nil {
				free = false
				break
			}
		}
		if free {
			return port
		}
	}
	return 0
}

// readListeners reads the host's sockets for a save, bounded so a slow /proc
// walk cannot hold the save up.
func readListeners(ctx context.Context) ([]Listener, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	listeners, err := streamListeners(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("reading the host's listening sockets took too long")
	}
	return listeners, err
}

// Stream states, as the listing gives them.
const (
	// StreamLive is a stream nginx reads and holds every socket for.
	StreamLive = "live"
	// StreamNotListening is one nginx reads and holds no socket for: another
	// program or a site has its port, or nginx has not taken it up.
	StreamNotListening = "not-listening"
	// StreamShadowed is one whose port a stream nginx reads first has:
	// nginx warns, and gives that one every connection.
	StreamShadowed = "shadowed"
	// StreamNotRead is one nginx does not read as a stream.
	StreamNotRead = "not-read"
	// StreamUnknown is one whose state could not be read.
	StreamUnknown = "unknown"
)

// fillStreamStates says what nginx does with each listed stream: first from
// the configuration nginx reads — whether the file is in its stream block,
// whether a stream read before it has its port, whether a site has — and
// then from the running nginx's sockets and its error log.
func (s *Service) fillStreamStates(ctx context.Context, status *StreamStatus, include streamInclude) {
	var todo []*StreamEntry
	for i := range status.Streams {
		entry := &status.Streams[i]
		if entry.Error != "" {
			entry.State, entry.StateReason = StreamUnknown, "The file could not be read: "+entry.Error+"."
			continue
		}
		todo = append(todo, entry)
	}
	if len(todo) == 0 {
		return
	}
	tree, err := NginxTree(include.files)
	if include.err != nil {
		err = include.err
	}
	if err != nil {
		for _, entry := range todo {
			entry.State, entry.StateReason = StreamUnknown, "Whether nginx reads it could not be told: nginx.conf could not be read ("+err.Error()+")."
		}
		return
	}
	streams, sites := configClaims(tree, s.nginxDir, status.Dir)
	read := streamBlockFiles(tree)
	binds := map[string][]bind{}
	for _, cl := range streams {
		file := filepath.Clean(cl.owner.File)
		binds[file] = append(binds[file], cl.binds...)
	}
	shadows := shadowedStreams(streams)

	var live []*StreamEntry
	for _, entry := range todo {
		path := filepath.Clean(entry.Path)
		switch {
		case moduleMissing(status.Module):
			entry.State, entry.StateReason = StreamNotRead, "This nginx has no stream module, so it cannot read a stream."
		case !read[path]:
			entry.State, entry.StateReason = StreamNotRead, notReadReason(status, entry)
		case shadows[path] != nil:
			first := shadows[path]
			entry.State, entry.Blocker = StreamShadowed, &first.owner
			entry.StateReason = fmt.Sprintf("%s is taken first by %s, so nginx gives that stream every connection there and ignores this one's listen.",
				sentence(first.bind.label()), first.owner)
		default:
			if site, b, ok := siteClash(binds[path], sites); ok {
				entry.State, entry.Blocker = StreamNotListening, &site
				entry.StateReason = fmt.Sprintf("%s is also where %s listens. nginx cannot bind it for both, so every reload fails until one of them moves.",
					sentence(b.label()), site)
				continue
			}
			live = append(live, entry)
		}
	}
	if len(live) == 0 {
		return
	}
	view := readNginx(ctx, s, include.files, 0)
	if view.nginx == nil {
		for _, entry := range live {
			entry.State, entry.StateReason = StreamUnknown, "Whether nginx listens for it could not be checked: "+view.why+"."
		}
		return
	}
	logged := func() func() map[string]bindFailure {
		var failures map[string]bindFailure
		return func() map[string]bindFailure {
			if failures == nil {
				failures = map[string]bindFailure{}
				build, err := s.nginxBuildInfo(ctx)
				if err != nil {
					return failures
				}
				if path, err := s.errorLogFile(tree, build); err == nil {
					failures, _ = lastBindFailures(path)
				}
			}
			return failures
		}
	}()
	for _, entry := range live {
		socketState(ctx, entry, binds[filepath.Clean(entry.Path)], view, logged)
	}
}

// streamBlockFiles are the files nginx reads inside its top-level stream
// block: where a stream is read as one.
func streamBlockFiles(tree []Directive) map[string]bool {
	read := map[string]bool{}
	for _, top := range tree {
		if top.Name == "stream" && len(top.Context) == 0 {
			for _, d := range top.Block {
				read[filepath.Clean(d.File)] = true
			}
		}
	}
	return read
}

// notReadReason is why nginx does not read a stream file it could.
func notReadReason(status *StreamStatus, entry *StreamEntry) string {
	for _, what := range entry.Unsupported {
		if strings.HasPrefix(what, "a syntax error") {
			return "It has " + what + ", so nginx's configuration test fails on it and every reload is refused until it is fixed or deleted."
		}
	}
	switch {
	case status.IncludedIn != "":
		place := "inside " + status.IncludedIn
		if strings.HasPrefix(status.IncludedIn, "the top level") {
			place = "at " + status.IncludedIn
		}
		return "nginx reads the stream directory " + place + ", where a stream is refused."
	case !status.Included:
		return "nginx.conf does not include the stream directory, so nginx does not read this file."
	}
	return "The include that reads the stream directory does not take this file."
}

// shadow is a stream whose port another, read before it, already has.
type shadow struct {
	owner PortOwner
	bind  bind
}

// shadowedStreams are the stream files nginx reads with a listen another
// stream read before them already has, by file. nginx warns "conflicting
// server name" and hands every connection to the first. A wildcard and one
// address on the same port are not a shadow: nginx binds the wildcard and
// gives each server its own addresses.
func shadowedStreams(streams []claim) map[string]*shadow {
	first := map[bind]PortOwner{}
	out := map[string]*shadow{}
	for _, cl := range streams {
		file := filepath.Clean(cl.owner.File)
		for _, b := range cl.binds {
			earlier, taken := first[b]
			if !taken {
				first[b] = cl.owner
				continue
			}
			if filepath.Clean(earlier.File) != file && out[file] == nil {
				earlier.Port, earlier.Proto = b.port, b.proto()
				out[file] = &shadow{owner: earlier, bind: b}
			}
		}
	}
	return out
}

// siteClash is a site nginx reads that listens where the stream does. One
// nginx cannot bind a socket for its http and its stream module both: nginx
// -t passes, and every reload fails on the second bind.
func siteClash(binds []bind, sites []claim) (PortOwner, bind, bool) {
	for _, b := range binds {
		for _, site := range sites {
			for _, o := range site.binds {
				if bindsClash(b, o) {
					owner := site.owner
					owner.Port, owner.Proto = b.port, b.proto()
					return owner, b, true
				}
			}
		}
	}
	return PortOwner{}, bind{}, false
}

// socketState reads a stream nginx reads from the running nginx: live where
// nginx holds a socket for every bind, and otherwise why not — a program
// holding the port, a bind nginx logged as failed, or a configuration nginx
// loaded before the file changed.
func socketState(ctx context.Context, entry *StreamEntry, binds []bind, view *socketView, logged func() map[string]bindFailure) {
	changed := false
	if info, err := os.Stat(entry.Path); err == nil && !view.nginx.loaded.IsZero() {
		changed = info.ModTime().After(view.nginx.loaded)
	}
	for _, b := range binds {
		if servedBy(view, b) {
			continue
		}
		entry.State = StreamNotListening
		if failure, ok := logged()[b.address()]; ok {
			entry.BindError = strings.TrimSpace(failure.when + " " + failure.text)
		}
		for _, sock := range view.sockets {
			if listenerHolds(sock.listener(), b) && !view.nginxOwns(sock) {
				name, pid := view.programOn(ctx, sock)
				owner := programOwner(name, pid)
				owner.Port, owner.Proto = b.port, b.proto()
				entry.Blocker = &owner
				entry.StateReason = fmt.Sprintf("%s is held by %s, so nginx cannot bind it, and every reload fails until it is free.",
					sentence(b.label()), owner)
				return
			}
		}
		switch {
		case entry.BindError != "":
			entry.StateReason = fmt.Sprintf("nginx holds no socket for %s: the last time it tried to bind one, it could not. Nothing holds the port now, so a reload should bind it.", b.label())
		case changed:
			entry.StateReason = fmt.Sprintf("nginx holds no socket for %s: it last loaded its configuration before this file changed.", b.label())
		default:
			entry.StateReason = fmt.Sprintf("nginx holds no socket for %s.", b.label())
		}
		return
	}
	if len(binds) == 0 {
		entry.State, entry.StateReason = StreamNotListening, "nginx reads it, but it asks for no port nginx can bind."
		return
	}
	entry.State = StreamLive
	if changed {
		entry.StateReason = "Changed after nginx last loaded its configuration: it forwards the earlier version until nginx reloads."
	}
}

// servedBy reports whether nginx holds a socket that takes b.
func servedBy(view *socketView, b bind) bool {
	for _, sock := range view.sockets {
		if serves(sock, b) && view.nginxOwns(sock) {
			return true
		}
	}
	return false
}
