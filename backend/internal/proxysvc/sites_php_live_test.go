package proxysvc

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The PHP site must send an existing script to FPM, route a missing page
// through the front controller, and refuse a missing script or Apache file.
// A cached PHP image makes this runnable locally without installing FPM on
// the host; CI runners without that image skip it.
func TestLivePHPSiteRoutesThroughFPM(t *testing.T) {
	root := liveNginx(t)
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker is unavailable for PHP-FPM")
	}
	const image = "php:8.3-fpm-alpine"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skip("the PHP-FPM test image is not cached")
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	params, err := os.ReadFile("/etc/nginx/fastcgi_params")
	if err != nil {
		t.Skip("nginx fastcgi_params is unavailable")
	}
	if err := os.WriteFile(filepath.Join(root, "fastcgi_params"), params, 0o644); err != nil {
		t.Fatal(err)
	}
	docroot := filepath.Join(root, "site")
	if err := os.Mkdir(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.php"),
		[]byte(`<?php echo "php:" . $_SERVER["REQUEST_URI"];`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, ".htaccess"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "fpm.sock")
	config := filepath.Join(root, "fpm.conf")
	content := fmt.Sprintf("[global]\ndaemonize = no\n[www]\nuser = www-data\ngroup = www-data\nlisten = %s\nlisten.mode = 0666\npm = static\npm.max_children = 2\n", socket)
	if err := os.WriteFile(config, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	container, err := exec.Command("docker", "run", "--rm", "-d", "--network", "none",
		"-v", root+":"+root, "--entrypoint", "php-fpm", image, "-F", "-y", config).CombinedOutput()
	if err != nil {
		t.Fatalf("start PHP-FPM: %v: %s", err, container)
	}
	id := strings.TrimSpace(string(container))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Fatalf("PHP-FPM did not create its socket: %s", logs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	spec := plainSpec("php", "php.test")
	spec.Kind, spec.Root, spec.PHPSocket, spec.PHPFrontController = "php", docroot, socket, true
	spec.Upstream = ""
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused PHP site: %s", result.Output)
	}
	startNginx(t, root)
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{path: "/", body: "php:/", status: http.StatusOK},
		{path: "/missing/page?x=1", body: "php:/missing/page?x=1", status: http.StatusOK},
		{path: "/missing.php", status: http.StatusNotFound},
		{path: "/.htaccess", status: http.StatusForbidden},
	} {
		response, body := siteGet(t, port, "php.test", tc.path)
		if response.StatusCode != tc.status || body != tc.body && tc.body != "" {
			t.Errorf("%s: status %d, body %q; want %d, %q", tc.path, response.StatusCode, body, tc.status, tc.body)
		}
	}
}
