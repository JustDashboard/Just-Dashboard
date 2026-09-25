package proxysvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var ErrRouteRecovery = errors.New("deployment route recovery failed")

type DeploymentRoute struct {
	Name       string   `json:"name"`
	Domains    []string `json:"domains"`
	Upstream   string   `json:"upstream"`
	TLS        bool     `json:"tls,omitempty"`
	CertPath   string   `json:"certPath,omitempty"`
	KeyPath    string   `json:"keyPath,omitempty"`
	ForceHTTPS bool     `json:"forceHttps,omitempty"`
	// BasicAuth puts a password in front of the route. Each entry is a user
	// and a bcrypt hash, which is what both proxies read.
	BasicAuth []BasicAuthUser `json:"basicAuth,omitempty"`
	// AccessLog asks the ingress to record what this route served. nginx has
	// always done it; the Docker Caddy driver did not, which is why a
	// deployment behind Caddy could not answer "is anyone using it" at all.
	AccessLog bool `json:"accessLog,omitempty"`
	// MaxBodyMB is the largest request body the route lets through, in
	// megabytes. Zero keeps each proxy's deployment default: nginx's own is
	// 1 MB, which refused a phone photo before the application saw it, so
	// its routes get DefaultDeploymentMaxBodyMB; Caddy sets no limit.
	MaxBodyMB int `json:"maxBodyMb,omitempty"`
}

// DefaultDeploymentMaxBodyMB is the request-body ceiling an nginx
// deployment route gets when the plan names none.
const DefaultDeploymentMaxBodyMB = 64

type BasicAuthUser struct {
	Username string `json:"username"`
	Hash     string `json:"hash"`
}

// deploymentAuthFileName is the htpasswd file a protected nginx route reads,
// named after the route so removal finds it again.
func deploymentAuthFileName(routeName string) string {
	return strings.TrimSuffix(routeName, ".conf") + ".htpasswd"
}

// deploymentAuthFile is where the route's credentials live on this host, or
// nothing for a route that asks for none.
func (s *Service) deploymentAuthFile(route DeploymentRoute) string {
	if len(route.BasicAuth) == 0 {
		return ""
	}
	return filepath.Join(s.authDir(), deploymentAuthFileName(route.Name))
}

// writeDeploymentAuthFile writes the route's credentials, or removes the
// file a previous protected route left when the route asks for none, so a
// stale file never outlives the plan that wrote it.
func (s *Service) writeDeploymentAuthFile(route DeploymentRoute) error {
	path := filepath.Join(s.authDir(), deploymentAuthFileName(route.Name))
	if !authFileRe.MatchString(deploymentAuthFileName(route.Name)) {
		return errors.New("invalid deployment route name")
	}
	if len(route.BasicAuth) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	lines := make([]string, 0, len(route.BasicAuth))
	for _, user := range route.BasicAuth {
		if !authUserRe.MatchString(user.Username) || !bcryptHashRe.MatchString(user.Hash) {
			return errors.New("invalid deployment route credentials")
		}
		lines = append(lines, user.Username+":"+user.Hash)
	}
	return s.writeAuthFile(path, lines)
}

type DeploymentRouteSnapshot struct {
	IngressIdentity string `json:"ingressIdentity,omitempty"`
	Driver          string `json:"driver,omitempty"`
	ContainerID     string `json:"containerId,omitempty"`
	Version         int    `json:"version"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	Existed         bool   `json:"existed"`
	Mode            uint32 `json:"mode,omitempty"`
	Content         string `json:"content,omitempty"`
	ContentDigest   string `json:"contentDigest"`
	LinkPath        string `json:"linkPath,omitempty"`
	LinkExisted     bool   `json:"linkExisted"`
	LinkTarget      string `json:"linkTarget,omitempty"`
}

type DeploymentRouteResult struct {
	Snapshot  DeploymentRouteSnapshot `json:"snapshot"`
	Applied   bool                    `json:"applied"`
	Recovered bool                    `json:"recovered"`
	Verified  bool                    `json:"verified"`
}

// ApplyDeploymentRoute atomically snapshots, validates, reloads and verifies
// a deployment-owned nginx route. If apply or reload fails, the exact prior
// bytes/link are restored and reloaded before the failure is returned.
func (s *Service) ApplyDeploymentRoute(ctx context.Context, route DeploymentRoute) (DeploymentRouteResult, error) {
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return DeploymentRouteResult{}, err
	} else if edge != nil {
		return s.applyDockerCaddyRoute(ctx, edge, route)
	}
	s.mu.Lock()
	edge, provisionErr := s.provisionIngress(ctx)
	s.mu.Unlock()
	if provisionErr != nil {
		return DeploymentRouteResult{}, provisionErr
	}
	if edge != nil {
		return s.applyDockerCaddyRoute(ctx, edge, route)
	}
	spec := deploymentSiteSpec(route, s.deploymentAuthFile(route))
	content, err := RenderNginx(spec)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotDeploymentRouteLocked(route.Name)
	if err != nil {
		return DeploymentRouteResult{}, err
	}
	result := DeploymentRouteResult{Snapshot: snapshot}
	if err := s.writeDeploymentAuthFile(route); err != nil {
		return result, err
	}
	if _, err := s.applySiteLocked(ctx, spec, content, true, true, true); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		restoreErr := s.restoreDeploymentRouteLocked(recoveryCtx, snapshot)
		cancel()
		result.Recovered = restoreErr == nil
		if restoreErr != nil {
			return result, fmt.Errorf("%w: apply: %v; restore: %v", ErrRouteRecovery, err, restoreErr)
		}
		return result, err
	}
	result.Applied = true
	result.Verified = s.routeMatchesLocked(route.Name, content, true)
	if !result.Verified {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		restoreErr := s.restoreDeploymentRouteLocked(recoveryCtx, snapshot)
		cancel()
		result.Recovered = restoreErr == nil
		if restoreErr != nil {
			return result, fmt.Errorf("%w: applied route did not verify and prior route could not be restored: %v", ErrRouteRecovery, restoreErr)
		}
		return result, errors.New("applied deployment route did not match its rendered specification")
	}
	return result, nil
}

func (s *Service) RestoreDeploymentRoute(ctx context.Context, snapshot DeploymentRouteSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.Driver == "docker-caddy" {
		edge, err := s.dockerCaddy(ctx)
		if err != nil {
			return err
		}
		if edge == nil {
			return ErrRouteRecovery
		}
		if err := edge.configurationSynced(ctx); err != nil {
			return err
		}
		return edge.restore(ctx, snapshot)
	}
	return s.restoreDeploymentRouteLocked(ctx, snapshot)
}

// RemoveDeploymentRoute uses the same snapshot/reload recovery boundary as a
// cutover. It is intentionally name-scoped so preview cleanup cannot select
// or remove a route owned by another feature.
func (s *Service) RemoveDeploymentRoute(ctx context.Context, name string) error {
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return err
	} else if edge != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		snapshot, err := edge.snapshot(ctx, name)
		if err != nil {
			return err
		}
		if !snapshot.Existed {
			return nil
		}
		if !strings.HasPrefix(snapshot.Content, "# Managed by Just Dashboard\n") {
			return ErrRouteRecovery
		}
		if err := edge.configurationSynced(ctx); err != nil {
			return err
		}
		empty := snapshot
		empty.Existed = false
		empty.Content = ""
		empty.ContentDigest = routeDigest("")
		if err := edge.restore(ctx, empty); err != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			return errors.Join(err, edge.restore(recovery, snapshot))
		}
		// The request record goes with the route. It holds client addresses,
		// so leaving it behind after the deployment it described is gone would
		// keep personal data on the host with nothing left to read it.
		edge.removeAccessLog(ctx, name)
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.snapshotDeploymentRouteLocked(name)
	if err != nil {
		return err
	}
	if !snapshot.Existed && !snapshot.LinkExisted {
		return nil
	}
	empty := snapshot
	empty.Existed = false
	empty.LinkExisted = false
	empty.Content = ""
	empty.ContentDigest = routeDigest("")
	if err = s.restoreDeploymentRouteLocked(ctx, empty); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		recoveryErr := s.restoreDeploymentRouteLocked(recoveryCtx, snapshot)
		cancel()
		if recoveryErr != nil {
			return fmt.Errorf("%w: remove: %v; restore: %v", ErrRouteRecovery, err, recoveryErr)
		}
		return err
	}
	// The credentials file is the route's own; nothing else reads it.
	if err := os.Remove(filepath.Join(s.authDir(), deploymentAuthFileName(name))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Service) VerifyDeploymentRoute(ctx context.Context, route DeploymentRoute) error {
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return err
	} else if edge != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		target, err := edge.target(ctx, route.Upstream)
		if err != nil {
			return err
		}
		want, err := renderDockerCaddyRoute(route, target.upstream())
		if err != nil {
			return err
		}
		snapshot, err := edge.snapshot(ctx, route.Name)
		if err != nil {
			return err
		}
		if !snapshot.Existed || snapshot.Content != want+target.metadata() {
			return ErrRouteRecovery
		}
		return edge.configurationSynced(ctx)
	}
	content, err := RenderNginx(deploymentSiteSpec(route, s.deploymentAuthFile(route)))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.routeMatchesLocked(route.Name, content, true) {
		return errors.New("deployment route does not match the requested target")
	}
	return nil
}

// ResolveDeploymentCertificate returns an existing certificate/key pair that
// covers every requested hostname. Deployment activation never invents paths
// or treats certbot availability as certificate evidence: issuance remains
// owned by the Certificates feature and must finish before cutover.
func (s *Service) ResolveDeploymentCertificate(ctx context.Context, domains []string) (string, string, error) {
	if len(domains) == 0 {
		return "", "", errors.New("a TLS deployment route needs at least one domain")
	}
	vhosts, _ := s.ListVHosts(ctx)
	keys := map[string]string{}
	for _, vhost := range vhosts {
		if vhost.CertPath == "" || vhost.Path == "" {
			continue
		}
		if content, err := os.ReadFile(vhost.Path); err == nil {
			if spec, _ := ParseSiteSpec(vhost.Name, string(content)); spec != nil && spec.KeyPath != "" {
				keys[vhost.CertPath] = spec.KeyPath
			}
		}
	}
	certificates, err := s.ListCertificates(ctx)
	if err != nil {
		return "", "", err
	}
	for _, certificate := range certificates {
		if certificate.Error != "" || certificate.Expired || !certificateCoversAll(certificate.Domains, domains) {
			continue
		}
		keyPath := keys[certificate.Path]
		if keyPath == "" {
			keyPath = filepath.Join(filepath.Dir(certificate.Path), "privkey.pem")
		}
		if _, err := os.Stat(certificate.Path); err != nil {
			continue
		}
		if _, err := os.Stat(keyPath); err != nil {
			continue
		}
		return certificate.Path, keyPath, nil
	}
	return "", "", errors.New("no available certificate covers every deployment domain")
}

func certificateCoversAll(names, domains []string) bool {
	for _, domain := range domains {
		covered := false
		for _, name := range names {
			name, domain = strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(domain))
			if name == domain || (strings.HasPrefix(name, "*.") && wildcardCertificateCovers(name, domain)) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func wildcardCertificateCovers(pattern, domain string) bool {
	suffix := strings.TrimPrefix(pattern, "*")
	if !strings.HasSuffix(domain, suffix) {
		return false
	}
	prefix := strings.TrimSuffix(domain, suffix)
	return prefix != "" && !strings.Contains(prefix, ".")
}

func deploymentSiteSpec(route DeploymentRoute, authFile string) *SiteSpec {
	spec := &SiteSpec{
		Name: route.Name, Domains: append([]string(nil), route.Domains...), Kind: "proxy", Upstream: route.Upstream,
		TLS: route.TLS, CertPath: route.CertPath, KeyPath: route.KeyPath, ForceHTTPS: route.ForceHTTPS,
		ManagedACME: true,
		HTTP2:       route.TLS,
		WebSockets:  true, Gzip: true, SecurityHeaders: true, AccessLog: true,
		AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
		ClientMaxBody: deploymentBodyLimit(route),
	}
	if authFile != "" {
		spec.BasicAuthFile, spec.BasicAuthRealm = authFile, "Protected deployment"
	}
	return spec
}

// deploymentBodyLimit is the route's nginx client_max_body_size.
func deploymentBodyLimit(route DeploymentRoute) string {
	limit := route.MaxBodyMB
	if limit <= 0 {
		limit = DefaultDeploymentMaxBodyMB
	}
	return strconv.Itoa(limit) + "m"
}

func (s *Service) snapshotDeploymentRouteLocked(name string) (DeploymentRouteSnapshot, error) {
	if !siteNameRe.MatchString(name) {
		return DeploymentRouteSnapshot{}, errors.New("invalid deployment route name")
	}
	path, link, err := s.deploymentRoutePaths(name)
	if err != nil {
		return DeploymentRouteSnapshot{}, err
	}
	snapshot := DeploymentRouteSnapshot{Version: 1, Name: name, Path: path, LinkPath: link}
	if raw, readErr := os.ReadFile(path); readErr == nil {
		snapshot.Existed, snapshot.Content = true, string(raw)
		info, statErr := os.Stat(path)
		if statErr != nil {
			return DeploymentRouteSnapshot{}, statErr
		}
		snapshot.Mode = uint32(info.Mode().Perm())
	} else if !os.IsNotExist(readErr) {
		return DeploymentRouteSnapshot{}, readErr
	}
	snapshot.ContentDigest = routeDigest(snapshot.Content)
	if link != "" {
		if target, linkErr := os.Readlink(link); linkErr == nil {
			snapshot.LinkExisted, snapshot.LinkTarget = true, target
		} else if !os.IsNotExist(linkErr) {
			return DeploymentRouteSnapshot{}, fmt.Errorf("deployment route enable path is not a symlink")
		}
	}
	return snapshot, nil
}

func (s *Service) deploymentRoutePaths(name string) (string, string, error) {
	available := filepath.Join(s.nginxDir, "sites-available", name)
	link := filepath.Join(s.nginxDir, "sites-enabled", name)
	if _, err := os.Stat(filepath.Dir(available)); err != nil {
		available, link = s.confdPath(name), ""
		if _, err := os.Stat(filepath.Dir(available)); err != nil {
			return "", "", fmt.Errorf("%s has no supported nginx site directory", s.nginxDir)
		}
	}
	full, err := s.allowedPath(available)
	return full, link, err
}

func (s *Service) restoreDeploymentRouteLocked(ctx context.Context, snapshot DeploymentRouteSnapshot) error {
	if snapshot.Version != 1 || snapshot.Name == "" || routeDigest(snapshot.Content) != snapshot.ContentDigest {
		return errors.New("deployment route snapshot is malformed")
	}
	path, link, err := s.deploymentRoutePaths(snapshot.Name)
	if err != nil || path != snapshot.Path || link != snapshot.LinkPath {
		return errors.New("deployment route location changed since snapshot")
	}
	if snapshot.Existed {
		if err := writeAtomic(path, snapshot.Content); err != nil {
			return err
		}
		if err := os.Chmod(path, os.FileMode(snapshot.Mode)); err != nil {
			return err
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if link != "" {
		if info, statErr := os.Lstat(link); statErr == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return errors.New("deployment route enable path became a regular file")
			}
			if err := os.Remove(link); err != nil {
				return err
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		if snapshot.LinkExisted {
			if err := os.Symlink(snapshot.LinkTarget, link); err != nil {
				return err
			}
		}
	}
	if validation := runValidator(ctx, "nginx", "-t"); !validation.Valid {
		return fmt.Errorf("restored nginx configuration did not validate: %s", validation.Output)
	}
	output, reloadErr := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
	if reloadErr != nil {
		return fmt.Errorf("restored nginx configuration did not reload: %s", strings.TrimSpace(string(output)))
	}
	if snapshot.Existed && !s.routeMatchesLocked(snapshot.Name, snapshot.Content, snapshot.LinkExisted) {
		return errors.New("restored deployment route bytes did not verify")
	}
	if snapshot.Existed {
		info, err := os.Stat(path)
		if err != nil || uint32(info.Mode().Perm()) != snapshot.Mode {
			return errors.New("restored deployment route mode did not verify")
		}
	}
	if !snapshot.Existed {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return errors.New("new deployment route remained after recovery")
		}
	}
	if link != "" {
		if snapshot.LinkExisted {
			target, err := os.Readlink(link)
			if err != nil || target != snapshot.LinkTarget {
				return errors.New("restored deployment route symlink target did not verify")
			}
		} else if _, err := os.Lstat(link); !os.IsNotExist(err) {
			return errors.New("deployment route enable link remained after recovery")
		}
	}
	return nil
}

func (s *Service) routeMatchesLocked(name, content string, enabled bool) bool {
	path, link, err := s.deploymentRoutePaths(name)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != content {
		return false
	}
	if link == "" {
		return true
	}
	target, err := os.Readlink(link)
	if !enabled {
		return os.IsNotExist(err)
	}
	return err == nil && target == path
}

func routeDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}
