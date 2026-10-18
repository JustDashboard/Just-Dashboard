package remotedesk

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

type fakeEngine struct{ status EngineStatus }

func (f *fakeEngine) Status(context.Context) EngineStatus         { return f.status }
func (f *fakeEngine) Install(context.Context, func(string)) error { return nil }
func (f *fakeEngine) Remove(context.Context) error                { return nil }
func (f *fakeEngine) Close() error                                { return nil }

// fakeGuacd answers the handshake the way guacd 1.6 does for RDP and then
// hands the connection to serve, recording what the dashboard sent.
type fakeGuacd struct {
	ln      net.Listener
	mu      sync.Mutex
	got     []Instruction
	connect []string
	args    []string
	serve   func(net.Conn, *Reader)
}

func startGuacd(t *testing.T, args []string, serve func(net.Conn, *Reader)) *fakeGuacd {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGuacd{ln: ln, args: args, serve: serve}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go g.handle(conn)
		}
	}()
	return g
}

func (g *fakeGuacd) handle(conn net.Conn) {
	defer conn.Close()
	rd := NewReader(conn)
	for {
		_, in, err := rd.Read()
		if err != nil {
			return
		}
		g.mu.Lock()
		g.got = append(g.got, in)
		g.mu.Unlock()
		switch in.Opcode {
		case "select":
			conn.Write(encode("args", append([]string{"VERSION_1_5_0"}, g.args...)...))
		case "connect":
			g.mu.Lock()
			g.connect = in.Args
			g.mu.Unlock()
			conn.Write(encode("ready", "$abc"))
			if g.serve != nil {
				g.serve(conn, rd)
			}
			return
		}
	}
}

func (g *fakeGuacd) opcodes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, in := range g.got {
		out = append(out, in.Opcode)
	}
	return out
}

func newTestService(t *testing.T, engine Engine) *Service {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	seal := func(v string) (string, error) { return "sealed:" + v, nil }
	open := func(v string) (string, error) { return strings.TrimPrefix(v, "sealed:"), nil }
	// Every name resolves to a documentation address, so no test asks real DNS.
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}, nil
	}
	return New(Options{DB: st.DB, Seal: seal, Open: open, Engine: engine, Bridge: &fakeEngine{status: EngineStatus{State: EngineMissing}}, Lookup: lookup})
}

func ptr(s string) *string { return &s }

func TestComputersAreValidatedAndKeepTheirPasswordSealed(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, &fakeEngine{})
	if _, err := svc.Add(ctx, ComputerInput{Name: "x", OS: "amiga", Host: "pc"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown OS accepted: %v", err)
	}
	if _, err := svc.Add(ctx, ComputerInput{Name: "x", OS: "windows", Host: "pc; rm -rf"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad host accepted: %v", err)
	}
	mac, err := svc.Add(ctx, ComputerInput{Name: "MacBook", OS: "macos", Host: "studio-mac", Password: ptr("vnc-secret"), Security: "nla", Layout: "ro-ro-qwerty"})
	if err != nil {
		t.Fatal(err)
	}
	if mac.Protocol != ProtocolVNC || mac.Port != 5900 || mac.Security != "" || mac.Layout != "" || !mac.HasPassword {
		t.Fatalf("a Mac should default to VNC on 5900 with no RDP options: %#v", mac)
	}
	pc, err := svc.Add(ctx, ComputerInput{Name: "office-pc", OS: "windows", Host: "100.101.12.7", Username: "wayy"})
	if err != nil {
		t.Fatal(err)
	}
	if pc.Protocol != ProtocolRDP || pc.Port != 3389 || pc.HasPassword {
		t.Fatalf("a PC should default to RDP on 3389: %#v", pc)
	}
	var sealed string
	svc.store.db.QueryRow(`SELECT password_enc FROM remote_desktop_computers WHERE id = ?`, mac.ID).Scan(&sealed)
	if sealed != "sealed:vnc-secret" {
		t.Fatalf("password stored as %q", sealed)
	}
	// An edit without a password keeps it; an empty one clears it.
	edited, err := svc.Update(ctx, mac.ID, ComputerInput{Name: "MacBook Air", OS: "macos", Host: "studio-mac"})
	if err != nil || !edited.HasPassword || edited.Name != "MacBook Air" {
		t.Fatalf("edit lost the password: %#v %v", edited, err)
	}
	cleared, err := svc.Update(ctx, mac.ID, ComputerInput{Name: "MacBook Air", OS: "macos", Host: "studio-mac", Password: ptr("")})
	if err != nil || cleared.HasPassword {
		t.Fatalf("empty password did not clear: %#v %v", cleared, err)
	}
	if err := svc.Delete(ctx, pc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Computer(ctx, pc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted computer still there: %v", err)
	}
	if err := svc.Delete(ctx, pc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestOverviewMatchesTailnetPeersAndSkipsProbingOfflineOnes(t *testing.T) {
	ctx := context.Background()
	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	port := open.Addr().(*net.TCPAddr).Port
	svc := newTestService(t, &fakeEngine{status: EngineStatus{State: EngineRunning}})
	var dialed []string
	var mu sync.Mutex
	svc.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	svc.tailnet = func(context.Context) (Tailnet, error) {
		return Tailnet{Installed: true, Peers: []Peer{
			{ID: "n1", Name: "OFFICE-PC", DNSName: "office-pc.example.ts.net.", Address: "127.0.0.1", Online: true, Direct: true},
			{ID: "n2", Name: "Studio", DNSName: "studio-mac.example.ts.net.", Address: "100.101.12.11", Online: false},
		}}, nil
	}
	on, _ := svc.Add(ctx, ComputerInput{Name: "office-pc", OS: "windows", Host: "127.0.0.1", Port: port})
	off, _ := svc.Add(ctx, ComputerInput{Name: "Mac", OS: "macos", Host: "studio-mac"})
	o, err := svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Computers) != 2 || len(o.Layouts) == 0 {
		t.Fatalf("overview %#v", o)
	}
	byID := map[string]ComputerView{}
	for _, c := range o.Computers {
		byID[c.ID] = c
	}
	if v := byID[on.ID]; v.Peer == nil || v.Peer.ID != "n1" || v.Reach.State != ReachOpen {
		t.Fatalf("online PC: %#v", v)
	}
	if v := byID[off.ID]; v.Peer == nil || v.Peer.ID != "n2" || v.Reach.State != ReachOffline {
		t.Fatalf("offline Mac matched by its short tailnet name: %#v", v)
	}
	for _, address := range dialed {
		if strings.HasPrefix(address, "studio-mac") {
			t.Fatalf("probed a computer the tailnet says is offline")
		}
	}
}

type pipeSocket struct {
	in  chan []byte
	out chan []byte
}

func (p *pipeSocket) WriteText(b []byte) error {
	p.out <- append([]byte(nil), b...)
	return nil
}

func (p *pipeSocket) ReadMessage() (int, []byte, error) {
	b, ok := <-p.in
	if !ok {
		return 0, nil, io.EOF
	}
	return 1, b, nil
}

func TestConnectHandsGuacdTheCredentialsAndRelaysWholeInstructions(t *testing.T) {
	ctx := context.Background()
	fromBrowser := make(chan Instruction, 8)
	guacd := startGuacd(t, []string{"hostname", "port", "username", "password", "security", "ignore-cert", "server-layout", "drive-path", "disable-gfx"},
		func(conn net.Conn, rd *Reader) {
			conn.Write([]byte(encode("size", "0", "1920", "1080")))
			conn.Write([]byte(encode("sync", "1")))
			for {
				_, in, err := rd.Read()
				if err != nil {
					return
				}
				fromBrowser <- in
			}
		})
	engine := &fakeEngine{status: EngineStatus{State: EngineRunning, Address: guacd.ln.Addr().String()}}
	svc := newTestService(t, engine)
	pc, err := svc.Add(ctx, ComputerInput{Name: "office-pc", OS: "windows", Host: "100.101.12.7", Username: "wayy", Password: ptr("hunter2"), Layout: "ro-ro-qwerty"})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := svc.Connect(ctx, pc.ID, Display{Width: 1600, Height: 900, DPI: 96, Timezone: "Europe/Chisinau"}, "wayy")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(guacd.opcodes(), " "); got != "select size audio video image timezone name connect" {
		t.Fatalf("handshake was %q", got)
	}
	want := []string{"VERSION_1_5_0", "100.101.12.7", "3389", "wayy", "hunter2", "any", "true", "ro-ro-qwerty", "/home/guacd/" + pc.ID, "true"}
	if strings.Join(guacd.connect, "|") != strings.Join(want, "|") {
		t.Fatalf("connect values %q, want %q", guacd.connect, want)
	}
	if svc.liveFor(pc.ID) != 1 {
		t.Fatalf("session not counted as live")
	}

	ws := &pipeSocket{in: make(chan []byte, 8), out: make(chan []byte, 64)}
	relayed := make(chan error, 1)
	go func() { relayed <- sess.Relay(ctx, ws) }()

	first := <-ws.out
	if _, in, err := NewReader(strings.NewReader(string(first))).Read(); err != nil || in.Opcode != "" || len(in.Args) != 1 || len(in.Args[0]) != 36 {
		t.Fatalf("the first frame must carry the tunnel's UUID, got %q", first)
	}
	var screen strings.Builder
	for !strings.Contains(screen.String(), "4.sync") {
		select {
		case frame := <-ws.out:
			if !strings.HasSuffix(string(frame), ";") {
				t.Fatalf("frame ends mid-instruction: %q", frame)
			}
			screen.Write(frame)
		case <-time.After(2 * time.Second):
			t.Fatalf("the screen never arrived, got %q", screen.String())
		}
	}

	ws.in <- []byte("0.,4.ping,13.1697040000000;5.mouse,2.10,2.20,1.1;")
	select {
	case echo := <-ws.out:
		if string(echo) != "0.,4.ping,13.1697040000000;" {
			t.Fatalf("ping echoed as %q", echo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ping was not echoed")
	}
	select {
	case in := <-fromBrowser:
		if in.Opcode != "mouse" {
			t.Fatalf("guacd got %q; the tunnel's own pings must not reach it", in.Opcode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mouse never reached guacd")
	}

	close(ws.in)
	select {
	case err := <-relayed:
		svc.Finish(ctx, sess, err)
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not end when the browser left")
	}
	if svc.liveFor(pc.ID) != 0 {
		t.Fatalf("finished session still counted")
	}
	history, err := svc.store.sessions(ctx, 0, 10)
	if err != nil || len(history) != 1 || history[0].EndedAt == 0 || history[0].Actor != "wayy" {
		t.Fatalf("history %#v %v", history, err)
	}
	if err := svc.Delete(ctx, pc.ID); err != nil {
		t.Fatal(err)
	}
	if history, _ := svc.store.sessions(ctx, 0, 10); len(history) != 0 {
		t.Fatalf("a deleted computer's sessions stayed behind")
	}
}

func TestConnectReportsGuacdsRefusalAndAnEngineThatIsDown(t *testing.T) {
	ctx := context.Background()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		NewReader(conn).Read()
		conn.Write(encode("error", "Unsupported protocol", "256"))
	}()
	svc := newTestService(t, &fakeEngine{status: EngineStatus{State: EngineRunning, Address: ln.Addr().String()}})
	pc, _ := svc.Add(ctx, ComputerInput{Name: "pc", OS: "windows", Host: "pc"})
	var guac *GuacError
	if _, err := svc.Connect(ctx, pc.ID, Display{}, "wayy"); !errors.As(err, &guac) || guac.Message != "Unsupported protocol" {
		t.Fatalf("guacd's refusal lost: %v", err)
	}

	svc.engine = &fakeEngine{status: EngineStatus{State: EngineMissing}}
	if _, err := svc.Connect(ctx, pc.ID, Display{}, "wayy"); !errors.As(err, &guac) || !strings.Contains(guac.Message, "not running") {
		t.Fatalf("a missing engine should say so: %v", err)
	}
	if history, _ := svc.store.sessions(ctx, 0, 10); len(history) != 0 {
		t.Fatalf("a session that never started was recorded")
	}
}

func TestRemovingTheEngineWaitsForOpenSessions(t *testing.T) {
	svc := newTestService(t, &fakeEngine{})
	svc.live["abc"] = 2
	var busy *BusyError
	if err := svc.RemoveEngine(context.Background()); !errors.As(err, &busy) || busy.Live != 2 {
		t.Fatalf("removed under open sessions: %v", err)
	}
}

func TestDisplayFallsBackToSaneValues(t *testing.T) {
	d := Display{Width: 99999, Height: 10, DPI: 0, Timezone: "Europe/Bad;zone"}.normalized()
	if d.Width != 1280 || d.Height != 720 || d.DPI != 96 || d.Timezone != "" {
		t.Fatalf("got %#v", d)
	}
}

func TestRelayEndsWhenTheComputerDoesEvenWhileTheBrowserPings(t *testing.T) {
	ctx := context.Background()
	guacd := startGuacd(t, []string{"hostname"}, func(conn net.Conn, rd *Reader) {
		conn.Write([]byte(encode("disconnect")))
	})
	svc := newTestService(t, &fakeEngine{status: EngineStatus{State: EngineRunning, Address: guacd.ln.Addr().String()}})
	pc, _ := svc.Add(ctx, ComputerInput{Name: "pc", OS: "windows", Host: "pc"})
	sess, err := svc.Connect(ctx, pc.ID, Display{}, "wayy")
	if err != nil {
		t.Fatal(err)
	}
	ws := &pipeSocket{in: make(chan []byte), out: make(chan []byte, 64)}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case ws.in <- []byte("0.,4.ping,1.1;"):
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	go func() {
		for range ws.out {
		}
	}()
	relayed := make(chan error, 1)
	go func() { relayed <- sess.Relay(ctx, ws) }()
	select {
	case <-relayed:
	case <-time.After(3 * time.Second):
		t.Fatal("the relay outlived the computer's end of the session")
	}
	svc.Finish(ctx, sess, nil)
}

func TestConnectHandsGuacdTheAddressThisServerResolves(t *testing.T) {
	ctx := context.Background()
	guacd := startGuacd(t, []string{"hostname", "port"}, nil)
	svc := newTestService(t, &fakeEngine{status: EngineStatus{State: EngineRunning, Address: guacd.ln.Addr().String()}})
	svc.lookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "studio-mac" {
			return nil, errors.New("no such host")
		}
		return []net.IPAddr{{IP: net.ParseIP("fd7a:115c:a1e0::1")}, {IP: net.ParseIP("100.101.12.11")}}, nil
	}
	mac, _ := svc.Add(ctx, ComputerInput{Name: "studio-mac", OS: "macos", Host: "studio-mac"})
	sess, err := svc.Connect(ctx, mac.ID, Display{}, "wayy")
	if err != nil {
		t.Fatal(err)
	}
	svc.Finish(ctx, sess, nil)
	if got := strings.Join(guacd.connect, "|"); got != "VERSION_1_5_0|100.101.12.11|5900" {
		t.Fatalf("guacd was handed %q; it should get the IPv4 address this server resolved", got)
	}

	gone, _ := svc.Add(ctx, ComputerInput{Name: "gone", OS: "windows", Host: "gone-pc"})
	var guac *GuacError
	if _, err := svc.Connect(ctx, gone.ID, Display{}, "wayy"); !errors.As(err, &guac) || !strings.Contains(guac.Message, "gone-pc does not resolve") {
		t.Fatalf("an unresolvable name should say so: %v", err)
	}
}

// guacd 1.6 draws a GFX session on a timer and sends it at two to three
// times the size, so the classic path is asked for unless the computer
// insists on the pipeline. VNC has no such choice.
func TestRDPTakesTheClassicPathUnlessGfxIsOn(t *testing.T) {
	d := Display{Width: 1280, Height: 720, DPI: 96}
	pc := Computer{Protocol: ProtocolRDP, Host: "100.101.12.7", Port: 3389}
	if p := connectParams(pc, "", d); p["disable-gfx"] != "true" {
		t.Fatalf("an RDP session must ask for the classic path, got %q", p["disable-gfx"])
	}
	pc.Gfx = true
	if p := connectParams(pc, "", d); p["disable-gfx"] != "" {
		t.Fatalf("a computer that insists on the pipeline must get it, got %q", p["disable-gfx"])
	}
	mac := Computer{Protocol: ProtocolVNC, Host: "100.84.53.82", Port: 5900}
	if p := connectParams(mac, "", d); len(p) != 4 {
		t.Fatalf("VNC takes only the address and the sign-in, got %v", p)
	}
}

func TestGfxIsSavedForRDPAndClearedForVNC(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, &fakeEngine{})
	pc, err := svc.Add(ctx, ComputerInput{Name: "office-pc", OS: "windows", Host: "100.101.12.7", Gfx: true})
	if err != nil || !pc.Gfx {
		t.Fatalf("add with gfx: %+v %v", pc, err)
	}
	if pc, err = svc.Update(ctx, pc.ID, ComputerInput{Name: "office-pc", OS: "windows", Host: "100.101.12.7"}); err != nil || pc.Gfx {
		t.Fatalf("update without gfx: %+v %v", pc, err)
	}
	mac, err := svc.Add(ctx, ComputerInput{Name: "studio-mac", OS: "macos", Host: "100.84.53.82", Gfx: true})
	if err != nil || mac.Gfx {
		t.Fatalf("a VNC computer keeps no gfx: %+v %v", mac, err)
	}
}

// The session page reads its computer just before it connects; that read
// asks the tailnet for the direct path to a peer still on a relay, once a
// minute, never for one already direct.
func TestViewWarmsThePathToARelayedPeerOncePerMinute(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Unix(1_700_000_000, 0)
	peer := Peer{ID: "n1", Name: "zoro-1", Address: "100.84.53.82", Online: true, Relay: "fra"}
	var warmed []string
	done := make(chan string, 4)
	svc := New(Options{
		DB: st.DB, Seal: func(v string) (string, error) { return v, nil }, Open: func(v string) (string, error) { return v, nil },
		Engine: &fakeEngine{}, Bridge: &fakeEngine{},
		Tailnet: func(context.Context) (Tailnet, error) { return Tailnet{Installed: true, Peers: []Peer{peer}}, nil },
		Warm:    func(_ context.Context, address string) { done <- address },
		Now:     func() time.Time { return now },
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return nil, errors.New("refused")
		},
	})
	mac, err := svc.Add(ctx, ComputerInput{Name: "zoro-1", OS: "macos", Host: "100.84.53.82", PeerID: "n1"})
	if err != nil {
		t.Fatal(err)
	}
	wait := func() {
		select {
		case address := <-done:
			warmed = append(warmed, address)
		case <-time.After(2 * time.Second):
			t.Fatal("the path was not warmed")
		}
	}
	if _, err := svc.View(ctx, mac.ID); err != nil {
		t.Fatal(err)
	}
	wait()
	now = now.Add(30 * time.Second)
	svc.View(ctx, mac.ID)
	now = now.Add(31 * time.Second)
	svc.View(ctx, mac.ID)
	wait()
	if strings.Join(warmed, ",") != "100.84.53.82,100.84.53.82" {
		t.Fatalf("warmed %v", warmed)
	}
	peer.Direct, peer.Relay = true, ""
	now = now.Add(2 * time.Minute)
	svc.View(ctx, mac.ID)
	select {
	case address := <-done:
		t.Fatalf("a direct peer was warmed: %s", address)
	case <-time.After(200 * time.Millisecond):
	}
}
