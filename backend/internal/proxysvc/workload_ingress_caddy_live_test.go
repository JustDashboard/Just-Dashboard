package proxysvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestLiveExistingDockerCaddyContinuityAndHandoff(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for owned Docker Caddy lifecycle")
	}
	dockerBinary, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, dockerBinary, args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		raw, err := command.Output()
		if err != nil {
			t.Fatalf("owned Docker fixture: %v: %s", err, stderr.String())
		}
		return strings.TrimSpace(string(raw))
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dockerBinary, filepath.Join(bin, "docker")); err != nil {
		t.Fatal(err)
	}
	// Do not inspect or reload the host's nginx/Caddy. Docker discovery reads
	// real network ownership, while the resolver selects only this loopback edge.
	t.Setenv("PATH", bin)
	network := "jd-existing-caddy-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	docker("network", "create", network)
	t.Cleanup(func() { exec.Command(dockerBinary, "network", "rm", network).Run() })
	startApp := func(name, alias, label string) (string, string) {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "index.html"), label)
		id := docker("run", "-d", "--network", network, "--network-alias", alias, "-v", dir+":/usr/share/nginx/html:ro", "nginx:alpine")
		t.Cleanup(func() { exec.Command(dockerBinary, "rm", "-f", "-v", id).Run() })
		address := docker("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id)
		return id, address
	}
	blueID, blueIP := startApp("blue", "api", "blue")
	_, greenIP := startApp("green", "candidate-api", "green")
	workerID, workerIP := startApp("worker", "worker", "worker")
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	certServer.Close()
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"caddy-import.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, leaf.PublicKey, cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "cert.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	writeFile(t, filepath.Join(root, "key.pem"), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})))
	hash, _ := bcrypt.GenerateFromPassword([]byte("fixture-password"), bcrypt.MinCost)
	source := filepath.Join(root, "Caddyfile")
	aliasConfig := fmt.Sprintf(`{
 auto_https off
}
https://caddy-import.example.test {
 tls /fixtures/cert.pem /fixtures/key.pem
 basic_auth {
  operator %s
 }
 handle_path /api/* {
  reverse_proxy api:80 {
   header_up X-Original unchanged
   header_up Upgrade {http.request.header.Upgrade}
  }
 }
 handle_path /worker/* {
  reverse_proxy worker:80
 }
}
http://neighbor.example.test {
 respond "neighbor"
}
`, hash)
	writeFile(t, source, aliasConfig)
	id := docker("run", "-d", "--network", network, "-p", "127.0.0.1::80", "-p", "127.0.0.1::443", "-v", source+":/etc/caddy/Caddyfile:ro", "-v", root+":/fixtures:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	t.Cleanup(func() { exec.Command(dockerBinary, "rm", "-f", "-v", id).Run() })
	var inspect []ingressContainer
	if json.Unmarshal([]byte(docker("inspect", id)), &inspect) != nil || len(inspect) != 1 {
		t.Fatal("fixture ownership cannot be read")
	}
	storage := map[string]string{}
	for _, mount := range inspect[0].Mounts {
		storage[mount.Destination] = mount.Source
	}
	edge := &dockerCaddy{ID: id, Name: "owned-fixture", Source: source, Identity: routeDigest(source + "\x00" + storage["/config"] + "\x00" + storage["/data"])}
	for deadline := time.Now().Add(15 * time.Second); ; {
		if edge.configurationSynced(ctx) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Caddy did not become active")
		}
		time.Sleep(100 * time.Millisecond)
	}
	svc := New(root, source).WithIngressJournalDir(filepath.Join(root, "journals"))
	svc.ingressResolve = func(context.Context) (*dockerCaddy, error) { return edge, nil }
	targets := []ExistingIngressTarget{{Service: "api", ContainerID: blueID, Network: network, Alias: "api", Address: blueIP, ContainerPort: 80}, {Service: "worker", ContainerID: workerID, Network: network, Alias: "worker", Address: workerIP, ContainerPort: 80}}
	capture := func() ([]ExistingIngressBinding, error) {
		all, err := svc.CaptureExistingIngress(ctx, targets)
		bindings := []ExistingIngressBinding{}
		for _, binding := range all {
			if binding.ContainerID == id {
				bindings = append(bindings, binding)
			}
		}
		return bindings, err
	}
	bindings, err := capture()
	if err != nil || len(bindings) != 2 {
		t.Fatal(bindings, err)
	}
	for _, binding := range bindings {
		if binding.Status != "linked" || binding.Continuity != "network_alias" || !binding.HTTPS {
			t.Fatal(binding)
		}
	}
	identity := ingressFileIdentity(source)
	if err := svc.ApplyExistingIngress(ctx, 51, 61, bindings, targets); err != nil || string(mustIngressRead(t, source)) != aliasConfig {
		t.Fatal("stable aliases changed existing configuration", err)
	}
	// A stopped baseline keeps its exact retained network alias. Import can
	// record it, while final activation requires an actual running owner.
	docker("stop", blueID)
	targets[0].Stopped = true
	stopped, err := capture()
	if err != nil || len(stopped) != 2 {
		t.Fatal(stopped, err)
	}
	for _, binding := range stopped {
		if binding.Service == "api" && (binding.Status != "linked" || !binding.CapturedStopped || binding.TargetContainerID != blueID) {
			t.Fatal("stopped container alias provenance missing", binding)
		}
	}
	if err := svc.VerifyExistingIngress(ctx, stopped); err != nil {
		t.Fatal("verified stopped baseline rejected during review", err)
	}
	if err := svc.ApplyExistingIngress(ctx, 51, 61, stopped, targets); !errors.Is(err, ErrExistingIngressChanged) || string(mustIngressRead(t, source)) != aliasConfig {
		t.Fatal("stopped final target accepted or proxy changed", err)
	}
	docker("start", blueID)
	blueIP = docker("inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", blueID)
	targets[0].Address, targets[0].Stopped = blueIP, false
	// A second running alias owner is ambiguous even if DNS happened to return
	// the desired container during this request.
	peer, _ := startApp("peer", "api", "peer")
	ambiguous, err := capture()
	if err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, binding := range ambiguous {
		blocked = blocked || binding.Status == "blocked"
	}
	if !blocked {
		t.Fatal("shared alias was not blocked")
	}
	docker("rm", "-f", "-v", peer)
	shared := strings.Replace(aliasConfig, "reverse_proxy api:80", "reverse_proxy "+blueIP+":80", 1)
	writeFile(t, source, shared)
	if err := edge.reload(ctx); err != nil {
		t.Fatal(err)
	}
	unsafe, err := capture()
	if err != nil {
		t.Fatal(err)
	}
	blocked = false
	for _, binding := range unsafe {
		blocked = blocked || binding.Status == "blocked"
	}
	if !blocked {
		t.Fatal("nonisolated Caddy literal was not blocked before stop")
	}
	literalConfig := strings.Replace(shared, "reverse_proxy worker:80", `respond "worker"`, 1)
	writeFile(t, source, literalConfig)
	if err := edge.reload(ctx); err != nil {
		t.Fatal(err)
	}
	bindings, err = capture()
	if err != nil || len(bindings) != 1 || bindings[0].Continuity != "retarget" || bindings[0].Status != "linked" {
		t.Fatal(bindings, err)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "caddy-import.example.test"}}}
	endpoint := "https://" + docker("port", id, "443/tcp")
	get := func(path string, auth bool) (string, int) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
		request.Host = "caddy-import.example.test"
		if auth {
			request.SetBasicAuth("operator", "fixture-password")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return strings.TrimSpace(string(raw)), response.StatusCode
	}
	if body, status := get("/api/", true); status != 200 || body != "blue" {
		t.Fatal(body, status)
	}
	targets[0].Address = greenIP
	if err := svc.ApplyExistingIngress(ctx, 51, 62, bindings, targets); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(literalConfig, blueIP+":80", greenIP+":80", 1)
	if string(mustIngressRead(t, source)) != want || ingressFileIdentity(source) != identity {
		t.Fatal("surrounding Caddy bytes or bind-mounted inode changed")
	}
	if body, status := get("/api/", true); status != 200 || body != "green" {
		t.Fatal(body, status)
	}
	if _, status := get("/api/", false); status != 401 {
		t.Fatal("authentication changed", status)
	}
	if err := svc.RestoreExistingIngress(ctx, 51, 62, bindings); err != nil {
		t.Fatal(err)
	}
	if body, status := get("/api/", true); status != 200 || body != "blue" {
		t.Fatal(body, status)
	}
	reloads := 0
	svc.ingressReload = func(ctx context.Context, _ ExistingIngressBinding) error {
		reloads++
		if reloads == 1 {
			return errors.New("injected Caddy activation failure")
		}
		return edge.reload(ctx)
	}
	if err := svc.ApplyExistingIngress(ctx, 51, 63, bindings, targets); err == nil {
		t.Fatal("reload failure accepted")
	}
	svc.ingressReload = nil
	if string(mustIngressRead(t, source)) != literalConfig {
		t.Fatal("reload failure did not restore source")
	}
	if body, status := get("/api/", true); status != 200 || body != "blue" {
		t.Fatal(body, status)
	}
	alternate := strings.Replace(literalConfig, `respond "neighbor"`, `respond "unsaved neighbor"`, 1)
	writeFile(t, filepath.Join(root, "alternate.caddy"), alternate)
	if _, err := edge.command(ctx, "", "caddy", "reload", "--config", "/fixtures/alternate.caddy", "--adapter", "caddyfile"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(svc.VerifyExistingIngress(ctx, bindings), ErrExistingIngressChanged) {
		t.Fatal("unsaved active API configuration was accepted")
	}
	if string(mustIngressRead(t, source)) != literalConfig {
		t.Fatal("unsaved API conflict rewrote saved source")
	}
	t.Log("Verified real Docker Caddy alias ownership, multiple service paths, TLS/auth/rewrites, bind-mounted inode, IP handoff, rollback, failed reload compensation, and unsaved active API blocker")
}
