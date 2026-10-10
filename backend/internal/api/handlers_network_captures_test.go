package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netcapture"
)

type captureFixture struct{ calls atomic.Int32 }

func (*captureFixture) Ready() error { return nil }
func (*captureFixture) Interfaces(context.Context) ([]netcapture.Interface, error) {
	return []netcapture.Interface{{Name: "lo", Index: 1, Up: true}}, nil
}
func (f *captureFixture) Capture(context.Context, netcapture.Request) (*netcapture.Result, error) {
	f.calls.Add(1)
	data := make([]byte, 24)
	binary.LittleEndian.PutUint32(data, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(data[4:], 2)
	binary.LittleEndian.PutUint16(data[6:], 4)
	binary.LittleEndian.PutUint32(data[16:], 128)
	binary.LittleEndian.PutUint32(data[20:], 1)
	packet := []byte("PRIVATE_CAPTURE_PACKET")
	record := make([]byte, 16)
	binary.LittleEndian.PutUint32(record[8:], uint32(len(packet)))
	binary.LittleEndian.PutUint32(record[12:], uint32(len(packet)))
	data = append(append(data, record...), packet...)
	digest := sha256.Sum256(data)
	return &netcapture.Result{Packets: 1, Bytes: len(data), SHA256: hex.EncodeToString(digest[:]), Artifact: data, ArtifactAvailable: true, IdentityVerified: true, StopReason: "time_limit", NativeSummary: "PRIVATE_NATIVE_TEXT"}, nil
}

func TestCaptureAPIPrivateDownloadAuditAndRedaction(t *testing.T) {
	c, s := newClient(t)
	native := &captureFixture{}
	s.modules.captureNative = native
	s.modules.captures = netcapture.New(s.Store.DB, s.modules.jobs, native.Capture)
	if err := s.modules.captures.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"PRIVATE_CAPTURE_NAME","request":{"interface":"lo","family":"inet","protocol":"udp","source":"127.0.0.2","destination":"127.0.0.3","port":53117,"packets":4,"seconds":2,"maxBytes":1024,"snapshotLength":128}}`
	w := c.do("POST", "/api/v1/network/captures/", body, nil)
	var run netcapture.Run
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &run) != nil {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err := s.modules.captures.Get(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if netcapture.Terminal(current.Status) {
			run = current
			break
		}
		time.Sleep(time.Millisecond)
	}
	if run.Status != "completed" {
		t.Fatal("capture did not finish")
	}
	base := "/api/v1/network/captures/" + run.ID
	w = c.do("GET", base+"/pcap", "", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Type") != "application/vnd.tcpdump.pcap" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Contains(w.Body.Bytes(), []byte("PRIVATE_CAPTURE_PACKET")) {
		t.Fatalf("download=%d %+v %q", w.Code, w.Header(), w.Body.Bytes())
	}
	w = c.do("GET", base+"/support", "", nil)
	if w.Code != 200 || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "PRIVATE_") || strings.Contains(w.Body.String(), "127.0.0.2") {
		t.Fatalf("support=%d %s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/v1/network/captures/", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE_CAPTURE_PACKET") {
		t.Fatal("list disclosed packet data")
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		roleClient := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "capture-"+string(role), role)}
		for _, request := range []struct{ method, path, body string }{{"GET", "/api/v1/network/captures/", ""}, {"GET", base, ""}, {"GET", base + "/pcap", ""}, {"GET", base + "/support", ""}, {"POST", "/api/v1/network/captures/", body}, {"POST", base + "/cancel", "{}"}, {"DELETE", base, ""}, {"GET", "/api/v1/jobs/" + run.JobID, ""}, {"GET", "/api/v1/jobs/" + run.JobID + "/stream", ""}, {"POST", "/api/v1/jobs/" + run.JobID + "/cancel", "{}"}} {
			if w := roleClient.do(request.method, request.path, request.body, nil); w.Code != 403 {
				t.Errorf("%s %s=%d %s", request.method, request.path, w.Code, w.Body.String())
			}
		}
		if w := roleClient.do("GET", "/api/v1/jobs/", "", nil); w.Code != 200 || strings.Contains(w.Body.String(), run.JobID) {
			t.Fatal("capture job identity leaked")
		}
	}
	var adminID int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM users WHERE username='tester'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	user, err := s.Auth.UserByID(t.Context(), adminID)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Auth.CreateAPIToken(t.Context(), user, "capture-narrowed", auth.RoleReadOnly, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	narrowed := &client{t: t, h: s.Routes()}
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, request := range []struct{ method, path, body string }{{"GET", "/api/v1/network/captures/interfaces", ""}, {"GET", base, ""}, {"GET", base + "/pcap", ""}, {"GET", base + "/support", ""}, {"POST", "/api/v1/network/captures/", body}, {"POST", base + "/cancel", "{}"}, {"DELETE", base, ""}, {"GET", "/api/v1/jobs/" + run.JobID, ""}, {"POST", "/api/v1/jobs/" + run.JobID + "/cancel", "{}"}} {
		if w := narrowed.do(request.method, request.path, request.body, headers); w.Code != 403 {
			t.Errorf("narrowed admin token %s %s=%d", request.method, request.path, w.Code)
		}
	}
	if w := c.do("POST", "/api/v1/network/captures/", strings.Replace(body, `"udp"`, `"udp or port 22"`, 1), nil); w.Code != 400 {
		t.Fatal("unsafe filter accepted")
	}
	if w := c.do("POST", "/api/v1/network/captures/", strings.Replace(body, `"snapshotLength":128`, `"snapshotLength":128,"incidentRunId":"`+strings.Repeat("a", 32)+`"`, 1), nil); w.Code != 409 {
		t.Fatal("unknown incident was associated")
	}
	if native.calls.Load() != 1 {
		t.Fatal("read, invalid filter or export launched native capture")
	}
	if w := c.do("DELETE", base, "", nil); w.Code != 204 {
		t.Fatalf("delete=%d %s", w.Code, w.Body.String())
	}
	for action, status := range map[string]int{"network.capture.create": 202, "network.capture.download": 200, "network.capture.support": 200, "network.capture.delete": 204} {
		var count int
		if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action=? AND status=?`, action, status).Scan(&count); err != nil || count == 0 {
			t.Errorf("missing audit %s: %v", action, err)
		}
	}
}

type captureWaitingFixture struct{ captureFixture }

func (f *captureWaitingFixture) Capture(ctx context.Context, _ netcapture.Request) (*netcapture.Result, error) {
	f.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestCaptureAPIGenericCancelRecordsNativeCancellation(t *testing.T) {
	c, s := newClient(t)
	native := &captureWaitingFixture{}
	s.modules.captureNative = native
	s.modules.captures = netcapture.New(s.Store.DB, s.modules.jobs, native.Capture)
	if err := s.modules.captures.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := c.do("POST", "/api/v1/network/captures/", `{"name":"cancel fixture","request":{"interface":"lo","family":"inet","protocol":"udp","packets":4,"seconds":2,"maxBytes":1024,"snapshotLength":128}}`, nil)
	var run netcapture.Run
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &run) != nil {
		t.Fatalf("launch=%d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for native.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if native.calls.Load() != 1 {
		t.Fatal("native fixture did not begin")
	}
	if w := c.do("DELETE", "/api/v1/network/captures/"+run.ID, "", nil); w.Code != 409 {
		t.Fatal("active record was deleted")
	}
	if w := c.do("POST", "/api/v1/jobs/"+run.JobID+"/cancel", `{}`, nil); w.Code != 204 {
		t.Fatalf("generic cancel=%d %s", w.Code, w.Body.String())
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err := s.modules.captures.Get(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "cancelled" {
			var count int
			if err := s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='network.capture.cancel' AND target=? AND status=204`, run.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("cancel audit=%d %v", count, err)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("generic cancel did not settle the capture lifecycle")
}
