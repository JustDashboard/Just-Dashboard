package dbx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The two things Redis does that are not a question and an answer: MONITOR,
// which turns a connection into a feed of every command the server runs, and
// a subscription, which turns one into a feed of published messages. Neither
// fits a request, so each is a function that runs until its context ends and
// hands what arrives to a callback. Bounding how long and how much is the
// caller's job, and the callback must not block: the server keeps sending
// whether or not anybody is reading, and what is not read piles up in its
// memory.

// RedisMonitorEvent is one command the server ran, as MONITOR reported it.
type RedisMonitorEvent struct {
	// At is the server's own clock, in seconds since the epoch.
	At float64 `json:"at"`
	DB int     `json:"db"`
	// Client is the address the command came from, or "lua" for one a script
	// issued.
	Client  string `json:"client"`
	Command string `json:"command"`
	// Args are the arguments after the command's name, written the way
	// redis-cli prints them and cut where they are long.
	Args []string `json:"args"`
}

const (
	redisMonitorMaxLine = 256 << 10
	redisMonitorMaxArg  = 512
)

// RedisMonitor runs MONITOR on a connection of its own and calls emit for
// every command until ctx ends, which is the only way it returns without an
// error.
//
// It speaks the protocol itself rather than through the client library. The
// library's MONITOR borrows a pooled connection and polls it from a goroutine
// nothing joins; this needs the opposite — one connection, owned here, closed
// the moment the context is — and the whole exchange is two commands.
func RedisMonitor(ctx context.Context, dsn string, emit func(RedisMonitorEvent)) error {
	wire, err := redisDial(ctx, dsn)
	if err != nil {
		return err
	}
	defer wire.close()
	if err := wire.authenticate(); err != nil {
		return redisLiveError(ctx, err)
	}
	started, err := wire.ask("MONITOR")
	if err != nil {
		return redisLiveError(ctx, err)
	}
	if started.Type == "error" {
		return errors.New(redisText(started.Value))
	}
	// From here the server talks and this side only listens, for as long as
	// the context lasts.
	wire.conn.SetDeadline(time.Time{})
	for {
		line, truncated, err := redisReadLine(wire.rd, redisMonitorMaxLine)
		if err != nil {
			return redisLiveError(ctx, err)
		}
		if strings.HasPrefix(line, "-") {
			return errors.New(strings.TrimPrefix(line, "-"))
		}
		if ev, ok := parseRedisMonitorLine(line, truncated); ok {
			emit(ev)
		}
	}
}

// redisLiveError reports the end of a feed. A context that ended is how a
// feed is meant to stop, and the read error it causes is not one.
func redisLiveError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// parseRedisMonitorLine reads
//
//	+1339518083.107412 [0 127.0.0.1:60866] "keys" "*"
func parseRedisMonitorLine(line string, truncated bool) (RedisMonitorEvent, bool) {
	line, ok := strings.CutPrefix(line, "+")
	if !ok {
		return RedisMonitorEvent{}, false
	}
	stamp, rest, ok := strings.Cut(line, " [")
	if !ok {
		return RedisMonitorEvent{}, false
	}
	source, rest, ok := strings.Cut(rest, "] ")
	if !ok {
		return RedisMonitorEvent{}, false
	}
	ev := RedisMonitorEvent{Args: []string{}}
	if ev.At, _ = strconv.ParseFloat(stamp, 64); ev.At == 0 {
		return RedisMonitorEvent{}, false
	}
	db, client, _ := strings.Cut(source, " ")
	ev.DB, _ = strconv.Atoi(db)
	ev.Client = client
	args, err := RedisParseCommand(rest)
	if err != nil {
		if !truncated {
			return RedisMonitorEvent{}, false
		}
		// A line cut short ends inside a quoted argument. Closing the quote
		// recovers everything before the cut — with a second quote for the
		// case where the cut fell just after a backslash, which would
		// otherwise escape the first.
		if args, err = RedisParseCommand(rest + `"`); err != nil {
			if args, err = RedisParseCommand(rest + `""`); err != nil {
				return RedisMonitorEvent{}, false
			}
		}
	}
	ev.Command = strings.ToUpper(args[0])
	for i, a := range args[1:] {
		shown := a
		if len(shown) > redisMonitorMaxArg {
			shown = redisTrimPartialRune(shown[:redisMonitorMaxArg])
		}
		text := redisEscape(shown)
		switch {
		case truncated && i == len(args)-2:
			// The line was cut inside this argument, so how long it really
			// was is not known.
			text += "…"
		case len(a) > len(shown):
			text += fmt.Sprintf("… (%d bytes)", len(a))
		}
		ev.Args = append(ev.Args, text)
	}
	return ev, true
}

// RedisPubSubMessage is one published message.
type RedisPubSubMessage struct {
	Channel RedisBytes `json:"channel"`
	// Pattern is the subscription that matched, for one made by pattern.
	Pattern string     `json:"pattern,omitempty"`
	Payload RedisBytes `json:"payload"`
	// Bytes is the payload's full size; Truncated says Payload is only the
	// start of it.
	Bytes     int  `json:"bytes"`
	Truncated bool `json:"truncated,omitempty"`
}

const redisPubSubMaxPayload = 64 << 10

// RedisSubscribe listens on channels and patterns and calls emit for every
// message until ctx ends.
func RedisSubscribe(ctx context.Context, client *redis.Client, channels, patterns []string, emit func(RedisPubSubMessage)) error {
	if len(channels) == 0 && len(patterns) == 0 {
		return fmt.Errorf("name at least one channel or pattern to listen on")
	}
	sub := client.Subscribe(ctx)
	defer sub.Close()
	if len(channels) > 0 {
		if err := sub.Subscribe(ctx, channels...); err != nil {
			return err
		}
	}
	if len(patterns) > 0 {
		if err := sub.PSubscribe(ctx, patterns...); err != nil {
			return err
		}
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		// A short wait rather than a blocking read, so a quiet channel still
		// notices the context end within a second.
		received, err := sub.ReceiveTimeout(ctx, time.Second)
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			return redisLiveError(ctx, err)
		}
		msg, ok := received.(*redis.Message)
		if !ok {
			continue
		}
		out := RedisPubSubMessage{
			Channel: RedisBytes(msg.Channel), Pattern: msg.Pattern,
			Payload: RedisBytes(msg.Payload), Bytes: len(msg.Payload),
		}
		if len(msg.Payload) > redisPubSubMaxPayload {
			out.Payload = RedisBytes(redisTrimPartialRune(msg.Payload[:redisPubSubMaxPayload]))
			out.Truncated = true
		}
		emit(out)
	}
}

// RedisPublish sends a message to a channel and reports how many subscribers
// received it.
func RedisPublish(ctx context.Context, client *redis.Client, channel, message RedisBytes) (int64, error) {
	if channel == "" {
		return 0, fmt.Errorf("a channel is required")
	}
	return client.Publish(ctx, string(channel), string(message)).Result()
}

// RedisChannel is one channel somebody is subscribed to.
type RedisChannel struct {
	Name        RedisBytes `json:"name"`
	Subscribers int64      `json:"subscribers"`
}

// RedisPubSubState is who is listening right now.
type RedisPubSubState struct {
	Channels []RedisChannel `json:"channels"`
	// ChannelsOmitted counts the channels past the first redisMaxChannels.
	ChannelsOmitted int `json:"channelsOmitted,omitempty"`
	// Patterns is how many pattern subscriptions exist across all clients.
	Patterns int64 `json:"patterns"`
}

const redisMaxChannels = 500

// RedisPubSubChannels lists the channels with at least one subscriber.
func RedisPubSubChannels(ctx context.Context, client *redis.Client, pattern string) (*RedisPubSubState, error) {
	if pattern == "" {
		pattern = "*"
	}
	names, err := client.PubSubChannels(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := &RedisPubSubState{Channels: []RedisChannel{}}
	if len(names) > redisMaxChannels {
		out.ChannelsOmitted = len(names) - redisMaxChannels
		names = names[:redisMaxChannels]
	}
	pipe := client.Pipeline()
	numpat := pipe.PubSubNumPat(ctx)
	var numsub *redis.MapStringIntCmd
	if len(names) > 0 {
		numsub = pipe.PubSubNumSub(ctx, names...)
	}
	_, _ = pipe.Exec(ctx)
	out.Patterns = numpat.Val()
	counts := map[string]int64{}
	if numsub != nil {
		counts = numsub.Val()
	}
	for _, name := range names {
		out.Channels = append(out.Channels, RedisChannel{Name: RedisBytes(name), Subscribers: counts[name]})
	}
	return out, nil
}
