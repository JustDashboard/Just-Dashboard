package dbx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The console.
//
// A Redis console is the engine's whole surface behind one text box, so the
// route that serves it cannot know from its path what a request will do:
// GET and FLUSHALL arrive at the same address. Every line is therefore
// parsed into its arguments here, and classified before it is sent — by a
// table of the commands the dashboard knows, and by the server's own flags
// for the ones it does not. The class decides which capability the caller
// needs, and anything that cannot be placed is treated as the worst case.
//
// There are four classes:
//
//   - read       cannot change anything.
//   - write      adds or overwrites data, or changes an expiry.
//   - dangerous  removes data, runs code, or changes the server itself. The
//     same things the key browser's delete button and the configuration page
//     ask the destructive capability for, so the console is not a cheaper way
//     to do them.
//   - blocked    cannot work over one request and one reply: a subscription,
//     a transaction, MONITOR, a pop that waits forever — or would take the
//     server down from a text box.

// Command classes.
const (
	RedisClassRead      = "read"
	RedisClassWrite     = "write"
	RedisClassDangerous = "dangerous"
	RedisClassBlocked   = "blocked"
)

// RedisVerdict is what the classifier decided about one command.
type RedisVerdict struct {
	// Name is the command as a reference lists it: "GET", "CONFIG SET".
	Name  string `json:"name"`
	Class string `json:"class"`
	// Admin marks a command that reads or changes the server's own
	// configuration or accounts, which the dashboard's pages for those keep
	// to administrators.
	Admin bool `json:"admin"`
	// Slow is a warning, not a gate: the command runs, and may stall every
	// other client while it does.
	Slow bool `json:"slow"`
	// Known is false for a command that is not in the dashboard's table.
	Known   bool     `json:"known"`
	Reasons []string `json:"reasons"`
}

// RedisCommandFlags is what the server itself says about a command.
type RedisCommandFlags struct {
	Name       string
	Arity      int64
	Flags      []string
	Categories []string
	FirstKey   int64
	LastKey    int64
	Step       int64
}

const (
	redisMaxCommandBytes = 1 << 20
	redisMaxCommandArgs  = 10000
	// redisMaxBlockSeconds is the longest a command may wait on the server
	// for data that is not there yet.
	redisMaxBlockSeconds = 5
)

// RedisParseCommand splits a console line into arguments the way redis-cli
// does: whitespace separates them, double quotes take \n \r \t \b \a \\ \"
// and \xHH, and single quotes take everything literally but \'.
func RedisParseCommand(line string) ([]string, error) {
	if len(line) > redisMaxCommandBytes {
		return nil, fmt.Errorf("the command is too long")
	}
	var (
		args    []string
		current strings.Builder
		started bool
	)
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}
	isSpace := func(c byte) bool {
		return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' || c == 0
	}
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case isSpace(c):
			flush()
			i++
		case c == '"':
			started = true
			i++
			closed := false
			for i < len(line) {
				c := line[i]
				if c == '\\' && i+3 < len(line) && line[i+1] == 'x' && isHex(line[i+2]) && isHex(line[i+3]) {
					b, _ := strconv.ParseUint(line[i+2:i+4], 16, 8)
					current.WriteByte(byte(b))
					i += 4
					continue
				}
				if c == '\\' && i+1 < len(line) {
					switch line[i+1] {
					case 'n':
						current.WriteByte('\n')
					case 'r':
						current.WriteByte('\r')
					case 't':
						current.WriteByte('\t')
					case 'b':
						current.WriteByte('\b')
					case 'a':
						current.WriteByte('\a')
					default:
						current.WriteByte(line[i+1])
					}
					i += 2
					continue
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				current.WriteByte(c)
				i++
			}
			if !closed || (i < len(line) && !isSpace(line[i])) {
				return nil, fmt.Errorf("unbalanced quotes: a closing quote must be followed by a space or the end of the command")
			}
		case c == '\'':
			started = true
			i++
			closed := false
			for i < len(line) {
				c := line[i]
				if c == '\\' && i+1 < len(line) && line[i+1] == '\'' {
					current.WriteByte('\'')
					i += 2
					continue
				}
				if c == '\'' {
					closed = true
					i++
					break
				}
				current.WriteByte(c)
				i++
			}
			if !closed || (i < len(line) && !isSpace(line[i])) {
				return nil, fmt.Errorf("unbalanced quotes: a closing quote must be followed by a space or the end of the command")
			}
		default:
			started = true
			current.WriteByte(c)
			i++
		}
		if len(args) > redisMaxCommandArgs {
			return nil, fmt.Errorf("the command has too many arguments")
		}
	}
	flush()
	if len(args) == 0 {
		return nil, fmt.Errorf("a command is required")
	}
	return args, nil
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// redisRule is one row of the command table.
type redisRule struct {
	class  string
	group  string
	admin  bool
	slow   bool
	reason string
	// check looks at the arguments, for the commands whose effect depends on
	// them. It may only raise the class, never lower it.
	check func(args []string) (class, reason string)
}

// redisRules is every command the dashboard has an opinion about, by name,
// with "CONFIG SET"-style entries for the subcommands of a container and a
// bare entry for the container itself, which is the answer for a subcommand
// the table does not list.
var redisRules, redisContainers = buildRedisRules()

func buildRedisRules() (map[string]redisRule, map[string]bool) {
	rules := map[string]redisRule{}
	containers := map[string]bool{}
	add := func(class, group, reason string, names ...string) {
		for _, name := range names {
			rules[name] = redisRule{class: class, group: group, reason: reason}
			if container, _, ok := strings.Cut(name, " "); ok {
				containers[container] = true
			}
		}
	}
	read := func(group string, names ...string) { add(RedisClassRead, group, "", names...) }
	write := func(group string, names ...string) { add(RedisClassWrite, group, "", names...) }
	danger := func(group, reason string, names ...string) { add(RedisClassDangerous, group, reason, names...) }
	block := func(group, reason string, names ...string) { add(RedisClassBlocked, group, reason, names...) }
	admin := func(names ...string) {
		for _, name := range names {
			r := rules[name]
			r.admin = true
			rules[name] = r
		}
	}
	slow := func(names ...string) {
		for _, name := range names {
			r := rules[name]
			r.slow = true
			rules[name] = r
		}
	}
	check := func(fn func(args []string) (string, string), names ...string) {
		for _, name := range names {
			r := rules[name]
			r.check = fn
			rules[name] = r
		}
	}
	container := func(class, group, reason string, names ...string) {
		for _, name := range names {
			containers[name] = true
			rules[name] = redisRule{class: class, group: group, reason: reason}
		}
	}

	const (
		removesKeys    = "deletes keys"
		removesMembers = "removes members from a collection"
		runsCode       = "runs a script on the server, which can do anything a client can"
		changesServer  = "changes the server itself, for every client"
		noSession      = "needs a connection that stays open, and each console command gets its own"
		unknownSub     = "is a subcommand the dashboard does not know, so it is treated as destructive"
	)

	// --- strings and bitmaps ---
	read("string", "GET", "MGET", "STRLEN", "GETRANGE", "SUBSTR", "LCS", "DIGEST")
	write("string", "SET", "SETNX", "SETEX", "PSETEX", "MSET", "MSETNX", "MSETEX", "GETSET", "GETEX",
		"APPEND", "SETRANGE", "INCR", "INCRBY", "INCRBYFLOAT", "DECR", "DECRBY")
	danger("string", removesKeys, "GETDEL", "DELEX")
	read("bitmap", "BITCOUNT", "BITPOS", "GETBIT", "BITFIELD_RO")
	write("bitmap", "SETBIT", "BITOP", "BITFIELD")

	// --- generic ---
	read("generic", "EXISTS", "TYPE", "TTL", "PTTL", "EXPIRETIME", "PEXPIRETIME", "SCAN", "KEYS",
		"RANDOMKEY", "DUMP", "TOUCH", "SORT_RO")
	write("generic", "EXPIRE", "PEXPIRE", "EXPIREAT", "PEXPIREAT", "PERSIST", "RENAMENX", "MOVE",
		"COPY", "RESTORE", "SORT")
	danger("generic", removesKeys, "DEL", "UNLINK")
	danger("generic", "replaces whatever key is already at the new name", "RENAME")
	slow("KEYS")
	check(redisCheckExpiry, "EXPIRE", "PEXPIRE", "EXPIREAT", "PEXPIREAT")
	check(redisCheckReplace, "COPY", "RESTORE")
	container(RedisClassRead, "generic", "", "OBJECT")
	read("generic", "OBJECT ENCODING", "OBJECT FREQ", "OBJECT IDLETIME", "OBJECT REFCOUNT", "OBJECT HELP")
	write("generic", "WAIT", "WAITAOF")
	check(redisCheckWait, "WAIT", "WAITAOF")
	block("generic", "moves keys to another server and holds this one while it does", "MIGRATE")

	// --- hashes ---
	read("hash", "HGET", "HMGET", "HGETALL", "HKEYS", "HVALS", "HLEN", "HEXISTS", "HSTRLEN",
		"HRANDFIELD", "HSCAN", "HTTL", "HPTTL", "HEXPIRETIME", "HPEXPIRETIME")
	write("hash", "HSET", "HSETNX", "HMSET", "HSETEX", "HGETEX", "HINCRBY", "HINCRBYFLOAT",
		"HEXPIRE", "HPEXPIRE", "HEXPIREAT", "HPEXPIREAT", "HPERSIST")
	danger("hash", removesMembers, "HDEL", "HGETDEL")

	// --- lists ---
	read("list", "LRANGE", "LINDEX", "LLEN", "LPOS")
	// A move takes an element out of one list and puts it in another; nothing
	// is lost, so it is a write like the push it ends with.
	write("list", "LPUSH", "RPUSH", "LPUSHX", "RPUSHX", "LSET", "LINSERT", "LMOVE", "RPOPLPUSH",
		"BLMOVE", "BRPOPLPUSH")
	danger("list", removesMembers, "LPOP", "RPOP", "LREM", "LTRIM", "LMPOP",
		"BLPOP", "BRPOP", "BLMPOP")
	check(redisCheckBlockLast, "BLPOP", "BRPOP", "BLMOVE", "BRPOPLPUSH")
	check(redisCheckBlockFirst, "BLMPOP")

	// --- sets ---
	read("set", "SMEMBERS", "SISMEMBER", "SMISMEMBER", "SCARD", "SRANDMEMBER", "SSCAN",
		"SINTER", "SUNION", "SDIFF", "SINTERCARD")
	write("set", "SADD", "SMOVE", "SINTERSTORE", "SUNIONSTORE", "SDIFFSTORE")
	danger("set", removesMembers, "SREM", "SPOP")

	// --- sorted sets ---
	read("sorted-set", "ZRANGE", "ZREVRANGE", "ZRANGEBYSCORE", "ZREVRANGEBYSCORE", "ZRANGEBYLEX",
		"ZREVRANGEBYLEX", "ZSCORE", "ZMSCORE", "ZRANK", "ZREVRANK", "ZCARD", "ZCOUNT", "ZLEXCOUNT",
		"ZRANDMEMBER", "ZSCAN", "ZINTER", "ZUNION", "ZDIFF", "ZINTERCARD")
	write("sorted-set", "ZADD", "ZINCRBY", "ZINTERSTORE", "ZUNIONSTORE", "ZDIFFSTORE", "ZRANGESTORE")
	danger("sorted-set", removesMembers, "ZREM", "ZPOPMIN", "ZPOPMAX", "ZMPOP", "ZREMRANGEBYRANK",
		"ZREMRANGEBYSCORE", "ZREMRANGEBYLEX", "BZPOPMIN", "BZPOPMAX", "BZMPOP")
	check(redisCheckBlockLast, "BZPOPMIN", "BZPOPMAX")
	check(redisCheckBlockFirst, "BZMPOP")

	// --- streams ---
	read("stream", "XRANGE", "XREVRANGE", "XLEN", "XREAD", "XPENDING")
	write("stream", "XADD", "XACK", "XCLAIM", "XAUTOCLAIM", "XSETID", "XREADGROUP")
	danger("stream", "removes entries from a stream", "XDEL", "XTRIM", "XDELEX", "XACKDEL")
	check(redisCheckXAdd, "XADD")
	check(redisCheckXRead, "XREAD", "XREADGROUP")
	container(RedisClassRead, "stream", "", "XINFO")
	read("stream", "XINFO STREAM", "XINFO GROUPS", "XINFO CONSUMERS", "XINFO HELP")
	container(RedisClassDangerous, "stream", unknownSub, "XGROUP")
	read("stream", "XGROUP HELP")
	write("stream", "XGROUP CREATE", "XGROUP SETID", "XGROUP CREATECONSUMER")
	danger("stream", "removes a consumer group or a consumer, with everything it had pending",
		"XGROUP DESTROY", "XGROUP DELCONSUMER")

	// --- geo and hyperloglog ---
	read("geo", "GEODIST", "GEOHASH", "GEOPOS", "GEOSEARCH", "GEORADIUS_RO", "GEORADIUSBYMEMBER_RO")
	write("geo", "GEOADD", "GEOSEARCHSTORE", "GEORADIUS", "GEORADIUSBYMEMBER")
	read("hyperloglog", "PFCOUNT")
	write("hyperloglog", "PFADD", "PFMERGE")
	danger("hyperloglog", "is an internal debugging command", "PFDEBUG", "PFSELFTEST")
	admin("PFDEBUG", "PFSELFTEST")

	// --- pub/sub ---
	write("pubsub", "PUBLISH", "SPUBLISH")
	container(RedisClassRead, "pubsub", "", "PUBSUB")
	read("pubsub", "PUBSUB CHANNELS", "PUBSUB NUMSUB", "PUBSUB NUMPAT", "PUBSUB SHARDCHANNELS",
		"PUBSUB SHARDNUMSUB", "PUBSUB HELP")
	block("pubsub", "turns the connection into a subscription; use the Pub/Sub page, which is built for one",
		"SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "UNSUBSCRIBE", "PUNSUBSCRIBE", "SUNSUBSCRIBE")

	// --- scripting ---
	danger("scripting", runsCode, "EVAL", "EVALSHA", "EVAL_RO", "EVALSHA_RO", "FCALL", "FCALL_RO")
	container(RedisClassDangerous, "scripting", unknownSub, "SCRIPT", "FUNCTION")
	read("scripting", "SCRIPT EXISTS", "SCRIPT HELP", "FUNCTION LIST", "FUNCTION STATS",
		"FUNCTION DUMP", "FUNCTION HELP")
	danger("scripting", "changes the scripts the server holds, or stops one that is running",
		"SCRIPT FLUSH", "SCRIPT KILL", "SCRIPT LOAD", "FUNCTION FLUSH", "FUNCTION DELETE",
		"FUNCTION KILL", "FUNCTION LOAD", "FUNCTION RESTORE")
	block("scripting", noSession, "SCRIPT DEBUG")

	// --- transactions and connection state ---
	block("transactions", noSession, "MULTI", "EXEC", "DISCARD", "WATCH", "UNWATCH")
	read("connection", "PING", "ECHO")
	block("connection", "would change how this connection is authenticated, which the dashboard manages itself",
		"AUTH", "HELLO")
	block("connection", "would be forgotten at once, because each console command gets its own connection; choose the database beside the console instead",
		"SELECT")
	block("connection", noSession, "QUIT", "RESET", "ASKING", "READONLY", "READWRITE")
	container(RedisClassDangerous, "connection", unknownSub, "CLIENT")
	read("connection", "CLIENT LIST", "CLIENT INFO", "CLIENT ID", "CLIENT GETNAME", "CLIENT GETREDIR",
		"CLIENT TRACKINGINFO", "CLIENT HELP")
	write("connection", "CLIENT SETNAME", "CLIENT SETINFO", "CLIENT NO-EVICT", "CLIENT NO-TOUCH",
		"CLIENT UNPAUSE")
	danger("connection", "disconnects or interrupts other clients", "CLIENT KILL", "CLIENT UNBLOCK")
	block("connection", "stops every other client from being served", "CLIENT PAUSE")
	block("connection", noSession, "CLIENT REPLY", "CLIENT TRACKING", "CLIENT CACHING")

	// --- server ---
	read("server", "INFO", "DBSIZE", "TIME", "ROLE", "LASTSAVE", "LOLWUT")
	write("server", "BGSAVE", "BGREWRITEAOF")
	danger("server", "empties a whole database", "FLUSHDB", "FLUSHALL")
	danger("server", "exchanges two databases under every connected client", "SWAPDB")
	danger("server", "saves in the foreground, serving nobody until it finishes", "SAVE")
	slow("FLUSHDB", "FLUSHALL", "SAVE")
	danger("server", "changes which server this one replicates, discarding its own data",
		"REPLICAOF", "SLAVEOF", "FAILOVER")
	admin("REPLICAOF", "SLAVEOF", "FAILOVER")
	block("server", "stops the server", "SHUTDOWN")
	block("server", "streams every command the server runs; use the Profiler, which is built for it", "MONITOR")
	block("server", "is the replication protocol, not a command for a client",
		"SYNC", "PSYNC", "REPLCONF", "RESTORE-ASKING")
	container(RedisClassRead, "server", "", "COMMAND")
	read("server", "COMMAND COUNT", "COMMAND DOCS", "COMMAND INFO", "COMMAND LIST", "COMMAND GETKEYS",
		"COMMAND GETKEYSANDFLAGS", "COMMAND HELP")
	container(RedisClassDangerous, "server", unknownSub, "CONFIG", "ACL", "MODULE",
		"LATENCY", "SLOWLOG", "MEMORY")
	container(RedisClassDangerous, "server", "reads and alters the server's internals", "DEBUG")
	admin("CONFIG", "ACL", "MODULE", "DEBUG")
	// CONFIG GET is a read, and it reads requirepass.
	read("server", "CONFIG GET", "CONFIG HELP")
	danger("server", changesServer, "CONFIG SET", "CONFIG REWRITE", "CONFIG RESETSTAT")
	admin("CONFIG GET", "CONFIG HELP", "CONFIG SET", "CONFIG REWRITE", "CONFIG RESETSTAT")
	// The ACL holds password hashes, so reading it is an administrator's read.
	read("server", "ACL LIST", "ACL GETUSER", "ACL USERS", "ACL WHOAMI", "ACL CAT", "ACL GENPASS",
		"ACL DRYRUN", "ACL HELP")
	danger("server", "changes who may connect and what they may do",
		"ACL SETUSER", "ACL DELUSER", "ACL LOAD", "ACL SAVE", "ACL LOG")
	admin("ACL LIST", "ACL GETUSER", "ACL USERS", "ACL WHOAMI", "ACL CAT", "ACL GENPASS", "ACL DRYRUN",
		"ACL HELP", "ACL SETUSER", "ACL DELUSER", "ACL LOAD", "ACL SAVE", "ACL LOG")
	read("server", "MODULE LIST", "MODULE HELP")
	danger("server", "loads or unloads native code in the server", "MODULE LOAD", "MODULE LOADEX", "MODULE UNLOAD")
	admin("MODULE LIST", "MODULE HELP", "MODULE LOAD", "MODULE LOADEX", "MODULE UNLOAD")
	block("server", "crashes, stalls or restarts the server",
		"DEBUG SEGFAULT", "DEBUG PANIC", "DEBUG RESTART", "DEBUG CRASH-AND-RECOVER", "DEBUG OOM",
		"DEBUG ASSERT", "DEBUG LEAK", "DEBUG SLEEP")
	read("server", "LATENCY LATEST", "LATENCY HISTORY", "LATENCY DOCTOR", "LATENCY GRAPH",
		"LATENCY HISTOGRAM", "LATENCY HELP", "SLOWLOG GET", "SLOWLOG LEN", "SLOWLOG HELP",
		"MEMORY USAGE", "MEMORY STATS", "MEMORY DOCTOR", "MEMORY MALLOC-STATS", "MEMORY HELP")
	write("server", "MEMORY PURGE")
	danger("server", "discards what the server has recorded", "LATENCY RESET", "SLOWLOG RESET")

	// --- cluster and sentinel ---
	container(RedisClassDangerous, "cluster", unknownSub, "CLUSTER", "SENTINEL")
	admin("CLUSTER", "SENTINEL")
	read("cluster", "CLUSTER INFO", "CLUSTER NODES", "CLUSTER SLOTS", "CLUSTER SHARDS", "CLUSTER MYID",
		"CLUSTER MYSHARDID", "CLUSTER KEYSLOT", "CLUSTER COUNTKEYSINSLOT", "CLUSTER GETKEYSINSLOT",
		"CLUSTER COUNT-FAILURE-REPORTS", "CLUSTER LINKS", "CLUSTER REPLICAS", "CLUSTER SLAVES",
		"CLUSTER HELP",
		"SENTINEL MASTERS", "SENTINEL MASTER", "SENTINEL REPLICAS", "SENTINEL SLAVES",
		"SENTINEL SENTINELS", "SENTINEL GET-MASTER-ADDR-BY-NAME", "SENTINEL CKQUORUM",
		"SENTINEL MYID", "SENTINEL HELP")

	// --- modules: JSON, search, time series, probabilistic, vector sets ---
	read("json", "JSON.GET", "JSON.MGET", "JSON.TYPE", "JSON.OBJKEYS", "JSON.OBJLEN", "JSON.ARRLEN",
		"JSON.ARRINDEX", "JSON.STRLEN", "JSON.RESP", "JSON.DEBUG")
	write("json", "JSON.SET", "JSON.MSET", "JSON.MERGE", "JSON.ARRAPPEND", "JSON.ARRINSERT",
		"JSON.NUMINCRBY", "JSON.NUMMULTBY", "JSON.STRAPPEND", "JSON.TOGGLE")
	danger("json", "removes part of a JSON document", "JSON.DEL", "JSON.FORGET", "JSON.CLEAR",
		"JSON.ARRPOP", "JSON.ARRTRIM")
	read("search", "FT._LIST", "FT.INFO", "FT.SEARCH", "FT.AGGREGATE", "FT.EXPLAIN", "FT.EXPLAINCLI",
		"FT.PROFILE", "FT.TAGVALS", "FT.SPELLCHECK", "FT.SYNDUMP", "FT.DICTDUMP", "FT.SUGGET", "FT.SUGLEN")
	write("search", "FT.CREATE", "FT.ALTER", "FT.ALIASADD", "FT.ALIASUPDATE", "FT.SYNUPDATE",
		"FT.DICTADD", "FT.SUGADD", "FT.CURSOR")
	danger("search", "removes an index or part of one", "FT.DROPINDEX", "FT.ALIASDEL", "FT.DICTDEL", "FT.SUGDEL")
	danger("search", changesServer, "FT.CONFIG")
	admin("FT.CONFIG")
	read("timeseries", "TS.GET", "TS.MGET", "TS.RANGE", "TS.REVRANGE", "TS.MRANGE", "TS.MREVRANGE",
		"TS.INFO", "TS.QUERYINDEX")
	write("timeseries", "TS.CREATE", "TS.ALTER", "TS.ADD", "TS.MADD", "TS.INCRBY", "TS.DECRBY", "TS.CREATERULE")
	danger("timeseries", "removes samples or a compaction rule", "TS.DEL", "TS.DELETERULE")
	read("probabilistic", "BF.EXISTS", "BF.MEXISTS", "BF.INFO", "BF.CARD", "BF.SCANDUMP",
		"CF.EXISTS", "CF.MEXISTS", "CF.COUNT", "CF.INFO", "CF.SCANDUMP",
		"CMS.QUERY", "CMS.INFO", "TOPK.QUERY", "TOPK.COUNT", "TOPK.LIST", "TOPK.INFO",
		"TDIGEST.BYRANK", "TDIGEST.BYREVRANK", "TDIGEST.CDF", "TDIGEST.INFO", "TDIGEST.MAX",
		"TDIGEST.MIN", "TDIGEST.QUANTILE", "TDIGEST.RANK", "TDIGEST.REVRANK", "TDIGEST.TRIMMED_MEAN")
	write("probabilistic", "BF.ADD", "BF.MADD", "BF.INSERT", "BF.RESERVE", "BF.LOADCHUNK",
		"CF.ADD", "CF.ADDNX", "CF.INSERT", "CF.INSERTNX", "CF.RESERVE", "CF.LOADCHUNK",
		"CMS.INCRBY", "CMS.INITBYDIM", "CMS.INITBYPROB", "CMS.MERGE",
		"TOPK.ADD", "TOPK.INCRBY", "TOPK.RESERVE", "TDIGEST.ADD", "TDIGEST.CREATE", "TDIGEST.MERGE")
	danger("probabilistic", "removes an item or empties a sketch", "CF.DEL", "TDIGEST.RESET")
	read("vector-set", "VCARD", "VDIM", "VEMB", "VGETATTR", "VINFO", "VLINKS", "VRANDMEMBER",
		"VSIM", "VISMEMBER", "VRANGE")
	write("vector-set", "VADD", "VSETATTR")
	danger("vector-set", removesMembers, "VREM")

	return rules, containers
}

// redisCommandName is the name a command is looked up by: the first word in
// upper case, with the second joined on for a container.
func redisCommandName(args []string) (name string, sub bool) {
	name = strings.ToUpper(args[0])
	if redisContainers[name] && len(args) > 1 {
		return name + " " + strings.ToUpper(args[1]), true
	}
	return name, false
}

// RedisClassify decides what a command is. server is what COMMAND INFO said
// about it, or nil when the server does not know it either.
func RedisClassify(args []string, server *RedisCommandFlags) RedisVerdict {
	v := RedisVerdict{Reasons: []string{}}
	if len(args) == 0 {
		v.Class = RedisClassBlocked
		v.Reasons = append(v.Reasons, "a command is required")
		return v
	}
	name, sub := redisCommandName(args)
	v.Name = name
	rule, known := redisRules[name]
	if !known && sub {
		// An unlisted subcommand takes its container's answer.
		rule, known = redisRules[strings.ToUpper(args[0])]
	}
	if known {
		v.Known = true
		v.Class, v.Admin, v.Slow = rule.class, rule.admin, rule.slow
		if rule.reason != "" {
			v.Reasons = append(v.Reasons, rule.reason)
		}
		if rule.check != nil {
			if class, reason := rule.check(args); redisClassRank(class) > redisClassRank(v.Class) {
				v.Class = class
				v.Reasons = append(v.Reasons, reason)
			}
		}
		// The table says what a command is for; the server says what its own
		// build of it does. Where the server calls something a write that the
		// table has as a read, the server is right about itself.
		if v.Class == RedisClassRead && server != nil {
			for _, f := range server.Flags {
				if strings.EqualFold(f, "write") {
					v.Class = RedisClassWrite
					v.Reasons = append(v.Reasons, "is a write on this server, by the server's own description of it")
				}
			}
		}
		if v.Slow {
			v.Reasons = append(v.Reasons, "may stall every other client while it runs")
		}
		return v
	}
	// Fail closed. A command nobody enumerated — a module's, a newer
	// release's — is a read only if the server itself says it writes nothing
	// and is neither administrative nor a way to hold the connection open.
	// Everything else costs the destructive capability, which is the side
	// this is allowed to be wrong on.
	v.Class = RedisClassDangerous
	if server == nil {
		v.Reasons = append(v.Reasons, "is not a command this server or the dashboard recognises, so it is treated as destructive")
		return v
	}
	flags := map[string]bool{}
	for _, f := range server.Flags {
		flags[strings.ToLower(f)] = true
	}
	for _, c := range server.Categories {
		flags[strings.ToLower(c)] = true
	}
	switch {
	case flags["pubsub"] || flags["@pubsub"] || flags["blocking"] || flags["@blocking"]:
		v.Class = RedisClassBlocked
		v.Reasons = append(v.Reasons, "holds the connection open, by the server's own description of it")
	case flags["readonly"] && !flags["write"] && !flags["admin"] && !flags["@admin"] && !flags["@dangerous"] && !flags["noscript"]:
		v.Class = RedisClassRead
	default:
		v.Reasons = append(v.Reasons, "is not a command the dashboard knows, and the server does not describe it as a plain read, so it is treated as destructive")
	}
	return v
}

func redisClassRank(class string) int {
	switch class {
	case RedisClassBlocked:
		return 3
	case RedisClassDangerous:
		return 2
	case RedisClassWrite:
		return 1
	}
	return 0
}

// redisCheckExpiry catches the expiry that is really a delete: zero or a
// negative number of seconds, or a moment already past.
func redisCheckExpiry(args []string) (string, string) {
	if len(args) < 3 {
		return "", ""
	}
	n, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return "", ""
	}
	past := n <= 0
	switch strings.ToUpper(args[0]) {
	case "EXPIREAT":
		past = n <= time.Now().Unix()
	case "PEXPIREAT":
		past = n <= time.Now().UnixMilli()
	}
	if past {
		return RedisClassDangerous, "an expiry that has already passed deletes the key at once"
	}
	return "", ""
}

func redisCheckReplace(args []string) (string, string) {
	for _, a := range args[1:] {
		if strings.EqualFold(a, "REPLACE") {
			return RedisClassDangerous, "REPLACE overwrites the key already at the destination"
		}
	}
	return "", ""
}

// redisBlockVerdict is the rule for anything that waits: a wait of a few
// seconds is a command, a wait with no end is a connection the dashboard
// never gets back.
func redisBlockVerdict(text string, unit float64) (string, string) {
	seconds, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return RedisClassBlocked, "its timeout could not be read"
	}
	seconds *= unit
	if seconds <= 0 || seconds > redisMaxBlockSeconds {
		return RedisClassBlocked, fmt.Sprintf(
			"would wait on the server for longer than the console holds a request open; give it a timeout of at most %d seconds", redisMaxBlockSeconds)
	}
	return "", ""
}

func redisCheckBlockLast(args []string) (string, string) {
	if len(args) < 3 {
		return RedisClassBlocked, "needs a timeout"
	}
	return redisBlockVerdict(args[len(args)-1], 1)
}

func redisCheckBlockFirst(args []string) (string, string) {
	if len(args) < 2 {
		return RedisClassBlocked, "needs a timeout"
	}
	return redisBlockVerdict(args[1], 1)
}

func redisCheckWait(args []string) (string, string) {
	at := 2
	if strings.EqualFold(args[0], "WAITAOF") {
		at = 3
	}
	if len(args) <= at {
		return RedisClassBlocked, "needs a timeout"
	}
	return redisBlockVerdict(args[at], 0.001)
}

func redisCheckXRead(args []string) (string, string) {
	for i := 1; i+1 < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "STREAMS":
			return "", ""
		case "BLOCK":
			return redisBlockVerdict(args[i+1], 0.001)
		}
	}
	return "", ""
}

// redisCheckXAdd finds the trimming options, which sit between the key and
// the entry id and make an append remove entries as well.
func redisCheckXAdd(args []string) (string, string) {
	for i := 2; i < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "NOMKSTREAM", "KEEPREF", "DELREF", "ACKED":
			continue
		case "MAXLEN", "MINID":
			return RedisClassDangerous, "trims the stream as it adds, which removes entries"
		}
		return "", ""
	}
	return "", ""
}

// RedisCommandKeys picks the key names out of a command by the positions the
// server gave for it. They are what the audit trail records: which keys a
// command touched, and never what was written to them.
func RedisCommandKeys(args []string, server *RedisCommandFlags) []RedisBytes {
	out := []RedisBytes{}
	if server == nil || server.FirstKey <= 0 || server.Step <= 0 {
		return out
	}
	last := server.LastKey
	if last < 0 {
		last = int64(len(args)) + last
	}
	for i := server.FirstKey; i <= last && i < int64(len(args)) && len(out) < 16; i += server.Step {
		out = append(out, RedisBytes(args[i]))
	}
	return out
}

// RedisCommandSubject names what an administrative command acted on — the
// parameter a CONFIG SET changed, the account an ACL SETUSER edited — again
// without the value it was given.
func RedisCommandSubject(args []string) []string {
	name, _ := redisCommandName(args)
	out := []string{}
	switch name {
	case "CONFIG SET":
		for i := 2; i < len(args); i += 2 {
			out = append(out, args[i])
		}
	case "CONFIG GET":
		out = append(out, args[2:]...)
	case "ACL SETUSER", "ACL GETUSER":
		if len(args) > 2 {
			out = append(out, args[2])
		}
	case "ACL DELUSER":
		out = append(out, args[2:]...)
	}
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

type redisCommandEntryT struct {
	RedisCommandFlags
	subs []redisCommandEntryT
}

// redisCommandEntry reads one element of a COMMAND reply: name, arity,
// flags, first key, last key, step, and from Redis 6 the ACL categories and
// from Redis 7 the subcommands.
func redisCommandEntry(v any) *redisCommandEntryT {
	fields := redisSlice(v)
	if len(fields) < 6 {
		return nil
	}
	e := &redisCommandEntryT{}
	e.Name = redisText(fields[0])
	e.Arity, _ = redisInt(fields[1])
	e.Flags = redisStringList(fields[2])
	e.FirstKey, _ = redisInt(fields[3])
	e.LastKey, _ = redisInt(fields[4])
	e.Step, _ = redisInt(fields[5])
	e.Categories = []string{}
	if len(fields) > 6 {
		e.Categories = redisStringList(fields[6])
	}
	if len(fields) > 9 {
		for _, s := range redisSlice(fields[9]) {
			if sub := redisCommandEntry(s); sub != nil {
				e.subs = append(e.subs, *sub)
			}
		}
	}
	return e
}

// RedisReply is a reply as a tree, so a page can draw a nested answer without
// knowing which command produced it.
//
// Type is one of nil, status, string, integer, double, boolean, bignumber,
// array, map and error. A status is the short acknowledgement — OK, PONG,
// QUEUED — as opposed to a string of data. An integer too large for a
// JavaScript number, a double that is not finite and a bignumber all carry
// their value as text. A map keeps its entries in the order the server sent
// them.
type RedisReply struct {
	Type    string
	Value   any
	Items   []RedisReply
	Entries []RedisReplyEntry
	// Length is the full size — bytes of a string, elements of an array or a
	// map — of a node that was cut short, and Truncated says it was.
	Length    int
	Truncated bool
}

// RedisReplyEntry is one pair of a map reply.
type RedisReplyEntry struct {
	Key   RedisReply `json:"key"`
	Value RedisReply `json:"value"`
}

// MarshalJSON writes only the fields a node of its type has, so an empty
// array is "items": [] and a scalar carries no "items" at all.
func (r RedisReply) MarshalJSON() ([]byte, error) {
	out := map[string]any{"type": r.Type}
	switch r.Type {
	case "nil":
	case "array":
		items := r.Items
		if items == nil {
			items = []RedisReply{}
		}
		out["items"] = items
	case "map":
		entries := r.Entries
		if entries == nil {
			entries = []RedisReplyEntry{}
		}
		out["entries"] = entries
	default:
		out["value"] = r.Value
	}
	if r.Truncated {
		out["truncated"] = true
		out["length"] = r.Length
	}
	return json.Marshal(out)
}

// RedisCommandResult is one console command, run.
type RedisCommandResult struct {
	RedisVerdict
	DB int `json:"db"`
	// Ms is the round trip to the server and back.
	Ms    float64    `json:"ms"`
	Reply RedisReply `json:"reply"`
	// Truncated says the reply was larger than the console shows.
	Truncated bool `json:"truncated"`
}

// RedisCommandRef is one command of the reference a console completes from.
type RedisCommandRef struct {
	// Name is upper case, with a container's subcommand after a space.
	Name       string   `json:"name"`
	Group      string   `json:"group,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Since      string   `json:"since,omitempty"`
	Complexity string   `json:"complexity,omitempty"`
	Syntax     string   `json:"syntax,omitempty"`
	Arity      int64    `json:"arity"`
	Flags      []string `json:"flags"`
	Categories []string `json:"categories"`
	// Class, Admin and Slow are how the console treats the command before
	// its arguments are known; arguments can only make it stricter.
	Class      string `json:"class"`
	Admin      bool   `json:"admin"`
	Slow       bool   `json:"slow,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

// RedisCommandReference lists every command the server has, with the
// documentation it carries for them where it carries any.
//
// The list is the server's own, which is the point: it is right on Valkey,
// KeyDB and Dragonfly, and it includes whatever modules are loaded, where a
// list compiled into the dashboard would be right for one release of one of
// them.
func RedisCommandReference(ctx context.Context, client *redis.Client, profile *RedisProfile) ([]RedisCommandRef, error) {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	raw, err := client.Do(ctx, "COMMAND").Slice()
	if err != nil {
		return nil, err
	}
	docs := map[string]map[string]any{}
	if profile.Features.CommandDocs {
		// Best effort: without it the reference still has every name, arity
		// and flag, and only the prose is missing.
		if reply, err := client.Do(ctx, "COMMAND", "DOCS").Result(); err == nil {
			for name, doc := range redisPairs(reply) {
				fields := redisPairs(doc)
				docs[strings.ToLower(name)] = fields
				for sub, subDoc := range redisPairs(fields["subcommands"]) {
					docs[strings.ToLower(sub)] = redisPairs(subDoc)
				}
			}
		}
	}
	out := make([]RedisCommandRef, 0, len(raw)+64)
	var add func(e *redisCommandEntryT)
	add = func(e *redisCommandEntryT) {
		name := strings.ToUpper(strings.ReplaceAll(e.Name, "|", " "))
		verdict := RedisClassify(strings.Fields(name), &e.RedisCommandFlags)
		ref := RedisCommandRef{
			Name: name, Arity: e.Arity, Flags: e.Flags, Categories: e.Categories,
			Class: redisBaseClass(name, verdict), Admin: verdict.Admin, Slow: verdict.Slow,
		}
		if rule, ok := redisRules[name]; ok {
			ref.Group = rule.group
		}
		if doc, ok := docs[strings.ToLower(e.Name)]; ok {
			ref.Summary = redisText(doc["summary"])
			ref.Since = redisText(doc["since"])
			ref.Complexity = redisText(doc["complexity"])
			if group := redisText(doc["group"]); group != "" {
				ref.Group = strings.ReplaceAll(group, "_", "-")
			}
			ref.Syntax = redisSyntax(redisSlice(doc["arguments"]))
			for _, flag := range redisStringList(doc["doc_flags"]) {
				if flag == "deprecated" {
					ref.Deprecated = true
				}
			}
		}
		// A container with subcommands is not itself something to run.
		if len(e.subs) == 0 || e.Arity > 0 {
			out = append(out, ref)
		}
		for i := range e.subs {
			add(&e.subs[i])
		}
	}
	for _, item := range raw {
		if e := redisCommandEntry(item); e != nil {
			add(e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// redisBaseClass is a command's class with no arguments to judge it by. The
// rules that read arguments would otherwise all report "needs a timeout" for
// a name on its own.
func redisBaseClass(name string, verdict RedisVerdict) string {
	if rule, ok := redisRules[name]; ok {
		return rule.class
	}
	return verdict.Class
}

// redisSyntax renders COMMAND DOCS's argument tree as the one-line synopsis
// the Redis documentation prints: key [NX | XX] [EX seconds].
func redisSyntax(arguments []any) string {
	parts := make([]string, 0, len(arguments))
	for _, a := range arguments {
		if s := redisSyntaxArg(redisPairs(a)); s != "" {
			parts = append(parts, s)
		}
	}
	out := strings.Join(parts, " ")
	if len(out) > 600 {
		out = out[:600] + "…"
	}
	return out
}

func redisSyntaxArg(arg map[string]any) string {
	flags := map[string]bool{}
	for _, f := range redisStringList(arg["flags"]) {
		flags[f] = true
	}
	token := redisText(arg["token"])
	var core string
	switch redisText(arg["type"]) {
	case "pure-token":
		core, token = token, ""
	case "oneof":
		options := []string{}
		for _, a := range redisSlice(arg["arguments"]) {
			options = append(options, redisSyntaxArg(redisPairs(a)))
		}
		core = strings.Join(options, " | ")
		if !flags["optional"] {
			core = "<" + core + ">"
		}
	case "block":
		core = redisSyntax(redisSlice(arg["arguments"]))
	default:
		core = redisText(arg["display_text"])
		if core == "" {
			core = redisText(arg["name"])
		}
	}
	if token != "" {
		core = token + " " + core
	}
	if flags["multiple"] {
		core = core + " [" + core + " ...]"
	}
	if flags["optional"] {
		core = "[" + core + "]"
	}
	return core
}
