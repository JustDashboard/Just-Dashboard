package remotedesk

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/docker/go-connections/nat"
)

// The stream bridge is Moonlight Web, an unofficial Moonlight client that
// receives a computer's screen from Sunshine as hardware-encoded video and
// passes it to the browser. guacd draws a screen as images it has to ask for
// one at a time, which a 50 ms path caps at twenty a second; Sunshine pushes
// frames, so a stream runs at the computer's own rate.
//
// It sits beside guacd on the engine's network, published on loopback, and
// the backend proxies it under BridgePrefix. It trusts the user the backend
// names in BridgeUserHeader, so the dashboard's sign-in is the only one. The
// picture goes to the browser over WebRTC (UDP) when this server is on a
// tailnet: a block of bridgeMediaPorts UDP ports is published on this
// server's tailnet address only, which a browser that reached the dashboard
// over the tailnet can reach too. Without one, and as the browser's fallback,
// it rides the dashboard's own HTTPS connection (WebSocket transport).
const (
	BridgeImage   = "mrcreativ3001/moonlight-web-stream@sha256:4ee561ec4043526e93b0b3dba4926031c86ee3c9dab0333475a6b3c7117c701a"
	BridgeVersion = "2.10.0"
	BridgePrefix  = "/api/v1/remote-desktop/bridge"
	// BridgeUserHeader carries the dashboard's account to the bridge. The
	// proxy sets it on every request and drops any copy the browser sent.
	BridgeUserHeader = "X-Jd-Stream-User"

	bridgeName   = "just-dashboard-remote-stream"
	bridgeRole   = "stream"
	bridgeVolume = "just-dashboard-remote-stream-data"
	bridgeHome   = "/moonlight-web/server"
	// SunshinePort is where Sunshine answers Moonlight's pairing and app list.
	SunshinePort = 47989
	// bridgeMediaPorts is the UDP block for WebRTC: a stream holds one or
	// two, and a server streams to a few browsers at most.
	bridgeMediaPorts = 20
	// bridgeRevision changes whenever bridgeConfig does, so a bridge made by
	// an older dashboard is made again (its pairings kept) on the next start.
	bridgeRevision = "2"

	// The ways a browser can be sent a stream: over WebRTC on the published
	// UDP block, or over the dashboard's own connection.
	TransportWebRTC    = "webrtc"
	TransportWebSocket = "websocket"
)

var bridgeSpec = engineSpec{
	name: bridgeName, role: bridgeRole, product: "Moonlight Web", image: BridgeImage, version: BridgeVersion,
	port: nat.Port("8080/tcp"), prefer: 4823, volume: bridgeVolume, mountAt: bridgeHome,
	env: []string{
		"PATH_PREFIX=" + BridgePrefix,
		"FORWARDED_HEADER=" + BridgeUserHeader,
		"DISABLE_DEFAULT_WEBRTC_ICE_SERVERS=true",
		"LOG_LEVEL=info",
	},
	memory: 1 << 30, hostname: "remote-stream", mediaPorts: bridgeMediaPorts, mediaEnv: "WEBRTC_PORT_RANGE",
	seed:     bridgeSeed,
	revision: bridgeRevision, probe: probeBridge,
}

// NewDockerStreamBridge is the Moonlight Web container's runtime. media names
// the address its WebRTC ports are published on: this server's tailnet
// address, or none.
func NewDockerStreamBridge(host string, media func(ctx context.Context) string) Engine {
	spec := bridgeSpec
	spec.media = media
	return newDockerEngine(host, spec)
}

// bridgeConfig is the bridge's config.json as it prints its own defaults for
// this version, with the dashboard's prefix and account header, and WebRTC
// held to the published block: its candidates name the media address, the
// one the browser can reach, never the container's own. How a stream is sent
// (its transport, codec, size and rate) is a browser setting the session page
// writes before it opens the stream.
func bridgeConfig(media string, firstPort int) map[string]any {
	webrtc := map[string]any{
		"ice_servers": []any{}, "ice_server_script": nil, "port_range": nil, "nat_1to1": nil,
		"network_types": []string{"udp4"}, "include_loopback_candidates": false,
	}
	if media != "" {
		webrtc["port_range"] = map[string]any{"min": firstPort, "max": firstPort + bridgeMediaPorts - 1}
		webrtc["nat_1to1"] = map[string]any{"ips": []string{media}, "ice_candidate_type": "host"}
	}
	return map[string]any{
		"data_storage": map[string]any{
			"type": "json", "path": "server/data.json",
			"session_expiration_check_interval": map[string]any{"secs": 300, "nanos": 0},
		},
		"webrtc": webrtc,
		"web_server": map[string]any{
			"bind_address": "0.0.0.0:8080", "certificate": nil, "url_path_prefix": BridgePrefix,
			"session_cookie_secure": true, "session_cookie_expiration": map[string]any{"secs": 86400, "nanos": 0},
			"first_login_create_admin": true, "first_login_assign_global_hosts": true,
			"default_user_id": nil, "default_role_id": nil,
			"forwarded_header": map[string]any{"username_header": BridgeUserHeader, "auto_create_missing_user": true},
		},
		"moonlight":     map[string]any{"default_http_port": SunshinePort, "pair_device_name": "Just Dashboard"},
		"streamer_path": "./streamer",
		"log":           map[string]any{"level_filter": "INFO", "file_path": nil, "dev_venator": false},
	}
}

func bridgeSeed(media string, firstPort int) ([]byte, error) {
	body, err := json.MarshalIndent(bridgeConfig(media, firstPort), "", "  ")
	if err != nil {
		return nil, err
	}
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	// The image runs as its own moonlight account (999), which writes
	// data.json beside this file.
	if err := w.WriteHeader(&tar.Header{Name: "moonlight-web/server/config.json", Mode: 0o644, Size: int64(len(body)), Uid: 999, Gid: 999, ModTime: time.Now()}); err != nil {
		return nil, err
	}
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

func probeBridge(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+BridgePrefix+"/", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 500 {
		return fmt.Errorf("answered %d", res.StatusCode)
	}
	return nil
}

// BridgeAccount is the one account the dashboard holds on the bridge. Who
// may stream is the dashboard's decision; the bridge only keeps the
// pairings, so every administrator sees the same paired computers.
const BridgeAccount = "just-dashboard"

var ErrBridgeNotRunning = errors.New("the stream bridge is not running")

// StreamApp is one thing Sunshine offers to stream; "Desktop" is the screen.
type StreamApp struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// StreamView is a computer's stream as the session page needs it: whether
// the bridge runs, whether Sunshine answers, and how far pairing has got.
type StreamView struct {
	Bridge EngineStatus `json:"bridge"`
	// Transport is how this browser can be sent the picture: WebRTC when the
	// bridge publishes its UDP block and the browser is on the same tailnet,
	// the WebSocket otherwise. The handler sets it, since it knows the browser.
	Transport string      `json:"transport"`
	Address   string      `json:"address,omitempty"`
	Sunshine  Reach       `json:"sunshine"`
	HostID    int         `json:"hostId"`
	Paired    bool        `json:"paired"`
	Pairing   bool        `json:"pairing"`
	Pin       string      `json:"pin,omitempty"`
	Error     string      `json:"error,omitempty"`
	Apps      []StreamApp `json:"apps"`
}

// pairing is a PIN the bridge is waiting for someone to type into Sunshine.
type pairing struct {
	hostID int
	pin    string
	err    string
	done   bool
}

// pairWait is how long a PIN stays open. Sunshine shows nothing on the
// computer itself; the PIN is typed into its own page, which is likely
// reached through a Control session, so it is given time.
const pairWait = 10 * time.Minute

func (s *Service) Bridge(ctx context.Context) EngineStatus { return s.bridge.Status(ctx) }

func (s *Service) InstallBridge(ctx context.Context, progress func(string)) error {
	s.ForgetBridgeAddress()
	return s.bridge.Install(ctx, progress)
}

// UpgradeBridge makes a running bridge again when an older dashboard made it,
// or for another tailnet address; its pairings are on its volume and stay.
// Install is a no-op for one that is current.
func (s *Service) UpgradeBridge(ctx context.Context, progress func(string)) error {
	if s.bridge.Status(ctx).State != EngineRunning {
		return nil
	}
	return s.InstallBridge(ctx, progress)
}

// RemoveBridge takes the bridge and its pairings away; every computer is
// paired again after the next install.
func (s *Service) RemoveBridge(ctx context.Context) error {
	if n := s.streamsOpen(); n > 0 {
		return &BusyError{Live: n}
	}
	s.ForgetBridgeAddress()
	if err := s.bridge.Remove(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.pairs = map[string]*pairing{}
	s.mu.Unlock()
	return s.store.forgetStreamHosts(ctx)
}

// BridgeAddress is where the proxy finds the bridge. It is asked for on every
// file the stream page loads, so Docker is asked only every few seconds.
func (s *Service) BridgeAddress(ctx context.Context) (string, error) {
	s.mu.Lock()
	address, fresh := s.bridgeAt, s.now().Before(s.bridgeUntil)
	s.mu.Unlock()
	if fresh {
		return address, nil
	}
	st := s.bridge.Status(ctx)
	if st.State != EngineRunning || st.Address == "" {
		return "", ErrBridgeNotRunning
	}
	s.mu.Lock()
	s.bridgeAt, s.bridgeUntil = st.Address, s.now().Add(10*time.Second)
	s.mu.Unlock()
	return st.Address, nil
}

// ForgetBridgeAddress drops the cached address, for a bridge that stopped
// answering there.
func (s *Service) ForgetBridgeAddress() {
	s.mu.Lock()
	s.bridgeAt, s.bridgeUntil = "", time.Time{}
	s.mu.Unlock()
}

// StreamOpened and StreamClosed count the streams the proxy is carrying, so
// the bridge is not removed under one.
func (s *Service) StreamOpened() {
	s.mu.Lock()
	s.streams++
	s.mu.Unlock()
}

func (s *Service) StreamClosed() {
	s.mu.Lock()
	s.streams--
	s.mu.Unlock()
}

func (s *Service) streamsOpen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams
}

// Stream reads a computer's stream: the bridge, Sunshine's port on the
// computer, the pairing and, once paired, the apps Sunshine offers.
func (s *Service) Stream(ctx context.Context, id string) (StreamView, error) {
	c, err := s.store.get(ctx, id)
	if err != nil {
		return StreamView{}, err
	}
	v := StreamView{Bridge: s.bridge.Status(ctx), Apps: []StreamApp{}}
	if target, err := s.resolve(ctx, c.Host); err != nil {
		v.Sunshine = Reach{State: ReachClosed, Error: "unknown host", CheckedAt: s.now().Unix()}
	} else {
		v.Address = target
		v.Sunshine = s.probeSunshine(ctx, target)
	}
	s.mu.Lock()
	if p := s.pairs[id]; p != nil && !p.done {
		if p.err != "" {
			v.Error = p.err
		} else {
			v.Pairing, v.Pin = true, p.pin
		}
	}
	s.mu.Unlock()
	if v.Bridge.State != EngineRunning || v.Bridge.Address == "" || c.StreamHostID == 0 {
		return v, nil
	}
	api := s.bridgeAPI(v.Bridge.Address)
	hosts, err := api.hosts(ctx)
	if err != nil {
		v.Error = "The stream bridge did not answer: " + err.Error()
		return v, nil
	}
	paired, known := hosts[c.StreamHostID]
	if !known {
		// The bridge was reinstalled or the host removed there: the computer
		// pairs again from the start.
		s.store.setStreamHost(ctx, id, 0)
		return v, nil
	}
	v.HostID, v.Paired = c.StreamHostID, paired
	if paired {
		// The bridge records the pairing a moment before it answers the
		// request still waiting on it.
		v.Pairing, v.Pin, v.Error = false, "", ""
	}
	if !paired || v.Sunshine.State != ReachOpen {
		return v, nil
	}
	if v.Apps, err = api.apps(ctx, c.StreamHostID); err != nil {
		v.Apps = []StreamApp{}
		v.Error = "Sunshine did not list what it streams: " + err.Error()
	}
	return v, nil
}

// Pair adds the computer to the bridge and starts pairing with Sunshine on
// it. The PIN comes back at once; the bridge then waits for it to be typed
// into Sunshine's own page, and Stream reports when it was.
func (s *Service) Pair(ctx context.Context, id string) (StreamView, error) {
	c, err := s.store.get(ctx, id)
	if err != nil {
		return StreamView{}, err
	}
	st := s.bridge.Status(ctx)
	if st.State != EngineRunning || st.Address == "" {
		return StreamView{}, ErrBridgeNotRunning
	}
	target, err := s.resolve(ctx, c.Host)
	if err != nil {
		return StreamView{}, invalid("%s does not resolve from this server", c.Host)
	}
	if reach := s.probeSunshine(ctx, target); reach.State != ReachOpen {
		if reach.Error == SunshineStuck {
			return StreamView{}, &StreamError{Message: fmt.Sprintf("Sunshine on %s accepts connections on %s:%d but does not answer them: it is running but stuck. Restart it on the computer.", c.Name, target, SunshinePort)}
		}
		return StreamView{}, &StreamError{Message: fmt.Sprintf("Sunshine is not answering on %s:%d (%s)", target, SunshinePort, reach.Error)}
	}
	s.mu.Lock()
	if p := s.pairs[id]; p != nil && !p.done && p.err == "" {
		s.mu.Unlock()
		return s.Stream(ctx, id)
	}
	s.mu.Unlock()

	api := s.bridgeAPI(st.Address)
	hosts, err := api.hosts(ctx)
	if err != nil {
		return StreamView{}, &StreamError{Message: "The stream bridge did not answer: " + err.Error()}
	}
	hostID := c.StreamHostID
	if paired, known := hosts[hostID]; !known || hostID == 0 {
		if hostID, err = api.addHost(ctx, target, SunshinePort); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return StreamView{}, &StreamError{Message: fmt.Sprintf("Sunshine on %s did not answer the stream bridge within 15 seconds. Restart it on the computer.", c.Name)}
			}
			return StreamView{}, &StreamError{Message: "The stream bridge could not reach Sunshine: " + err.Error()}
		}
		if err := s.store.setStreamHost(ctx, id, hostID); err != nil {
			return StreamView{}, err
		}
	} else if paired {
		return s.Stream(ctx, id)
	}

	// The pairing outlives this request: it ends when the PIN is typed into
	// Sunshine, or when Sunshine or the wait gives up.
	pairCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pairWait)
	pin, result, err := api.pair(pairCtx, hostID)
	if err != nil {
		cancel()
		return StreamView{}, &StreamError{Message: "Sunshine would not start pairing: " + err.Error()}
	}
	p := &pairing{hostID: hostID, pin: pin}
	s.mu.Lock()
	s.pairs[id] = p
	s.mu.Unlock()
	go func() {
		defer cancel()
		err := <-result
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			p.err = "Pairing did not finish: " + err.Error() + ". Pair again for a new PIN."
			return
		}
		p.done = true
	}()
	return s.Stream(ctx, id)
}

// Unpair forgets the computer on the bridge, for pairing it again.
func (s *Service) Unpair(ctx context.Context, id string) error {
	c, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.pairs, id)
	s.mu.Unlock()
	if c.StreamHostID == 0 {
		return nil
	}
	if st := s.bridge.Status(ctx); st.State == EngineRunning && st.Address != "" {
		if err := s.bridgeAPI(st.Address).deleteHost(ctx, c.StreamHostID); err != nil {
			return &StreamError{Message: "The stream bridge could not forget " + c.Name + ": " + err.Error()}
		}
	}
	return s.store.setStreamHost(ctx, id, 0)
}

// SunshineStuck is the error of a Sunshine that accepts a connection on its
// pairing port and then answers nothing: running, but not serving.
const SunshineStuck = "stuck"

// sunshineWait is how long Sunshine has to answer the probe.
var sunshineWait = 3 * time.Second

// probeSunshine asks Sunshine for its server info, the first thing the bridge
// asks when a computer is added. A connect alone is not enough: a Sunshine
// that is running but stuck accepts the connection and then says nothing,
// and the bridge waits on it until it gives up. Any HTTP answer is an answer;
// the round trip is the connect's, as the other probes measure it.
func (s *Service) probeSunshine(ctx context.Context, target string) Reach {
	ctx, cancel := context.WithTimeout(ctx, sunshineWait)
	defer cancel()
	started := s.now()
	var connected atomic.Int64
	client := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := s.dial(ctx, network, address)
			if err == nil {
				connected.Store(int64(s.now().Sub(started)) + 1)
			}
			return conn, err
		},
	}}
	r := Reach{State: ReachClosed, CheckedAt: s.now().Unix()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(target, strconv.Itoa(SunshinePort))+"/serverinfo", nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	res, err := client.Do(req)
	took := time.Duration(connected.Load() - 1)
	switch {
	case err == nil:
		res.Body.Close()
		r.State, r.LatencyMs = ReachOpen, float64(took.Microseconds())/1000
	case took >= 0 && errors.Is(err, context.DeadlineExceeded):
		r.Error = SunshineStuck
	default:
		r.Error = reachError(err)
	}
	return r
}

// StreamError is the bridge or Sunshine refusing, said in the reader's terms.
type StreamError struct{ Message string }

func (e *StreamError) Error() string { return e.Message }

// bridgeAPI is the bridge's own JSON API, spoken as the dashboard's account.
type bridgeAPI struct {
	base   string
	client *http.Client
}

func (s *Service) bridgeAPI(address string) bridgeAPI {
	return bridgeAPI{
		base:   "http://" + address + BridgePrefix + "/api",
		client: &http.Client{Transport: &http.Transport{DialContext: s.dial}},
	}
}

func (b bridgeAPI) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set(BridgeUserHeader, BridgeAccount)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		if msg := strings.TrimSpace(string(detail)); msg != "" {
			return nil, fmt.Errorf("%s %s answered %d: %s", method, path, res.StatusCode, msg)
		}
		return nil, fmt.Errorf("%s %s answered %d", method, path, res.StatusCode)
	}
	return res, nil
}

// hosts reads the bridge's own record of each host and whether it is
// paired. The bridge streams fresher details after this first line by
// asking each host; the record is all that is needed here.
func (b bridgeAPI) hosts(ctx context.Context) (map[int]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := b.do(ctx, http.MethodGet, "/hosts", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var first struct {
		Hosts []struct {
			HostID int    `json:"host_id"`
			Paired string `json:"paired"`
		} `json:"hosts"`
	}
	if err := json.NewDecoder(res.Body).Decode(&first); err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(first.Hosts))
	for _, h := range first.Hosts {
		out[h.HostID] = h.Paired == "Paired"
	}
	return out, nil
}

func (b bridgeAPI) addHost(ctx context.Context, address string, port int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := b.do(ctx, http.MethodPost, "/host", map[string]any{"address": address, "http_port": port})
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	var out struct {
		Host struct {
			HostID int `json:"host_id"`
		} `json:"host"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.Host.HostID == 0 {
		return 0, errors.New("the bridge did not name the host it added")
	}
	return out.Host.HostID, nil
}

func (b bridgeAPI) deleteHost(ctx context.Context, hostID int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := b.do(ctx, http.MethodDelete, "/host?host_id="+strconv.Itoa(hostID), nil)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func (b bridgeAPI) apps(ctx context.Context, hostID int) ([]StreamApp, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	res, err := b.do(ctx, http.MethodGet, "/apps?host_id="+strconv.Itoa(hostID), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Apps []struct {
			AppID int    `json:"app_id"`
			Title string `json:"title"`
		} `json:"apps"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	apps := make([]StreamApp, 0, len(out.Apps))
	for _, a := range out.Apps {
		apps = append(apps, StreamApp{ID: a.AppID, Title: a.Title})
	}
	return apps, nil
}

// pair asks the bridge to pair with the host. Its answer is one JSON line
// with the PIN, and a second, much later, when Sunshine took it or gave up.
func (b bridgeAPI) pair(ctx context.Context, hostID int) (string, <-chan error, error) {
	res, err := b.do(ctx, http.MethodPost, "/pair", map[string]any{"host_id": hostID})
	if err != nil {
		return "", nil, err
	}
	dec := json.NewDecoder(res.Body)
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		res.Body.Close()
		return "", nil, err
	}
	var pin struct {
		Pin string `json:"Pin"`
	}
	if json.Unmarshal(first, &pin) != nil || pin.Pin == "" {
		res.Body.Close()
		return "", nil, fmt.Errorf("the bridge answered %s", strings.Trim(string(first), `"`))
	}
	result := make(chan error, 1)
	go func() {
		defer res.Body.Close()
		var second json.RawMessage
		if err := dec.Decode(&second); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				err = errors.New("the PIN was not entered in time")
			}
			result <- err
			return
		}
		var done struct {
			Paired json.RawMessage `json:"Paired"`
		}
		if json.Unmarshal(second, &done) != nil || len(done.Paired) == 0 {
			result <- errors.New("Sunshine did not accept the PIN")
			return
		}
		result <- nil
	}()
	return pin.Pin, result, nil
}
