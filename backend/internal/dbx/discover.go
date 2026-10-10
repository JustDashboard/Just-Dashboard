package dbx

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Everything on this machine that holds a database, as one list.
//
// detect.go answers a narrower question — "which running containers can I
// connect to right now" — and its answer was the whole of what the Databases
// page knew. A stopped Postgres, a service a compose file declares and nobody
// has brought up, a cluster apt installed that listens on a socket only, a
// SQLite file inside an application's volume, a Memcached nothing here can
// open: none of them is a connection, and all of them are databases the
// operator runs. A control panel that cannot list them is one the operator
// has to keep a second list beside.
//
// So this file separates seeing from connecting. An Instance is something
// that was found, with why it was taken for what it is and, where it cannot
// be connected, the reason in a sentence. Connecting is a later, explicit act
// on one instance, addressed by its key.
//
// Everything here is a pure function of facts the caller collected. dbx opens
// no socket and reads no file to produce an inventory, which is what lets the
// whole classification be tested with a machine written out in a struct.

// Kinds of instance.
const (
	// KindServer is a database server: a process, running or not, that
	// answers on a socket.
	KindServer = "server"
	// KindFile is a database that is one file — SQLite, DuckDB.
	KindFile = "file"
	// KindData is an engine's data directory with no server on it: what a
	// removed container leaves in the volume it kept.
	KindData = "data"
	// KindEmbedded is a database file inside a container's own writable
	// layer: an application that was given no volume and keeps its data
	// where removing the container deletes it.
	KindEmbedded = "embedded"
)

// Where an instance was found, beyond the two detect_host.go names.
const (
	SourceCompose = "compose"
	SourceFile    = "file"
	SourceVolume  = "volume"
)

// States an instance can be in. A container's own state (exited, created,
// paused, restarting, dead) is passed through as Docker words it.
const (
	StateRunning  = "running"
	StateInactive = "inactive"
	StateFailed   = "failed"
	StateDeclared = "declared"
	StateFile     = "file"
	StateData     = "data"
)

// How an instance's credentials are known, which decides what the operator is
// asked for. None of these carries the credential itself.
const (
	// CredentialsEnv: the container's environment states them.
	CredentialsEnv = "env"
	// CredentialsArgs: the container's command line states them.
	CredentialsArgs = "args"
	// CredentialsSecretFile: the environment names a file holding them.
	CredentialsSecretFile = "secret-file"
	// CredentialsOpen: the server accepts connections with none.
	CredentialsOpen = "open"
	// CredentialsPeer: the server admits its own system account over its unix
	// socket, so an account can be made from the host's shell.
	CredentialsPeer = "peer"
	// CredentialsNeeded: a password exists and nothing here states it.
	CredentialsNeeded = "needed"
	// CredentialsUnknown: nothing is known either way.
	CredentialsUnknown = "unknown"
)

// How sure the classification is: which rung of the ladder matched.
const (
	ConfidenceImage       = "image"
	ConfidenceFingerprint = "fingerprint"
	ConfidenceCommand     = "command"
	ConfidencePort        = "port"
	ConfidenceProcess     = "process"
	ConfidenceSocket      = "socket"
	ConfidenceUnit        = "unit"
	ConfidenceMagic       = "magic"
	ConfidenceMarker      = "marker"
)

// Instance is one database-bearing thing found on this machine.
//
// It carries no password, for the reason Candidate does not: this is the
// description handed to a browser, and the secret is read on the server at the
// moment the instance is connected.
type Instance struct {
	// Key identifies the instance across polls, restarts and recreations:
	// docker:<container>, compose:<project>/<service>, host:<unit>,
	// host:<engine>:<port or socket>, file:<path>, data:<path>,
	// embedded:<container>:<path>.
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Name is what to call it in a list: the container, the unit, the file.
	Name string `json:"name"`
	// Engine is the driver's id for anything this dashboard opens, and the
	// product's own id for one it only sees. Driver is empty for the latter.
	Engine string `json:"engine"`
	Driver Driver `json:"driver"`
	// Flavor is which product behind that driver it is — mariadb behind
	// mysql, valkey behind redis — and Variant what an image adds to it.
	Flavor  string `json:"flavor,omitempty"`
	Variant string `json:"variant,omitempty"`
	Label   string `json:"label"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source"`
	State   string `json:"state"`
	// Endpoints is every address the instance answers at. One of them is
	// primary: the one a connection is made to.
	Endpoints []Endpoint    `json:"endpoints"`
	Container *ContainerRef `json:"container,omitempty"`
	Host      *HostRef      `json:"host,omitempty"`
	File      *FileRef      `json:"file,omitempty"`
	// User and Database are what a connection would sign in as and open: the
	// container's own statement where it makes one, the engine's convention
	// otherwise. They fill a form; neither is a secret.
	User        string `json:"user,omitempty"`
	Database    string `json:"database,omitempty"`
	Credentials string `json:"credentials"`
	Confidence  string `json:"confidence"`
	// Evidence is why it was taken for what it is, in sentences that name
	// variables and never their values.
	Evidence []string `json:"evidence"`
	// Connectable says a connection can be attempted now; Reason says why not
	// where it cannot.
	Connectable bool   `json:"connectable"`
	Reason      string `json:"reason,omitempty"`
	// Connections are the saved connections that point here.
	Connections []int64 `json:"connections"`
	Ignored     bool    `json:"ignored,omitempty"`
	// Self marks the dashboard's own store: listed, and never connected.
	Self bool `json:"self,omitempty"`
}

// Endpoint is one address an instance answers at.
type Endpoint struct {
	// Kind is tcp, unix, container (the container's own address on a bridge
	// network) or file.
	Kind string `json:"kind"`
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	Path string `json:"path,omitempty"`
	// Bind is the address the socket is bound to where that differs from the
	// one to dial: a server on 0.0.0.0 is dialled on loopback.
	Bind string `json:"bind,omitempty"`
	// Scope is how far the address reaches: loopback, private or public.
	Scope   string `json:"scope,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// Scopes of an endpoint.
const (
	ScopeLoopback = "loopback"
	ScopePrivate  = "private"
	ScopePublic   = "public"
)

// ContainerRef is the container an instance runs in.
type ContainerRef struct {
	ID             string       `json:"id,omitempty"`
	Name           string       `json:"name"`
	Image          string       `json:"image"`
	ComposeProject string       `json:"composeProject,omitempty"`
	ComposeService string       `json:"composeService,omitempty"`
	Health         string       `json:"health,omitempty"`
	Status         string       `json:"status,omitempty"`
	NetworkMode    string       `json:"networkMode,omitempty"`
	DataVolumes    []DataVolume `json:"dataVolumes"`
	// EnvironmentID is the deployment environment that owns the container,
	// from its io.just-dashboard labels.
	EnvironmentID string `json:"environmentId,omitempty"`
}

// DataVolume is where a container keeps what it stores.
type DataVolume struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
}

// HostRef is the process and unit behind a server installed on the machine.
type HostRef struct {
	PID       int32  `json:"pid,omitempty"`
	Process   string `json:"process,omitempty"`
	Unit      string `json:"unit,omitempty"`
	UnitState string `json:"unitState,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
	User      string `json:"user,omitempty"`
	// Cluster is a Debian PostgreSQL cluster's "<version>/<name>".
	Cluster    string `json:"cluster,omitempty"`
	DataDir    string `json:"dataDir,omitempty"`
	ConfigFile string `json:"configFile,omitempty"`
}

// FileRef is a database file, or a data directory, and who it belongs to.
type FileRef struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// WAL says a write-ahead log sits beside the file: something has it open,
	// or had it open and did not close it cleanly.
	WAL bool `json:"wal,omitempty"`
	// Holder is whose file it is: self, container, deployment, compose,
	// application, tool or system. The last two are state a program keeps for
	// itself, which the page counts and does not list by default.
	Holder     string   `json:"holder"`
	Containers []string `json:"containers,omitempty"`
	Volume     string   `json:"volume,omitempty"`
	Project    string   `json:"project,omitempty"`
}

// Holders of a file.
const (
	HolderSelf        = "self"
	HolderContainer   = "container"
	HolderDeployment  = "deployment"
	HolderCompose     = "compose"
	HolderApplication = "application"
	HolderTool        = "tool"
	HolderSystem      = "system"
)

// Access is how a discovered instance is signed in to. It is produced beside
// the Instance and never serialised: the password it holds is the one thing
// an inventory must not carry.
type Access struct {
	Candidate Candidate
	Password  string
	// SecretFile is the path inside the container of the file the password is
	// kept in, where the environment names one instead of stating it.
	SecretFile string
	// Unverified marks a password taken from a variable the stock image does
	// not itself read. The server may be open all the same, so a refusal is
	// worth one more try with nothing.
	Unverified bool
}

// Facts is everything discovery is told about the machine.
type Facts struct {
	Containers []ContainerFacts
	Declared   []DeclaredService
	Listeners  []HostListener
	Sockets    []UnixSocket
	Units      []HostUnit
	Files      []FileFacts
	DataDirs   []DataDirFacts
	Embedded   []EmbeddedFile
	// Places says whose a path is, for files.
	Places Places
}

// Inventory is what discovery found. Instances are what a browser is shown;
// how each is signed in to stays on the server.
type Inventory struct {
	Instances []Instance
	access    map[string]Access
}

// Access is how the instance with that key is signed in to, where anything is
// known about it.
func (inv Inventory) Access(key string) (Access, bool) {
	a, ok := inv.access[key]
	return a, ok
}

// Find returns the instance with that key.
func (inv Inventory) Find(key string) (*Instance, bool) {
	for i := range inv.Instances {
		if inv.Instances[i].Key == key {
			return &inv.Instances[i], true
		}
	}
	return nil, false
}

// FindContainer returns the server found in the container with that name. A
// container is looked for by its name rather than by the key its labels would
// give it, because two containers can state the same labels and only one of
// them holds that key.
func (inv Inventory) FindContainer(name string) (*Instance, bool) {
	for i := range inv.Instances {
		inst := &inv.Instances[i]
		if inst.Kind == KindServer && inst.State != StateDeclared && inst.Container != nil && inst.Container.Name == name {
			return inst, true
		}
	}
	return nil, false
}

// Discover turns the facts about a machine into the list of what holds a
// database on it.
//
// The order matters in one place: containers are read before the host's
// sockets, because a container on the host's own network looks from the socket
// table exactly like a server installed there, and it is the container half
// that knows its credentials. A socket whose process lives in a container is
// attached to that container rather than reported a second time.
func Discover(f Facts) Inventory {
	inv := Inventory{Instances: []Instance{}, access: map[string]Access{}}
	discoverContainers(f.Containers, &inv)
	discoverDeclared(f.Declared, &inv)
	discoverHost(f.Listeners, f.Sockets, f.Units, f.Containers, &inv)
	discoverFiles(f.Files, f.DataDirs, f.Places, &inv)
	discoverEmbedded(f.Embedded, f.Containers, &inv)
	for i := range inv.Instances {
		finish(&inv.Instances[i])
	}
	sortInstances(inv.Instances)
	return inv
}

// finish settles what every instance must state however it was found: which
// endpoint is the one to dial, whether it can be connected, and a list rather
// than null where there is nothing.
func finish(inst *Instance) {
	if inst.Endpoints == nil {
		inst.Endpoints = []Endpoint{}
	}
	if inst.Evidence == nil {
		inst.Evidence = []string{}
	}
	if inst.Connections == nil {
		inst.Connections = []int64{}
	}
	if inst.Container != nil && inst.Container.DataVolumes == nil {
		inst.Container.DataVolumes = []DataVolume{}
	}
	if inst.Reason == "" && inst.Driver == "" {
		inst.Reason = "this dashboard has no driver for " + inst.Label + " yet"
	}
	if inst.Reason == "" && inst.Kind == KindServer && primaryEndpoint(inst) == nil {
		inst.Reason = "nothing it listens on can be dialled from here"
	}
	inst.Connectable = inst.Reason == "" && !inst.Self
}

// primaryEndpoint is the endpoint a connection is made to.
func primaryEndpoint(inst *Instance) *Endpoint {
	for i := range inst.Endpoints {
		if inst.Endpoints[i].Primary {
			return &inst.Endpoints[i]
		}
	}
	return nil
}

// sortInstances puts what is running first, then what could be, then what is
// only data — and within each, by name, so the list does not shuffle between
// two polls.
func sortInstances(list []Instance) {
	rank := func(inst *Instance) int {
		switch {
		case inst.Kind == KindServer && inst.State == StateRunning:
			return 0
		case inst.Kind == KindServer:
			return 1
		case inst.Kind == KindData:
			return 2
		}
		return 3
	}
	sort.SliceStable(list, func(i, j int) bool {
		if a, b := rank(&list[i]), rank(&list[j]); a != b {
			return a < b
		}
		if a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name); a != b {
			return a < b
		}
		return list[i].Key < list[j].Key
	})
}

// describe fills the fields every instance of a product shares.
func describe(inst *Instance, p *product) {
	inst.Engine, inst.Driver, inst.Flavor, inst.Label = p.engine, p.driver, p.flavor(), p.label
}

// AddressIdentity names a server's address the way two spellings of the same
// address agree on.
//
// The identity of a server used to be the string "host:port", compared as
// typed. A connection made by hand to localhost:5432 therefore did not cover
// the server detection found at 127.0.0.1:5432, and the sync added it a second
// time; an IPv6 address written with brackets never matched the same address
// parsed without them. Every loopback spelling, and every wildcard — which is
// dialled on loopback — is one place on this machine.
func AddressIdentity(host string, port int) string {
	return canonicalHost(host) + ":" + strconv.Itoa(port)
}

func canonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	switch host {
	case "", "localhost", "*":
		return "loopback"
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		// A zone names the interface, not the server.
		host = host[:i]
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsUnspecified() {
		return "loopback"
	}
	if addr.Is6() {
		return "[" + addr.String() + "]"
	}
	return addr.String()
}

// bindScope is how far a bound address reaches.
func bindScope(address string) string {
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(address), "["), "]")
	switch host {
	case "", "*":
		return ScopePublic
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		if strings.EqualFold(host, "localhost") {
			return ScopeLoopback
		}
		return ScopePublic
	}
	addr = addr.Unmap()
	switch {
	case addr.IsUnspecified():
		// Every interface the machine has, or will have.
		return ScopePublic
	case addr.IsLoopback():
		return ScopeLoopback
	case addr.IsPrivate(), addr.IsLinkLocalUnicast(), carrierGradeNAT.Contains(addr):
		return ScopePrivate
	}
	return ScopePublic
}

// carrierGradeNAT is the range a tailnet hands out addresses from.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// SavedConnection is a connection the operator already has, in the terms
// needed to say which instance it points at.
type SavedConnection struct {
	ID     int64
	Driver Driver
	// Origin is the key of the instance the connection was made from, where
	// it was made from one.
	Origin string
	Host   string
	Port   int
	// Path is a SQLite connection's file.
	Path string
}

// AttachConnections records, on each instance, the saved connections that
// point at it.
//
// A connection made from an instance says so in its origin, and that is
// believed first: it survives the container getting a new address. One made
// before origins were recorded, or typed by hand, is matched by where it
// dials — compared as an identity, so localhost and 127.0.0.1 are the same
// server.
func AttachConnections(instances []Instance, conns []SavedConnection) {
	byKey := map[string]int{}
	byAddress := map[string]int{}
	byPath := map[string]int{}
	for i := range instances {
		inst := &instances[i]
		inst.Connections = []int64{}
		byKey[inst.Key] = i
		if inst.Kind == KindFile && inst.File != nil {
			byPath[inst.File.Path] = i
			continue
		}
		if inst.Kind != KindServer {
			continue
		}
		for _, e := range inst.Endpoints {
			if e.Port <= 0 || (e.Kind != "tcp" && e.Kind != "container") {
				continue
			}
			id := AddressIdentity(e.Host, e.Port)
			if _, taken := byAddress[id]; !taken {
				byAddress[id] = i
			}
		}
	}
	for _, c := range conns {
		if i, ok := byKey[c.Origin]; ok && c.Origin != "" {
			instances[i].Connections = append(instances[i].Connections, c.ID)
			continue
		}
		if c.Driver == DriverSQLite {
			if i, ok := byPath[c.Path]; ok {
				instances[i].Connections = append(instances[i].Connections, c.ID)
			}
			continue
		}
		if i, ok := byAddress[AddressIdentity(c.Host, c.Port)]; ok && instances[i].Driver == c.Driver {
			instances[i].Connections = append(instances[i].Connections, c.ID)
		}
	}
	for i := range instances {
		sort.Slice(instances[i].Connections, func(a, b int) bool {
			return instances[i].Connections[a] < instances[i].Connections[b]
		})
	}
}

// ValidInstanceKey reports whether a string has the shape of a key this
// package produces. It says nothing about whether such an instance exists.
func ValidInstanceKey(key string) bool {
	if key == "" || len(key) > 1024 {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, prefix := range []string{"docker:", "compose:", "host:", "file:", "data:", "embedded:"} {
		if rest, ok := strings.CutPrefix(key, prefix); ok {
			return rest != ""
		}
	}
	return false
}
