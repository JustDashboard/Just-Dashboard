package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/gorilla/websocket"
)

type liveRoom struct {
	t       *testing.T
	s       *Server
	srv     *httptest.Server
	cookies map[string]string
	path    string
	id      int64
}

func newLiveRoom(t *testing.T) (*liveRoom, *client) {
	t.Helper()
	admin, s := newClient(t)
	id, path := newTestBoard(t, admin)
	srv := httptest.NewServer(admin.h)
	t.Cleanup(srv.Close)
	room := &liveRoom{t: t, s: s, srv: srv, path: path, id: id, cookies: map[string]string{"tester": admin.cookie}}
	return room, admin
}

func (r *liveRoom) cookie(name string, role auth.Role) string {
	if c, ok := r.cookies[name]; ok {
		return c
	}
	r.cookies[name] = signInAs(r.t, r.s, name, role)
	return r.cookies[name]
}

func (r *liveRoom) dial(cookie string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if cookie != "" {
		header.Set("Cookie", cookie)
	}
	url := "ws" + strings.TrimPrefix(r.srv.URL, "http") + r.path + "/live"
	return websocket.DefaultDialer.Dial(url, header)
}

func (r *liveRoom) join(name string, role auth.Role) *websocket.Conn {
	r.t.Helper()
	conn, _, err := r.dial(r.cookie(name, role))
	if err != nil {
		r.t.Fatalf("%s joins: %v", name, err)
	}
	r.t.Cleanup(func() { conn.Close() })
	return conn
}

type liveMessage map[string]json.RawMessage

func (m liveMessage) str(key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func readLive(t *testing.T, c *websocket.Conn) liveMessage {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("waiting for a frame: %v", err)
	}
	var m liveMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("frame %q: %v", data, err)
	}
	return m
}

// expectNext is how a test shows a frame was not delivered: the sender follows
// it with a presence frame, and the receiver's next frame must be that one.
// A read that times out cannot be used for this, because it ends the socket.
func expectNext(t *testing.T, sender *websocket.Conn, senderID string, receivers ...*websocket.Conn) {
	t.Helper()
	sendLive(t, sender, `{"type":"idle","state":"active"}`)
	for _, c := range receivers {
		if got := readLive(t, c); got.str("type") != "idle" || got.str("from") != senderID {
			t.Fatalf("an unexpected frame before the marker: %v", got)
		}
	}
}

func sendLive(t *testing.T, c *websocket.Conn, frame string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatal(err)
	}
}

func TestBoardLiveIntroducesPeersAndForwardsScenes(t *testing.T) {
	room, _ := newLiveRoom(t)
	alice := room.join("tester", auth.RoleAdmin)
	helloA := readLive(t, alice)
	if helloA.str("type") != "hello" || helloA.str("you") == "" || string(helloA["peers"]) != "[]" {
		t.Fatalf("first hello = %v", helloA)
	}
	idA := helloA.str("you")

	bob := room.join("bob", auth.RoleAdmin)
	helloB := readLive(t, bob)
	var others []struct {
		ID, User, Name string
		CanWrite       bool
	}
	if err := json.Unmarshal(helloB["peers"], &others); err != nil || len(others) != 1 || others[0].ID != idA ||
		others[0].User != "tester" || !others[0].CanWrite {
		t.Fatalf("second hello peers = %s (%v)", helloB["peers"], err)
	}
	idB := helloB.str("you")
	joined := readLive(t, alice)
	var peer struct {
		ID, User, Name string
		CanWrite       bool
	}
	if joined.str("type") != "joined" || json.Unmarshal(joined["peer"], &peer) != nil || peer.ID != idB || peer.User != "bob" || peer.Name == "" || !peer.CanWrite {
		t.Fatalf("joined = %v", joined)
	}

	// Scenes go to everyone else, forwarded as they came.
	sendLive(t, alice, `{"type":"scene","elements":[{"id":"x","version":1, "points":[[0,1.50]]}]}`)
	got := readLive(t, bob)
	if got.str("type") != "scene" || got.str("from") != idA || string(got["elements"]) != `[{"id":"x","version":1,"points":[[0,1.50]]}]` {
		t.Fatalf("forwarded scene = %v", got)
	}

	// Presence and file ids follow, with the sender named.
	sendLive(t, bob, `{"type":"pointer","pointer":{"x":1,"y":2,"tool":"pointer"},"button":"down","selectedElementIds":{"x":true}}`)
	got = readLive(t, alice)
	if got.str("type") != "pointer" || got.str("from") != idB || string(got["pointer"]) != `{"x":1,"y":2,"tool":"pointer"}` ||
		got.str("button") != "down" || string(got["selectedElementIds"]) != `{"x":true}` {
		t.Fatalf("pointer = %v", got)
	}
	sendLive(t, bob, `{"type":"idle","state":"away"}`)
	if got = readLive(t, alice); got.str("type") != "idle" || got.str("state") != "away" || got.str("from") != idB {
		t.Fatalf("idle = %v", got)
	}
	sendLive(t, bob, `{"type":"idle","state":"dancing"}`)
	sendLive(t, bob, `{"type":"files","ids":["f1","f2"]}`)
	if got = readLive(t, alice); got.str("type") != "files" || string(got["ids"]) != `["f1","f2"]` || got.str("from") != idB {
		t.Fatalf("files = %v", got)
	}
	sendLive(t, bob, `{"type":"unheard-of","x":1}`)
	sendLive(t, bob, `not json`)
	sendLive(t, bob, `{"type":"scene","elements":"nope"}`)
	expectNext(t, bob, idB, alice)

	// A scene as large as a real board is carried, not refused.
	big := `[` + strings.Repeat(`{"id":"e","version":1,"text":"`+strings.Repeat("a", 900)+`"},`, 3000) + `{"id":"last"}]`
	sendLive(t, alice, fmt.Sprintf(`{"type":"scene","elements":%s}`, big))
	if got = readLive(t, bob); string(got["elements"]) != big {
		t.Fatalf("a %d byte scene arrived as %d bytes", len(big), len(got["elements"]))
	}

	bob.Close()
	if left := readLive(t, alice); left.str("type") != "left" || left.str("id") != idB {
		t.Fatalf("left = %v", left)
	}
}

func TestBoardLiveSceneCanBeAddressedToOnePeer(t *testing.T) {
	room, _ := newLiveRoom(t)
	a := room.join("tester", auth.RoleAdmin)
	idA := readLive(t, a).str("you")
	b := room.join("bob", auth.RoleAdmin)
	idB := readLive(t, b).str("you")
	readLive(t, a)
	c := room.join("carol", auth.RoleAdmin)
	readLive(t, c)
	readLive(t, a)
	readLive(t, b)

	sendLive(t, a, fmt.Sprintf(`{"type":"scene","to":%q,"elements":[{"id":"only-bob"}]}`, idB))
	if got := readLive(t, b); got.str("type") != "scene" || string(got["elements"]) != `[{"id":"only-bob"}]` {
		t.Fatalf("addressed scene = %v", got)
	}
	expectNext(t, a, idA, b, c)
	sendLive(t, a, `{"type":"scene","to":"nobody","elements":[]}`)
	expectNext(t, a, idA, b, c)
}

func TestBoardLiveReadOnlyPeersWatchButDoNotChange(t *testing.T) {
	room, _ := newLiveRoom(t)
	writer := room.join("tester", auth.RoleAdmin)
	readLive(t, writer)
	viewer := room.join("viewer", auth.RoleReadOnly)
	idV := readLive(t, viewer).str("you")
	var joined struct{ Peer struct{ CanWrite bool } }
	data, _ := json.Marshal(readLive(t, writer))
	if json.Unmarshal(data, &joined) != nil || joined.Peer.CanWrite {
		t.Fatalf("a viewer joined as %s", data)
	}

	sendLive(t, viewer, `{"type":"scene","elements":[{"id":"nope"}]}`)
	if got := readLive(t, viewer); got.str("type") != "error" || got.str("message") == "" {
		t.Fatalf("viewer's scene = %v", got)
	}
	sendLive(t, viewer, `{"type":"files","ids":["f"]}`)
	if got := readLive(t, viewer); got.str("type") != "error" {
		t.Fatalf("viewer's files = %v", got)
	}

	sendLive(t, viewer, `{"type":"pointer","pointer":{"x":3,"y":4}}`)
	if got := readLive(t, writer); got.str("type") != "pointer" || got.str("from") != idV {
		t.Fatalf("viewer's pointer = %v", got)
	}
	// And the viewer sees what the writer draws.
	sendLive(t, writer, `{"type":"scene","elements":[]}`)
	if got := readLive(t, viewer); got.str("type") != "scene" {
		t.Fatalf("viewer's view = %v", got)
	}
}

func TestBoardLiveDropsOversizedSmallFrames(t *testing.T) {
	room, _ := newLiveRoom(t)
	a := room.join("tester", auth.RoleAdmin)
	readLive(t, a)
	b := room.join("bob", auth.RoleAdmin)
	readLive(t, b)
	readLive(t, a)

	sendLive(t, a, fmt.Sprintf(`{"type":"pointer","pointer":{"x":1},"pad":%q}`, strings.Repeat("a", maxBoardSmallFrame)))
	sendLive(t, a, fmt.Sprintf(`{"type":"files","ids":[%q]}`, strings.Repeat("a", maxBoardSmallFrame)))
	sendLive(t, a, `{"type":"pointer","pointer":{"x":2}}`)
	if got := readLive(t, b); got.str("type") != "pointer" || string(got["pointer"]) != `{"x":2}` {
		t.Fatalf("after the oversized frames: %v", got)
	}
}

func TestBoardLiveRoomHoldsThirtyTwoPeers(t *testing.T) {
	room, _ := newLiveRoom(t)
	var first *websocket.Conn
	for i := range maxBoardPeers {
		c := room.join("tester", auth.RoleAdmin)
		if i == 0 {
			first = c
		}
		readLive(t, c)
	}
	extra := room.join("tester", auth.RoleAdmin)
	if got := readLive(t, extra); got.str("type") != "error" || got.str("message") == "" {
		t.Fatalf("the 33rd peer = %v", got)
	}
	_ = extra.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := extra.ReadMessage(); err == nil {
		t.Fatal("the 33rd peer's socket stayed open")
	}
	// The room is unharmed: it was told of the 31 who joined after the first.
	for range maxBoardPeers - 1 {
		if got := readLive(t, first); got.str("type") != "joined" {
			t.Fatalf("first peer saw %v", got)
		}
	}
	first.Close()
}

func TestBoardLiveTellsTheRoomWhenTheBoardIsDeleted(t *testing.T) {
	room, admin := newLiveRoom(t)
	a := room.join("tester", auth.RoleAdmin)
	readLive(t, a)
	b := room.join("viewer", auth.RoleReadOnly)
	readLive(t, b)
	readLive(t, a)

	if rec := admin.do(http.MethodDelete, room.path, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range []*websocket.Conn{a, b} {
		if got := readLive(t, c); got.str("type") != "deleted" {
			t.Fatalf("after the delete: %v", got)
		}
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, err := c.ReadMessage(); err == nil {
			t.Fatal("the socket stayed open after the board was deleted")
		}
	}
	room.s.boards.hub.mu.Lock()
	defer room.s.boards.hub.mu.Unlock()
	if len(room.s.boards.hub.rooms) != 0 {
		t.Errorf("rooms left: %v", room.s.boards.hub.rooms)
	}
}

func TestBoardLiveRefusesBeforeUpgrading(t *testing.T) {
	room, _ := newLiveRoom(t)
	room.path = "/api/v1/boards/9999"
	_, res, err := room.dial(room.cookie("tester", auth.RoleAdmin))
	if err == nil || res == nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing board: %v %v", err, res)
	}
	room.path = "/api/v1/boards/abc"
	if _, res, err = room.dial(room.cookie("tester", auth.RoleAdmin)); err == nil || res == nil || res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: %v %v", err, res)
	}
	if _, res, err = room.dial(""); err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no session: %v %v", err, res)
	}
}

func TestBoardHubDropsAPeerThatCannotKeepUp(t *testing.T) {
	t.Parallel()
	var hub boardHub
	dropped := false
	slow := &boardPeer{boardPeerInfo: boardPeerInfo{ID: "slow"}, out: make(chan []byte, 2), cancel: func() { dropped = true }}
	sender := &boardPeer{boardPeerInfo: boardPeerInfo{ID: "sender"}, out: make(chan []byte, 8), cancel: func() {}}
	if !hub.join(1, slow) || !hub.join(1, sender) {
		t.Fatal("could not join")
	}
	// hello filled one slot of the slow peer's queue, and joined another.
	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.relay(1, sender, "", []byte(`{"type":"pointer"}`))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a peer that does not read held the room")
	}
	if !dropped {
		t.Error("the peer that cannot keep up was not dropped")
	}
	hub.leave(1, slow)
	hub.leave(1, sender)
	if len(hub.rooms) != 0 {
		t.Errorf("rooms = %v", hub.rooms)
	}
}
