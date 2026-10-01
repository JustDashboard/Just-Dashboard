package dbx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// "Redis" on a connection row is a protocol, not a product. The same driver
// reaches Redis, Valkey, KeyDB and Dragonfly, across a decade of versions, and
// they do not agree on what exists: Dragonfly has no OBJECT, KeyDB has no
// COMMAND DOCS or per-field expiry, Redis 5 has no SCAN … TYPE. A page that
// sends a command the server lacks gets an error where a fact should have
// been, so everything optional is asked about first and left out — as an
// absent field, never a failed request — where the server cannot answer.

// Flavour ids, shared with the rest of the dashboard.
const (
	RedisFlavorRedis     = "redis"
	RedisFlavorValkey    = "valkey"
	RedisFlavorKeyDB     = "keydb"
	RedisFlavorDragonfly = "dragonfly"
)

// RedisInfoSection is one "# Section" of INFO with its fields in the order
// the server printed them.
type RedisInfoSection struct {
	Name   string           `json:"name"`
	Fields []RedisInfoField `json:"fields"`
}

type RedisInfoField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// parseRedisInfo splits INFO into its sections. A field before any heading —
// which some servers print for a single-section request — lands in a section
// with an empty name rather than being dropped.
func parseRedisInfo(raw string) []RedisInfoSection {
	var out []RedisInfoSection
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			out = append(out, RedisInfoSection{
				Name: strings.TrimSpace(strings.TrimPrefix(line, "#")), Fields: []RedisInfoField{},
			})
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		if len(out) == 0 {
			out = append(out, RedisInfoSection{Fields: []RedisInfoField{}})
		}
		last := &out[len(out)-1]
		last.Fields = append(last.Fields, RedisInfoField{Name: line[:colon], Value: line[colon+1:]})
	}
	return out
}

// redisInfoMap flattens sections for lookups by field name. The first value
// wins, which only matters to a server that repeats a name across sections.
func redisInfoMap(sections []RedisInfoSection) map[string]string {
	out := map[string]string{}
	for _, s := range sections {
		for _, f := range s.Fields {
			if _, seen := out[f.Name]; !seen {
				out[f.Name] = f.Value
			}
		}
	}
	return out
}

// RedisFlavorFromInfo names the product behind the protocol from what INFO
// says about itself. Valkey and Dragonfly both report a redis_version for the
// sake of old clients, so that field identifies nothing; their own do.
func RedisFlavorFromInfo(info map[string]string) (flavor, version string) {
	switch {
	case info["dragonfly_version"] != "":
		return RedisFlavorDragonfly, strings.TrimPrefix(info["dragonfly_version"], "df-v")
	case info["valkey_version"] != "":
		return RedisFlavorValkey, info["valkey_version"]
	case strings.EqualFold(info["server_name"], "valkey"):
		return RedisFlavorValkey, info["redis_version"]
	case info["mvcc_depth"] != "", strings.Contains(strings.ToLower(info["executable"]), "keydb"):
		return RedisFlavorKeyDB, info["redis_version"]
	}
	return RedisFlavorRedis, info["redis_version"]
}

// RedisModule is one loaded module.
type RedisModule struct {
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

// RedisFeatures is what this particular server can do. The page draws from
// it, so a control is absent on a server that lacks the command rather than
// present and failing.
type RedisFeatures struct {
	// Databases is false where there is one keyspace and SELECT is refused:
	// a cluster node.
	Databases      bool `json:"databases"`
	ScanType       bool `json:"scanType"`
	MemoryUsage    bool `json:"memoryUsage"`
	ObjectEncoding bool `json:"objectEncoding"`
	// Idle time and access frequency are alternatives: the server tracks one
	// or the other, depending on its eviction policy.
	ObjectIdleTime bool `json:"objectIdleTime"`
	ObjectFreq     bool `json:"objectFreq"`
	Unlink         bool `json:"unlink"`
	Copy           bool `json:"copy"`
	KeepTTL        bool `json:"keepTtl"`
	HashFieldTTL   bool `json:"hashFieldTtl"`
	Streams        bool `json:"streams"`
	JSON           bool `json:"json"`
	Search         bool `json:"search"`
	TimeSeries     bool `json:"timeSeries"`
	CommandDocs    bool `json:"commandDocs"`
	Slowlog        bool `json:"slowlog"`
	Latency        bool `json:"latency"`
	Config         bool `json:"config"`
	ACL            bool `json:"acl"`
	ClientList     bool `json:"clientList"`
	Monitor        bool `json:"monitor"`
	PubSub         bool `json:"pubsub"`
	Functions      bool `json:"functions"`
	AOF            bool `json:"aof"`
}

// RedisProfile is what one server said it is.
type RedisProfile struct {
	Flavor string `json:"flavor"`
	// Version is the product's own; RedisVersion is the Redis release it says
	// it is compatible with, which is what option-level decisions go by.
	Version      string        `json:"version"`
	RedisVersion string        `json:"redisVersion"`
	Mode         string        `json:"mode"`
	Features     RedisFeatures `json:"features"`
	Modules      []RedisModule `json:"modules"`

	compat [2]int
	// known holds the commands the server confirmed or denied. It is nil when
	// COMMAND INFO itself was refused — a managed service that renamed it —
	// and the version then stands in.
	known  map[string]bool
	policy string
}

// redisProbed is every command some feature depends on. They are asked about
// in one COMMAND INFO, which answers nil for a name the server does not have.
var redisProbed = []string{
	"object", "memory", "unlink", "copy", "httl", "xadd", "json.get", "ft._list",
	"ts.get", "latency", "slowlog", "config", "acl", "client", "monitor",
	"psubscribe", "function", "bgrewriteaof",
}

// RedisProbe asks a server what it is and what it has. One pipeline, three
// commands; the reply decides every optional command for the rest of the
// request.
func RedisProbe(ctx context.Context, client *redis.Client) (*RedisProfile, error) {
	args := make([]any, 0, len(redisProbed)+2)
	args = append(args, "COMMAND", "INFO")
	for _, name := range redisProbed {
		args = append(args, name)
	}
	pipe := client.Pipeline()
	// One section at a time: INFO with several arguments is Redis 7, and the
	// servers that most need describing are older than that.
	server := pipe.Info(ctx, "server")
	memory := pipe.Info(ctx, "memory")
	commands := pipe.Do(ctx, args...)
	modules := pipe.Do(ctx, "MODULE", "LIST")
	_, _ = pipe.Exec(ctx)
	if err := server.Err(); err != nil {
		return nil, err
	}
	info := redisInfoMap(parseRedisInfo(server.Val()))
	p := &RedisProfile{Mode: "standalone", Modules: []RedisModule{}}
	p.Flavor, p.Version = RedisFlavorFromInfo(info)
	p.RedisVersion = info["redis_version"]
	p.compat = redisVersionNumbers(p.RedisVersion)
	if mode := info["redis_mode"]; mode != "" {
		p.Mode = mode
	}
	if memory.Err() == nil {
		p.policy = redisInfoMap(parseRedisInfo(memory.Val()))["maxmemory_policy"]
	}
	if list, err := commands.Slice(); err == nil && len(list) == len(redisProbed) {
		p.known = map[string]bool{}
		for i, entry := range list {
			p.known[redisProbed[i]] = entry != nil
		}
	}
	if list, err := modules.Slice(); err == nil {
		for _, m := range list {
			fields := redisPairs(m)
			mod := RedisModule{Name: redisText(fields["name"])}
			mod.Version, _ = redisInt(fields["ver"])
			if mod.Name != "" {
				p.Modules = append(p.Modules, mod)
			}
		}
		sort.Slice(p.Modules, func(i, j int) bool { return p.Modules[i].Name < p.Modules[j].Name })
	}
	p.Features = p.features()
	return p, nil
}

// redisVersionNumbers reads "7.4.11" as {7, 4}. An unreadable version is
// {0, 0}, which fails every "at least" test — the conservative answer.
func redisVersionNumbers(v string) [2]int {
	var out [2]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i > 1 {
			break
		}
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return [2]int{}
		}
		out[i] = n
	}
	return out
}

func (p *RedisProfile) atLeast(major, minor int) bool {
	return p.compat[0] > major || (p.compat[0] == major && p.compat[1] >= minor)
}

// has reports whether the server has a command: by its own answer where it
// gave one, and otherwise by the release that introduced it.
func (p *RedisProfile) has(name string, major, minor int) bool {
	if p.known != nil {
		return p.known[name]
	}
	return major >= 0 && p.atLeast(major, minor)
}

func (p *RedisProfile) features() RedisFeatures {
	lfu := strings.Contains(p.policy, "lfu")
	object := p.has("object", 2, 2)
	f := RedisFeatures{
		Databases:      p.Mode == "standalone",
		ScanType:       p.atLeast(6, 0),
		MemoryUsage:    p.has("memory", 4, 0),
		ObjectEncoding: object,
		ObjectIdleTime: object && !lfu,
		ObjectFreq:     object && lfu && p.atLeast(4, 0),
		Unlink:         p.has("unlink", 4, 0),
		Copy:           p.has("copy", 6, 2),
		KeepTTL:        p.atLeast(6, 0),
		HashFieldTTL:   p.has("httl", 7, 4),
		Streams:        p.has("xadd", 5, 0),
		// A module the version cannot vouch for: without the server's own
		// answer it is taken to be absent.
		JSON:        p.has("json.get", -1, 0),
		Search:      p.has("ft._list", -1, 0),
		TimeSeries:  p.has("ts.get", -1, 0),
		CommandDocs: p.atLeast(7, 0),
		Slowlog:     p.has("slowlog", 2, 2),
		Latency:     p.has("latency", 2, 8),
		Config:      p.has("config", 2, 0),
		ACL:         p.has("acl", 6, 0),
		ClientList:  p.has("client", 2, 4),
		Monitor:     p.has("monitor", 1, 0),
		PubSub:      p.has("psubscribe", 2, 0),
		Functions:   p.has("function", 7, 0),
		AOF:         p.has("bgrewriteaof", 1, 0),
	}
	switch p.Flavor {
	case RedisFlavorDragonfly:
		// Dragonfly answers to a Redis 7 version number and is not Redis 7:
		// COMMAND DOCS is a syntax error, there is no append-only file, and
		// its HTTL is its own command with other arguments.
		f.CommandDocs, f.AOF, f.HashFieldTTL = false, false, false
	case RedisFlavorKeyDB:
		f.HashFieldTTL = false
	}
	if p.Mode == "sentinel" {
		// A sentinel holds no keys. What is left is what it answers about
		// itself.
		f = RedisFeatures{Config: f.Config, ClientList: f.ClientList, PubSub: f.PubSub, ACL: f.ACL}
	}
	return f
}

// ErrRedisSentinel is returned for a key operation against a sentinel.
var ErrRedisSentinel = errors.New("this is a Redis Sentinel endpoint: it watches other servers and holds no keys. Connect to the master it reports instead")

// RedisExplainError turns the two refusals that mean "this is not a single
// server" into sentences. A cluster node answers MOVED for a key it does not
// own and a sentinel answers "unknown command" for nearly everything, and
// neither says what the operator needs to hear: the dashboard talks to one
// node, and this is the wrong one for that.
func RedisExplainError(ctx context.Context, client *redis.Client, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "MOVED "), strings.HasPrefix(msg, "ASK "):
		fields := strings.Fields(msg)
		where := "another node"
		if len(fields) >= 3 {
			where = fields[2]
		}
		return fmt.Errorf("this server is one node of a Redis Cluster and that key lives on %s; the dashboard talks to a single node, so connect to that one to reach it", where)
	case strings.HasPrefix(msg, "CLUSTERDOWN"):
		return fmt.Errorf("this server is a Redis Cluster node and the cluster is not serving that slot (%s)", msg)
	case strings.HasPrefix(msg, "CROSSSLOT"):
		return fmt.Errorf("this server is a Redis Cluster node and those keys hash to different slots, so one command cannot touch them together")
	case strings.Contains(msg, "unknown command"):
		raw, ierr := client.Info(ctx, "server").Result()
		if ierr == nil && redisInfoMap(parseRedisInfo(raw))["redis_mode"] == "sentinel" {
			return ErrRedisSentinel
		}
	}
	return err
}

// --- reading generic replies ------------------------------------------------
//
// Several of the commands here answer in a shape that changed between
// protocol versions and between flavours: a map under RESP3, a flat list of
// alternating names and values under RESP2. These read either.

// redisPairs reads a reply that is conceptually a map.
func redisPairs(v any) map[string]any {
	out := map[string]any{}
	switch x := v.(type) {
	case map[any]any:
		for k, val := range x {
			out[redisText(k)] = val
		}
	case []any:
		for i := 0; i+1 < len(x); i += 2 {
			out[redisText(x[i])] = x[i+1]
		}
	}
	return out
}

// redisText reads a scalar reply as text.
func redisText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	case error:
		return x.Error()
	}
	return fmt.Sprint(v)
}

// redisInt reads a scalar reply as an integer, accepting the decimal strings
// RESP2 servers send where RESP3 ones send numbers.
func redisInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if ferr != nil {
				return 0, false
			}
			return int64(f), true
		}
		return n, true
	}
	return 0, false
}

func redisSlice(v any) []any {
	if x, ok := v.([]any); ok {
		return x
	}
	return nil
}

// redisStringList reads a reply that is a list of names.
func redisStringList(v any) []string {
	list := redisSlice(v)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, redisText(item))
	}
	return out
}
