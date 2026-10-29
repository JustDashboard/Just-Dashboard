package proxysvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var ErrRouteRecovery = errors.New("deployment route recovery failed")

type DeploymentRoute struct {
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	// Upstreams are the servers the route shares its requests between: one
	// per replica of the release it serves. A release of one container is
	// one server and renders exactly as a route did before replicas.
	Upstreams []string `json:"upstreams,omitempty"`
	// Upstream is the single server a route named before it could name
	// several. It is kept so a route built or decoded with it means what it
	// did; Servers is what the renderers read, and a route sets one or the
	// other.
	Upstream string `json:"upstream"`
	// Canary, while a candidate release is tried beside the live one, sends
	// a share of the route's requests to the candidate's servers.
	Canary     *DeploymentCanary `json:"canary,omitempty"`
	TLS        bool              `json:"tls,omitempty"`
	CertPath   string            `json:"certPath,omitempty"`
	KeyPath    string            `json:"keyPath,omitempty"`
	ForceHTTPS bool              `json:"forceHttps,omitempty"`
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
	// Redirects are the route's other names, each sending visitors to one of
	// Domains: the www. a bare domain keeps for old links, or the reverse.
	// They are not in Domains, which are the names that reach the
	// application, but the route's certificate covers them (Hostnames).
	Redirects []HostRedirect `json:"redirects,omitempty"`
	// ResponseHeaders are set on every answer the route gives, replacing any
	// header of the same name the application sent.
	ResponseHeaders []HeaderValue `json:"responseHeaders,omitempty"`
	// AllowFrom, when it names any address or range, is the only place the
	// route answers; everyone else is refused with 403 before the password,
	// if there is one, is asked for.
	AllowFrom []string `json:"allowFrom,omitempty"`
	// ForwardAuth has every request the route serves checked with an auth
	// server first, the way a Proxy site's single sign-on is: a visitor
	// without a session is sent to sign in, and one the server refuses, or
	// that it cannot answer for, is refused. It replaces the password, so a
	// route has one or the other. PathsOnly is not a route's to set: a
	// deployment has no paths of its own.
	ForwardAuth *SiteForwardAuth `json:"forwardAuth,omitempty"`
	// Maintenance is who still reaches the application while the route is
	// in maintenance, and how long its 503 tells clients to wait. Whether
	// the route is in maintenance is not part of it: every route carries the
	// check, and a file beside it turns it on (SetDeploymentMaintenance), so
	// it changes at once rather than with the next release.
	Maintenance DeploymentMaintenance `json:"maintenance"`
	// ErrorPages are the codes the route answers with a page of its own
	// rather than the proxy's stock one: 404, 502, 503 or 504.
	// InterceptErrors gives the application's own answers with those codes
	// the page too, not only the proxy's.
	ErrorPages      []int `json:"errorPages,omitempty"`
	InterceptErrors bool  `json:"interceptErrors,omitempty"`
	// PlainHostnames are the names, among Domains and Redirects, served over
	// plain HTTP alone while TLS is on for the rest of the route: no
	// certificate, no redirect to HTTPS and no HSTS. Each domain of a plan
	// chooses, so one HTTPS name no longer puts the others behind a
	// certificate that does not cover them. A route whose TLS is off serves
	// every name that way and lists none.
	PlainHostnames []string `json:"plainHostnames,omitempty"`
	// HSTS asks browsers to keep to HTTPS on the route's HTTPS names. Nil
	// sends no header, which is where a route starts: the policy outlives
	// a mistake for as long as it says.
	HSTS *DeploymentHSTS `json:"hsts,omitempty"`
}

// MaxDeploymentUpstreams is the most servers a route shares requests
// between: a release's replicas are at most eight.
const MaxDeploymentUpstreams = 8

// DeploymentCanary is a candidate release sharing a route with the live
// one. Percent of the requests, each given one release at random, go to
// Upstreams, the candidate's replicas; the rest to the route's own servers.
// Every answer is recorded with the release that gave it — Live or
// Candidate, the releases' ids — so the two can be read apart while they
// share the route.
type DeploymentCanary struct {
	Percent   int      `json:"percent"`
	Upstreams []string `json:"upstreams"`
	Live      string   `json:"live"`
	Candidate string   `json:"candidate"`
}

// validateDeploymentCanary holds a canary to what both renderers write.
func validateDeploymentCanary(route DeploymentRoute) error {
	canary := route.Canary
	if canary == nil {
		return nil
	}
	if canary.Percent < 1 || canary.Percent > 99 {
		return errors.New("a canary takes between 1 and 99 percent of the route's requests")
	}
	if len(canary.Upstreams) == 0 || len(canary.Upstreams) > MaxDeploymentUpstreams {
		return fmt.Errorf("a canary is between 1 and %d servers", MaxDeploymentUpstreams)
	}
	live := route.Servers()
	if len(live) == 0 {
		return errors.New("a canary shares a route that already serves a release")
	}
	seen := map[string]bool{}
	for _, upstream := range live {
		seen[upstream] = true
	}
	for _, upstream := range canary.Upstreams {
		if upstream == "" || seen[upstream] {
			return fmt.Errorf("%q cannot serve the canary and the live release at once", upstream)
		}
		seen[upstream] = true
	}
	if !splitLabelRe.MatchString(canary.Live) || !splitLabelRe.MatchString(canary.Candidate) || canary.Live == canary.Candidate {
		return errors.New("a canary's two releases need different names of letters, digits, dashes or underscores")
	}
	return nil
}

// Servers are the addresses the route forwards to, in order: Upstreams, or
// the one Upstream of a route built before replicas.
func (route DeploymentRoute) Servers() []string {
	if len(route.Upstreams) > 0 {
		return append([]string(nil), route.Upstreams...)
	}
	if route.Upstream != "" {
		return []string{route.Upstream}
	}
	return nil
}

// PrimaryUpstream is the first server, which is what a single-server reader
// asked of Upstream: the release's first replica.
func (route DeploymentRoute) PrimaryUpstream() string {
	if servers := route.Servers(); len(servers) > 0 {
		return servers[0]
	}
	return ""
}

// validateDeploymentServers refuses a pool a renderer would write wrong: both
// spellings at once, too many servers, or one server twice.
func validateDeploymentServers(route DeploymentRoute) error {
	if len(route.Upstreams) > 0 && route.Upstream != "" {
		return errors.New("a route names its servers as upstreams or as its one upstream, not both")
	}
	if len(route.Upstreams) > MaxDeploymentUpstreams {
		return fmt.Errorf("a route shares its requests between at most %d servers", MaxDeploymentUpstreams)
	}
	seen := map[string]bool{}
	for _, upstream := range route.Upstreams {
		if upstream == "" {
			return errors.New("a route's server address is empty")
		}
		if seen[upstream] {
			return fmt.Errorf("%s is named twice among the route's servers", upstream)
		}
		seen[upstream] = true
	}
	return nil
}

// DeploymentHSTS is a route's Strict-Transport-Security policy, held to the
// bounds a Proxy site's own HSTS control is: MaxAge is seconds, zero six
// months, at least five minutes and at most two years; Preload needs a year
// and IncludeSubDomains, as the browsers' list does.
type DeploymentHSTS struct {
	MaxAge            int  `json:"maxAge,omitempty"`
	IncludeSubDomains bool `json:"includeSubDomains,omitempty"`
	Preload           bool `json:"preload,omitempty"`
}

// Value is the header the policy is sent as.
func (h DeploymentHSTS) Value() string { return h.siteSpec().hstsValue() }

// siteSpec is the policy as a site's renderer and validation read it.
func (h DeploymentHSTS) siteSpec() *SiteSpec {
	return &SiteSpec{HSTS: true, HSTSMaxAge: h.MaxAge, HSTSOwnNameOnly: !h.IncludeSubDomains, HSTSPreload: h.Preload}
}

// ValidateDeploymentHSTS checks a policy by the rules a Proxy site's is
// held to, so a plan that saves is one either proxy sends.
func ValidateDeploymentHSTS(h DeploymentHSTS) error { return validTLSOptions(h.siteSpec()) }

// DeploymentMaintenance is what a route's maintenance does while it is on.
type DeploymentMaintenance struct {
	// RetryAfter is the seconds the 503 tells clients and crawlers to wait;
	// 0 sends no Retry-After.
	RetryAfter int `json:"retryAfter,omitempty"`
	// BypassFrom are the addresses and ranges that reach the application as
	// usual while the route is in maintenance.
	BypassFrom []string `json:"bypassFrom,omitempty"`
}

// MaxRetryAfter is the longest wait a maintenance 503 may ask for: a day.
const MaxRetryAfter = 86400

// ErrorPageCodes are the codes a route or a site may give a page of its own.
func ErrorPageCodes() []int { return slices.Clone(errorPageCodes) }

// Hostnames is every name the route answers, served and redirecting.
func (route DeploymentRoute) Hostnames() []string {
	names := append([]string(nil), route.Domains...)
	for _, redirect := range route.Redirects {
		names = append(names, redirect.From)
	}
	return names
}

// TLSHostnames is every name the route answers over HTTPS, served and
// redirecting, which is what its certificate has to cover.
func (route DeploymentRoute) TLSHostnames() []string {
	var names []string
	for _, name := range route.Hostnames() {
		if !route.plain(name) {
			names = append(names, name)
		}
	}
	return names
}

// plain says whether the route answers the name over plain HTTP alone.
func (route DeploymentRoute) plain(name string) bool {
	return !route.TLS || containsFold(route.PlainHostnames, name)
}

// servedByScheme splits the served names into those answered over HTTPS
// and those over plain HTTP, each in the order Domains lists them.
func (route DeploymentRoute) servedByScheme() (tls, plain []string) {
	for _, name := range route.Domains {
		if route.plain(name) {
			plain = append(plain, name)
		} else {
			tls = append(tls, name)
		}
	}
	return tls, plain
}

// Limits on a deployment route's own rules. A deployment has one route, so
// these bound a typo or a paste rather than a use case.
const (
	MaxDeploymentHeaders = 32
	MaxDeploymentAllow   = 64
)

// deploymentReservedHeaders are response headers a deployment route cannot
// set: the framing and hop-by-hop headers belong to the connection rather
// than the answer, and nginx writes Date and Server itself, so a second copy
// would be sent beside its own.
var deploymentReservedHeaders = []string{
	"Connection", "Content-Length", "Date", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Connection", "Server", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
}

// ValidateDeploymentHeaders checks a deployment route's response headers by
// the rules a Proxy site's own are held to, on the nginx spelling a
// deployment route gets — security headers on — so a plan that saves is a
// plan that renders on either proxy, whichever this host turns out to run.
func ValidateDeploymentHeaders(headers []HeaderValue) error {
	if len(headers) > MaxDeploymentHeaders {
		return fmt.Errorf("at most %d response headers", MaxDeploymentHeaders)
	}
	for _, header := range headers {
		if containsFold(deploymentReservedHeaders, header.Name) {
			return fmt.Errorf("the %s header belongs to the connection and cannot be set", header.Name)
		}
	}
	spec := deploymentSiteSpec(DeploymentRoute{ResponseHeaders: headers}, "")
	return validateHeaders(spec)
}

// NormalizeAccessEntry reads an allowed address or range the way an access
// list does, and returns it in its canonical spelling.
func NormalizeAccessEntry(raw string) (string, error) {
	entry, _, err := accessEntry(raw)
	return entry, err
}

// validateDeploymentRules checks what both proxies render from a route
// beyond its names: the redirects, the headers and the allowed addresses.
func validateDeploymentRules(route DeploymentRoute) error {
	if err := validateDeploymentServers(route); err != nil {
		return err
	}
	if err := validateDeploymentCanary(route); err != nil {
		return err
	}
	if err := validateHostRedirects(route.Domains, route.Redirects); err != nil {
		return err
	}
	if err := ValidateDeploymentHeaders(route.ResponseHeaders); err != nil {
		return err
	}
	if err := validateDeploymentSchemes(route); err != nil {
		return err
	}
	if len(route.AllowFrom) > MaxDeploymentAllow {
		return fmt.Errorf("at most %d allowed addresses", MaxDeploymentAllow)
	}
	for _, entry := range route.AllowFrom {
		if normalized, err := NormalizeAccessEntry(entry); err != nil {
			return err
		} else if normalized != entry {
			return fmt.Errorf("%s is written %s", entry, normalized)
		}
	}
	if route.ForwardAuth != nil {
		if err := ValidateDeploymentForwardAuth(*route.ForwardAuth); err != nil {
			return err
		}
		// The browser would send the password to the auth server too, which
		// reads an Authorization header as its own credentials.
		if len(route.BasicAuth) > 0 {
			return errors.New("signing in replaces the route's password — remove the password first")
		}
	}
	return validateDeploymentPages(route)
}

// validateDeploymentSchemes checks the names a TLS route serves over plain
// HTTP, each one of the route's own, and its HSTS, which only a TLS route
// has an answer to send on.
func validateDeploymentSchemes(route DeploymentRoute) error {
	if !route.TLS && (len(route.PlainHostnames) > 0 || route.HSTS != nil) {
		return errors.New("a route without TLS serves every name over plain HTTP and sends no HSTS")
	}
	seen := map[string]bool{}
	for _, name := range route.PlainHostnames {
		if !containsFold(route.Hostnames(), name) {
			return fmt.Errorf("%s is not one of the route's names, so it cannot be served over plain HTTP", name)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("%s is listed twice as a plain-HTTP name", name)
		}
		seen[strings.ToLower(name)] = true
	}
	if route.HSTS != nil {
		return ValidateDeploymentHSTS(*route.HSTS)
	}
	return nil
}

// validateDeploymentPages checks a route's maintenance and error pages by
// the rules a Proxy site's are held to.
func validateDeploymentPages(route DeploymentRoute) error {
	m := route.Maintenance
	if m.RetryAfter < 0 || m.RetryAfter > MaxRetryAfter {
		return fmt.Errorf("retry after must be between 0 and %d seconds", MaxRetryAfter)
	}
	if len(m.BypassFrom) > MaxDeploymentAllow {
		return fmt.Errorf("at most %d addresses can be let past maintenance", MaxDeploymentAllow)
	}
	for _, entry := range m.BypassFrom {
		if normalized, err := NormalizeAccessEntry(entry); err != nil {
			return err
		} else if normalized != entry {
			return fmt.Errorf("%s is written %s", entry, normalized)
		}
	}
	seen := map[int]bool{}
	for _, code := range route.ErrorPages {
		if !slices.Contains(errorPageCodes, code) {
			return fmt.Errorf("an error page can be set for 404, 502, 503 or 504, not %d", code)
		}
		if seen[code] {
			return fmt.Errorf("the %d page is listed twice", code)
		}
		seen[code] = true
	}
	if route.InterceptErrors && len(route.ErrorPages) == 0 {
		return errors.New("replacing the application's error pages needs a page to replace them with")
	}
	if route.InterceptErrors && route.ForwardAuth != nil {
		return errors.New("with the application's own error responses replaced, its own 401s would be sent to sign in — turn that off")
	}
	return nil
}

// ValidateDeploymentForwardAuth checks a deployment route's sign-in by the
// rules a Proxy site's single sign-on is held to, less what a route does not
// have: paths of its own to sign in on.
func ValidateDeploymentForwardAuth(f SiteForwardAuth) error {
	if !ValidForwardAuthProvider(f.Provider) {
		return errForwardAuthProvider
	}
	if f.PathsOnly {
		return errors.New("a deployment route signs in on every path")
	}
	if err := ValidateForwardAuthCheck(f.Verify); err != nil {
		return err
	}
	return ValidateForwardAuthSignIn(f.SignIn)
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
	if err := validateDeploymentRules(route); err != nil {
		return DeploymentRouteResult{}, err
	}
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
	spec := s.deploymentSpec(route)
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
	// Before the route that serves them, so nginx never answers with a page
	// that is not there. A page the operator wrote is kept.
	if err := s.writeMissingPages(spec); err != nil {
		return result, fmt.Errorf("the route's pages could not be written: %w", err)
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
		// So do its pages and its maintenance switch, which would otherwise
		// put the next route written under this name into maintenance.
		return edge.removePages(ctx, name)
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
	// So are its pages and its maintenance switch, which would otherwise
	// put the next route written under this name into maintenance.
	return s.removeDeploymentPagesLocked(name)
}

func (s *Service) VerifyDeploymentRoute(ctx context.Context, route DeploymentRoute) error {
	if edge, err := s.dockerCaddy(ctx); err != nil {
		return err
	} else if edge != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		want, _, err := edge.routeContent(ctx, route)
		if err != nil {
			return err
		}
		snapshot, err := edge.snapshot(ctx, route.Name)
		if err != nil {
			return err
		}
		if !snapshot.Existed || snapshot.Content != want {
			return ErrRouteRecovery
		}
		return edge.configurationSynced(ctx)
	}
	content, err := RenderNginx(s.deploymentSpec(route))
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
	pairs, err := s.coveringCertificates(ctx, domains)
	if err != nil {
		return "", "", err
	}
	if len(pairs) == 0 {
		if pair, ok := s.caddyEvidenceForEach(ctx, domains); ok {
			return pair.Path, pair.KeyPath, nil
		}
		return "", "", errors.New("no available certificate covers every deployment domain")
	}
	return pairs[0].Path, pairs[0].KeyPath, nil
}

// caddyEvidenceForEach answers for a route on the Docker Caddy ingress,
// where each name has a certificate of its own and none covers them all. It
// returns the first name's release copy once every name has one, which is
// enough because a Caddy route never reads the path: Caddy serves what it
// holds. On an nginx host that path would be served as the certificate for
// every name, so it is not offered there.
func (s *Service) caddyEvidenceForEach(ctx context.Context, domains []string) (certificatePair, bool) {
	if len(domains) < 2 {
		return certificatePair{}, false
	}
	if edge, err := s.dockerCaddy(ctx); err != nil || edge == nil {
		return certificatePair{}, false
	}
	var first certificatePair
	for index, domain := range domains {
		pairs, err := s.coveringCertificates(ctx, []string{domain})
		if err != nil {
			return certificatePair{}, false
		}
		found := false
		for _, pair := range pairs {
			if pair.CaddyEvidence() {
				if index == 0 {
					first = pair
				}
				found = true
				break
			}
		}
		if !found {
			return certificatePair{}, false
		}
	}
	return first, true
}

// certificatePair is a usable certificate with the key that goes with it.
type certificatePair struct {
	Certificate
	KeyPath string
}

// coveringCertificates lists the unexpired certificates covering every one of
// domains whose certificate and key files are both on disk, in the order the
// certificates are listed. The key is the one a site already pairs with the
// certificate, or certbot's privkey.pem beside it.
func (s *Service) coveringCertificates(ctx context.Context, domains []string) ([]certificatePair, error) {
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
		return nil, err
	}
	var pairs []certificatePair
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
		pairs = append(pairs, certificatePair{Certificate: certificate, KeyPath: keyPath})
	}
	return pairs, nil
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

// deploymentSpec is the site a route is on this host's nginx: its password
// file and the folder its pages are served from are the service's.
func (s *Service) deploymentSpec(route DeploymentRoute) *SiteSpec {
	spec := deploymentSiteSpec(route, s.deploymentAuthFile(route))
	s.SetPagesDir(spec)
	return spec
}

func deploymentSiteSpec(route DeploymentRoute, authFile string) *SiteSpec {
	spec := &SiteSpec{
		Name: route.Name, Domains: append([]string(nil), route.Domains...), Kind: "proxy", Upstream: route.PrimaryUpstream(),
		TLS: route.TLS, CertPath: route.CertPath, KeyPath: route.KeyPath, ForceHTTPS: route.ForceHTTPS,
		ManagedACME: true,
		HTTP2:       route.TLS,
		WebSockets:  true, Gzip: true, SecurityHeaders: true, AccessLog: true,
		AllowFrom: append([]string{}, route.AllowFrom...), DenyFrom: []string{}, Locations: []SiteLocation{},
		ClientMaxBody: deploymentBodyLimit(route),
		HostRedirects: append([]HostRedirect(nil), route.Redirects...),
		deployment:    true,
	}
	if pool := deploymentPool(route.Servers(), route.Canary != nil); pool != nil {
		spec.Upstream, spec.Pool = "", pool
	}
	if canary := route.Canary; canary != nil && spec.Pool != nil {
		if candidate := deploymentPool(canary.Upstreams, true); candidate != nil {
			spec.Split = &SiteSplit{Percent: canary.Percent, Servers: candidate.Servers, Live: canary.Live, Candidate: canary.Candidate}
		}
	}
	if route.TLS {
		// Each name on the scheme its domain chose: the plain ones get a
		// server of their own, and a redirecting plain name answers on
		// port 80 alone.
		_, spec.PlainDomains = route.servedByScheme()
		for index := range spec.HostRedirects {
			spec.HostRedirects[index].Plain = route.plain(spec.HostRedirects[index].From)
		}
		if route.HSTS != nil {
			spec.HSTS, spec.HSTSMaxAge = true, route.HSTS.MaxAge
			spec.HSTSOwnNameOnly, spec.HSTSPreload = !route.HSTS.IncludeSubDomains, route.HSTS.Preload
		}
	}
	if len(route.ResponseHeaders) > 0 || spec.HSTS {
		// Hidden as well as added: add_header only appends, so without the
		// hide an application that sends the header itself would answer with
		// two, where Caddy's deferred header replaces the application's. The
		// route's HSTS replaces the application's the same way: a browser
		// reads the first of two and the operator's policy is the one meant.
		headers := &SiteHeaders{Response: append([]HeaderValue(nil), route.ResponseHeaders...)}
		for _, header := range route.ResponseHeaders {
			if !containsFold(nginxHiddenHeaders, header.Name) {
				headers.Hide = append(headers.Hide, header.Name)
			}
		}
		if spec.HSTS {
			headers.Hide = append(headers.Hide, "Strict-Transport-Security")
		}
		spec.Headers = headers
	}
	if authFile != "" {
		spec.BasicAuthFile, spec.BasicAuthRealm = authFile, "Protected deployment"
	}
	if route.ForwardAuth != nil {
		// The site's own single sign-on, every path checked: the redirecting
		// names are servers of their own and answer before it, and the ACME
		// challenge is left open by the renderer, so a certificate covering
		// the route can still be issued and renewed.
		gate := *route.ForwardAuth
		spec.ForwardAuth = &gate
	}
	// Every route carries the switch, off until its file is written, so a
	// deployment can be put into maintenance without a release.
	spec.Maintenance = &SiteMaintenance{
		Switch: true, RetryAfter: route.Maintenance.RetryAfter,
		BypassFrom: append([]string{}, route.Maintenance.BypassFrom...),
	}
	spec.ErrorPages = slices.Sorted(slices.Values(route.ErrorPages))
	spec.InterceptErrors = route.InterceptErrors
	return spec
}

// deploymentPool is a replicated release's servers as an nginx upstream
// block, or nil for a release of one container, which keeps the route it
// always had: proxy_pass to the one address, with no upstream block —
// unless always, for a canary, whose two releases are a pool each.
//
// Least connections rather than round robin, because a route carries
// WebSockets and long streamed answers: a replica holding them is not the
// one a new request should wait behind. Health is nginx's passive kind, the
// only one it has, written out rather than left to the defaults so the file
// says it: a replica whose request fails to connect or times out is set
// aside for ten seconds and the request is passed to the next one — nginx
// passes only an idempotent request, so a POST that reached a dying replica
// is answered with its error rather than carried out twice. A replica gets
// two seconds to accept a connection, as on Caddy, not nginx's sixty: the
// kernel completes the handshake whatever the application is doing, so a
// replica that takes longer is not there, and the visitor's request is
// better passed on than held. No keepalive, as before: each request is a
// fresh upstream connection.
func deploymentPool(servers []string, always bool) *SitePool {
	if len(servers) == 0 || (len(servers) < 2 && !always) {
		return nil
	}
	pool := &SitePool{Method: "least_conn", ConnectTimeout: 2, Servers: make([]PoolServer, 0, len(servers))}
	for _, server := range servers {
		pool.Servers = append(pool.Servers, PoolServer{
			Address: strings.TrimPrefix(server, "http://"), MaxFails: 1, FailTimeout: 10,
		})
	}
	return pool
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
	before, beforeExisted := readIfPresent(path)
	// Forgotten before the first write rather than after the last, so a
	// restore that fails half-way still leaves no stale dump behind.
	s.forgetEffective()
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
	// Recorded like any other write, or a history would go on showing a
	// route holding what was just rolled back. A restore has no undo, so the
	// record is made once the files are in place rather than after the test.
	if beforeExisted != snapshot.Existed || before != snapshot.Content {
		change := Change{Path: path, Action: ChangeWrite,
			Before: []byte(before), BeforeExisted: beforeExisted, After: []byte(snapshot.Content)}
		if !snapshot.Existed {
			change.Action, change.After = ChangeDelete, nil
		}
		s.recordChange(ctx, change)
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
