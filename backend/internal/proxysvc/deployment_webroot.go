package proxysvc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// /srv is shared at the same path by the backend container and host nginx.
// Challenge tokens need no access to the dashboard's private state directory.
const deploymentACMEWebroot = "/srv/just-dashboard-acme"

type deploymentHTTPChallenge struct {
	method    string
	listeners []Listener
}

func chooseDeploymentHTTPChallenge(plugins map[string]bool, listeners []Listener, nginxAvailable bool) (deploymentHTTPChallenge, error) {
	var httpListeners []Listener
	for _, listener := range listeners {
		if listener.Protocol != "tcp" || listener.Port != 80 {
			continue
		}
		httpListeners = append(httpListeners, listener)
		if listener.Process != "nginx" {
			owner := listener.Process
			if owner == "" {
				owner = "an unidentified process"
			}
			return deploymentHTTPChallenge{}, fmt.Errorf("port 80 is already used by %s (PID %d); automatic HTTP-01 supports the managed host nginx listener. Configure challenge routing in that web server or provision a DNS-01 certificate on the Certificates page", owner, listener.PID)
		}
	}
	if len(httpListeners) > 0 {
		if !nginxAvailable || !plugins["webroot"] {
			return deploymentHTTPChallenge{}, fmt.Errorf("%w: port 80 is served by nginx; its host command and certbot's webroot plugin are required", ErrCertbotUnavailable)
		}
		return deploymentHTTPChallenge{method: "webroot", listeners: httpListeners}, nil
	}
	if plugins["standalone"] {
		return deploymentHTTPChallenge{method: "standalone"}, nil
	}
	return deploymentHTTPChallenge{}, fmt.Errorf("%w: no running HTTP server and no standalone authenticator", ErrCertbotUnavailable)
}

// The caller holds s.mu for preparation, issuance and restoration. Existing
// sites are never overwritten just to obtain a certificate for a new release.
func (s *Service) prepareDeploymentWebroot(ctx context.Context, names []string, root string, listeners []Listener, probe func(context.Context, []string) error) (func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := os.MkdirAll(filepath.Join(root, ".well-known", "acme-challenge"), 0o755); err != nil {
		return nil, err
	}
	var pending []string
	for _, name := range names {
		if probe(ctx, []string{name}) != nil {
			pending = append(pending, name)
		}
	}
	if len(pending) == 0 {
		return func() error { return nil }, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, vhost := range s.nginxVHosts() {
		if !vhost.Enabled {
			continue
		}
		for _, name := range pending {
			for _, existing := range vhost.ServerNames {
				if strings.EqualFold(existing, name) {
					return nil, fmt.Errorf("nginx site %s already serves %s but its HTTP challenge path does not serve %s; configure that site's %s location to use this webroot before deploying", vhost.Name, name, root, acmeChallengePath)
				}
			}
		}
	}
	ordered := append([]string(nil), pending...)
	sort.Strings(ordered)
	digest := sha256.Sum256([]byte(strings.Join(ordered, "\n")))
	name := fmt.Sprintf("just-dashboard-acme-%x.conf", digest[:12])
	snapshot, err := s.snapshotDeploymentRouteLocked(name)
	if err != nil {
		return nil, err
	}
	if snapshot.Existed || snapshot.LinkExisted {
		return nil, fmt.Errorf("challenge site %s already exists; inspect it in Proxy before retrying", name)
	}
	cleanup := func() error {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return s.restoreDeploymentRouteLocked(restoreCtx, snapshot)
	}
	content := renderDeploymentChallenge(pending, root, listeners)
	_, err = s.applySiteLocked(ctx, &SiteSpec{Name: name}, content, true, true, false)
	if err == nil {
		err = waitDeploymentWebroot(ctx, func(ctx context.Context) error { return probe(ctx, names) })
	}
	if err != nil {
		if restoreErr := cleanup(); restoreErr != nil {
			return nil, fmt.Errorf("challenge setup failed: %v; restore failed: %w", err, restoreErr)
		}
		return nil, fmt.Errorf("nginx could not serve the certificate challenge: %w", err)
	}
	return cleanup, nil
}

func renderDeploymentChallenge(names []string, root string, listeners []Listener) string {
	var binds []string
	seen := map[string]bool{}
	for _, listener := range listeners {
		address := listener.Address
		if address == "" || address == "*" {
			address = "0.0.0.0"
		}
		bind := net.JoinHostPort(address, strconv.Itoa(int(listener.Port)))
		if !seen[bind] {
			binds = append(binds, "    listen "+bind+";")
			seen[bind] = true
		}
	}
	return fmt.Sprintf(`# Just Dashboard temporary certificate challenge
server {
%s
    server_name %s;
    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type text/plain;
        try_files $uri =404;
    }
    location / { return 404; }
}
`, strings.Join(binds, "\n"), strings.Join(names, " "), root)
}

// A local probe checks routing and shared filesystem visibility before an ACME
// order. It does not claim to verify the public firewall or upstream NAT.
func probeDeploymentWebroot(ctx context.Context, names []string, root string, listeners []Listener) error {
	file, err := os.CreateTemp(filepath.Join(root, ".well-known", "acme-challenge"), "jd-probe-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	token := filepath.Base(file.Name())
	_, writeErr := file.WriteString(token)
	modeErr := file.Chmod(0o644)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if modeErr != nil {
		return modeErr
	}
	if closeErr != nil {
		return closeErr
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	seen := map[string]bool{}
	for _, listener := range listeners {
		address := listener.Address
		if address == "::" {
			address = "::1"
		} else if isWildcard(address) {
			address = "127.0.0.1"
		}
		endpoint := "http://" + net.JoinHostPort(address, strconv.Itoa(int(listener.Port)))
		if seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		for _, name := range names {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+acmeChallengePath+token, nil)
			if err != nil {
				return err
			}
			request.Host = name
			response, err := client.Do(request)
			if err != nil {
				return fmt.Errorf("probe %s at %s: %w", name, endpoint, err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
			response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusOK || string(body) != token {
				return fmt.Errorf("%s at %s did not return the challenge file (HTTP %d)", name, endpoint, response.StatusCode)
			}
		}
	}
	if len(seen) == 0 {
		return fmt.Errorf("no nginx HTTP listener was available to probe")
	}
	return nil
}

// nginx reload is asynchronous: the old worker can answer the first probe.
func waitDeploymentWebroot(ctx context.Context, probe func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		err := probe(ctx)
		if err == nil {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}
