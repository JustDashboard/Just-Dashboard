package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A deployment's maintenance is a switch beside its route rather than part
// of it. The route is the release's: it is written only when a release goes
// live, under the snapshot, verify and restore a cutover runs, and rewriting
// it from anywhere else could put an old release's upstream back in the
// middle of a cutover. So every route carries the check, written once with
// the release, and the check looks on every request for one file in the
// route's pages folder — maintenance.on. Turning maintenance on writes that
// file and turning it off removes it: nothing is reloaded, the route's bytes
// do not change, and a cutover that runs at the same moment neither undoes
// the switch nor is undone by it. Who is let past and what the 503 says are
// the route's, so they change with the next release.

// DeploymentPages are the pages a deployment's route can serve, by the name
// its file carries.
var DeploymentPages = []string{"maintenance", "404", "502", "503", "504", "waking"}

// ErrMaintenanceUnready refuses to turn on maintenance for a route that has
// no check to read the switch: none is live, or it was written before the
// switch existed.
var ErrMaintenanceUnready = errors.New("the deployment's live route has no maintenance switch")

// MaintenanceSwitch is where a deployment's maintenance stands on this host.
type MaintenanceSwitch struct {
	// Driver is the proxy the route is on: "nginx" or "caddy".
	Driver string `json:"driver"`
	// Route says the deployment has a route on that proxy at all.
	Route bool `json:"route"`
	// Ready says the route carries the check, so the switch acts at once.
	// A route written by a release from before the switch existed does
	// not, until the next release writes it again.
	Ready bool `json:"ready"`
	// On says the switch is on: visitors get the maintenance page, unless
	// the route is not Ready, in which case nothing reads it yet.
	On bool `json:"on"`
	// Since is when it was turned on.
	Since *time.Time `json:"since,omitempty"`
}

// deploymentPageNames are the pages a route serves: the maintenance page,
// which every route has, and its error pages.
func deploymentPageNames(route DeploymentRoute) []string {
	pages := []string{"maintenance"}
	for _, code := range slices.Sorted(slices.Values(route.ErrorPages)) {
		pages = append(pages, strconv.Itoa(code))
	}
	if route.Sleep != nil {
		pages = append(pages, "waking")
	}
	return pages
}

// DeploymentMaintenance reads a deployment's switch from the proxy its route
// is on.
func (s *Service) DeploymentMaintenance(ctx context.Context, name string) (MaintenanceSwitch, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return MaintenanceSwitch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge != nil {
		return edge.maintenance(ctx, name)
	}
	return s.nginxMaintenanceLocked(name)
}

// SetDeploymentMaintenance turns a deployment's maintenance on or off. On is
// refused for a route that would not read it; off is always allowed, so a
// switch left behind can be cleared whatever the route is.
func (s *Service) SetDeploymentMaintenance(ctx context.Context, name string, on bool) (MaintenanceSwitch, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return MaintenanceSwitch{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge != nil {
		return edge.setMaintenance(ctx, name, on)
	}
	return s.setNginxMaintenanceLocked(name, on)
}

// ReadDeploymentPage reads one of a deployment's pages, or the page the
// dashboard ships when it has none of its own.
func (s *Service) ReadDeploymentPage(ctx context.Context, name, page string) (*SitePage, error) {
	if !slices.Contains(DeploymentPages, page) {
		return nil, fmt.Errorf("there is no page called %s", page)
	}
	fallback, err := ShippedSitePage(page)
	if err != nil {
		return nil, err
	}
	out := &SitePage{Site: name, Page: page, Content: fallback}
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge != nil {
		dir, err := dockerCaddyPagesDir(name)
		if err != nil {
			return nil, err
		}
		content, exists, err := edge.read(ctx, dir+"/"+pageFileName(page))
		if err != nil {
			return nil, err
		}
		if exists {
			out.Content, out.Custom = content, true
		}
		return out, nil
	}
	full, err := s.pageFile(name, page, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(full); err == nil {
		out.Content, out.Custom = string(b), true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// WriteDeploymentPage saves one of a deployment's pages. Either proxy reads
// the file on every answer that uses it, so it is served from the next one.
func (s *Service) WriteDeploymentPage(ctx context.Context, name, page, content string) (*SitePage, error) {
	if !slices.Contains(DeploymentPages, page) {
		return nil, fmt.Errorf("there is no page called %s", page)
	}
	if len(content) > MaxPageSize {
		return nil, fmt.Errorf("a page may be at most %d KiB", MaxPageSize>>10)
	}
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if edge != nil {
		dir, err := dockerCaddyPagesDir(name)
		if err != nil {
			return nil, err
		}
		if err := edge.write(ctx, dir+"/"+pageFileName(page), content); err != nil {
			return nil, err
		}
		return &SitePage{Site: name, Page: page, Content: content, Custom: true}, nil
	}
	full, err := s.pageFile(name, page, true)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(full, content); err != nil {
		return nil, err
	}
	// Readable by nginx's workers whatever mode a file there had before.
	if err := os.Chmod(full, 0o644); err != nil {
		return nil, err
	}
	return &SitePage{Site: name, Page: page, Content: content, Custom: true}, nil
}

// nginxMaintenanceLocked reads the switch of a route on this host's nginx.
// The caller holds s.mu.
func (s *Service) nginxMaintenanceLocked(name string) (MaintenanceSwitch, error) {
	out := MaintenanceSwitch{Driver: "nginx"}
	if !siteNameRe.MatchString(name) {
		return out, errors.New("invalid deployment route name")
	}
	path, _, err := s.deploymentRoutePaths(name)
	if err != nil {
		return out, err
	}
	switchPath := filepath.Join(s.SitePagesDir(name), maintenanceSwitchFile)
	if content, exists := readIfPresent(path); exists {
		out.Route = true
		out.Ready = strings.Contains(content, "if (-f "+switchPath+") {")
	}
	if info, err := os.Stat(switchPath); err == nil {
		out.On = true
		since := switchSince(readSwitch(switchPath), info.ModTime())
		out.Since = &since
	}
	return out, nil
}

func (s *Service) setNginxMaintenanceLocked(name string, on bool) (MaintenanceSwitch, error) {
	current, err := s.nginxMaintenanceLocked(name)
	if err != nil {
		return current, err
	}
	switchPath := filepath.Join(s.SitePagesDir(name), maintenanceSwitchFile)
	if !on {
		// The folder is checked as a page's is, so a link put in its place
		// never has a file removed wherever it points.
		if _, err := s.pageFile(name, "maintenance", false); errors.Is(err, os.ErrNotExist) {
			return s.nginxMaintenanceLocked(name)
		} else if err != nil {
			return current, err
		}
		if err := os.Remove(switchPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return current, err
		}
		return s.nginxMaintenanceLocked(name)
	}
	if !current.Ready {
		return current, ErrMaintenanceUnready
	}
	if current.On {
		return current, nil
	}
	// The page first, so the switch never sends anyone to a page that is not
	// there; the release that wrote the route wrote it too, unless it went.
	full, err := s.pageFile(name, "maintenance", true)
	if err != nil {
		return current, err
	}
	if _, err := os.Lstat(full); errors.Is(err, os.ErrNotExist) {
		page, _ := ShippedSitePage("maintenance")
		if err := writeAtomic(full, page); err != nil {
			return current, err
		}
		if err := os.Chmod(full, 0o644); err != nil {
			return current, err
		}
	}
	if err := writeAtomic(switchPath, time.Now().UTC().Format(time.RFC3339)+"\n"); err != nil {
		return current, err
	}
	// nginx's workers only need to see it, but a mode a file there had
	// before is no reason for them not to.
	if err := os.Chmod(switchPath, 0o644); err != nil {
		return current, err
	}
	return s.nginxMaintenanceLocked(name)
}

// removeDeploymentPagesLocked removes a route's pages and its switch. The
// caller holds s.mu.
func (s *Service) removeDeploymentPagesLocked(name string) error {
	if !siteNameRe.MatchString(name) {
		return errors.New("invalid deployment route name")
	}
	dir := s.SitePagesDir(name)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		// A route from before the pages existed, or one that never served.
		return nil
	} else if err != nil {
		return err
	}
	// The folder the pages are in has to be the proxy's own; RemoveAll
	// removes a link in the route's place, never what it points at.
	if _, err := s.allowedPath(s.pagesRoot()); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// readSwitch is what a switch file says, which is when it was turned on.
func readSwitch(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// switchSince reads the time a switch file holds, or its fallback for one
// that holds none: a file written by hand still turns maintenance on.
func switchSince(content string, fallback time.Time) time.Time {
	if at, err := time.Parse(time.RFC3339, strings.TrimSpace(content)); err == nil {
		return at.UTC()
	}
	return fallback.UTC()
}

// maintenance reads a route's switch inside the Docker Caddy ingress.
func (c *dockerCaddy) maintenance(ctx context.Context, name string) (MaintenanceSwitch, error) {
	out := MaintenanceSwitch{Driver: "caddy"}
	path, err := dockerCaddyRoutePath(name)
	if err != nil {
		return out, err
	}
	dir, err := dockerCaddyPagesDir(name)
	if err != nil {
		return out, err
	}
	content, exists, err := c.read(ctx, path)
	if err != nil {
		return out, err
	}
	if exists && strings.HasPrefix(content, "# Managed by Just Dashboard\n") {
		out.Route = true
		out.Ready = strings.Contains(content, " root "+dir+"\n") && strings.Contains(content, " try_files /"+maintenanceSwitchFile+"\n")
	}
	flag, on, err := c.read(ctx, dir+"/"+maintenanceSwitchFile)
	if err != nil {
		return out, err
	}
	if on {
		out.On = true
		since := switchSince(flag, time.Now())
		out.Since = &since
	}
	return out, nil
}

func (c *dockerCaddy) setMaintenance(ctx context.Context, name string, on bool) (MaintenanceSwitch, error) {
	current, err := c.maintenance(ctx, name)
	if err != nil {
		return current, err
	}
	dir, err := dockerCaddyPagesDir(name)
	if err != nil {
		return current, err
	}
	if !on {
		if _, err := c.command(ctx, "", "rm", "-f", dir+"/"+maintenanceSwitchFile); err != nil {
			return current, err
		}
		return c.maintenance(ctx, name)
	}
	if !current.Ready {
		return current, ErrMaintenanceUnready
	}
	if current.On {
		return current, nil
	}
	if err := c.ensurePages(ctx, DeploymentRoute{Name: name}); err != nil {
		return current, err
	}
	if err := c.write(ctx, dir+"/"+maintenanceSwitchFile, time.Now().UTC().Format(time.RFC3339)+"\n"); err != nil {
		return current, err
	}
	return c.maintenance(ctx, name)
}

// ensurePages gives a route the page the dashboard ships for each page it
// serves and has no file for, so Caddy never answers with a page that is
// not there. A page the operator wrote is kept.
func (c *dockerCaddy) ensurePages(ctx context.Context, route DeploymentRoute) error {
	dir, err := dockerCaddyPagesDir(route.Name)
	if err != nil {
		return err
	}
	for _, page := range deploymentPageNames(route) {
		path := dir + "/" + pageFileName(page)
		evidence, err := c.command(ctx, "", "sh", "-c", `if [ -e "$1" ]; then printf present; else printf absent; fi`, "sh", path)
		if err != nil {
			return err
		}
		if string(evidence) == "present" {
			continue
		}
		content, err := ShippedSitePage(page)
		if err != nil {
			return err
		}
		if err := c.write(ctx, path, content); err != nil {
			return err
		}
	}
	return nil
}

// removePages removes a route's pages and its switch from the ingress.
func (c *dockerCaddy) removePages(ctx context.Context, name string) error {
	dir, err := dockerCaddyPagesDir(name)
	if err != nil {
		return err
	}
	_, err = c.command(ctx, "", "rm", "-rf", dir)
	return err
}
