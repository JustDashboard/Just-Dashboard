package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"golang.org/x/sys/unix"
)

// The wake's own listener. The proxy asks the dashboard to wake an
// application over a Unix socket rather than the dashboard's API: the API's
// routes all sit behind the network allowlist and a session, and a socket in
// a directory only the proxy reads is reachable by the proxy and nothing
// else — no address is bound at all, routable or not. The socket is where
// the proxy can reach it (proxysvc.WakeSockets): beside host nginx's sites,
// or inside the volume the Docker Caddy ingress keeps its configuration in.
//
// The socket answers one thing, GET /wake/<route>, and only to a peer whose
// process is the proxy (SO_PEERCRED, then /proc/<pid>/comm): the dashboard
// runs in the host's process namespace, so the ingress container's Caddy is
// a process it can see. What it does — start an application asleep by
// design — is what any visitor's request to the domain does anyway.

var wakePathRe = regexp.MustCompile(`^/wake/(just-dashboard-env-(\d+)\.conf)$`)

type peerKey struct{}

// ServeHTTP is the wake: 200 once the application answers (with its address
// for Caddy), 401 while it is still starting after the hold, 403 when there
// is nothing asleep to wake.
func (s *Sleeper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if peer, ok := r.Context().Value(peerKey{}).(string); ok && !wakePeerAllowed(peer) {
		s.log.Warn("refused a wake from a process that is not the proxy", "process", peer)
		http.Error(w, "only the proxy wakes an application", http.StatusForbidden)
		return
	}
	match := wakePathRe.FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodGet || match == nil {
		http.Error(w, "not a wake", http.StatusNotFound)
		return
	}
	environmentID, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil || deploymentRouteName(environmentID) != match[1] {
		http.Error(w, "not a wake", http.StatusNotFound)
		return
	}
	hold := s.HoldFor(r.Context(), environmentID)
	host := r.Header.Get("X-Forwarded-Host")
	if len(host) > 253 || strings.ContainsAny(host, " \r\n\t/") {
		host = ""
	}
	outcome := s.Wake(r.Context(), environmentID, WakeRequest{Trigger: WakeByRequest, Detail: host}, hold)
	if outcome.Status == proxysvc.WakeAwake && validUpstream(outcome.Upstream) {
		w.Header().Set(proxysvc.WakeUpstreamHeader, outcome.Upstream)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(outcome.Status)
	fmt.Fprintln(w, outcome.Reason)
}

func validUpstream(upstream string) bool {
	host, port, err := net.SplitHostPort(upstream)
	if err != nil || net.ParseIP(host) == nil {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number > 0 && number < 65536
}

// wakePeerAllowed says whether the process at the other end of the socket
// is a proxy: nginx's workers, or Caddy.
func wakePeerAllowed(peer string) bool {
	return peer == "nginx" || peer == "caddy" || strings.HasPrefix(peer, "nginx")
}

// peerProcess is the name of the process at the other end of a Unix
// connection, "" when it cannot be told.
func peerProcess(conn net.Conn) string {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return ""
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return ""
	}
	var cred *unix.Ucred
	if err := raw.Control(func(fd uintptr) {
		cred, _ = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || cred == nil || cred.Pid <= 0 {
		return ""
	}
	comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(int(cred.Pid)), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(comm))
}

// wakeSockets keeps a listener on every socket the proxy reaches the wake
// through, and makes one again when its file goes: a recreated ingress has
// an empty volume.
type wakeSockets struct {
	sleeper *Sleeper
	mu      sync.Mutex
	open    map[string]*wakeListener
}

type wakeListener struct {
	server *http.Server
	inode  uint64
	proxy  string
}

func newWakeSockets(s *Sleeper) *wakeSockets {
	return &wakeSockets{sleeper: s, open: map[string]*wakeListener{}}
}

// serving says whether the wake is listening for this host's proxy.
func (w *wakeSockets) serving() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.open) > 0
}

// ensure listens where the proxy expects the wake, and stops listening where
// it no longer does.
func (w *wakeSockets) ensure(ctx context.Context) {
	proxy := w.sleeper.proxy()
	if proxy == nil {
		return
	}
	wanted, err := proxy.WakeSockets(ctx)
	if err != nil {
		w.sleeper.log.Warn("the wake socket's place could not be found", "error", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	keep := map[string]bool{}
	for _, socket := range wanted {
		keep[socket.Path] = true
		if current, ok := w.open[socket.Path]; ok && current.inode != 0 && socketInode(socket.Path) == current.inode {
			continue
		}
		if current, ok := w.open[socket.Path]; ok {
			current.server.Close()
			delete(w.open, socket.Path)
		}
		listener, err := listenWake(socket.Path)
		if err != nil {
			w.sleeper.log.Warn("the wake socket could not be opened", "path", socket.Path, "error", err)
			continue
		}
		server := &http.Server{
			Handler:           w.sleeper,
			ReadHeaderTimeout: 10 * time.Second,
			ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
				return context.WithValue(ctx, peerKey{}, peerProcess(conn))
			},
		}
		go func() { _ = server.Serve(listener) }()
		if !proxy.WakeSocketReached(ctx, socket) {
			// Opened somewhere the proxy does not look: listening there
			// would let an application sleep that nothing can wake.
			server.Close()
			w.sleeper.log.Warn("the wake socket is not where the proxy reaches it", "path", socket.Path, "proxy", socket.Proxy)
			continue
		}
		w.open[socket.Path] = &wakeListener{server: server, inode: socketInode(socket.Path), proxy: socket.Proxy}
	}
	for path, current := range w.open {
		if !keep[path] {
			current.server.Close()
			delete(w.open, path)
		}
	}
}

func (w *wakeSockets) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for path, current := range w.open {
		current.server.Close()
		delete(w.open, path)
	}
}

// listenWake opens the socket at path, replacing a stale socket a previous
// process left there and nothing else. A path longer than a socket address
// carries — a Docker volume's is — is bound through the directory's file
// descriptor, which names it in a few bytes.
func listenWake(path string) (*net.UnixListener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	address := path
	var dirFile *os.File
	if len(path) > 100 {
		var err error
		if dirFile, err = os.Open(dir); err != nil {
			return nil, err
		}
		defer dirFile.Close()
		address = fmt.Sprintf("/proc/self/fd/%d/%s", dirFile.Fd(), filepath.Base(path))
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		return nil, err
	}
	// The listener removes the file it bound when it closes; through a
	// descriptor's path it would try the wrong one.
	listener.SetUnlinkOnClose(false)
	// Connecting takes write permission on the socket, and nginx's workers
	// are not root. The directory is root's, and what the socket answers is
	// what the domain answers anyone.
	if err := os.Chmod(path, 0o666); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

func socketInode(path string) uint64 {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return 0
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}

// Listen opens the wake sockets now, rather than at the next pass: for a
// test, and for whoever started the dashboard and is waiting on a wake.
func (s *Sleeper) Listen(ctx context.Context) { s.sockets.ensure(ctx) }

// Close stops listening.
func (s *Sleeper) Close() { s.sockets.close() }
