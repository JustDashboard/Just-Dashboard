package dbx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// Reading inside a key.
//
// A key is not a value, it is a container, and the old read treated it as a
// value: the first 500 of a list and nothing after, a random 500 of a set that
// changed on every refresh, and the whole of a hash however many million
// fields it had. Everything here is a page with a cursor instead, in the
// command Redis has for walking that type: HSCAN, SSCAN and ZSCAN where the
// order is the server's, LRANGE and ZRANGE where there is a position to ask
// for, XRANGE for a stream, GETRANGE for a string too long to send at once.

// ErrRedisKeyNotFound is returned when the key a request names is not there —
// most often because it expired while the page showing it was open.
var ErrRedisKeyNotFound = errors.New("that key does not exist; it may have expired or been deleted")

// redisJSONType is what TYPE answers for a RedisJSON document.
const redisJSONType = "ReJSON-RL"

// RedisRow is one entry of a collection. Which fields are set depends on the
// key's type; each is a pointer so that an empty member, position 0 and score
// 0 — all of which are real — are told apart from "not this kind of row".
type RedisRow struct {
	// Field is a hash field's name.
	Field *RedisBytes `json:"field,omitempty"`
	// Index is a list element's position, counted from the head.
	Index *int64 `json:"index,omitempty"`
	// Score is a sorted-set member's score.
	Score *RedisScore `json:"score,omitempty"`
	// ID is a stream entry's id.
	ID string `json:"id,omitempty"`
	// Value is the hash value, list element, set member or sorted-set member.
	Value *RedisBytes `json:"value,omitempty"`
	// TTL is a hash field's own expiry in seconds, on a server that has one;
	// -1 means the field does not expire.
	TTL *int64 `json:"ttl,omitempty"`
	// Fields are a stream entry's pairs, in the order they were added. A list
	// rather than an object: an entry may repeat a field name.
	Fields [][2]RedisBytes `json:"fields,omitempty"`
}

// RedisStringChunk is a window of a string value.
type RedisStringChunk struct {
	Value RedisBytes `json:"value"`
	// Offset is where this window starts, in bytes.
	Offset int64 `json:"offset"`
}

// RedisJSONValue is what a JSONPath selected out of a JSON document.
type RedisJSONValue struct {
	Path string `json:"path"`
	// Matches is the server's answer, which for a JSONPath is always an array
	// of what matched: one element for "$".
	Matches json.RawMessage `json:"matches,omitempty"`
	Bytes   int             `json:"bytes"`
	// TooLarge is set instead of Matches when the selection is bigger than a
	// page should carry. A narrower path reaches inside it.
	TooLarge bool `json:"tooLarge,omitempty"`
}

// RedisMembers is one page of a key's contents.
type RedisMembers struct {
	Key  RedisBytes `json:"key"`
	DB   int        `json:"db"`
	Type string     `json:"type"`
	// TTL in seconds and PTTL in milliseconds; -1 means no expiry.
	TTL  int64 `json:"ttl"`
	PTTL int64 `json:"pttl"`
	// Length is the whole key: bytes of a string, entries of anything else.
	Length int64      `json:"length"`
	Rows   []RedisRow `json:"rows"`
	// String and JSON carry the two types that are not a list of rows.
	String *RedisStringChunk `json:"string,omitempty"`
	JSON   *RedisJSONValue   `json:"json,omitempty"`
	// Cursor is what to send back for the next page, while Done is false. Its
	// meaning belongs to the type — a scan cursor, a position, a stream id —
	// and the caller does not need to know which.
	Cursor string `json:"cursor"`
	Done   bool   `json:"done"`
	// Unsupported says why nothing is shown for a type that has no reader
	// here — a module's own — rather than failing the request.
	Unsupported string `json:"unsupported,omitempty"`
}

// RedisMembersOptions is one page request.
type RedisMembersOptions struct {
	Key    RedisBytes
	Cursor string
	// Count is how many rows — or, for a string, how many bytes.
	Count int
	// Match is a glob over hash fields, set members or sorted-set members.
	Match string
	// Desc reads a list from its tail, a sorted set from its highest score, a
	// stream from its newest entry.
	Desc bool
	// Min and Max bound a sorted set by score, in ZRANGEBYSCORE's own syntax.
	Min, Max string
	// From and To bound a stream by entry id or millisecond timestamp.
	From, To string
	// Path is a JSONPath into a JSON document.
	Path string

	// legacy is the first-window read RedisGet makes: larger defaults, and no
	// facts that would cost a probe of the server.
	legacy bool
}

const (
	redisMaxPageRows     = 1000
	redisDefaultPageRows = 100
	redisDefaultChunk    = 64 << 10
	redisMaxJSONBytes    = 2 << 20
	// redisMemberScanTurns bounds the turns one page spends on a selective
	// MATCH, for the reason RedisScanKeys bounds its own.
	redisMemberScanTurns = 30
)

var (
	redisScoreBoundRe = regexp.MustCompile(`^\(?(-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?|[-+]?inf)$`)
	redisStreamIDRe   = regexp.MustCompile(`^[0-9]{1,20}(-[0-9]{1,20})?$`)
)

// RedisReadMembers reads one page of a key. profile may be nil; it is asked
// for only when the key turns out to be of a type whose reading depends on it.
func RedisReadMembers(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisMembersOptions) (*RedisMembers, error) {
	key := string(o.Key)
	pipe := client.Pipeline()
	typeCmd := pipe.Type(ctx, key)
	pttlCmd := pipe.PTTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil && typeCmd.Err() != nil {
		return nil, RedisExplainError(ctx, client, typeCmd.Err())
	}
	typ := typeCmd.Val()
	if typ == "none" || typ == "" {
		return nil, ErrRedisKeyNotFound
	}
	page := &RedisMembers{
		Key: o.Key, DB: client.Options().DB, Type: typ, Rows: []RedisRow{},
		PTTL: ttlMillis(pttlCmd), Cursor: "0", Done: true,
	}
	page.TTL = redisSecondsFromMillis(page.PTTL)

	count := o.Count
	if count <= 0 {
		count = redisDefaultPageRows
	}
	if count > redisMaxPageRows && typ != "string" {
		count = redisMaxPageRows
	}

	var err error
	switch typ {
	case "string":
		err = redisReadString(ctx, client, page, o)
	case "hash":
		if profile == nil && !o.legacy {
			if profile, err = RedisProbe(ctx, client); err != nil {
				return nil, err
			}
		}
		err = redisReadHash(ctx, client, profile, page, o, count)
	case "list":
		err = redisReadList(ctx, client, page, o, count)
	case "set":
		err = redisReadSet(ctx, client, page, o, count)
	case "zset":
		err = redisReadZSet(ctx, client, page, o, count)
	case "stream":
		err = redisReadStream(ctx, client, page, o, count)
	case redisJSONType:
		if profile == nil {
			if profile, err = RedisProbe(ctx, client); err != nil {
				return nil, err
			}
		}
		if !profile.Features.JSON {
			page.Unsupported = "This server reports a JSON document but has no JSON commands to read it with."
			break
		}
		err = redisReadJSON(ctx, client, page, o)
	default:
		// TimeSeries, Bloom filters, vector sets, whatever the next module
		// brings. Each has its own commands and none of them is a list of
		// members, so the page says what it is and points at the console.
		page.Unsupported = fmt.Sprintf("A %s value belongs to a module with its own commands; open it from the console.", typ)
	}
	if err != nil {
		return nil, err
	}
	return page, nil
}

func redisSecondsFromMillis(ms int64) int64 {
	if ms < 0 {
		return ms
	}
	// Rounded up: a key with 400 ms left has not expired, and "0" would say
	// it had.
	return (ms + 999) / 1000
}

// redisOffset reads a cursor that is a position rather than a scan cursor.
func redisOffset(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return n, nil
}

func redisScanCursor(cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(cursor, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	return n, nil
}

func redisReadString(ctx context.Context, client *redis.Client, page *RedisMembers, o RedisMembersOptions) error {
	key := string(o.Key)
	offset, err := redisOffset(o.Cursor)
	if err != nil {
		return err
	}
	size := int64(o.Count)
	if size <= 0 {
		size = redisDefaultChunk
	}
	if o.legacy {
		size = redisMaxStringBytes
	}
	if size > redisMaxStringBytes {
		size = redisMaxStringBytes
	}
	// Never fewer bytes than the longest character: a window that is nothing
	// but the first half of one would be cut back to empty and read as the
	// end of the value.
	if size < utf8.UTFMax {
		size = utf8.UTFMax
	}
	pipe := client.Pipeline()
	lenCmd := pipe.StrLen(ctx, key)
	rangeCmd := pipe.GetRange(ctx, key, offset, offset+size-1)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	page.Length = lenCmd.Val()
	chunk := rangeCmd.Val()
	end := offset + int64(len(chunk))
	if end < page.Length {
		// A window that stops inside a multi-byte character would make text
		// look like binary. It is cut back to the last whole character and the
		// next window starts there.
		chunk = redisTrimPartialRune(chunk)
		end = offset + int64(len(chunk))
	}
	page.String = &RedisStringChunk{Value: RedisBytes(chunk), Offset: offset}
	if end < page.Length && len(chunk) > 0 {
		page.Cursor, page.Done = strconv.FormatInt(end, 10), false
	}
	return nil
}

// redisTrimPartialRune drops a trailing fragment of a UTF-8 sequence. Bytes
// that are simply not UTF-8 are left alone: there is nothing to complete.
func redisTrimPartialRune(s string) string {
	for back := 1; back <= 3 && back <= len(s); back++ {
		b := s[len(s)-back]
		if b < 0x80 {
			return s
		}
		if b >= 0xC0 {
			// A lead byte: the sequence it starts is complete only if all of
			// it is here.
			if utf8.FullRuneInString(s[len(s)-back:]) {
				return s
			}
			return s[:len(s)-back]
		}
	}
	return s
}

func redisReadHash(ctx context.Context, client *redis.Client, profile *RedisProfile, page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	cursor, err := redisScanCursor(o.Cursor)
	if err != nil {
		return err
	}
	if page.Length, err = client.HLen(ctx, key).Result(); err != nil {
		return err
	}
	for turn := 0; turn < redisMemberScanTurns; turn++ {
		pairs, next, err := client.HScan(ctx, key, cursor, o.Match, int64(count)).Result()
		if err != nil {
			return err
		}
		for i := 0; i+1 < len(pairs); i += 2 {
			field, value := RedisBytes(pairs[i]), RedisBytes(pairs[i+1])
			page.Rows = append(page.Rows, RedisRow{Field: &field, Value: &value})
		}
		cursor = next
		if cursor == 0 || len(page.Rows) >= count {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if cursor != 0 {
		page.Cursor, page.Done = strconv.FormatUint(cursor, 10), false
	}
	if profile != nil && profile.Features.HashFieldTTL && len(page.Rows) > 0 {
		fields := make([]string, len(page.Rows))
		for i, r := range page.Rows {
			fields[i] = string(*r.Field)
		}
		// Best effort: a field's expiry is a detail of the row, and a server
		// that will not say leaves the row as it is.
		if ttls, err := client.HTTL(ctx, key, fields...).Result(); err == nil && len(ttls) == len(fields) {
			for i := range page.Rows {
				if ttls[i] >= -1 {
					ttl := ttls[i]
					page.Rows[i].TTL = &ttl
				}
			}
		}
	}
	return nil
}

func redisReadList(ctx context.Context, client *redis.Client, page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	offset, err := redisOffset(o.Cursor)
	if err != nil {
		return err
	}
	if page.Length, err = client.LLen(ctx, key).Result(); err != nil {
		return err
	}
	if offset >= page.Length {
		return nil
	}
	// Positions are always reported from the head, whichever end the page was
	// read from: that is the number LSET and the remove-by-position take.
	start, stop := offset, offset+int64(count)-1
	if o.Desc {
		stop = page.Length - 1 - offset
		start = stop - int64(count) + 1
		if start < 0 {
			start = 0
		}
	}
	values, err := client.LRange(ctx, key, start, stop).Result()
	if err != nil {
		return err
	}
	for i := range values {
		at := i
		if o.Desc {
			at = len(values) - 1 - i
		}
		index, value := start+int64(at), RedisBytes(values[at])
		page.Rows = append(page.Rows, RedisRow{Index: &index, Value: &value})
	}
	if next := offset + int64(len(values)); next < page.Length && len(values) > 0 {
		page.Cursor, page.Done = strconv.FormatInt(next, 10), false
	}
	return nil
}

func redisReadSet(ctx context.Context, client *redis.Client, page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	cursor, err := redisScanCursor(o.Cursor)
	if err != nil {
		return err
	}
	if page.Length, err = client.SCard(ctx, key).Result(); err != nil {
		return err
	}
	for turn := 0; turn < redisMemberScanTurns; turn++ {
		members, next, err := client.SScan(ctx, key, cursor, o.Match, int64(count)).Result()
		if err != nil {
			return err
		}
		for _, m := range members {
			value := RedisBytes(m)
			page.Rows = append(page.Rows, RedisRow{Value: &value})
		}
		cursor = next
		if cursor == 0 || len(page.Rows) >= count {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if cursor != 0 {
		page.Cursor, page.Done = strconv.FormatUint(cursor, 10), false
	}
	return nil
}

func redisReadZSet(ctx context.Context, client *redis.Client, page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	var err error
	if page.Length, err = client.ZCard(ctx, key).Result(); err != nil {
		return err
	}
	row := func(member string, score float64) {
		m, s := RedisBytes(member), RedisScore(score)
		page.Rows = append(page.Rows, RedisRow{Value: &m, Score: &s})
	}

	// A search by member has to be a scan: a sorted set is ordered by score,
	// and nothing but ZSCAN looks at member names.
	if o.Match != "" {
		cursor, err := redisScanCursor(o.Cursor)
		if err != nil {
			return err
		}
		for turn := 0; turn < redisMemberScanTurns; turn++ {
			pairs, next, err := client.ZScan(ctx, key, cursor, o.Match, int64(count)).Result()
			if err != nil {
				return err
			}
			for i := 0; i+1 < len(pairs); i += 2 {
				score, _ := parseRedisScore(pairs[i+1])
				row(pairs[i], score)
			}
			cursor = next
			if cursor == 0 || len(page.Rows) >= count {
				break
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if cursor != 0 {
			page.Cursor, page.Done = strconv.FormatUint(cursor, 10), false
		}
		return nil
	}

	offset, err := redisOffset(o.Cursor)
	if err != nil {
		return err
	}
	var members []redis.Z
	bounded := o.Min != "" || o.Max != ""
	if bounded {
		by := &redis.ZRangeBy{Min: "-inf", Max: "+inf", Offset: offset, Count: int64(count)}
		for _, b := range []struct {
			given string
			into  *string
		}{{o.Min, &by.Min}, {o.Max, &by.Max}} {
			if b.given == "" {
				continue
			}
			if !redisScoreBoundRe.MatchString(b.given) {
				return fmt.Errorf("%q is not a score bound; use a number, -inf or +inf, with a leading ( to exclude it", b.given)
			}
			*b.into = b.given
		}
		if o.Desc {
			members, err = client.ZRevRangeByScoreWithScores(ctx, key, by).Result()
		} else {
			members, err = client.ZRangeByScoreWithScores(ctx, key, by).Result()
		}
	} else if o.Desc {
		members, err = client.ZRevRangeWithScores(ctx, key, offset, offset+int64(count)-1).Result()
	} else {
		members, err = client.ZRangeWithScores(ctx, key, offset, offset+int64(count)-1).Result()
	}
	if err != nil {
		return err
	}
	for _, m := range members {
		row(redisText(m.Member), m.Score)
	}
	next := offset + int64(len(members))
	more := next < page.Length
	if bounded {
		// The size of a score range is not known without counting it; a full
		// page is the only sign that there may be another.
		more = len(members) == count
	}
	if more && len(members) > 0 {
		page.Cursor, page.Done = strconv.FormatInt(next, 10), false
	}
	return nil
}

func redisReadStream(ctx context.Context, client *redis.Client, page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	var err error
	if page.Length, err = client.XLen(ctx, key).Result(); err != nil {
		return err
	}
	from, to := "-", "+"
	for _, b := range []struct {
		given string
		into  *string
	}{{o.From, &from}, {o.To, &to}} {
		if b.given == "" || b.given == "-" || b.given == "+" {
			continue
		}
		if !redisStreamIDRe.MatchString(b.given) {
			return fmt.Errorf("%q is not a stream id; use a millisecond timestamp or an id such as 1700000000000-0", b.given)
		}
		*b.into = b.given
	}
	if o.Cursor != "" {
		if !redisStreamIDRe.MatchString(o.Cursor) {
			return fmt.Errorf("invalid cursor")
		}
		if o.Desc {
			to = o.Cursor
		} else {
			from = o.Cursor
		}
	}
	args := []any{"XRANGE", key, from, to, "COUNT", count}
	if o.Desc {
		args = []any{"XREVRANGE", key, to, from, "COUNT", count}
	}
	reply, err := client.Do(ctx, args...).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	entries := redisStreamEntries(reply)
	page.Rows = append(page.Rows, entries...)
	if len(entries) == count {
		// The next page starts at the id after the last one shown, or before
		// it when reading backwards. Computed here rather than written as an
		// exclusive range, which Redis only learnt in 6.2.
		if next, ok := redisStreamNeighbour(entries[len(entries)-1].ID, o.Desc); ok {
			page.Cursor, page.Done = next, false
		}
	}
	return nil
}

// redisStreamEntries reads an XRANGE-shaped reply: a list of [id, [field,
// value, …]]. The pairs keep their order and their duplicates, both of which
// the driver's own map-backed type throws away.
func redisStreamEntries(reply any) []RedisRow {
	list := redisSlice(reply)
	out := make([]RedisRow, 0, len(list))
	for _, item := range list {
		entry := redisSlice(item)
		if len(entry) < 1 {
			continue
		}
		row := RedisRow{ID: redisText(entry[0]), Fields: [][2]RedisBytes{}}
		if len(entry) > 1 {
			pairs := redisSlice(entry[1])
			for i := 0; i+1 < len(pairs); i += 2 {
				row.Fields = append(row.Fields, [2]RedisBytes{
					RedisBytes(redisText(pairs[i])), RedisBytes(redisText(pairs[i+1])),
				})
			}
		}
		out = append(out, row)
	}
	return out
}

// redisStreamNeighbour is the id immediately after one, or immediately
// before. ok is false at either end of the id space, where there is none.
func redisStreamNeighbour(id string, before bool) (string, bool) {
	msText, seqText, _ := strings.Cut(id, "-")
	ms, err := strconv.ParseUint(msText, 10, 64)
	if err != nil {
		return "", false
	}
	var seq uint64
	if seqText != "" {
		if seq, err = strconv.ParseUint(seqText, 10, 64); err != nil {
			return "", false
		}
	}
	switch {
	case !before && seq < math.MaxUint64:
		seq++
	case !before && ms < math.MaxUint64:
		ms, seq = ms+1, 0
	case before && seq > 0:
		seq--
	case before && ms > 0:
		ms, seq = ms-1, math.MaxUint64
	default:
		return "", false
	}
	return strconv.FormatUint(ms, 10) + "-" + strconv.FormatUint(seq, 10), true
}

// redisJSONPath checks a JSONPath. Only the "$" form is taken: the legacy
// dotted form answers with a bare value rather than a list of matches, and
// one shape is enough to have to parse.
func redisJSONPath(path string) (string, error) {
	if path == "" {
		return "$", nil
	}
	if !strings.HasPrefix(path, "$") || len(path) > 1024 || strings.ContainsAny(path, "\r\n\x00") {
		return "", fmt.Errorf("a JSON path starts with $ — for example $.user.name")
	}
	return path, nil
}

func redisReadJSON(ctx context.Context, client *redis.Client, page *RedisMembers, o RedisMembersOptions) error {
	path, err := redisJSONPath(o.Path)
	if err != nil {
		return err
	}
	text, err := client.Do(ctx, "JSON.GET", string(o.Key), path).Text()
	if err != nil && err != redis.Nil {
		return err
	}
	page.JSON = &RedisJSONValue{Path: path, Bytes: len(text)}
	switch {
	case len(text) > redisMaxJSONBytes:
		page.JSON.TooLarge = true
	case text == "":
		page.JSON.Matches = json.RawMessage("[]")
	case !json.Valid([]byte(text)):
		return fmt.Errorf("the server's answer for %s was not JSON", path)
	default:
		page.JSON.Matches = json.RawMessage(text)
	}
	return nil
}

// RedisKeyMeta is what the server knows about a key without reading it.
type RedisKeyMeta struct {
	Key  RedisBytes `json:"key"`
	DB   int        `json:"db"`
	Type string     `json:"type"`
	TTL  int64      `json:"ttl"`
	PTTL int64      `json:"pttl"`
	// ExpiresAt is PTTL as a moment, so a countdown does not drift with the
	// time the reply spent in transit.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Length    int64      `json:"length"`
	// The four below are each one command some server lacks. An absent field
	// means the server could not say, and Unavailable says why.
	Memory      *int64 `json:"memory,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
	IdleSeconds *int64 `json:"idleSeconds,omitempty"`
	Frequency   *int64 `json:"frequency,omitempty"`
	// Unavailable maps a missing fact — "memory", "encoding", "idleSeconds",
	// "frequency" — to the reason it is missing.
	Unavailable map[string]string `json:"unavailable,omitempty"`
}

// RedisKeyMetadata reads a key's type, expiry, size, cost and encoding.
func RedisKeyMetadata(ctx context.Context, client *redis.Client, profile *RedisProfile, key RedisBytes) (*RedisKeyMeta, error) {
	if profile == nil {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	if profile.Mode == "sentinel" {
		return nil, ErrRedisSentinel
	}
	k := string(key)
	f := profile.Features
	pipe := client.Pipeline()
	typeCmd := pipe.Type(ctx, k)
	pttlCmd := pipe.PTTL(ctx, k)
	var (
		memCmd, freqCmd *redis.IntCmd
		encCmd          *redis.StringCmd
		idleCmd         *redis.DurationCmd
	)
	if f.MemoryUsage {
		memCmd = pipe.MemoryUsage(ctx, k)
	}
	if f.ObjectEncoding {
		encCmd = pipe.ObjectEncoding(ctx, k)
	}
	if f.ObjectIdleTime {
		idleCmd = pipe.ObjectIdleTime(ctx, k)
	}
	if f.ObjectFreq {
		freqCmd = pipe.ObjectFreq(ctx, k)
	}
	read := time.Now()
	if _, err := pipe.Exec(ctx); err != nil && typeCmd.Err() != nil {
		return nil, RedisExplainError(ctx, client, typeCmd.Err())
	}
	typ := typeCmd.Val()
	if typ == "none" || typ == "" {
		return nil, ErrRedisKeyNotFound
	}
	meta := &RedisKeyMeta{
		Key: key, DB: client.Options().DB, Type: typ,
		PTTL: ttlMillis(pttlCmd), Unavailable: map[string]string{},
	}
	meta.TTL = redisSecondsFromMillis(meta.PTTL)
	if meta.PTTL > 0 {
		at := read.Add(time.Duration(meta.PTTL) * time.Millisecond).UTC()
		meta.ExpiresAt = &at
	}

	server := redisProductName(profile)
	switch {
	case memCmd == nil:
		meta.Unavailable["memory"] = server + " has no MEMORY USAGE."
	case memCmd.Err() != nil:
		meta.Unavailable["memory"] = memCmd.Err().Error()
	default:
		n := memCmd.Val()
		meta.Memory = &n
	}
	switch {
	case encCmd == nil:
		meta.Unavailable["encoding"] = server + " has no OBJECT ENCODING."
	case encCmd.Err() != nil:
		meta.Unavailable["encoding"] = encCmd.Err().Error()
	default:
		meta.Encoding = encCmd.Val()
	}
	switch {
	case idleCmd != nil && idleCmd.Err() == nil:
		n := int64(idleCmd.Val().Seconds())
		meta.IdleSeconds = &n
	case idleCmd != nil:
		meta.Unavailable["idleSeconds"] = idleCmd.Err().Error()
	case !f.ObjectEncoding:
		meta.Unavailable["idleSeconds"] = server + " has no OBJECT IDLETIME."
	default:
		meta.Unavailable["idleSeconds"] = "The eviction policy is LFU, so this server tracks how often a key is used rather than how long ago."
	}
	switch {
	case freqCmd != nil && freqCmd.Err() == nil:
		n := freqCmd.Val()
		meta.Frequency = &n
	case freqCmd != nil:
		meta.Unavailable["frequency"] = freqCmd.Err().Error()
	case !f.ObjectEncoding:
		meta.Unavailable["frequency"] = server + " has no OBJECT FREQ."
	default:
		meta.Unavailable["frequency"] = "Access frequency is tracked only under an LFU eviction policy (maxmemory-policy allkeys-lfu or volatile-lfu)."
	}
	if len(meta.Unavailable) == 0 {
		meta.Unavailable = nil
	}

	lenPipe := client.Pipeline()
	lenCmd := redisLengthCmd(ctx, lenPipe, typ, k)
	if lenCmd != nil {
		_, _ = lenPipe.Exec(ctx)
		meta.Length = lenCmd.Val()
	}
	return meta, nil
}

// redisProductName is the server as a sentence names it: "Dragonfly 2.0.0".
func redisProductName(p *RedisProfile) string {
	names := map[string]string{
		RedisFlavorRedis: "Redis", RedisFlavorValkey: "Valkey",
		RedisFlavorKeyDB: "KeyDB", RedisFlavorDragonfly: "Dragonfly",
	}
	name := names[p.Flavor]
	if name == "" {
		name = "This server"
	}
	if p.Version == "" {
		return name
	}
	return name + " " + p.Version
}
