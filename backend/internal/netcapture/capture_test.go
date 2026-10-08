package netcapture

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func request() Request {
	return Request{Interface: "lo", Family: "inet", Protocol: "udp", Source: "127.0.0.2", Destination: "127.0.0.3", Port: 53117, Packets: 4, Seconds: 2, MaxBytes: 1024, SnapshotLength: 128}
}
func pcap(order binary.ByteOrder, nano bool, packets ...[]byte) []byte {
	head := make([]byte, 24)
	magic := uint32(0xa1b2c3d4)
	if nano {
		magic = 0xa1b23c4d
	}
	order.PutUint32(head, magic)
	order.PutUint16(head[4:], 2)
	order.PutUint16(head[6:], 4)
	order.PutUint32(head[16:], 128)
	order.PutUint32(head[20:], 1)
	for _, body := range packets {
		record := make([]byte, 16)
		order.PutUint32(record, 1791471390)
		order.PutUint32(record[4:], 100)
		order.PutUint32(record[8:], uint32(len(body)))
		order.PutUint32(record[12:], uint32(len(body)))
		head = append(head, record...)
		head = append(head, body...)
	}
	return head
}

func TestTypedFilterAndIndependentLimits(t *testing.T) {
	r, err := Validate(request())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--signal=TERM", "--kill-after=2s", "2s", "tcpdump", "-nn", "-p", "-U", "-s", "128", "-c", "4", "-i", "lo", "-w", "-", "ip and udp and src host 127.0.0.2 and dst host 127.0.0.3 and port 53117"}
	if !reflect.DeepEqual(Argv(r), want) {
		t.Fatalf("argv=%q", Argv(r))
	}
	for _, mutate := range []func(*Request){func(r *Request) { r.Interface = "--help" }, func(r *Request) { r.Interface = "../../etc" }, func(r *Request) { r.Protocol = "tcp or port 22" }, func(r *Request) { r.Source = "localhost" }, func(r *Request) { r.Source = "127.0.0.2;true" }, func(r *Request) { r.Destination = "::ffff:127.0.0.3" }, func(r *Request) { r.Family = "inet6" }, func(r *Request) { r.Protocol = "icmp6" }, func(r *Request) { r.Protocol = "all" }, func(r *Request) { r.SnapshotLength = 0 }, func(r *Request) { r.Packets = 10001 }, func(r *Request) { r.Seconds = 121 }, func(r *Request) { r.MaxBytes = MaxArtifactBytes + 1 }, func(r *Request) { r.MaxBytes = 10 }, func(r *Request) { r.IncidentRunID = "../private" }} {
		bad := request()
		mutate(&bad)
		if _, err := Validate(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
	for _, name := range []string{"", "\tname", "bad\u0085name", string([]byte{255}), strings.Repeat("界", 34)} {
		if _, err := ValidateName(name); err == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
}

func TestPCAPStreamingCapsIntegrityAndByteOrder(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, nano := range []bool{false, true} {
			body := []byte("private original bytes")
			data := pcap(order, nano, body, body)
			r := request()
			r.Packets = 2
			stops := 0
			w := &pcapWriter{request: r, stop: func() { stops++ }}
			for _, value := range data {
				_, _ = w.Write([]byte{value})
			}
			if w.err != nil || w.packets != 2 || stops != 1 || w.reason != "packet_limit" || !bytes.Equal(w.data, data) || len(w.pending) != 0 {
				t.Fatalf("stream=%+v", w)
			}
		}
	}
	r := request()
	r.MaxBytes = 24 + 16 + 128
	w := &pcapWriter{request: r, stop: func() {}}
	data := pcap(binary.LittleEndian, false, bytes.Repeat([]byte("x"), 128), bytes.Repeat([]byte("y"), 128))
	_, _ = w.Write(data)
	if w.reason != "byte_limit" || w.packets != 1 || len(w.data) != r.MaxBytes || w.result().PartialPacket {
		t.Fatalf("byte cap=%+v", w.result())
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[0] = 0 }, func(b []byte) { b[4] = 3 }, func(b []byte) { b[16] = 255 }, func(b []byte) { binary.LittleEndian.PutUint32(b[32:], 129) }, func(b []byte) { binary.LittleEndian.PutUint32(b[36:], 0) }, func(b []byte) { binary.LittleEndian.PutUint32(b[28:], 1000000) }} {
		bad := pcap(binary.LittleEndian, false, []byte("bytes"))
		mutate(bad)
		w := &pcapWriter{request: request(), stop: func() {}}
		_, _ = w.Write(bad)
		if w.err == nil || w.result().ArtifactAvailable {
			t.Fatalf("accepted corrupt stream %x", bad)
		}
	}
	w = &pcapWriter{request: request(), stop: func() {}}
	_, _ = w.Write(data[:len(data)-1])
	if !w.result().PartialPacket || len(w.data) != 24+16+128 {
		t.Fatal("incomplete suffix was retained")
	}
	var log limitedOutput
	_, _ = log.Write(bytes.Repeat([]byte("x"), 100000))
	if log.buffer.Len() != 16384 {
		t.Fatal("stderr exceeded bound")
	}
}

func service(t *testing.T, runner Runner) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.New(slog.Default())
	s := New(st.DB, manager, runner)
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		manager.Shutdown()
		_ = st.Close()
	})
	return s, st
}
func captured() *Result {
	w := &pcapWriter{request: request(), stop: func() {}}
	_, _ = w.Write(pcap(binary.LittleEndian, false, []byte("SECRET_PACKET_PAYLOAD")))
	r := w.result()
	r.StopReason = "time_limit"
	r.IdentityVerified = true
	r.NativeSummary = "PRIVATE_NATIVE_TEXT"
	return &r
}
func waitRun(t *testing.T, s *Service, id string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if Terminal(run.Status) {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("capture did not finish")
	return Run{}
}

func TestDurableCapturePrivateArtifactSupportAndRetention(t *testing.T) {
	var calls atomic.Int32
	s, st := service(t, func(context.Context, Request) (*Result, error) { calls.Add(1); return captured(), nil })
	run, err := s.Create(t.Context(), "PRIVATE_NAME", request(), "PRIVATE_ACTOR")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitRun(t, s, run.ID)
	if finished.Status != "completed" || !finished.Result.ArtifactAvailable || len(finished.Result.Artifact) != 0 {
		t.Fatalf("record=%+v", finished)
	}
	_, artifact, err := s.Artifact(t.Context(), run.ID)
	if err != nil || !bytes.Contains(artifact, []byte("SECRET_PACKET_PAYLOAD")) {
		t.Fatalf("artifact=%q %v", artifact, err)
	}
	support, err := s.Support(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE_NAME", "PRIVATE_ACTOR", "PRIVATE_NATIVE_TEXT", "SECRET_PACKET_PAYLOAD", "127.0.0.2", "53117", `"interface"`} {
		if bytes.Contains(support, []byte(secret)) {
			t.Fatalf("support disclosed %q: %s", secret, support)
		}
	}
	reopened := New(st.DB, s.jobs, func(context.Context, Request) (*Result, error) { t.Error("read replayed capture"); return nil, nil })
	if err := reopened.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reopened.Artifact(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("read or restart launched traffic")
	}
	corrupt := append([]byte(nil), artifact...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := st.DB.Exec(`UPDATE network_packet_captures SET artifact=? WHERE id=?`, corrupt, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Artifact(t.Context(), run.ID); err == nil {
		t.Fatal("changed artifact download was permitted")
	}
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if rows, err := s.List(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("expired=%v %v", rows, err)
	}
}

func TestCancellationWaitsForCleanupAndRestartDoesNotReplay(t *testing.T) {
	entered := make(chan struct{})
	cleanup := make(chan struct{})
	s, st := service(t, func(ctx context.Context, _ Request) (*Result, error) {
		close(entered)
		<-ctx.Done()
		<-cleanup
		return captured(), ctx.Err()
	})
	run, err := s.Create(t.Context(), "cancel", request(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := s.Cancel(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if current, err := s.Get(t.Context(), run.ID); err != nil || current.Status != "cancelling" {
		t.Fatalf("cleanup=%+v %v", current, err)
	}
	if err := s.Delete(t.Context(), run.ID); !errors.Is(err, ErrRunning) {
		t.Fatal("deleted active capture")
	}
	close(cleanup)
	if ended := waitRun(t, s, run.ID); ended.Status != "cancelled" {
		t.Fatalf("ended=%+v", ended)
	}
	queued := run
	queued.ID = strings.Repeat("a", 32)
	queued.Status = "running"
	raw, _ := json.Marshal(queued)
	if _, err := st.DB.Exec(`INSERT INTO network_packet_captures(id,status,created_at,payload,artifact) VALUES(?,?,?,?,X'')`, queued.ID, queued.Status, queued.CreatedAt.UnixMilli(), string(raw)); err != nil {
		t.Fatal(err)
	}
	reopened := New(st.DB, s.jobs, func(context.Context, Request) (*Result, error) {
		t.Error("restart replayed native capture")
		return nil, nil
	})
	if err := reopened.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ended, err := reopened.Get(t.Context(), queued.ID); err != nil || ended.Status != "interrupted" || !strings.Contains(ended.Error, "cleanup") {
		t.Fatalf("restart=%+v %v", ended, err)
	}
}

func TestFailedFinalRecordingRetriesWithoutRecapture(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s, st := service(t, func(context.Context, Request) (*Result, error) {
		calls.Add(1)
		close(entered)
		<-release
		return captured(), nil
	})
	run, err := s.Create(t.Context(), "record failure", request(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := st.DB.Exec(`CREATE TRIGGER fail_capture_record BEFORE UPDATE ON network_packet_captures WHEN NEW.status='completed' BEGIN SELECT RAISE(FAIL,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		pending := len(s.pending)
		s.mu.Unlock()
		if pending == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Get(t.Context(), run.ID); err == nil {
		t.Fatal("unrecorded final result was reported durable")
	}
	if _, err := s.Create(t.Context(), "another", request(), "admin"); err == nil {
		t.Fatal("new capture proceeded over unsaved final artifact")
	}
	if _, err := st.DB.Exec(`DROP TRIGGER fail_capture_record`); err != nil {
		t.Fatal(err)
	}
	if ended := waitRun(t, s, run.ID); ended.Status != "completed" {
		t.Fatalf("retry=%+v", ended)
	}
	if _, _, err := s.Artifact(t.Context(), run.ID); err != nil || calls.Load() != 1 {
		t.Fatalf("retry recaptured=%d: %v", calls.Load(), err)
	}
}

func TestNewCaptureTablePreservesExistingInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "existing")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE network_packet_captures; INSERT INTO settings(key,value) VALUES('existing','preserved')`); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var value string
	if err := st.DB.QueryRow(`SELECT value FROM settings WHERE key='existing'`).Scan(&value); err != nil || value != "preserved" {
		t.Fatal("existing setting changed")
	}
	if _, err := st.DB.Exec(`INSERT INTO network_packet_captures(id,status,created_at,payload) VALUES('new','queued',0,'{}')`); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCountCapPreservesOldActiveRecordsAndProvisionalProgress(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	s, st := service(t, func(ctx context.Context, _ Request) (*Result, error) {
		ReportProgress(ctx, Result{CheckedAt: time.Now().UTC(), Packets: 2, Bytes: 184, ArtifactAvailable: true})
		entered <- struct{}{}
		select {
		case <-release:
			return captured(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	older := time.Now().UTC().Add(-time.Hour)
	s.now = func() time.Time { return older }
	first, err := s.Create(t.Context(), "old active", request(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	current := time.Now().UTC()
	s.mu.Lock()
	s.now = func() time.Time { return current }
	s.mu.Unlock()
	for i := range 40 {
		terminal := Run{ID: fmt.Sprintf("%032x", i+1), Name: "terminal", Request: request(), Status: "completed", CreatedAt: current.Add(time.Duration(i) * time.Millisecond)}
		payload, _ := json.Marshal(terminal)
		if _, err := st.DB.Exec(`INSERT INTO network_packet_captures(id,status,created_at,payload,artifact) VALUES(?,?,?,?,?)`, terminal.ID, terminal.Status, terminal.CreatedAt.UnixMilli(), string(payload), []byte{}); err != nil {
			t.Fatal(err)
		}
	}
	second, err := s.Create(t.Context(), "new active", request(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := s.Create(t.Context(), "excess", request(), "admin"); !errors.Is(err, ErrBusy) {
		t.Fatalf("third launch=%v", err)
	}
	runs, err := s.List(t.Context())
	if err != nil || len(runs) != MaxRetained {
		t.Fatalf("count=%d %v", len(runs), err)
	}
	progress, err := s.Get(t.Context(), first.ID)
	if err != nil || progress.Status != "running" || progress.Result == nil || progress.Result.Packets != 2 || progress.Result.ArtifactAvailable {
		t.Fatalf("provisional=%+v %v", progress, err)
	}
	if _, err := s.Get(t.Context(), second.ID); err != nil {
		t.Fatal("active record was pruned", err)
	}
	close(release)
	waitRun(t, s, first.ID)
	waitRun(t, s, second.ID)
	runs, err = s.List(t.Context())
	if err != nil || len(runs) != MaxRetained {
		t.Fatalf("final count=%d %v", len(runs), err)
	}
}
