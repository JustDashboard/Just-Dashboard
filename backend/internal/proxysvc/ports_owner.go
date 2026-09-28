package proxysvc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// socketHolders maps each wanted socket inode to every process holding a
// descriptor on it, read from /proc/<pid>/fd.
//
// gopsutil kept only the first holder its walk met, and the walk meets PID 1
// first — so a socket-activated sshd, whose port systemd holds as well, was
// reported as systemd, /sbin/init. Keeping every holder lets ownerOf choose.
//
// A process that exits mid-walk, or whose descriptors this account may not
// read, is skipped: its sockets are listed without an owner rather than the
// whole listing failing.
func socketHolders(ctx context.Context, root string, wanted map[uint64]bool) (map[uint64][]int32, error) {
	holders := map[uint64][]int32{}
	if len(wanted) == 0 {
		return holders, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.ParseInt(entry.Name(), 10, 32)
		if err != nil {
			continue
		}
		dir := filepath.Join(root, entry.Name(), "fd")
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(dir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok || !wanted[inode] {
				continue
			}
			// One socket on two descriptors (a dup) is still one holder.
			if held := holders[inode]; len(held) > 0 && held[len(held)-1] == int32(pid) {
				continue
			}
			holders[inode] = append(holders[inode], int32(pid))
		}
	}
	return holders, nil
}

// socketInode reads a descriptor link of the form "socket:[12345]".
func socketInode(target string) (uint64, bool) {
	rest, ok := strings.CutPrefix(target, "socket:[")
	if !ok {
		return 0, false
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(rest, "]"), 10, 64)
	return inode, err == nil
}

// parentsOf reads each holder's parent PID from /proc/<pid>/stat. A process
// that exited since the walk is left out, and counts as nobody's child.
func parentsOf(root string, holders map[uint64][]int32) map[int32]int32 {
	parents := map[int32]int32{}
	for _, pids := range holders {
		for _, pid := range pids {
			if _, done := parents[pid]; done {
				continue
			}
			stat, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(pid)), "stat"))
			if err != nil {
				continue
			}
			if ppid, ok := parentFromStat(string(stat)); ok {
				parents[pid] = ppid
			}
		}
	}
	return parents
}

// parentFromStat reads the fourth field of "pid (comm) state ppid …". The
// command name may hold spaces and parentheses of its own, so the fields are
// counted from the last ")".
func parentFromStat(stat string) (int32, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.ParseInt(fields[1], 10, 32)
	return int32(ppid), err == nil
}

// ownerOf names the process to show for a socket that several hold.
//
// PID 1 is set aside: systemd keeps every socket-activated listener it hands
// on, and naming init sends the reader to the one process not serving the
// port. Of the rest, the owner is the holder whose parent is not itself a
// holder — for a prefork server its master, whose workers inherited the
// socket from it. Not the lowest PID: once the PID counter wraps, workers
// respawned on a reload are numbered below the master that forked them.
// Only when several holders are unrelated (or a parent could not be read)
// does the lowest of them decide. When systemd is the only holder — a service
// not started yet, or one it spawns per connection — systemd is what
// answers. Nobody (0) means no process this account can see.
func ownerOf(pids []int32, parents map[int32]int32) int32 {
	held := map[int32]bool{}
	for _, pid := range pids {
		if pid != 1 {
			held[pid] = true
		}
	}
	if len(held) == 0 {
		if len(pids) > 0 {
			return 1
		}
		return 0
	}
	lowest := func(keep func(int32) bool) int32 {
		owner := int32(0)
		for pid := range held {
			if keep(pid) && (owner == 0 || pid < owner) {
				owner = pid
			}
		}
		return owner
	}
	root := lowest(func(pid int32) bool {
		parent, known := parents[pid]
		return !known || !held[parent]
	})
	if root != 0 {
		return root
	}
	return lowest(func(int32) bool { return true })
}

// threadName reports a comm that names a thread rather than a program. Linux
// takes a process's name from its main thread, and a runtime that names its
// threads names that one too: node calls it "MainThread", which is every
// node server's name in the listing and matches no product.
func threadName(comm string) bool {
	return strings.Contains(strings.ToLower(comm), "thread")
}

// displayName is the program's own name where comm is a thread's: the
// executable's base name, or the command line's first word where the
// executable could not be read. Empty where comm is not a thread's name, or
// is the program's (a program may be called "threadweaver").
func displayName(comm, exe, cmdline string) string {
	if !threadName(comm) {
		return ""
	}
	path := strings.TrimSuffix(exe, " (deleted)")
	if path == "" {
		fields := strings.Fields(cmdline)
		if len(fields) == 0 {
			return ""
		}
		path = fields[0]
	}
	name := filepath.Base(path)
	// comm is the name cut to fifteen bytes.
	if name == "." || name == "/" || strings.HasPrefix(name, comm) {
		return ""
	}
	return name
}

// ListenerContainer is the container a socket answers for.
type ListenerContainer struct {
	// ID is the short form, twelve characters, as `docker ps` prints it.
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
	// Project and Service are the compose project and service that started
	// it, empty for a container started otherwise.
	Project string `json:"project,omitempty"`
	Service string `json:"service,omitempty"`
	// Published is a port Docker publishes on the host — through
	// docker-proxy, or its NAT rules alone — rather than a socket a
	// container on the host's network holds itself. Docker forwards a
	// published port ahead of the host firewall's inbound rules.
	Published bool `json:"published,omitempty"`
	// Deployment is the dashboard deployment the container runs for.
	Deployment *ListenerDeployment `json:"deployment,omitempty"`
	// EnvironmentID is the deployment environment its label names, for the
	// API to look the deployment up by.
	EnvironmentID int64 `json:"-"`
}

// ListenerDeployment names a deployment project and the environment a
// container runs for.
type ListenerDeployment struct {
	ProjectID   int64  `json:"projectId"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
}

// RunningContainer is what the listing needs of a running container.
type RunningContainer struct {
	ID, Name, Image  string
	Project, Service string
	// EnvironmentID is the deployment environment the dashboard's labels
	// name, 0 for a container no deployment started.
	EnvironmentID int64
	Ports         []PublishedPort
	// Mounts are the container's mount destinations.
	Mounts []string
}

// PublishedPort is one host binding Docker made for a container.
type PublishedPort struct {
	// HostIP is Docker's own spelling: "0.0.0.0", "::", "127.0.0.1".
	HostIP   string
	HostPort uint16
	Protocol string
}

// SocketUnit is one inet address a systemd .socket unit listens on.
type SocketUnit struct {
	Protocol  string
	Address   string
	Port      uint32
	Unit      string
	Activates string
}

// SocketUnitAt reads systemctl list-sockets' address and type: "0.0.0.0:22"
// Stream is tcp on 0.0.0.0:22, "[::]:53" Datagram udp on ::. A path, an
// abstract or netlink socket, or vsock is not an inet socket and is not one.
func SocketUnitAt(listen, kind, unit, activates string) (SocketUnit, bool) {
	var protocol string
	switch kind {
	case "Stream":
		protocol = "tcp"
	case "Datagram":
		protocol = "udp"
	default:
		return SocketUnit{}, false
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return SocketUnit{}, false
	}
	// A link-local bind names its interface; the kernel's table does not.
	host, _, _ = strings.Cut(host, "%")
	ip := net.ParseIP(host)
	number, err := strconv.ParseUint(port, 10, 16)
	if ip == nil || err != nil || number == 0 {
		return SocketUnit{}, false
	}
	return SocketUnit{
		Protocol: protocol, Address: ip.String(), Port: uint32(number),
		Unit: unit, Activates: activates,
	}, true
}

// OwnerInput is what names a socket's owner beyond the process holding it.
// Every source may be missing — Docker unreachable, no systemd, no PM2 — and
// a missing one only leaves the process as the answer.
type OwnerInput struct {
	Containers  []RunningContainer
	SocketUnits []SocketUnit
	// PM2 is each PM2 application's PID and name.
	PM2 map[int32]string
	// SelfPID is the dashboard's own process.
	SelfPID int32
	// DataDir is the dashboard's data directory: a container that mounts
	// it is the dashboard's own, and so is the compose project it is in.
	DataDir string
}

// SourceDockerNAT is a port Docker publishes through NAT rules alone.
const SourceDockerNAT = "docker-nat"

// AttributeOwners names who each socket answers for: the PM2 app, the
// socket unit systemd listens with, the container — a port Docker publishes,
// joined by protocol, host address and port, or a container on the host's
// network holding it itself — and whether it is the dashboard's own. A port
// Docker publishes with the userland proxy off has no socket; each is added
// as a row of its own.
func AttributeOwners(listeners []Listener, in OwnerInput) []Listener {
	byID := map[string]*RunningContainer{}
	bindings := map[bindingKey]*RunningContainer{}
	for i := range in.Containers {
		c := &in.Containers[i]
		if len(c.ID) >= 12 {
			byID[c.ID[:12]] = c
		}
		for _, p := range c.Ports {
			if p.HostPort != 0 {
				bindings[bindingOf(p)] = c
			}
		}
	}
	units := map[bindingKey]SocketUnit{}
	for _, u := range in.SocketUnits {
		units[bindingKey{u.Protocol, u.Address, u.Port}] = u
	}
	self := selfProject(in.Containers, in.DataDir)
	isSelf := func(l *Listener) bool {
		if in.SelfPID > 0 && l.PID == in.SelfPID {
			return true
		}
		return self != "" && l.Container != nil && l.Container.Project == self
	}

	joined := map[bindingKey]bool{}
	for i := range listeners {
		l := &listeners[i]
		key := bindingKey{l.Protocol, l.Address, l.Port}
		if app, ok := in.PM2[l.PID]; ok && l.PID > 0 {
			l.Manager, l.ManagerName = "pm2", app
		}
		if u, ok := units[key]; ok {
			l.SocketUnit, l.Activates = u.Unit, u.Activates
		}
		if l.Manager == "container" {
			if c := byID[l.ManagerName]; c != nil {
				l.Container = containerOf(c, false)
			}
		} else if c, binding := publisher(bindings, l); c != nil {
			l.Container = containerOf(c, true)
			joined[binding] = true
		}
		l.Self = isSelf(l)
	}

	for i := range in.Containers {
		c := &in.Containers[i]
		for _, p := range c.Ports {
			key := bindingOf(p)
			if p.HostPort == 0 || joined[key] {
				continue
			}
			// A binding the listing repeats is still one row.
			joined[key] = true
			scope := bindScope(key.address)
			l := Listener{
				Protocol:  key.proto,
				Family:    familyOf(key.address),
				Address:   key.address,
				Port:      key.port,
				Scope:     scope,
				Exposed:   scope != ScopeLoopback,
				Container: containerOf(c, true),
				Source:    SourceDockerNAT,
			}
			l.Self = isSelf(&l)
			listeners = append(listeners, l)
		}
	}
	sortListeners(listeners)
	return listeners
}

type bindingKey struct {
	proto, address string
	port           uint32
}

// bindingOf is a published port as the kernel's table would list its
// socket. Docker has spelt "every interface" as an empty address.
func bindingOf(p PublishedPort) bindingKey {
	address := "0.0.0.0"
	if ip := net.ParseIP(p.HostIP); ip != nil {
		address = ip.String()
	}
	return bindingKey{strings.ToLower(p.Protocol), address, uint32(p.HostPort)}
}

// publisher is the container a socket is Docker's publication of. The
// kernel lets one socket hold an address and port, so a published binding
// that names the socket's names its container, whatever holds it:
// docker-proxy, or dockerd reserving the port. A Docker older than 20.10
// published every interface as one docker-proxy on ::, which the binding
// calls 0.0.0.0, so a wildcard socket docker-proxy holds (or one whose
// holder this account cannot see) also takes the other family's wildcard.
func publisher(bindings map[bindingKey]*RunningContainer, l *Listener) (*RunningContainer, bindingKey) {
	key := bindingKey{l.Protocol, l.Address, l.Port}
	if c := bindings[key]; c != nil {
		return c, key
	}
	if l.Scope != ScopeAll || (l.PID != 0 && l.Process != "docker-proxy") {
		return nil, bindingKey{}
	}
	for _, other := range []string{"0.0.0.0", "::"} {
		key.address = other
		if c := bindings[key]; c != nil {
			return c, key
		}
	}
	return nil, bindingKey{}
}

func familyOf(address string) string {
	if strings.Contains(address, ":") {
		return "ipv6"
	}
	return "ipv4"
}

func containerOf(c *RunningContainer, published bool) *ListenerContainer {
	id := c.ID
	if len(id) > 12 {
		id = id[:12]
	}
	return &ListenerContainer{
		ID: id, Name: c.Name, Image: c.Image,
		Project: c.Project, Service: c.Service,
		Published: published, EnvironmentID: c.EnvironmentID,
	}
}

// selfProject is the compose project of the dashboard's own container: the
// one mounting its data directory, which holds the database this process
// has open. Two dashboards on one host have two data directories. Where
// several containers mount it, the compose service named backend is the
// dashboard's; where none does, the dashboard is not a container, and only
// its own PID is its own.
func selfProject(containers []RunningContainer, dataDir string) string {
	if dataDir == "" {
		return ""
	}
	want := filepath.Clean(dataDir)
	var mounting []*RunningContainer
	for i := range containers {
		for _, m := range containers[i].Mounts {
			if filepath.Clean(m) == want {
				mounting = append(mounting, &containers[i])
				break
			}
		}
	}
	if len(mounting) == 1 {
		return mounting[0].Project
	}
	for _, c := range mounting {
		if c.Service == "backend" {
			return c.Project
		}
	}
	return ""
}

// UnderPM2 reports a socket whose owner a PM2 daemon started: its parent is
// named as PM2 titles its daemon, "PM2 v7.0.4: God Daemon (…)", which the
// kernel keeps as "PM2 v7.0.4: God".
func UnderPM2(listeners []Listener) bool {
	root := procRoot()
	seen := map[int32]bool{}
	for _, l := range listeners {
		if l.PPID <= 1 || seen[l.PPID] {
			continue
		}
		seen[l.PPID] = true
		comm, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(l.PPID)), "comm"))
		if err == nil && strings.HasPrefix(string(comm), "PM2 v") {
			return true
		}
	}
	return false
}
