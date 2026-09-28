package dockerx

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

// A reader that leaves a busy container's log must get the channel closed
// behind it. The log of a container without a TTY is two streams in one, split
// through a pipe each; when the stdout side stopped reading, the split blocked
// writing into it for good and held the stderr side, the channel and the
// connection with it.
func TestLogsCloseWhenTheReaderLeavesABusyContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.47/containers/busy/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"busy","Config":{"Tty":false}}`))
		case "/v1.47/containers/busy/logs":
			w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
			flusher, _ := w.(http.Flusher)
			for i := 0; r.Context().Err() == nil; i++ {
				payload := fmt.Sprintf("line %d\n", i)
				header := make([]byte, 8)
				header[0] = byte(1 + i%2)
				binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
				if _, err := w.Write(append(header, payload...)); err != nil {
					return
				}
				if flusher != nil && i%64 == 0 {
					flusher.Flush()
				}
			}
		default:
			t.Errorf("unexpected Docker request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	owner := &Client{cli: cli}

	for run := range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		ch, closer, err := owner.Logs(ctx, "busy", LogOptions{Follow: true})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		for range 50 {
			<-ch
		}
		cancel()
		closer.Close()
		deadline := time.After(time.Second)
	drain:
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					break drain
				}
			case <-deadline:
				t.Fatalf("run %d: the log channel was still open a second after the reader left", run)
			}
		}
	}
}
