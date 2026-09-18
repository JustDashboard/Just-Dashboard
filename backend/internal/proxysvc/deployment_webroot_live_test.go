package proxysvc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Exercises real nginx reloads and file serving on a private high port, without
// changing the host's nginx configuration or making a public ACME order.
func TestLiveDeploymentWebroot(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise real nginx")
	}
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, dir := range []string{"conf.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := filepath.Join(root, "nginx.conf")
	user := ""
	if os.Getuid() == 0 {
		user = "user root;\n"
	}
	content := fmt.Sprintf(`%spid %s/nginx.pid;
error_log %s/error.log;
events {}
http {
    access_log off;
    server { listen 127.0.0.1:%d default_server; server_name existing.example.test; location / { return 200 "existing"; } }
    include %s/conf.d/*.conf;
}
`, user, root, root, port, root)
	if err := os.WriteFile(config, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command(binary, "-c", config, "-g", "daemon off;")
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	})
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: time.Second}
	checkExisting := func(ctx context.Context) error {
		request, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		request.Host = "existing.example.test"
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if string(body) != "existing" {
			return fmt.Errorf("existing site changed: %s", body)
		}
		return nil
	}
	if err := waitDeploymentWebroot(context.Background(), checkExisting); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec \"$JD_TEST_NGINX_BINARY\" -c \"$JD_TEST_NGINX_CONFIG\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_NGINX_BINARY", binary)
	t.Setenv("JD_TEST_NGINX_CONFIG", config)
	service := New(root, "")
	names := []string{"fresh.example.test", "second.example.test"}
	webroot := filepath.Join(root, "webroot")
	listeners := []Listener{{Address: "127.0.0.1", Port: uint32(port), Process: "nginx", Protocol: "tcp"}}
	probe := func(ctx context.Context, domains []string) error {
		return probeDeploymentWebroot(ctx, domains, webroot, listeners)
	}
	cleanup, err := service.prepareDeploymentWebroot(context.Background(), names, webroot, listeners, probe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanup != nil {
			_ = cleanup()
		}
	})
	if err := probe(context.Background(), names); err != nil {
		t.Fatal(err)
	}
	if err := checkExisting(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	cleanup = nil
	files, err := os.ReadDir(filepath.Join(root, "conf.d"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary configuration remains: %v, %v", files, err)
	}
	if err := checkExisting(context.Background()); err != nil {
		t.Fatal(err)
	}
}
