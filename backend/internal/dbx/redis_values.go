package dbx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// Truncated says the row carries only the start of its value, because
	// the whole of it is more than a page should hold; Bytes is how long the
	// value really is. A row that says so must not be written back as it is:
	// that would replace the value with its own first few kilobytes.
	Truncated bool   `json:"truncated,omitempty"`
	Bytes     *int64 `json:"bytes,omitempty"`
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
//
// The page itself is read over a connection of its own, by the same bounded
// reader the console uses. A page is a bounded number of members, but a
// member is as large as whoever wrote it made it — a hash of cached pages
// holds megabytes per field — and the client library would read a hundred of
// those into memory to hand them over. Here each member is kept up to
// redisMaxMemberBytes, the rest of it is read past, and the row says it was
// cut and how long it really is.
func RedisReadMembers(ctx context.Context, client *redis.Client, profile *RedisProfile, o RedisMembersOptions) (*RedisMembers, error) {
	key := string(o.Key)
	pipe := client.Pipeline()
	typeCmd := pipe.Type(ctx, key)
	pttlCmd := redisTTLCmd(ctx, pipe, "pttl", key)
	if _, err := pipe.Exec(ctx); err != nil && typeCmd.Err() != nil {
		return nil, RedisExplainError(ctx, client, typeCmd.Err())
	}
	typ := typeCmd.Val()
	if typ == "none" || typ == "" {
		return nil, ErrRedisKeyNotFound
	}
	page := &RedisMembers{
		Key: o.Key, DB: client.Options().DB, Type: typ, Rows: []RedisRow{},
		PTTL: redisTTL(pttlCmd), Cursor: "0", Done: true,
	}
	page.TTL = redisSecondsFromMillis(page.PTTL)

	count := o.Count
	if count <= 0 {
		count = redisDefaultPageRows
	}
	if count > redisMaxPageRows {
		count = redisMaxPageRows
	}
	switch typ {
	case "string", "hash", "list", "set", "zset", "stream", redisJSONType:
	default:
		// TimeSeries, Bloom filters, vector sets, whatever the next module
		// brings. Each has its own commands and none of them is a list of
		// members, so the page says what it is and points at the console.
		page.Unsupported = fmt.Sprintf("A %s value belongs to a module with its own commands; open it from the console.", typ)
		return page, nil
	}
	if profile == nil && (typ == redisJSONType || (typ == "hash" && !o.legacy)) {
		var err error
		if profile, err = RedisProbe(ctx, client); err != nil {
			return nil, err
		}
	}
	if typ == redisJSONType && !profile.Features.JSON {
		page.Unsupported = "This server reports a JSON document but has no JSON commands to read it with."
		return page, nil
	}

	wire, err := redisDialOptions(ctx, client.Options())
	if err != nil {
		return nil, err
	}
	defer wire.close()
	if err := wire.login(client.Options().DB); err != nil {
		return nil, err
	}
	// A page is somebody looking, not the application using the key.
	wire.quiet()
	pager := &redisPager{wire: wire}
	switch typ {
	case "string":
		err = pager.readString(page, o)
	case "hash":
		if err = pager.readHash(page, o, count); err == nil {
			redisHashFieldTTLs(ctx, client, profile, page)
		}
	case "list":
		err = pager.readList(page, o, count)
	case "set":
		err = pager.readSet(page, o, count)
	case "zset":
		err = pager.readZSet(page, o, count)
	case "stream":
		err = pager.readStream(page, o, count)
	case redisJSONType:
		err = pager.readJSON(page, o)
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

const (
	// redisMaxMemberBytes is how much of one member a page carries.
	redisMaxMemberBytes = 64 << 10
	// redisPageBytes and redisPageNodes are how much one reply may keep in
	// all: strings, and values of any kind.
	redisPageBytes = 8 << 20
	redisPageNodes = 50000
	// redisPageTimeout bounds one command of a page, including the time it
	// takes to read past whatever was too long to keep.
	redisPageTimeout = 20 * time.Second
)

// redisPager reads the commands a page is made of over one raw connection.
type redisPager struct {
	wire *redisWire
}

// ask sends one command and reads its reply, keeping at most maxString bytes
// of any one string. Whatever is not kept is read past, so the connection is
// ready for the next command; an error reply comes back as an error.
func (g *redisPager) ask(maxString int, args ...string) (RedisReply, error) {
	if err := g.wire.send(redisPageTimeout, args...); err != nil {
		return RedisReply{}, err
	}
	reader := &redisReplyReader{
		rd: g.wire.rd, nodes: redisPageNodes, bytes: redisPageBytes,
		maxString: maxString, drain: true,
	}
	reply, err := reader.read(0)
	if err != nil {
		return RedisReply{}, err
	}
	if reply.Type == "error" {
		msg := redisText(reply.Value)
		if sentence, ok := redisExplainCluster(msg); ok {
			return RedisReply{}, errors.New(sentence)
		}
		return RedisReply{}, redisServerError(msg)
	}
	return reply, nil
}

// length asks a command that answers with a number.
func (g *redisPager) length(args ...string) (int64, error) {
	reply, err := g.ask(redisMaxMemberBytes, args...)
	if err != nil {
		return 0, err
	}
	n, _ := redisInt(reply.generic())
	return n, nil
}

// redisMember reads one string out of a reply: its bytes, whether they are
// all of it, and its real size.
func redisMember(r RedisReply) (RedisBytes, bool, int64) {
	b, ok := r.Value.(RedisBytes)
	if !ok {
		b = RedisBytes(redisText(r.generic()))
	}
	if r.Truncated {
		return b, true, int64(r.Length)
	}
	return b, false, int64(len(b))
}

// value sets a row's value, marking the row when the value was cut.
func (row *RedisRow) value(r RedisReply) {
	v, cut, size := redisMember(r)
	row.Value = &v
	if cut {
		row.Truncated, row.Bytes = true, &size
	}
}

func (g *redisPager) readString(page *RedisMembers, o RedisMembersOptions) error {
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
	if page.Length, err = g.length("STRLEN", key); err != nil {
		return err
	}
	reply, err := g.ask(int(size), "GETRANGE", key, strconv.FormatInt(offset, 10), strconv.FormatInt(offset+size-1, 10))
	if err != nil {
		return err
	}
	value, _, _ := redisMember(reply)
	chunk := string(value)
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

// scan runs one turn of HSCAN, SSCAN or ZSCAN and returns what it handed
// over.
//
// A turn's reply is all or nothing: its cursor is past everything in it, so
// items dropped for want of room would never be seen again. When a turn
// brings more than a page can hold it is asked again for fewer.
func (g *redisPager) scan(verb, key string, cursor uint64, match string, count int) ([]RedisReply, uint64, error) {
	for {
		args := []string{verb, key, strconv.FormatUint(cursor, 10)}
		if match != "" {
			args = append(args, "MATCH", match)
		}
		args = append(args, "COUNT", strconv.Itoa(count))
		reply, err := g.ask(redisMaxMemberBytes, args...)
		if err != nil {
			return nil, 0, err
		}
		if len(reply.Items) != 2 {
			return nil, 0, fmt.Errorf("the server's answer to %s was not a cursor and a list", verb)
		}
		if reply.Items[1].Truncated {
			if count == 1 {
				return nil, 0, fmt.Errorf("one turn of %s returned more than a page can carry; narrow it with a match pattern", verb)
			}
			count = max(count/4, 1)
			continue
		}
		next, err := strconv.ParseUint(redisText(reply.Items[0].generic()), 10, 64)
		if err != nil {
			return nil, 0, fmt.Errorf("the server's answer to %s carried no cursor", verb)
		}
		return reply.Items[1].Items, next, nil
	}
}

// scanPage turns scans into a page: turn after turn until there is a page's
// worth, the scan ends, or the turns allowed for a selective MATCH run out.
func (g *redisPager) scanPage(page *RedisMembers, verb string, o RedisMembersOptions, count, width int, row func(items []RedisReply) RedisRow) error {
	cursor, err := redisScanCursor(o.Cursor)
	if err != nil {
		return err
	}
	for turn := 0; turn < redisMemberScanTurns; turn++ {
		items, next, err := g.scan(verb, string(o.Key), cursor, o.Match, count)
		if err != nil {
			return err
		}
		for i := 0; i+width <= len(items); i += width {
			page.Rows = append(page.Rows, row(items[i:i+width]))
		}
		cursor = next
		if cursor == 0 || len(page.Rows) >= count {
			break
		}
	}
	if cursor != 0 {
		page.Cursor, page.Done = strconv.FormatUint(cursor, 10), false
	}
	return nil
}

func (g *redisPager) readHash(page *RedisMembers, o RedisMembersOptions, count int) error {
	var err error
	if page.Length, err = g.length("HLEN", string(o.Key)); err != nil {
		return err
	}
	return g.scanPage(page, "HSCAN", o, count, 2, func(pair []RedisReply) RedisRow {
		field, cut, _ := redisMember(pair[0])
		row := RedisRow{Field: &field}
		row.value(pair[1])
		// A field name too long to carry cannot be sent back to name the
		// field either; the row is marked so nothing tries.
		row.Truncated = row.Truncated || cut
		return row
	})
}

// redisHashFieldTTLs adds each field's own expiry to a page of a hash, on a
// server that has such a thing. Best effort: a field's expiry is a detail of
// the row, and a server that will not say leaves the row as it is.
func redisHashFieldTTLs(ctx context.Context, client *redis.Client, profile *RedisProfile, page *RedisMembers) {
	if profile == nil || !profile.Features.HashFieldTTL || len(page.Rows) == 0 {
		return
	}
	fields := make([]string, len(page.Rows))
	for i, r := range page.Rows {
		if r.Truncated {
			return
		}
		fields[i] = string(*r.Field)
	}
	ttls, err := client.HTTL(ctx, string(page.Key), fields...).Result()
	if err != nil || len(ttls) != len(fields) {
		return
	}
	for i := range page.Rows {
		if ttls[i] >= -1 {
			ttl := ttls[i]
			page.Rows[i].TTL = &ttl
		}
	}
}

func (g *redisPager) readSet(page *RedisMembers, o RedisMembersOptions, count int) error {
	var err error
	if page.Length, err = g.length("SCARD", string(o.Key)); err != nil {
		return err
	}
	return g.scanPage(page, "SSCAN", o, count, 1, func(item []RedisReply) RedisRow {
		row := RedisRow{}
		row.value(item[0])
		return row
	})
}

// redisWholeItems is the items of a reply that can be trusted to be whole,
// in groups of width. When a reply ran out of room the last group it kept may
// have been cut to fit what room was left rather than at a member's own
// limit, so that group is left for the next page — which starts at it — to
// read properly. The first group is always kept: a page that carries nothing
// cannot move on.
func redisWholeItems(reply RedisReply, width int) []RedisReply {
	items := reply.Items[:len(reply.Items)-len(reply.Items)%width]
	if reply.Truncated && len(items) > width {
		items = items[:len(items)-width]
	}
	return items
}

// redisScoredRow is one member and its score, as the older protocol sends
// them: two strings side by side.
func redisScoredRow(pair []RedisReply) RedisRow {
	row := RedisRow{}
	row.value(pair[0])
	f, _ := parseRedisScore(redisText(pair[1].generic()))
	score := RedisScore(f)
	row.Score = &score
	return row
}

func (g *redisPager) readList(page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	offset, err := redisOffset(o.Cursor)
	if err != nil {
		return err
	}
	if page.Length, err = g.length("LLEN", key); err != nil {
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
		start = max(stop-int64(count)+1, 0)
	}
	reply, err := g.ask(redisMaxMemberBytes, "LRANGE", key, strconv.FormatInt(start, 10), strconv.FormatInt(stop, 10))
	if err != nil {
		return err
	}
	values := redisWholeItems(reply, 1)
	if o.Desc && reply.Truncated {
		// Read from the tail, the elements nearest the tail are the last in
		// the reply — the ones that were dropped. The page is asked for again
		// at a size that fits rather than served with its first rows missing.
		return g.readList(page, o, max(len(values)/2, 1))
	}
	for i := range values {
		at := i
		if o.Desc {
			at = len(values) - 1 - i
		}
		index := start + int64(at)
		row := RedisRow{Index: &index}
		row.value(values[at])
		page.Rows = append(page.Rows, row)
	}
	if next := offset + int64(len(values)); next < page.Length && len(values) > 0 {
		page.Cursor, page.Done = strconv.FormatInt(next, 10), false
	}
	return nil
}

func (g *redisPager) readZSet(page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	var err error
	if page.Length, err = g.length("ZCARD", key); err != nil {
		return err
	}
	// A search by member has to be a scan: a sorted set is ordered by score,
	// and nothing but ZSCAN looks at member names.
	if o.Match != "" {
		return g.scanPage(page, "ZSCAN", o, count, 2, redisScoredRow)
	}

	offset, err := redisOffset(o.Cursor)
	if err != nil {
		return err
	}
	bounded := o.Min != "" || o.Max != ""
	var args []string
	if bounded {
		lo, hi := "-inf", "+inf"
		for _, b := range []struct {
			given string
			into  *string
		}{{o.Min, &lo}, {o.Max, &hi}} {
			if b.given == "" {
				continue
			}
			if !redisScoreBoundRe.MatchString(b.given) {
				return fmt.Errorf("%q is not a score bound; use a number, -inf or +inf, with a leading ( to exclude it", b.given)
			}
			*b.into = b.given
		}
		args = []string{"ZRANGEBYSCORE", key, lo, hi}
		if o.Desc {
			args = []string{"ZREVRANGEBYSCORE", key, hi, lo}
		}
		args = append(args, "WITHSCORES", "LIMIT", strconv.FormatInt(offset, 10), strconv.Itoa(count))
	} else {
		verb := "ZRANGE"
		if o.Desc {
			verb = "ZREVRANGE"
		}
		args = []string{verb, key, strconv.FormatInt(offset, 10), strconv.FormatInt(offset+int64(count)-1, 10), "WITHSCORES"}
	}
	reply, err := g.ask(redisMaxMemberBytes, args...)
	if err != nil {
		return err
	}
	items := redisWholeItems(reply, 2)
	for i := 0; i+1 < len(items); i += 2 {
		page.Rows = append(page.Rows, redisScoredRow(items[i:i+2]))
	}
	next := offset + int64(len(page.Rows))
	more := next < page.Length
	if bounded {
		// The size of a score range is not known without counting it; a full
		// page is the only sign that there may be another.
		more = len(page.Rows) == count || reply.Truncated
	}
	if more && len(page.Rows) > 0 {
		page.Cursor, page.Done = strconv.FormatInt(next, 10), false
	}
	return nil
}

func (g *redisPager) readStream(page *RedisMembers, o RedisMembersOptions, count int) error {
	key := string(o.Key)
	var err error
	if page.Length, err = g.length("XLEN", key); err != nil {
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
	args := []string{"XRANGE", key, from, to, "COUNT", strconv.Itoa(count)}
	if o.Desc {
		args = []string{"XREVRANGE", key, to, from, "COUNT", strconv.Itoa(count)}
	}
	reply, err := g.ask(redisMaxMemberBytes, args...)
	if err != nil {
		return err
	}
	entries := redisStreamEntries(RedisReply{Items: redisWholeItems(reply, 1)})
	page.Rows = append(page.Rows, entries...)
	if (len(entries) == count || reply.Truncated) && len(entries) > 0 {
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
func redisStreamEntries(reply RedisReply) []RedisRow {
	out := make([]RedisRow, 0, len(reply.Items))
	for _, entry := range reply.Items {
		if len(entry.Items) < 1 {
			continue
		}
		row := RedisRow{ID: redisText(entry.Items[0].generic()), Fields: [][2]RedisBytes{}}
		if len(entry.Items) > 1 {
			pairs := entry.Items[1]
			row.Truncated = pairs.Truncated
			for i := 0; i+1 < len(pairs.Items); i += 2 {
				field, fieldCut, _ := redisMember(pairs.Items[i])
				value, valueCut, _ := redisMember(pairs.Items[i+1])
				row.Fields = append(row.Fields, [2]RedisBytes{field, value})
				row.Truncated = row.Truncated || fieldCut || valueCut
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

func (g *redisPager) readJSON(page *RedisMembers, o RedisMembersOptions) error {
	path, err := redisJSONPath(o.Path)
	if err != nil {
		return err
	}
	// The document's size is in the reply's first line, so one that is too
	// large is known to be before any of it is kept.
	reply, err := g.ask(redisMaxJSONBytes, "JSON.GET", string(o.Key), path)
	if err != nil {
		return err
	}
	text, cut, size := redisMember(reply)
	page.JSON = &RedisJSONValue{Path: path, Bytes: int(size)}
	switch {
	case reply.Type == "nil":
		page.JSON.Bytes = 0
		page.JSON.Matches = json.RawMessage("[]")
	case cut:
		page.JSON.TooLarge = true
	case !json.Valid([]byte(text)):
		return fmt.Errorf("the server's answer for %s was not JSON", path)
	default:
		page.JSON.Matches = json.RawMessage(text)
	}
	return nil
}

// RedisWhole names one value to be read in full: a string key, one field of
// a hash, one element of a list, or one member of a set or a sorted set.
type RedisWhole struct {
	Key RedisBytes
	// Field is a hash field's name; Index a list position. Neither is given
	// for a string.
	Field *RedisBytes
	Index *int64
	// Member names a member of a set or a sorted set, which has no name but
	// itself. Whoever is asking for the whole of one was shown only its
	// start, so that is what it is named by: Member is how it begins and Size
	// how many bytes it is, both as the page that cut it reported them. With
	// no Size, Member is the member, all of it.
	Member *RedisBytes
	Size   *int64
}

// redisWholeTimeout bounds the copy of one value, which may be the half
// gigabyte Redis allows a string to be.
const redisWholeTimeout = 5 * time.Minute

// RedisCopyWhole streams one value, all of it, to wherever open says.
//
// A page carries the start of a long member and says how long it really is.
// This is the other half of that: the way to get the rest. The value is
// copied from the server's reply straight through, a buffer at a time, so its
// size is the reader's concern and never this process's. open is told the
// size before the first byte, and is not called at all when there is nothing
// to send.
func RedisCopyWhole(ctx context.Context, client *redis.Client, v RedisWhole, open func(size int64) (io.Writer, error)) error {
	key := string(v.Key)
	typ, err := client.Type(ctx, key).Result()
	if err != nil {
		return RedisExplainError(ctx, client, err)
	}
	var args []string
	switch {
	case typ == "none":
		return ErrRedisKeyNotFound
	case typ == "string" && v.Field == nil && v.Index == nil:
		args = []string{"GET", key}
	case typ == "hash" && v.Field != nil:
		args = []string{"HGET", key, string(*v.Field)}
	case typ == "list" && v.Index != nil:
		args = []string{"LINDEX", key, strconv.FormatInt(*v.Index, 10)}
	case (typ == "set" || typ == "zset") && v.Member != nil:
	case typ == "string", typ == "hash", typ == "list", typ == "set", typ == "zset":
		return fmt.Errorf("say which value of the %s to read: nothing for a string, a field for a hash, a position for a list, a member for a set or a sorted set", typ)
	default:
		return fmt.Errorf("only a string, a hash field, a list element or a member of a set can be read whole; a %s has none of them", typ)
	}
	wire, err := redisDialOptions(ctx, client.Options())
	if err != nil {
		return err
	}
	defer wire.close()
	if err := wire.login(client.Options().DB); err != nil {
		return err
	}
	// A download is somebody looking, like the page it was asked from.
	wire.quiet()
	if args == nil {
		return redisCopyMember(ctx, wire, typ, v, open)
	}
	if err := wire.send(redisWholeTimeout, args...); err != nil {
		return err
	}
	header, _, err := redisReadLine(wire.rd, 64<<10)
	if err != nil {
		return err
	}
	if strings.HasPrefix(header, "-") {
		return redisServerError(strings.TrimPrefix(header, "-"))
	}
	size, err := strconv.ParseInt(strings.TrimPrefix(header, "$"), 10, 64)
	if err != nil || !strings.HasPrefix(header, "$") {
		return fmt.Errorf("the server's answer was not a value")
	}
	if size < 0 {
		// The key was there a moment ago; the field or the position is not.
		return ErrRedisMemberNotFound
	}
	out, err := open(size)
	if err != nil {
		return err
	}
	_, err = io.CopyN(out, wire.rd, size)
	return err
}

// ErrRedisMemberNotFound is a hash field, a list position or a member that
// is not there, in a key that is.
var ErrRedisMemberNotFound = errors.New("that field, position or member is not in the key; it may have been removed")

const (
	// redisMemberSearchBudget is how long a set is searched for the member a
	// download names before the search is given up.
	redisMemberSearchBudget = 30 * time.Second
	// redisMemberMatchBytes is how much of the member's beginning the server
	// is asked to match. The rest of what was given is compared here, as the
	// members go past.
	redisMemberMatchBytes = 256
)

// redisCopyMember streams one member of a set or a sorted set.
//
// A member has no name to ask for it by except itself, and the reader who
// wants the whole of one has only its beginning. So the collection is walked
// with the scan that looks at members — narrowed by the server to those that
// begin the same way — and each member that goes past is measured against
// what was asked for: the same size, the same beginning. The first that is
// both is copied straight through from the reply it arrived in. Nothing is
// held: a member that is not the one is read past, and the one that is goes to
// the writer a buffer at a time.
func redisCopyMember(ctx context.Context, wire *redisWire, typ string, v RedisWhole, open func(size int64) (io.Writer, error)) error {
	begins := string(*v.Member)
	size := int64(len(begins))
	if v.Size != nil {
		size = *v.Size
	}
	switch {
	case len(begins) > redisMaxMemberBytes:
		return fmt.Errorf("the beginning of a member is at most the %d bytes a page shows of one", redisMaxMemberBytes)
	case size < int64(len(begins)):
		return fmt.Errorf("a member cannot be shorter than its beginning: bytes is its whole size")
	case begins == "" && size > 0:
		return fmt.Errorf("say how the member begins; its size alone does not name it")
	}
	verb, width := "SSCAN", 1
	if typ == "zset" {
		// Member and score, side by side.
		verb, width = "ZSCAN", 2
	}
	match := begins
	if len(match) > redisMemberMatchBytes {
		match = match[:redisMemberMatchBytes]
	}
	pattern := redisGlobEscape(match) + "*"
	started := time.Now()
	cursor := "0"
	for {
		if err := wire.send(redisPageTimeout, verb, string(v.Key), cursor, "MATCH", pattern, "COUNT", strconv.Itoa(redisScanBatch)); err != nil {
			return err
		}
		head, err := redisReplyHeader(wire.rd, '*')
		if err != nil {
			return err
		}
		if head != 2 {
			return fmt.Errorf("the server's answer to %s was not a cursor and a list", verb)
		}
		n, err := redisReplyHeader(wire.rd, '$')
		if err != nil {
			return err
		}
		if n > 32 {
			return fmt.Errorf("the server's answer to %s carried no cursor", verb)
		}
		next := make([]byte, n+2)
		if _, err := io.ReadFull(wire.rd, next); err != nil {
			return err
		}
		cursor = string(next[:n])
		items, err := redisReplyHeader(wire.rd, '*')
		if err != nil {
			return err
		}
		for i := int64(0); i < items; i++ {
			length, err := redisReplyHeader(wire.rd, '$')
			if err != nil {
				return err
			}
			if i%int64(width) != 0 || length != size {
				if err := redisDiscard(wire.rd, length+2); err != nil {
					return err
				}
				continue
			}
			lead := make([]byte, len(begins))
			if _, err := io.ReadFull(wire.rd, lead); err != nil {
				return err
			}
			if string(lead) != begins {
				if err := redisDiscard(wire.rd, length-int64(len(lead))+2); err != nil {
					return err
				}
				continue
			}
			// The one. Whatever follows it in the reply is left unread: the
			// connection is closed when this returns.
			wire.conn.SetDeadline(time.Now().Add(redisWholeTimeout))
			out, err := open(size)
			if err != nil {
				return err
			}
			if _, err := out.Write(lead); err != nil {
				return err
			}
			_, err = io.CopyN(out, wire.rd, size-int64(len(lead)))
			return err
		}
		if cursor == "0" {
			return ErrRedisMemberNotFound
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Since(started) > redisMemberSearchBudget {
			return fmt.Errorf("the member was not found in the %s a search is given; the collection is too large to look through for it",
				redisMemberSearchBudget)
		}
	}
}

// redisReplyHeader reads the first line of an array or of a string in the
// older protocol and returns the count it declares: elements for '*', bytes
// for '$'. An error reply is returned as the server's error.
func redisReplyHeader(rd *bufio.Reader, kind byte) (int64, error) {
	line, _, err := redisReadLine(rd, 64<<10)
	if err != nil {
		return 0, err
	}
	if strings.HasPrefix(line, "-") {
		msg := strings.TrimPrefix(line, "-")
		if sentence, ok := redisExplainCluster(msg); ok {
			return 0, errors.New(sentence)
		}
		return 0, redisServerError(msg)
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(line, string(kind)), 10, 64)
	if err != nil || len(line) == 0 || line[0] != kind || n < 0 {
		return 0, fmt.Errorf("the server's answer was not the list of members that was asked for")
	}
	return n, nil
}

// redisDiscard reads past n bytes without keeping them.
func redisDiscard(rd *bufio.Reader, n int64) error {
	_, err := io.CopyN(io.Discard, rd, n)
	return err
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
	pttlCmd := redisTTLCmd(ctx, pipe, "pttl", k)
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
	if _, err := pipe.Exec(ctx); err != nil && typeCmd.Err() != nil {
		return nil, RedisExplainError(ctx, client, typeCmd.Err())
	}
	// Taken once the reply is in, not before the request went out: the
	// server's own clock was read in between, so this plus what it said is
	// left can only land on or after the moment the key expires. Taken
	// before, a key expiring on the first millisecond of the year 10000 was
	// given a date in 9999 whenever the round trip crossed a millisecond.
	read := time.Now()
	typ := typeCmd.Val()
	if typ == "none" || typ == "" {
		return nil, ErrRedisKeyNotFound
	}
	meta := &RedisKeyMeta{
		Key: key, DB: client.Options().DB, Type: typ,
		PTTL: redisTTL(pttlCmd), Unavailable: map[string]string{},
	}
	meta.TTL = redisSecondsFromMillis(meta.PTTL)
	// A moment past the year 9999 cannot be written as RFC 3339, whose years
	// have four digits, and a page given one could not parse it. Such a key
	// has its expiry in PTTL and no date.
	if at := read.UnixMilli() + meta.PTTL; meta.PTTL > 0 && at < redisYear10000Ms {
		moment := time.UnixMilli(at).UTC()
		meta.ExpiresAt = &moment
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
