package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// A site's request record, read where the site says it writes one.
//
// A deployment's route is addressed by a name this dashboard chose, so its
// record is at a path the dashboard wrote. A site is not: a hand-written file
// logs wherever its author put the directive, and a guess at the managed
// spelling read an empty file and reported a site nobody visits. So the path
// comes from the site's own file, and — because that file is something an
// operator can edit to point anywhere — it is held to the log roots the logs
// page is, generations included, before a byte of it is read.

var (
	// ErrSiteNotFound is a site this host has no file or route for.
	ErrSiteNotFound = errors.New("no such site")
	// ErrNoSiteAccessLog is a site that logs no requests of its own: off,
	// to syslog, or — with no directive — to nginx's shared log, whose
	// combined lines do not say which site answered them.
	ErrNoSiteAccessLog = errors.New("this site writes no access log of its own")
	// ErrOutsideLogRoots is a site whose access log is somewhere the logs
	// page may not read either.
	ErrOutsideLogRoots = errors.New("the site's access log is outside the log roots")
)

// SiteConfig reads a site's file by its name, in either layout: the listing
// on a conf.d host reports a name that already ends in .conf, and a host set
// up by hand may have a file without it. It reads through the config
// editor's own allowlist, so a name that turns out to be a path is refused
// there rather than joined here.
func (s *Service) SiteConfig(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", ErrSiteNotFound
	}
	for _, candidate := range []string{
		filepath.Join(s.nginxDir, "sites-available", name),
		filepath.Join(s.nginxDir, "conf.d", name),
		filepath.Join(s.nginxDir, "conf.d", name+".conf"),
	} {
		if content, err := s.ReadConfig(candidate); err == nil {
			return content, nil
		}
	}
	return "", ErrSiteNotFound
}

// SiteAccessLogReader resolves a site to its request record. An nginx site
// is read from the file its access_log names, refused unless allow accepts
// it; a route on the shared Docker Caddy ingress, which has no file on the
// host, is read the way the deployment it belongs to is.
func (s *Service) SiteAccessLogReader(ctx context.Context, name string, allow func(string) error) (accesslog.Reader, accesslog.Facts, error) {
	content, err := s.SiteConfig(name)
	if err != nil {
		if _, routeErr := dockerCaddyRoutePath(name); routeErr == nil {
			if edge, _ := s.dockerCaddy(ctx); edge != nil {
				return s.AccessLogReader(ctx, name)
			}
		}
		return nil, accesslog.Facts{}, err
	}
	spec, _ := ParseSiteSpec(name, content)
	if spec.AccessLogPath == "" {
		return nil, accesslog.Facts{}, ErrNoSiteAccessLog
	}
	if err := allow(spec.AccessLogPath); err != nil {
		return nil, accesslog.Facts{}, fmt.Errorf("%w: %v", ErrOutsideLogRoots, err)
	}
	facts := accesslog.Facts{Driver: accessDriverNginx, Format: accesslog.FormatCombined, Latency: false, Path: spec.AccessLogPath}
	return &fileAccessLog{path: spec.AccessLogPath, allow: allow}, facts, nil
}
