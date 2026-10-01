package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The keyspace as a whole: its namespaces, and operations on every key a
// pattern matches. Both are a SCAN that somebody has to stop. A keyspace has
// no size limit and a request has a deadline, so each of these walks a bounded
// number of keys, says whether that was all of them, and hands back the cursor
// for whoever wants the rest.

// RedisTreeOptions is one request for a level of the namespace tree.
type RedisTreeOptions struct {
	// Delimiter separates the segments of a key name; ":" unless given.
	Delimiter string
	// Prefix is the namespace to look inside, with its trailing delimiter;
	// empty is the top.
	Prefix RedisBytes
	// Pattern and Type narrow the keys counted, as they do in a listing.
	Pattern string
	Type    string
	// Limit is how many keys one call may examine.
	Limit  int
	Cursor uint64
	// Depth is how many levels of namespaces to describe below Prefix.
	Depth int
	// Leaves is how many of the keys sitting directly under Prefix to list.
	Leaves int
}

// RedisTreeFolder is one namespace: every key that shares a prefix.
type RedisTreeFolder struct {
	// Name is the segment; Prefix is the whole path with its trailing
	// delimiter, which is what to ask for to look inside.
	Name   RedisBytes `json:"name"`
	Prefix RedisBytes `json:"prefix"`
	// Pattern lists the namespace's keys when given to a scan. It is not
	// Prefix with a star: a namespace called cache[v2] has to be escaped.
	Pattern RedisBytes `json:"pattern"`
	// Count is every key under the prefix, KeyCount those directly under it,
	// Folders the namespaces directly under it.
	Count    int64            `json:"count"`
	KeyCount int64            `json:"keyCount"`
	Folders  int64            `json:"folders"`
	Types    map[string]int64 `json:"types"`
	// Children are the namespaces below, when more than one level was asked
	// for.
	Children        []RedisTreeFolder `json:"children,omitempty"`
	ChildrenOmitted int               `json:"childrenOmitted,omitempty"`
}

// RedisTree is one level of the keyspace, grouped by delimiter.
//
// Every count in it is a count of the keys this call examined. Complete says
// whether that was the whole keyspace; when it is not, Cursor continues the
// walk and the caller adds the next answer to this one.
type RedisTree struct {
	DB        int        `json:"db"`
	Delimiter string     `json:"delimiter"`
	Prefix    RedisBytes `json:"prefix"`
	// Folders are the namespaces directly under Prefix, largest first.
	Folders        []RedisTreeFolder `json:"folders"`
	FoldersOmitted int               `json:"foldersOmitted,omitempty"`
	// Keys are those directly under Prefix: the first Leaves of KeyCount.
	Keys     []RedisKey `json:"keys"`
	KeyCount int64      `json:"keyCount"`
	// Count is every examined key under Prefix, and Types the same by type.
	Count int64            `json:"count"`
	Types map[string]int64 `json:"types"`
	// Scanned is how many keys the server handed over for this call.
	Scanned  int64  `json:"scanned"`
	Cursor   uint64 `json:"cursor,string"`
	Complete bool   `json:"complete"`
	// Total is every key in the database.
	Total     int64 `json:"total"`
	ElapsedMs int64 `json:"elapsedMs"`
}

const (
	redisTreeDefaultLimit = 10000
	redisTreeMaxLimit     = 50000
	redisTreeMaxFolders   = 1000
	redisTreeMaxDepth     = 6
	redisTreeBudget       = 10 * time.Second
	redisScanBatch        = 1000
)

type redisTreeNode struct {
	count, keyCount int64
	types           map[string]int64
	children        map[string]*redisTreeNode
}

func newRedisTreeNode() *redisTreeNode {
	return &redisTreeNode{types: map[string]int64{}, children: map[string]*redisTreeNode{}}
}

// add files one key, given as its segments below the root, under the tree.
// Levels past depth are counted in their ancestors and not kept as nodes of
// their own, except for one more level of bare names — which is what lets a
// folder say whether it has anything inside to open.
func (n *redisTreeNode) add(segments []string, typ string, depth int) {
	n.count++
	n.types[typ]++
	if len(segments) == 1 {
		n.keyCount++
		return
	}
	child := n.children[segments[0]]
	if child == nil {
		child = newRedisTreeNode()
		n.children[segments[0]] = child
	}
	if depth <= 0 {
		return
	}
	child.add(segments[1:], typ, depth-1)
}

func (n *redisTreeNode) folders(prefix, delimiter string, depth int) ([]RedisTreeFolder, int) {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := n.children[names[i]], n.children[names[j]]
		if a.count != b.count {
			return a.count > b.count
		}
		return names[i] < names[j]
	})
	omitted := 0
	if len(names) > redisTreeMaxFolders {
		omitted = len(names) - redisTreeMaxFolders
		names = names[:redisTreeMaxFolders]
	}
	out := make([]RedisTreeFolder, 0, len(names))
	for _, name := range names {
		child := n.children[name]
		path := prefix + name + delimiter
		folder := RedisTreeFolder{
			Name: RedisBytes(name), Prefix: RedisBytes(path),
			Pattern: RedisBytes(redisGlobEscape(path) + "*"),
			Count:   child.count, KeyCount: child.keyCount,
			Folders: int64(len(child.children)), Types: child.types,
		}
		if depth > 1 {
			folder.Children, folder.ChildrenOmitted = child.folders(path, delimiter, depth-1)
		}
		out = append(out, folder)
	}
	return out, omitted
}

// RedisKeyTree groups a bounded scan of the keyspace into namespaces.
func RedisKeyTree(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisTreeOptions) (*RedisTree, error) {
	started := time.Now()
	if o.Delimiter == "" {
		o.Delimiter = ":"
	}
	if len(o.Delimiter) > 8 {
		return nil, fmt.Errorf("a delimiter is at most 8 bytes")
	}
	if o.Type != "" && !redisTypeRe.MatchString(o.Type) {
		return nil, fmt.Errorf("%q is not a Redis type name", o.Type)
	}
	if o.Limit <= 0 {
		o.Limit = redisTreeDefaultLimit
	}
	if o.Limit > redisTreeMaxLimit {
		o.Limit = redisTreeMaxLimit
	}
	if o.Depth <= 0 {
		o.Depth = 1
	}
	if o.Depth > redisTreeMaxDepth {
		o.Depth = redisTreeMaxDepth
	}
	if o.Leaves <= 0 {
		o.Leaves = 200
	}
	if o.Leaves > redisMaxScanCount {
		o.Leaves = redisMaxScanCount
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

	prefix := string(o.Prefix)
	// The server does whichever filter is the narrower to ask for; the other
	// is applied here. With no pattern the prefix itself is the MATCH.
	match := o.Pattern
	if match == "" || match == "*" {
		match = "*"
		if prefix != "" {
			match = redisGlobEscape(prefix) + "*"
		}
	}
	serverType := ""
	if o.Type != "" && profile.Features.ScanType {
		serverType = o.Type
	}

	root := newRedisTreeNode()
	tree := &RedisTree{
		DB: client.Options().DB, Delimiter: o.Delimiter, Prefix: o.Prefix,
		Keys: []RedisKey{}, Cursor: o.Cursor,
	}
	var leaves []string
	next := o.Cursor
	for {
		var (
			batch []string
			err   error
		)
		if serverType != "" {
			batch, next, err = client.ScanType(ctx, next, match, redisScanBatch, serverType).Result()
		} else {
			batch, next, err = client.Scan(ctx, next, match, redisScanBatch).Result()
		}
		if err != nil {
			return nil, RedisExplainError(ctx, client, err)
		}
		tree.Scanned += int64(len(batch))
		kept := batch[:0:0]
		for _, k := range batch {
			if strings.HasPrefix(k, prefix) {
				kept = append(kept, k)
			}
		}
		types, err := redisTypes(ctx, client, kept, serverType)
		if err != nil {
			return nil, err
		}
		for i, k := range kept {
			typ := types[i]
			if typ == "none" || typ == "" || (o.Type != "" && typ != o.Type) {
				continue
			}
			segments := strings.Split(k[len(prefix):], o.Delimiter)
			if len(segments) == 1 && len(leaves) < o.Leaves {
				leaves = append(leaves, k)
			}
			root.add(segments, typ, o.Depth)
		}
		if next == 0 || tree.Scanned >= int64(o.Limit) || time.Since(started) > redisTreeBudget {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	tree.Cursor, tree.Complete = next, next == 0
	tree.Count, tree.KeyCount, tree.Types = root.count, root.keyCount, root.types
	tree.Folders, tree.FoldersOmitted = root.folders(prefix, o.Delimiter, o.Depth)

	// SCAN hands keys back in hash order. Sorted, the same folder opens on the
	// same names twice running.
	sort.Strings(leaves)
	described, err := redisDescribeKeys(ctx, client, profile, leaves, false)
	if err != nil {
		return nil, err
	}
	if described != nil {
		tree.Keys = described
	}
	tree.Total, _ = client.DBSize(ctx).Result()
	tree.ElapsedMs = time.Since(started).Milliseconds()
	return tree, nil
}

// redisTypes asks the type of each key in one pipeline. When the scan was
// already filtered by type on the server there is nothing to ask.
func redisTypes(ctx context.Context, client *redis.Client, keys []string, known string) ([]string, error) {
	out := make([]string, len(keys))
	if known != "" {
		for i := range out {
			out[i] = known
		}
		return out, nil
	}
	if len(keys) == 0 {
		return out, nil
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.StatusCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Type(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && cmds[0].Err() != nil {
		return nil, RedisExplainError(ctx, client, cmds[0].Err())
	}
	for i, c := range cmds {
		out[i] = c.Val()
	}
	return out, nil
}

// RedisBulkOptions is one operation over every key a pattern matches.
type RedisBulkOptions struct {
	Pattern string
	// Type narrows the match to one type.
	Type string
	// Action is delete, expire or persist.
	Action string
	// TTL in seconds, for expire.
	TTL int64
	// DryRun counts what would be touched and touches nothing.
	DryRun bool
	Cursor uint64
	// Limit is how many matching keys one call may act on.
	Limit int
}

// RedisBulkResult is what one call did, or would have done.
type RedisBulkResult struct {
	Action  string `json:"action"`
	DryRun  bool   `json:"dryRun"`
	DB      int    `json:"db"`
	Pattern string `json:"pattern"`
	Type    string `json:"type,omitempty"`
	// Matched is how many keys the scan found; Affected is how many were
	// actually changed, which is zero on a dry run and may be lower than
	// Matched when a key expired between being found and being touched.
	Matched  int64 `json:"matched"`
	Affected int64 `json:"affected"`
	// Complete says the scan reached the end of the keyspace. When it is
	// false Matched is a floor, not a total, and Cursor continues from here.
	Complete bool   `json:"complete"`
	Cursor   uint64 `json:"cursor,string"`
	// Sample is the first few names found, so a confirmation can show what is
	// about to go rather than only how much.
	Sample []RedisBytes `json:"sample"`
	// Total is every key in the database, before anything was removed.
	Total     int64 `json:"total"`
	ElapsedMs int64 `json:"elapsedMs"`
}

const (
	redisBulkDefaultLimit = 100000
	redisBulkMaxLimit     = 1000000
	redisBulkBudget       = 20 * time.Second
	redisBulkBatch        = 500
	redisBulkSample       = 20
)

// RedisBulkActions are the operations RedisBulk performs.
var RedisBulkActions = map[string]bool{"delete": true, "expire": true, "persist": true}

// RedisBulk applies one action to the keys matching a pattern, a batch at a
// time as the scan finds them.
//
// A delete is UNLINK in batches of a few hundred: never KEYS to find them,
// never one DEL of a million names, both of which stop the server for
// everybody else while they run.
func RedisBulk(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisBulkOptions) (*RedisBulkResult, error) {
	started := time.Now()
	if !RedisBulkActions[o.Action] {
		return nil, fmt.Errorf("action must be delete, expire or persist")
	}
	if o.Pattern == "" {
		return nil, fmt.Errorf("a pattern is required; use * to mean every key")
	}
	if len(o.Pattern) > 1024 {
		return nil, fmt.Errorf("the pattern is too long")
	}
	if o.Type != "" && !redisTypeRe.MatchString(o.Type) {
		return nil, fmt.Errorf("%q is not a Redis type name", o.Type)
	}
	if o.Action == "expire" && o.TTL <= 0 {
		return nil, fmt.Errorf("an expiry of at least one second is required")
	}
	if o.Limit <= 0 {
		o.Limit = redisBulkDefaultLimit
	}
	if o.Limit > redisBulkMaxLimit {
		o.Limit = redisBulkMaxLimit
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
	serverType := ""
	if o.Type != "" && profile.Features.ScanType {
		serverType = o.Type
	}
	res := &RedisBulkResult{
		Action: o.Action, DryRun: o.DryRun, DB: client.Options().DB,
		Pattern: o.Pattern, Type: o.Type, Sample: []RedisBytes{}, Cursor: o.Cursor,
	}
	res.Total, _ = client.DBSize(ctx).Result()
	next := o.Cursor
	for {
		var (
			batch []string
			err   error
		)
		if serverType != "" {
			batch, next, err = client.ScanType(ctx, next, o.Pattern, redisScanBatch, serverType).Result()
		} else {
			batch, next, err = client.Scan(ctx, next, o.Pattern, redisScanBatch).Result()
		}
		if err != nil {
			return res, RedisExplainError(ctx, client, err)
		}
		if o.Type != "" && serverType == "" {
			types, err := redisTypes(ctx, client, batch, "")
			if err != nil {
				return res, err
			}
			kept := batch[:0]
			for i, k := range batch {
				if types[i] == o.Type {
					kept = append(kept, k)
				}
			}
			batch = kept
		}
		res.Matched += int64(len(batch))
		for _, k := range batch {
			if len(res.Sample) >= redisBulkSample {
				break
			}
			res.Sample = append(res.Sample, RedisBytes(k))
		}
		if !o.DryRun {
			for start := 0; start < len(batch); start += redisBulkBatch {
				end := start + redisBulkBatch
				if end > len(batch) {
					end = len(batch)
				}
				n, err := redisBulkApply(ctx, client, profile, o, batch[start:end])
				res.Affected += n
				if err != nil {
					// What was done stays done and is reported with the error:
					// a bulk delete that failed half way has still deleted.
					res.Cursor = next
					res.ElapsedMs = time.Since(started).Milliseconds()
					return res, RedisExplainError(ctx, client, err)
				}
			}
		}
		if next == 0 || res.Matched >= int64(o.Limit) || time.Since(started) > redisBulkBudget {
			break
		}
		if err := ctx.Err(); err != nil {
			res.Cursor = next
			return res, err
		}
	}
	res.Cursor, res.Complete = next, next == 0
	res.ElapsedMs = time.Since(started).Milliseconds()
	return res, nil
}

func redisBulkApply(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisBulkOptions, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	if o.Action == "delete" {
		return redisUnlink(ctx, client, profile, keys)
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.BoolCmd, len(keys))
	for i, k := range keys {
		if o.Action == "expire" {
			cmds[i] = pipe.Expire(ctx, k, time.Duration(o.TTL)*time.Second)
		} else {
			cmds[i] = pipe.Persist(ctx, k)
		}
	}
	_, err := pipe.Exec(ctx)
	var changed int64
	for _, c := range cmds {
		if c.Val() {
			changed++
		}
	}
	return changed, err
}
