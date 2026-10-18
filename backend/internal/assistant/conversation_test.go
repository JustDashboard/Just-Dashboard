package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The conversation's own rules, against a provider that answers what the
// test tells it to.

type scripted struct {
	replies  []Reply
	err      error
	requests []Request
}

func (s *scripted) Stream(_ context.Context, req Request, emit func(Event)) (Reply, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return Reply{}, s.err
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	for _, b := range reply.Blocks {
		if b.Type == BlockText {
			emit(Event{Text: b.Text})
		}
	}
	return reply, nil
}

func (s *scripted) Probe(context.Context) error { return nil }
func (s *scripted) Tools() bool                 { return true }

func text(s string) Block { return Block{Type: BlockText, Text: s} }

func call(id, input string) Block {
	return Block{Type: BlockToolUse, ToolID: id, ToolName: "describe_tables", Input: json.RawMessage(input)}
}

func TestAnExchangeAppendsWhatWasAnsweredAndNothingElse(t *testing.T) {
	c := &Conversation{System: "system", Tools: true}
	client := &scripted{replies: []Reply{
		{Blocks: []Block{text("SELECT count(*) FROM orders;")}, Stop: StopEnd, Usage: Usage{Input: 10, Output: 5}},
		{Stop: StopRefusal},
		{Blocks: []Block{text("Reading"), call("toolu_1", `{"tables":["orders"`)}, Stop: StopLength},
	}}
	if err := c.Ask("count the orders"); err != nil {
		t.Fatal(err)
	}
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || !out.Kept || len(c.Messages) != 2 || c.Turns != 1 || out.Usage.Output != 5 {
		t.Fatalf("a plain answer: %+v, %v, %d messages", out, err, len(c.Messages))
	}
	if req := client.requests[0]; req.System != "system" || len(req.Tools) != 1 || req.Tools[0].Name != "describe_tables" {
		t.Errorf("request %+v", req)
	}

	for _, why := range []string{"a refusal", "an answer cut off in the middle of a call"} {
		mark := c.Mark()
		_ = c.Ask("and the next")
		out, err = Exchange(context.Background(), client, c, func(Event) {})
		if err != nil || out.Kept {
			t.Errorf("%s was kept: %+v, %v", why, out, err)
		}
		c.Restore(mark)
		if len(c.Messages) != 2 || c.Turns != 1 {
			t.Errorf("after %s the conversation has %d messages", why, len(c.Messages))
		}
	}
}

func TestAToolCallWaitsAndAQuestionDeclinesIt(t *testing.T) {
	c := &Conversation{Tools: true}
	client := &scripted{replies: []Reply{
		{Blocks: []Block{{Type: BlockRaw, Raw: json.RawMessage(`{"type":"thinking","signature":"s"}`)}, call("toolu_1", `{"tables":["orders","orders"," items "]}`)}, Stop: StopTool},
		{Blocks: []Block{text("Without them: SELECT 1;")}, Stop: StopEnd},
	}}
	_ = c.Ask("count the orders")
	out, err := Exchange(context.Background(), client, c, func(Event) {})
	if err != nil || len(out.Pending) != 1 || strings.Join(out.Pending[0].Tables, ",") != "orders,items" || len(c.Pending) != 1 {
		t.Fatalf("pending %+v, %v", out.Pending, err)
	}
	// Asking something else is not allowing it: the call is answered as
	// declined in the same message as the new question.
	_ = c.Ask("never mind, just say hello")
	last := c.Messages[len(c.Messages)-1]
	if len(last.Blocks) != 2 || last.Blocks[0].Type != BlockToolResult || !last.Blocks[0].IsError ||
		last.Blocks[0].ToolID != "toolu_1" || last.Blocks[0].Text != Declined || last.Blocks[1].Text != "never mind, just say hello" {
		t.Errorf("the declining message %+v", last.Blocks)
	}
	if len(c.Pending) != 0 {
		t.Error("the call is still waiting")
	}
	if _, err := Exchange(context.Background(), client, c, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	// The thinking block the call was made with went back unchanged.
	if string(client.requests[1].Messages[1].Blocks[0].Raw) != `{"type":"thinking","signature":"s"}` {
		t.Error("the thinking block did not go back as it came")
	}
}

func TestAnAnswerAskingForSomethingElseRunsNothing(t *testing.T) {
	for _, input := range []Block{
		{Type: BlockToolUse, ToolID: "toolu_1", ToolName: "run_query", Input: json.RawMessage(`{"sql":"DROP TABLE orders"}`)},
		call("toolu_2", `{}`),
		call("toolu_3", `{"tables":[]}`),
		call("toolu_4", `{"tables":["a","b","c","d","e","f","g","h","i","j","k"]}`),
		call("toolu_5", `{"tables":["orders"],"sql":"DELETE FROM orders"}`),
		call("toolu_6", `{"tables":["a\nb"]}`),
		call("", `{"tables":["orders"]}`),
	} {
		c := &Conversation{Tools: true}
		_ = c.Ask("q")
		_, err := Exchange(context.Background(), &scripted{replies: []Reply{{Blocks: []Block{input}, Stop: StopTool}}}, c, func(Event) {})
		if !errors.Is(err, ErrMalformedCall) || len(c.Pending) != 0 {
			t.Errorf("%s %s was taken as a call: %v", input.ToolName, input.Input, err)
		}
	}
}

func TestAProviderFailureLeavesTheConversationAsItWas(t *testing.T) {
	c := &Conversation{}
	mark := c.Mark()
	_ = c.Ask("q")
	_, err := Exchange(context.Background(), &scripted{err: &ProviderError{Message: "down"}}, c, func(Event) {})
	if err == nil {
		t.Fatal("no error")
	}
	c.Restore(mark)
	if len(c.Messages) != 0 || c.Turns != 0 {
		t.Errorf("%d messages left after a failure", len(c.Messages))
	}
}

func TestAConversationLongEnoughIsEnded(t *testing.T) {
	c := &Conversation{Turns: maxTurns}
	if err := c.Ask("q"); !errors.Is(err, ErrFull) {
		t.Errorf("turn past the bound: %v", err)
	}
	c = &Conversation{System: strings.Repeat("x", maxHistory+1)}
	if err := c.Ask("q"); !errors.Is(err, ErrFull) {
		t.Errorf("history past the bound: %v", err)
	}
}

func TestTheStoreKeepsConversationsToTheirOwnerAndConnection(t *testing.T) {
	s := NewStore()
	now := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return now }

	c := s.Begin("ann", 1)
	if _, err := s.Take(c.ID, "ann", 1); !errors.Is(err, ErrGone) {
		t.Error("a conversation with no kept turn was found")
	}
	s.Release(c)
	if _, err := s.Take(c.ID, "ann", 1); !errors.Is(err, ErrGone) {
		t.Error("an empty conversation was kept")
	}

	c = s.Begin("ann", 1)
	c.Messages = []Message{{Role: "user", Blocks: []Block{text("q")}}}
	s.Release(c)
	for _, other := range []struct {
		owner string
		conn  int64
	}{{"bob", 1}, {"ann", 2}} {
		if _, err := s.Take(c.ID, other.owner, other.conn); !errors.Is(err, ErrGone) {
			t.Errorf("%s on connection %d reached ann's conversation", other.owner, other.conn)
		}
	}
	held, err := s.Take(c.ID, "ann", 1)
	if err != nil || held != c {
		t.Fatalf("take: %v", err)
	}
	if _, err := s.Take(c.ID, "ann", 1); !errors.Is(err, ErrBusy) {
		t.Error("a conversation was answering two questions at once")
	}
	s.Release(c)

	now = now.Add(idleConversation + time.Second)
	if _, err := s.Take(c.ID, "ann", 1); !errors.Is(err, ErrGone) {
		t.Error("an idle conversation outlived its time")
	}

	held = s.Begin("ann", 1)
	held.Messages = c.Messages
	s.Release(held)
	held, _ = s.Take(held.ID, "ann", 1)
	s.End(held)
	if _, err := s.Take(held.ID, "ann", 1); !errors.Is(err, ErrGone) {
		t.Error("an ended conversation carried on")
	}
}

func TestTheStoreIsBounded(t *testing.T) {
	s := NewStore()
	now := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return now }
	var first string
	for i := range maxConversations + 5 {
		now = now.Add(time.Second)
		c := s.Begin("ann", 1)
		c.Messages = []Message{{Role: "user", Blocks: []Block{text("q")}}}
		s.Release(c)
		if i == 0 {
			first = c.ID
		}
	}
	if len(s.items) != maxConversations {
		t.Errorf("%d conversations kept", len(s.items))
	}
	if _, err := s.Take(first, "ann", 1); !errors.Is(err, ErrGone) {
		t.Error("the oldest conversation was not the one let go")
	}
}
