package logdrain

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Bounds of one drain's delivery. A batch goes out a second after its first
// record or as soon as it is full; a destination that is down holds the
// queue, and past its bound the oldest records are dropped and counted.
const (
	queueLimit    = 20000
	batchRecords  = 1000
	batchBytes    = 1 << 20
	batchInterval = time.Second
	sendTimeout   = 15 * time.Second
	maxBackoff    = time.Minute
)

// shipper delivers one drain's records.
type shipper struct {
	drain   Drain
	headers []Header
	store   *Store
	client  *http.Client
	host    string
	log     *slog.Logger

	mu       sync.Mutex
	queue    []Record
	dropped  int64
	wake     chan struct{}
	stop     context.CancelFunc
	done     chan struct{}
	detached bool
}

func newShipper(row *drainRow, store *Store, client *http.Client, host string, log *slog.Logger) *shipper {
	return &shipper{drain: row.Drain, headers: row.headers, store: store, client: client, host: host, log: log, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

// wants says whether a record of this stream and project goes to the drain.
func (s *shipper) wants(stream string, projectID int64) bool {
	if !s.drain.Enabled {
		return false
	}
	streamWanted := false
	for _, candidate := range s.drain.Streams {
		if candidate == stream {
			streamWanted = true
		}
	}
	if !streamWanted {
		return false
	}
	if len(s.drain.Projects) == 0 {
		return true
	}
	for _, id := range s.drain.Projects {
		if id == projectID {
			return true
		}
	}
	return false
}

// offer queues a record without blocking the source it came from: a slow
// destination must not hold up the container whose output is being read.
func (s *shipper) offer(record Record) {
	s.mu.Lock()
	if len(s.queue) >= queueLimit {
		// The newest is what an operator looks for first; the oldest goes.
		s.queue = s.queue[1:]
		s.dropped++
	}
	s.queue = append(s.queue, record)
	full := len(s.queue) >= batchRecords
	s.mu.Unlock()
	if full {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *shipper) queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *shipper) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.stop = cancel
	go s.run(ctx)
}

func (s *shipper) close() {
	if s.stop != nil {
		s.stop()
		<-s.done
	}
}

// detach stops the shipper without a last delivery and hands back what it
// had queued, for the shipper that replaces it after an edit.
func (s *shipper) detach() ([]Record, int64) {
	s.mu.Lock()
	s.detached = true
	s.mu.Unlock()
	s.close()
	s.mu.Lock()
	defer s.mu.Unlock()
	queue, dropped := s.queue, s.dropped
	s.queue, s.dropped = nil, 0
	return queue, dropped
}

func (s *shipper) run(ctx context.Context) {
	defer close(s.done)
	backoff := time.Duration(0)
	timer := time.NewTimer(batchInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			// One last attempt for what is queued, bounded, so a restart of
			// the dashboard does not lose the last second of a deployment's
			// output when the destination is up. A shipper replaced by an
			// edit hands its queue on instead.
			s.mu.Lock()
			detached := s.detached
			s.mu.Unlock()
			if !detached {
				flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = s.deliver(flush)
				cancel()
			}
			return
		case <-s.wake:
		case <-timer.C:
		}
		if backoff > 0 {
			select {
			case <-ctx.Done():
				continue
			case <-time.After(backoff):
			}
		}
		if err := s.deliver(ctx); err != nil {
			if backoff == 0 {
				backoff = time.Second
			} else if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = 0
		}
		timer.Reset(batchInterval)
	}
}

// take removes up to one batch from the queue: at most batchRecords, and no
// more than batchBytes of message text.
func (s *shipper) take() ([]Record, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, n := 0, 0
	for n < len(s.queue) && n < batchRecords {
		size += len(s.queue[n].Message) + 256
		if size > batchBytes && n > 0 {
			break
		}
		n++
	}
	batch := append([]Record(nil), s.queue[:n]...)
	s.queue = s.queue[n:]
	dropped := s.dropped
	s.dropped = 0
	return batch, dropped
}

// requeue puts a batch that was not delivered back at the head, as far as
// the bound allows, and counts what no longer fits.
func (s *shipper) requeue(batch []Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := queueLimit - len(s.queue)
	if room < len(batch) {
		s.dropped += int64(len(batch) - room)
		batch = batch[len(batch)-max(room, 0):]
	}
	s.queue = append(batch, s.queue...)
}

// deliver sends what is queued, a batch at a time, until the queue is empty
// or a delivery fails.
func (s *shipper) deliver(ctx context.Context) error {
	for {
		batch, dropped := s.take()
		if len(batch) == 0 {
			if dropped > 0 {
				s.store.recordDelivery(context.Background(), s.drain.ID, 0, dropped, fmt.Sprintf("%d records were dropped while the destination did not take them", dropped), time.Now())
			}
			return nil
		}
		err := s.send(ctx, batch)
		if err != nil {
			s.requeue(batch)
			s.mu.Lock()
			s.dropped += dropped
			s.mu.Unlock()
			s.store.recordDelivery(context.Background(), s.drain.ID, 0, 0, err.Error(), time.Now())
			if s.log != nil {
				s.log.Warn("log drain delivery failed", "drain", s.drain.Name, "records", len(batch), "err", err)
			}
			return err
		}
		s.store.recordDelivery(context.Background(), s.drain.ID, int64(len(batch)), dropped, "", time.Now())
	}
}

// send makes one request with a batch, gzip-compressed.
func (s *shipper) send(ctx context.Context, batch []Record) error {
	return deliverBatch(ctx, s.client, s.drain.Kind, s.drain.Endpoint, s.headers, batch, s.host)
}

func deliverBatch(ctx context.Context, client *http.Client, kind, endpoint string, headers []Header, batch []Record, host string) error {
	encoded, err := encode(kind, batch, host)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(encoded.Body); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointFor(kind, endpoint), &compressed)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", encoded.ContentType)
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("User-Agent", "Just-Dashboard")
	for _, header := range headers {
		request.Header.Set(header.Name, header.Value)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("the destination could not be reached: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		message := strings.Join(strings.Fields(string(raw)), " ")
		if len(message) > 200 {
			message = message[:200] + "…"
		}
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("the destination refused the credentials (%d): %s", response.StatusCode, message)
		case http.StatusNotFound:
			return fmt.Errorf("the destination has nothing at this address (404); check the endpoint's path: %s", message)
		default:
			return fmt.Errorf("the destination answered %d: %s", response.StatusCode, message)
		}
	}
	return nil
}

// newClient never follows a redirect: a redirect is where a header would
// leave for an address nobody chose.
func newClient() *http.Client {
	return &http.Client{Timeout: sendTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// hostName is this server's name, as each record's resource says it.
func hostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	// The dashboard's container runs on the host's network, which gives it
	// the host's own name rather than a container id.
	return name
}
