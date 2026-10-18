// Package remotedesk opens remote desktop sessions to the operator's own
// computers from the browser. guacd, run by the dashboard in its own
// container, speaks RDP and VNC to the computer; the backend speaks the
// Guacamole protocol to guacd and relays it to the page. Computers are
// usually reached over the tailnet, so neither RDP nor VNC is ever exposed.
package remotedesk

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Peer is a device on the tailnet, as the overview needs it.
type Peer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DNSName  string `json:"dnsName"`
	OS       string `json:"os"`
	Address  string `json:"address"`
	Online   bool   `json:"online"`
	Direct   bool   `json:"direct"`
	Relay    string `json:"relay"`
	LastSeen int64  `json:"lastSeen"`
}

// Tailnet is this server's view of its tailnet.
type Tailnet struct {
	Installed bool   `json:"installed"`
	Self      *Peer  `json:"self,omitempty"`
	Peers     []Peer `json:"peers"`
	Error     string `json:"error,omitempty"`
}

type Options struct {
	DB     *sql.DB
	Seal   func(string) (string, error)
	Open   func(string) (string, error)
	Engine Engine
	// Bridge is the stream bridge's container, beside the engine.
	Bridge  Engine
	Tailnet func(ctx context.Context) (Tailnet, error)
	// Dial reaches guacd and probes computers; tests swap it.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Lookup resolves a computer's name the way this server does; tests swap it.
	Lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
	// Warm, when set, asks the tailnet to find its direct path to a peer
	// before a session needs it. None is fine: paths are found on traffic
	// too, only later.
	Warm func(ctx context.Context, address string)
	Now  func() time.Time
}

type Service struct {
	store   *store
	engine  Engine
	bridge  Engine
	tailnet func(ctx context.Context) (Tailnet, error)
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
	lookup  func(ctx context.Context, host string) ([]net.IPAddr, error)
	warm    func(ctx context.Context, address string)
	now     func() time.Time

	mu          sync.Mutex
	live        map[string]int
	pairs       map[string]*pairing
	streams     int
	bridgeAt    string
	bridgeUntil time.Time
	// warmed is when each peer's path was last asked for, so the session
	// page's polling asks once a minute rather than every few seconds.
	warmed map[string]time.Time
}

func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Dial == nil {
		var d net.Dialer
		o.Dial = d.DialContext
	}
	if o.Lookup == nil {
		o.Lookup = net.DefaultResolver.LookupIPAddr
	}
	if o.Tailnet == nil {
		o.Tailnet = func(context.Context) (Tailnet, error) { return Tailnet{Peers: []Peer{}}, nil }
	}
	return &Service{
		store:   &store{db: o.DB, seal: o.Seal, open: o.Open, now: o.Now},
		engine:  o.Engine,
		bridge:  o.Bridge,
		tailnet: o.Tailnet,
		dial:    o.Dial,
		lookup:  o.Lookup,
		warm:    o.Warm,
		now:     o.Now,
		live:    map[string]int{},
		pairs:   map[string]*pairing{},
		warmed:  map[string]time.Time{},
	}
}

// warmInterval is how long one warm-up is trusted for. Tailscale lets a
// direct path lapse after a few minutes without traffic.
const warmInterval = time.Minute

// warmPath asks the tailnet, in the background, to find its direct path to
// the peer the computer is, so a session that starts in the next moments
// starts on it rather than on a relay. A path is only looked for on traffic,
// and the first seconds of a session are what suffer when it is still being
// found: the picture arrives through the nearest relay, at twice the round
// trip, until it is.
func (s *Service) warmPath(ctx context.Context, view ComputerView) {
	if s.warm == nil || view.Peer == nil || !view.Peer.Online || view.Peer.Direct || view.Peer.Address == "" {
		return
	}
	s.mu.Lock()
	last, seen := s.warmed[view.Peer.Address]
	if seen && s.now().Sub(last) < warmInterval {
		s.mu.Unlock()
		return
	}
	s.warmed[view.Peer.Address] = s.now()
	s.mu.Unlock()
	address := view.Peer.Address
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		s.warm(ctx, address)
	}()
}

// Reconcile closes the history of sessions a restart cut off.
func (s *Service) Reconcile(ctx context.Context) error { return s.store.interruptSessions(ctx) }

func (s *Service) Close() error {
	var errs []error
	for _, e := range []Engine{s.engine, s.bridge} {
		if e != nil {
			errs = append(errs, e.Close())
		}
	}
	return errors.Join(errs...)
}

func (s *Service) Engine(ctx context.Context) EngineStatus { return s.engine.Status(ctx) }

func (s *Service) InstallEngine(ctx context.Context, progress func(string)) error {
	return s.engine.Install(ctx, progress)
}

// UpgradeEngine makes a running engine again when an older dashboard made it
// with another configuration; its volume, and the files on it, stay. Install
// is a no-op for one that is current. Nothing is open at start, which is
// when it runs.
func (s *Service) UpgradeEngine(ctx context.Context, progress func(string)) error {
	if s.engine.Status(ctx).State != EngineRunning {
		return nil
	}
	return s.engine.Install(ctx, progress)
}

func (s *Service) RemoveEngine(ctx context.Context) error {
	if n := s.liveTotal(); n > 0 {
		return &BusyError{Live: n}
	}
	return s.engine.Remove(ctx)
}

// BusyError refuses to pull the engine out from under open sessions.
type BusyError struct{ Live int }

func (e *BusyError) Error() string {
	if e.Live == 1 {
		return "a remote session is open; end it before removing the engine"
	}
	return strconv.Itoa(e.Live) + " remote sessions are open; end them before removing the engine"
}

// Reach is whether the computer's remote desktop port answered just now.
type Reach struct {
	State     string  `json:"state"`
	LatencyMs float64 `json:"latencyMs,omitempty"`
	Error     string  `json:"error,omitempty"`
	CheckedAt int64   `json:"checkedAt"`
}

const (
	ReachOpen    = "open"
	ReachClosed  = "closed"
	ReachOffline = "offline"
)

const probeTimeout = 1500 * time.Millisecond

// Probe connects to the port and hangs up. The connect time is one round
// trip, which is what the overview shows as the computer's distance.
func (s *Service) Probe(ctx context.Context, host string, port int) Reach {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	started := s.now()
	conn, err := s.dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	r := Reach{CheckedAt: s.now().Unix()}
	if err != nil {
		r.State, r.Error = ReachClosed, reachError(err)
		return r
	}
	conn.Close()
	r.State = ReachOpen
	r.LatencyMs = float64(s.now().Sub(started).Microseconds()) / 1000
	return r
}

func reachError(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "i/o timeout"):
		return "no answer"
	case strings.Contains(msg, "connection refused"):
		return "refused"
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"):
		return "no route"
	case strings.Contains(msg, "no such host"):
		return "unknown host"
	}
	return msg
}

// ComputerView is a saved computer with what is known about it right now.
type ComputerView struct {
	Computer
	Peer  *Peer `json:"peer,omitempty"`
	Reach Reach `json:"reach"`
	Live  int   `json:"live"`
}

type Overview struct {
	Engine    EngineStatus    `json:"engine"`
	Stream    EngineStatus    `json:"stream"`
	Tailnet   Tailnet         `json:"tailnet"`
	Computers []ComputerView  `json:"computers"`
	Sessions  []SessionRecord `json:"sessions"`
	Layouts   []string        `json:"layouts"`
}

// historyWindow is how far back the overview's session history reaches.
const historyWindow = 14 * 24 * time.Hour

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	computers, err := s.store.list(ctx)
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Layouts: Layouts}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		o.Engine = s.engine.Status(ctx)
	}()
	go func() {
		defer wg.Done()
		o.Stream = s.bridge.Status(ctx)
	}()
	go func() {
		defer wg.Done()
		o.Tailnet = s.readTailnet(ctx)
	}()
	wg.Wait()
	o.Computers = s.views(ctx, computers, o.Tailnet)
	if o.Sessions, err = s.store.sessions(ctx, s.now().Add(-historyWindow).Unix(), 200); err != nil {
		return Overview{}, err
	}
	return o, nil
}

func (s *Service) readTailnet(ctx context.Context) Tailnet {
	t, err := s.tailnet(ctx)
	if t.Peers == nil {
		t.Peers = []Peer{}
	}
	if err != nil {
		t.Error = err.Error()
	}
	return t
}

func (s *Service) views(ctx context.Context, computers []Computer, t Tailnet) []ComputerView {
	out := make([]ComputerView, len(computers))
	var wg sync.WaitGroup
	for i, c := range computers {
		out[i] = ComputerView{Computer: c, Peer: matchPeer(c, t.Peers), Live: s.liveFor(c.ID)}
		if out[i].Peer != nil && !out[i].Peer.Online {
			out[i].Reach = Reach{State: ReachOffline, CheckedAt: s.now().Unix()}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i].Reach = s.Probe(ctx, out[i].Host, out[i].Port)
		}(i)
	}
	wg.Wait()
	return out
}

func matchPeer(c Computer, peers []Peer) *Peer {
	host := strings.TrimSuffix(strings.ToLower(c.Host), ".")
	for i := range peers {
		p := &peers[i]
		if c.PeerID != "" && p.ID == c.PeerID {
			return p
		}
	}
	for i := range peers {
		p := &peers[i]
		dns := strings.TrimSuffix(strings.ToLower(p.DNSName), ".")
		short, _, _ := strings.Cut(dns, ".")
		if host == p.Address || host == dns || (host == short && short != "") || host == strings.ToLower(p.Name) {
			return p
		}
	}
	return nil
}

func (s *Service) Computer(ctx context.Context, id string) (Computer, error) {
	return s.store.get(ctx, id)
}

// ProbeTarget checks an address before it is saved, for the add flow's
// "is Remote Desktop on yet" step.
func (s *Service) ProbeTarget(ctx context.Context, host string, port int) (Reach, error) {
	host = strings.TrimSpace(host)
	if !validHost(host) {
		return Reach{}, invalid("host must be an IP address or a host name")
	}
	if port < 1 || port > 65535 {
		return Reach{}, invalid("port must be between 1 and 65535")
	}
	return s.Probe(ctx, host, port), nil
}

// View is one computer as the session page reads it. The page reads it
// before it connects, which is when the path to the computer is warmed.
func (s *Service) View(ctx context.Context, id string) (ComputerView, error) {
	c, err := s.store.get(ctx, id)
	if err != nil {
		return ComputerView{}, err
	}
	views := s.views(ctx, []Computer{c}, s.readTailnet(ctx))
	s.warmPath(ctx, views[0])
	return views[0], nil
}

func (s *Service) Add(ctx context.Context, in ComputerInput) (Computer, error) {
	return s.store.create(ctx, in)
}

func (s *Service) Update(ctx context.Context, id string, in ComputerInput) (Computer, error) {
	return s.store.update(ctx, id, in)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if s.liveFor(id) > 0 {
		return &BusyError{Live: s.liveFor(id)}
	}
	return s.store.delete(ctx, id)
}

// Connect opens a session through guacd. The returned session is already
// past the handshake; the caller relays it and then calls Finish.
func (s *Service) Connect(ctx context.Context, id string, d Display, actor string) (*Session, error) {
	c, err := s.store.get(ctx, id)
	if err != nil {
		return nil, err
	}
	password, err := s.store.password(ctx, id)
	if err != nil {
		return nil, err
	}
	st := s.engine.Status(ctx)
	if st.State != EngineRunning || st.Address == "" {
		return nil, &GuacError{Message: "Remote Desktop's engine is not running on this server", Code: statusServerError}
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := s.dial(dialCtx, "tcp", st.Address)
	cancel()
	if err != nil {
		return nil, &GuacError{Message: "Remote Desktop's engine is not answering: " + reachError(err), Code: statusServerError}
	}
	target, err := s.resolve(ctx, c.Host)
	if err != nil {
		conn.Close()
		return nil, &GuacError{Message: c.Host + " does not resolve from this server", Code: statusUpstreamNotFound}
	}
	params := connectParams(c, password, d)
	params["hostname"] = target
	d = d.normalized()
	rd := NewReader(conn)
	if err := handshake(conn, rd, c.Protocol, params, d, actor); err != nil {
		conn.Close()
		var guac *GuacError
		if errors.As(err, &guac) {
			return nil, guac
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, &GuacError{Message: c.Name + " did not answer in time", Code: statusUpstreamTimeout}
		}
		return nil, &GuacError{Message: "The session could not start: " + err.Error(), Code: statusUpstreamError}
	}
	record, err := s.store.openSession(ctx, c.ID, actor)
	if err != nil {
		conn.Close()
		return nil, err
	}
	s.mu.Lock()
	s.live[c.ID]++
	s.mu.Unlock()
	return &Session{conn: conn, rd: rd, computer: c.ID, record: record}, nil
}

// resolve turns a computer's name into an address here, where the probe
// resolves it too. guacd's container resolves with musl and Docker's DNS,
// which never try the tailnet's search domain, so a MagicDNS short name like
// "office-pc" answered the probe and then failed inside guacd.
func (s *Service) resolve(ctx context.Context, host string) (string, error) {
	if net.ParseIP(host) != nil {
		return host, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := s.lookup(ctx, host)
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if addr.IP.To4() != nil {
			return addr.IP.String(), nil
		}
	}
	if len(addrs) == 0 {
		return "", errors.New("no address")
	}
	return addrs[0].IP.String(), nil
}

// Finish closes the session and writes how it ended into the history.
func (s *Service) Finish(ctx context.Context, sess *Session, err error) {
	sess.Close()
	s.mu.Lock()
	if s.live[sess.computer]--; s.live[sess.computer] <= 0 {
		delete(s.live, sess.computer)
	}
	s.mu.Unlock()
	outcome := "closed"
	if err != nil {
		outcome = "error"
	}
	s.store.closeSession(context.WithoutCancel(ctx), sess.record, outcome)
}

func (s *Service) liveFor(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[id]
}

func (s *Service) liveTotal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.live {
		n += v
	}
	return n
}

func connectParams(c Computer, password string, d Display) map[string]string {
	p := map[string]string{
		"hostname": c.Host,
		"port":     strconv.Itoa(c.Port),
		"username": c.Username,
		"password": password,
	}
	if c.Protocol == ProtocolVNC {
		return p
	}
	p["domain"] = c.Domain
	p["security"] = c.Security
	if p["security"] == "" {
		p["security"] = "any"
	}
	// The tailnet already authenticates and encrypts the path; a Windows
	// PC's self-signed RDP certificate has nothing to be checked against.
	p["ignore-cert"] = "true"
	p["resize-method"] = "display-update"
	p["enable-font-smoothing"] = "true"
	// guacd 1.6 turns RDP's Graphics Pipeline on by default, and with it
	// loses the server's frame marks: frames are cut on a timer, between
	// ten and a hundred milliseconds, and carry two to three times the data.
	// The classic path draws on the marks, at 16-bit colour. A computer
	// whose server insists on the pipeline asks for it.
	if !c.Gfx {
		p["disable-gfx"] = "true"
	}
	p["client-name"] = "Just Dashboard"
	p["server-layout"] = c.Layout
	p["timezone"] = d.Timezone
	p["enable-drive"] = "true"
	p["drive-name"] = "Just Dashboard"
	// guacd creates only the last folder of the path, so each computer's
	// folder sits directly in the engine's home, which the volume holds.
	p["drive-path"] = engineHome + "/" + c.ID
	p["create-drive-path"] = "true"
	return p
}
