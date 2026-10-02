package dbx

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// A stream is the one Redis type with readers attached. Its entries are only
// half of what an operator comes to look at; the other half is who is
// consuming them and how far behind they are — the consumer groups, their
// consumers, and the entries delivered but never acknowledged.

// RedisStreamInfo is a stream and everything reading from it.
type RedisStreamInfo struct {
	Key    RedisBytes `json:"key"`
	DB     int        `json:"db"`
	Length int64      `json:"length"`
	// FirstID and LastID are the oldest and newest entries still in the
	// stream; both are empty when it holds none.
	FirstID string `json:"firstId,omitempty"`
	LastID  string `json:"lastId,omitempty"`
	// LastGeneratedID is the highest id ever given out, which stays where it
	// was when the newest entry is deleted.
	LastGeneratedID string `json:"lastGeneratedId,omitempty"`
	// EntriesAdded counts every entry ever added, on a server that keeps it.
	EntriesAdded *int64             `json:"entriesAdded,omitempty"`
	Groups       []RedisStreamGroup `json:"groups"`
	// GroupsOmitted counts the groups past the first redisMaxStreamGroups.
	GroupsOmitted int `json:"groupsOmitted,omitempty"`
}

// RedisStreamGroup is one consumer group.
type RedisStreamGroup struct {
	Name RedisBytes `json:"name"`
	// LastDeliveredID is how far the group has read.
	LastDeliveredID string `json:"lastDeliveredId"`
	// Pending counts entries delivered to a consumer and not yet
	// acknowledged; the two ids bound them.
	Pending         int64  `json:"pending"`
	OldestPendingID string `json:"oldestPendingId,omitempty"`
	NewestPendingID string `json:"newestPendingId,omitempty"`
	// Lag is how many entries the group has not been delivered yet. The
	// server reports it from Redis 7 and cannot always compute it.
	Lag         *int64                `json:"lag,omitempty"`
	EntriesRead *int64                `json:"entriesRead,omitempty"`
	Consumers   []RedisStreamConsumer `json:"consumers"`
}

// RedisStreamConsumer is one named reader in a group.
type RedisStreamConsumer struct {
	Name    RedisBytes `json:"name"`
	Pending int64      `json:"pending"`
	// IdleMs is how long since the consumer last asked for anything.
	IdleMs int64 `json:"idleMs"`
}

// RedisStreamPending is one delivered, unacknowledged entry.
type RedisStreamPending struct {
	ID       string     `json:"id"`
	Consumer RedisBytes `json:"consumer"`
	// IdleMs is how long since it was last delivered.
	IdleMs     int64 `json:"idleMs"`
	Deliveries int64 `json:"deliveries"`
}

const redisMaxStreamGroups = 50

// RedisStreamDescribe reads a stream's bounds and its consumer groups.
func RedisStreamDescribe(ctx context.Context, client *redis.Client, key RedisBytes) (*RedisStreamInfo, error) {
	k := string(key)
	pipe := client.Pipeline()
	typeCmd := pipe.Type(ctx, k)
	infoCmd := pipe.Do(ctx, "XINFO", "STREAM", k)
	groupsCmd := pipe.Do(ctx, "XINFO", "GROUPS", k)
	_, _ = pipe.Exec(ctx)
	if err := typeCmd.Err(); err != nil {
		return nil, RedisExplainError(ctx, client, err)
	}
	switch typeCmd.Val() {
	case "none":
		return nil, ErrRedisKeyNotFound
	case "stream":
	default:
		return nil, fmt.Errorf("%q is a %s, not a stream", key.Display(), typeCmd.Val())
	}
	raw, err := infoCmd.Result()
	if err != nil {
		return nil, RedisExplainError(ctx, client, err)
	}
	fields := redisPairs(raw)
	out := &RedisStreamInfo{Key: key, DB: client.Options().DB, Groups: []RedisStreamGroup{}}
	out.Length, _ = redisInt(fields["length"])
	out.LastGeneratedID = redisText(fields["last-generated-id"])
	if n, ok := redisInt(fields["entries-added"]); ok {
		out.EntriesAdded = &n
	}
	if first := redisSlice(fields["first-entry"]); len(first) > 0 {
		out.FirstID = redisText(first[0])
	}
	if last := redisSlice(fields["last-entry"]); len(last) > 0 {
		out.LastID = redisText(last[0])
	}

	groups := redisSlice(groupsCmd.Val())
	if len(groups) > redisMaxStreamGroups {
		out.GroupsOmitted = len(groups) - redisMaxStreamGroups
		groups = groups[:redisMaxStreamGroups]
	}
	if len(groups) == 0 {
		return out, nil
	}
	detail := client.Pipeline()
	consumerCmds := make([]*redis.Cmd, len(groups))
	pendingCmds := make([]*redis.Cmd, len(groups))
	for i, g := range groups {
		gf := redisPairs(g)
		group := RedisStreamGroup{
			Name:            RedisBytes(redisText(gf["name"])),
			LastDeliveredID: redisText(gf["last-delivered-id"]),
			Consumers:       []RedisStreamConsumer{},
		}
		group.Pending, _ = redisInt(gf["pending"])
		if n, ok := redisInt(gf["lag"]); ok && gf["lag"] != nil {
			group.Lag = &n
		}
		if n, ok := redisInt(gf["entries-read"]); ok && gf["entries-read"] != nil {
			group.EntriesRead = &n
		}
		out.Groups = append(out.Groups, group)
		consumerCmds[i] = detail.Do(ctx, "XINFO", "CONSUMERS", k, string(group.Name))
		pendingCmds[i] = detail.Do(ctx, "XPENDING", k, string(group.Name))
	}
	// Best effort per group: one destroyed between the two pipelines answers
	// with an error for itself and leaves the others readable.
	_, _ = detail.Exec(ctx)
	for i := range out.Groups {
		for _, c := range redisSlice(consumerCmds[i].Val()) {
			cf := redisPairs(c)
			consumer := RedisStreamConsumer{Name: RedisBytes(redisText(cf["name"]))}
			consumer.Pending, _ = redisInt(cf["pending"])
			consumer.IdleMs, _ = redisInt(cf["idle"])
			out.Groups[i].Consumers = append(out.Groups[i].Consumers, consumer)
		}
		// XPENDING's summary form: count, lowest id, highest id, consumers.
		if summary := redisSlice(pendingCmds[i].Val()); len(summary) >= 3 {
			if n, ok := redisInt(summary[0]); ok {
				out.Groups[i].Pending = n
			}
			out.Groups[i].OldestPendingID = redisText(summary[1])
			out.Groups[i].NewestPendingID = redisText(summary[2])
		}
	}
	return out, nil
}

// RedisStreamPendingOptions is one page of a group's unacknowledged entries.
type RedisStreamPendingOptions struct {
	Key   RedisBytes
	Group RedisBytes
	// Consumer narrows the list to one consumer; empty is all of them.
	Consumer RedisBytes
	// Cursor is the id to start from; empty is the oldest.
	Cursor string
	Count  int
}

// RedisStreamPendingPage is that page.
type RedisStreamPendingPage struct {
	Entries []RedisStreamPending `json:"entries"`
	Cursor  string               `json:"cursor"`
	Done    bool                 `json:"done"`
}

// RedisStreamPendingEntries lists what a group was handed and has not
// acknowledged, oldest first.
func RedisStreamPendingEntries(ctx context.Context, client *redis.Client, o RedisStreamPendingOptions) (*RedisStreamPendingPage, error) {
	if o.Group == "" {
		return nil, fmt.Errorf("a consumer group is required")
	}
	if o.Count <= 0 {
		o.Count = redisDefaultPageRows
	}
	if o.Count > redisMaxPageRows {
		o.Count = redisMaxPageRows
	}
	start := "-"
	if o.Cursor != "" {
		if !redisStreamIDRe.MatchString(o.Cursor) {
			return nil, fmt.Errorf("invalid cursor")
		}
		start = o.Cursor
	}
	args := []any{"XPENDING", string(o.Key), string(o.Group), start, "+", o.Count}
	if o.Consumer != "" {
		args = append(args, string(o.Consumer))
	}
	raw, err := client.Do(ctx, args...).Result()
	if err != nil && err != redis.Nil {
		return nil, RedisExplainError(ctx, client, err)
	}
	page := &RedisStreamPendingPage{Entries: []RedisStreamPending{}, Cursor: "0", Done: true}
	for _, item := range redisSlice(raw) {
		entry := redisSlice(item)
		if len(entry) < 4 {
			continue
		}
		p := RedisStreamPending{ID: redisText(entry[0]), Consumer: RedisBytes(redisText(entry[1]))}
		p.IdleMs, _ = redisInt(entry[2])
		p.Deliveries, _ = redisInt(entry[3])
		page.Entries = append(page.Entries, p)
	}
	if len(page.Entries) == o.Count {
		if next, ok := redisStreamNeighbour(page.Entries[len(page.Entries)-1].ID, false); ok {
			page.Cursor, page.Done = next, false
		}
	}
	return page, nil
}

// RedisStreamTrim says how much of a stream to keep.
type RedisStreamTrim struct {
	Key RedisBytes
	// MaxLen keeps the newest that many entries. MinID drops everything older
	// than an id. Exactly one is given.
	MaxLen *int64
	MinID  string
	// Approximate lets the server stop at a node boundary, which is far
	// cheaper and leaves slightly more than was asked for.
	Approximate bool
}

// RedisStreamTrimEntries shortens a stream and reports how many entries went.
func RedisStreamTrimEntries(ctx context.Context, client *redis.Client, t RedisStreamTrim) (int64, error) {
	args := []any{"XTRIM", string(t.Key)}
	switch {
	case t.MaxLen != nil && t.MinID != "":
		return 0, fmt.Errorf("trim by length or by id, not both")
	case t.MaxLen != nil:
		if *t.MaxLen < 0 {
			return 0, fmt.Errorf("the length to keep cannot be negative")
		}
		args = append(args, "MAXLEN")
	case t.MinID != "":
		if !redisStreamIDRe.MatchString(t.MinID) {
			return 0, fmt.Errorf("%q is not a stream id", t.MinID)
		}
		args = append(args, "MINID")
	default:
		return 0, fmt.Errorf("say how much to keep: maxLen entries, or everything from minId on")
	}
	if t.Approximate {
		args = append(args, "~")
	}
	if t.MaxLen != nil {
		args = append(args, strconv.FormatInt(*t.MaxLen, 10))
	} else {
		args = append(args, t.MinID)
	}
	removed, err := client.Do(ctx, args...).Int64()
	if err != nil {
		return 0, RedisExplainError(ctx, client, err)
	}
	return removed, nil
}

// redisGroupStartID checks where a group should start reading: an entry id,
// "$" for only what arrives from now on, or "0" for the whole stream.
func redisGroupStartID(id string) (string, error) {
	if id == "" || id == "$" {
		return "$", nil
	}
	if !redisStreamIDRe.MatchString(id) {
		return "", fmt.Errorf("%q is not a stream id; use $ for new entries only or 0 for the whole stream", id)
	}
	return id, nil
}

// RedisStreamGroupCreate adds a consumer group reading from id.
func RedisStreamGroupCreate(ctx context.Context, client *redis.Client, key, group RedisBytes, id string) error {
	if group == "" {
		return fmt.Errorf("a consumer group needs a name")
	}
	start, err := redisGroupStartID(id)
	if err != nil {
		return err
	}
	// No MKSTREAM: creating a group must not conjure a stream out of a
	// mistyped key name.
	if err := client.Do(ctx, "XGROUP", "CREATE", string(key), string(group), start).Err(); err != nil {
		return RedisExplainError(ctx, client, err)
	}
	return nil
}

// RedisStreamGroupSetID moves a group's read position, which re-delivers or
// skips everything between the old position and the new.
func RedisStreamGroupSetID(ctx context.Context, client *redis.Client, key, group RedisBytes, id string) error {
	if group == "" {
		return fmt.Errorf("a consumer group is required")
	}
	start, err := redisGroupStartID(id)
	if err != nil {
		return err
	}
	if err := client.Do(ctx, "XGROUP", "SETID", string(key), string(group), start).Err(); err != nil {
		return RedisExplainError(ctx, client, err)
	}
	return nil
}

// RedisStreamGroupRemove destroys a group, or with a consumer named, removes
// that one consumer and whatever it had pending. It reports what went: groups
// destroyed, or the consumer's pending entries.
//
// The consumer is named by being given, not by what it is called: a consumer
// may be called "", and removing that one is not destroying its group.
func RedisStreamGroupRemove(ctx context.Context, client *redis.Client, key, group RedisBytes, consumer *RedisBytes) (int64, error) {
	if group == "" {
		return 0, fmt.Errorf("a consumer group is required")
	}
	args := []any{"XGROUP", "DESTROY", string(key), string(group)}
	if consumer != nil {
		args = []any{"XGROUP", "DELCONSUMER", string(key), string(group), string(*consumer)}
	}
	n, err := client.Do(ctx, args...).Int64()
	if err != nil {
		return 0, RedisExplainError(ctx, client, err)
	}
	return n, nil
}

// RedisStreamAck acknowledges entries on a group's behalf, taking them off
// its pending list.
func RedisStreamAck(ctx context.Context, client *redis.Client, key, group RedisBytes, ids []string) (int64, error) {
	if group == "" {
		return 0, fmt.Errorf("a consumer group is required")
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("name at least one entry id to acknowledge")
	}
	args := make([]any, 0, len(ids)+3)
	args = append(args, "XACK", string(key), string(group))
	for _, id := range ids {
		if !redisStreamIDRe.MatchString(id) {
			return 0, fmt.Errorf("an entry is acknowledged by its id, such as 1700000000000-0")
		}
		args = append(args, id)
	}
	n, err := client.Do(ctx, args...).Int64()
	if err != nil {
		return 0, RedisExplainError(ctx, client, err)
	}
	return n, nil
}

// RedisStreamClaim hands pending entries of a consumer group to one of its
// consumers: the entries named, or — Auto — whatever has been pending long
// enough, a page at a time.
type RedisStreamClaim struct {
	Key   RedisBytes
	Group RedisBytes
	// Consumer is who the entries go to. It is a pointer for the reason a
	// removal's is: a consumer may be named "", and that is a name.
	Consumer *RedisBytes
	IDs      []string
	// MinIdleMs keeps the claim to entries nobody has been handed for at
	// least that long, so one a consumer is working on is not taken from it.
	MinIdleMs int64
	Auto      bool
	// Cursor is where an automatic claim starts looking; empty is the oldest.
	Cursor string
	Count  int
}

// RedisStreamClaimed is what a claim moved.
type RedisStreamClaimed struct {
	// Claimed are the entries now pending for the consumer.
	Claimed []string `json:"claimed"`
	// Deleted are entries that were pending and are no longer in the stream.
	// The server drops them from the pending list as it finds them, and from
	// Redis 7 says which.
	Deleted []string `json:"deleted"`
	// Cursor continues an automatic claim, while Done is false.
	Cursor string `json:"cursor"`
	Done   bool   `json:"done"`
}

// ErrRedisNoAutoClaim is an automatic claim asked of a server without one.
var ErrRedisNoAutoClaim = errors.New("this server has no XAUTOCLAIM (it came with Redis 6.2); name the entries to claim instead")

// RedisStreamClaimEntries moves pending entries to a consumer.
//
// Only the ids travel, in both directions (JUSTID): what an entry holds is the
// application's, and a claim made from a form has no use for it. Claimed that
// way an entry's delivery count is left as it was, which is right for a move
// an operator made — it was not delivered again, it was reassigned.
//
// profile may be nil; it is asked for only by an automatic claim.
func RedisStreamClaimEntries(ctx context.Context, client *redis.Client, profile *RedisProfile, c RedisStreamClaim) (*RedisStreamClaimed, error) {
	switch {
	case c.Group == "":
		return nil, fmt.Errorf("a consumer group is required")
	case c.Consumer == nil:
		return nil, fmt.Errorf("name the consumer the entries go to")
	case c.MinIdleMs < 0:
		return nil, fmt.Errorf("minIdleMs cannot be negative")
	case c.Auto && len(c.IDs) > 0:
		return nil, fmt.Errorf("claim the entries named or whatever has been idle long enough, not both")
	case !c.Auto && len(c.IDs) == 0:
		return nil, fmt.Errorf("name at least one entry id to claim, or ask for an automatic claim")
	case len(c.IDs) > redisMaxPageRows:
		return nil, fmt.Errorf("at most %d entries are claimed at once", redisMaxPageRows)
	}
	out := &RedisStreamClaimed{Claimed: []string{}, Deleted: []string{}, Cursor: "0-0", Done: true}
	idle := strconv.FormatInt(c.MinIdleMs, 10)
	if !c.Auto {
		args := make([]any, 0, len(c.IDs)+6)
		args = append(args, "XCLAIM", string(c.Key), string(c.Group), string(*c.Consumer), idle)
		for _, id := range c.IDs {
			if !redisStreamIDRe.MatchString(id) {
				return nil, fmt.Errorf("an entry is claimed by its id, such as 1700000000000-0")
			}
			args = append(args, id)
		}
		raw, err := client.Do(ctx, append(args, "JUSTID")...).Result()
		if err != nil && err != redis.Nil {
			return nil, RedisExplainError(ctx, client, err)
		}
		out.Claimed = redisStringList(raw)
		return out, nil
	}

	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	if !profile.Features.StreamAutoClaim {
		return nil, ErrRedisNoAutoClaim
	}
	start := "0-0"
	if c.Cursor != "" {
		if !redisStreamIDRe.MatchString(c.Cursor) {
			return nil, fmt.Errorf("invalid cursor")
		}
		start = c.Cursor
	}
	if c.Count <= 0 {
		c.Count = redisDefaultPageRows
	}
	if c.Count > redisMaxPageRows {
		c.Count = redisMaxPageRows
	}
	raw, err := client.Do(ctx, "XAUTOCLAIM", string(c.Key), string(c.Group), string(*c.Consumer), idle, start,
		"COUNT", c.Count, "JUSTID").Result()
	if err != nil && err != redis.Nil {
		return nil, RedisExplainError(ctx, client, err)
	}
	// The next cursor, the ids claimed, and from Redis 7 the ids that were
	// pending for entries since deleted.
	reply := redisSlice(raw)
	if len(reply) < 2 {
		return nil, fmt.Errorf("the server's answer to XAUTOCLAIM was not a cursor and a list")
	}
	out.Claimed = redisStringList(reply[1])
	if len(reply) > 2 {
		out.Deleted = redisStringList(reply[2])
	}
	if next := redisText(reply[0]); next != "0-0" && next != "" {
		out.Cursor, out.Done = next, false
	}
	return out, nil
}
