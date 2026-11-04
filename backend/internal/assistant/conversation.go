package assistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
// transcript the page could change. The SQL editor's end with the process. A
// deployment's or the server's is a saved chat as well: the API writes it to
// the database after every step (State) and loads one that is not here back
// (TakeChat), so it outlives a restart and can be carried on from anywhere.

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
	// ErrTakenOver is why a press stops when the chat is carried on somewhere
	// else: it is the cause its context is cancelled with.
	ErrTakenOver = errors.New("this chat was continued somewhere else")
)

// takeOverWait bounds how long a press that takes a chat over waits for the
// press it stopped to put the chat down.
const takeOverWait = 30 * time.Second

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
	// Answered is the results of calls that answer ended on and that needed
	// nobody's leave — a proposal, which runs nothing — held until the
	// person answers the rest, because every call of an answer is answered
	// in the one message that follows it.
	Answered []Block
	Turns    int
	// Offer and Check are the tools a conversation other than the SQL
	// editor's offers and the check its calls are read through. Empty is the
	// editor's: describe_tables, when Tools says the provider takes tools.
	Offer []Tool
	Check func([]Block) ([]ToolCall, error)
	// TurnLimit, when set, stands in for maxTurns: a conversation that takes
	// many steps to a question counts each as a turn.
	TurnLimit int
	// OwnerID is the person a saved chat belongs to, by user id; Chat is what
	// it says about itself. Both are zero for the SQL editor's.
	OwnerID int64
	Chat    *ChatInfo

	busy    bool
	touched time.Time
	// cancel stops the press holding the conversation, and released is closed
	// when that press puts it down, for a press that takes it over.
	cancel   context.CancelCauseFunc
	released chan struct{}
	// retitle is a new title given while a press held the chat, for the next.
	retitle string
}

// ChatInfo is what a saved chat says about itself besides what the model is
// sent: what it is about, its title, and what it has done.
type ChatInfo struct {
	// Kind is "deployment", "draft", "server" or "database".
	Kind        string `json:"kind"`
	Deployment  int64  `json:"deployment,omitempty"`
	Environment int64  `json:"environment,omitempty"`
	Draft       string `json:"draft,omitempty"`
	Database    int64  `json:"database,omitempty"`
	// About is the page a chat about the server was opened from.
	About    *ChatAbout `json:"about,omitempty"`
	Name     string     `json:"name"`
	Title    string     `json:"title"`
	Provider string     `json:"provider"`
	Model    string     `json:"model"`
	Created  time.Time  `json:"created"`
	Updated  time.Time  `json:"updated"`
	Steps    int        `json:"steps"`
	Changes  int        `json:"changes"`
}

// Mark is where a conversation stood before a turn, to go back to when the
// turn does not finish: a turn the person stopped, or one the provider
// failed, leaves nothing behind for the next question to be sent with.
type Mark struct {
	messages int
	pending  []ToolCall
	answered []Block
	turns    int
}

func (c *Conversation) Mark() Mark {
	return Mark{messages: len(c.Messages), pending: c.Pending, answered: c.Answered, turns: c.Turns}
}

func (c *Conversation) Restore(m Mark) {
	c.Messages, c.Pending, c.Answered, c.Turns = c.Messages[:m.messages], m.pending, m.answered, m.turns
}

func (c *Conversation) size() int {
	n := len(c.System)
	for _, m := range c.Messages {
		for _, b := range m.Blocks {
			n += len(b.Text) + len(b.Input) + len(b.Raw)
			if b.Image != nil {
				n += imageWeight
			}
		}
	}
	return n
}

// Long reports a conversation past the size worth sending whole, which a press of
// many steps can reach between questions, once the oldest results that can be
// left out have been (Shrink).
func (c *Conversation) Long() bool {
	if c.size() <= maxHistory {
		return false
	}
	c.Shrink()
	return c.size() > maxHistory
}

// LeftOut is what an older tool result reads as once a long chat is shrunk.
const LeftOut = "(an older result, left out to keep the chat short)"

// keepWhole is how many of the newest messages Shrink leaves as they are: what
// the model is working on now.
const keepWhole = 6

// Shrink leaves out the text of a saved chat's oldest tool results, oldest
// first, until it is well under the size worth sending whole, so a chat carried
// on for days goes on rather than ending. The calls stay, so the model still
// knows what it did; only what they read is gone. The SQL editor's is not
// shrunk: its turn limit ends it first.
func (c *Conversation) Shrink() {
	if c.TurnLimit == 0 {
		return
	}
	target := maxHistory * 3 / 4
	size := c.size()
	for i := 0; i < len(c.Messages)-keepWhole && size > target; i++ {
		for j := range c.Messages[i].Blocks {
			b := &c.Messages[i].Blocks[j]
			if b.Type != BlockToolResult || len(b.Text) <= len(LeftOut) {
				continue
			}
			size -= len(b.Text) - len(LeftOut)
			b.Text = LeftOut
		}
	}
}

// Ask appends a question, with any images the person pasted before its words,
// where a model reads them best. Calls still waiting are declined in the same
// message: asking something else is not allowing them.
func (c *Conversation) Ask(text string, images ...Image) error {
	limit := maxTurns
	if c.TurnLimit > 0 {
		limit = c.TurnLimit
	}
	if c.Turns >= limit || c.Long() {
		return ErrFull
	}
	blocks := append([]Block(nil), c.Answered...)
	blocks = append(blocks, ToolResults(c.Pending, func(ToolCall) (string, bool) { return Declined, false })...)
	for i := range images {
		blocks = append(blocks, Block{Type: BlockImage, Image: &images[i]})
	}
	// A provider refuses an empty text block, and an image may be the whole question.
	if text != "" || len(images) == 0 {
		blocks = append(blocks, Block{Type: BlockText, Text: text})
	}
	c.Pending, c.Answered = nil, nil
	defer c.leaveOutOldImages()
	// A question nothing answered yet — a chat taken over before its answer —
	// is asked again with this one, in one message, as providers take them.
	if last := len(c.Messages) - 1; last >= 0 && c.Messages[last].Role == "user" {
		merged := append(append([]Block(nil), c.Messages[last].Blocks...), blocks...)
		c.Messages[last] = Message{Role: "user", Blocks: merged}
		return nil
	}
	c.Messages = append(c.Messages, Message{Role: "user", Blocks: blocks})
	return nil
}

// Answer appends the results of the calls that were waiting, after those
// answered without anybody's leave.
func (c *Conversation) Answer(results []Block) {
	blocks := append(append([]Block(nil), c.Answered...), results...)
	c.Messages = append(c.Messages, Message{Role: "user", Blocks: blocks})
	c.Pending, c.Answered = nil, nil
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
	switch {
	case len(c.Offer) > 0 && c.Tools:
		req.Tools = c.Offer
	case c.Tools:
		req.Tools = []Tool{DescribeTables}
	}
	check := Calls
	if c.Check != nil {
		check = c.Check
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
		calls, err := check(reply.Blocks)
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
// kept unused. A saved chat that leaves it is still in the database.
type Store struct {
	mu    sync.Mutex
	items map[string]*Conversation
	now   func() time.Time
}

func NewStore() *Store {
	return &Store{items: map[string]*Conversation{}, now: time.Now}
}

// Begin starts a conversation, held by the caller. It is in the store from
// here, busy, so the chat it becomes can be found and taken over while its
// first press runs; one that keeps nothing is forgotten when it is released.
func (s *Store) Begin(owner string, connection int64) *Conversation {
	return s.BeginChat(owner, 0, connection, nil)
}

// BeginChat starts a conversation that is a saved chat of its owner's, by user
// id, held by the caller as Begin's is.
func (s *Store) BeginChat(owner string, ownerID, connection int64, chat *ChatInfo) *Conversation {
	c := &Conversation{ID: newID(), Owner: owner, OwnerID: ownerID, Connection: connection, Chat: chat, busy: true, released: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if len(s.items) >= maxConversations {
		s.evictOldest()
	}
	s.items[c.ID] = c
	return c
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
	c.busy, c.released = true, make(chan struct{})
	return c, nil
}

// TakeChat hands the caller a saved chat of its owner's to carry on: the one
// held here, or the one load reads back from the database. A chat a press is
// running is ErrBusy, unless takeOver says to stop that press, which this then
// waits for it to put the chat down, as it stood, and carries on from there.
func (s *Store) TakeChat(ctx context.Context, id string, ownerID, connection int64, takeOver bool,
	load func(context.Context, string) (*Conversation, error)) (*Conversation, error) {
	s.mu.Lock()
	s.sweep()
	c := s.items[id]
	if c == nil {
		s.mu.Unlock()
		loaded, err := load(ctx, id)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		// Another press may have loaded it meanwhile; there is one of it.
		if c = s.items[id]; c == nil {
			if len(s.items) >= maxConversations {
				s.evictOldest()
			}
			c = loaded
			c.touched = s.now()
			s.items[id] = c
		}
	}
	if c.OwnerID != ownerID || c.OwnerID == 0 || c.Connection != connection {
		s.mu.Unlock()
		return nil, ErrGone
	}
	if c.busy {
		if !takeOver {
			s.mu.Unlock()
			return nil, ErrBusy
		}
		cancel, released := c.cancel, c.released
		s.mu.Unlock()
		if cancel != nil {
			cancel(ErrTakenOver)
		}
		wait := time.NewTimer(takeOverWait)
		defer wait.Stop()
		select {
		case <-released:
		case <-wait.C:
			return nil, ErrBusy
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		if c.busy || s.items[id] != c {
			s.mu.Unlock()
			return nil, ErrBusy
		}
	}
	c.busy, c.released, c.cancel = true, make(chan struct{}), nil
	if c.retitle != "" && c.Chat != nil {
		c.Chat.Title, c.retitle = c.retitle, ""
	}
	s.mu.Unlock()
	return c, nil
}

// Retitle renames a chat held here. One a press holds takes the name when it
// is next taken, since the press reads its title as it goes.
func (s *Store) Retitle(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.items[id]
	switch {
	case c == nil || c.Chat == nil:
	case c.busy:
		c.retitle = title
	default:
		c.Chat.Title = title
	}
}

// Running records how to stop the press holding a conversation, for a press
// that takes it over.
func (s *Store) Running(c *Conversation, cancel context.CancelCauseFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.cancel = cancel
}

// Busy reports whether a press is running the conversation of this id now.
func (s *Store) Busy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.items[id]
	return c != nil && c.busy
}

// Release hands a conversation back. One with nothing in it is forgotten.
func (s *Store) Release(c *Conversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.busy, c.touched, c.cancel = false, s.now(), nil
	if c.released != nil {
		close(c.released)
		c.released = nil
	}
	if len(c.Messages) == 0 && c.Chat == nil {
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

// Forget drops a conversation that is not running, by id, as when its saved
// chat is deleted. One a press is running is ErrBusy.
func (s *Store) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.items[id]; c != nil && c.busy {
		return ErrBusy
	}
	delete(s.items, id)
	return nil
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

// conversationState is a saved chat's conversation as the database keeps it:
// exactly what the provider is sent, so it is carried on as it was. A block's
// input and a provider's own block are kept as their text, because an input a
// model wrote need not be JSON.
type conversationState struct {
	System     string        `json:"system"`
	Revision   string        `json:"revision"`
	Tools      bool          `json:"tools"`
	Connection int64         `json:"connection"`
	Messages   []savedRecord `json:"messages"`
	Pending    []ToolCall    `json:"pending,omitempty"`
	Answered   []savedBlock  `json:"answered,omitempty"`
	Turns      int           `json:"turns"`
}

type savedRecord struct {
	Role   string       `json:"role"`
	Blocks []savedBlock `json:"blocks"`
}

type savedBlock struct {
	Type     BlockType `json:"type"`
	Text     string    `json:"text,omitempty"`
	ToolID   string    `json:"toolId,omitempty"`
	ToolName string    `json:"toolName,omitempty"`
	Input    string    `json:"input,omitempty"`
	IsError  bool      `json:"isError,omitempty"`
	Raw      string    `json:"raw,omitempty"`
	Image    *Image    `json:"image,omitempty"`
}

func saveBlocks(blocks []Block) []savedBlock {
	out := make([]savedBlock, len(blocks))
	for i, b := range blocks {
		out[i] = savedBlock{Type: b.Type, Text: b.Text, ToolID: b.ToolID, ToolName: b.ToolName, Input: string(b.Input), IsError: b.IsError, Raw: string(b.Raw), Image: b.Image}
	}
	return out
}

func loadBlocks(saved []savedBlock) []Block {
	out := make([]Block, len(saved))
	for i, b := range saved {
		out[i] = Block{Type: b.Type, Text: b.Text, ToolID: b.ToolID, ToolName: b.ToolName, IsError: b.IsError, Image: b.Image}
		if b.Input != "" {
			out[i].Input = json.RawMessage(b.Input)
		}
		if b.Raw != "" {
			out[i].Raw = json.RawMessage(b.Raw)
		}
	}
	return out
}

// State is the conversation as it is saved.
func (c *Conversation) State() ([]byte, error) {
	state := conversationState{
		System: c.System, Revision: c.Revision, Tools: c.Tools, Connection: c.Connection,
		Pending: c.Pending, Answered: saveBlocks(c.Answered), Turns: c.Turns,
		Messages: make([]savedRecord, len(c.Messages)),
	}
	for i, m := range c.Messages {
		state.Messages[i] = savedRecord{Role: m.Role, Blocks: saveBlocks(m.Blocks)}
	}
	return json.Marshal(state)
}

// LoadConversation reads a saved conversation back. What it offers and how its
// calls are checked are the caller's to set, from what the chat is about.
func LoadConversation(id string, raw []byte) (*Conversation, error) {
	var state conversationState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	c := &Conversation{
		ID: id, System: state.System, Revision: state.Revision, Tools: state.Tools, Connection: state.Connection,
		Pending: state.Pending, Answered: loadBlocks(state.Answered), Turns: state.Turns,
		Messages: make([]Message, len(state.Messages)),
	}
	for i, m := range state.Messages {
		c.Messages[i] = Message{Role: m.Role, Blocks: loadBlocks(m.Blocks)}
	}
	return c, nil
}

// ForgetReasoning drops what a provider gave back to be sent back as it came —
// a thinking block, bound by its signature to the model that wrote it — for a
// chat carried on by another model, which would refuse it.
func (c *Conversation) ForgetReasoning() {
	for i := range c.Messages {
		kept := c.Messages[i].Blocks[:0]
		for _, b := range c.Messages[i].Blocks {
			if b.Type != BlockRaw {
				kept = append(kept, b)
			}
		}
		c.Messages[i].Blocks = kept
	}
}
