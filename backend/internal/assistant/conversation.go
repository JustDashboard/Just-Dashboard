package assistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// A conversation is held here, in memory, rather than sent back and forth
// with the page. What the provider is sent each turn has to be exactly what
// it was sent the turn before with the new turn appended — the prompt cache
// and, on the newest Claude models, the reasoning a tool call was made with
// both depend on it — and a transcript the page kept and returned would be a
// transcript the page could change. A restart ends every conversation; the
// page then starts a new one.

const (
	maxConversations = 200
	idleConversation = 30 * time.Minute
	// A conversation past either is asked to start again rather than sent
	// whole every turn at a growing cost.
	maxTurns   = 40
	maxHistory = 600_000
)

var (
	// ErrGone is a conversation that is not here: never begun, ended by a
	// restart or by sitting idle, or somebody else's.
	ErrGone = errors.New("this conversation has ended")
	// ErrBusy is a conversation already answering a question.
	ErrBusy = errors.New("this conversation is still answering")
	// ErrFull is a conversation long enough that a new one is cheaper.
	ErrFull = errors.New("this conversation is long enough; start a new one")
)

// Conversation is one person's questions about one connection, in order.
type Conversation struct {
	ID         string
	Owner      string
	Connection int64
	// Schema, System, Tools and Known are fixed when it begins.
	Schema   string
	Revision string
	System   string
	Tools    bool
	Known    []TableName
	Context  ContextStats
	Messages []Message
	// Pending is the tool calls the last answer ended on, waiting for the
	// person to allow or decline them.
	Pending []ToolCall
	Turns   int

	busy    bool
	touched time.Time
}

// Mark is where a conversation stood before a turn, to go back to when the
// turn does not finish: a turn the person stopped, or one the provider
// failed, leaves nothing behind for the next question to be sent with.
type Mark struct {
	messages int
	pending  []ToolCall
	turns    int
}

func (c *Conversation) Mark() Mark {
	return Mark{messages: len(c.Messages), pending: c.Pending, turns: c.Turns}
}

func (c *Conversation) Restore(m Mark) {
	c.Messages, c.Pending, c.Turns = c.Messages[:m.messages], m.pending, m.turns
}

func (c *Conversation) size() int {
	n := len(c.System)
	for _, m := range c.Messages {
		for _, b := range m.Blocks {
			n += len(b.Text) + len(b.Input) + len(b.Raw)
		}
	}
	return n
}

// Ask appends a question. Calls still waiting are declined in the same
// message: asking something else is not allowing them.
func (c *Conversation) Ask(text string) error {
	if c.Turns >= maxTurns || c.size() > maxHistory {
		return ErrFull
	}
	blocks := ToolResults(c.Pending, func(ToolCall) (string, bool) { return Declined, false })
	blocks = append(blocks, Block{Type: BlockText, Text: text})
	c.Messages = append(c.Messages, Message{Role: "user", Blocks: blocks})
	c.Pending = nil
	return nil
}

// Answer appends the results of the calls that were waiting.
func (c *Conversation) Answer(results []Block) {
	c.Messages = append(c.Messages, Message{Role: "user", Blocks: results})
	c.Pending = nil
}

// Outcome is how one exchange ended.
type Outcome struct {
	Stop    Stop
	Usage   Usage
	Model   string
	Pending []ToolCall
	// Kept says the answer is now part of the conversation. One that is not
	// — a refusal, an empty answer, an answer cut off in the middle of a
	// call — is a turn the caller goes back to its mark from, so the next
	// question is not sent after a turn that did not happen.
	Kept bool
}

// Exchange sends the conversation as it stands and appends the answer.
func Exchange(ctx context.Context, client Client, c *Conversation, emit func(Event)) (Outcome, error) {
	req := Request{System: c.System, Messages: c.Messages}
	if c.Tools {
		req.Tools = []Tool{DescribeTables}
	}
	reply, err := client.Stream(ctx, req, emit)
	if err != nil {
		return Outcome{Usage: reply.Usage, Model: reply.Model}, err
	}
	out := Outcome{Stop: reply.Stop, Usage: reply.Usage, Model: reply.Model}
	calling := false
	for _, b := range reply.Blocks {
		calling = calling || b.Type == BlockToolUse
	}
	if reply.Stop == StopRefusal || len(reply.Blocks) == 0 || (calling && reply.Stop != StopTool) {
		return out, nil
	}
	if reply.Stop == StopTool {
		calls, err := Calls(reply.Blocks)
		if err != nil {
			return out, err
		}
		out.Pending = calls
	}
	c.Messages = append(c.Messages, Message{Role: "assistant", Blocks: reply.Blocks})
	c.Pending = out.Pending
	c.Turns++
	out.Kept = true
	return out, nil
}

// Store holds the conversations, bounded in number and in how long one is
// kept unused.
type Store struct {
	mu    sync.Mutex
	items map[string]*Conversation
	now   func() time.Time
}

func NewStore() *Store {
	return &Store{items: map[string]*Conversation{}, now: time.Now}
}

// Begin starts a conversation, held by the caller. It is kept only once a
// turn of it has been kept.
func (s *Store) Begin(owner string, connection int64) *Conversation {
	return &Conversation{ID: newID(), Owner: owner, Connection: connection, busy: true}
}

// Take hands the caller a conversation of its own to carry on.
func (s *Store) Take(id, owner string, connection int64) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	c := s.items[id]
	if c == nil || c.Owner != owner || c.Connection != connection {
		return nil, ErrGone
	}
	if c.busy {
		return nil, ErrBusy
	}
	c.busy = true
	return c, nil
}

// Release hands a conversation back. One with nothing in it is forgotten.
func (s *Store) Release(c *Conversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.busy, c.touched = false, s.now()
	if len(c.Messages) == 0 {
		delete(s.items, c.ID)
		return
	}
	if _, kept := s.items[c.ID]; kept {
		return
	}
	s.sweep()
	if len(s.items) >= maxConversations {
		s.evictOldest()
	}
	s.items[c.ID] = c
}

// End forgets a conversation that may not be carried on.
func (s *Store) End(c *Conversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, c.ID)
}

func (s *Store) sweep() {
	cutoff := s.now().Add(-idleConversation)
	for id, c := range s.items {
		if !c.busy && c.touched.Before(cutoff) {
			delete(s.items, id)
		}
	}
}

func (s *Store) evictOldest() {
	idle := make([]*Conversation, 0, len(s.items))
	for _, c := range s.items {
		if !c.busy {
			idle = append(idle, c)
		}
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].touched.Before(idle[j].touched) })
	if len(idle) > 0 {
		delete(s.items, idle[0].ID)
	}
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
