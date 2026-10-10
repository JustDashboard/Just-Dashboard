package dbx

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Where the memory went.
//
// The server knows how much memory it uses and nothing about what uses it.
// That question is answered by looking at keys, and looking at all of them is
// not an option on a keyspace worth asking about. So this takes a sample: the
// first so-many keys SCAN hands over — which arrive in hash order, and are
// therefore a fair draw with respect to their names — measured with one
// pipeline per batch. Whatever the sample did not cover is extrapolated, and
// the report says how much that was.

// RedisAnalysisOptions is one analysis request.
type RedisAnalysisOptions struct {
	// Sample is how many keys to measure.
	Sample int
	// Delimiter separates a key's namespace from the rest of its name.
	Delimiter string
	// Top is how many of the largest keys to name.
	Top int
}

// RedisAnalysisGroup is one slice of the sample — a type, a namespace, an
// expiry band, an encoding — with what it measured and what that scales to.
type RedisAnalysisGroup struct {
	Name   RedisBytes `json:"name"`
	Keys   int64      `json:"keys"`
	Memory int64      `json:"memory"`
	// The estimates are the measured figures scaled to the whole database.
	// They equal them when the sample was the whole database.
	EstimatedKeys   int64 `json:"estimatedKeys"`
	EstimatedMemory int64 `json:"estimatedMemory"`
}

// RedisAnalysisKey is one of the largest keys found.
type RedisAnalysisKey struct {
	Key    RedisBytes `json:"key"`
	Type   string     `json:"type"`
	Memory int64      `json:"memory"`
	// TTL in seconds; -1 means it never expires.
	TTL int64 `json:"ttl"`
	// Size is its length or cardinality.
	Size     int64  `json:"size"`
	Encoding string `json:"encoding,omitempty"`
}

// RedisAnalysis is a sampled account of what one logical database holds.
type RedisAnalysis struct {
	DB          int    `json:"db"`
	Delimiter   string `json:"delimiter"`
	SampledAtMs int64  `json:"sampledAtMs"`
	ElapsedMs   int64  `json:"elapsedMs"`
	// Total is every key in the database; Sampled is how many were measured.
	Total   int64 `json:"total"`
	Sampled int64 `json:"sampled"`
	// Complete says the sample was the whole database, so nothing here is an
	// estimate. Otherwise Scale is what the measured figures were multiplied
	// by, and TimedOut says the sample is smaller than asked for because the
	// time ran out.
	Complete bool    `json:"complete"`
	Scale    float64 `json:"scale"`
	TimedOut bool    `json:"timedOut"`
	// Memory is the sample's own bytes and EstimatedMemory the whole
	// database's. UsedMemory is what the server reports for itself, which
	// also covers every other database and its own overhead.
	Memory          int64 `json:"memory"`
	EstimatedMemory int64 `json:"estimatedMemory"`
	UsedMemory      int64 `json:"usedMemory"`
	// Types is by key type, Namespaces by the part of the name before the
	// first delimiter (the empty name is the keys with none), both largest
	// first.
	Types             []RedisAnalysisGroup `json:"types"`
	Namespaces        []RedisAnalysisGroup `json:"namespaces"`
	NamespacesOmitted int                  `json:"namespacesOmitted,omitempty"`
	TopKeys           []RedisAnalysisKey   `json:"topKeys"`
	// Expiry is always the same five bands in the same order: none, hour,
	// day, week, later — keys with no expiry, and keys due within an hour, a
	// day, seven days, or after that.
	Expiry []RedisAnalysisGroup `json:"expiry"`
	// Encodings is by "type:encoding", so a hash that outgrew its compact
	// form shows up as its own line.
	Encodings []RedisAnalysisGroup `json:"encodings"`
	// Unavailable maps "memory" and "encodings" to the reason either is
	// missing on this server.
	Unavailable map[string]string `json:"unavailable,omitempty"`
}

const (
	redisAnalysisDefault = 10000
	redisAnalysisMax     = 50000
	redisAnalysisBudget  = 25 * time.Second
	redisAnalysisGroups  = 100
)

var redisExpiryBands = []string{"none", "hour", "day", "week", "later"}

func redisExpiryBand(pttl int64) string {
	switch {
	case pttl < 0:
		return "none"
	case pttl < int64(time.Hour/time.Millisecond):
		return "hour"
	case pttl < int64(24*time.Hour/time.Millisecond):
		return "day"
	case pttl < int64(7*24*time.Hour/time.Millisecond):
		return "week"
	}
	return "later"
}

type redisTally struct{ keys, memory int64 }

// redisTopKeys is a min-heap on memory, so the smallest of the keys kept is
// the one a larger newcomer displaces.
type redisTopKeys []RedisAnalysisKey

func (h redisTopKeys) Len() int           { return len(h) }
func (h redisTopKeys) Less(i, j int) bool { return h[i].Memory < h[j].Memory }
func (h redisTopKeys) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *redisTopKeys) Push(x any)        { *h = append(*h, x.(RedisAnalysisKey)) }
func (h *redisTopKeys) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// RedisAnalyze samples one logical database and accounts for its memory.
//
// It stops at the sample size, at the end of the keyspace, when its time is
// up, or when ctx is cancelled — whichever comes first.
func RedisAnalyze(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisAnalysisOptions) (*RedisAnalysis, error) {
	started := time.Now()
	if o.Sample <= 0 {
		o.Sample = redisAnalysisDefault
	}
	if o.Sample > redisAnalysisMax {
		o.Sample = redisAnalysisMax
	}
	if o.Delimiter == "" {
		o.Delimiter = ":"
	}
	if len(o.Delimiter) > 8 {
		return nil, fmt.Errorf("a delimiter is at most 8 bytes")
	}
	if o.Top <= 0 {
		o.Top = 50
	}
	if o.Top > 200 {
		o.Top = 200
	}
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	if profile.Mode == "sentinel" {
		return nil, ErrRedisSentinel
	}
	f := profile.Features
	out := &RedisAnalysis{
		DB: client.Options().DB, Delimiter: o.Delimiter, SampledAtMs: started.UnixMilli(),
		Unavailable: map[string]string{},
	}
	if !f.MemoryUsage {
		out.Unavailable["memory"] = redisProductName(profile) + " has no MEMORY USAGE, so this counts keys and cannot weigh them."
	}
	if !f.ObjectEncoding {
		out.Unavailable["encodings"] = redisProductName(profile) + " has no OBJECT ENCODING."
	}

	types, namespaces := map[string]*redisTally{}, map[string]*redisTally{}
	expiry, encodings := map[string]*redisTally{}, map[string]*redisTally{}
	tally := func(m map[string]*redisTally, name string, memory int64) {
		t := m[name]
		if t == nil {
			t = &redisTally{}
			m[name] = t
		}
		t.keys++
		t.memory += memory
	}
	top := &redisTopKeys{}
	var cursor uint64
	// cut records keys the scan handed over that the sample had no room for.
	// The scan may well have finished on that turn, and the sample still did
	// not cover them.
	cut := false
	for {
		batch, next, err := client.Scan(ctx, cursor, "*", redisScanBatch).Result()
		if err != nil {
			return nil, RedisExplainError(ctx, client, err)
		}
		cursor = next
		if room := o.Sample - int(out.Sampled); len(batch) > room {
			batch, cut = batch[:room], true
		}
		if len(batch) > 0 {
			pipe := client.Pipeline()
			typeCmds := make([]*redis.StatusCmd, len(batch))
			ttlCmds := make([]*redis.IntCmd, len(batch))
			memCmds := make([]*redis.IntCmd, len(batch))
			encCmds := make([]*redis.StringCmd, len(batch))
			for i, k := range batch {
				typeCmds[i] = pipe.Type(ctx, k)
				ttlCmds[i] = redisTTLCmd(ctx, pipe, "pttl", k)
				if f.MemoryUsage {
					memCmds[i] = pipe.MemoryUsage(ctx, k)
				}
				if f.ObjectEncoding {
					encCmds[i] = pipe.ObjectEncoding(ctx, k)
				}
			}
			// Best effort per key: one that expired since the scan answers
			// "none" and is left out of the sample.
			_, _ = pipe.Exec(ctx)
			for i, k := range batch {
				typ := typeCmds[i].Val()
				if typ == "none" || typ == "" {
					continue
				}
				var memory int64
				if memCmds[i] != nil {
					memory = memCmds[i].Val()
				}
				pttl := redisTTL(ttlCmds[i])
				encoding := ""
				if encCmds[i] != nil && encCmds[i].Err() == nil {
					encoding = encCmds[i].Val()
				}
				out.Sampled++
				out.Memory += memory
				tally(types, typ, memory)
				namespace, _, found := strings.Cut(k, o.Delimiter)
				if !found {
					namespace = ""
				}
				tally(namespaces, namespace, memory)
				tally(expiry, redisExpiryBand(pttl), memory)
				if encoding != "" {
					tally(encodings, typ+":"+encoding, memory)
				}
				if f.MemoryUsage {
					entry := RedisAnalysisKey{
						Key: RedisBytes(k), Type: typ, Memory: memory,
						TTL: redisSecondsFromMillis(pttl), Encoding: encoding,
					}
					if top.Len() < o.Top {
						heap.Push(top, entry)
					} else if memory > (*top)[0].Memory {
						(*top)[0] = entry
						heap.Fix(top, 0)
					}
				}
			}
		}
		if cursor == 0 || out.Sampled >= int64(o.Sample) {
			break
		}
		if time.Since(started) > redisAnalysisBudget {
			out.TimedOut = true
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	out.Complete = cursor == 0 && !cut

	pipe := client.Pipeline()
	sizeCmd := pipe.DBSize(ctx)
	infoCmd := pipe.Info(ctx, "memory")
	_, _ = pipe.Exec(ctx)
	out.Total = sizeCmd.Val()
	if infoCmd.Err() == nil {
		out.UsedMemory = redisInfoInt(redisInfoMap(parseRedisInfo(infoCmd.Val())), "used_memory")
	}
	out.Scale = 1
	if !out.Complete && out.Sampled > 0 && out.Total > out.Sampled {
		out.Scale = float64(out.Total) / float64(out.Sampled)
	}
	if out.Complete {
		// The scan saw every key, so its own count is the truth even if keys
		// came or went while it ran.
		out.Total = out.Sampled
	}
	scale := func(n int64) int64 { return int64(float64(n)*out.Scale + 0.5) }
	out.EstimatedMemory = scale(out.Memory)
	groups := func(m map[string]*redisTally, byMemory bool) []RedisAnalysisGroup {
		list := make([]RedisAnalysisGroup, 0, len(m))
		for name, t := range m {
			list = append(list, RedisAnalysisGroup{
				Name: RedisBytes(name), Keys: t.keys, Memory: t.memory,
				EstimatedKeys: scale(t.keys), EstimatedMemory: scale(t.memory),
			})
		}
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if byMemory && a.Memory != b.Memory {
				return a.Memory > b.Memory
			}
			if a.Keys != b.Keys {
				return a.Keys > b.Keys
			}
			return a.Name < b.Name
		})
		return list
	}
	out.Types = groups(types, f.MemoryUsage)
	out.Encodings = groups(encodings, f.MemoryUsage)
	out.Namespaces = groups(namespaces, f.MemoryUsage)
	if len(out.Namespaces) > redisAnalysisGroups {
		out.NamespacesOmitted = len(out.Namespaces) - redisAnalysisGroups
		out.Namespaces = out.Namespaces[:redisAnalysisGroups]
	}
	out.Expiry = make([]RedisAnalysisGroup, 0, len(redisExpiryBands))
	for _, band := range redisExpiryBands {
		g := RedisAnalysisGroup{Name: RedisBytes(band)}
		if t := expiry[band]; t != nil {
			g.Keys, g.Memory = t.keys, t.memory
			g.EstimatedKeys, g.EstimatedMemory = scale(t.keys), scale(t.memory)
		}
		out.Expiry = append(out.Expiry, g)
	}

	out.TopKeys = make([]RedisAnalysisKey, top.Len())
	for i := len(out.TopKeys) - 1; i >= 0; i-- {
		out.TopKeys[i] = heap.Pop(top).(RedisAnalysisKey)
	}
	if len(out.TopKeys) > 0 {
		sizes := client.Pipeline()
		cmds := make([]*redis.IntCmd, len(out.TopKeys))
		for i, k := range out.TopKeys {
			cmds[i] = redisLengthCmd(ctx, sizes, k.Type, string(k.Key))
		}
		_, _ = sizes.Exec(ctx)
		for i, c := range cmds {
			if c != nil {
				out.TopKeys[i].Size = c.Val()
			}
		}
	}
	if len(out.Unavailable) == 0 {
		out.Unavailable = nil
	}
	out.ElapsedMs = time.Since(started).Milliseconds()
	return out, nil
}
