package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// PortInUseError is a save refused because something already listens where
// the stream would. nginx -t does not bind, so it passes; the reload then
// fails inside the master with "Address already in use" while the command
// that sent the signal exits 0, and a second stream on a taken port is only a
// warning nginx prints and ignores. Neither says anything to the dashboard,
// which is why the check happens before the file is written.
type PortInUseError struct {
	Port  int
	Proto string
	// Owner is "the stream <name>", or the process holding the socket.
	Owner string
	PID   int32
	// Suggest is the next port up that nothing holds, or 0.
	Suggest int
}

func (e *PortInUseError) Error() string {
	msg := fmt.Sprintf("port %d/%s is already in use by %s", e.Port, e.Proto, e.Owner)
	if e.PID > 0 {
		msg += fmt.Sprintf(" (pid %d)", e.PID)
	}
	if e.Suggest > 0 {
		msg += fmt.Sprintf(" — %d is free", e.Suggest)
	}
	return msg
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

// otherStreamBinds are the sockets every other file in the stream directory
// asks for, by stream name. skip is the file being replaced.
func otherStreamBinds(dir, skip string) map[string][]bind {
	out := map[string][]bind{}
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
		out[strings.TrimSuffix(e.Name(), ".conf")] = parseStreamFile(e.Name(), string(b)).binds
	}
	return out
}

// streamPortConflict refuses a spec whose sockets another stream or another
// program already holds. previous is the file the save replaces; nginx
// holding its sockets is this stream serving, not a conflict. listeners is
// nil when the host's sockets could not be read.
func streamPortConflict(dir string, spec *StreamSpec, previous string, previousBinds []bind, listeners []Listener) error {
	others := otherStreamBinds(dir, previous)
	for _, b := range streamBinds(spec) {
		for name, binds := range others {
			for _, o := range binds {
				if bindsClash(b, o) {
					return &PortInUseError{Port: b.port, Proto: b.proto(), Owner: "the stream " + name,
						Suggest: freePort(spec, others, listeners, previousBinds)}
				}
			}
		}
		for _, l := range listeners {
			if !listenerHolds(l, b) || ownSocket(l, previousBinds) {
				continue
			}
			owner := l.Process
			if owner == "" {
				owner = "another program"
			}
			return &PortInUseError{Port: b.port, Proto: b.proto(), Owner: owner, PID: l.PID,
				Suggest: freePort(spec, others, listeners, previousBinds)}
		}
	}
	return nil
}

// ownSocket is nginx holding a socket for the stream being saved. A listener
// whose owner could not be read is given the benefit of the doubt only when
// the stream already asks for that very socket.
func ownSocket(l Listener, previous []bind) bool {
	if l.Process != "" && l.Process != "nginx" {
		return false
	}
	for _, p := range previous {
		if listenerHolds(l, p) {
			return true
		}
	}
	return false
}

// freePort is the next port above the requested one that neither another
// stream nor another program holds, looked for within a hundred ports.
func freePort(spec *StreamSpec, others map[string][]bind, listeners []Listener, previous []bind) int {
	try := *spec
	for port := spec.Listen + 1; port <= 65535 && port <= spec.Listen+100; port++ {
		try.Listen = port
		taken := false
		for _, b := range streamBinds(&try) {
			for _, binds := range others {
				for _, o := range binds {
					taken = taken || bindsClash(b, o)
				}
			}
			for _, l := range listeners {
				taken = taken || (listenerHolds(l, b) && !ownSocket(l, previous))
			}
		}
		if !taken {
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
