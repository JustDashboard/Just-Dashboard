package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// The CrowdSec tie-in: a deployment route with the switch on refuses the
// addresses CrowdSec's local engine has banned, at the route, whether or not
// a firewall bouncer is installed. The list is one file per proxy that every
// such route takes in — a geo include on nginx, an import on the Docker
// Caddy ingress — rewritten from the engine's decisions on a cadence and
// reloaded only when it changed and something reads it.

// MaxCrowdSecEntries caps the list. A host under a scan collects thousands
// of community decisions; past the cap the newest are kept, which is the
// order the engine lists them in, and the refresh says how many it left out.
const MaxCrowdSecEntries = 10000

// dockerCaddyCrowdSecFile is the list on the Docker Caddy ingress: beside
// the routes rather than among them, so the routes' import glob does not
// read it as a site.
const dockerCaddyCrowdSecFile = dockerCaddyRoot + "/shared/crowdsec.caddy"

// crowdSecMu serialises refreshes with route applies, so a route that turns
// the switch on never reads a list another write is half way through.
var crowdSecMu sync.Mutex

// CrowdSecRefresh is what one refresh did.
type CrowdSecRefresh struct {
	Entries  int  `json:"entries"`
	Omitted  int  `json:"omitted"`
	Changed  bool `json:"changed"`
	Reloaded bool `json:"reloaded"`
	// Proxy is nginx or docker-caddy, whichever serves the routes.
	Proxy string `json:"proxy"`
}

// SetTrustedProxies is the dashboard's own JD_TRUSTED_PROXIES: the proxies
// whose X-Forwarded-For a route's limits and rules believe.
func (s *Service) SetTrustedProxies(cidrs []string) {
	trusted := make([]string, 0, len(cidrs))
	for _, entry := range cidrs {
		if normalized, err := NormalizeAccessEntry(entry); err == nil && !slices.Contains(trusted, normalized) {
			trusted = append(trusted, normalized)
		}
	}
	s.trustedMu.Lock()
	s.trustedProxies = trusted
	s.trustedMu.Unlock()
}

// TrustedProxies is what SetTrustedProxies kept.
func (s *Service) TrustedProxies() []string {
	s.trustedMu.RLock()
	defer s.trustedMu.RUnlock()
	return slices.Clone(s.trustedProxies)
}

func (s *Service) nginxCrowdSecList() string {
	return filepath.Join(s.nginxDir, "jd-edge", "crowdsec.conf")
}

// CrowdSecBan is one of the engine's decisions as the list reads it.
type CrowdSecBan struct {
	// Scope is Ip, Range, Country or As; Value is what it names; Type is
	// ban or captcha.
	Scope string
	Value string
	Type  string
}

// CrowdSecBanned turns the engine's decisions into the list the routes read:
// the bans on an address or a range, each once in its canonical spelling,
// in the order given (the engine lists the newest first), capped. A captcha
// is not a ban, and a country or AS scope is not something either proxy can
// match without a database it does not have.
func CrowdSecBanned(decisions []CrowdSecBan) (banned []string, omitted int) {
	seen := map[string]bool{}
	for _, decision := range decisions {
		if !strings.EqualFold(decision.Type, "ban") {
			continue
		}
		scope := strings.ToLower(decision.Scope)
		if scope != "ip" && scope != "range" {
			continue
		}
		entry, err := NormalizeAccessEntry(decision.Value)
		if err != nil {
			prefix, prefixErr := netip.ParsePrefix(strings.TrimSpace(decision.Value))
			if prefixErr != nil {
				continue
			}
			entry = prefix.Masked().String()
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		if len(banned) >= MaxCrowdSecEntries {
			omitted++
			continue
		}
		banned = append(banned, entry)
	}
	return banned, omitted
}

// RefreshCrowdSecList writes the banned addresses where the serving proxy's
// routes read them and reloads it when the list changed and a route takes
// it in. A list nobody reads is still kept current, so a route that turns
// the switch on starts from today's bans.
func (s *Service) RefreshCrowdSecList(ctx context.Context, banned []string) (CrowdSecRefresh, error) {
	if len(banned) > MaxCrowdSecEntries {
		banned = banned[:MaxCrowdSecEntries]
	}
	for _, entry := range banned {
		if normalized, err := NormalizeAccessEntry(entry); err != nil || normalized != entry {
			return CrowdSecRefresh{}, fmt.Errorf("%q is not a canonical address or range", entry)
		}
	}
	crowdSecMu.Lock()
	defer crowdSecMu.Unlock()
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return CrowdSecRefresh{Entries: len(banned)}, err
	}
	if edge != nil {
		return s.refreshDockerCaddyCrowdSec(ctx, edge, banned)
	}
	return s.refreshNginxCrowdSec(ctx, banned)
}

func (s *Service) refreshDockerCaddyCrowdSec(ctx context.Context, edge *dockerCaddy, banned []string) (CrowdSecRefresh, error) {
	result := CrowdSecRefresh{Entries: len(banned), Proxy: accessDriverCaddy}
	content := renderCaddyCrowdSecFile(banned)
	if current, found, err := edge.read(ctx, dockerCaddyCrowdSecFile); err == nil && found && current == content {
		return result, nil
	}
	if err := edge.write(ctx, dockerCaddyCrowdSecFile, content); err != nil {
		return result, err
	}
	result.Changed = true
	if !s.dockerCaddyReadsCrowdSec(ctx, edge) {
		return result, nil
	}
	if err := edge.reload(ctx); err != nil {
		return result, fmt.Errorf("the ingress did not take the refreshed list: %w", err)
	}
	result.Reloaded = true
	return result, nil
}

func (s *Service) refreshNginxCrowdSec(ctx context.Context, banned []string) (CrowdSecRefresh, error) {
	result := CrowdSecRefresh{Entries: len(banned), Proxy: accessDriverNginx}
	path := s.nginxCrowdSecList()
	content := renderNginxCrowdSecFile(banned)
	if current, err := os.ReadFile(path); err == nil && string(current) == content {
		return result, nil
	}
	if err := writeFileAtomic(path, content); err != nil {
		return result, err
	}
	result.Changed = true
	if !s.nginxReadsCrowdSec() {
		return result, nil
	}
	if _, err := s.Reload(ctx, KindNginx); err != nil {
		return result, fmt.Errorf("nginx did not take the refreshed list: %w", err)
	}
	result.Reloaded = true
	return result, nil
}

// ensureNginxCrowdSecList writes an empty list where none is yet, so the
// first route that includes it loads before the first refresh has run.
func (s *Service) ensureNginxCrowdSecList() error {
	path := s.nginxCrowdSecList()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeFileAtomic(path, renderNginxCrowdSecFile(nil))
}

// ensureDockerCaddyCrowdSecList is the same for the ingress.
func (s *Service) ensureDockerCaddyCrowdSecList(ctx context.Context, edge *dockerCaddy) error {
	if _, found, err := edge.read(ctx, dockerCaddyCrowdSecFile); err == nil && found {
		return nil
	}
	return edge.write(ctx, dockerCaddyCrowdSecFile, renderCaddyCrowdSecFile(nil))
}

// nginxReadsCrowdSec says whether an enabled site includes the list.
func (s *Service) nginxReadsCrowdSec() bool {
	marker := "include " + s.nginxCrowdSecList() + ";"
	for _, dir := range []string{"sites-enabled", "conf.d"} {
		entries, err := os.ReadDir(filepath.Join(s.nginxDir, dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			content, err := os.ReadFile(filepath.Join(s.nginxDir, dir, entry.Name()))
			if err == nil && strings.Contains(string(content), marker) {
				return true
			}
		}
	}
	return false
}

// dockerCaddyReadsCrowdSec says whether a route of the ingress imports it.
func (s *Service) dockerCaddyReadsCrowdSec(ctx context.Context, edge *dockerCaddy) bool {
	out, err := edge.command(ctx, "", "grep", "-rlF", "import "+dockerCaddyCrowdSecFile, dockerCaddyRoot+"/routes")
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// writeFileAtomic replaces a file whole, through a temporary file beside it,
// so a reader never sees half a list.
func writeFileAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
