package proxysvc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The certificate copies a Docker Caddy route serves, and keeping them current.
//
// A route that serves a certificate certbot or an import holds — a wildcard
// proved over DNS-01, a name under one, a private import — carries an
// immutable copy of it in Caddy's own volume, <root>/certs/<sha256>.crt and
// .key, named after the certificate's bytes (dockerCaddyCertificatePaths),
// and a `tls` line pointing at the pair. Caddy serves that copy and nothing
// else, so certbot renewing the lineage on the host changed nothing Caddy
// read: the route served the old certificate until the next deploy, and past
// its expiry if none came.
//
// So every such route records where its copy came from, as a metadata line
// beside the target's, and the copy is refreshed from there. A renewed
// certificate is copied under its own hash, the route's `tls` lines are
// rewritten to it and Caddy reloaded, which keeps the listener and finishes
// the requests in flight; the copy under the old hash stays until no route
// names it and an activation could no longer be holding a snapshot that does,
// then it is removed. Copies stay content-addressed rather than being
// replaced in place for two reasons: a snapshot taken before the renewal
// restores exactly the bytes it names, and a changed path is a changed
// configuration, which `caddy reload` applies — a reload of an unchanged
// Caddyfile is a no-op that would have left the old certificate loaded.
//
// The dashboard's own renewal and issuance jobs refresh at once
// (RefreshIngressCertificates). The lifecycle worker's pass refreshes what
// certbot's timer renewed within a tick, so no hook has to reach back into
// the dashboard, and it records the source for a route written before this
// existed — found by hashing the host's lineages and their archives — so an
// upgrade turns refreshing on for every existing route.

// dockerCaddyCertsRoot holds the copies, inside the ingress's persistent
// /config volume beside the routes that name them.
const dockerCaddyCertsRoot = dockerCaddyRoot + "/certs"

// dockerCaddyCertificatePrefix carries the host paths a route's copy was
// taken from, as the target line carries the application's container.
const dockerCaddyCertificatePrefix = "# Just Dashboard certificate: "

// certificateCopyGrace is how long a copy no route names is kept. An
// activation that fails after writing its route restores the route it
// replaced, copy and all, and the step's checks take minutes, not hours; the
// lifecycle worker prunes between passes, not under the activation's lock.
var certificateCopyGrace = time.Hour

// dockerCaddyCertificateSource is where a route's copy came from: the
// certificate and key paths activation resolved, on the host.
type dockerCaddyCertificateSource struct {
	CertPath string `json:"certPath"`
	KeyPath  string `json:"keyPath"`
}

func (source dockerCaddyCertificateSource) metadata() string {
	raw, _ := json.Marshal(source)
	return dockerCaddyCertificatePrefix + string(raw) + "\n"
}

// servesExplicitCertificate reports a route Caddy serves from a copy rather
// than from a certificate it obtained itself: an HTTPS route resolved to a
// pair that is not one of Caddy's own release copies (caddy-<hash>, which
// the route never reads because Caddy holds the certificate).
func servesExplicitCertificate(route DeploymentRoute) bool {
	return route.TLS && route.CertPath != "" && !isCaddyEvidence(filepath.Base(filepath.Dir(route.CertPath)))
}

// certificateSourceFor is the source a route records, when it serves a copy.
func certificateSourceFor(route DeploymentRoute) (dockerCaddyCertificateSource, bool) {
	if !servesExplicitCertificate(route) {
		return dockerCaddyCertificateSource{}, false
	}
	return dockerCaddyCertificateSource{CertPath: route.CertPath, KeyPath: route.KeyPath}, true
}

// parseDockerCaddyCertificate reads the source a managed route recorded.
func parseDockerCaddyCertificate(content string) (dockerCaddyCertificateSource, bool) {
	if !strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		return dockerCaddyCertificateSource{}, false
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, dockerCaddyCertificatePrefix) {
			continue
		}
		var source dockerCaddyCertificateSource
		if json.Unmarshal([]byte(strings.TrimPrefix(line, dockerCaddyCertificatePrefix)), &source) != nil ||
			source.CertPath == "" || source.KeyPath == "" {
			return source, false
		}
		return source, true
	}
	return dockerCaddyCertificateSource{}, false
}

// certificateCopyHash names a copy: the hash of the certificate's bytes.
func certificateCopyHash(cert []byte) string {
	return strings.TrimPrefix(routeDigest(string(cert)), "sha256:")
}

// certificateCopyPaths is where the copy under a hash lives.
func certificateCopyPaths(hash string) (string, string) {
	prefix := dockerCaddyCertsRoot + "/" + hash
	return prefix + ".crt", prefix + ".key"
}

// tlsCopyLine is the directive a route serves a copy with, as the renderer
// writes it.
func tlsCopyLine(hash string) string {
	certPath, keyPath := certificateCopyPaths(hash)
	return "  tls " + strconv.Quote(certPath) + " " + strconv.Quote(keyPath)
}

// dockerCaddyTLSCopyRe is that line, with the hash captured, in a route or
// in the redirecting names' blocks that share its certificate handling.
var dockerCaddyTLSCopyRe = regexp.MustCompile(`(?m)^  tls "` + regexp.QuoteMeta(dockerCaddyCertsRoot) +
	`/([0-9a-f]{64})\.crt" "` + regexp.QuoteMeta(dockerCaddyCertsRoot) + `/([0-9a-f]{64})\.key"$`)

// routeCertificateCopy is the copy a managed route's tls lines name, by its
// hash, or false for a route that serves no copy.
func routeCertificateCopy(content string) (string, bool) {
	match := dockerCaddyTLSCopyRe.FindStringSubmatch(content)
	if match == nil || match[1] != match[2] {
		return "", false
	}
	return match[1], true
}

// withCertificateCopy points every tls line of a managed route — the route's
// and its redirecting names' — at the copy under hash, and changes nothing
// else: the target metadata the next pass parses, the credentials, the rules.
func withCertificateCopy(content, hash string) string {
	return dockerCaddyTLSCopyRe.ReplaceAllLiteralString(content, tlsCopyLine(hash))
}

// withCertificateSource records where a route's copy came from, for a route
// written before refreshing existed, after the other metadata lines, where
// routeContent puts it. It answers false for a route that already says, and
// refuses anything not written by this dashboard.
func withCertificateSource(content string, source dockerCaddyCertificateSource) (string, bool, error) {
	if !strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		return content, false, errors.New("not a managed Caddy route")
	}
	if _, ok := parseDockerCaddyCertificate(content); ok {
		return content, false, nil
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + source.metadata(), true, nil
}

// trustedCertificateSource is a source the refresh will read from: certbot's
// live directory or the dashboard's import directory, the two places a
// deployment's certificate is resolved from. The paths come out of a file
// inside the ingress, and nothing read from there points this process at a
// file elsewhere on the host.
func trustedCertificateSource(source dockerCaddyCertificateSource) bool {
	within := func(path, dir string) bool {
		return strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(filepath.Separator))
	}
	for _, path := range []string{source.CertPath, source.KeyPath} {
		if !filepath.IsAbs(path) || (!within(path, filepath.Join(letsencryptDir, "live")) && !within(path, importedDir)) {
			return false
		}
	}
	return true
}

// certificateSourceByHash finds the lineage or import a copy was taken from,
// for a route written before its source was recorded: certbot's live
// certificates, then their archived generations (a copy taken before a
// renewal matches one of those), then the imports.
func certificateSourceByHash(hash string) (dockerCaddyCertificateSource, bool) {
	hashOf := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return certificateCopyHash(raw)
	}
	live := filepath.Join(letsencryptDir, "live")
	if entries, err := os.ReadDir(live); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			source := dockerCaddyCertificateSource{
				CertPath: filepath.Join(live, entry.Name(), "fullchain.pem"),
				KeyPath:  filepath.Join(live, entry.Name(), "privkey.pem"),
			}
			if hashOf(source.CertPath) == hash {
				return source, true
			}
			archived, _ := filepath.Glob(filepath.Join(letsencryptDir, "archive", entry.Name(), "fullchain*.pem"))
			for _, generation := range archived {
				if hashOf(generation) == hash {
					return source, true
				}
			}
		}
	}
	if entries, err := os.ReadDir(importedDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || isCaddyEvidence(entry.Name()) {
				continue
			}
			source := dockerCaddyCertificateSource{
				CertPath: filepath.Join(importedDir, entry.Name(), "fullchain.pem"),
				KeyPath:  filepath.Join(importedDir, entry.Name(), "privkey.pem"),
			}
			if hashOf(source.CertPath) == hash {
				return source, true
			}
		}
	}
	return dockerCaddyCertificateSource{}, false
}

// certificateRefresh is what one route's certificate needs: its source
// recorded, its copy replaced, or nothing.
type certificateRefresh struct {
	// served names the copy the route serves now, and hash the one it
	// serves once refreshed — the same when nothing was renewed. The prune
	// keeps whichever a route ends the pass with.
	served, hash string
	// source is what the route records, added when it did not.
	source       dockerCaddyCertificateSource
	recordSource bool
	// cert and key are the renewed pair to copy under hash, when the copy
	// the route serves is not the certificate the host holds any more.
	cert, key []byte
}

func (r certificateRefresh) changed() bool { return r.recordSource || r.cert != nil }

// apply rewrites a route's content for the refresh: on the content as it is
// now, so the repairs a pass makes compose.
func (r certificateRefresh) apply(content string) string {
	if r.recordSource {
		content, _, _ = withCertificateSource(content, r.source)
	}
	if r.cert != nil {
		content = withCertificateCopy(content, r.hash)
	}
	return content
}

// planCertificateRefresh reads what a route serves against what the host
// holds. A route that serves no copy, or whose source cannot be found or
// read — a lineage deleted since, a renewal half-written — plans nothing and
// keeps serving what it has; the next pass asks again.
func planCertificateRefresh(content string) (certificateRefresh, bool) {
	hash, ok := routeCertificateCopy(content)
	if !ok {
		return certificateRefresh{}, false
	}
	plan := certificateRefresh{served: hash, hash: hash}
	source, known := parseDockerCaddyCertificate(content)
	if !known {
		if source, known = certificateSourceByHash(hash); !known {
			return plan, true
		}
		plan.source, plan.recordSource = source, true
	}
	if !trustedCertificateSource(source) {
		return certificateRefresh{served: hash, hash: hash}, true
	}
	cert, err := os.ReadFile(source.CertPath)
	if err != nil {
		return plan, true
	}
	current := certificateCopyHash(cert)
	if current == hash {
		return plan, true
	}
	key, err := os.ReadFile(source.KeyPath)
	if err != nil {
		return plan, true
	}
	leaf, _, err := parseCertChain(string(cert))
	if err != nil || keyMatchesCertificate(string(key), leaf) != nil {
		return plan, true
	}
	plan.hash, plan.cert, plan.key = current, cert, key
	return plan, true
}

// writeCertificateCopy puts the renewed pair where the route is about to
// name it, as activation writes a route's copy.
func (c *dockerCaddy) writeCertificateCopy(ctx context.Context, plan certificateRefresh) error {
	if plan.cert == nil {
		return nil
	}
	certPath, keyPath := certificateCopyPaths(plan.hash)
	if err := c.write(ctx, certPath, string(plan.cert)); err != nil {
		return err
	}
	return c.write(ctx, keyPath, string(plan.key))
}

// pruneCertificateCopies removes the copies no route names any more, once
// they are older than the grace: what a renewal or a deploy left behind.
func (c *dockerCaddy) pruneCertificateCopies(ctx context.Context, referenced map[string]bool) error {
	script := `if [ -d "$1" ]; then find "$1" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.key' \) -mmin +"$2"; fi`
	minutes := strconv.Itoa(int(certificateCopyGrace / time.Minute))
	if certificateCopyGrace <= 0 {
		script = `if [ -d "$1" ]; then find "$1" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.key' \); fi`
	}
	raw, err := c.command(ctx, "", "sh", "-c", script, "sh", dockerCaddyCertsRoot, minutes)
	if err != nil {
		return err
	}
	stale := staleCertificateCopies(strings.Fields(string(raw)), referenced)
	if len(stale) == 0 {
		return nil
	}
	_, err = c.command(ctx, "", append([]string{"rm", "-f"}, stale...)...)
	return err
}

// staleCertificateCopies picks, from the copies old enough to prune, the
// ones no route names: a file under the copies' directory, named by a hash
// and .crt or .key, whose hash is not in use. Anything else there is left.
func staleCertificateCopies(listed []string, referenced map[string]bool) []string {
	var stale []string
	for _, path := range listed {
		if filepath.Dir(path) != dockerCaddyCertsRoot {
			continue
		}
		name := filepath.Base(path)
		hash := strings.TrimSuffix(strings.TrimSuffix(name, ".crt"), ".key")
		if len(hash) != 64 || hash == name || referenced[hash] {
			continue
		}
		stale = append(stale, path)
	}
	return stale
}

// ErrIngressUnavailable is a Docker ingress that could not be inspected —
// the daemon not answering, the named container not running — which says
// nothing about what its routes serve: the lifecycle pass refreshes them
// once it can be.
var ErrIngressUnavailable = errors.New("the Docker ingress could not be inspected")

// RefreshIngressCertificates refreshes, on the Docker Caddy ingress, every
// route whose copy is no longer the certificate the host holds — what a
// renewal job calls once certbot has renewed — and answers with the routes
// it refreshed. A host without the ingress refreshes nothing; one whose
// ingress cannot be inspected answers ErrIngressUnavailable.
func (s *Service) RefreshIngressCertificates(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, errors.Join(ErrIngressUnavailable, err)
	}
	if edge == nil {
		return nil, nil
	}
	return edge.reconcile(ctx, nil)
}

// RefreshedFor says what a refresh after a renewal did for the Caddy routes
// serving it.
func RefreshedFor(routes []string) string {
	verb := "serve"
	if len(routes) == 1 {
		verb = "serves"
	}
	return "Refreshed the Caddy ingress's copy of the certificate, so " + describeSites(routes) + " " + verb + " the new one."
}

// NotRefreshedFor says why the Caddy routes serving a renewed certificate
// may still serve the previous one.
func NotRefreshedFor(err error) error {
	return errors.New("the certificate was renewed, but the Caddy ingress's copies could not be refreshed: " + err.Error() +
		". A route serving a copy keeps the previous certificate until the ingress lifecycle pass, which runs every 30 seconds, refreshes it")
}
