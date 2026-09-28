package proxysvc

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A stream nginx's test passed and `nginx -s reload` accepted may still
// forward nothing. The reload command only signals the master; the master
// then opens every socket the new configuration asks for, and where one is
// taken — by another program, or by an http server of nginx's own on the same
// port — it logs `bind() to 0.0.0.0:5432 failed (98: Address in use)`, tries
// four more times, gives up with "still could not bind()" and goes on serving
// the configuration it had. The command that sent the signal has long since
// exited 0. So whether a stream is live is asked of the running nginx itself:
// the sockets of its network namespace, which of them its master holds, and
// what it wrote to its error log.

// procDir is where the kernel shows processes.
const procDir = "/proc"

// userHZ is the clock tick /proc counts process start times in. Linux fixes
// it at 100 for userspace on every architecture it runs this on.
const userHZ = 100

// buildTTL is how long what `nginx -V` said is kept: it changes only with the
// binary.
const buildTTL = time.Minute

var builds = struct {
	sync.Mutex
	by map[*Service]keptBuild
}{by: map[*Service]keptBuild{}}

type keptBuild struct {
	at    time.Time
	build streamNginxBuild
}

// nginxBuildInfo is `nginx -V` for this host's nginx, kept for a minute.
func (s *Service) nginxBuildInfo(ctx context.Context) (streamNginxBuild, error) {
	builds.Lock()
	kept, ok := builds.by[s]
	builds.Unlock()
	if ok && time.Since(kept.at) < buildTTL {
		return kept.build, nil
	}
	out, err := hostexec.Command(ctx, "nginx", "-V").CombinedOutput()
	if err != nil {
		return streamNginxBuild{}, fmt.Errorf("nginx -V: %s", strings.TrimSpace(firstLine(string(out))+" "+err.Error()))
	}
	build := parseStreamNginxBuild(string(out))
	builds.Lock()
	builds.by[s] = keptBuild{at: time.Now(), build: build}
	builds.Unlock()
	return build, nil
}

// streamNginxProcess is the running nginx that reads this configuration.
type streamNginxProcess struct {
	master int32
	uid    uint32
	// workers are the worker processes serving now. A reload that takes
	// starts new ones, so a worker that was not there before is the proof.
	workers []int32
	// loaded is when the newest worker started: the last time nginx took up
	// a configuration.
	loaded time.Time
}

// procStat is what /proc/<pid>/stat says that the search needs.
type procStat struct {
	pid  int32
	comm string
	ppid int32
	// start is clock ticks after boot.
	start uint64
}

// parseStreamProcStat reads /proc/<pid>/stat. The name sits in parentheses and may
// hold spaces and parentheses of its own, so fields are counted from the last
// ")": the state, the parent, and the start time twenty fields on.
func parseStreamProcStat(content string) (procStat, bool) {
	open, end := strings.IndexByte(content, '('), strings.LastIndexByte(content, ')')
	if open < 0 || end < open {
		return procStat{}, false
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(content[:open]), 10, 32)
	fields := strings.Fields(content[end+1:])
	if err != nil || len(fields) < 20 {
		return procStat{}, false
	}
	ppid, err := strconv.ParseInt(fields[1], 10, 32)
	if err != nil {
		return procStat{}, false
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procStat{}, false
	}
	return procStat{pid: int32(pid), comm: content[open+1 : end], ppid: int32(ppid), start: start}, true
}

// masterConfig is the configuration a master process was started on, read
// from its title — nginx rewrites its command line to "nginx: master process
// /usr/sbin/nginx -c /etc/nginx/nginx.conf" — and resolved as nginx resolves
// it: a relative -c against -p, or else against the directory of the build's
// own configuration, which is also the file read when there is no -c.
func masterConfig(cmdline string, build streamNginxBuild) (string, bool) {
	title := strings.TrimSpace(strings.ReplaceAll(cmdline, "\x00", " "))
	rest, ok := strings.CutPrefix(title, "nginx: master process")
	if !ok {
		return "", false
	}
	args := strings.Fields(rest)
	conf, prefix := "", ""
	for i := 1; i < len(args); i++ {
		for flag, value := range map[string]*string{"-c": &conf, "-p": &prefix} {
			switch {
			case args[i] == flag && i+1 < len(args):
				*value = args[i+1]
			case strings.HasPrefix(args[i], flag) && len(args[i]) > len(flag):
				*value = args[i][len(flag):]
			}
		}
	}
	if conf == "" {
		return build.confPath, true
	}
	if !filepath.IsAbs(conf) {
		base := prefix
		if base == "" {
			base = filepath.Dir(build.confPath)
		}
		conf = filepath.Join(base, conf)
	}
	return filepath.Clean(conf), true
}

// streamSameFile reports whether two paths name one file, through any symbolic
// link in either.
func streamSameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// readNginx reads what the running nginx listens on: found afresh when master
// is 0, or read again from a master already found. Replaced in tests.
var readNginx = func(ctx context.Context, s *Service, files []ConfigFile, master int32) *socketView {
	return s.readNginxSockets(ctx, files, master)
}

// readNginxSockets finds the running nginx and reads its sockets. Where no
// nginx reading this configuration is running — or none can be seen — the
// host's listener list stands in for the port checks, which name nginx by
// its process name, and the states say why they could not be checked.
func (s *Service) readNginxSockets(ctx context.Context, files []ConfigFile, master int32) *socketView {
	var proc *streamNginxProcess
	var err error
	if master > 0 {
		proc, err = readNginxProcess(master)
	} else {
		proc, err = s.runningNginx(ctx, files)
	}
	switch {
	case err != nil:
		return listenerView(err.Error())
	case proc == nil:
		return listenerView(fmt.Sprintf("no running nginx reads %s", filepath.Join(s.nginxDir, "nginx.conf")))
	}
	sockets, err := readSockets(proc.master)
	if err != nil {
		return listenerView("nginx's sockets could not be read: " + err.Error())
	}
	view := &socketView{nginx: proc, sockets: sockets}
	// A master whose descriptors cannot be read — the dashboard runs as
	// another user — leaves held nil, and nginx's sockets are told by owner.
	view.held, _ = socketInodes(proc.master)
	view.local = sameNetwork(proc.master, sockets)
	return view
}

// listenerView is the view of a host whose running nginx could not be read:
// the host's listener list, read when a port check first needs it.
func listenerView(why string) *socketView {
	return &socketView{why: why, local: true}
}

// runningNginx finds the master process of the nginx that reads this
// configuration. Every master names its configuration in its title, so one
// started on another file — a second nginx, another slot's — is not taken for
// this one. Two that read the same file — a container's nginx beside the
// host's, each seeing its own /etc/nginx — are told apart by the pid file.
func (s *Service) runningNginx(ctx context.Context, files []ConfigFile) (*streamNginxProcess, error) {
	build, err := s.nginxBuildInfo(ctx)
	if err != nil {
		return nil, err
	}
	want := filepath.Join(s.nginxDir, "nginx.conf")
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}
	var candidates []int32
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "stat"))
		if err != nil {
			continue
		}
		st, ok := parseStreamProcStat(string(b))
		if !ok || st.comm != "nginx" {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(procDir, e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if conf, ok := masterConfig(string(cmdline), build); ok && streamSameFile(conf, want) {
			candidates = append(candidates, st.pid)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return readNginxProcess(candidates[0])
	}
	pid := s.pidFromFile(ctx, files, build)
	for _, candidate := range candidates {
		if candidate == pid {
			return readNginxProcess(pid)
		}
	}
	return nil, fmt.Errorf("%d nginx masters read %s and its pid file names none of them", len(candidates), want)
}

// pidFromFile is the master's pid as nginx wrote it: at the configuration's
// pid directive, or the build's. On a host where the dashboard runs in a
// container the file is on the host's /run, which is read there.
func (s *Service) pidFromFile(ctx context.Context, files []ConfigFile, build streamNginxBuild) int32 {
	file := build.pidPath
	if tree, err := NginxTree(files); err == nil {
		for _, d := range tree {
			if d.Name == "pid" && len(d.Context) == 0 && len(d.Args) == 1 {
				file = d.Args[0]
				if !filepath.IsAbs(file) {
					file = filepath.Join(build.prefix, file)
				}
			}
		}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		b, err = hostexec.CommandOnHost(ctx, "cat", file).Output()
	}
	if err != nil {
		return 0
	}
	pid, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 32)
	return int32(pid)
}

// readNginxProcess reads a master and the workers it started, from its own
// list of children.
func readNginxProcess(master int32) (*streamNginxProcess, error) {
	dir := filepath.Join(procDir, strconv.Itoa(int(master)))
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("the nginx master %d is gone", master)
	}
	proc := &streamNginxProcess{master: master}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		proc.uid = st.Uid
	}
	boot := bootTime()
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return nil, err
	}
	if st, ok := parseStreamProcStat(string(stat)); ok {
		proc.loaded = startedAt(boot, st.start)
	}
	children, _ := os.ReadFile(filepath.Join(dir, "task", strconv.Itoa(int(master)), "children"))
	for _, field := range strings.Fields(string(children)) {
		pid, err := strconv.ParseInt(field, 10, 32)
		if err != nil {
			continue
		}
		child := filepath.Join(procDir, field)
		cmdline, err := os.ReadFile(filepath.Join(child, "cmdline"))
		title := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
		// A worker still serving connections from before the last reload
		// calls itself "shutting down"; it is not what nginx loaded last.
		if err != nil || !strings.HasPrefix(title, "nginx: worker process") || strings.Contains(title, "shutting down") {
			continue
		}
		proc.workers = append(proc.workers, int32(pid))
		if b, err := os.ReadFile(filepath.Join(child, "stat")); err == nil {
			if st, ok := parseStreamProcStat(string(b)); ok {
				if at := startedAt(boot, st.start); at.After(proc.loaded) {
					proc.loaded = at
				}
			}
		}
	}
	return proc, nil
}

// bootTime is when the host booted, which /proc counts start times from.
func bootTime() time.Time {
	b, err := os.ReadFile(filepath.Join(procDir, "stat"))
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			if secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); err == nil {
				return time.Unix(secs, 0)
			}
		}
	}
	return time.Time{}
}

func startedAt(boot time.Time, ticks uint64) time.Time {
	if boot.IsZero() {
		return time.Time{}
	}
	return boot.Add(time.Duration(ticks) * time.Second / userHZ)
}

// socket is one listening socket of a network namespace, as /proc/net shows
// it: a TCP socket listening, or a UDP one bound and not connected.
type socket struct {
	udp   bool
	addr  string
	port  int
	inode uint64
	uid   uint32
}

// listener is the socket as the host's listener list has it, to ask the same
// questions of either.
func (s socket) listener() Listener {
	proto := "tcp"
	if s.udp {
		proto = "udp"
	}
	return Listener{Protocol: proto, Address: s.addr, Port: uint32(s.port)}
}

// readSockets reads the listening sockets of the network namespace a process
// is in. /proc/<pid>/net is readable by anyone, which is what lets a
// dashboard see into the namespace of an nginx it shares none with.
func readSockets(pid int32) ([]socket, error) {
	var out []socket
	for _, table := range []struct {
		file    string
		udp, v6 bool
	}{{"tcp", false, false}, {"tcp6", false, true}, {"udp", true, false}, {"udp6", true, true}} {
		b, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(int(pid)), "net", table.file))
		if err != nil {
			if table.v6 && errors.Is(err, fs.ErrNotExist) {
				continue // a kernel without IPv6
			}
			return nil, err
		}
		out = append(out, parseSocketTable(string(b), table.udp)...)
	}
	return out, nil
}

// parseSocketTable reads one of /proc/net/{tcp,tcp6,udp,udp6}: the local
// address, the state — 0A is LISTEN — the owner's uid and the inode.
func parseSocketTable(content string, udp bool) []socket {
	var out []socket
	lines := strings.Split(content, "\n")
	for _, line := range lines[min(1, len(lines)):] {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		if udp && !strings.HasSuffix(f[2], ":0000") || !udp && f[3] != "0A" {
			continue
		}
		addr, port, ok := decodeSocketAddress(f[1])
		if !ok {
			continue
		}
		uid, _ := strconv.ParseUint(f[7], 10, 32)
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		out = append(out, socket{udp: udp, addr: addr, port: port, inode: inode, uid: uint32(uid)})
	}
	return out
}

// decodeSocketAddress reads "0100007F:1F90" as 127.0.0.1 and 8080. The
// kernel prints each 32-bit word of the address as the number it is in the
// machine's own byte order, and the port as a number.
func decodeSocketAddress(field string) (string, int, bool) {
	host, portHex, ok := strings.Cut(field, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	raw, errHex := hex.DecodeString(host)
	if err != nil || errHex != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, false
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(ip[i:i+4], binary.BigEndian.Uint32(raw[i:i+4]))
	}
	return ip.String(), int(port), true
}

// socketInodes are the sockets a process holds, by inode. nginx's master
// opens every listening socket and keeps it, so the master's are enough.
func socketInodes(pid int32) (map[uint64]bool, error) {
	dir := filepath.Join(procDir, strconv.Itoa(int(pid)), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	held := map[uint64]bool{}
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if inode, ok := strings.CutPrefix(link, "socket:["); ok {
			if n, err := strconv.ParseUint(strings.TrimSuffix(inode, "]"), 10, 64); err == nil {
				held[n] = true
			}
		}
	}
	return held, nil
}

// sameNetwork reports whether a process shares the dashboard's network
// namespace, where the host's listener list names who holds a socket. The
// namespace links say so where they can be read; else two namespaces are one
// when their tables share a socket, since an inode is in one namespace only.
func sameNetwork(pid int32, theirs []socket) bool {
	own, errOwn := os.Readlink(filepath.Join(procDir, "self", "ns", "net"))
	other, errOther := os.Readlink(filepath.Join(procDir, strconv.Itoa(int(pid)), "ns", "net"))
	if errOwn == nil && errOther == nil {
		return own == other
	}
	ours, err := readSockets(int32(os.Getpid()))
	if err != nil {
		return false
	}
	if len(ours) == 0 && len(theirs) == 0 {
		return true
	}
	inodes := map[uint64]bool{}
	for _, s := range ours {
		inodes[s.inode] = true
	}
	for _, s := range theirs {
		if inodes[s.inode] {
			return true
		}
	}
	return false
}

// socketView is what the running nginx listens on.
type socketView struct {
	// nginx is nil when no running nginx reading this configuration could be
	// read; why says why, and listeners are the host's own instead.
	nginx *streamNginxProcess
	why   string
	// sockets are the listening sockets of nginx's network namespace.
	sockets []socket
	// held are the socket inodes nginx's master holds. Nil where its
	// descriptors cannot be read — the dashboard runs as another user — and
	// a socket owned by nginx's user is then taken to be nginx's.
	held map[uint64]bool
	// local is whether nginx's network namespace is the dashboard's, where
	// the host's listener list can name who holds a socket.
	local bool

	listeners  []Listener
	listenErr  error
	listenRead bool
}

// nginxOwns reports whether nginx holds a socket.
func (v *socketView) nginxOwns(s socket) bool {
	if v.held != nil {
		return v.held[s.inode]
	}
	return s.uid == v.nginx.uid
}

// hostListeners are the dashboard's own namespace's sockets with their
// owners, read once and only when a name is needed: walking every process's
// descriptors is the slow part of all this.
func (v *socketView) hostListeners(ctx context.Context) []Listener {
	if !v.listenRead {
		v.listeners, v.listenErr = readListeners(ctx)
		v.listenRead = true
	}
	return v.listeners
}

// programOn names the program holding a socket nginx does not, where the
// host's listener list can: only in nginx's own network namespace.
func (v *socketView) programOn(ctx context.Context, s socket) (string, int32) {
	if !v.local {
		return "", 0
	}
	want := s.listener()
	for _, l := range v.hostListeners(ctx) {
		if l.Protocol == want.Protocol && l.Address == want.Address && l.Port == want.Port {
			return l.Process, l.PID
		}
	}
	return "", 0
}

// serves reports whether a socket takes a stream's bind the way nginx shares
// one: the same address, or the wildcard of its family. nginx opens its [::]
// listens IPv6-only, so a [::] socket of nginx's never takes an IPv4 bind.
func serves(s socket, b bind) bool {
	if s.port != b.port || s.udp != b.udp {
		return false
	}
	if s.addr == b.addr {
		return true
	}
	ip, want := net.ParseIP(s.addr), net.ParseIP(b.addr)
	return ip != nil && want != nil && ip.IsUnspecified() && (ip.To4() == nil) == (want.To4() == nil)
}

// servedByNginx reports whether nginx holds a socket that takes every bind.
func (v *socketView) servedByNginx(binds []bind) bool {
	for _, b := range binds {
		found := false
		for _, s := range v.sockets {
			if serves(s, b) && v.nginxOwns(s) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(binds) > 0
}

// errorLogRoots are where nginx's error log is read from: the system's logs,
// and the nginx directory the dashboard already edits. A configuration
// pointing error_log anywhere else is not followed — the dashboard runs as
// root, and the states it reads are shown to every account.
func (s *Service) errorLogRoots() []string {
	return []string{"/var/log", s.nginxDir}
}

// errorLogFile is the file nginx writes its errors to — the configuration's
// first error_log at the top level, or the build's — when it is one the
// dashboard may read.
func (s *Service) errorLogFile(tree []Directive, build streamNginxBuild) (string, error) {
	target := ""
	for _, d := range tree {
		if d.Name == "error_log" && len(d.Context) == 0 && len(d.Args) > 0 && streamLogFile(d.Args[0]) {
			target = d.Args[0]
			break
		}
	}
	if target == "" {
		target = build.errorLog
	}
	if !streamLogFile(target) {
		return "", fmt.Errorf("nginx writes its errors to %s, not to a file", target)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(build.prefix, target)
	}
	resolved := resolveExisting(target)
	for _, root := range s.errorLogRoots() {
		root = resolveExisting(root)
		if strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return target, nil
		}
	}
	return "", fmt.Errorf("nginx's error log, %s, is outside /var/log and the nginx directory, so it is not read", target)
}

// streamLogFile tells a file from nginx's other log targets.
func streamLogFile(target string) bool {
	return target != "stderr" && target != "/dev/null" &&
		!strings.HasPrefix(target, "syslog:") && !strings.HasPrefix(target, "memory:")
}

// resolveExisting is a path with its symbolic links resolved as far as the
// part of it that exists.
func resolveExisting(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolveExisting(parent), filepath.Base(path))
}

// bindFailure is one `bind() … failed` nginx logged.
type bindFailure struct {
	// address is as nginx writes it: 0.0.0.0:5432, [::]:5432.
	address string
	// text is nginx's words from bind() on; when is its timestamp.
	text string
	when string
}

var bindFailureRe = regexp.MustCompile(`^(?:(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d) )?.*?(bind\(\) to (\S+) failed \(\d+: [^)]*\))`)

// parseBindFailure reads one error log line, if it is a failed bind.
func parseBindFailure(line string) (bindFailure, bool) {
	m := bindFailureRe.FindStringSubmatch(line)
	if m == nil {
		return bindFailure{}, false
	}
	return bindFailure{when: m[1], text: m[2], address: m[3]}, true
}

// address is a bind as nginx writes it in its log.
func (b bind) address() string {
	if strings.Contains(b.addr, ":") {
		return fmt.Sprintf("[%s]:%d", b.addr, b.port)
	}
	return fmt.Sprintf("%s:%d", b.addr, b.port)
}

// errorLogTail is how much of the end of the error log the listing reads.
const errorLogTail = 256 << 10

// lastBindFailures are the last bind() failure nginx logged for each
// address, from the end of its error log.
func lastBindFailures(path string) (map[string]bindFailure, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	from := max(info.Size()-errorLogTail, 0)
	b, err := io.ReadAll(io.NewSectionReader(f, from, info.Size()-from))
	if err != nil {
		return nil, err
	}
	out := map[string]bindFailure{}
	for _, line := range strings.Split(string(b), "\n") {
		if failure, ok := parseBindFailure(line); ok {
			out[failure.address] = failure
		}
	}
	return out, nil
}

// appendedLines are the whole lines written to a log after offset, and where
// the next read starts. A log that shrank was rotated and is read from its
// start.
func appendedLines(path string, offset int64) ([]string, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset
	}
	if info.Size() < offset {
		offset = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, offset, min(info.Size()-offset, 1<<20)))
	if err != nil {
		return nil, offset
	}
	end := strings.LastIndexByte(string(b), '\n')
	if end < 0 {
		return nil, offset
	}
	return strings.Split(string(b[:end]), "\n"), offset + int64(end) + 1
}

// listenWait is how long a save waits for nginx to take its reload up. The
// master opens the new sockets as soon as it has read the configuration, and
// logs a port it cannot bind at the first of its five attempts.
var listenWait = 3 * time.Second

// reloadMark is the running nginx, and the end of its error log, just before
// a reload.
type reloadMark struct {
	nginx *streamNginxProcess
	why   string
	log   string
	// offset is the log's length before the reload: what nginx writes after
	// it is about this reload.
	offset int64
}

// markReload notes what the reload is to be measured against.
func (s *Service) markReload(ctx context.Context, files []ConfigFile) reloadMark {
	view := readNginx(ctx, s, files, 0)
	if view.nginx == nil {
		return reloadMark{why: view.why}
	}
	mark := reloadMark{nginx: view.nginx}
	build, err := s.nginxBuildInfo(ctx)
	tree, treeErr := NginxTree(files)
	if err != nil || treeErr != nil {
		return mark
	}
	if path, err := s.errorLogFile(tree, build); err == nil {
		mark.log = path
		if info, err := os.Stat(path); err == nil {
			mark.offset = info.Size()
		}
	}
	return mark
}

// listenCheck is what a reload did to a stream's sockets.
type listenCheck struct {
	// checked is false when the running nginx could not be read; note says
	// why.
	checked   bool
	listening bool
	// bindError is nginx refusing to bind one of the stream's own sockets:
	// the reload failed on it, and nginx kept the configuration it had.
	bindError string
	// failure is the first thing nginx logged when the reload failed on
	// something else — another stream's port, a site's.
	failure string
	note    string
}

// awaitListening watches the running nginx take a reload up, for listenWait
// at most: done when a new worker has started and nginx holds every socket
// the stream asks for, failed when nginx logs an error.
func (s *Service) awaitListening(ctx context.Context, mark reloadMark, binds []bind) listenCheck {
	if mark.nginx == nil {
		return listenCheck{note: "Whether nginx took it up could not be checked: " + mark.why + "."}
	}
	deadline := time.Now().Add(listenWait)
	offset := mark.offset
	var logged []string
	for {
		var fresh []string
		if mark.log != "" {
			fresh, offset = appendedLines(mark.log, offset)
			logged = append(logged, emergencies(fresh)...)
		}
		if len(logged) > 0 {
			// A failed attempt logs every socket it could not bind before it
			// waits to try again; the rest of that attempt is read first.
			time.Sleep(150 * time.Millisecond)
			fresh, _ = appendedLines(mark.log, offset)
			logged = append(logged, emergencies(fresh)...)
			for _, line := range logged {
				failure, ok := parseBindFailure(line)
				if !ok {
					continue
				}
				for _, b := range binds {
					if failure.address == b.address() {
						return listenCheck{checked: true, bindError: failure.text}
					}
				}
			}
			return listenCheck{checked: true, failure: emergencyText(logged[0])}
		}
		view := readNginx(ctx, s, nil, mark.nginx.master)
		reloaded := view.nginx != nil && startedAnew(mark.nginx, view.nginx)
		if reloaded && view.servedByNginx(binds) {
			return listenCheck{checked: true, listening: true}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			note := fmt.Sprintf("nginx had not taken the reload up %s after it was sent.", listenWait)
			if reloaded {
				note = fmt.Sprintf("nginx took the reload up but holds no socket for %s.", bindsLabel(binds))
			}
			return listenCheck{checked: true, note: note}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// startedAnew reports a worker that was not serving before: a reload nginx
// took up.
func startedAnew(before, after *streamNginxProcess) bool {
	seen := map[int32]bool{}
	for _, pid := range before.workers {
		seen[pid] = true
	}
	for _, pid := range after.workers {
		if !seen[pid] {
			return true
		}
	}
	return false
}

// emergencies are the lines nginx logged at [emerg]: what makes a reload
// fail.
func emergencies(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.Contains(line, "[emerg]") {
			out = append(out, line)
		}
	}
	return out
}

var logPrefixRe = regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d \[emerg\] \d+#\d+: (?:\*\d+ )?`)

// emergencyText is a logged line without its timestamp, level and pids.
func emergencyText(line string) string {
	return strings.TrimSpace(logPrefixRe.ReplaceAllString(line, ""))
}
