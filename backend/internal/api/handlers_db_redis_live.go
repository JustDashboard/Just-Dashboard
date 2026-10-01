package api

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/wsx"
)

// The two live feeds: every command the server runs (MONITOR), and every
// message published on the channels asked for.
//
// Both are sockets with an end. MONITOR costs the server throughput for as
// long as it is attached — Redis's own documentation puts it at half on a
// busy instance — and a subscription nobody is watching is a connection held
// open for nothing. So each runs for a bounded time and a bounded number of
// events, says which of the two ended it, and closes; the page reconnects if
// the operator is still there.

const (
	redisMonitorDefaultSeconds = 30
	redisMonitorMaxSeconds     = 300
	redisMonitorDefaultEvents  = 2000
	redisMonitorMaxEvents      = 20000

	redisSubscribeDefaultSeconds = 300
	redisSubscribeMaxSeconds     = 1800
	redisSubscribeDefaultEvents  = 5000
	redisSubscribeMaxEvents      = 50000
	redisSubscribeMaxNames       = 20

	// redisFeedBuffer is how far the socket may fall behind the server before
	// events are dropped rather than queued. Dropping is the right failure:
	// the alternative is the server buffering them for a reader that is not
	// keeping up.
	redisFeedBuffer = 4096
)

// redisFeedBounds reads the two limits a feed runs under.
func redisFeedBounds(r *http.Request, defSeconds, maxSeconds, defEvents, maxEvents int) (time.Duration, int) {
	q := r.URL.Query()
	seconds := atoiDefault(q.Get("seconds"), defSeconds)
	if seconds < 1 {
		seconds = defSeconds
	}
	if seconds > maxSeconds {
		seconds = maxSeconds
	}
	events := atoiDefault(q.Get("max"), defEvents)
	if events < 1 {
		events = defEvents
	}
	if events > maxEvents {
		events = maxEvents
	}
	return time.Duration(seconds) * time.Second, events
}

// redisFeedEnd is the last frame of a feed.
type redisFeedEnd struct {
	// Reason is "duration" or "limit" for the two bounds, or "error" when the
	// server ended it.
	Reason string `json:"reason"`
	// Count is how many events were sent; Dropped is how many arrived while
	// the socket was too far behind to take them.
	Count   int    `json:"count"`
	Dropped int64  `json:"dropped"`
	Error   string `json:"error,omitempty"`
}

// redisFeed batches a feed onto the socket until one of its bounds is
// reached, and reports how it ended. Batching matters here for the reason it
// does for logs: one frame per event saturates the browser long before the
// network.
//
// box is the feed's own clock; live is the socket's. The end frame is only
// worth sending while the socket is still there to read it.
func redisFeed[T any](live, box context.Context, ws *wsx.Conn, kind string, in <-chan T, failed <-chan error, limit int) (int, string, error) {
	batch := make([]T, 0, 256)
	flush := time.NewTicker(150 * time.Millisecond)
	defer flush.Stop()
	sent := 0
	send := func() bool {
		if len(batch) == 0 {
			return true
		}
		if err := ws.Send(kind, batch); err != nil {
			return false
		}
		batch = batch[:0]
		return true
	}
	for {
		select {
		case <-box.Done():
			if live.Err() != nil {
				return sent, "closed", nil
			}
			send()
			return sent, "duration", nil
		case err := <-failed:
			send()
			if err != nil {
				return sent, "error", err
			}
			if live.Err() != nil {
				return sent, "closed", nil
			}
			return sent, "duration", nil
		case ev := <-in:
			batch = append(batch, ev)
			sent++
			if sent >= limit {
				send()
				return sent, "limit", nil
			}
			if len(batch) >= 256 && !send() {
				return sent, "closed", nil
			}
		case <-flush.C:
			if !send() {
				return sent, "closed", nil
			}
		}
	}
}

// handleRedisMonitor streams every command the server runs, for a bounded
// time.
func (s *Server) handleRedisMonitor(w http.ResponseWriter, r *http.Request) error {
	conn, dsn, err := s.redisRow(r)
	if err != nil {
		return err
	}
	duration, limit := redisFeedBounds(r, redisMonitorDefaultSeconds, redisMonitorMaxSeconds,
		redisMonitorDefaultEvents, redisMonitorMaxEvents)
	// Asked before the upgrade, so a server that is down or has no MONITOR is
	// an error the page can show rather than a socket that opens and ends.
	probeCtx, cancelProbe := timeoutCtx(r, 15*time.Second)
	client, err := dbx.RedisOpen(probeCtx, dsn, dbx.RedisOpenOptions{DB: dbx.RedisDSNDatabase})
	if err != nil {
		cancelProbe()
		return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	profile, err := dbx.RedisProbe(probeCtx, client)
	client.Close()
	cancelProbe()
	if err != nil {
		return redisFail(err, false)
	}
	if !profile.Features.Monitor {
		return httpx.BadRequest("this server does not offer MONITOR to this connection")
	}

	s.recordAudit(r, "database.redis.monitor.open", conn.Name, map[string]any{
		"seconds": int(duration.Seconds()), "max": limit,
	})
	ws, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer ws.Close()
	live, stop := contextWithCancel(r)
	defer stop()
	go ws.Keepalive(live)
	go ws.DrainControl(stop)
	box, cancel := context.WithTimeout(live, duration)
	defer cancel()

	events := make(chan dbx.RedisMonitorEvent, redisFeedBuffer)
	failed := make(chan error, 1)
	var dropped atomic.Int64
	go func() {
		failed <- dbx.RedisMonitor(box, dsn, func(ev dbx.RedisMonitorEvent) {
			select {
			case events <- ev:
			default:
				dropped.Add(1)
			}
		})
	}()
	ws.Send("meta", map[string]any{
		"seconds": int(duration.Seconds()), "max": limit,
		"flavor": profile.Flavor, "version": profile.Version,
	})
	count, reason, ferr := redisFeed(live, box, ws, "commands", events, failed, limit)
	// Detach from the server first: every command it runs from here on is one
	// it should not have to report.
	cancel()
	end := redisFeedEnd{Reason: reason, Count: count, Dropped: dropped.Load()}
	if ferr != nil {
		end.Error = ferr.Error()
	}
	if reason != "closed" {
		ws.Send("end", end)
	}
	return nil
}

// redisFeedNames reads the channels or patterns a subscription is for.
func redisFeedNames(values []string, what string) ([]string, error) {
	if len(values) > redisSubscribeMaxNames {
		return nil, httpx.BadRequest("at most %d %ss can be listened to at once", redisSubscribeMaxNames, what)
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || len(v) > 512 {
			return nil, httpx.BadRequest("a %s is between 1 and 512 characters", what)
		}
		out = append(out, v)
	}
	return out, nil
}

// handleRedisSubscribe streams the messages published on the channels and
// patterns named in the query string, for a bounded time.
func (s *Server) handleRedisSubscribe(w http.ResponseWriter, r *http.Request) error {
	conn, dsn, err := s.redisRow(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	channels, err := redisFeedNames(q["channel"], "channel")
	if err != nil {
		return err
	}
	patterns, err := redisFeedNames(q["pattern"], "pattern")
	if err != nil {
		return err
	}
	if len(channels) == 0 && len(patterns) == 0 {
		return httpx.BadRequest("name at least one channel or pattern to listen on")
	}
	duration, limit := redisFeedBounds(r, redisSubscribeDefaultSeconds, redisSubscribeMaxSeconds,
		redisSubscribeDefaultEvents, redisSubscribeMaxEvents)
	// Asked before the upgrade, for the reason MONITOR is: a server that is
	// down is an error the page can show.
	client, err := dbx.RedisOpen(r.Context(), dsn, dbx.RedisOpenOptions{DB: dbx.RedisDSNDatabase})
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	client.Close()

	s.recordAudit(r, "database.redis.subscribe.open", conn.Name, map[string]any{
		"channels": channels, "patterns": patterns, "seconds": int(duration.Seconds()), "max": limit,
	})
	ws, err := s.WS.Upgrade(w, r)
	if err != nil {
		return nil
	}
	defer ws.Close()
	live, stop := contextWithCancel(r)
	defer stop()
	go ws.Keepalive(live)
	go ws.DrainControl(stop)
	box, cancel := context.WithTimeout(live, duration)
	defer cancel()

	messages := make(chan dbx.RedisPubSubMessage, redisFeedBuffer)
	failed := make(chan error, 1)
	var dropped atomic.Int64
	go func() {
		failed <- dbx.RedisSubscribe(box, dsn, channels, patterns, func(m dbx.RedisPubSubMessage) {
			select {
			case messages <- m:
			default:
				dropped.Add(1)
			}
		})
	}()
	ws.Send("meta", map[string]any{
		"seconds": int(duration.Seconds()), "max": limit,
		"channels": channels, "patterns": patterns,
	})
	count, reason, ferr := redisFeed(live, box, ws, "messages", messages, failed, limit)
	cancel()
	end := redisFeedEnd{Reason: reason, Count: count, Dropped: dropped.Load()}
	if ferr != nil {
		end.Error = ferr.Error()
	}
	if reason != "closed" {
		ws.Send("end", end)
	}
	return nil
}
