package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/wsx"
	"github.com/gorilla/websocket"
)

// Live collaboration is a relay: the people who have a board open exchange
// what they draw through this server and nothing is kept here. Saving stays
// the audited PUT, so a room that dies loses no more than an unsaved tab.

const (
	maxBoardPeers = 32
	// A scene frame carries the element array, which can be most of a board;
	// images travel by id, never in a frame.
	maxBoardLiveFrame = 16 << 20
	// Pointers, presence and file ids are small. A larger one is a mistake.
	maxBoardSmallFrame = 64 << 10
	// A peer that has this many frames waiting, or this many bytes of them, is
	// not keeping up, and is dropped rather than made to hold the room back.
	peerQueueFrames = 256
	peerQueueBytes  = 64 << 20
)

// boardsState is what the board routes keep between requests.
type boardsState struct {
	// fetch downloads shared libraries; a test gives it a transport that
	// reaches its own server.
	fetch *http.Client
	hub   boardHub
	// What one board's stored images may add up to; a test lowers them.
	maxFiles, maxFileBytes int64
}

func newBoardsState() *boardsState {
	return &boardsState{fetch: newLibraryClient(), maxFiles: maxBoardFiles, maxFileBytes: maxBoardFileTotal}
}

type boardPeerInfo struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	Name     string `json:"name"`
	CanWrite bool   `json:"canWrite"`
}

type boardPeer struct {
	boardPeerInfo
	out     chan []byte
	pending atomic.Int64
	cancel  context.CancelFunc
}

type boardRoom struct {
	peers map[string]*boardPeer
	// order is who joined, oldest first, so hello lists peers stably.
	order []string
}

type boardHub struct {
	mu    sync.Mutex
	rooms map[int64]*boardRoom
}

// queue hands a frame to a peer without waiting. The caller holds the hub's
// lock, which is also what lets the hub close a peer's queue safely.
func (p *boardPeer) queue(frame []byte) {
	if p.pending.Add(int64(len(frame))) > peerQueueBytes {
		p.cancel()
		return
	}
	select {
	case p.out <- frame:
	default:
		p.cancel()
	}
}

func marshalFrame(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func errorFrame(message string) []byte {
	return marshalFrame(map[string]string{"type": "error", "message": message})
}

// join adds a peer and tells the room. It answers false when the room is full.
func (h *boardHub) join(boardID int64, p *boardPeer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms == nil {
		h.rooms = map[int64]*boardRoom{}
	}
	room := h.rooms[boardID]
	if room == nil {
		room = &boardRoom{peers: map[string]*boardPeer{}}
		h.rooms[boardID] = room
	}
	if len(room.peers) >= maxBoardPeers {
		return false
	}
	others := make([]boardPeerInfo, 0, len(room.order))
	for _, id := range room.order {
		others = append(others, room.peers[id].boardPeerInfo)
	}
	p.queue(marshalFrame(map[string]any{"type": "hello", "you": p.ID, "peers": others}))
	joined := marshalFrame(map[string]any{"type": "joined", "peer": p.boardPeerInfo})
	for _, other := range room.peers {
		other.queue(joined)
	}
	room.peers[p.ID] = p
	room.order = append(room.order, p.ID)
	return true
}

// leave removes a peer and tells the room. It does nothing for a peer the hub
// already removed, which is what closing a deleted board's room does.
func (h *boardHub) leave(boardID int64, p *boardPeer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[boardID]
	if room == nil || room.peers[p.ID] != p {
		return
	}
	delete(room.peers, p.ID)
	for i, id := range room.order {
		if id == p.ID {
			room.order = append(room.order[:i], room.order[i+1:]...)
			break
		}
	}
	if len(room.peers) == 0 {
		delete(h.rooms, boardID)
		return
	}
	left := marshalFrame(map[string]any{"type": "left", "id": p.ID})
	for _, other := range room.peers {
		other.queue(left)
	}
}

// relay delivers a frame to one peer, or to everyone but the sender.
func (h *boardHub) relay(boardID int64, from *boardPeer, to string, frame []byte) {
	if frame == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[boardID]
	if room == nil || room.peers[from.ID] != from {
		return
	}
	if to != "" {
		if target := room.peers[to]; target != nil && target != from {
			target.queue(frame)
		}
		return
	}
	for id, p := range room.peers {
		if id != from.ID {
			p.queue(frame)
		}
	}
}

// closeRoom tells everyone a board is gone and ends their sockets once the
// frame has been written. The hub is the only sender on a queue, so it is the
// one to close it.
func (h *boardHub) closeRoom(boardID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[boardID]
	delete(h.rooms, boardID)
	if room == nil {
		return
	}
	deleted := marshalFrame(map[string]string{"type": "deleted"})
	for _, p := range room.peers {
		p.queue(deleted)
		close(p.out)
	}
}

func newPeerID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Server) handleBoardLive(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var exists int
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT 1 FROM boards WHERE id = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return errBoardNotFound()
	} else if err != nil {
		return httpx.Internal(err)
	}
	peerID, err := newPeerID()
	if err != nil {
		return httpx.Internal(err)
	}
	principal := httpx.MustPrincipal(r)
	name := principal.Username()
	if principal.User != nil && principal.User.DisplayName != "" {
		name = principal.User.DisplayName
	}

	conn, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil // the upgrader has already written a response
	}
	defer conn.Close()
	conn.SetReadLimit(maxBoardLiveFrame)

	ctx, cancel := contextWithCancel(r)
	defer cancel()
	peer := &boardPeer{
		boardPeerInfo: boardPeerInfo{ID: peerID, User: principal.Username(), Name: name, CanWrite: principal.Can(auth.CapServiceControl)},
		out:           make(chan []byte, peerQueueFrames),
		cancel:        cancel,
	}
	hub := &s.boards.hub
	if !hub.join(id, peer) {
		_ = conn.WriteText(errorFrame("this board already has 32 people in it; try again when someone leaves"))
		return nil
	}
	defer hub.leave(id, peer)

	go conn.Keepalive(ctx)
	go writeBoardFrames(ctx, conn, peer, cancel)
	s.readBoardFrames(conn, id, peer)
	return nil
}

// writeBoardFrames is the peer's one writer. It ends when the socket does, or
// when the hub closes the queue after the last frame it meant to send.
func writeBoardFrames(ctx context.Context, conn *wsx.Conn, p *boardPeer, cancel context.CancelFunc) {
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-p.out:
			if !ok {
				return
			}
			p.pending.Add(-int64(len(frame)))
			if conn.WriteText(frame) != nil {
				return
			}
		}
	}
}

// liveFrame is what a client may send. Fields stay raw so an element array
// is forwarded as it came, not decoded and written again.
type liveFrame struct {
	Type               string          `json:"type"`
	To                 string          `json:"to"`
	Elements           json.RawMessage `json:"elements"`
	Pointer            json.RawMessage `json:"pointer"`
	Button             json.RawMessage `json:"button"`
	SelectedElementIDs json.RawMessage `json:"selectedElementIds"`
	State              string          `json:"state"`
	IDs                json.RawMessage `json:"ids"`
}

func (s *Server) readBoardFrames(conn *wsx.Conn, boardID int64, p *boardPeer) {
	hub := &s.boards.hub
	for {
		kind, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			continue
		}
		var f liveFrame
		if len(msg) > maxBoardSmallFrame {
			// A frame this large is only ever a scene; read just its type.
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &head) != nil || head.Type != "scene" {
				continue
			}
		}
		if json.Unmarshal(msg, &f) != nil {
			continue
		}
		switch f.Type {
		case "scene":
			if !p.CanWrite {
				s.tellBoardPeer(boardID, p, "you can look at this board but not change it")
				continue
			}
			if !isJSONArray(f.Elements) {
				continue
			}
			hub.relay(boardID, p, f.To, marshalFrame(struct {
				Type     string          `json:"type"`
				From     string          `json:"from"`
				Elements json.RawMessage `json:"elements"`
			}{"scene", p.ID, f.Elements}))
		case "pointer":
			if !isJSONObject(f.Pointer) {
				continue
			}
			hub.relay(boardID, p, "", marshalFrame(struct {
				Type               string          `json:"type"`
				From               string          `json:"from"`
				Pointer            json.RawMessage `json:"pointer"`
				Button             json.RawMessage `json:"button,omitempty"`
				SelectedElementIDs json.RawMessage `json:"selectedElementIds,omitempty"`
			}{"pointer", p.ID, f.Pointer, f.Button, f.SelectedElementIDs}))
		case "idle":
			if f.State != "active" && f.State != "idle" && f.State != "away" {
				continue
			}
			hub.relay(boardID, p, "", marshalFrame(map[string]string{"type": "idle", "from": p.ID, "state": f.State}))
		case "files":
			if !p.CanWrite {
				s.tellBoardPeer(boardID, p, "you can look at this board but not change it")
				continue
			}
			if !isJSONArray(f.IDs) {
				continue
			}
			hub.relay(boardID, p, "", marshalFrame(struct {
				Type string          `json:"type"`
				From string          `json:"from"`
				IDs  json.RawMessage `json:"ids"`
			}{"files", p.ID, f.IDs}))
		}
	}
}

func (s *Server) tellBoardPeer(boardID int64, p *boardPeer, message string) {
	hub := &s.boards.hub
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if room := hub.rooms[boardID]; room != nil && room.peers[p.ID] == p {
		p.queue(errorFrame(message))
	}
}

func isJSONObject(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '{'
}
