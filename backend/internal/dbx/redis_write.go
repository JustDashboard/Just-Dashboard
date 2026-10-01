package dbx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Writing.
//
// Every collection type is writable, but each write names the *member* it
// changes rather than the collection as a whole. That distinction is the whole
// design: a form showing 500 of a list's 10,000 entries must never be able to
// save "the list" — it would silently drop the 9,500 it never showed. So a
// write here always identifies one member, and replacing a whole collection is
// something the operator does deliberately from the console.
//
// The second rule is that a write decides nothing from the absence of a
// field. "No TTL given" keeps the key's expiry rather than clearing it, and
// "no member given" is not the same request as "the member whose name is the
// empty string" — which is a member Redis is perfectly happy to store.

// RedisKeyExistsError is a create, rename or copy that would have landed on a
// key that is already there.
type RedisKeyExistsError struct{ Key RedisBytes }

func (e *RedisKeyExistsError) Error() string {
	return fmt.Sprintf("a key named %q already exists", e.Key.Display())
}

// RedisConflictError is a write that was refused because the key was not in
// the state the request described: another client changed it in between.
type RedisConflictError struct{ Reason string }

func (e *RedisConflictError) Error() string { return e.Reason }

// RedisWrite is one write to one key.
type RedisWrite struct {
	Key RedisBytes
	// Type is string, hash, list, set, zset, stream or json. Empty is string.
	Type string
	// Create refuses the write when the key already exists.
	Create bool
	// Value is the string, the hash field's value, the list element, the set
	// or sorted-set member, or a JSON document as text.
	Value *RedisBytes
	// Field is a hash field's name. For a list it may carry a position and
	// for a sorted set a score, which is how those were sent before Index and
	// Score existed.
	Field *RedisBytes
	Score *RedisScore
	// Index is a list position. On its own the element there is replaced;
	// with Insert the new element goes in before it and the rest move down.
	Index  *int64
	Insert bool
	// Head pushes a new list element onto the front instead of the back.
	Head bool
	// Expect is what the caller believes is at Index. The write is refused if
	// something else is: a position is only a name for an element until
	// somebody pushes onto the list.
	Expect *RedisBytes
	// Replace is the hash field, set member or sorted-set member this write
	// renames. It is removed in the same transaction the new one is written.
	Replace *RedisBytes
	// TTL in seconds. Nil or zero leaves the key's expiry as it is, a
	// positive value sets it, a negative one removes it.
	TTL *int64
	// ID and Entries are a stream entry: the id ("*" for the server's own)
	// and its field/value pairs in order.
	ID      string
	Entries [][2]RedisBytes
	// Path is where in a JSON document Value goes; empty is the root.
	Path string
}

// RedisWriteResult is what a write produced that the caller did not send.
type RedisWriteResult struct {
	// ID is the id the server gave a new stream entry.
	ID string `json:"id,omitempty"`
}

var redisStreamAddIDRe = regexp.MustCompile(`^(\*|[0-9]{1,20}(-([0-9]{1,20}|\*))?)$`)

// normalise reads the older request form into the fields the write uses.
func (w *RedisWrite) normalise() error {
	if w.Type == "" {
		w.Type = "string"
	}
	switch w.Type {
	case "string", "hash", "set", "json":
	case "list":
		if w.Index == nil && w.Field != nil && *w.Field != "" {
			idx, err := strconv.ParseInt(string(*w.Field), 10, 64)
			if err != nil {
				return fmt.Errorf("a list position must be a number")
			}
			w.Index = &idx
		}
	case "zset":
		if w.Score == nil {
			if w.Field == nil {
				return fmt.Errorf("a numeric score is required for a sorted set member")
			}
			f, err := parseRedisScore(string(*w.Field))
			if err != nil {
				return err
			}
			score := RedisScore(f)
			w.Score = &score
		}
	case "stream":
		if len(w.Entries) == 0 {
			return fmt.Errorf("a stream entry needs at least one field and value")
		}
		if w.ID == "" {
			w.ID = "*"
		}
		if !redisStreamAddIDRe.MatchString(w.ID) {
			return fmt.Errorf("%q is not a stream id; use * to let the server choose", w.ID)
		}
		return nil
	default:
		return fmt.Errorf("a %s cannot be written from here; use the console", w.Type)
	}
	if w.Value == nil {
		return fmt.Errorf("a value is required")
	}
	if w.Type == "hash" && w.Field == nil {
		return fmt.Errorf("a hash field name is required")
	}
	if w.Type == "json" && !json.Valid([]byte(*w.Value)) {
		return fmt.Errorf("the value is not valid JSON")
	}
	return nil
}

// RedisWriteValue applies one write.
//
// It runs under WATCH: the key's type is read first and the write is queued
// only if it still fits, so a string is never written over a hash and a
// create never lands on a key somebody else made a moment ago. profile may be
// nil.
func RedisWriteValue(ctx context.Context, client *redis.Client, profile *RedisProfile, w RedisWrite) (*RedisWriteResult, error) {
	if err := w.normalise(); err != nil {
		return nil, err
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
	stored := w.Type
	if w.Type == "json" {
		if !profile.Features.JSON {
			return nil, fmt.Errorf("%s has no JSON module loaded", redisProductName(profile))
		}
		stored = redisJSONType
	}
	key := string(w.Key)
	result := &RedisWriteResult{}
	err := redisWatch(ctx, client, key, func(tx *redis.Tx) error {
		existing, err := tx.Type(ctx, key).Result()
		if err != nil {
			return err
		}
		switch {
		case w.Create && existing != "none":
			return &RedisKeyExistsError{Key: w.Key}
		case existing != "none" && existing != stored:
			return fmt.Errorf("%q holds a %s, not a %s; a write of another type would replace all of it",
				w.Key.Display(), existing, w.Type)
		}
		queue, err := redisPlanWrite(ctx, tx, profile, w, existing != "none")
		if err != nil {
			return err
		}
		var added *redis.Cmd
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			added = queue(pipe)
			// SET carries its own expiry; the other writes do not, so an
			// explicit TTL is applied in the same transaction rather than
			// silently dropped.
			if w.Type != "string" && w.TTL != nil {
				switch {
				case *w.TTL > 0:
					pipe.Expire(ctx, key, time.Duration(*w.TTL)*time.Second)
				case *w.TTL < 0:
					pipe.Persist(ctx, key)
				}
			}
			return nil
		})
		if err == nil && added != nil {
			result.ID, _ = added.Text()
		}
		return err
	})
	if err != nil {
		return nil, RedisExplainError(ctx, client, err)
	}
	return result, nil
}

// redisWatch runs fn under WATCH on one key and retries it when the key
// changed before the transaction committed. Three attempts, then the caller
// is told the key is too busy to edit by hand.
func redisWatch(ctx context.Context, client *redis.Client, key string, fn func(tx *redis.Tx) error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = client.Watch(ctx, fn, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return &RedisConflictError{Reason: "the key kept changing while this was being applied; nothing was written. Try again"}
}

// redisPlanWrite does the reads a write depends on and returns the commands
// to queue. The reads happen under WATCH, so what they saw is what the
// transaction acts on or the transaction does not run.
func redisPlanWrite(ctx context.Context, tx *redis.Tx, profile *RedisProfile, w RedisWrite, exists bool) (func(redis.Pipeliner) *redis.Cmd, error) {
	key := string(w.Key)
	none := func(q func(pipe redis.Pipeliner)) func(redis.Pipeliner) *redis.Cmd {
		return func(pipe redis.Pipeliner) *redis.Cmd {
			q(pipe)
			return nil
		}
	}
	switch w.Type {
	case "string":
		// Saving a value is not a statement about its expiry. A plain SET
		// clears the TTL, which is how editing a session by hand used to make
		// it immortal.
		var expiry time.Duration
		switch {
		case w.TTL != nil && *w.TTL > 0:
			expiry = time.Duration(*w.TTL) * time.Second
		case w.TTL != nil && *w.TTL < 0:
		case !exists:
		case profile.Features.KeepTTL:
			expiry = redis.KeepTTL
		default:
			// Before Redis 6 there is no KEEPTTL; the remaining time is read
			// and written back, under the same WATCH.
			left, err := tx.PTTL(ctx, key).Result()
			if err != nil {
				return nil, err
			}
			if left > 0 {
				expiry = left
			}
		}
		return none(func(pipe redis.Pipeliner) { pipe.Set(ctx, key, string(*w.Value), expiry) }), nil

	case "hash":
		field := string(*w.Field)
		renaming := w.Replace != nil && *w.Replace != *w.Field
		if renaming {
			taken, err := tx.HExists(ctx, key, field).Result()
			if err != nil {
				return nil, err
			}
			if taken {
				return nil, &RedisConflictError{Reason: fmt.Sprintf(
					"a field named %q already exists; renaming onto it would replace its value", w.Field.Display())}
			}
		}
		// HSET clears a field's own expiry, the way SET clears a key's.
		var fieldTTL int64
		if exists && profile.Features.HashFieldTTL {
			from := field
			if renaming {
				from = string(*w.Replace)
			}
			if ttls, err := tx.Do(ctx, "HPTTL", key, "FIELDS", 1, from).Int64Slice(); err == nil && len(ttls) == 1 {
				fieldTTL = ttls[0]
			}
		}
		return none(func(pipe redis.Pipeliner) {
			if renaming {
				pipe.HDel(ctx, key, string(*w.Replace))
			}
			pipe.HSet(ctx, key, field, string(*w.Value))
			if fieldTTL > 0 {
				pipe.Do(ctx, "HPEXPIRE", key, fieldTTL, "FIELDS", 1, field)
			}
		}), nil

	case "set":
		return none(func(pipe redis.Pipeliner) {
			if w.Replace != nil && *w.Replace != *w.Value {
				pipe.SRem(ctx, key, string(*w.Replace))
			}
			pipe.SAdd(ctx, key, string(*w.Value))
		}), nil

	case "zset":
		return none(func(pipe redis.Pipeliner) {
			if w.Replace != nil && *w.Replace != *w.Value {
				pipe.ZRem(ctx, key, string(*w.Replace))
			}
			pipe.Do(ctx, "ZADD", key, formatRedisScore(float64(*w.Score)), string(*w.Value))
		}), nil

	case "list":
		return redisPlanListWrite(ctx, tx, w)

	case "stream":
		args := make([]any, 0, 3+2*len(w.Entries))
		args = append(args, "XADD", key, w.ID)
		for _, pair := range w.Entries {
			args = append(args, string(pair[0]), string(pair[1]))
		}
		return func(pipe redis.Pipeliner) *redis.Cmd { return pipe.Do(ctx, args...) }, nil

	case "json":
		path, err := redisJSONPath(w.Path)
		if err != nil {
			return nil, err
		}
		if !exists && path != "$" {
			return nil, fmt.Errorf("a new JSON document is written at its root, $")
		}
		return none(func(pipe redis.Pipeliner) { pipe.Do(ctx, "JSON.SET", key, path, string(*w.Value)) }), nil
	}
	return nil, fmt.Errorf("a %s cannot be written from here; use the console", w.Type)
}

// redisPlanListWrite covers the four things a list write can be: a push onto
// either end, a replacement at a position, and an insertion before one.
func redisPlanListWrite(ctx context.Context, tx *redis.Tx, w RedisWrite) (func(redis.Pipeliner) *redis.Cmd, error) {
	key, value := string(w.Key), string(*w.Value)
	none := func(q func(pipe redis.Pipeliner)) func(redis.Pipeliner) *redis.Cmd {
		return func(pipe redis.Pipeliner) *redis.Cmd {
			q(pipe)
			return nil
		}
	}
	if w.Index == nil {
		if w.Head {
			return none(func(pipe redis.Pipeliner) { pipe.LPush(ctx, key, value) }), nil
		}
		return none(func(pipe redis.Pipeliner) { pipe.RPush(ctx, key, value) }), nil
	}
	idx := *w.Index
	length, err := tx.LLen(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if w.Insert && idx == length {
		// Inserting after the last element is appending.
		return none(func(pipe redis.Pipeliner) { pipe.RPush(ctx, key, value) }), nil
	}
	if idx < 0 || idx >= length {
		return nil, fmt.Errorf("position %d is outside the list, which has %d elements", idx, length)
	}
	current, err := tx.LIndex(ctx, key, idx).Result()
	if err != nil {
		return nil, err
	}
	if w.Expect != nil && current != string(*w.Expect) {
		return nil, &RedisConflictError{Reason: fmt.Sprintf(
			"the list changed: position %d no longer holds the element this edit was made against. Reload and try again", idx)}
	}
	if !w.Insert {
		return none(func(pipe redis.Pipeliner) { pipe.LSet(ctx, key, idx, value) }), nil
	}
	// Redis inserts beside a value, not at a position. So the position is
	// given a value nothing else in the list can have, the new element goes in
	// before that, and the element that was there is put back one place
	// further on. It is one MULTI, so no other client sees the marker.
	marker, err := redisMarker()
	if err != nil {
		return nil, err
	}
	return none(func(pipe redis.Pipeliner) {
		pipe.LSet(ctx, key, idx, marker)
		pipe.LInsertBefore(ctx, key, marker, value)
		pipe.LSet(ctx, key, idx+1, current)
	}), nil
}

// redisMarker is a list element no real list holds. It used to be a fixed
// word, and a list that happened to contain that word lost the wrong element.
func redisMarker() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "\x00jd:" + hex.EncodeToString(raw) + "\x00", nil
}

// RedisRemoval names what to take out of one collection.
type RedisRemoval struct {
	Key RedisBytes
	// Type is looked up when it is empty.
	Type string
	// Members are hash fields, set or sorted-set members, or stream entry
	// ids. The empty string is a member like any other.
	Members []RedisBytes
	// Index removes the list element at one position instead.
	Index *int64
	// Expect is what the caller believes is at Index.
	Expect *RedisBytes
}

// RedisRemoveMembers removes members of a collection, leaving the rest.
func RedisRemoveMembers(ctx context.Context, client *redis.Client, r RedisRemoval) (int64, error) {
	key := string(r.Key)
	if r.Type == "" {
		typ, err := client.Type(ctx, key).Result()
		if err != nil {
			return 0, RedisExplainError(ctx, client, err)
		}
		if typ == "none" {
			return 0, ErrRedisKeyNotFound
		}
		r.Type = typ
	}
	if r.Index != nil {
		if r.Type != "list" {
			return 0, fmt.Errorf("only a list has positions; a %s is addressed by member", r.Type)
		}
		return redisRemoveListIndex(ctx, client, r)
	}
	if len(r.Members) == 0 {
		return 0, fmt.Errorf("name at least one member to remove")
	}
	members := make([]any, len(r.Members))
	for i, m := range r.Members {
		members[i] = string(m)
	}
	var (
		removed int64
		err     error
	)
	switch r.Type {
	case "hash":
		removed, err = client.HDel(ctx, key, redisStrings(r.Members)...).Result()
	case "set":
		removed, err = client.SRem(ctx, key, members...).Result()
	case "zset":
		removed, err = client.ZRem(ctx, key, members...).Result()
	case "stream":
		for _, m := range r.Members {
			if !redisStreamIDRe.MatchString(string(m)) {
				return 0, fmt.Errorf("%q is not a stream entry id", m.Display())
			}
		}
		removed, err = client.XDel(ctx, key, redisStrings(r.Members)...).Result()
	case "list":
		// A list may hold the same value many times, so a value does not say
		// which element is meant. A position does.
		return 0, fmt.Errorf("a list element is removed by its position")
	default:
		return 0, fmt.Errorf("a %s has no members to remove individually", r.Type)
	}
	if err != nil {
		return 0, RedisExplainError(ctx, client, err)
	}
	return removed, nil
}

// redisRemoveListIndex removes one element by position.
//
// Lists are the awkward case: Redis has no "delete by index". The documented
// idiom is to write a marker into the position and then LREM it, which is
// what this does — and it is done in a MULTI so no other client can observe
// the marker as if it were real data.
func redisRemoveListIndex(ctx context.Context, client *redis.Client, r RedisRemoval) (int64, error) {
	key, idx := string(r.Key), *r.Index
	var removed int64
	err := redisWatch(ctx, client, key, func(tx *redis.Tx) error {
		current, err := tx.LIndex(ctx, key, idx).Result()
		if err == redis.Nil {
			return fmt.Errorf("the list has no element at position %d", idx)
		}
		if err != nil {
			return err
		}
		if r.Expect != nil && current != string(*r.Expect) {
			return &RedisConflictError{Reason: fmt.Sprintf(
				"the list changed: position %d no longer holds the element this was meant to remove. Reload and try again", idx)}
		}
		marker, err := redisMarker()
		if err != nil {
			return err
		}
		var rem *redis.IntCmd
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.LSet(ctx, key, idx, marker)
			rem = pipe.LRem(ctx, key, 1, marker)
			return nil
		})
		if err == nil {
			removed = rem.Val()
		}
		return err
	})
	if err != nil {
		return 0, RedisExplainError(ctx, client, err)
	}
	return removed, nil
}

// RedisRenameKey moves a key. An existing destination is refused rather than
// silently overwritten, which is what plain RENAME would do — unless
// overwrite says to, and then the key that was there is gone.
func RedisRenameKey(ctx context.Context, client *redis.Client, from, to RedisBytes, overwrite bool) error {
	if from == to {
		return fmt.Errorf("the new name is the same as the old one")
	}
	var err error
	if overwrite {
		err = client.Rename(ctx, string(from), string(to)).Err()
	} else {
		var moved bool
		moved, err = client.RenameNX(ctx, string(from), string(to)).Result()
		if err == nil && !moved {
			return &RedisKeyExistsError{Key: to}
		}
	}
	if err != nil {
		if redisNoSuchKey(err) {
			return ErrRedisKeyNotFound
		}
		return RedisExplainError(ctx, client, err)
	}
	return nil
}

func redisNoSuchKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such key")
}

// RedisCopy is one key duplicated under another name.
type RedisCopy struct {
	From, To RedisBytes
	// DB is the logical database to copy into; nil is the one being read.
	DB *int
	// Replace overwrites a key already at the destination.
	Replace bool
}

// RedisCopyKey duplicates a key with its expiry.
//
// COPY does it in one command where the server has it. Before Redis 6.2 the
// same result is DUMP and RESTORE, which is what COPY replaced — within one
// database only, since RESTORE has no way to name another.
func RedisCopyKey(ctx context.Context, client *redis.Client, profile *RedisProfile, c RedisCopy) error {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return err
		}
	}
	from, to := string(c.From), string(c.To)
	here := client.Options().DB
	sameDB := c.DB == nil || *c.DB == here
	if sameDB && from == to {
		return fmt.Errorf("the copy needs a different name from the original")
	}
	if c.DB != nil && *c.DB < 0 {
		return fmt.Errorf("a database number cannot be negative")
	}
	if profile.Features.Copy {
		args := []any{"COPY", from, to}
		if !sameDB {
			args = append(args, "DB", *c.DB)
		}
		if c.Replace {
			args = append(args, "REPLACE")
		}
		copied, err := client.Do(ctx, args...).Int64()
		if err != nil {
			return RedisExplainError(ctx, client, err)
		}
		if copied == 1 {
			return nil
		}
		// Zero means either end was wrong, and COPY does not say which.
		if n, err := client.Exists(ctx, from).Result(); err == nil && n == 0 {
			return ErrRedisKeyNotFound
		}
		return &RedisKeyExistsError{Key: c.To}
	}
	if !sameDB {
		return fmt.Errorf("copying into another database needs COPY, which %s does not have", redisProductName(profile))
	}
	pipe := client.Pipeline()
	dump := pipe.Dump(ctx, from)
	pttl := pipe.PTTL(ctx, from)
	_, _ = pipe.Exec(ctx)
	payload, err := dump.Result()
	if err == redis.Nil {
		return ErrRedisKeyNotFound
	}
	if err != nil {
		return RedisExplainError(ctx, client, err)
	}
	ttl := pttl.Val()
	if ttl < 0 {
		ttl = 0
	}
	if c.Replace {
		err = client.RestoreReplace(ctx, to, ttl, payload).Err()
	} else {
		err = client.Restore(ctx, to, ttl, payload).Err()
	}
	if err != nil {
		if strings.HasPrefix(err.Error(), "BUSYKEY") {
			return &RedisKeyExistsError{Key: c.To}
		}
		return RedisExplainError(ctx, client, err)
	}
	return nil
}

// RedisDeleteKeys removes whole keys with UNLINK where the server has it,
// which frees a large key in the background instead of stalling every other
// client while a million-member set is taken apart.
func RedisDeleteKeys(ctx context.Context, client *redis.Client, profile *RedisProfile, keys []RedisBytes) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return 0, err
		}
	}
	removed, err := redisUnlink(ctx, client, profile, redisStrings(keys))
	if err != nil {
		return removed, RedisExplainError(ctx, client, err)
	}
	return removed, nil
}

// redisUnlink removes one batch of keys. A cluster node refuses one command
// across keys in different slots, so there each key is its own command.
func redisUnlink(ctx context.Context, client *redis.Client, profile *RedisProfile, keys []string) (int64, error) {
	verb := "DEL"
	if profile.Features.Unlink {
		verb = "UNLINK"
	}
	if profile.Mode != "cluster" {
		args := make([]any, 0, len(keys)+1)
		args = append(args, verb)
		for _, k := range keys {
			args = append(args, k)
		}
		return client.Do(ctx, args...).Int64()
	}
	pipe := client.Pipeline()
	cmds := make([]*redis.Cmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Do(ctx, verb, k)
	}
	_, err := pipe.Exec(ctx)
	var removed int64
	for _, c := range cmds {
		if n, cerr := c.Int64(); cerr == nil {
			removed += n
		}
	}
	return removed, err
}

// RedisExpiry says when a key should expire, in exactly one of three ways.
type RedisExpiry struct {
	// Seconds and Millis are from now; zero or less removes the expiry.
	Seconds *int64
	Millis  *int64
	// At is a moment, in milliseconds since the Unix epoch.
	At *int64
}

// RedisSetExpiry sets or clears a key's expiry and reports what it is
// afterwards, in milliseconds (-1 for none).
//
// A relative expiry of zero or less persists the key, matching what PERSIST
// does, rather than deleting it immediately — which is what EXPIRE with a
// non-positive value would otherwise do and is never what somebody clearing a
// TTL in a form meant.
func RedisSetExpiry(ctx context.Context, client *redis.Client, key RedisBytes, e RedisExpiry) (int64, error) {
	k := string(key)
	given := 0
	for _, set := range []bool{e.Seconds != nil, e.Millis != nil, e.At != nil} {
		if set {
			given++
		}
	}
	if given != 1 {
		return 0, fmt.Errorf("give the expiry one way: ttl in seconds, ttlMs in milliseconds, or at as a moment")
	}
	pipe := client.Pipeline()
	var applied *redis.BoolCmd
	switch {
	case e.At != nil:
		// A moment already past deletes the key the instant it is set, which
		// is a delete under another name and is refused as one.
		if *e.At <= time.Now().UnixMilli() {
			return 0, fmt.Errorf("that moment has already passed; setting it would delete the key at once")
		}
		applied = pipe.PExpireAt(ctx, k, time.UnixMilli(*e.At))
	case e.Millis != nil && *e.Millis > 0:
		applied = pipe.PExpire(ctx, k, time.Duration(*e.Millis)*time.Millisecond)
	case e.Seconds != nil && *e.Seconds > 0:
		applied = pipe.Expire(ctx, k, time.Duration(*e.Seconds)*time.Second)
	default:
		pipe.Persist(ctx, k)
	}
	exists := pipe.Exists(ctx, k)
	pttl := pipe.PTTL(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil && exists.Err() != nil {
		return 0, RedisExplainError(ctx, client, exists.Err())
	}
	if applied != nil && applied.Err() != nil {
		return 0, RedisExplainError(ctx, client, applied.Err())
	}
	if exists.Val() == 0 {
		return 0, ErrRedisKeyNotFound
	}
	return ttlMillis(pttl), nil
}

// RedisJSONDelete removes what a JSONPath selects from a JSON document and
// reports how many values went. The root path removes the document itself.
func RedisJSONDelete(ctx context.Context, client *redis.Client, key RedisBytes, path string) (int64, error) {
	checked, err := redisJSONPath(path)
	if err != nil {
		return 0, err
	}
	n, err := client.Do(ctx, "JSON.DEL", string(key), checked).Int64()
	if err != nil {
		return 0, RedisExplainError(ctx, client, err)
	}
	return n, nil
}
