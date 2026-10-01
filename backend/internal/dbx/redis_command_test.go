package dbx

import (
	"bufio"
	"encoding/json"
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

// readWire reads one reply out of raw protocol bytes.
func readWire(wire string) (RedisReply, *redisReplyReader, *bufio.Reader, error) {
	rd := bufio.NewReader(strings.NewReader(wire))
	p := &redisReplyReader{rd: rd, nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	reply, err := p.read(0)
	return reply, p, rd, err
}

func TestRedisReplyReader(t *testing.T) {
	cases := []struct {
		name, wire, json string
	}{
		// The older protocol: five types.
		{"status", "+OK\r\n", `{"type":"status","value":"OK"}`},
		{"error", "-WRONGTYPE not a list\r\n", `{"type":"error","value":"WRONGTYPE not a list"}`},
		{"integer", ":7\r\n", `{"type":"integer","value":7}`},
		{"negative integer", ":-3\r\n", `{"type":"integer","value":-3}`},
		// Past what a JavaScript number holds exactly, the digits travel as
		// text.
		{"large integer", ":9223372036854775807\r\n", `{"type":"integer","value":"9223372036854775807"}`},
		{"string", "$5\r\nhello\r\n", `{"type":"string","value":"hello"}`},
		{"empty string", "$0\r\n\r\n", `{"type":"string","value":""}`},
		{"string with a line break in it", "$7\r\na\r\nb\r\nc\r\n", `{"type":"string","value":"a\r\nb\r\nc"}`},
		// Bytes that are not text are carried, not replaced.
		{"binary string", "$2\r\n\xff\xfe\r\n", `{"type":"string","value":{"base64":"//4="}}`},
		{"nil string", "$-1\r\n", `{"type":"nil"}`},
		{"array", "*2\r\n$1\r\na\r\n:1\r\n", `{"items":[{"type":"string","value":"a"},{"type":"integer","value":1}],"type":"array"}`},
		{"empty array", "*0\r\n", `{"items":[],"type":"array"}`},
		{"nil array", "*-1\r\n", `{"type":"nil"}`},
		{"nested", "*1\r\n*1\r\n-ERR inner\r\n", `{"items":[{"items":[{"type":"error","value":"ERR inner"}],"type":"array"}],"type":"array"}`},
		// The newer protocol.
		{"null", "_\r\n", `{"type":"nil"}`},
		{"double", ",1.5\r\n", `{"type":"double","value":1.5}`},
		{"infinity", ",inf\r\n", `{"type":"double","value":"inf"}`},
		{"boolean", "#t\r\n", `{"type":"boolean","value":true}`},
		{"false", "#f\r\n", `{"type":"boolean","value":false}`},
		{"big number", "(3492890328409238509324850943850943825024385\r\n", `{"type":"bignumber","value":"3492890328409238509324850943850943825024385"}`},
		// A map keeps the order the server sent, which a Go map would not.
		{"map", "%2\r\n+b\r\n:2\r\n+a\r\n:1\r\n",
			`{"entries":[{"key":{"type":"status","value":"b"},"value":{"type":"integer","value":2}},` +
				`{"key":{"type":"status","value":"a"},"value":{"type":"integer","value":1}}],"type":"map"}`},
		{"set", "~2\r\n:1\r\n:2\r\n", `{"items":[{"type":"integer","value":1},{"type":"integer","value":2}],"type":"array"}`},
		{"verbatim string", "=9\r\ntxt:hello\r\n", `{"type":"string","value":"hello"}`},
		{"blob error", "!9\r\nERR nope!\r\n", `{"type":"error","value":"ERR nope!"}`},
		// An attribute is a note about the reply after it, and is read past.
		{"attribute", "|1\r\n+ttl\r\n:3600\r\n:5\r\n", `{"type":"integer","value":5}`},
	}
	for _, c := range cases {
		reply, p, rd, err := readWire(c.wire)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		raw, err := json.Marshal(reply)
		if err != nil || string(raw) != c.json {
			t.Errorf("%s = %s (%v), want %s", c.name, raw, err, c.json)
		}
		if p.truncated || p.stopped {
			t.Errorf("%s was reported cut short", c.name)
		}
		if rd.Buffered() != 0 {
			t.Errorf("%s left %d bytes unread", c.name, rd.Buffered())
		}
	}
	for name, wire := range map[string]string{
		"nothing at all":         "",
		"an unknown type":        "?what\r\n",
		"a length that is text":  "$five\r\nhello\r\n",
		"a string cut short":     "$50\r\nhello\r\n",
		"an array cut short":     "*3\r\n:1\r\n",
		"an integer that is not": ":seven\r\n",
		"nesting without end":    strings.Repeat("*1\r\n", redisReplyMaxDepth+5) + ":1\r\n",
	} {
		if reply, _, _, err := readWire(wire); err == nil {
			t.Errorf("%s was read as %+v", name, reply)
		}
	}
}

// A reply is read only as far as a page can show. What matters is not only
// that the tree is small but that the rest was never pulled off the wire:
// that is the memory a KEYS * on fifty million keys would have cost.
func TestRedisReplyReaderStopsAtItsBudget(t *testing.T) {
	const n = 50000
	var wire strings.Builder
	wire.WriteString("*" + strconv.Itoa(n) + "\r\n")
	for i := 0; i < n; i++ {
		wire.WriteString(":" + strconv.Itoa(i) + "\r\n")
	}
	src := strings.NewReader(wire.String())
	p := &redisReplyReader{rd: bufio.NewReaderSize(src, 4096), nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	reply, err := p.read(0)
	if err != nil {
		t.Fatal(err)
	}
	if !p.truncated || !p.stopped || !reply.Truncated || reply.Length != n {
		t.Fatalf("a long array: truncated=%v stopped=%v node=%v length=%d", p.truncated, p.stopped, reply.Truncated, reply.Length)
	}
	if len(reply.Items) == 0 || len(reply.Items) > redisReplyMaxNodes {
		t.Errorf("a long array kept %d of %d items", len(reply.Items), n)
	}
	if src.Len() < wire.Len()/2 {
		t.Errorf("only %d of %d bytes were left unread: the reader went on past its budget", src.Len(), wire.Len())
	}
	raw, _ := json.Marshal(reply)
	if !strings.Contains(string(raw), `"truncated":true`) || !strings.Contains(string(raw), `"length":50000`) {
		t.Errorf("the cut is not in the JSON: %.200s", raw)
	}

	// A string longer than a page shows is cut, and what follows it is still
	// read: the remainder is small enough to read past.
	long := strings.Repeat("x", redisReplyMaxString+100)
	reply, p, _, err = readWire("*2\r\n$" + strconv.Itoa(len(long)) + "\r\n" + long + "\r\n:5\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Items) != 2 || reply.Items[1].Value != int64(5) || p.stopped || !p.truncated {
		t.Fatalf("after a long string: %d items, stopped=%v truncated=%v", len(reply.Items), p.stopped, p.truncated)
	}
	if s := reply.Items[0]; !s.Truncated || s.Length != len(long) || len(s.Value.(RedisBytes)) != redisReplyMaxString {
		t.Errorf("a long string: truncated=%v length=%d kept=%d", s.Truncated, s.Length, len(s.Value.(RedisBytes)))
	}

	// A string far longer than that is not read past at all.
	huge := redisReplyMaxString + redisReplyMaxDrain + 4096
	src = strings.NewReader("$" + strconv.Itoa(huge) + "\r\n" + strings.Repeat("y", huge) + "\r\n")
	p = &redisReplyReader{rd: bufio.NewReaderSize(src, 4096), nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes}
	reply, err = p.read(0)
	if err != nil || !p.stopped || !reply.Truncated || reply.Length != huge {
		t.Fatalf("a huge string: %v stopped=%v truncated=%v length=%d", err, p.stopped, reply.Truncated, reply.Length)
	}
	if src.Len() < redisReplyMaxDrain {
		t.Errorf("a huge string was read to within %d bytes of its end", src.Len())
	}

	// Many strings that together exceed the byte budget.
	var many strings.Builder
	many.WriteString("*100\r\n")
	chunk := strings.Repeat("z", 100<<10)
	for i := 0; i < 100; i++ {
		many.WriteString("$" + strconv.Itoa(len(chunk)) + "\r\n" + chunk + "\r\n")
	}
	reply, p, _, err = readWire(many.String())
	if err != nil || !p.stopped || !reply.Truncated || reply.Length != 100 || len(reply.Items) >= 100 {
		t.Fatalf("many strings: %v stopped=%v truncated=%v items=%d", err, p.stopped, reply.Truncated, len(reply.Items))
	}
	kept := 0
	for _, item := range reply.Items {
		kept += len(item.Value.(RedisBytes))
	}
	if kept > redisReplyMaxBytes {
		t.Errorf("%d bytes of string were kept, over the budget of %d", kept, redisReplyMaxBytes)
	}
}

// A page of a key is several commands on one connection, so there the reader
// reads past what it does not keep instead of abandoning the connection: the
// tree is as bounded as ever and the next command still gets its own reply.
func TestRedisReplyReaderDrainsWhenAsked(t *testing.T) {
	long := strings.Repeat("x", 5000)
	huge := strings.Repeat("y", redisReplyMaxDrain+redisReplyMaxString)
	var wire strings.Builder
	// A list of six: short, long, short, huge, a nested list, short.
	wire.WriteString("*6\r\n$1\r\na\r\n")
	wire.WriteString("$" + strconv.Itoa(len(long)) + "\r\n" + long + "\r\n")
	wire.WriteString("$1\r\nb\r\n")
	wire.WriteString("$" + strconv.Itoa(len(huge)) + "\r\n" + huge + "\r\n")
	wire.WriteString("*2\r\n:1\r\n$1\r\nz\r\n")
	wire.WriteString("$1\r\nc\r\n")
	// And the reply to the next command.
	wire.WriteString(":42\r\n")

	rd := bufio.NewReader(strings.NewReader(wire.String()))
	p := &redisReplyReader{rd: rd, nodes: redisReplyMaxNodes, bytes: redisReplyMaxBytes, maxString: 100, drain: true}
	reply, err := p.read(0)
	if err != nil {
		t.Fatal(err)
	}
	if p.stopped || !p.truncated || reply.Truncated || len(reply.Items) != 6 {
		t.Fatalf("stopped=%v truncated=%v list cut=%v items=%d", p.stopped, p.truncated, reply.Truncated, len(reply.Items))
	}
	for i, want := range []struct {
		kept int
		size int
	}{{1, 0}, {100, len(long)}, {1, 0}, {100, len(huge)}} {
		item := reply.Items[i]
		if len(item.Value.(RedisBytes)) != want.kept || item.Truncated != (want.size > 0) || (item.Truncated && item.Length != want.size) {
			t.Errorf("item %d: kept %d bytes, truncated=%v, length=%d", i, len(item.Value.(RedisBytes)), item.Truncated, item.Length)
		}
	}
	if reply.Items[5].Value != RedisBytes("c") {
		t.Errorf("the item after the huge one = %+v", reply.Items[5])
	}
	next := &redisReplyReader{rd: rd, nodes: 10, bytes: 100}
	if after, err := next.read(0); err != nil || after.Value != int64(42) {
		t.Fatalf("the next reply on the connection = %+v, %v", after, err)
	}

	// Out of room part way through a list: the list is marked cut with its
	// real length, the rest of it — nested values and all — is read past, and
	// the connection is still in step.
	var deep strings.Builder
	deep.WriteString("*5\r\n:1\r\n:2\r\n*2\r\n$3\r\nabc\r\n*1\r\n:9\r\n%1\r\n+k\r\n$2\r\nvv\r\n:5\r\n+next\r\n")
	rd = bufio.NewReader(strings.NewReader(deep.String()))
	p = &redisReplyReader{rd: rd, nodes: 3, bytes: redisReplyMaxBytes, drain: true}
	reply, err = p.read(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reply.Truncated || reply.Length != 5 || len(reply.Items) != 2 || p.stopped {
		t.Fatalf("a list cut for room: truncated=%v length=%d items=%d stopped=%v", reply.Truncated, reply.Length, len(reply.Items), p.stopped)
	}
	if after, err := (&redisReplyReader{rd: rd, nodes: 10, bytes: 100}).read(0); err != nil || after.Value != "next" {
		t.Fatalf("the next reply after a cut list = %+v, %v", after, err)
	}
}

// The reply tree read off the wire and the one the client library hands back
// are read by the same COMMAND parser.
func TestRedisReplyGeneric(t *testing.T) {
	reply, _, _, err := readWire("*1\r\n*7\r\n$3\r\nget\r\n:2\r\n*2\r\n+readonly\r\n+fast\r\n:1\r\n:1\r\n:1\r\n*1\r\n+@read\r\n")
	if err != nil {
		t.Fatal(err)
	}
	entry := redisCommandEntry(reply.Items[0].generic())
	if entry == nil || entry.Name != "get" || entry.Arity != 2 || entry.FirstKey != 1 ||
		!reflect.DeepEqual(entry.Flags, []string{"readonly", "fast"}) || !reflect.DeepEqual(entry.Categories, []string{"@read"}) {
		t.Errorf("entry = %+v", entry)
	}
	m, _, _, _ := readWire("%1\r\n$4\r\nname\r\n,2.5\r\n")
	if got := redisPairs(m.generic()); got["name"] != 2.5 {
		t.Errorf("a map as generic values = %#v", got)
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
