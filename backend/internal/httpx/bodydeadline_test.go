package httpx

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowClient opens a connection to srv and sends the head of a POST that
// promises length bytes of JSON, then whatever the test writes.
func slowClient(t *testing.T, srv *httptest.Server, length int) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: dashboard\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", length)
	return conn
}

// answer reads the response on conn, failing the test if none comes in time.
func answer(t *testing.T, conn net.Conn, within time.Duration) (*http.Response, string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(within))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer within %s: %v", within, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func decodingServer(t *testing.T, grace time.Duration, minRate int64, handler Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(BodyDeadline(grace, minRate)(handler))
	t.Cleanup(srv.Close)
	return srv
}

func decodeName(w http.ResponseWriter, r *http.Request) error {
	var dst struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(r, &dst); err != nil {
		return err
	}
	JSON(w, http.StatusOK, dst)
	return nil
}

// A client that sends its headers and then stops, or sends a byte now and
// then, used to hold the handler for as long as it liked: the server has a
// header timeout and no other.
func TestABodyThatStopsArrivingIsCutOff(t *testing.T) {
	srv := decodingServer(t, 200*time.Millisecond, 1<<20, decodeName)

	stalled := slowClient(t, srv, 64)
	io.WriteString(stalled, `{"name":`)
	started := time.Now()
	res, body := answer(t, stalled, 5*time.Second)
	if res.StatusCode != http.StatusRequestTimeout || !strings.Contains(body, `"code":"body_timeout"`) {
		t.Errorf("a stalled body = %d %s, want 408 body_timeout", res.StatusCode, body)
	}
	if took := time.Since(started); took < 150*time.Millisecond || took > 3*time.Second {
		t.Errorf("a stalled body was cut off after %s, want about the grace of 200ms", took)
	}
	if !res.Close {
		t.Error("the connection is kept for a request whose body never arrived")
	}

	// A byte every 40 ms never goes quiet for long, and is still out of time:
	// what is bounded is the waiting in all, not the gap between two bytes.
	trickle := slowClient(t, srv, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if _, err := io.WriteString(trickle, " "); err != nil {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	}()
	started = time.Now()
	res, body = answer(t, trickle, 6*time.Second)
	if res.StatusCode != http.StatusRequestTimeout || !strings.Contains(body, "body_timeout") {
		t.Errorf("a trickled body = %d %s, want 408 body_timeout", res.StatusCode, body)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("a trickled body was cut off after %s", took)
	}
	trickle.Close()
	<-done
}

// The same wait is bounded when the handler answers without reading the body:
// net/http reads what is left of a small body before it writes the answer.
func TestARefusalIsNotHeldUpByABodyThatNeverComes(t *testing.T) {
	srv := decodingServer(t, 200*time.Millisecond, 1<<20, func(w http.ResponseWriter, r *http.Request) error {
		return ErrForbidden
	})
	conn := slowClient(t, srv, 64)
	started := time.Now()
	res, _ := answer(t, conn, 5*time.Second)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("the refusal = %d", res.StatusCode)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("the refusal waited %s for a body it never read", took)
	}
}

// Only waiting on the client is charged. A handler that takes its time before
// it reads, or after, has spent none of the client's allowance, and a body
// that has arrived whole leaves no deadline behind to cancel the handler.
func TestTheHandlersOwnTimeIsNotChargedToTheClient(t *testing.T) {
	const grace = 150 * time.Millisecond
	cancelled := make(chan error, 1)
	srv := decodingServer(t, grace, 1<<20, func(w http.ResponseWriter, r *http.Request) error {
		time.Sleep(3 * grace) // thinking, before the body is asked for
		var dst struct {
			Name string `json:"name"`
		}
		if err := DecodeJSON(r, &dst); err != nil {
			return err
		}
		time.Sleep(3 * grace) // and working, long after it has arrived
		cancelled <- r.Context().Err()
		JSON(w, http.StatusOK, dst)
		return nil
	})
	res, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"name":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"name":"ok"`) {
		t.Errorf("a slow handler = %d %s", res.StatusCode, body)
	}
	if err := <-cancelled; err != nil {
		t.Errorf("the handler's context was cancelled while it worked: %v", err)
	}
}

// What has arrived buys more waiting, so a large body on a slow link is not
// cut for being large: here four seconds' worth of allowance at the minimum
// rate, sent in pieces that together wait longer than the grace alone.
func TestALargeBodyEarnsItsTime(t *testing.T) {
	const grace = 150 * time.Millisecond
	srv := decodingServer(t, grace, 1024, decodeName) // 1 KB buys a second
	payload := `{"name":"` + strings.Repeat("a", 4096) + `"}`
	conn := slowClient(t, srv, len(payload))
	for sent := 0; sent < len(payload); {
		end := min(sent+1024, len(payload))
		io.WriteString(conn, payload[sent:end])
		sent = end
		time.Sleep(100 * time.Millisecond) // 400 ms of waiting in all, past the grace
	}
	res, body := answer(t, conn, 5*time.Second)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"aaaa`) {
		t.Errorf("a body that kept arriving = %d %.80s", res.StatusCode, body)
	}
}

// A handler that never reads a small body and answers while it works on is
// left alone: its body is read to the end by net/http, which then watches the
// connection for the client leaving, and nothing of the body's allowance is
// standing on the connection to be mistaken for that.
func TestAHandlerThatIgnoresItsBodyIsNotDisturbed(t *testing.T) {
	const grace = 150 * time.Millisecond
	cancelled := make(chan error, 1)
	srv := decodingServer(t, grace, 1<<20, func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusAccepted)
		w.(http.Flusher).Flush()
		time.Sleep(4 * grace)
		cancelled <- r.Context().Err()
		return nil
	})
	res, err := http.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	if err := <-cancelled; err != nil {
		t.Errorf("the handler's context was cancelled %s after a body it never read: %v", 4*grace, err)
	}
}

// A recorder, and a request the dashboard makes of itself, take no deadline.
// The body is read as it always was.
func TestABodyWithNoConnectionUnderItIsReadAsBefore(t *testing.T) {
	h := BodyDeadline(time.Millisecond, 1)(Handler(decodeName))
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("= %d %s", rec.Code, rec.Body.String())
	}
}
