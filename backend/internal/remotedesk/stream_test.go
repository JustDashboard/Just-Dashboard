package remotedesk

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBridgeSeedIsItsConfigOwnedByItsAccount(t *testing.T) {
	archive, err := bridgeSeed()
	if err != nil {
		t.Fatal(err)
	}
	r := tar.NewReader(bytes.NewReader(archive))
	h, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "moonlight-web/server/config.json" || h.Uid != 999 || h.Gid != 999 {
		t.Fatalf("header %+v", h)
	}
	var config struct {
		WebServer struct {
			Prefix    string `json:"url_path_prefix"`
			Forwarded struct {
				Header string `json:"username_header"`
				Create bool   `json:"auto_create_missing_user"`
			} `json:"forwarded_header"`
		} `json:"web_server"`
		Moonlight struct {
			Port int `json:"default_http_port"`
		} `json:"moonlight"`
	}
	if err := json.NewDecoder(r).Decode(&config); err != nil {
		t.Fatal(err)
	}
	if config.WebServer.Prefix != BridgePrefix || config.WebServer.Forwarded.Header != BridgeUserHeader ||
		!config.WebServer.Forwarded.Create || config.Moonlight.Port != SunshinePort {
		t.Fatalf("config %+v", config)
	}
}

// fakeBridge answers the parts of Moonlight Web's API the service uses, as
// the dashboard's account only, and holds a pairing open until Sunshine
// "takes" the PIN.
type fakeBridge struct {
	srv     *httptest.Server
	mu      sync.Mutex
	hosts   map[int]bool
	added   []string
	accepts chan bool
	users   []string
}

func newFakeBridge(t *testing.T) *fakeBridge {
	b := &fakeBridge{hosts: map[int]bool{}, accepts: make(chan bool, 1)}
	mux := http.NewServeMux()
	api := BridgePrefix + "/api"
	mux.HandleFunc("GET "+api+"/hosts", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := []map[string]any{}
		for id, paired := range b.hosts {
			state := "NotPaired"
			if paired {
				state = "Paired"
			}
			list = append(list, map[string]any{"host_id": id, "paired": state, "name": "box"})
		}
		json.NewEncoder(w).Encode(map[string]any{"hosts": list})
	})
	mux.HandleFunc("POST "+api+"/host", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Address string `json:"address"`
			Port    int    `json:"http_port"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		b.mu.Lock()
		b.added = append(b.added, net.JoinHostPort(in.Address, "47989"))
		b.hosts[41] = false
		b.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"host": map[string]any{"host_id": 41}})
	})
	mux.HandleFunc("POST "+api+"/pair", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, `{"Pin":"4821"}`+"\n")
		w.(http.Flusher).Flush()
		if !<-b.accepts {
			io.WriteString(w, `"PairError"`+"\n")
			return
		}
		b.mu.Lock()
		b.hosts[41] = true
		b.mu.Unlock()
		io.WriteString(w, `{"Paired":{"host_id":41}}`+"\n")
	})
	mux.HandleFunc("GET "+api+"/apps", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"apps":[{"app_id":7,"title":"Steam Big Picture"},{"app_id":3,"title":"Desktop"}]}`)
	})
	mux.HandleFunc("DELETE "+api+"/host", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		delete(b.hosts, 41)
		b.mu.Unlock()
	})
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.users = append(b.users, r.Header.Get(BridgeUserHeader))
		b.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *fakeBridge) address() string { return strings.TrimPrefix(b.srv.URL, "http://") }

// newStreamService is a service whose bridge is the fake, and whose
// computers' Sunshine port answers: every name resolves to 192.0.2.10, and a
// dial there connects to nothing but succeeds.
func newStreamService(t *testing.T, b *fakeBridge) *Service {
	s := newTestService(t, &fakeEngine{status: EngineStatus{State: EngineRunning, Address: "127.0.0.1:4822"}})
	s.bridge = &fakeEngine{status: EngineStatus{State: EngineRunning, Address: b.address()}}
	var d net.Dialer
	s.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "192.0.2.10:") {
			client, server := net.Pipe()
			server.Close()
			return client, nil
		}
		return d.DialContext(ctx, network, address)
	}
	return s
}

func TestPairShowsThePinAndFinishesWhenSunshineTakesIt(t *testing.T) {
	b := newFakeBridge(t)
	s := newStreamService(t, b)
	ctx := t.Context()
	c, err := s.Add(ctx, ComputerInput{Name: "studio-mac", OS: OSMac, Host: "studio-mac"})
	if err != nil {
		t.Fatal(err)
	}

	v, err := s.Stream(ctx, c.ID)
	if err != nil || v.Sunshine.State != ReachOpen || v.HostID != 0 || v.Pairing {
		t.Fatalf("before pairing: %+v %v", v, err)
	}

	v, err = s.Pair(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Pairing || v.Pin != "4821" || v.HostID != 41 || v.Paired {
		t.Fatalf("pairing: %+v", v)
	}
	// The bridge was handed the address this server resolved, not the name.
	if len(b.added) != 1 || b.added[0] != "192.0.2.10:47989" {
		t.Fatalf("added %v", b.added)
	}
	// Asking again while the PIN waits is the same pairing, not a second.
	if again, err := s.Pair(ctx, c.ID); err != nil || again.Pin != "4821" || len(b.added) != 1 {
		t.Fatalf("again: %+v %v %v", again, err, b.added)
	}

	b.accepts <- true
	deadline := time.Now().Add(3 * time.Second)
	for {
		v, err = s.Stream(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if v.Paired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never paired: %+v", v)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v.Pairing || v.Pin != "" || len(v.Apps) != 2 || desktopTitle(v.Apps) != "Desktop" {
		t.Fatalf("paired: %+v", v)
	}
	if saved, _ := s.Computer(ctx, c.ID); saved.StreamHostID != 41 {
		t.Fatalf("stream host not saved: %+v", saved)
	}
	for _, user := range b.users {
		if user != BridgeAccount {
			t.Fatalf("the bridge was asked as %q", user)
		}
	}
}

func desktopTitle(apps []StreamApp) string {
	for _, a := range apps {
		if a.Title == "Desktop" {
			return a.Title
		}
	}
	return ""
}

func TestPairThatSunshineRefusesSaysSoAndCanStartAgain(t *testing.T) {
	b := newFakeBridge(t)
	s := newStreamService(t, b)
	ctx := t.Context()
	c, _ := s.Add(ctx, ComputerInput{Name: "office-pc", OS: OSWindows, Host: "office-pc"})
	if _, err := s.Pair(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	b.accepts <- false
	deadline := time.Now().Add(3 * time.Second)
	var v StreamView
	for v.Error == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no error: %+v", v)
		}
		time.Sleep(20 * time.Millisecond)
		v, _ = s.Stream(ctx, c.ID)
	}
	if v.Pairing || v.Paired || !strings.Contains(v.Error, "did not accept the PIN") {
		t.Fatalf("refused: %+v", v)
	}
	// The host the bridge already knows is paired again, not added twice.
	if v, err := s.Pair(ctx, c.ID); err != nil || v.Pin != "4821" || len(b.added) != 1 {
		t.Fatalf("again: %+v %v %v", v, err, b.added)
	}
	b.accepts <- true
}

func TestStreamForgetsAHostTheBridgeNoLongerHas(t *testing.T) {
	b := newFakeBridge(t)
	s := newStreamService(t, b)
	ctx := t.Context()
	c, _ := s.Add(ctx, ComputerInput{Name: "office-pc", OS: OSWindows, Host: "office-pc"})
	if err := s.store.setStreamHost(ctx, c.ID, 99); err != nil {
		t.Fatal(err)
	}
	v, err := s.Stream(ctx, c.ID)
	if err != nil || v.HostID != 0 || v.Paired {
		t.Fatalf("stream %+v %v", v, err)
	}
	if saved, _ := s.Computer(ctx, c.ID); saved.StreamHostID != 0 {
		t.Fatalf("kept a host the bridge lost: %+v", saved)
	}
}

func TestPairNeedsTheBridgeAndSunshine(t *testing.T) {
	s := newTestService(t, &fakeEngine{status: EngineStatus{State: EngineRunning}})
	ctx := t.Context()
	c, _ := s.Add(ctx, ComputerInput{Name: "office-pc", OS: OSWindows, Host: "office-pc"})
	if _, err := s.Pair(ctx, c.ID); err != ErrBridgeNotRunning {
		t.Fatalf("no bridge: %v", err)
	}

	b := newFakeBridge(t)
	s = newStreamService(t, b)
	s.dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: errRefused{}}
	}
	c, _ = s.Add(ctx, ComputerInput{Name: "office-pc", OS: OSWindows, Host: "office-pc"})
	_, err := s.Pair(ctx, c.ID)
	var refused *StreamError
	if !errors.As(err, &refused) || !strings.Contains(refused.Message, "Sunshine is not answering on 192.0.2.10:47989 (refused)") {
		t.Fatalf("closed port: %v", err)
	}
	if len(b.added) != 0 {
		t.Fatalf("added a host Sunshine never answered for: %v", b.added)
	}
}

type errRefused struct{}

func (errRefused) Error() string { return "connection refused" }

func TestMovingAComputerUnpairsIt(t *testing.T) {
	s := newTestService(t, &fakeEngine{})
	ctx := t.Context()
	c, _ := s.Add(ctx, ComputerInput{Name: "office-pc", OS: OSWindows, Host: "office-pc"})
	s.store.setStreamHost(ctx, c.ID, 41)
	renamed, err := s.Update(ctx, c.ID, ComputerInput{Name: "Office PC", OS: OSWindows, Host: "office-pc"})
	if err != nil || renamed.StreamHostID != 41 {
		t.Fatalf("a rename unpaired it: %+v %v", renamed, err)
	}
	moved, err := s.Update(ctx, c.ID, ComputerInput{Name: "Office PC", OS: OSWindows, Host: "100.64.0.9"})
	if err != nil || moved.StreamHostID != 0 {
		t.Fatalf("a move kept the pairing: %+v %v", moved, err)
	}
}
