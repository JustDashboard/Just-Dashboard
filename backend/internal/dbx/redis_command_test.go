package dbx

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRedisParseCommand(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"GET foo", []string{"GET", "foo"}},
		{"  SET   k    v  ", []string{"SET", "k", "v"}},
		{"SET k \"two words\"", []string{"SET", "k", "two words"}},
		{`SET k "line\nbreak\ttab"`, []string{"SET", "k", "line\nbreak\ttab"}},
		{`SET k "quote \" and slash \\"`, []string{"SET", "k", `quote " and slash \`}},
		// \xHH is how a byte that is not text is typed, which is the only way
		// to write a binary value in a text box.
		{`SET k "\xff\x00\xfe"`, []string{"SET", "k", "\xff\x00\xfe"}},
		{`SET k 'single "quoted" \n stays'`, []string{"SET", "k", `single "quoted" \n stays`}},
		{`SET k 'it\'s'`, []string{"SET", "k", "it's"}},
		// An empty string is an argument; without the quotes it would be none.
		{`SREM tags ""`, []string{"SREM", "tags", ""}},
		{"HSET h f\tv\r\n", []string{"HSET", "h", "f", "v"}},
		// A quote in the middle of a word opens a quoted run, as it does in
		// redis-cli.
		{`ECHO foo"bar baz"`, []string{"ECHO", "foobar baz"}},
	}
	for _, c := range cases {
		got, err := RedisParseCommand(c.line)
		if err != nil {
			t.Errorf("RedisParseCommand(%q): %v", c.line, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("RedisParseCommand(%q) = %q, want %q", c.line, got, c.want)
		}
	}

	// These are refused rather than guessed at: running the wrong command is
	// worse than running none.
	for _, line := range []string{
		"", "   ", `SET k "unterminated`, `SET k 'unterminated`,
		`SET k "closed"tail`, `SET k 'closed'tail`,
	} {
		if got, err := RedisParseCommand(line); err == nil {
			t.Errorf("RedisParseCommand(%q) = %q, want an error", line, got)
		}
	}
	if _, err := RedisParseCommand(strings.Repeat("x ", redisMaxCommandArgs+2)); err == nil {
		t.Error("a command with more arguments than the limit was accepted")
	}
}

func TestRedisClassify(t *testing.T) {
	past := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	cases := []struct {
		line  string
		class string
		admin bool
	}{
		{"GET k", RedisClassRead, false},
		{"hgetall h", RedisClassRead, false},
		{"SCAN 0 MATCH x*", RedisClassRead, false},
		{"INFO", RedisClassRead, false},
		{"XINFO STREAM s", RedisClassRead, false},
		{"OBJECT ENCODING k", RedisClassRead, false},

		{"SET k v", RedisClassWrite, false},
		{"HSET h f v", RedisClassWrite, false},
		{"LPUSH l v", RedisClassWrite, false},
		{"EXPIRE k 60", RedisClassWrite, false},
		{"RENAMENX a b", RedisClassWrite, false},
		{"BGSAVE", RedisClassWrite, false},
		{"XADD s * f v", RedisClassWrite, false},
		{"XGROUP CREATE s g $", RedisClassWrite, false},

		// Whatever removes data needs what the key browser's delete button
		// needs. The console must not be the cheaper way.
		{"DEL k", RedisClassDangerous, false},
		{"UNLINK a b c", RedisClassDangerous, false},
		{"HDEL h f", RedisClassDangerous, false},
		{"SREM s m", RedisClassDangerous, false},
		{"LPOP l", RedisClassDangerous, false},
		{"LTRIM l 0 9", RedisClassDangerous, false},
		{"XDEL s 1-1", RedisClassDangerous, false},
		{"XTRIM s MAXLEN 10", RedisClassDangerous, false},
		{"XGROUP DESTROY s g", RedisClassDangerous, false},
		{"RENAME a b", RedisClassDangerous, false},
		{"FLUSHDB", RedisClassDangerous, false},
		{"flushall async", RedisClassDangerous, false},
		{"SWAPDB 0 1", RedisClassDangerous, false},
		{"SAVE", RedisClassDangerous, false},
		{"SCRIPT FLUSH", RedisClassDangerous, false},
		{"CLIENT KILL ID 12", RedisClassDangerous, false},
		{"SLOWLOG RESET", RedisClassDangerous, false},

		// Code that runs on the server can do anything a client can, so no
		// form of it is a read — not even the ones named _RO.
		{"EVAL \"return 1\" 0", RedisClassDangerous, false},
		{"EVALSHA abc 0", RedisClassDangerous, false},
		{"EVAL_RO \"return 1\" 0", RedisClassDangerous, false},
		{"FCALL f 0", RedisClassDangerous, false},
		{"FCALL_RO f 0", RedisClassDangerous, false},
		{"FUNCTION LOAD x", RedisClassDangerous, false},

		// The server's own configuration and accounts: what the configuration
		// page and the users page keep to administrators.
		{"CONFIG SET maxmemory 1gb", RedisClassDangerous, true},
		{"config rewrite", RedisClassDangerous, true},
		{"CONFIG GET requirepass", RedisClassRead, true},
		{"ACL SETUSER app on", RedisClassDangerous, true},
		{"ACL DELUSER app", RedisClassDangerous, true},
		{"ACL LIST", RedisClassRead, true},
		{"DEBUG OBJECT k", RedisClassDangerous, true},
		{"DEBUG RELOAD", RedisClassDangerous, true},
		{"CLUSTER RESET HARD", RedisClassDangerous, true},
		{"CLUSTER INFO", RedisClassRead, false},
		{"REPLICAOF NO ONE", RedisClassDangerous, true},
		{"MODULE LOAD /tmp/x.so", RedisClassDangerous, true},

		// An arguments-dependent verdict only ever goes up.
		{"EXPIRE k 0", RedisClassDangerous, false},
		{"EXPIRE k -1", RedisClassDangerous, false},
		{"PEXPIRE k 0", RedisClassDangerous, false},
		{"EXPIREAT k " + past, RedisClassDangerous, false},
		{"EXPIREAT k " + future, RedisClassWrite, false},
		{"COPY a b", RedisClassWrite, false},
		{"COPY a b REPLACE", RedisClassDangerous, false},
		{"COPY a b DB 1 replace", RedisClassDangerous, false},
		{"RESTORE k 0 payload REPLACE", RedisClassDangerous, false},
		{"XADD s MAXLEN ~ 1000 * f v", RedisClassDangerous, false},
		{"XADD s NOMKSTREAM MINID 5 * f v", RedisClassDangerous, false},
		// A field that happens to be called maxlen is not the option.
		{"XADD s * maxlen 5", RedisClassWrite, false},

		// Nothing here can be a request and a reply.
		{"SUBSCRIBE ch", RedisClassBlocked, false},
		{"PSUBSCRIBE *", RedisClassBlocked, false},
		{"MONITOR", RedisClassBlocked, false},
		{"SYNC", RedisClassBlocked, false},
		{"PSYNC ? -1", RedisClassBlocked, false},
		{"SHUTDOWN NOSAVE", RedisClassBlocked, false},
		{"MIGRATE host 6379 k 0 1000", RedisClassBlocked, false},
		{"CLIENT PAUSE 1000", RedisClassBlocked, false},
		{"MULTI", RedisClassBlocked, false},
		{"EXEC", RedisClassBlocked, false},
		{"WATCH k", RedisClassBlocked, false},
		{"SELECT 3", RedisClassBlocked, false},
		{"AUTH secret", RedisClassBlocked, false},
		{"HELLO 3", RedisClassBlocked, false},
		{"DEBUG SEGFAULT", RedisClassBlocked, false},
		{"DEBUG SLEEP 100", RedisClassBlocked, false},

		// A pop that waits is a pop when the wait is short, and a connection
		// that never comes back when it is not.
		{"BLPOP l 0", RedisClassBlocked, false},
		{"BLPOP l 60", RedisClassBlocked, false},
		{"BLPOP l 2", RedisClassDangerous, false},
		{"BRPOP a b 0.5", RedisClassDangerous, false},
		{"BLPOP l", RedisClassBlocked, false},
		{"BLMOVE a b LEFT RIGHT 2", RedisClassWrite, false},
		{"BLMOVE a b LEFT RIGHT 0", RedisClassBlocked, false},
		{"BLMPOP 0 1 l LEFT", RedisClassBlocked, false},
		{"BLMPOP 1 1 l LEFT", RedisClassDangerous, false},
		{"BZPOPMIN z 0", RedisClassBlocked, false},
		{"XREAD COUNT 5 STREAMS s 0", RedisClassRead, false},
		{"XREAD BLOCK 0 STREAMS s $", RedisClassBlocked, false},
		{"XREAD BLOCK 60000 STREAMS s $", RedisClassBlocked, false},
		{"XREAD BLOCK 2000 STREAMS s $", RedisClassRead, false},
		{"XREADGROUP GROUP g c BLOCK 0 STREAMS s >", RedisClassBlocked, false},
		{"XREADGROUP GROUP g c STREAMS s >", RedisClassWrite, false},
		{"WAIT 1 0", RedisClassBlocked, false},
		{"WAIT 1 100", RedisClassWrite, false},

		// A subcommand nobody listed takes its container's answer, which for
		// anything that can change the server is the worst one.
		{"CONFIG FROBNICATE", RedisClassDangerous, true},
		{"CLIENT NEWTHING", RedisClassDangerous, false},
		{"SCRIPT NEWTHING", RedisClassDangerous, false},
		{"ACL NEWTHING", RedisClassDangerous, true},
		{"XINFO NEWTHING s", RedisClassRead, false},
	}
	for _, c := range cases {
		args, err := RedisParseCommand(c.line)
		if err != nil {
			t.Fatalf("parse %q: %v", c.line, err)
		}
		got := RedisClassify(args, nil)
		if got.Class != c.class || got.Admin != c.admin {
			t.Errorf("RedisClassify(%q) = %s admin=%v, want %s admin=%v (%v)",
				c.line, got.Class, got.Admin, c.class, c.admin, got.Reasons)
		}
		if !got.Known {
			t.Errorf("RedisClassify(%q) is not in the table", c.line)
		}
		if got.Class != RedisClassRead && got.Class != RedisClassWrite && len(got.Reasons) == 0 {
			t.Errorf("RedisClassify(%q) is %s and gives no reason", c.line, got.Class)
		}
	}
}

// A command the table does not hold is the case the classifier exists for:
// it has to be placed without knowing what it does.
func TestRedisClassifyFailsClosed(t *testing.T) {
	// The server does not know it either.
	got := RedisClassify([]string{"FROBNICATE", "k"}, nil)
	if got.Class != RedisClassDangerous || got.Known {
		t.Errorf("an unrecognised command classified %s known=%v, want dangerous", got.Class, got.Known)
	}
	cases := []struct {
		name  string
		flags RedisCommandFlags
		want  string
	}{
		{"a plain read", RedisCommandFlags{Flags: []string{"readonly", "fast"}, Categories: []string{"@read"}}, RedisClassRead},
		{"a write", RedisCommandFlags{Flags: []string{"write", "denyoom"}, Categories: []string{"@write"}}, RedisClassDangerous},
		{"no flags at all", RedisCommandFlags{}, RedisClassDangerous},
		{"a read the server calls administrative", RedisCommandFlags{Flags: []string{"readonly", "admin"}}, RedisClassDangerous},
		{"a read in the dangerous category", RedisCommandFlags{Flags: []string{"readonly"}, Categories: []string{"@dangerous"}}, RedisClassDangerous},
		{"a read that also writes", RedisCommandFlags{Flags: []string{"readonly", "write"}}, RedisClassDangerous},
		{"something that blocks", RedisCommandFlags{Flags: []string{"readonly", "blocking"}}, RedisClassBlocked},
		{"a subscription", RedisCommandFlags{Flags: []string{"pubsub"}}, RedisClassBlocked},
		// Dragonfly writes its categories in upper case.
		{"upper-case categories", RedisCommandFlags{Flags: []string{"readonly"}, Categories: []string{"@ADMIN"}}, RedisClassDangerous},
	}
	for _, c := range cases {
		flags := c.flags
		if got := RedisClassify([]string{"MODULE.NEWCOMMAND", "k"}, &flags); got.Class != c.want {
			t.Errorf("%s: classified %s, want %s", c.name, got.Class, c.want)
		}
	}
	// For a command the table does hold, the server's flags can only make it
	// stricter: a read this build of the server performs as a write is one.
	writes := RedisCommandFlags{Flags: []string{"write", "fast"}}
	if got := RedisClassify([]string{"TOUCH", "k"}, &writes); got.Class != RedisClassWrite || len(got.Reasons) == 0 {
		t.Errorf("a read the server flags as a write classified %s %v", got.Class, got.Reasons)
	}
	// The server's flags never loosen what the table says.
	loose := RedisCommandFlags{Flags: []string{"readonly", "fast"}}
	if got := RedisClassify([]string{"FLUSHALL"}, &loose); got.Class != RedisClassDangerous {
		t.Errorf("FLUSHALL classified %s because the server called it a read", got.Class)
	}
	if got := RedisClassify(nil, nil); got.Class != RedisClassBlocked {
		t.Errorf("no command at all classified %s", got.Class)
	}
}

func TestRedisClassifyFlagsSlowCommands(t *testing.T) {
	keys := RedisClassify([]string{"KEYS", "*"}, nil)
	if keys.Class != RedisClassRead || !keys.Slow {
		t.Errorf("KEYS = %s slow=%v, want an allowed read that is flagged slow", keys.Class, keys.Slow)
	}
	if RedisClassify([]string{"GET", "k"}, nil).Slow {
		t.Error("GET is flagged slow")
	}
}

// Every rule has to be reachable by the name the classifier computes, or it
// is a row that silently does nothing.
func TestRedisRulesAreWellFormed(t *testing.T) {
	classes := map[string]bool{
		RedisClassRead: true, RedisClassWrite: true, RedisClassDangerous: true, RedisClassBlocked: true,
	}
	for name, rule := range redisRules {
		if name != strings.ToUpper(name) {
			t.Errorf("rule %q is not upper case and can never match", name)
		}
		if !classes[rule.class] {
			t.Errorf("rule %q has class %q", name, rule.class)
		}
		if rule.group == "" {
			t.Errorf("rule %q has no group", name)
		}
		if container, _, ok := strings.Cut(name, " "); ok {
			if !redisContainers[container] {
				t.Errorf("rule %q is a subcommand of %q, which is not a container", name, container)
			}
			if _, ok := redisRules[container]; !ok {
				t.Errorf("container %q has no rule of its own, so an unlisted subcommand of it is unknown rather than refused", container)
			}
		}
		if rule.admin && rule.class == RedisClassWrite {
			t.Errorf("rule %q is administrative and only a write", name)
		}
	}
}

func TestRedisCommandKeys(t *testing.T) {
	cases := []struct {
		line  string
		flags RedisCommandFlags
		want  []RedisBytes
	}{
		{"SET k v", RedisCommandFlags{FirstKey: 1, LastKey: 1, Step: 1}, []RedisBytes{"k"}},
		{"MSET a 1 b 2", RedisCommandFlags{FirstKey: 1, LastKey: -1, Step: 2}, []RedisBytes{"a", "b"}},
		{"DEL a b c", RedisCommandFlags{FirstKey: 1, LastKey: -1, Step: 1}, []RedisBytes{"a", "b", "c"}},
		{"PING", RedisCommandFlags{}, []RedisBytes{}},
		// Fewer arguments than the positions describe: no panic, no keys
		// invented.
		{"SET", RedisCommandFlags{FirstKey: 1, LastKey: 1, Step: 1}, []RedisBytes{}},
	}
	for _, c := range cases {
		args, _ := RedisParseCommand(c.line)
		flags := c.flags
		if got := RedisCommandKeys(args, &flags); !reflect.DeepEqual(got, c.want) {
			t.Errorf("RedisCommandKeys(%q) = %q, want %q", c.line, got, c.want)
		}
	}
	if got := RedisCommandKeys([]string{"SET", "k", "v"}, nil); len(got) != 0 {
		t.Errorf("keys reported for a command the server did not describe: %q", got)
	}
}

// The audit trail names what an administrative command acted on and must
// never carry what it was set to.
func TestRedisCommandSubjectLeavesValuesOut(t *testing.T) {
	cases := map[string][]string{
		"CONFIG SET requirepass hunter2":            {"requirepass"},
		"CONFIG SET maxmemory 1gb save \"3600 1\"":  {"maxmemory", "save"},
		"CONFIG GET maxmemory*":                     {"maxmemory*"},
		"ACL SETUSER app on >s3cret ~* +@all":       {"app"},
		"ACL DELUSER a b":                           {"a", "b"},
		"GET k":                                     {},
		"SET session:1 the-token-that-must-not-log": {},
	}
	for line, want := range cases {
		args, _ := RedisParseCommand(line)
		got := RedisCommandSubject(args)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("RedisCommandSubject(%q) = %q, want %q", line, got, want)
		}
		for _, secret := range []string{"hunter2", "s3cret", "the-token-that-must-not-log", "1gb"} {
			for _, g := range got {
				if strings.Contains(g, secret) {
					t.Errorf("RedisCommandSubject(%q) carries a value: %q", line, g)
				}
			}
		}
	}
}

func TestRedisReplyTree(t *testing.T) {
	budget := &redisReplyBudget{nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	tree := budget.tree([]any{
		"text", int64(7), nil, 1.5, true, big.NewInt(9), errors.New("ERR nested"),
		map[any]any{"b": int64(2), "a": int64(1)},
		"\xff\xfe",
		int64(math.MaxInt64), math.Inf(1),
	})
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"items":[` +
		`{"type":"string","value":"text"},` +
		`{"type":"integer","value":7},` +
		`{"type":"nil"},` +
		`{"type":"double","value":1.5},` +
		`{"type":"boolean","value":true},` +
		`{"type":"bignumber","value":"9"},` +
		`{"type":"error","value":"ERR nested"},` +
		// A map's entries come out in key order, so the same command gives
		// the same reply twice.
		`{"entries":[{"key":{"type":"string","value":"a"},"value":{"type":"integer","value":1}},` +
		`{"key":{"type":"string","value":"b"},"value":{"type":"integer","value":2}}],"type":"map"},` +
		// Bytes that are not text are carried, not replaced.
		`{"type":"string","value":{"base64":"//4="}},` +
		// Past what a JavaScript number holds exactly, the digits travel as
		// text.
		`{"type":"integer","value":"9223372036854775807"},` +
		`{"type":"double","value":"inf"}` +
		`],"type":"array"}`
	if string(raw) != want {
		t.Errorf("reply tree =\n%s\nwant\n%s", raw, want)
	}
	if budget.truncated {
		t.Error("a small reply was reported truncated")
	}

	empty, _ := json.Marshal((&redisReplyBudget{nodes: 10, bytes: 10}).tree([]any{}))
	if string(empty) != `{"items":[],"type":"array"}` {
		t.Errorf("an empty array = %s, want its items present and empty", empty)
	}
}

func TestRedisReplyTreeIsBounded(t *testing.T) {
	long := make([]any, 50000)
	for i := range long {
		long[i] = int64(i)
	}
	budget := &redisReplyBudget{nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	tree := budget.tree(long)
	if !budget.truncated || !tree.Truncated || tree.Length != len(long) {
		t.Fatalf("a long array: truncated=%v/%v length=%d", budget.truncated, tree.Truncated, tree.Length)
	}
	if len(tree.Items) >= len(long) || len(tree.Items) == 0 {
		t.Errorf("a long array kept %d of %d items", len(tree.Items), len(long))
	}

	budget = &redisReplyBudget{nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	big := strings.Repeat("x", redisReplyMaxString+100)
	node := budget.tree(big)
	if !node.Truncated || node.Length != len(big) {
		t.Fatalf("a long string: truncated=%v length=%d", node.Truncated, node.Length)
	}
	if got := len(node.Value.(RedisBytes)); got != redisReplyMaxString {
		t.Errorf("a long string kept %d bytes, want %d", got, redisReplyMaxString)
	}
}

func TestRedisSyntax(t *testing.T) {
	// The shape COMMAND DOCS gives for SET, reduced.
	args := []any{
		map[any]any{"name": "key", "type": "key"},
		map[any]any{"name": "value", "type": "string"},
		map[any]any{"name": "condition", "type": "oneof", "flags": []any{"optional"}, "arguments": []any{
			map[any]any{"name": "nx", "type": "pure-token", "token": "NX"},
			map[any]any{"name": "xx", "type": "pure-token", "token": "XX"},
		}},
		map[any]any{"name": "seconds", "type": "integer", "token": "EX", "flags": []any{"optional"}},
		map[any]any{"name": "member", "type": "string", "flags": []any{"multiple"}},
	}
	want := "key value [NX | XX] [EX seconds] member [member ...]"
	if got := redisSyntax(args); got != want {
		t.Errorf("redisSyntax = %q, want %q", got, want)
	}
}

func TestRedisCommandEntry(t *testing.T) {
	// Redis 7: ten fields, the last of them the subcommands.
	e := redisCommandEntry([]any{
		"config", int64(-2), []any{}, int64(0), int64(0), int64(0), []any{"@slow"}, []any{}, []any{},
		[]any{[]any{"config|get", int64(-3), []any{"admin", "noscript"}, int64(0), int64(0), int64(0), []any{"@admin"}}},
	})
	if e == nil || e.Name != "config" || len(e.subs) != 1 || e.subs[0].Name != "config|get" {
		t.Fatalf("entry = %+v", e)
	}
	// Redis 6 and KeyDB: seven fields, no subcommands.
	old := redisCommandEntry([]any{"get", int64(2), []any{"readonly", "fast"}, int64(1), int64(1), int64(1), []any{"@read"}})
	if old == nil || old.FirstKey != 1 || len(old.Flags) != 2 || len(old.subs) != 0 {
		t.Fatalf("old-style entry = %+v", old)
	}
	if redisCommandEntry(nil) != nil || redisCommandEntry([]any{"x"}) != nil {
		t.Error("a malformed entry was accepted")
	}
}
