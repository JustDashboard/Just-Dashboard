package proxysvc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// sshBackend is a TCP backend that greets every connection like sshd.
func sshBackend(t *testing.T) string {
	t.Helper()
	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
			conn.Close()
		}
	}()
	return backend.Addr().String()
}

// forwards reports whether the port answers with the backend's greeting,
// waiting a little for a reload to take.
func forwards(port int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 64)
			n, _ := conn.Read(buf)
			conn.Close()
			if strings.HasPrefix(string(buf[:n]), "SSH-2.0") {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// nginxDump is `nginx -T` from the running nginx.
func nginxDump(t *testing.T) string {
	t.Helper()
	out, err := hostexec.Command(context.Background(), "nginx", "-T").CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -T: %v: %s", err, out)
	}
	return string(out)
}

// Ubuntu's shape, on a real nginx: a stream saved for later forwards nothing;
// connecting drops a file into modules-enabled, nginx -T then reads the
// stream, and the port forwards; disconnecting stops it again.
func TestLiveConnectingTheStreamDirectory(t *testing.T) {
	svc, root := liveStreamNginxWith(t, func(root string) string {
		return fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\ninclude %[1]s/modules-enabled/*.conf;\nevents {}\n", root)
	})
	ctx := context.Background()
	spec := &StreamSpec{Name: "bastion", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: sshBackend(t),
		AllowFrom: []string{"127.0.0.1"}}
	saved, err := svc.ApplyStream(ctx, spec, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Validation.Note == "" {
		t.Fatal("a stream nginx does not read was tested as if it did")
	}
	if forwards(spec.Listen, time.Second) {
		t.Fatal("forwarding before the directory was connected")
	}

	plan, err := svc.PlanStreamInclude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != IncludeDropIn || plan.Path != filepath.Join(root, "modules-enabled", streamDropIn) {
		t.Fatalf("plan = %+v", plan)
	}
	res, err := svc.ApplyStreamInclude(ctx, plan.Mode, plan.Path, true)
	if err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if !res.Validation.Valid || !res.Reloaded {
		t.Fatalf("result = %+v %+v", res, res.Validation)
	}
	if out := nginxDump(t); !strings.Contains(out, "# configuration file "+filepath.Join(root, "stream.d", "bastion.conf")+":") ||
		!strings.Contains(out, "# configuration file "+plan.Path+":") {
		t.Fatalf("nginx -T does not read the stream:\n%s", out)
	}
	if !forwards(spec.Listen, 5*time.Second) {
		t.Fatal("nothing forwarded after connecting")
	}
	if status, err := svc.Streams(ctx); err != nil || !status.Included || status.Connection == nil {
		t.Fatalf("status = %+v, %v", status, err)
	}

	if _, err := svc.RemoveStreamInclude(ctx, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for forwards(spec.Listen, 0) {
		if time.Now().After(deadline) {
			t.Fatal("still forwarding after disconnecting")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(nginxDump(t), "stream.d") {
		t.Fatal("nginx still reads the stream directory")
	}
}

// A load_module after the modules directory: a drop-in there would be refused
// by nginx itself ("specified too late"), so the plan appends to nginx.conf,
// which passes.
func TestLiveConnectAppendsWhenAModuleLoadsLate(t *testing.T) {
	svc, root := liveStreamNginxWith(t, func(root string) string {
		return fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\ninclude %[1]s/modules-enabled/*.conf;\n"+
			"load_module /usr/lib/nginx/modules/ngx_stream_js_module.so;\nevents {}\n", root)
	})
	ctx := context.Background()
	dropIn := filepath.Join(root, "modules-enabled", streamDropIn)
	if err := os.WriteFile(dropIn, []byte(renderDropIn(svc.streamDir())), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runValidator(ctx, "nginx", "-t"); res.Valid || !strings.Contains(res.Output, "too late") {
		t.Fatalf("nginx took a stream block before a load_module: %+v", res)
	}
	if err := os.Remove(dropIn); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.PlanStreamInclude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != IncludeMainFile || plan.Reason != AppendModuleLoadsAfter || plan.DropIn != dropIn || !plan.KeepsCopy {
		t.Fatalf("plan = %+v", plan)
	}
	res, err := svc.ApplyStreamInclude(ctx, plan.Mode, plan.Path, true)
	if err != nil || !res.Validation.Valid || !res.Reloaded || res.Backup == "" {
		t.Fatalf("%v %+v", err, res)
	}
	if !streamIncludeFound(root, svc.streamDir()) {
		t.Fatal("not included")
	}
}

// A stream block in a file of a directory nginx.conf includes whole: a copy of
// that file beside it is a second stream block, which nginx refuses as
// duplicate. The connect keeps none, says so first, and both the stream that
// was there and the one it starts reading forward.
func TestLiveConnectKeepsNoCopyNginxWouldRead(t *testing.T) {
	existing := freeLoopbackPort(t, "tcp")
	backend := sshBackend(t)
	block := fmt.Sprintf("stream {\n    server {\n        listen 127.0.0.1:%d;\n        proxy_pass %s;\n    }\n}\n", existing, backend)
	svc, root := liveStreamNginxWith(t, func(root string) string {
		if err := os.MkdirAll(filepath.Join(root, "main.d"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "main.d", "streams"), []byte(block), 0o644); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\ninclude %[1]s/main.d/*;\n", root)
	})
	ctx := context.Background()
	file := filepath.Join(root, "main.d", "streams")
	if !forwards(existing, 5*time.Second) {
		t.Fatal("the hand-written stream does not forward to begin with")
	}
	copied := file + ".jd-stream-1.bak"
	if err := os.WriteFile(copied, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runValidator(ctx, "nginx", "-t"); res.Valid || !strings.Contains(res.Output, "duplicate") {
		t.Fatalf("nginx took a copy of the stream block beside it: %+v", res)
	}
	if err := os.Remove(copied); err != nil {
		t.Fatal(err)
	}

	spec := &StreamSpec{Name: "second", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: backend,
		AllowFrom: []string{"127.0.0.1"}}
	if _, err := svc.ApplyStream(ctx, spec, "", false); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanStreamInclude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != IncludeStreamBlock || plan.Path != file || plan.KeepsCopy ||
		!strings.Contains(strings.Join(plan.Warnings, " "), "No copy of "+file) {
		t.Fatalf("plan = %+v", plan)
	}
	res, err := svc.ApplyStreamInclude(ctx, plan.Mode, plan.Path, true)
	if err != nil || !res.Validation.Valid || !res.Reloaded || res.Backup != "" {
		t.Fatalf("%v %+v", err, res)
	}
	if copies, _ := filepath.Glob(filepath.Join(root, "main.d", "*.bak")); len(copies) != 0 {
		t.Fatalf("copies kept where nginx reads them: %v", copies)
	}
	if !forwards(spec.Listen, 5*time.Second) || !forwards(existing, time.Second) {
		t.Fatal("not both streams forward")
	}
}

// With a stream block already there, a second is "duplicate" to nginx; the
// include goes into the one that is there, and both it and a new stream
// forward.
func TestLiveConnectIntoTheStreamBlockThatIsThere(t *testing.T) {
	existing := freeLoopbackPort(t, "tcp")
	backend := sshBackend(t)
	svc, root := liveStreamNginxWith(t, func(root string) string {
		return fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\nstream {\n    server {\n        listen 127.0.0.1:%[2]d;\n        proxy_pass %[3]s;\n    }\n}\n",
			root, existing, backend)
	})
	ctx := context.Background()
	if !forwards(existing, 5*time.Second) {
		t.Fatal("the hand-written stream does not forward to begin with")
	}
	spec := &StreamSpec{Name: "second", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: backend,
		AllowFrom: []string{"127.0.0.1"}}
	if _, err := svc.ApplyStream(ctx, spec, "", false); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.PlanStreamInclude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != IncludeStreamBlock || plan.Path != filepath.Join(root, "nginx.conf") {
		t.Fatalf("plan = %+v", plan)
	}
	res, err := svc.ApplyStreamInclude(ctx, plan.Mode, plan.Path, true)
	if err != nil || !res.Validation.Valid || !res.Reloaded {
		t.Fatalf("%v %+v", err, res)
	}
	if !forwards(spec.Listen, 5*time.Second) || !forwards(existing, time.Second) {
		t.Fatal("not both streams forward")
	}
}
